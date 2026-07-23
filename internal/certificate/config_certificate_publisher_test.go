/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"
)

func TestConfigCertificatePublisherRevalidatesPlanAndReturnsExactBindings(t *testing.T) {
	now := time.Date(2026, 7, 21, 19, 0, 0, 0, time.UTC)
	snapshot := certificatePlanSnapshot(t,
		"events {}\nhttp {\n  server {\n    listen 443;\n    server_name example.com www.example.com;\n  }\n}\n")
	project, err := ProjectFromDraft(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	ref := oneEditableServerRef(t, project)
	certificateID := CertificateID("33333333333333333333333333333333")
	versionID := VersionID("44444444444444444444444444444444")
	bindingPlan, err := PlanCertificateBinding(context.Background(), project, []ServerRef{ref}, certificateID, versionID)
	if err != nil {
		t.Fatal(err)
	}
	refsJSON, _ := json.Marshal(bindingPlan.ServerRefs)
	diffJSON, _ := json.Marshal(bindingPlan.Files)
	plan := OrderPlan{
		ID: "11111111111111111111111111111111", State: PlanStateExecuted,
		Environment: EnvironmentProduction, Challenge: ChallengeHTTP01,
		AccountID: "22222222222222222222222222222222", CertificateID: certificateID, VersionID: versionID,
		PrimaryIdentifier: "example.com", IdentifiersJSON: `["example.com","www.example.com"]`,
		ServerRefsJSON: string(refsJSON), BindingDiffJSON: string(diffJSON),
		ProductionDigest: Digest(snapshot.Workspace.ProductionDigest), ExpiresAt: now.Add(time.Minute),
		CreatedBy: 7, RequestID: "request-plan", CreatedAt: now.Add(-time.Minute), ExecutedAt: now.Add(-time.Second),
	}
	task := Task{
		ID: testHTTPTaskID, Kind: TaskKindIssue, State: TaskStateRunning, Stage: TaskStageDeploying,
		PlanID: plan.ID, CertificateID: certificateID, VersionID: versionID, AccountID: plan.AccountID,
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-deploy",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
	}
	publisher := &configurationPublisherStub{
		snapshot: snapshot, events: &[]string{},
		result: ConfigurationPublication{ReleaseID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Changed: true},
	}
	service, err := NewConfigCertificatePublisher(ConfigCertificatePublisherOptions{
		Publisher: publisher, Random: bytes.NewReader(bytes.Repeat([]byte{0xdd}, 16)),
		Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := service.Deploy(context.Background(), task, plan, validStoredMaterial(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(publisher.changes) != 1 || len(publisher.changes[0].Replacements) != 1 {
		t.Fatalf("changes=%#v", publisher.changes)
	}
	publishedRef := oneEditableServerRef(t, bindingProject(t, string(publisher.changes[0].Replacements[0].Content)))
	if result.ReleaseID != "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee" || len(result.Bindings) != 1 ||
		result.Bindings[0].ConfigPath != publishedRef.Path ||
		result.Bindings[0].ServerFingerprint != publishedRef.Fingerprint ||
		len(publisher.changes) != 1 || publisher.changes[0].OperationKind != "certificate.bind" ||
		len(publisher.changes[0].Replacements) != 1 {
		t.Fatalf("result=%#v changes=%#v", result, publisher.changes)
	}
	if !stringsContainAll(string(publisher.changes[0].Replacements[0].Content),
		CertificateFullchainPath(certificateID, versionID), CertificatePrivateKeyPath(certificateID, versionID), "listen 443 ssl;") {
		t.Fatalf("replacement=%s", publisher.changes[0].Replacements[0].Content)
	}

	stale := snapshot
	stale.Workspace.ProductionDigest[0]++
	publisher.snapshot = stale
	_, err = service.Deploy(context.Background(), task, plan, validStoredMaterial(now))
	if !errors.Is(err, ErrPlanChanged) {
		t.Fatalf("Deploy(stale) error=%v, want ErrPlanChanged", err)
	}

	unboundPlan := plan
	unboundPlan.ServerRefsJSON = `[]`
	unboundPlan.BindingDiffJSON = `[]`
	beforeCalls := len(publisher.changes)
	unbound, err := service.Deploy(context.Background(), task, unboundPlan, validStoredMaterial(now))
	if err != nil || unbound.ReleaseID != UnboundDeploymentReleaseID || len(unbound.Bindings) != 0 ||
		len(publisher.changes) != beforeCalls {
		t.Fatalf("Deploy(unbound)=%#v error=%v changes=%d", unbound, err, len(publisher.changes))
	}
}

func validStoredMaterial(now time.Time) StoredCertificateMaterial {
	return StoredCertificateMaterial{
		FullchainDigest:  "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa",
		PrivateKeyDigest: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb",
		LeafFingerprint:  "cccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccccc",
		SerialNumber:     "1234", Issuer: "CN=Test CA", NotBefore: now.Add(-time.Hour), NotAfter: now.Add(90 * 24 * time.Hour),
	}
}

func stringsContainAll(value string, fragments ...string) bool {
	for _, fragment := range fragments {
		if !strings.Contains(value, fragment) {
			return false
		}
	}
	return true
}
