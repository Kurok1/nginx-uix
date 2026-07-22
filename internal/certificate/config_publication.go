/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/kuroky/nginx-uix/internal/config"
)

const (
	configurationPublicationFileLimit = 200
	configurationCleanupTimeout       = 30 * time.Second
)

var (
	// ErrConfigurationReleaseFailed indicates that the ordinary release path reached a safe negative terminal state.
	ErrConfigurationReleaseFailed = errors.New("certificate configuration release failed")
	// ErrConfigurationReleaseUncertain indicates that production state cannot be established automatically.
	ErrConfigurationReleaseUncertain = errors.New("certificate configuration release uncertain")
)

// ConfigurationFile is one new task-owned managed text file.
type ConfigurationFile struct {
	Path    config.RelativePath
	Content []byte
}

// ConfigurationChange is one callback-produced mutation against an exact fresh production snapshot.
type ConfigurationChange struct {
	Creates       []ConfigurationFile
	Replacements  []config.FileReplacement
	Deletes       []config.RelativePath
	OperationKind string
	PreviewID     string
	TargetID      string
}

// ConfigurationMutation plans a bounded change without publishing it directly.
type ConfigurationMutation func(config.DraftSnapshot) (ConfigurationChange, error)

// ConfigurationPublication records only safe release and workspace cleanup evidence.
type ConfigurationPublication struct {
	ReleaseID               string
	Changed                 bool
	WorkspaceCleanupPending bool
}

// ConfigurationPublisher applies a callback only through the ordinary validated release pipeline.
type ConfigurationPublisher interface {
	Publish(context.Context, config.Actor, string, ConfigurationMutation) (ConfigurationPublication, error)
}

// ConfigPublicationWorkspace is the exact workspace capability required by certificate transactions.
type ConfigPublicationWorkspace interface {
	Create(context.Context, config.Actor, string) (config.Workspace, error)
	DraftSnapshot(context.Context, config.WorkspaceID) (config.DraftSnapshot, error)
	CreateFile(context.Context, config.Actor, config.WorkspaceID, config.CreateFileInput) (config.MutationResult, error)
	ReplaceFiles(context.Context, config.Actor, config.WorkspaceID, config.ReplaceFilesInput) (config.ReplaceFilesResult, error)
	DeleteFile(context.Context, config.Actor, config.WorkspaceID, config.DeleteFileInput) (config.MutationResult, error)
	Delete(context.Context, config.Actor, config.WorkspaceID, string, string) error
}

// ConfigPublicationRelease is the normal digest-bound release coordinator.
type ConfigPublicationRelease interface {
	Check(context.Context, config.Actor, config.PublishCheckInput) (config.PublishCheck, error)
	Queue(context.Context, config.Actor, config.QueueReleaseInput) (config.Release, error)
	Run(context.Context, config.ReleaseID) error
	Release(context.Context, config.ReleaseID) (config.Release, error)
}

// ConfigPublicationServiceOptions are the complete publication adapter dependencies.
type ConfigPublicationServiceOptions struct {
	Workspaces ConfigPublicationWorkspace
	Releases   ConfigPublicationRelease
}

// ConfigPublicationService owns short-lived certificate system workspaces.
type ConfigPublicationService struct {
	workspaces ConfigPublicationWorkspace
	releases   ConfigPublicationRelease
}

// NewConfigPublicationService validates the reusable production-publication adapter.
func NewConfigPublicationService(options ConfigPublicationServiceOptions) (*ConfigPublicationService, error) {
	if options.Workspaces == nil || options.Releases == nil {
		return nil, fmt.Errorf("create certificate configuration publisher: dependencies are required")
	}
	return &ConfigPublicationService{workspaces: options.Workspaces, releases: options.Releases}, nil
}

