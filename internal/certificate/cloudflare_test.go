/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestCloudflareClientVerifiesTokenFindsLongestZoneAndDeletesExactRecord(t *testing.T) {
	t.Parallel()

	const token = "cfut_super-secret"
	requests := make([]string, 0, 8)
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Header.Get("Authorization") != "Bearer "+token {
			t.Errorf("Authorization = %q", request.Header.Get("Authorization"))
		}
		requests = append(requests, request.Method+" "+request.URL.RequestURI())
		writer.Header().Set("Content-Type", "application/json")
		switch {
		case request.URL.Path == "/user/tokens/verify":
			_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":{"id":"token-id","status":"active"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones":
			name := request.URL.Query().Get("name")
			if name == "deep.sub.example.com" || name == "sub.example.com" {
				_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":[]}`))
				return
			}
			if name != "" && name != "example.com" {
				t.Errorf("zone name = %q", name)
			}
			_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":[{"id":"0123456789abcdef0123456789abcdef","name":"example.com","status":"active","name_servers":["ns1.example.net"]}]}`))
		case request.Method == http.MethodPost && request.URL.Path == "/zones/0123456789abcdef0123456789abcdef/dns_records":
			_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":{"id":"fedcba9876543210fedcba9876543210","name":"_acme-challenge.sub.example.com","type":"TXT","content":"record-value"}}`))
		case request.Method == http.MethodGet && request.URL.Path == "/zones/0123456789abcdef0123456789abcdef/dns_records/fedcba9876543210fedcba9876543210":
			_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":{"id":"fedcba9876543210fedcba9876543210","name":"_acme-challenge.sub.example.com","type":"TXT","content":"record-value"}}`))
		case request.Method == http.MethodDelete && request.URL.Path == "/zones/0123456789abcdef0123456789abcdef/dns_records/fedcba9876543210fedcba9876543210":
			_, _ = writer.Write([]byte(`{"success":true,"errors":[],"result":{"id":"fedcba9876543210fedcba9876543210"}}`))
		default:
			http.NotFound(writer, request)
		}
	}))
	t.Cleanup(server.Close)

	baseURL, err := url.Parse(server.URL)
	if err != nil {
		t.Fatal(err)
	}
	client, err := newCloudflareClient(baseURL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := client.VerifyToken(ctx, token); err != nil {
		t.Fatalf("VerifyToken() error = %v", err)
	}
	zones, err := client.ListZones(ctx, token)
	if err != nil || len(zones) != 1 || zones[0].Name != "example.com" {
		t.Fatalf("ListZones() = %#v, %v", zones, err)
	}
	zone, err := client.FindZone(ctx, token, "deep.sub.example.com")
	if err != nil {
		t.Fatalf("FindZone() error = %v", err)
	}
	if zone.Name != "example.com" {
		t.Fatalf("FindZone().Name = %q", zone.Name)
	}
	record, err := client.CreateTXT(ctx, token, zone.ID, "_acme-challenge.sub.example.com", "record-value", "task-prefix")
	if err != nil {
		t.Fatalf("CreateTXT() error = %v", err)
	}
	observed, err := client.ReadTXT(ctx, token, zone.ID, record.ID)
	if err != nil || observed != record {
		t.Fatalf("ReadTXT() = %#v, %v, want %#v", observed, err, record)
	}
	if err := client.DeleteRecord(ctx, token, zone.ID, record.ID); err != nil {
		t.Fatalf("DeleteRecord() error = %v", err)
	}
	wantDelete := "DELETE /zones/0123456789abcdef0123456789abcdef/dns_records/fedcba9876543210fedcba9876543210"
	if requests[len(requests)-1] != wantDelete {
		t.Fatalf("last request = %q, want %q", requests[len(requests)-1], wantDelete)
	}
}

func TestCloudflareClientErrorsNeverContainTokenOrRecordValue(t *testing.T) {
	t.Parallel()

	const token = "cfut_do-not-leak"
	const value = "do-not-leak-record-value"
	server := httptest.NewServer(http.HandlerFunc(func(writer http.ResponseWriter, _ *http.Request) {
		writer.Header().Set("Content-Type", "application/json")
		writer.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprintf(writer, `{"success":false,"errors":[{"code":9109,"message":%q}]}`, token+" "+value)
	}))
	t.Cleanup(server.Close)
	baseURL, _ := url.Parse(server.URL)
	client, err := newCloudflareClient(baseURL, server.Client())
	if err != nil {
		t.Fatal(err)
	}
	_, err = client.CreateTXT(context.Background(), token, "0123456789abcdef0123456789abcdef", "_acme-challenge.example.com", value, "task")
	if !errors.Is(err, ErrCloudflarePermission) {
		t.Fatalf("CreateTXT() error = %v, want %v", err, ErrCloudflarePermission)
	}
	if strings.Contains(err.Error(), token) || strings.Contains(err.Error(), value) {
		t.Fatalf("error leaked secret: %v", err)
	}
}
