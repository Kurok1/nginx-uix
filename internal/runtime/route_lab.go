/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"
	"unicode"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/nginxast"
	"github.com/kuroky/nginx-uix/internal/routelab"
)

const (
	routeLabExecutionTimeout  = 45 * time.Second
	routeLabValidationTimeout = 15 * time.Second
	routeLabDiagnosticLimit   = 1 << 20
	routeLabFileLimit         = int64(64 << 20)
)

type portReserver func([]routelab.ListenerKey) (map[routelab.ListenerKey]int, func() error, error)
type sandboxRunner func(context.Context, sandboxRun) (
	routelab.Response,
	routelab.RuntimeEvidence,
	routelab.CleanupEvidence,
	error,
)

type routeLabOptions struct {
	NginxRoot       string
	WorkspaceRoot   string
	StageRoot       string
	Entry           config.RelativePath
	Limits          config.Limits
	NginxExecutable string
	Executor        commandExecutor
	TokenSource     io.Reader
	ReservePorts    portReserver
	RunSandbox      sandboxRunner
}

type sandboxRun struct {
	RunID           string
	StagePath       string
	EntryPath       string
	Executable      string
	TargetPort      int
	Request         routelab.ValidatedRequest
	Routes          []routelab.RouteDefinition
	AccessLogPath   string
	ErrorLogPath    string
	PIDPath         string
	TestToken       string
	OwnerNonce      string
	CandidateDigest config.Digest
}

func defaultRouteLabOptions() routeLabOptions {
	return routeLabOptions{
		NginxRoot: defaultConfigNginxRoot, WorkspaceRoot: defaultConfigWorkspaceRoot,
		StageRoot: "/var/lib/nginx-uix/route-lab", Entry: "nginx.conf",
		Limits: config.DefaultLimits(), NginxExecutable: nginxExecutable,
		Executor: executeCommand, TokenSource: rand.Reader,
		ReservePorts: reserveRouteLabPorts, RunSandbox: runRouteLabSandbox,
	}
}

func newRouteLabService(options routeLabOptions) (*Service, error) {
	options = normalizedRouteLabOptions(options)
	if err := validateRouteLabOptions(options); err != nil {
		return nil, err
	}
	service := newServiceWithExecutor(options.Executor)
	service.routeLab = options
	service.routeLabLock = make(chan struct{}, 2)
	return service, nil
}

func normalizedRouteLabOptions(options routeLabOptions) routeLabOptions {
	defaults := defaultRouteLabOptions()
	if options.Entry == "" {
		options.Entry = defaults.Entry
	}
	if options.Limits == (config.Limits{}) {
		options.Limits = defaults.Limits
	}
	if options.NginxExecutable == "" {
		options.NginxExecutable = defaults.NginxExecutable
	}
	if options.Executor == nil {
		options.Executor = defaults.Executor
	}
	if options.TokenSource == nil {
		options.TokenSource = defaults.TokenSource
	}
	if options.ReservePorts == nil {
		options.ReservePorts = defaults.ReservePorts
	}
	if options.RunSandbox == nil {
		options.RunSandbox = defaults.RunSandbox
	}
	return options
}

func validateRouteLabOptions(options routeLabOptions) error {
	if options.Executor == nil || options.TokenSource == nil || options.ReservePorts == nil || options.RunSandbox == nil ||
		options.NginxExecutable == "" || !filepath.IsAbs(options.NginxExecutable) ||
		filepath.Clean(options.NginxExecutable) != options.NginxExecutable {
		return fmt.Errorf("configure route lab: dependencies are required")
	}
	for _, root := range []string{options.NginxRoot, options.WorkspaceRoot, options.StageRoot} {
		if root == "" || !filepath.IsAbs(root) || filepath.Clean(root) != root {
			return fmt.Errorf("configure route lab root: %w", config.ErrPathInvalid)
		}
		information, err := os.Lstat(root)
		if err != nil || information.Mode()&fs.ModeSymlink != 0 || !information.IsDir() ||
			information.Mode().Perm()&0o077 != 0 {
			return errors.Join(fmt.Errorf("configure route lab root: %w", config.ErrPathInvalid), err)
		}
	}
	if options.NginxRoot == options.WorkspaceRoot || options.NginxRoot == options.StageRoot ||
		options.WorkspaceRoot == options.StageRoot {
		return fmt.Errorf("configure route lab roots: %w", config.ErrPathInvalid)
	}
	if _, err := config.ParseRelativePath(string(options.Entry), options.Limits); err != nil {
		return fmt.Errorf("configure route lab entry: %w", err)
	}
	return nil
}

