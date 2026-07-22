/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"path"
	"strings"
	"time"
)

const (
	cloudflareAPIBaseURL  = "https://api.cloudflare.com/client/v4"
	cloudflareBodyLimit   = 1 << 20
	cloudflareCallTimeout = 30 * time.Second
)

// CloudflareZone is the bounded active Zone required for DNS-01.
type CloudflareZone struct {
	ID          string
	Name        string
	NameServers []string
}

// CloudflareRecord is the exact task-owned TXT record used for cleanup.
type CloudflareRecord struct {
	ID      string
	ZoneID  string
	Name    string
	Content string
}

// CloudflareClient implements only the fixed Token and DNS operations required by v0.5.
type CloudflareClient struct {
	baseURL    *url.URL
	httpClient *http.Client
}

type cloudflareEnvelope[T any] struct {
	Success bool              `json:"success"`
	Errors  []cloudflareError `json:"errors"`
	Result  T                 `json:"result"`
}

type cloudflareError struct {
	Code int `json:"code"`
}

type cloudflareTokenResult struct {
	ID     string `json:"id"`
	Status string `json:"status"`
}

type cloudflareZoneResult struct {
	ID          string   `json:"id"`
	Name        string   `json:"name"`
	Status      string   `json:"status"`
	NameServers []string `json:"name_servers"`
}

type cloudflareRecordResult struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Type    string `json:"type"`
	Content string `json:"content"`
}

// NewCloudflareClient creates the production fixed-origin provider client.
func NewCloudflareClient() (*CloudflareClient, error) {
	baseURL, err := url.Parse(cloudflareAPIBaseURL)
	if err != nil {
		return nil, fmt.Errorf("create Cloudflare client: %w", err)
	}
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	transport := &http.Transport{
		Proxy: nil, DialContext: dialer.DialContext, DisableCompression: true, DisableKeepAlives: true,
		TLSClientConfig:        &tls.Config{MinVersion: tls.VersionTLS12},
		TLSHandshakeTimeout:    10 * time.Second,
		ResponseHeaderTimeout:  10 * time.Second,
		MaxResponseHeaderBytes: 64 << 10,
		ForceAttemptHTTP2:      true,
	}
	return newCloudflareClient(baseURL, &http.Client{Transport: transport})
}

func newCloudflareClient(baseURL *url.URL, client *http.Client) (*CloudflareClient, error) {
	if baseURL == nil || client == nil || (baseURL.Scheme != "http" && baseURL.Scheme != "https") ||
		baseURL.Host == "" || baseURL.User != nil || baseURL.RawQuery != "" || baseURL.Fragment != "" {
		return nil, fmt.Errorf("create Cloudflare client: invalid base URL")
	}
	clonedURL := *baseURL
	clonedClient := *client
	clonedClient.Timeout = cloudflareCallTimeout
	clonedClient.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	clonedClient.Jar = nil
	return &CloudflareClient{baseURL: &clonedURL, httpClient: &clonedClient}, nil
}

// VerifyToken requires an active Cloudflare user API Token.
func (c *CloudflareClient) VerifyToken(ctx context.Context, token string) error {
	if !validCloudflareTokenSecret(token) {
		return fmt.Errorf("verify Cloudflare token: %w", ErrCloudflareTokenInvalid)
	}
	var envelope cloudflareEnvelope[cloudflareTokenResult]
	if err := c.do(ctx, token, http.MethodGet, "/user/tokens/verify", nil, &envelope); err != nil {
		return fmt.Errorf("verify Cloudflare token: %w", err)
	}
	if envelope.Result.ID == "" || envelope.Result.Status != "active" {
		return fmt.Errorf("verify Cloudflare token: %w", ErrCloudflareTokenInvalid)
	}
	return nil
}

// ListZones proves Zone Read and returns a bounded active inventory.
func (c *CloudflareClient) ListZones(ctx context.Context, token string) ([]CloudflareZone, error) {
	if !validCloudflareTokenSecret(token) {
		return nil, fmt.Errorf("list Cloudflare zones: %w", ErrCloudflareTokenInvalid)
	}
	query := url.Values{"status": []string{"active"}, "per_page": []string{"50"}}
	var envelope cloudflareEnvelope[[]cloudflareZoneResult]
	if err := c.do(ctx, token, http.MethodGet, "/zones?"+query.Encode(), nil, &envelope); err != nil {
		return nil, fmt.Errorf("list Cloudflare zones: %w", err)
	}
	zones := make([]CloudflareZone, 0, min(len(envelope.Result), 50))
	for _, zone := range envelope.Result {
		canonical, _, err := normalizeDNSIdentifier(zone.Name)
		if err != nil || canonical != zone.Name || zone.Status != "active" || !validCloudflareID(zone.ID) {
			continue
		}
		zones = append(zones, CloudflareZone{
			ID: zone.ID, Name: canonical, NameServers: boundedNameServers(zone.NameServers),
		})
		if len(zones) == 50 {
			break
		}
	}
	return zones, nil
}

