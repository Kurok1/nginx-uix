/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.2.1
 */

package httpapi

import (
	"errors"
	"net/http/httptest"
	"strings"
	"testing"
)

type strictJSONFixture struct {
	Name   string            `json:"name"`
	Nested map[string]string `json:"nested,omitempty"`
}

func TestDecodeStrictJSONRejectsInvalidRepresentations(t *testing.T) {
	tests := []struct {
		name        string
		contentType string
		body        string
	}{
		{name: "missing content type", body: `{"name":"review"}`},
		{name: "wrong content type", contentType: "text/plain", body: `{"name":"review"}`},
		{name: "empty", contentType: "application/json"},
		{name: "null", contentType: "application/json", body: `null`},
		{name: "array", contentType: "application/json", body: `[]`},
		{name: "duplicate field", contentType: "application/json", body: `{"name":"a","name":"b"}`},
		{name: "recursive duplicate field", contentType: "application/json", body: `{"name":"a","nested":{"key":"a","key":"b"}}`},
		{name: "unknown field", contentType: "application/json", body: `{"name":"a","other":true}`},
		{name: "wrong field case", contentType: "application/json", body: `{"Name":"a"}`},
		{name: "trailing json", contentType: "application/json", body: `{"name":"a"}{"name":"b"}`},
		{name: "malformed utf8", contentType: "application/json", body: "{\"name\":\"\xff\"}"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			request := httptest.NewRequest("POST", "/", strings.NewReader(test.body))
			if test.contentType != "" {
				request.Header.Set("Content-Type", test.contentType)
			}
			if _, err := decodeStrictJSON[strictJSONFixture](request, 4096); err == nil {
				t.Fatal("decodeStrictJSON() error = nil")
			}
		})
	}
}

func TestDecodeStrictJSONHonorsExactByteLimit(t *testing.T) {
	const limit = 64
	body := `{"name":"` + strings.Repeat("a", limit-len(`{"name":""}`)) + `"}`
	request := httptest.NewRequest("POST", "/", strings.NewReader(body))
	request.Header.Set("Content-Type", "application/json; charset=utf-8")
	decoded, err := decodeStrictJSON[strictJSONFixture](request, limit)
	if err != nil || decoded.Name == "" {
		t.Fatalf("decodeStrictJSON(exact) = %#v, %v", decoded, err)
	}

	request = httptest.NewRequest("POST", "/", strings.NewReader(body+" "))
	request.Header.Set("Content-Type", "application/json")
	if _, err := decodeStrictJSON[strictJSONFixture](request, limit); !errors.Is(err, errRequestBodyTooLarge) {
		t.Fatalf("decodeStrictJSON(+1) error = %v, want errRequestBodyTooLarge", err)
	}
}