// ExecuteRouteTest materializes, instruments, executes and removes one fixed-root sandbox.
func (s *Service) ExecuteRouteTest(
	ctx context.Context,
	request routelab.AgentRequest,
) (result routelab.AgentResult, returnErr error) {
	if ctx == nil || s == nil {
		return result, errors.New("execute route test: service is unavailable")
	}
	if !validRouteRunID(request.RunID) {
		return result, fmt.Errorf("validate route run id: %w", routelab.ErrInvalidRequest)
	}
	if _, err := config.ParseWorkspaceID(string(request.WorkspaceID)); err != nil ||
		request.ProductionDigest == (config.Digest{}) || request.DraftDigest == (config.Digest{}) ||
		!validRouteRequestID(request.RequestID) {
		return result, errors.Join(fmt.Errorf("validate route agent request: %w", routelab.ErrInvalidRequest), err)
	}
	validated, err := routelab.ValidateAgentRequest(request.Request)
	if err != nil {
		return result, err
	}
	request.Request = validated

	select {
	case s.routeLabLock <- struct{}{}:
		defer func() { <-s.routeLabLock }()
	case <-ctx.Done():
		return result, fmt.Errorf("wait for route lab slot: %w", ctx.Err())
	}
	options := normalizedRouteLabOptions(s.routeLab)
	if err := validateRouteLabOptions(options); err != nil {
		return result, err
	}
	operationCtx, cancel := context.WithTimeout(ctx, routeLabExecutionTimeout)
	defer cancel()

	productionRoot, err := config.OpenScopedRoot(options.NginxRoot)
	if err != nil {
		return result, fmt.Errorf("open route production root: %w", err)
	}
	productionInventory, inventoryErr := config.BuildInventory(operationCtx, productionRoot, config.SnapshotOptions{
		Entry: options.Entry, Limits: options.Limits, Policy: config.NewPolicy(),
		FileMode: 0o400, DirectoryMode: 0o700,
	})
	if inventoryErr != nil || productionInventory.Digest != request.ProductionDigest {
		return result, errors.Join(
			fmt.Errorf("verify route production digest: %w", config.ErrSnapshotChanged),
			inventoryErr,
			productionRoot.Close(),
		)
	}

	workspacePath := filepath.Join(options.WorkspaceRoot, string(request.WorkspaceID))
	workspaceRoot, err := config.OpenScopedRoot(workspacePath)
	if err != nil {
		return result, errors.Join(fmt.Errorf("open route workspace: %w", err), productionRoot.Close())
	}
	state, stateErr := config.ReadControlState(operationCtx, workspaceRoot)
	manifest, manifestErr := config.ReadControlManifest(operationCtx, workspaceRoot, options.Limits)
	workspaceCloseErr := workspaceRoot.Close()
	if stateErr != nil || manifestErr != nil || workspaceCloseErr != nil {
		return result, errors.Join(
			fmt.Errorf("read route workspace control: %w", config.ErrConflict),
			stateErr,
			manifestErr,
			workspaceCloseErr,
			productionRoot.Close(),
		)
	}
	if state.WorkspaceID != request.WorkspaceID || state.State != config.StateReady ||
		manifest.Digest() != request.DraftDigest {
		return result, errors.Join(
			fmt.Errorf("verify route workspace binding: %w", config.ErrConflict),
			productionRoot.Close(),
		)
	}
	if err := verifyCandidateDraft(operationCtx, workspacePath, manifest, request.DraftDigest, options.Limits); err != nil {
		return result, errors.Join(
			fmt.Errorf("verify route draft: %w", err),
			productionRoot.Close(),
		)
	}

	stagePath, err := os.MkdirTemp(options.StageRoot, ".route-"+request.RunID[:8]+"-")
	if err != nil {
		return result, errors.Join(fmt.Errorf("create route stage: %w", err), productionRoot.Close())
	}
	if err := os.Chmod(stagePath, 0o700); err != nil { // #nosec G302 -- the target is an owner-only directory, not a regular file.
		return result, errors.Join(
			fmt.Errorf("protect route stage: %w", err),
			productionRoot.Close(),
			os.RemoveAll(stagePath),
		)
	}
	stageRemovalSafe := true
	defer func() {
		if !stageRemovalSafe {
			returnErr = errors.Join(returnErr, fmt.Errorf("%w: retain route stage for startup reconciliation", routelab.ErrCleanupFailed))
			return
		}
		if err := os.RemoveAll(stagePath); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("%w: remove route stage: %w", routelab.ErrCleanupFailed, err))
			return
		}
		result.Cleanup.StageRemoved = true
	}()

	if err := copyCandidateProduction(operationCtx, productionRoot, stagePath, options.Limits); err != nil {
		return result, errors.Join(fmt.Errorf("copy route candidate: %w", err), productionRoot.Close())
	}
	if err := productionRoot.Close(); err != nil {
		return result, fmt.Errorf("close route production root: %w", err)
	}
	if err := overlayCandidateDraft(
		operationCtx,
		stagePath,
		workspacePath,
		productionInventory.Manifest,
		manifest,
		options.Limits,
	); err != nil {
		return result, fmt.Errorf("overlay route draft: %w", err)
	}
	candidateDigest, err := digestCandidateTree(operationCtx, stagePath, options.Limits)
	if err != nil {
		return result, fmt.Errorf("digest route candidate: %w", err)
	}
	result.CandidateDigest = candidateDigest
	ownerNonce, err := routeRandomHex(options.TokenSource)
	if err != nil {
		return result, err
	}
	if err := writeRouteOwnerMarker(stagePath, routeOwnerMarker{
		Version: routeOwnerMarkerVersion, RunID: request.RunID, Nonce: ownerNonce,
		CandidateDigest: candidateDigest.String(),
	}); err != nil {
		return result, fmt.Errorf("write route stage ownership: %w", err)
	}
	if err := isolateCandidateIncludes(operationCtx, stagePath, options.NginxRoot, manifest, options.Limits); err != nil {
		return result, fmt.Errorf("isolate route includes: %w", err)
	}
	if err := isolateRouteFilePaths(operationCtx, stagePath, options.NginxRoot, manifest, options.Limits); err != nil {
		return result, fmt.Errorf("isolate route file paths: %w", err)
	}
	if err := relativizeRouteIncludes(operationCtx, stagePath, manifest, options.Limits); err != nil {
		return result, fmt.Errorf("relativize route includes: %w", err)
	}
	if err := prepareRouteStageDirectories(stagePath); err != nil {
		return result, err
	}

	project, err := buildRouteProject(operationCtx, stagePath, options.Entry, options.Limits)
	if err != nil {
		return result, err
	}
	if routelab.ProjectMayContactUpstream(project) != validated.UpstreamSideEffect {
		return result, fmt.Errorf("verify route upstream confirmation: %w", routelab.ErrInvalidRequest)
	}
	keys, err := routelab.ListenerKeys(project)
	if err != nil {
		return result, err
	}
	ports, releasePorts, err := options.ReservePorts(keys)
	if err != nil {
		return result, fmt.Errorf("reserve route listener ports: %w", err)
	}
	portsReleased := false
	defer func() {
		if portsReleased {
			return
		}
		if err := releasePorts(); err != nil {
			returnErr = errors.Join(returnErr, fmt.Errorf("%w: release route listener ports: %w", routelab.ErrCleanupFailed, err))
		}
	}()
	runToken, err := routeRandomHex(options.TokenSource)
	if err != nil {
		return result, err
	}
	instrumentation, err := routelab.Instrument(project, routelab.InstrumentOptions{
		Prefix: stagePath, RunToken: runToken, Request: validated.StaticRequest, ListenerPorts: ports,
	})
	if err != nil {
		return result, err
	}
	if err := writeRouteInstrumentation(operationCtx, stagePath, instrumentation.Files, options.Limits); err != nil {
		return result, err
	}
	result.Routes = slices.Clone(instrumentation.Routes)

	validation, validationErr := options.Executor(operationCtx, commandSpec{
		executable: options.NginxExecutable,
		arguments: []string{
			"-t",
			"-p",
			stagePath + string(filepath.Separator),
			"-c",
			filepath.Join(stagePath, filepath.FromSlash(string(options.Entry))),
		},
		timeout: routeLabValidationTimeout, maxOutputBytes: routeLabDiagnosticLimit,
		allowedExitCodes: map[int]struct{}{0: {}},
	})
	if validationErr != nil {
		var exitErr *commandExitError
		if errors.As(validationErr, &exitErr) {
			result.Diagnostics = routeDiagnostics(validation.stderr, stagePath, options.Limits)
			return result, fmt.Errorf("validate route candidate: %w", routelab.ErrCandidateInvalid)
		}
		return result, fmt.Errorf("validate route candidate: %w", validationErr)
	}
	if err := releasePorts(); err != nil {
		return result, fmt.Errorf("%w: release route listener reservations: %w", routelab.ErrCleanupFailed, err)
	}
	portsReleased = true

	testToken, err := routeRandomHex(options.TokenSource)
	if err != nil {
		return result, err
	}
	stageRemovalSafe = false
	response, evidence, cleanup, runErr := options.RunSandbox(operationCtx, sandboxRun{
		RunID:           request.RunID,
		StagePath:       stagePath,
		EntryPath:       filepath.Join(stagePath, filepath.FromSlash(string(options.Entry))),
		Executable:      options.NginxExecutable,
		TargetPort:      instrumentation.TargetPort,
		Request:         validated,
		Routes:          slices.Clone(instrumentation.Routes),
		AccessLogPath:   instrumentation.AccessLogPath,
		ErrorLogPath:    instrumentation.ErrorLogPath,
		PIDPath:         instrumentation.PIDPath,
		TestToken:       testToken,
		OwnerNonce:      ownerNonce,
		CandidateDigest: candidateDigest,
	})
	result.Response = response
	result.Evidence = evidence
	result.Cleanup = cleanup
	stageRemovalSafe = cleanup.MasterReaped
	if runErr != nil {
		return result, runErr
	}
	if err := validateRouteEvidence(result); err != nil {
		return result, err
	}
	return result, nil
}