// FindZone selects the longest active exact Zone suffix visible to the Token.
func (c *CloudflareClient) FindZone(ctx context.Context, token, identifier string) (CloudflareZone, error) {
	if !validCloudflareTokenSecret(token) {
		return CloudflareZone{}, fmt.Errorf("find Cloudflare zone: %w", ErrCloudflareTokenInvalid)
	}
	canonical, _, err := normalizeDNSIdentifier(baseIdentifier(identifier))
	if err != nil {
		return CloudflareZone{}, fmt.Errorf("find Cloudflare zone: %w", ErrIdentifierInvalid)
	}
	labels := strings.Split(canonical, ".")
	for index := 0; index < len(labels)-1; index++ {
		candidate := strings.Join(labels[index:], ".")
		query := url.Values{"name": []string{candidate}, "status": []string{"active"}, "per_page": []string{"50"}}
		var envelope cloudflareEnvelope[[]cloudflareZoneResult]
		if err := c.do(ctx, token, http.MethodGet, "/zones?"+query.Encode(), nil, &envelope); err != nil {
			return CloudflareZone{}, fmt.Errorf("find Cloudflare zone: %w", err)
		}
		for _, zone := range envelope.Result {
			if zone.Name != candidate || zone.Status != "active" || !validCloudflareID(zone.ID) {
				continue
			}
			return CloudflareZone{ID: zone.ID, Name: zone.Name, NameServers: boundedNameServers(zone.NameServers)}, nil
		}
	}
	return CloudflareZone{}, fmt.Errorf("find Cloudflare zone: %w", ErrCloudflareZoneNotFound)
}

// CreateTXT creates one unproxied automatic-TTL challenge record.
func (c *CloudflareClient) CreateTXT(
	ctx context.Context,
	token, zoneID, name, value, taskLabel string,
) (CloudflareRecord, error) {
	if !validCloudflareTokenSecret(token) || !validCloudflareID(zoneID) || !validTXTName(name) ||
		value == "" || len(value) > 1024 || strings.ContainsAny(value, "\x00\r\n") || !validTaskLabel(taskLabel) {
		return CloudflareRecord{}, fmt.Errorf("create Cloudflare TXT: %w", ErrCloudflareUnavailable)
	}
	body, err := json.Marshal(struct {
		Type    string `json:"type"`
		Name    string `json:"name"`
		Content string `json:"content"`
		TTL     int    `json:"ttl"`
		Proxied bool   `json:"proxied"`
		Comment string `json:"comment"`
	}{Type: "TXT", Name: name, Content: value, TTL: 1, Proxied: false, Comment: "nginx-uix acme challenge " + taskLabel})
	if err != nil {
		return CloudflareRecord{}, fmt.Errorf("create Cloudflare TXT: %w", ErrCloudflareUnavailable)
	}
	var envelope cloudflareEnvelope[cloudflareRecordResult]
	endpoint := "/zones/" + zoneID + "/dns_records"
	if err := c.do(ctx, token, http.MethodPost, endpoint, body, &envelope); err != nil {
		return CloudflareRecord{}, fmt.Errorf("create Cloudflare TXT: %w", err)
	}
	result := envelope.Result
	if !validCloudflareID(result.ID) || result.Type != "TXT" || result.Name != name || result.Content != value {
		return CloudflareRecord{}, fmt.Errorf("create Cloudflare TXT: %w", ErrCloudflareUnavailable)
	}
	return CloudflareRecord{ID: result.ID, ZoneID: zoneID, Name: result.Name, Content: result.Content}, nil
}

// ReadTXT confirms that Cloudflare exposes the exact task-owned record before authoritative polling begins.
func (c *CloudflareClient) ReadTXT(
	ctx context.Context,
	token, zoneID, recordID string,
) (CloudflareRecord, error) {
	if !validCloudflareTokenSecret(token) || !validCloudflareID(zoneID) || !validCloudflareID(recordID) {
		return CloudflareRecord{}, fmt.Errorf("read Cloudflare TXT: %w", ErrCloudflareUnavailable)
	}
	var envelope cloudflareEnvelope[cloudflareRecordResult]
	endpoint := "/zones/" + zoneID + "/dns_records/" + recordID
	if err := c.do(ctx, token, http.MethodGet, endpoint, nil, &envelope); err != nil {
		return CloudflareRecord{}, fmt.Errorf("read Cloudflare TXT: %w", err)
	}
	result := envelope.Result
	if result.ID != recordID || result.Type != "TXT" || !validTXTName(result.Name) ||
		result.Content == "" || len(result.Content) > 1024 || strings.ContainsAny(result.Content, "\x00\r\n") {
		return CloudflareRecord{}, fmt.Errorf("read Cloudflare TXT: %w", ErrCloudflareUnavailable)
	}
	return CloudflareRecord{
		ID: result.ID, ZoneID: zoneID, Name: result.Name, Content: result.Content,
	}, nil
}

