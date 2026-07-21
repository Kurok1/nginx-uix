/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"encoding/json"
	"strings"
	"testing"
	"time"
)

func TestNewSafeRequestNeverPersistsBodyOrSensitiveHeaderValue(t *testing.T) {
	validated, err := ValidateRequest(Request{
		StaticRequest: StaticRequest{Scheme: SchemeHTTP, Host: "example.test", Port: 80, URI: "/submit"},
		Method:        "POST",
		Headers: []Header{
			{Name: "Authorization", Value: "Bearer top-secret"},
			{Name: "X-Trace-ID", Value: "trace-1"},
		},
		Body: []byte("sensitive-payload"), Timeout: time.Second,
		Confirmation: SideEffectConfirmation,
	})
	if err != nil {
		t.Fatal(err)
	}
	safe := NewSafeRequest(validated)
	payload, err := json.Marshal(safe)
	if err != nil {
		t.Fatal(err)
	}
	text := string(payload)
	if strings.Contains(text, "top-secret") || strings.Contains(text, "sensitive-payload") ||
		!strings.Contains(text, "Authorization") || !strings.Contains(text, "trace-1") ||
		safe.Replayable || safe.BodyBytes != int64(len(validated.Body)) || len(safe.BodyDigest) != 64 {
		t.Fatalf("safe request = %s", text)
	}
}

func TestEvaluateAssertionsDistinguishesFailureFromTruncation(t *testing.T) {
	passed := EvaluateAssertions(Assertions{
		StatusCode: 201, ContainsText: "created", ForbiddenText: "failed",
	}, 201, []byte("resource created"), false)
	if !passed.Passed || !passed.Complete || len(passed.Results) != 3 {
		t.Fatalf("passed outcome = %+v", passed)
	}

	truncated := EvaluateAssertions(Assertions{ContainsText: "tail"}, 200, []byte("prefix"), true)
	if truncated.Passed || truncated.Complete || len(truncated.Results) != 1 || truncated.Results[0].Complete {
		t.Fatalf("truncated outcome = %+v", truncated)
	}

	failed := EvaluateAssertions(Assertions{ForbiddenText: "secret"}, 200, []byte("secret"), true)
	if failed.Passed || !failed.Complete || failed.Results[0].Passed {
		t.Fatalf("failed outcome = %+v", failed)
	}
}
