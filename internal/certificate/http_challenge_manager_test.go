/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestConfigHTTPChallengeManagerPersistsBeforePublishAndCleansLatestSnapshot(t *testing.T) {
	now := time.Date(2026, 7, 21, 18, 0, 0, 0, time.UTC)
	source := "events {}\nhttp {\n  server {\n    listen 80;\n    server_name example.com;\n  }\n}\n"
	initial := certificatePlanSnapshot(t, source)
	project, err := ProjectFromDraft(initial)
	if err != nil {
		t.Fatal(err)
	}
	ref := oneEditableServerRef(t, project)
	events := make([]string, 0)
	repository := &httpChallengeRepositoryStub{events: &events}
	publisher := &configurationPublisherStub{
		snapshot: initial, events: &events,
		result: ConfigurationPublication{ReleaseID: "eeeeeeeeeeeeeeeeeeeeeeeeeeeeeeee", Changed: true},
	}
	manager, err := NewConfigHTTPChallengeManager(ConfigHTTPChallengeManagerOptions{
		Publisher: publisher, Repository: repository,
		Random: bytes.NewReader(bytes.Repeat([]byte{0xcc}, 16)), Now: func() time.Time { return now },
	})
	if err != nil {
		t.Fatal(err)
	}
	task := validHTTPChallengeTask(now)
	repository.task = task
	if err := manager.Provision(context.Background(), task, []ServerRef{ref}, []HTTPChallengeResponse{{
		Identifier: "example.com", Token: "token-value", KeyAuthorization: "token-value.thumbprint",
	}}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "artifact.create,publish" || len(publisher.changes) != 1 ||
		len(publisher.changes[0].Creates) != 1 || len(publisher.changes[0].Replacements) != 1 {
		t.Fatalf("events=%v changes=%#v", events, publisher.changes)
	}
	provision := publisher.changes[0]
	if provision.Creates[0].Path != config.RelativePath(HTTPChallengeConfigPath(task.ID)) ||
		!strings.Contains(string(provision.Creates[0].Content), `return 200 "token-value.thumbprint";`) ||
		!strings.Contains(string(provision.Replacements[0].Content),
			"include /etc/nginx/"+HTTPChallengeConfigPath(task.ID)+";") {
		t.Fatalf("provision change = %#v", provision)
	}

	latestSource := string(provision.Replacements[0].Content)
	publisher.snapshot = certificatePlanSnapshot(t, latestSource)
	publisher.snapshot.Files = append(publisher.snapshot.Files, config.DraftFile{
		Path: provision.Creates[0].Path, Content: provision.Creates[0].Content,
	})
	events = events[:0]
	if err := manager.Cleanup(context.Background(), task.ID); err != nil {
		t.Fatal(err)
	}
	if strings.Join(events, ",") != "publish,artifact.cleaned" || len(publisher.changes) != 2 {
		t.Fatalf("cleanup events=%v changes=%#v", events, publisher.changes)
	}
	cleanup := publisher.changes[1]
	if len(cleanup.Deletes) != 1 || cleanup.Deletes[0] != provision.Creates[0].Path ||
		len(cleanup.Replacements) != 1 || string(cleanup.Replacements[0].Content) != source ||
		repository.artifact.State != ArtifactStateCleaned {
		t.Fatalf("cleanup=%#v artifact=%#v", cleanup, repository.artifact)
	}
}

func validHTTPChallengeTask(now time.Time) Task {
	return Task{
		ID: testHTTPTaskID, Kind: TaskKindIssue, State: TaskStateRunning, Stage: TaskStageProvisioning,
		PlanID: "11111111111111111111111111111111", AccountID: "22222222222222222222222222222222",
		Challenge: ChallengeHTTP01, CreatedBy: 7, RequestID: "request-http-challenge",
		CreatedAt: now.Add(-time.Minute), UpdatedAt: now, StartedAt: now.Add(-time.Minute),
	}
}

type configurationPublisherStub struct {
	snapshot config.DraftSnapshot
	changes  []ConfigurationChange
	result   ConfigurationPublication
	err      error
	events   *[]string
}

func (publisher *configurationPublisherStub) Publish(
	_ context.Context, _ config.Actor, _ string, mutation ConfigurationMutation,
) (ConfigurationPublication, error) {
	*publisher.events = append(*publisher.events, "publish")
	change, err := mutation(publisher.snapshot)
	if err != nil {
		return ConfigurationPublication{}, err
	}
	publisher.changes = append(publisher.changes, change)
	if publisher.err != nil {
		return publisher.result, publisher.err
	}
	return publisher.result, nil
}

type httpChallengeRepositoryStub struct {
	task     Task
	artifact ChallengeArtifact
	events   *[]string
}

func (repository *httpChallengeRepositoryStub) CreateCertificateChallengeArtifact(
	_ context.Context, artifact ChallengeArtifact,
) error {
	*repository.events = append(*repository.events, "artifact.create")
	repository.artifact = artifact
	return nil
}

func (repository *httpChallengeRepositoryStub) CertificateChallengeArtifacts(
	context.Context, TaskID,
) ([]ChallengeArtifact, error) {
	if repository.artifact.ID == "" {
		return nil, nil
	}
	return []ChallengeArtifact{repository.artifact}, nil
}

func (repository *httpChallengeRepositoryStub) UpdateCertificateChallengeArtifact(
	_ context.Context, _ ArtifactID, state ArtifactState, at time.Time,
) error {
	repository.artifact.State = state
	repository.artifact.UpdatedAt = at
	*repository.events = append(*repository.events, "artifact."+string(state))
	return nil
}

func (repository *httpChallengeRepositoryStub) CertificateTask(context.Context, TaskID) (Task, error) {
	return repository.task, nil
}
