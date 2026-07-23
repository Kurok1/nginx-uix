/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestXCryptoACMEClientCompletesLocalIssuanceRenewalAndDeactivation(t *testing.T) {
	service := newLocalACMETestService(t)
	defer service.Close()

	factory := newXCryptoACMEFactory(service.Client(), service.DirectoryURL())
	accountKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	accountClient, err := factory.NewAccountClient(service.DirectoryURL(), accountKey, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	directory, err := accountClient.Discover(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if directory.URL != service.DirectoryURL() || directory.TermsURL != service.TermsURL() ||
		directory.ExternalAccountRequired {
		t.Fatalf("directory = %#v", directory)
	}
	account, err := accountClient.Register(ctx, "operator@example.test", directory.TermsURL)
	if err != nil {
		t.Fatal(err)
	}
	if account.Status != AccountStatusValid || account.URI != service.AccountURL() {
		t.Fatalf("registered account = %#v", account)
	}
	existing, err := accountClient.GetRegistration(ctx, account.URI)
	if err != nil {
		t.Fatal(err)
	}
	if existing != account {
		t.Fatalf("existing account = %#v, want %#v", existing, account)
	}

	orderClient, err := factory.NewOrderClient(service.DirectoryURL(), accountKey, account.URI)
	if err != nil {
		t.Fatal(err)
	}
	for _, challengeType := range []string{"http-01", "dns-01"} {
		t.Run(challengeType, func(t *testing.T) {
			chain, certificateKey := completeLocalACMEOrder(t, ctx, orderClient, challengeType)
			issued, validateErr := ValidateIssuedCertificate(
				chain,
				certificateKey,
				[]string{"service.example.test"},
				time.Now().UTC(),
			)
			if validateErr != nil {
				t.Fatal(validateErr)
			}
			if len(issued.FullChainPEM) == 0 || issued.LeafFingerprint == "" {
				t.Fatalf("issued certificate metadata = %#v", issued)
			}
		})
	}
	if service.IssuanceCount() != 2 {
		t.Fatalf("issuance count = %d, want initial issue plus renewal", service.IssuanceCount())
	}
	if err := accountClient.Deactivate(ctx); err != nil {
		t.Fatal(err)
	}
	if service.AccountStatus() != AccountStatusDeactivated {
		t.Fatalf("account status = %q", service.AccountStatus())
	}
	service.AssertClean(t)

	if _, err := factory.NewAccountClient(LetsEncryptProductionDirectory, accountKey, ""); err == nil {
		t.Fatal("test-injected factory accepted a directory outside its construction allowlist")
	}
}

func completeLocalACMEOrder(
	t *testing.T,
	ctx context.Context,
	client ACMEOrderClient,
	challengeType string,
) ([][]byte, *ecdsa.PrivateKey) {
	t.Helper()
	order, err := client.CreateOrder(ctx, []string{"service.example.test"})
	if err != nil {
		t.Fatal(err)
	}
	if len(order.AuthorizationURLs) != 1 || order.FinalizeURL == "" {
		t.Fatalf("order = %#v", order)
	}
	authorization, err := client.Authorization(ctx, order.AuthorizationURLs[0])
	if err != nil {
		t.Fatal(err)
	}
	var selected ACMEChallenge
	for _, challenge := range authorization.Challenges {
		if challenge.Type == challengeType {
			selected = challenge
			break
		}
	}
	if selected.URI == "" || selected.Token == "" {
		t.Fatalf("authorization lacks %s challenge: %#v", challengeType, authorization)
	}
	switch challengeType {
	case "http-01":
		value, responseErr := client.HTTP01Response(selected.Token)
		if responseErr != nil || !strings.HasPrefix(value, selected.Token+".") {
			t.Fatalf("HTTP01Response() = %q, %v", value, responseErr)
		}
	case "dns-01":
		value, responseErr := client.DNS01Record(selected.Token)
		if responseErr != nil || value == "" || strings.Contains(value, ".") {
			t.Fatalf("DNS01Record() = %q, %v", value, responseErr)
		}
	default:
		t.Fatalf("unsupported test challenge %q", challengeType)
	}
	if err := client.Accept(ctx, selected); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitAuthorization(ctx, authorization.URI); err != nil {
		t.Fatal(err)
	}
	if err := client.WaitOrderReady(ctx, order.URI); err != nil {
		t.Fatal(err)
	}
	certificateKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	csr, err := x509.CreateCertificateRequest(rand.Reader, &x509.CertificateRequest{
		DNSNames: []string{"service.example.test"},
	}, certificateKey)
	if err != nil {
		t.Fatal(err)
	}
	chain, err := client.Finalize(ctx, order.FinalizeURL, csr)
	if err != nil {
		t.Fatal(err)
	}
	if len(chain) != 2 {
		t.Fatalf("certificate chain length = %d, want 2", len(chain))
	}
	return chain, certificateKey
}

type localACMETestService struct {
	server *httptest.Server
	caKey  *ecdsa.PrivateKey
	caCert *x509.Certificate
	caDER  []byte

	mu            sync.Mutex
	nonce         uint64
	accountStatus AccountStatus
	accountEmail  string
	nextOrderID   int
	orders        map[int]*localACMETestOrder
	issues        int
	errors        []string
}

type localACMETestOrder struct {
	id         int
	identifier string
	accepted   bool
	issuedPEM  []byte
}

func newLocalACMETestService(t *testing.T) *localACMETestService {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "Nginx UIX local ACME test CA"},
		NotBefore:             now.Add(-time.Hour),
		NotAfter:              now.Add(24 * time.Hour),
		IsCA:                  true,
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageCertSign | x509.KeyUsageDigitalSignature,
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, key.Public(), key)
	if err != nil {
		t.Fatal(err)
	}
	parsed, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	service := &localACMETestService{
		caKey: key, caCert: parsed, caDER: der,
		accountStatus: AccountStatusValid, orders: make(map[int]*localACMETestOrder),
	}
	service.server = httptest.NewTLSServer(http.HandlerFunc(service.serveHTTP))
	return service
}