func prepareRouteStageDirectories(stagePath string) error {
	for _, relative := range []string{
		"logs", "temp", "temp/client", "temp/proxy", "temp/fastcgi", "temp/uwsgi", "temp/scgi",
	} {
		directory := filepath.Join(stagePath, filepath.FromSlash(relative))
		if err := os.MkdirAll(directory, 0o700); err != nil {
			return fmt.Errorf("create route stage directory: %w", err)
		}
		if err := os.Chmod(directory, 0o700); err != nil { // #nosec G302 -- Route Lab runtime directories must be owner-only.
			return fmt.Errorf("protect route stage directory: %w", err)
		}
	}
	return nil
}

func buildRouteProject(
	ctx context.Context,
	stagePath string,
	entry config.RelativePath,
	limits config.Limits,
) (_ *nginxast.Project, returnErr error) {
	root, err := config.OpenScopedRoot(stagePath)
	if err != nil {
		return nil, fmt.Errorf("open route project root: %w", err)
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	inventory, err := config.BuildInventory(ctx, root, config.SnapshotOptions{
		Entry: entry, Limits: limits, Policy: config.NewPolicy(),
		FileMode: 0o400, DirectoryMode: 0o700,
	})
	if err != nil {
		return nil, fmt.Errorf("inventory route project: %w", err)
	}
	files := make([]nginxast.SourceFile, 0)
	for _, manifestEntry := range inventory.Manifest.Entries {
		if manifestEntry.Class != config.EntryManagedText || manifestEntry.Type != config.EntryRegular {
			continue
		}
		content, information, err := root.ReadRegular(ctx, manifestEntry.Path, limits.MaxFileBytes)
		if err != nil || !information.Mode().IsRegular() {
			return nil, errors.Join(fmt.Errorf("read route project source: %w", config.ErrConflict), err)
		}
		files = append(files, nginxast.SourceFile{Path: string(manifestEntry.Path), Source: string(content)})
	}
	edges := make([]nginxast.IncludeEdge, 0, len(inventory.Manifest.Dependencies))
	for _, dependency := range inventory.Manifest.Dependencies {
		status, ok := routeIncludeStatus(dependency.Status)
		if !ok {
			return nil, fmt.Errorf("build route project: invalid include status")
		}
		edges = append(edges, nginxast.IncludeEdge{
			Source: string(dependency.Source), Line: dependency.Line, Column: dependency.Column,
			Target: string(dependency.Target), Status: status,
		})
	}
	project, err := nginxast.BuildProject(files, edges, nginxast.DefaultProjectLimits())
	if errors.Is(err, nginxast.ErrLimitExceeded) {
		return nil, routelab.ErrLimitExceeded
	}
	if err != nil {
		return nil, fmt.Errorf("build route project: %w", err)
	}
	if !project.Complete {
		return nil, routelab.ErrProjectIncomplete
	}
	return project, nil
}

func relativizeRouteIncludes(
	ctx context.Context,
	stagePath string,
	manifest config.Manifest,
	limits config.Limits,
) (returnErr error) {
	root, err := config.OpenScopedRoot(stagePath)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	for _, entry := range manifest.Entries {
		if entry.Class != config.EntryManagedText || entry.Type != config.EntryRegular {
			continue
		}
		contents, information, err := root.ReadRegular(ctx, entry.Path, limits.MaxFileBytes)
		if err != nil {
			return err
		}
		directives, err := config.ScanDirectives(contents, limits)
		if err != nil {
			return err
		}
		rewritten := string(contents)
		for _, directive := range directives {
			if directive.Name != "include" || len(directive.Arguments) != 1 {
				continue
			}
			argument := filepath.Clean(directive.Arguments[0])
			if !filepath.IsAbs(argument) ||
				(argument != stagePath && !strings.HasPrefix(argument, stagePath+string(filepath.Separator))) {
				continue
			}
			relative, err := filepath.Rel(stagePath, argument)
			if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			rewritten = strings.ReplaceAll(rewritten, directive.Arguments[0], filepath.ToSlash(relative))
		}
		if rewritten != string(contents) {
			if err := root.AtomicReplace(ctx, entry.Path, []byte(rewritten), information.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func isolateRouteFilePaths(
	ctx context.Context,
	stagePath string,
	productionRoot string,
	manifest config.Manifest,
	limits config.Limits,
) (returnErr error) {
	root, err := config.OpenScopedRoot(stagePath)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	for _, entry := range manifest.Entries {
		if entry.Class != config.EntryManagedText || entry.Type != config.EntryRegular {
			continue
		}
		contents, information, err := root.ReadRegular(ctx, entry.Path, limits.MaxFileBytes)
		if err != nil {
			return err
		}
		directives, err := config.ScanDirectives(contents, limits)
		if err != nil {
			return err
		}
		rewritten := string(contents)
		for _, directive := range directives {
			if !routeSandboxFileDirective(directive.Name) || len(directive.Arguments) != 1 ||
				strings.Contains(directive.Arguments[0], "$") {
				continue
			}
			argument := filepath.Clean(directive.Arguments[0])
			if !filepath.IsAbs(argument) ||
				(argument != productionRoot && !strings.HasPrefix(argument, productionRoot+string(filepath.Separator))) {
				continue
			}
			relative, err := filepath.Rel(productionRoot, argument)
			if err != nil || relative == "." || relative == ".." || strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
				return config.ErrPathInvalid
			}
			replacement := filepath.Join(stagePath, relative)
			rewritten = strings.ReplaceAll(rewritten, directive.Arguments[0], replacement)
		}
		if rewritten != string(contents) {
			if err := root.AtomicReplace(ctx, entry.Path, []byte(rewritten), information.Mode().Perm()); err != nil {
				return err
			}
		}
	}
	return nil
}

func routeSandboxFileDirective(name string) bool {
	switch name {
	case "ssl_certificate", "ssl_certificate_key", "ssl_trusted_certificate", "ssl_client_certificate",
		"ssl_crl", "ssl_dhparam", "ssl_password_file", "auth_basic_user_file", "root", "alias":
		return true
	default:
		return false
	}
}

func routeIncludeStatus(status config.DependencyStatus) (nginxast.IncludeStatus, bool) {
	switch status {
	case config.DependencyResolved:
		return nginxast.IncludeResolved, true
	case config.DependencyMissing:
		return nginxast.IncludeMissing, true
	case config.DependencyExternal:
		return nginxast.IncludeExternal, true
	case config.DependencyUnresolved:
		return nginxast.IncludeUnresolved, true
	case config.DependencySymlink:
		return nginxast.IncludeSymlink, true
	case config.DependencySpecial:
		return nginxast.IncludeSpecial, true
	case config.DependencyCycle:
		return nginxast.IncludeCycle, true
	default:
		return "", false
	}
}

func writeRouteInstrumentation(
	ctx context.Context,
	stagePath string,
	files map[string]string,
	limits config.Limits,
) (returnErr error) {
	root, err := config.OpenScopedRoot(stagePath)
	if err != nil {
		return err
	}
	defer func() {
		returnErr = errors.Join(returnErr, root.Close())
	}()
	paths := make([]string, 0, len(files))
	for sourcePath := range files {
		paths = append(paths, sourcePath)
	}
	slices.Sort(paths)
	for _, sourcePath := range paths {
		path, err := config.ParseRelativePath(sourcePath, limits)
		if err != nil {
			return err
		}
		content := []byte(files[sourcePath])
		if int64(len(content)) > limits.MaxFileBytes {
			return routelab.ErrLimitExceeded
		}
		_, information, err := root.ReadRegular(ctx, path, routeLabFileLimit)
		if err != nil || !information.Mode().IsRegular() {
			return errors.Join(config.ErrConflict, err)
		}
		if err := root.AtomicReplace(ctx, path, content, information.Mode().Perm()); err != nil {
			return fmt.Errorf("write route instrumentation: %w", err)
		}
	}
	return nil
}

func routeDiagnostics(stderr []byte, stagePath string, limits config.Limits) []routelab.AgentDiagnostic {
	diagnostics := parseCandidateDiagnostics(stderr, stagePath, limits)
	result := make([]routelab.AgentDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, routelab.AgentDiagnostic{
			Code: diagnostic.Code, Path: string(diagnostic.Path), Line: diagnostic.Line, Summary: diagnostic.Summary,
		})
	}
	if len(result) == 0 {
		result = append(result, routelab.AgentDiagnostic{
			Code: "nginx_config_invalid", Summary: "Nginx rejected the isolated route candidate",
		})
	}
	return result
}

func validateRouteEvidence(result routelab.AgentResult) error {
	if result.Response.StatusCode < 100 || result.Response.StatusCode > 599 ||
		result.Evidence.StatusCode != result.Response.StatusCode || result.Evidence.FinalURI == "" {
		return routelab.ErrEvidenceIncomplete
	}
	kinds := make(map[string]routelab.RouteKind, len(result.Routes))
	for _, route := range result.Routes {
		kinds[route.RouteID] = route.Kind
	}
	if kinds[result.Evidence.ServerRouteID] != routelab.RouteServer {
		return routelab.ErrEvidenceIncomplete
	}
	kind := kinds[result.Evidence.RouteID]
	if kind != routelab.RouteServer && kind != routelab.RouteLocation {
		return routelab.ErrEvidenceIncomplete
	}
	return nil
}

func routeRandomHex(source io.Reader) (string, error) {
	var raw [16]byte
	if _, err := io.ReadFull(source, raw[:]); err != nil {
		return "", fmt.Errorf("generate route token: %w", err)
	}
	return hex.EncodeToString(raw[:]), nil
}

func validRouteRunID(value string) bool {
	if len(value) != 32 {
		return false
	}
	for _, character := range value {
		if character >= '0' && character <= '9' || character >= 'a' && character <= 'f' {
			continue
		}
		return false
	}
	return true
}

func validRouteRequestID(value string) bool {
	if value == "" || len(value) > 128 || strings.TrimSpace(value) != value {
		return false
	}
	for _, character := range value {
		if unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func reserveRouteLabPorts(keys []routelab.ListenerKey) (
	map[routelab.ListenerKey]int,
	func() error,
	error,
) {
	listeners := make([]*net.TCPListener, 0, len(keys))
	ports := make(map[routelab.ListenerKey]int, len(keys))
	cleanup := func() error {
		var cleanupErr error
		for _, listener := range listeners {
			cleanupErr = errors.Join(cleanupErr, listener.Close())
		}
		return cleanupErr
	}
	for _, key := range keys {
		listener, err := net.ListenTCP("tcp4", &net.TCPAddr{IP: net.ParseIP("127.0.0.1")})
		if err != nil {
			return nil, nil, errors.Join(err, cleanup())
		}
		listeners = append(listeners, listener)
		address, ok := listener.Addr().(*net.TCPAddr)
		if !ok || address.Port <= 0 {
			return nil, nil, errors.Join(errors.New("reserve route port: invalid address"), cleanup())
		}
		ports[key] = address.Port
	}
	var once sync.Once
	var cleanupErr error
	return ports, func() error {
		once.Do(func() { cleanupErr = cleanup() })
		return cleanupErr
	}, nil
}
