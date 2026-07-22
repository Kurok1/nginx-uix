/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"slices"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

// UnboundDeploymentReleaseID records that immutable material required no production configuration mutation.
const UnboundDeploymentReleaseID = "not_required"

// ConfigCertificatePublisherOptions are the immutable-material deployment dependencies.
type ConfigCertificatePublisherOptions struct {
	Publisher ConfigurationPublisher
	Random    io.Reader
	Now       func() time.Time
}

// ConfigCertificatePublisher revalidates one persisted diff before using the normal release path.
type ConfigCertificatePublisher struct {
	publisher ConfigurationPublisher
	random    io.Reader
	now       func() time.Time
}

// NewConfigCertificatePublisher validates deployment dependencies.
func NewConfigCertificatePublisher(options ConfigCertificatePublisherOptions) (*ConfigCertificatePublisher, error) {
	if options.Publisher == nil || options.Random == nil {
		return nil, fmt.Errorf("create certificate deployment publisher: dependencies are required")
	}
	if options.Now == nil {
		options.Now = time.Now
	}
	return &ConfigCertificatePublisher{publisher: options.Publisher, random: options.Random, now: options.Now}, nil
}

// Deploy proves the production digest, source refs and exact diff again immediately before publication.
func (publisher *ConfigCertificatePublisher) Deploy(
	ctx context.Context,
	task Task,
	plan OrderPlan,
	material StoredCertificateMaterial,
) (DeploymentResult, error) {
	if ctx == nil || publisher == nil || ValidateTask(task) != nil || ValidateOrderPlan(plan) != nil ||
		task.State != TaskStateRunning || task.Stage != TaskStageDeploying ||
		plan.Environment != EnvironmentProduction || plan.State != PlanStateExecuted ||
		task.PlanID != plan.ID || task.CertificateID != plan.CertificateID || task.VersionID != plan.VersionID ||
		!validStoredMaterialMetadata(material) {
		return DeploymentResult{}, fmt.Errorf("deploy certificate configuration: %w", ErrBindingConflict)
	}
	var refs []ServerRef
	if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil {
		return DeploymentResult{}, fmt.Errorf("deploy certificate configuration: %w", ErrPlanChanged)
	}
	if len(refs) == 0 {
		if plan.ServerRefsJSON != `[]` || plan.BindingDiffJSON != `[]` {
			return DeploymentResult{}, fmt.Errorf("deploy certificate configuration: %w", ErrPlanChanged)
		}
		return DeploymentResult{ReleaseID: UnboundDeploymentReleaseID, Bindings: []Binding{}}, nil
	}
	actor := config.Actor{UserID: task.CreatedBy, RequestID: task.RequestID}
	var bindings []Binding
	publication, err := publisher.publisher.Publish(
		ctx, actor, "Certificate deploy "+string(task.ID[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			if Digest(snapshot.Workspace.ProductionDigest) != plan.ProductionDigest {
				return ConfigurationChange{}, fmt.Errorf("deploy certificate configuration: %w", ErrPlanChanged)
			}
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			bindingPlan, planErr := PlanCertificateBinding(
				ctx, project, refs, plan.CertificateID, plan.VersionID,
			)
			if planErr != nil {
				return ConfigurationChange{}, fmt.Errorf("deploy certificate configuration: %w", ErrPlanChanged)
			}
			currentRefs, refsErr := json.Marshal(bindingPlan.ServerRefs)
			currentDiff, diffErr := json.Marshal(bindingPlan.Files)
			if refsErr != nil || diffErr != nil || string(currentRefs) != plan.ServerRefsJSON ||
				string(currentDiff) != plan.BindingDiffJSON {
				return ConfigurationChange{}, fmt.Errorf("deploy certificate configuration: %w", ErrPlanChanged)
			}
			bindings, planErr = publisher.buildBindings(plan, bindingPlan.ServerRefs)
			if planErr != nil {
				return ConfigurationChange{}, planErr
			}
			return ConfigurationChange{
				Replacements: bindingReplacements(bindingPlan), OperationKind: "certificate.bind",
				PreviewID: configurationPlanDigest(bindingPlan, nil), TargetID: string(task.ID),
			}, nil
		},
	)
	if err != nil {
		return DeploymentResult{}, err
	}
	if !publication.Changed || publication.ReleaseID == "" || len(bindings) != len(refs) {
		return DeploymentResult{}, fmt.Errorf("deploy certificate configuration: %w", ErrConfigurationReleaseUncertain)
	}
	return DeploymentResult{ReleaseID: publication.ReleaseID, Bindings: slices.Clone(bindings)}, nil
}

// Bind publishes one standalone binding plan for an existing immutable active version.
func (publisher *ConfigCertificatePublisher) Bind(
	ctx context.Context,
	task Task,
	plan BindingPlan,
) (DeploymentResult, error) {
	if ctx == nil || publisher == nil || ValidateTask(task) != nil || ValidateBindingPlan(plan) != nil ||
		task.Kind != TaskKindBind || task.State != TaskStateRunning || task.Stage != TaskStageDeploying ||
		plan.State != PlanStateExecuted || BindingPlanID(task.PlanID) != plan.ID ||
		task.CertificateID != plan.CertificateID || task.VersionID != plan.VersionID {
		return DeploymentResult{}, fmt.Errorf("bind certificate configuration: %w", ErrBindingConflict)
	}
	var refs []ServerRef
	if err := json.Unmarshal([]byte(plan.ServerRefsJSON), &refs); err != nil || len(refs) == 0 {
		return DeploymentResult{}, fmt.Errorf("bind certificate configuration: %w", ErrPlanChanged)
	}
	actor := config.Actor{UserID: task.CreatedBy, RequestID: task.RequestID}
	var bindings []Binding
	publication, err := publisher.publisher.Publish(
		ctx, actor, "Certificate bind "+string(task.ID[:8]),
		func(snapshot config.DraftSnapshot) (ConfigurationChange, error) {
			if Digest(snapshot.Workspace.ProductionDigest) != plan.ProductionDigest {
				return ConfigurationChange{}, fmt.Errorf("bind certificate configuration: %w", ErrPlanChanged)
			}
			project, projectErr := ProjectFromDraft(snapshot)
			if projectErr != nil {
				return ConfigurationChange{}, projectErr
			}
			bindingPlan, planErr := PlanCertificateBinding(ctx, project, refs, plan.CertificateID, plan.VersionID)
			if planErr != nil {
				return ConfigurationChange{}, fmt.Errorf("bind certificate configuration: %w", ErrPlanChanged)
			}
			currentRefs, refsErr := json.Marshal(bindingPlan.ServerRefs)
			currentDiff, diffErr := json.Marshal(bindingPlan.Files)
			if refsErr != nil || diffErr != nil || string(currentRefs) != plan.ServerRefsJSON ||
				string(currentDiff) != plan.BindingDiffJSON {
				return ConfigurationChange{}, fmt.Errorf("bind certificate configuration: %w", ErrPlanChanged)
			}
			bindings, planErr = publisher.buildBindings(OrderPlan{
				CertificateID: plan.CertificateID, VersionID: plan.VersionID,
			}, bindingPlan.ServerRefs)
			if planErr != nil {
				return ConfigurationChange{}, planErr
			}
			return ConfigurationChange{
				Replacements: bindingReplacements(bindingPlan), OperationKind: "certificate.bind",
				PreviewID: configurationPlanDigest(bindingPlan, nil), TargetID: string(task.ID),
			}, nil
		},
	)
	if err != nil {
		return DeploymentResult{}, err
	}
	if !publication.Changed || publication.ReleaseID == "" || len(bindings) != len(refs) {
		return DeploymentResult{}, fmt.Errorf("bind certificate configuration: %w", ErrConfigurationReleaseUncertain)
	}
	return DeploymentResult{ReleaseID: publication.ReleaseID, Bindings: slices.Clone(bindings)}, nil
}

func (publisher *ConfigCertificatePublisher) buildBindings(plan OrderPlan, refs []ServerRef) ([]Binding, error) {
	now := publisher.now().UTC()
	bindings := make([]Binding, 0, len(refs))
	for _, ref := range refs {
		id, err := NewBindingID(publisher.random)
		if err != nil {
			return nil, fmt.Errorf("build certificate bindings: %w", err)
		}
		names, namesErr := json.Marshal(ref.ServerNames)
		listeners, listenersErr := json.Marshal(ref.Listeners)
		binding := Binding{
			ID: id, CertificateID: plan.CertificateID, VersionID: plan.VersionID,
			ConfigPath: ref.Path, ServerStartOffset: int64(ref.StartOffset),
			ServerNamesJSON: string(names), ListenersJSON: string(listeners),
			ServerFingerprint: ref.Fingerprint, CreatedAt: now, UpdatedAt: now,
		}
		if namesErr != nil || listenersErr != nil || ValidateBinding(binding) != nil {
			return nil, fmt.Errorf("build certificate bindings: %w", ErrBindingConflict)
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

func validStoredMaterialMetadata(material StoredCertificateMaterial) bool {
	return validLowerHex(material.FullchainDigest, 64) && validLowerHex(material.PrivateKeyDigest, 64) &&
		validLowerHex(material.LeafFingerprint, 64) && material.SerialNumber != "" && len(material.SerialNumber) <= 256 &&
		material.Issuer != "" && len(material.Issuer) <= 512 && !material.NotBefore.IsZero() &&
		material.NotAfter.After(material.NotBefore)
}