func (service *localACMETestService) Client() *http.Client { return service.server.Client() }

func (service *localACMETestService) Close() { service.server.Close() }

func (service *localACMETestService) DirectoryURL() string { return service.server.URL + "/directory" }

func (service *localACMETestService) TermsURL() string { return service.server.URL + "/terms" }

func (service *localACMETestService) AccountURL() string { return service.server.URL + "/accounts/1" }

func (service *localACMETestService) AccountStatus() AccountStatus {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.accountStatus
}

func (service *localACMETestService) IssuanceCount() int {
	service.mu.Lock()
	defer service.mu.Unlock()
	return service.issues
}

func (service *localACMETestService) AssertClean(t *testing.T) {
	t.Helper()
	service.mu.Lock()
	defer service.mu.Unlock()
	if len(service.errors) != 0 {
		t.Fatalf("local ACME service errors: %v", service.errors)
	}
}

func (service *localACMETestService) serveHTTP(writer http.ResponseWriter, request *http.Request) {
	writer.Header().Set("Replay-Nonce", service.nextNonce())
	writer.Header().Set("Cache-Control", "no-store")
	if request.URL.Path == "/directory" && request.Method == http.MethodGet {
		service.writeJSON(writer, http.StatusOK, map[string]any{
			"newNonce":   service.server.URL + "/new-nonce",
			"newAccount": service.server.URL + "/new-account",
			"newOrder":   service.server.URL + "/new-order",
			"revokeCert": service.server.URL + "/revoke-cert",
			"keyChange":  service.server.URL + "/key-change",
			"meta": map[string]any{
				"termsOfService": service.TermsURL(), "externalAccountRequired": false,
			},
		})
		return
	}
	if request.URL.Path == "/new-nonce" && (request.Method == http.MethodHead || request.Method == http.MethodGet) {
		writer.WriteHeader(http.StatusNoContent)
		return
	}
	if request.Method != http.MethodPost {
		service.fail(writer, "unexpected method %s for %s", request.Method, request.URL.Path)
		return
	}
	payload, ok := service.jwsPayload(writer, request)
	if !ok {
		return
	}
	switch {
	case request.URL.Path == "/new-account":
		service.handleAccount(writer, payload)
	case request.URL.Path == "/accounts/1":
		service.handleAccountUpdate(writer, payload)
	case request.URL.Path == "/new-order":
		service.handleNewOrder(writer, payload)
	case strings.HasPrefix(request.URL.Path, "/authz/"):
		service.handleAuthorization(writer, request.URL.Path)
	case strings.HasPrefix(request.URL.Path, "/challenge/"):
		service.handleChallenge(writer, request.URL.Path)
	case strings.HasPrefix(request.URL.Path, "/finalize/"):
		service.handleFinalize(writer, request.URL.Path, payload)
	case strings.HasPrefix(request.URL.Path, "/order/"):
		service.handleOrder(writer, request.URL.Path)
	case strings.HasPrefix(request.URL.Path, "/certificate/"):
		service.handleCertificate(writer, request.URL.Path)
	default:
		service.fail(writer, "unexpected ACME path %s", request.URL.Path)
	}
}

