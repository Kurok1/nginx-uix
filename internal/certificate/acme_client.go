/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto"
	"crypto/tls"
	"fmt"
	"net"
	"net/http"
	"time"

	"golang.org/x/crypto/acme"
)

const acmeCallTimeout = 45 * time.Second

// XCryptoACMEFactory creates bounded official x/crypto/acme clients.
type XCryptoACMEFactory struct {
	httpClient         *http.Client
	allowedDirectories map[string]struct{}
}

// NewXCryptoACMEFactory creates a fixed-network-policy ACME client factory.
func NewXCryptoACMEFactory() *XCryptoACMEFactory {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext, DisableCompression: true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  15 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		ForceAttemptHTTP2:      true,
	}
	client := &http.Client{Transport: transport, Timeout: acmeCallTimeout}
	return newXCryptoACMEFactory(
		client,
		LetsEncryptStagingDirectory,
		LetsEncryptProductionDirectory,
	)
}

// newXCryptoACMEFactory injects an exact construction-time directory allowlist for local RFC 8555 tests.
// Production assembly uses NewXCryptoACMEFactory and therefore permits only the two fixed Let's Encrypt URLs.
func newXCryptoACMEFactory(httpClient *http.Client, directoryURLs ...string) *XCryptoACMEFactory {
	if httpClient == nil {
		return &XCryptoACMEFactory{}
	}
	client := *httpClient
	client.Timeout = acmeCallTimeout
	client.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	allowed := make(map[string]struct{}, len(directoryURLs))
	for _, directoryURL := range directoryURLs {
		if validHTTPSURL(directoryURL) {
			allowed[directoryURL] = struct{}{}
		}
	}
	return &XCryptoACMEFactory{httpClient: &client, allowedDirectories: allowed}
}

func (factory *XCryptoACMEFactory) allowsDirectory(directoryURL string) bool {
	if factory == nil {
		return false
	}
	_, allowed := factory.allowedDirectories[directoryURL]
	return allowed
}

// NewAccountClient binds one supported key to one fixed Let's Encrypt directory.
func (factory *XCryptoACMEFactory) NewAccountClient(
	directoryURL string,
	key crypto.Signer,
	accountURI string,
) (ACMEAccountClient, error) {
	if factory == nil || factory.httpClient == nil || !validPrivateKey(key) ||
		!factory.allowsDirectory(directoryURL) ||
		(accountURI != "" && !validACMEAccountURI(directoryURL, accountURI)) {
		return nil, fmt.Errorf("create ACME account client: %w", ErrACMEAccountInvalid)
	}
	client := &acme.Client{
		Key: key, HTTPClient: factory.httpClient, DirectoryURL: directoryURL,
		UserAgent: "nginx-uix/0.5",
	}
	if accountURI != "" {
		client.KID = acme.KeyID(accountURI)
	}
	return &xCryptoACMEAccountClient{client: client, directoryURL: directoryURL}, nil
}

type xCryptoACMEAccountClient struct {
	client       *acme.Client
	directoryURL string
}

func (client *xCryptoACMEAccountClient) Discover(ctx context.Context) (ACMEDirectory, error) {
	directory, err := client.client.Discover(ctx)
	if err != nil {
		return ACMEDirectory{}, err
	}
	return ACMEDirectory{
		URL: client.directoryURL, TermsURL: directory.Terms, Website: directory.Website,
		ExternalAccountRequired: directory.ExternalAccountRequired,
	}, nil
}

func (client *xCryptoACMEAccountClient) Register(
	ctx context.Context,
	email string,
	termsURL string,
) (RemoteAccount, error) {
	registration, err := client.client.Register(ctx, &acme.Account{Contact: []string{"mailto:" + email}}, func(actual string) bool {
		return actual == termsURL
	})
	if err != nil {
		return RemoteAccount{}, err
	}
	return remoteAccount(registration), nil
}

func (client *xCryptoACMEAccountClient) GetRegistration(ctx context.Context, uri string) (RemoteAccount, error) {
	registration, err := client.client.GetReg(ctx, uri)
	if err != nil {
		return RemoteAccount{}, err
	}
	return remoteAccount(registration), nil
}

func (client *xCryptoACMEAccountClient) Deactivate(ctx context.Context) error {
	return client.client.DeactivateReg(ctx)
}

func remoteAccount(account *acme.Account) RemoteAccount {
	if account == nil {
		return RemoteAccount{}
	}
	status := AccountStatus(account.Status)
	if !status.Valid() {
		status = ""
	}
	return RemoteAccount{URI: account.URI, Status: status}
}