// DeleteRecord deletes only one persisted exact zone/record identifier pair.
func (c *CloudflareClient) DeleteRecord(ctx context.Context, token, zoneID, recordID string) error {
	if !validCloudflareTokenSecret(token) || !validCloudflareID(zoneID) || !validCloudflareID(recordID) {
		return fmt.Errorf("delete Cloudflare record: %w", ErrCloudflareUnavailable)
	}
	var envelope cloudflareEnvelope[struct {
		ID string `json:"id"`
	}]
	endpoint := "/zones/" + zoneID + "/dns_records/" + recordID
	if err := c.do(ctx, token, http.MethodDelete, endpoint, nil, &envelope); err != nil {
		return fmt.Errorf("delete Cloudflare record: %w", err)
	}
	if envelope.Result.ID != recordID {
		return fmt.Errorf("delete Cloudflare record: %w", ErrCloudflareUnavailable)
	}
	return nil
}

func (c *CloudflareClient) do(ctx context.Context, token, method, endpoint string, body []byte, target any) error {
	if ctx == nil || c == nil || c.baseURL == nil || c.httpClient == nil || target == nil || ctx.Err() != nil {
		return ErrCloudflareUnavailable
	}
	requestURL := *c.baseURL
	endpointPath, rawQuery, _ := strings.Cut(endpoint, "?")
	requestURL.Path = path.Join(strings.TrimSuffix(c.baseURL.Path, "/"), endpointPath)
	requestURL.RawQuery = rawQuery
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	request, err := http.NewRequestWithContext(ctx, method, requestURL.String(), reader)
	if err != nil {
		return ErrCloudflareUnavailable
	}
	request.Header.Set("Accept", "application/json")
	request.Header.Set("Authorization", "Bearer "+token)
	request.Header.Set("User-Agent", "nginx-uix/0.5")
	if body != nil {
		request.Header.Set("Content-Type", "application/json")
	}
	response, err := c.httpClient.Do(request)
	if err != nil {
		if ctx.Err() != nil {
			return ctx.Err()
		}
		return ErrCloudflareUnavailable
	}
	payload, readErr := io.ReadAll(io.LimitReader(response.Body, cloudflareBodyLimit+1))
	closeErr := response.Body.Close()
	if readErr != nil || closeErr != nil || len(payload) > cloudflareBodyLimit {
		return ErrCloudflareUnavailable
	}
	classified := classifyCloudflareStatus(response.StatusCode)
	if classified != nil {
		return classified
	}
	if err := json.Unmarshal(payload, target); err != nil {
		return ErrCloudflareUnavailable
	}
	success, ok := cloudflareSuccess(target)
	if !ok || !success {
		return ErrCloudflareUnavailable
	}
	return nil
}

func cloudflareSuccess(target any) (bool, bool) {
	payload, err := json.Marshal(target)
	if err != nil {
		return false, false
	}
	var header struct {
		Success bool              `json:"success"`
		Errors  []json.RawMessage `json:"errors"`
	}
	if err := json.Unmarshal(payload, &header); err != nil {
		return false, false
	}
	return header.Success && len(header.Errors) == 0, true
}

func classifyCloudflareStatus(status int) error {
	switch status {
	case http.StatusOK, http.StatusCreated:
		return nil
	case http.StatusUnauthorized:
		return ErrCloudflareTokenInvalid
	case http.StatusForbidden:
		return ErrCloudflarePermission
	case http.StatusTooManyRequests:
		return ErrCloudflareRateLimited
	default:
		return ErrCloudflareUnavailable
	}
}

func validCloudflareID(value string) bool {
	return validOpaqueID(value)
}

func validTXTName(value string) bool {
	if !strings.HasPrefix(value, "_acme-challenge.") || len(value) > 255 || strings.ContainsAny(value, "\x00\r\n") {
		return false
	}
	_, _, err := normalizeDNSIdentifier(strings.TrimPrefix(value, "_acme-challenge."))
	return err == nil
}

func validTaskLabel(value string) bool {
	if value == "" || len(value) > 64 {
		return false
	}
	for _, character := range value {
		if (character < 'a' || character > 'z') && (character < '0' || character > '9') && character != '-' {
			return false
		}
	}
	return true
}

func boundedNameServers(input []string) []string {
	result := make([]string, 0, min(len(input), 8))
	for _, server := range input {
		server = strings.TrimSuffix(strings.ToLower(server), ".")
		if len(result) >= 8 || !validASCIIDomain(server) {
			continue
		}
		result = append(result, server)
	}
	return result
}
