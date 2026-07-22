/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"testing"

	"github.com/kuroky/nginx-uix/internal/config"
)

func TestConfigPublicationServiceUsesValidatedReleasePipelineAndDeletesWorkspace(t *testing.T) {
	workspaces := &publicationWorkspaceStub{snapshot: certificatePlanSnapshot(t,
		"events {}\nhttp { server { listen 80; server_name example.com; } }\n")}
	releases := &publicationReleaseStub{terminal: config.Release{State: config.ReleaseStateSucceeded}}
	service, err := NewConfigPublicationService(ConfigPublicationServiceOptions{
		Workspaces: workspaces, Releases: releases,
	})
	if err != nil {
		t.Fatal(err)
	}
	actor := config.Actor{UserID: 7, RequestID: "request-publication"}
	publication, err := service.Publish(context.Background(), actor, "Certificate deploy deadbeef", func(
		snapshot config.DraftSnapshot,
	) (ConfigurationChange, error) {
		return ConfigurationChange{
			Replacements:  []config.FileReplacement{{Path: "nginx.conf", Content: []byte("events {}\n")}},
			OperationKind: "certificate.bind", PreviewID: digestHex(snapshot.Workspace.DraftDigest),
			TargetID: "11111111111111111111111111111111",
		}, nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if publication.ReleaseID != "dddddddddddddddddddddddddddddddd" || !publication.Changed ||
		workspaces.sequence != "create,snapshot,replace,delete" ||
		releases.sequence != "check,queue,run,release" || releases.queue.ConfirmName != "Certificate deploy deadbeef" {
		t.Fatalf("publication=%#v workspace sequence=%q release sequence=%q queue=%#v",
			publication, workspaces.sequence, releases.sequence, releases.queue)
	}
}

func TestConfigPublicationServicePreservesWorkspaceWhenReleaseIsUncertain(t *testing.T) {
	workspaces := &publicationWorkspaceStub{snapshot: certificatePlanSnapshot(t, "events {}\n")}
	releases := &publicationReleaseStub{
		runErr:   errors.New("connection lost"),
		terminal: config.Release{State: config.ReleaseStateNeedsAttention},
	}
	service, err := NewConfigPublicationService(ConfigPublicationServiceOptions{
		Workspaces: workspaces, Releases: releases,
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = service.Publish(
		context.Background(), config.Actor{UserID: 7, RequestID: "request-publication"}, "Certificate deploy deadbeef",
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			return ConfigurationChange{
				Creates:  []ConfigurationFile{{Path: "challenge.conf", Content: []byte("return 204;\n")}},
				TargetID: "11111111111111111111111111111111",
			}, nil
		},
	)
	if !errors.Is(err, ErrConfigurationReleaseUncertain) || workspaces.deleted {
		t.Fatalf("Publish() error=%v deleted=%v", err, workspaces.deleted)
	}
}

type publicationWorkspaceStub struct {
	snapshot config.DraftSnapshot
	sequence string
	deleted  bool
}

func (workspace *publicationWorkspaceStub) add(value string) {
	if workspace.sequence != "" {
		workspace.sequence += ","
	}
	workspace.sequence += value
}

func (workspace *publicationWorkspaceStub) Create(_ context.Context, _ config.Actor, name string) (config.Workspace, error) {
	workspace.add("create")
	value := workspace.snapshot.Workspace
	value.ID = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	value.Name = name
	workspace.snapshot.Workspace = value
	workspace.snapshot.WorkspaceETag = value.ETag()
	return value, nil
}

func (workspace *publicationWorkspaceStub) DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error) {
	workspace.add("snapshot")
	return workspace.snapshot, nil
}

func (workspace *publicationWorkspaceStub) CreateFile(
	_ context.Context, _ config.Actor, _ config.WorkspaceID, input config.CreateFileInput,
) (config.MutationResult, error) {
	workspace.add("create_file")
	workspace.snapshot.Workspace.Revision++
	workspace.snapshot.Workspace.DraftDigest[0]++
	return config.MutationResult{Workspace: workspace.snapshot.Workspace}, nil
}

func (workspace *publicationWorkspaceStub) ReplaceFiles(
	_ context.Context, _ config.Actor, _ config.WorkspaceID, _ config.ReplaceFilesInput,
) (config.ReplaceFilesResult, error) {
	workspace.add("replace")
	workspace.snapshot.Workspace.Revision++
	workspace.snapshot.Workspace.DraftDigest[0]++
	return config.ReplaceFilesResult{Workspace: workspace.snapshot.Workspace}, nil
}

func (workspace *publicationWorkspaceStub) DeleteFile(
	_ context.Context, _ config.Actor, _ config.WorkspaceID, _ config.DeleteFileInput,
) (config.MutationResult, error) {
	workspace.add("delete_file")
	workspace.snapshot.Workspace.Revision++
	workspace.snapshot.Workspace.DraftDigest[0]++
	return config.MutationResult{Workspace: workspace.snapshot.Workspace}, nil
}

func (workspace *publicationWorkspaceStub) Delete(
	_ context.Context, _ config.Actor, _ config.WorkspaceID, _ string, _ string,
) error {
	workspace.add("delete")
	workspace.deleted = true
	return nil
}

type publicationReleaseStub struct {
	sequence string
	queue    config.QueueReleaseInput
	runErr   error
	terminal config.Release
}

func (release *publicationReleaseStub) add(value string) {
	if release.sequence != "" {
		release.sequence += ","
	}
	release.sequence += value
}

func (release *publicationReleaseStub) Check(
	_ context.Context, _ config.Actor, input config.PublishCheckInput,
) (config.PublishCheck, error) {
	release.add("check")
	return config.PublishCheck{ID: "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb", WorkspaceID: input.WorkspaceID}, nil
}

func (release *publicationReleaseStub) Queue(
	_ context.Context, _ config.Actor, input config.QueueReleaseInput,
) (config.Release, error) {
	release.add("queue")
	release.queue = input
	return config.Release{ID: "dddddddddddddddddddddddddddddddd", State: config.ReleaseStateQueued}, nil
}

func (release *publicationReleaseStub) Run(context.Context, config.ReleaseID) error {
	release.add("run")
	return release.runErr
}

func (release *publicationReleaseStub) Release(context.Context, config.ReleaseID) (config.Release, error) {
	release.add("release")
	value := release.terminal
	value.ID = "dddddddddddddddddddddddddddddddd"
	return value, nil
}

func digestHex(value config.Digest) string {
	const hexDigits = "0123456789abcdef"
	encoded := make([]byte, len(value)*2)
	for index, item := range value {
		encoded[index*2] = hexDigits[item>>4]
		encoded[index*2+1] = hexDigits[item&0x0f]
	}
	return string(encoded)
}