func (service *localACMETestService) handleAccount(writer http.ResponseWriter, payload []byte) {
	var input struct {
		OnlyReturnExisting   bool     `json:"onlyReturnExisting"`
		TermsOfServiceAgreed bool     `json:"termsOfServiceAgreed"`
		Contact              []string `json:"contact"`
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		service.fail(writer, "decode account payload: %v", err)
		return
	}
	service.mu.Lock()
	defer service.mu.Unlock()
	status := http.StatusOK
	if !input.OnlyReturnExisting {
		if !input.TermsOfServiceAgreed || len(input.Contact) != 1 ||
			!strings.HasPrefix(input.Contact[0], "mailto:") {
			service.recordError("new account omitted terms or contact")
			writer.WriteHeader(http.StatusBadRequest)
			return
		}
		service.accountEmail = input.Contact[0]
		service.accountStatus = AccountStatusValid
		status = http.StatusCreated
	}
	writer.Header().Set("Location", service.AccountURL())
	service.writeJSON(writer, status, map[string]any{
		"status":  service.accountStatus,
		"contact": []string{service.accountEmail},
		"orders":  service.server.URL + "/accounts/1/orders",
	})
}

func (service *localACMETestService) handleAccountUpdate(writer http.ResponseWriter, payload []byte) {
	var input struct {
		Status AccountStatus `json:"status"`
	}
	if err := json.Unmarshal(payload, &input); err != nil || input.Status != AccountStatusDeactivated {
		service.fail(writer, "invalid account update")
		return
	}
	service.mu.Lock()
	service.accountStatus = input.Status
	service.mu.Unlock()
	writer.Header().Set("Location", service.AccountURL())
	service.writeJSON(writer, http.StatusOK, map[string]any{"status": input.Status})
}

func (service *localACMETestService) handleNewOrder(writer http.ResponseWriter, payload []byte) {
	var input struct {
		Identifiers []struct {
			Type  string `json:"type"`
			Value string `json:"value"`
		} `json:"identifiers"`
	}
	if err := json.Unmarshal(payload, &input); err != nil || len(input.Identifiers) != 1 ||
		input.Identifiers[0].Type != "dns" || input.Identifiers[0].Value != "service.example.test" {
		service.fail(writer, "invalid order identifiers")
		return
	}
	service.mu.Lock()
	if service.accountStatus != AccountStatusValid {
		service.mu.Unlock()
		service.fail(writer, "order created with inactive account")
		return
	}
	service.nextOrderID++
	order := &localACMETestOrder{id: service.nextOrderID, identifier: input.Identifiers[0].Value}
	service.orders[order.id] = order
	service.mu.Unlock()
	writer.Header().Set("Location", service.orderURL(order.id))
	service.writeOrder(writer, http.StatusCreated, order)
}

func (service *localACMETestService) handleAuthorization(writer http.ResponseWriter, path string) {
	id, ok := localACMEPathID(path, "/authz/")
	if !ok {
		service.fail(writer, "invalid authorization path")
		return
	}
	service.mu.Lock()
	order := service.orders[id]
	service.mu.Unlock()
	if order == nil {
		service.fail(writer, "unknown authorization")
		return
	}
	status := "pending"
	if order.accepted {
		status = "valid"
	}
	service.writeJSON(writer, http.StatusOK, map[string]any{
		"status":     status,
		"identifier": map[string]string{"type": "dns", "value": order.identifier},
		"challenges": []map[string]string{
			{"type": "http-01", "url": service.challengeURL(id, "http-01"), "status": status, "token": localACMEHTTPToken},
			{"type": "dns-01", "url": service.challengeURL(id, "dns-01"), "status": status, "token": localACMEDNSToken},
		},
	})
}

