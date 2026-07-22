/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

// HTTPChallengeRepository persists task ownership before a challenge configuration can become live.
type HTTPChallengeRepository interface {
	CreateCertificateChallengeArtifact(context.Context, ChallengeArtifact) error
	CertificateChallengeArtifacts(context.Context, TaskID) ([]ChallengeArtifact, error)
	UpdateCertificateChallengeArtifact(context.Context, ArtifactID, ArtifactState, time.Time) error
	CertificateTask(context.Context, TaskID) (Task, error)
}

// ConfigHTTPChallengeManagerOptions are the complete HTTP-01 publication dependencies.
type ConfigHTTPChallengeManagerOptions struct {
	Publisher  ConfigurationPublisher
	Repository HTTPChallengeRepository
	Random     io.Reader
	Now        func() time.Time
}

// ConfigHTTPChallengeManager publishes and cleans only exact task-owned includes and fragments.
type ConfigHTTPChallengeManager struct {
	publisher  ConfigurationPublisher
	repository HTTPChallengeRepository
	random     io.Reader
	now        func() time.Time
}

// NewConfigHTTPChallengeManager validates the HTTP-01 transaction dependencies.
func NewConfigHTTPChallengeManager(options ConfigHTTPChallengeManagerOptions) (*ConfigHTTPChallengeManager, error) {
	if options.Publisher == nil || options.Repository == nil || options.Random == nil {
		return nil, fmt.Errorf("create HTTP challenge manager: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &ConfigHTTPChallengeManager{
		publisher: options.Publisher, repository: options.Repository, random: options.Random, now: options.Now,
	}, nil
}

// Provision persists cleanup ownership, then publishes one fragment and exact server includes.
func (manager *ConfigHTTPChallengeManager) Provision(
	ctx context.Context,
	task Task,
	refs []ServerRef,
	responses []HTTPChallengeResponse,
) error {
	if ctx == nil || manager == nil || ValidateTask(task) != nil || task.State != TaskStateRunning ||
		task.Challenge != ChallengeHTTP01 || len(refs) == 0 {
		return fmt.Errorf("provision HTTP challenge: %w", ErrBindingConflict)
	}
	existing, err := manager.repository.CertificateChallengeArtifacts(ctx, task.ID)
	if err != nil || len(existing) != 0 {
		return fmt.Errorf("provision HTTP challenge: existing artifact: %w", ErrConfigurationReleaseUncertain)
	}
	fragment, err := RenderHTTPChallengeFragment(responses)
	if err != nil {
		return err
	}
	artifactID, err := NewArtifactID(manager.random)
	if err != nil {
		return fmt.Errorf("provision HTTP challenge: generate artifact: %w", ErrConfigurationReleaseFailed)
	}
	now := manager.now().UTC()
	artifact := ChallengeArtifact{
		ID: artifactID, TaskID: task.ID, Kind: ArtifactHTTPInclude, State: ArtifactStateCreated,
		ConfigPath: HTTPChallengeConfigPath(task.ID), CreatedAt: now, UpdatedAt: now,
	}
	if err := manager.repository.CreateCertificateChallengeArtifact(ctx, artifact); err != nil {
		return fmt.Errorf("provision HTTP challenge: persist cleanup target: %w", err)
	}
	actor := config.Actor{UserID: task.CreatedBy, RequestID: task.RequestID}
	publication, err := manager.publisher.Publish(
		ctx, actor, "ACME HTTP challenge "+string(task.ID[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			plan, planErr := PlanHTTPChallengeProvision(ctx, project, refs, task.ID)
			if planErr != nil {
				return ConfigurationChange{}, planErr
			}
			return ConfigurationChange{
				Creates: []ConfigurationFile{{
					Path: config.RelativePath(artifact.ConfigPath), Content: []byte(fragment),
				}},
				Replacements:  bindingReplacements(plan),
				OperationKind: "certificate.http_challenge_provision",
				PreviewID:     configurationPlanDigest(plan, []byte(fragment)), TargetID: string(task.ID),
			}, nil
		},
	)
	if err != nil {
		return fmt.Errorf("provision HTTP challenge: %w", err)
	}
	if !publication.Changed || publication.ReleaseID == "" {
		return fmt.Errorf("provision HTTP challenge: %w", ErrConfigurationReleaseUncertain)
	}
	return nil
}

// Cleanup removes exact task includes from a fresh snapshot and deletes only its managed fragment.
func (manager *ConfigHTTPChallengeManager) Cleanup(ctx context.Context, taskID TaskID) error {
	if ctx == nil || manager == nil || parseOpaqueID(string(taskID)) != nil {
		return fmt.Errorf("cleanup HTTP challenge: %w", ErrBindingConflict)
	}
	task, err := manager.repository.CertificateTask(ctx, taskID)
	if err != nil || ValidateTask(task) != nil || task.ID != taskID {
		return fmt.Errorf("cleanup HTTP challenge: task: %w", ErrConfigurationReleaseUncertain)
	}
	artifacts, err := manager.repository.CertificateChallengeArtifacts(ctx, taskID)
	if err != nil {
		return fmt.Errorf("cleanup HTTP challenge: artifacts: %w", ErrConfigurationReleaseUncertain)
	}
	created := make([]ChallengeArtifact, 0, 1)
	for _, artifact := range artifacts {
		if artifact.Kind != ArtifactHTTPInclude {
			continue
		}
		if ValidateArtifact(artifact) != nil || artifact.TaskID != taskID ||
			artifact.ConfigPath != HTTPChallengeConfigPath(taskID) {
			return fmt.Errorf("cleanup HTTP challenge: artifact: %w", ErrConfigurationReleaseUncertain)
		}
		switch artifact.State {
		case ArtifactStateCreated:
			created = append(created, artifact)
		case ArtifactStateCleaned:
			continue
		case ArtifactStateNeedsAttention:
			return fmt.Errorf("cleanup HTTP challenge: artifact: %w", ErrConfigurationReleaseUncertain)
		}
	}
	if len(created) == 0 {
		return nil
	}
	if len(created) != 1 {
		return fmt.Errorf("cleanup HTTP challenge: duplicate artifact: %w", ErrConfigurationReleaseUncertain)
	}
	artifact := created[0]
	actor := config.Actor{UserID: task.CreatedBy, RequestID: task.RequestID}
	_, publishErr := manager.publisher.Publish(
		ctx, actor, "ACME HTTP cleanup "+string(task.ID[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			plan, planErr := PlanHTTPChallengeCleanup(ctx, project, task.ID)
			if planErr != nil {
				return ConfigurationChange{}, planErr
			}
			change := ConfigurationChange{
				Replacements:  bindingReplacements(plan),
				OperationKind: "certificate.http_challenge_cleanup",
				PreviewID:     configurationPlanDigest(plan, nil), TargetID: string(task.ID),
			}
			if draftContainsPath(snapshot, config.RelativePath(artifact.ConfigPath)) {
				change.Deletes = []config.RelativePath{config.RelativePath(artifact.ConfigPath)}
			}
			return change, nil
		},
	)
	if publishErr != nil {
		if errors.Is(publishErr, ErrConfigurationReleaseUncertain) {
			updateContext, cancel := detachedOperationContext(ctx, certificateCommitTimeout)
			updateErr := manager.repository.UpdateCertificateChallengeArtifact(
				updateContext, artifact.ID, ArtifactStateNeedsAttention, manager.now().UTC(),
			)
			cancel()
			publishErr = errors.Join(publishErr, updateErr)
		}
		return fmt.Errorf("cleanup HTTP challenge: %w", publishErr)
	}
	if err := manager.repository.UpdateCertificateChallengeArtifact(
		ctx, artifact.ID, ArtifactStateCleaned, manager.now().UTC(),
	); err != nil {
		return fmt.Errorf("cleanup HTTP challenge: persist result: %w", ErrConfigurationReleaseUncertain)
	}
	return nil
}

func bindingReplacements(plan BindingChangePlan) []config.FileReplacement {
	replacements := make([]config.FileReplacement, 0, len(plan.Files))
	for _, file := range plan.Files {
		replacements = append(replacements, config.FileReplacement{
			Path: config.RelativePath(file.Path), Content: []byte(file.After),
		})
	}
	return replacements
}

func configurationPlanDigest(plan BindingChangePlan, additional []byte) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("nginx-uix-certificate-config-v1\x00" + plan.Mode + "\x00"))
	for _, file := range plan.Files {
		_, _ = digest.Write([]byte(file.Path + "\x00" + file.After + "\x00"))
	}
	_, _ = digest.Write(additional)
	return hex.EncodeToString(digest.Sum(nil))
}

func draftContainsPath(snapshot config.DraftSnapshot, wanted config.RelativePath) bool {
	return slices.ContainsFunc(snapshot.Files, func(file config.DraftFile) bool { return file.Path == wanted })
}
