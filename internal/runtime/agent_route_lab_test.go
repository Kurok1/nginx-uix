/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

func TestAgentClientRoundTripsTypedRouteTest(t *testing.T) {
	request := testAgentRouteRequest(t)
	want := routelab.AgentResult{
		CandidateDigest: config.Digest{4},
		Routes: []routelab.RouteDefinition{
			{RouteID: "srv_1", NodeID: "node_1", Kind: routelab.RouteServer},
			{RouteID: "loc_1", NodeID: "node_2", ParentRouteID: "srv_1", Kind: routelab.RouteLocation},
		},
		Response: routelab.Response{
			StatusCode: 201, Headers: []routelab.Header{{Name: "Content-Type", Value: "text/plain"}},
			BodySnippet: "created", BodyBytes: 7, BodyDigest: strings.Repeat("0", 64), Duration: 11 * time.Millisecond,
			Assertions: routelab.AssertionOutcome{Results: []routelab.AssertionResult{}},
		},
		Evidence: routelab.RuntimeEvidence{
			ServerRouteID: "srv_1", RouteID: "loc_1", FinalURI: "/final", StatusCode: 201,
			RequestTime: 9 * time.Millisecond,
		},
		Cleanup:     routelab.CleanupEvidence{MasterReaped: true, PortClosed: true, StageRemoved: true},
		Diagnostics: []routelab.AgentDiagnostic{},
	}
	operations := &recordingRouteAgentOperations{
		recordingAgentOperations: &recordingAgentOperations{}, result: want,
	}
	path := startAgentClientUnixServer(t, newAgentProtocolHandler(operations, nil))

	got, err := newAgentClient(path).ExecuteRouteTest(context.Background(), request)
	if err != nil {
		t.Fatalf("ExecuteRouteTest() error = %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("ExecuteRouteTest() = %+v, want %+v", got, want)
	}
	if !reflect.DeepEqual(operations.request, request) {
		t.Fatalf("agent request = %+v, want %+v", operations.request, request)
	}
}

func TestAgentClientMapsRouteCleanupFailure(t *testing.T) {
	operations := &recordingRouteAgentOperations{
		recordingAgentOperations: &recordingAgentOperations{},
		err:                      errors.Join(context.DeadlineExceeded, routelab.ErrCleanupFailed),
	}
	path := startAgentClientUnixServer(t, newAgentProtocolHandler(operations, nil))
	_, err := newAgentClient(path).ExecuteRouteTest(context.Background(), testAgentRouteRequest(t))
	if !errors.Is(err, routelab.ErrCleanupFailed) {
		t.Fatalf("ExecuteRouteTest() error = %v, want ErrCleanupFailed", err)
	}
}

func testAgentRouteRequest(t *testing.T) routelab.AgentRequest {
	t.Helper()
	validated, err := routelab.ValidateRequest(routelab.Request{
		StaticRequest: routelab.StaticRequest{
			Scheme: routelab.SchemeHTTP, Host: "example.test", Port: 8080, URI: "/submit",
		},
		Method: "POST", Query: "source=test",
		Headers: []routelab.Header{{Name: "Authorization", Value: "Bearer secret"}},
		Body:    []byte("payload"), Timeout: time.Second,
		Assertions:   routelab.Assertions{StatusCode: 201, ContainsText: "created"},
		Confirmation: routelab.SideEffectConfirmation,
	})
	if err != nil {
		t.Fatal(err)
	}
	validated.UpstreamSideEffect = true
	return routelab.AgentRequest{
		RunID:            "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		WorkspaceID:      "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		ProductionDigest: config.Digest{1}, DraftDigest: config.Digest{2},
		Request: validated, RequestID: "route-request-1",
	}
}

type recordingRouteAgentOperations struct {
	*recordingAgentOperations
	request routelab.AgentRequest
	result  routelab.AgentResult
	err     error
}

func (operations *recordingRouteAgentOperations) ExecuteRouteTest(
	_ context.Context,
	request routelab.AgentRequest,
) (routelab.AgentResult, error) {
	operations.request = request
	return operations.result, operations.err
}