func (service *localACMETestService) handleChallenge(writer http.ResponseWriter, path string) {
	parts := strings.Split(strings.TrimPrefix(path, "/challenge/"), "/")
	if len(parts) != 2 || (parts[1] != "http-01" && parts[1] != "dns-01") {
		service.fail(writer, "invalid challenge path")
		return
	}
	id, ok := parsePositiveTestID(parts[0])
	if !ok {
		service.fail(writer, "invalid challenge order")
		return
	}
	service.mu.Lock()
	order := service.orders[id]
	if order != nil {
		order.accepted = true
	}
	service.mu.Unlock()
	if order == nil {
		service.fail(writer, "unknown challenge")
		return
	}
	token := localACMEHTTPToken
	if parts[1] == "dns-01" {
		token = localACMEDNSToken
	}
	service.writeJSON(writer, http.StatusOK, map[string]string{
		"type": parts[1], "url": service.challengeURL(id, parts[1]), "status": "processing", "token": token,
	})
}

func (service *localACMETestService) handleFinalize(writer http.ResponseWriter, path string, payload []byte) {
	id, ok := localACMEPathID(path, "/finalize/")
	if !ok {
		service.fail(writer, "invalid finalize path")
		return
	}
	var input struct {
		CSR string `json:"csr"`
	}
	if err := json.Unmarshal(payload, &input); err != nil {
		service.fail(writer, "decode finalize payload: %v", err)
		return
	}
	csrDER, err := base64.RawURLEncoding.DecodeString(input.CSR)
	if err != nil {
		service.fail(writer, "decode CSR: %v", err)
		return
	}
	csr, err := x509.ParseCertificateRequest(csrDER)
	if err != nil || csr.CheckSignature() != nil {
		service.fail(writer, "invalid CSR")
		return
	}
	service.mu.Lock()
	order := service.orders[id]
	service.mu.Unlock()
	if order == nil || !order.accepted || len(csr.DNSNames) != 1 || csr.DNSNames[0] != order.identifier {
		service.fail(writer, "finalize before valid authorization or with mismatched SAN")
		return
	}
	now := time.Now().UTC()
	template := &x509.Certificate{
		SerialNumber:          big.NewInt(int64(id + 100)),
		Subject:               pkix.Name{CommonName: "local ACME leaf"},
		DNSNames:              append([]string(nil), csr.DNSNames...),
		NotBefore:             now.Add(-time.Minute),
		NotAfter:              now.Add(12 * time.Hour),
		BasicConstraintsValid: true,
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	leafDER, err := x509.CreateCertificate(rand.Reader, template, service.caCert, csr.PublicKey, service.caKey)
	if err != nil {
		service.fail(writer, "issue local certificate: %v", err)
		return
	}
	chain := append(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: leafDER}),
		pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: service.caDER})...)
	service.mu.Lock()
	order.issuedPEM = chain
	service.issues++
	service.mu.Unlock()
	writer.Header().Set("Location", service.orderURL(id))
	service.writeOrder(writer, http.StatusOK, order)
}

func (service *localACMETestService) handleOrder(writer http.ResponseWriter, path string) {
	id, ok := localACMEPathID(path, "/order/")
	if !ok {
		service.fail(writer, "invalid order path")
		return
	}
	service.mu.Lock()
	order := service.orders[id]
	service.mu.Unlock()
	if order == nil {
		service.fail(writer, "unknown order")
		return
	}
	writer.Header().Set("Location", service.orderURL(id))
	service.writeOrder(writer, http.StatusOK, order)
}

func (service *localACMETestService) handleCertificate(writer http.ResponseWriter, path string) {
	id, ok := localACMEPathID(path, "/certificate/")
	if !ok {
		service.fail(writer, "invalid certificate path")
		return
	}
	service.mu.Lock()
	order := service.orders[id]
	var chain []byte
	if order != nil {
		chain = append([]byte(nil), order.issuedPEM...)
	}
	service.mu.Unlock()
	if len(chain) == 0 {
		service.fail(writer, "certificate not issued")
		return
	}
	writer.Header().Set("Content-Type", "application/pem-certificate-chain")
	writer.WriteHeader(http.StatusOK)
	_, _ = writer.Write(chain)
}