// Publish snapshots current production, applies one bounded change, validates, publishes and cleans its workspace.
func (service *ConfigPublicationService) Publish(
	ctx context.Context,
	actor config.Actor,
	name string,
	mutation ConfigurationMutation,
) (publication ConfigurationPublication, returnErr error) {
	if ctx == nil || service == nil || actor.UserID <= 0 || !validRequestID(actor.RequestID) || mutation == nil {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: invalid input")
	}
	if validated, err := config.ValidateDisplayName(name); err != nil || validated != name {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: invalid name")
	}
	actor.System = true
	workspace, err := service.workspaces.Create(ctx, actor, name)
	if err != nil {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: create workspace: %w", err)
	}
	workspaceETag := workspace.ETag()
	cleanupAllowed := true
	productionSucceeded := false
	defer func() {
		if !cleanupAllowed {
			return
		}
		cleanupContext, cancel := context.WithTimeout(context.WithoutCancel(ctx), configurationCleanupTimeout)
		cleanupErr := service.workspaces.Delete(cleanupContext, actor, workspace.ID, workspaceETag, workspace.Name)
		cancel()
		if cleanupErr == nil {
			return
		}
		if productionSucceeded {
			publication.WorkspaceCleanupPending = true
			return
		}
		returnErr = errors.Join(returnErr, fmt.Errorf("clean certificate configuration workspace: %w", cleanupErr))
	}()

	snapshot, err := service.workspaces.DraftSnapshot(ctx, workspace.ID)
	if err != nil {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: snapshot: %w", err)
	}
	workspace = snapshot.Workspace
	workspaceETag = snapshot.WorkspaceETag
	change, err := mutation(snapshot)
	if err != nil {
		return ConfigurationPublication{}, err
	}
	if err := validateConfigurationChange(change); err != nil {
		return ConfigurationPublication{}, err
	}
	if len(change.Creates)+len(change.Replacements)+len(change.Deletes) == 0 {
		return ConfigurationPublication{}, nil
	}
	for _, file := range change.Creates {
		result, createErr := service.workspaces.CreateFile(ctx, actor, workspace.ID, config.CreateFileInput{
			Path: file.Path, Content: file.Content, IfMatch: workspaceETag,
		})
		if createErr != nil {
			return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: create file: %w", createErr)
		}
		workspace = result.Workspace
		workspaceETag = workspace.ETag()
	}
	if len(change.Replacements) > 0 {
		result, replaceErr := service.workspaces.ReplaceFiles(ctx, actor, workspace.ID, config.ReplaceFilesInput{
			Replacements: change.Replacements, IfMatch: workspaceETag,
			OperationKind: change.OperationKind, PreviewID: change.PreviewID, TargetID: change.TargetID,
		})
		if replaceErr != nil {
			return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: replace files: %w", replaceErr)
		}
		workspace = result.Workspace
		workspaceETag = workspace.ETag()
	}
	for _, filePath := range change.Deletes {
		result, deleteErr := service.workspaces.DeleteFile(ctx, actor, workspace.ID, config.DeleteFileInput{
			Path: filePath, ConfirmPath: string(filePath), IfMatch: workspaceETag,
		})
		if deleteErr != nil {
			return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: delete file: %w", deleteErr)
		}
		workspace = result.Workspace
		workspaceETag = workspace.ETag()
	}
	check, err := service.releases.Check(ctx, actor, config.PublishCheckInput{
		WorkspaceID: workspace.ID, IfMatch: workspaceETag,
	})
	if err != nil {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: check: %w", err)
	}
	release, err := service.releases.Queue(ctx, actor, config.QueueReleaseInput{
		WorkspaceID: workspace.ID, CheckID: check.ID, IfMatch: workspaceETag, ConfirmName: workspace.Name,
	})
	if err != nil {
		return ConfigurationPublication{}, fmt.Errorf("publish certificate configuration: queue: %w", err)
	}
	runErr := service.releases.Run(ctx, release.ID)
	readContext, cancelRead := detachedOperationContext(ctx, certificateCommitTimeout)
	terminal, readErr := service.releases.Release(readContext, release.ID)
	cancelRead()
	if readErr != nil {
		cleanupAllowed = false
		return ConfigurationPublication{ReleaseID: string(release.ID), Changed: true},
			errors.Join(ErrConfigurationReleaseUncertain, runErr)
	}
	switch terminal.State {
	case config.ReleaseStateSucceeded:
		productionSucceeded = true
		return ConfigurationPublication{ReleaseID: string(release.ID), Changed: true}, nil
	case config.ReleaseStateFailed, config.ReleaseStateRolledBack, config.ReleaseStateCancelled:
		return ConfigurationPublication{ReleaseID: string(release.ID), Changed: true},
			errors.Join(ErrConfigurationReleaseFailed, runErr)
	case config.ReleaseStateNeedsAttention, config.ReleaseStateQueued,
		config.ReleaseStateRunning, config.ReleaseStateRollingBack:
		cleanupAllowed = false
		return ConfigurationPublication{ReleaseID: string(release.ID), Changed: true},
			errors.Join(ErrConfigurationReleaseUncertain, runErr)
	default:
		cleanupAllowed = false
		return ConfigurationPublication{ReleaseID: string(release.ID), Changed: true},
			errors.Join(ErrConfigurationReleaseUncertain, runErr)
	}
}

func validateConfigurationChange(change ConfigurationChange) error {
	count := len(change.Creates) + len(change.Replacements) + len(change.Deletes)
	if count > configurationPublicationFileLimit {
		return fmt.Errorf("validate certificate configuration change: too many files")
	}
	if len(change.Replacements) > 0 &&
		(change.OperationKind == "" || parseOpaqueID(change.TargetID) != nil || len(change.PreviewID) != 64 || !validLowerHex(change.PreviewID, 64)) {
		return fmt.Errorf("validate certificate configuration change: invalid replacement metadata")
	}
	seen := make(map[config.RelativePath]bool, count)
	checkPath := func(filePath config.RelativePath) error {
		parsed, err := config.ParseRelativePath(string(filePath), config.DefaultLimits())
		if err != nil || parsed != filePath || seen[filePath] {
			return fmt.Errorf("validate certificate configuration change: invalid path")
		}
		seen[filePath] = true
		return nil
	}
	for _, file := range change.Creates {
		if len(file.Content) == 0 || checkPath(file.Path) != nil {
			return fmt.Errorf("validate certificate configuration change: invalid create")
		}
	}
	for _, file := range change.Replacements {
		if len(file.Content) == 0 || checkPath(file.Path) != nil {
			return fmt.Errorf("validate certificate configuration change: invalid replacement")
		}
	}
	for _, filePath := range change.Deletes {
		if checkPath(filePath) != nil {
			return fmt.Errorf("validate certificate configuration change: invalid deletion")
		}
	}
	return nil
}