func (service *localACMETestService) writeOrder(
	writer http.ResponseWriter,
	status int,
	order *localACMETestOrder,
) {
	state := "pending"
	if order.accepted {
		state = "ready"
	}
	response := map[string]any{
		"status":         state,
		"identifiers":    []map[string]string{{"type": "dns", "value": order.identifier}},
		"authorizations": []string{service.authorizationURL(order.id)},
		"finalize":       service.finalizeURL(order.id),
	}
	if len(order.issuedPEM) != 0 {
		response["status"] = "valid"
		response["certificate"] = service.certificateURL(order.id)
	}
	service.writeJSON(writer, status, response)
}

func (service *localACMETestService) jwsPayload(
	writer http.ResponseWriter,
	request *http.Request,
) ([]byte, bool) {
	data, err := io.ReadAll(io.LimitReader(request.Body, 1<<20+1))
	if err != nil || len(data) > 1<<20 {
		service.fail(writer, "read JWS envelope")
		return nil, false
	}
	var envelope struct {
		Protected string `json:"protected"`
		Payload   string `json:"payload"`
		Signature string `json:"signature"`
	}
	if err := json.Unmarshal(data, &envelope); err != nil || envelope.Protected == "" || envelope.Signature == "" {
		service.fail(writer, "decode JWS envelope")
		return nil, false
	}
	protected, err := base64.RawURLEncoding.DecodeString(envelope.Protected)
	if err != nil {
		service.fail(writer, "decode JWS protected header")
		return nil, false
	}
	var header struct {
		Algorithm string          `json:"alg"`
		Nonce     string          `json:"nonce"`
		URL       string          `json:"url"`
		KID       string          `json:"kid"`
		JWK       json.RawMessage `json:"jwk"`
	}
	if err := json.Unmarshal(protected, &header); err != nil || header.Algorithm != "ES256" ||
		header.Nonce == "" || header.URL != service.server.URL+request.URL.Path ||
		(header.KID == "" && len(header.JWK) == 0) {
		service.fail(writer, "invalid JWS protected header")
		return nil, false
	}
	if header.KID != "" && header.KID != service.AccountURL() {
		service.fail(writer, "unexpected JWS account KID")
		return nil, false
	}
	payload, err := base64.RawURLEncoding.DecodeString(envelope.Payload)
	if err != nil {
		service.fail(writer, "decode JWS payload")
		return nil, false
	}
	return payload, true
}

func (service *localACMETestService) nextNonce() string {
	service.mu.Lock()
	defer service.mu.Unlock()
	service.nonce++
	return fmt.Sprintf("local-nonce-%d", service.nonce)
}

func (service *localACMETestService) fail(writer http.ResponseWriter, format string, arguments ...any) {
	message := fmt.Sprintf(format, arguments...)
	service.mu.Lock()
	service.recordError(message)
	service.mu.Unlock()
	service.writeJSON(writer, http.StatusBadRequest, map[string]string{
		"type": "urn:ietf:params:acme:error:malformed", "detail": "local ACME request rejected",
	})
}

func (service *localACMETestService) recordError(message string) {
	service.errors = append(service.errors, message)
}

func (service *localACMETestService) writeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}

func (service *localACMETestService) orderURL(id int) string {
	return fmt.Sprintf("%s/order/%d", service.server.URL, id)
}

func (service *localACMETestService) authorizationURL(id int) string {
	return fmt.Sprintf("%s/authz/%d", service.server.URL, id)
}

func (service *localACMETestService) challengeURL(id int, challengeType string) string {
	return fmt.Sprintf("%s/challenge/%d/%s", service.server.URL, id, challengeType)
}

func (service *localACMETestService) finalizeURL(id int) string {
	return fmt.Sprintf("%s/finalize/%d", service.server.URL, id)
}

func (service *localACMETestService) certificateURL(id int) string {
	return fmt.Sprintf("%s/certificate/%d", service.server.URL, id)
}

func localACMEPathID(path, prefix string) (int, bool) {
	return parsePositiveTestID(strings.TrimPrefix(path, prefix))
}

func parsePositiveTestID(value string) (int, bool) {
	var id int
	if _, err := fmt.Sscanf(value, "%d", &id); err != nil || id <= 0 || fmt.Sprintf("%d", id) != value {
		return 0, false
	}
	return id, true
}

const (
	localACMEHTTPToken = "bG9jYWwtaHR0cC0wMS10b2tlbg"
	localACMEDNSToken  = "bG9jYWwtZG5zLTAxLXRva2Vu"
)
