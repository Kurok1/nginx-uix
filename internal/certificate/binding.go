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
	"slices"
	"sort"
	"strings"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/nginxast"
)

const (
	certificateMaterialRoot = "/var/lib/nginx-uix/certs/certificates"
	bindingDiffMaxWork      = 8_000_000
	maxBindingServers       = 100
)

var (
	// ErrServerNotFound indicates that a persisted server reference no longer exists.
	ErrServerNotFound = errors.New("certificate server not found")
	// ErrServerAmbiguous indicates that a source server cannot be uniquely and safely edited.
	ErrServerAmbiguous = errors.New("certificate server ambiguous")
	// ErrBindingConflict indicates unsupported or foreign direct TLS syntax.
	ErrBindingConflict = errors.New("certificate binding conflict")
)

// ServerRef is a secret-free, source-derived stable server identity.
type ServerRef struct {
	Path        string   `json:"path"`
	StartOffset int      `json:"start_offset"`
	ServerNames []string `json:"server_names"`
	Listeners   []string `json:"listeners"`
	Fingerprint string   `json:"fingerprint"`
}

// ServerCandidate is one uniquely addressable HTTP server block.
type ServerCandidate struct {
	Ref            ServerRef `json:"ref"`
	StartLine      int       `json:"start_line"`
	StartColumn    int       `json:"start_column"`
	TLSEnabled     bool      `json:"tls_enabled"`
	Editable       bool      `json:"editable"`
	ReadOnlyReason string    `json:"read_only_reason,omitempty"`
}

// BindingFileChange is one bounded source-local patch. Before and After stay internal.
type BindingFileChange struct {
	Path         string `json:"path"`
	Patch        string `json:"patch"`
	AddedLines   int    `json:"added_lines"`
	RemovedLines int    `json:"removed_lines"`
	Before       string `json:"-"`
	After        string `json:"-"`
}

// BindingChangePlan is a deterministic lossless set of server-local source changes.
type BindingChangePlan struct {
	Mode       string                `json:"mode"`
	ServerRefs []ServerRef           `json:"server_refs"`
	Files      []BindingFileChange   `json:"files"`
	Edits      []nginxast.SourceEdit `json:"-"`
}

// CertificateFullchainPath derives the fixed immutable full-chain path.
func CertificateFullchainPath(certificateID CertificateID, versionID VersionID) string {
	return certificateMaterialRoot + "/" + string(certificateID) + "/versions/" + string(versionID) + "/fullchain.pem"
}

// CertificatePrivateKeyPath derives the fixed immutable private-key path.
func CertificatePrivateKeyPath(certificateID CertificateID, versionID VersionID) string {
	return certificateMaterialRoot + "/" + string(certificateID) + "/versions/" + string(versionID) + "/privkey.pem"
}

// BuildServerCandidates derives stable, secret-free HTTP server references.
func BuildServerCandidates(project *nginxast.Project) ([]ServerCandidate, error) {
	if project == nil || !project.Complete {
		return nil, fmt.Errorf("build certificate server candidates: %w", ErrServerAmbiguous)
	}
	candidates := make([]ServerCandidate, 0)
	for _, reference := range project.Nodes {
		block, ok := reference.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "server" || !hasServerPlacement(reference, nginxast.ContextHTTP) {
			continue
		}
		names, listeners, tlsEnabled := serverSummary(project, reference.ID)
		candidate := ServerCandidate{
			Ref: ServerRef{
				Path: reference.Path, StartOffset: block.Span.Start.Offset,
				ServerNames: names, Listeners: listeners,
			},
			StartLine: block.Span.Start.Line, StartColumn: block.Span.Start.Column,
			TLSEnabled: tlsEnabled,
			Editable:   !reference.Ambiguous && reference.Instances == 1 && len(block.Arguments) == 0,
		}
		candidate.Ref.Fingerprint = serverFingerprint(candidate.Ref.Path, names, listeners)
		if !candidate.Editable {
			candidate.ReadOnlyReason = "ambiguous_context"
		}
		candidates = append(candidates, candidate)
	}
	sort.SliceStable(candidates, func(left, right int) bool {
		if candidates[left].Ref.Path == candidates[right].Ref.Path {
			return candidates[left].Ref.StartOffset < candidates[right].Ref.StartOffset
		}
		return candidates[left].Ref.Path < candidates[right].Ref.Path
	})
	for left := range candidates {
		for right := left + 1; right < len(candidates); right++ {
			if candidates[left].Ref.Path == candidates[right].Ref.Path &&
				candidates[left].Ref.Fingerprint == candidates[right].Ref.Fingerprint {
				candidates[left].Editable = false
				candidates[right].Editable = false
				candidates[left].ReadOnlyReason = "duplicate_identity"
				candidates[right].ReadOnlyReason = "duplicate_identity"
			}
		}
	}
	return candidates, nil
}

// ResolveServerRefs re-identifies persisted refs after unrelated source-offset changes.
func ResolveServerRefs(project *nginxast.Project, refs []ServerRef) ([]ServerCandidate, error) {
	if len(refs) == 0 || len(refs) > maxBindingServers {
		return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerNotFound)
	}
	candidates, err := BuildServerCandidates(project)
	if err != nil {
		return nil, err
	}
	resolved := make([]ServerCandidate, 0, len(refs))
	seen := make(map[string]bool, len(refs))
	for _, reference := range refs {
		if !validServerRef(reference) {
			return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerNotFound)
		}
		key := reference.Path + "\x00" + reference.Fingerprint
		if seen[key] {
			return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerAmbiguous)
		}
		seen[key] = true
		matches := make([]ServerCandidate, 0, 1)
		for _, candidate := range candidates {
			if candidate.Ref.Path == reference.Path && candidate.Ref.Fingerprint == reference.Fingerprint {
				matches = append(matches, candidate)
			}
		}
		switch len(matches) {
		case 0:
			return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerNotFound)
		case 1:
			if !matches[0].Editable {
				return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerAmbiguous)
			}
			resolved = append(resolved, matches[0])
		default:
			return nil, fmt.Errorf("resolve certificate server refs: %w", ErrServerAmbiguous)
		}
	}
	return resolved, nil
}

// PlanCertificateBinding creates only direct TLS certificate and listener edits.
func PlanCertificateBinding(
	ctx context.Context,
	project *nginxast.Project,
	refs []ServerRef,
	certificateID CertificateID,
	versionID VersionID,
) (BindingChangePlan, error) {
	if ctx == nil || parseOpaqueID(string(certificateID)) != nil || parseOpaqueID(string(versionID)) != nil {
		return BindingChangePlan{}, fmt.Errorf("plan certificate binding: %w", ErrBindingConflict)
	}
	resolved, err := ResolveServerRefs(project, refs)
	if err != nil {
		return BindingChangePlan{}, fmt.Errorf("plan certificate binding: %w", err)
	}
	edits := make([]nginxast.SourceEdit, 0, len(resolved)*4)
	currentRefs := make([]ServerRef, 0, len(resolved))
	fullchain := CertificateFullchainPath(certificateID, versionID)
	privateKey := CertificatePrivateKeyPath(certificateID, versionID)
	for _, candidate := range resolved {
		serverEdits, planErr := planServerBinding(project, candidate, fullchain, privateKey)
		if planErr != nil {
			return BindingChangePlan{}, fmt.Errorf("plan certificate binding: %w", planErr)
		}
		edits = append(edits, serverEdits...)
		currentRefs = append(currentRefs, candidate.Ref)
	}
	return renderBindingPlan(ctx, project, "bind", currentRefs, edits)
}

// PlanCertificateUnbinding removes only the exact active version's direct cert/key directives.
func PlanCertificateUnbinding(
	ctx context.Context,
	project *nginxast.Project,
	refs []ServerRef,
	certificateID CertificateID,
	versionID VersionID,
) (BindingChangePlan, error) {
	if ctx == nil || parseOpaqueID(string(certificateID)) != nil || parseOpaqueID(string(versionID)) != nil {
		return BindingChangePlan{}, fmt.Errorf("plan certificate unbinding: %w", ErrBindingConflict)
	}
	resolved, err := ResolveServerRefs(project, refs)
	if err != nil {
		return BindingChangePlan{}, fmt.Errorf("plan certificate unbinding: %w", err)
	}
	fullchain := CertificateFullchainPath(certificateID, versionID)
	privateKey := CertificatePrivateKeyPath(certificateID, versionID)
	edits := make([]nginxast.SourceEdit, 0, len(resolved)*2)
	currentRefs := make([]ServerRef, 0, len(resolved))
	for _, candidate := range resolved {
		serverEdits, planErr := planServerUnbinding(project, candidate, fullchain, privateKey)
		if planErr != nil {
			return BindingChangePlan{}, fmt.Errorf("plan certificate unbinding: %w", planErr)
		}
		edits = append(edits, serverEdits...)
		currentRefs = append(currentRefs, candidate.Ref)
	}
	return renderBindingPlan(ctx, project, "unbind", currentRefs, edits)
}

func planServerBinding(
	project *nginxast.Project,
	candidate ServerCandidate,
	fullchain, privateKey string,
) ([]nginxast.SourceEdit, error) {
	reference := findServerReference(project, candidate.Ref)
	block, ok := serverBlock(reference)
	if !ok {
		return nil, ErrServerNotFound
	}
	certificateDirectives := directServerDirectives(project, reference.ID, "ssl_certificate")
	keyDirectives := directServerDirectives(project, reference.ID, "ssl_certificate_key")
	if len(certificateDirectives) > 1 || len(keyDirectives) > 1 {
		return nil, ErrBindingConflict
	}
	edits := make([]nginxast.SourceEdit, 0, 4)
	appendLines := make([]string, 0, 3)
	if len(certificateDirectives) == 1 {
		edit, err := replaceStaticDirective(project, certificateDirectives[0], "ssl_certificate", fullchain)
		if err != nil {
			return nil, err
		}
		if edit != nil {
			edits = append(edits, *edit)
		}
	} else {
		appendLines = append(appendLines, "ssl_certificate "+fullchain+";")
	}
	if len(keyDirectives) == 1 {
		edit, err := replaceStaticDirective(project, keyDirectives[0], "ssl_certificate_key", privateKey)
		if err != nil {
			return nil, err
		}
		if edit != nil {
			edits = append(edits, *edit)
		}
	} else {
		appendLines = append(appendLines, "ssl_certificate_key "+privateKey+";")
	}

	listenEdit, needsListen := planTLSListen(project, reference.ID)
	if listenEdit != nil {
		edits = append(edits, *listenEdit)
	}
	if needsListen {
		appendLines = append([]string{"listen 443 ssl;"}, appendLines...)
	}
	if len(appendLines) > 0 {
		document := project.Documents[reference.Path].Document
		edit, appendErr := document.AppendToBlock(block, strings.Join(appendLines, "\n"))
		if appendErr != nil {
			return nil, ErrBindingConflict
		}
		edits = append(edits, nginxast.SourceEdit{Path: reference.Path, Edit: edit})
	}
	return edits, nil
}

func planServerUnbinding(
	project *nginxast.Project,
	candidate ServerCandidate,
	fullchain, privateKey string,
) ([]nginxast.SourceEdit, error) {
	reference := findServerReference(project, candidate.Ref)
	if _, ok := serverBlock(reference); !ok {
		return nil, ErrServerNotFound
	}
	certificateDirectives := directServerDirectives(project, reference.ID, "ssl_certificate")
	keyDirectives := directServerDirectives(project, reference.ID, "ssl_certificate_key")
	if len(certificateDirectives) != 1 || len(keyDirectives) != 1 ||
		!directiveHasExactStaticArgument(certificateDirectives[0], fullchain) ||
		!directiveHasExactStaticArgument(keyDirectives[0], privateKey) {
		return nil, ErrBindingConflict
	}
	edits := make([]nginxast.SourceEdit, 0, 2)
	for _, directive := range []*nginxast.NodeRef{certificateDirectives[0], keyDirectives[0]} {
		node, ok := directive.Node.(*nginxast.Directive)
		if !ok {
			return nil, ErrBindingConflict
		}
		document := project.Documents[directive.Path].Document
		span, err := document.StatementDeleteSpan(node)
		if err != nil {
			return nil, ErrBindingConflict
		}
		edits = append(edits, nginxast.SourceEdit{Path: directive.Path, Edit: nginxast.Edit{Span: span}})
	}
	return edits, nil
}

func replaceStaticDirective(
	project *nginxast.Project,
	reference *nginxast.NodeRef,
	name, value string,
) (*nginxast.SourceEdit, error) {
	directive, ok := reference.Node.(*nginxast.Directive)
	if !ok || directive.Name.Value != name || len(directive.Arguments) != 1 ||
		strings.Contains(directive.Arguments[0].Value, "$") {
		return nil, ErrBindingConflict
	}
	document := project.Documents[reference.Path].Document
	if document == nil || strings.Contains(document.Text(directive.Span), "#") {
		return nil, ErrBindingConflict
	}
	replacement := name + " " + value + ";"
	if document.Text(directive.Span) == replacement {
		return nil, nil
	}
	return &nginxast.SourceEdit{
		Path: reference.Path,
		Edit: nginxast.Edit{Span: directive.Span, Replacement: replacement},
	}, nil
}

func planTLSListen(project *nginxast.Project, serverID string) (*nginxast.SourceEdit, bool) {
	listeners := directServerDirectives(project, serverID, "listen")
	for _, reference := range listeners {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || len(directive.Arguments) == 0 {
			continue
		}
		for _, argument := range directive.Arguments {
			if strings.EqualFold(argument.Value, "ssl") {
				return nil, false
			}
		}
	}
	for _, reference := range listeners {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || len(directive.Arguments) == 0 || !listenEndpointIs443(directive.Arguments[0].Value) {
			continue
		}
		position := directive.TerminatorSpan.Start
		return &nginxast.SourceEdit{
			Path: reference.Path,
			Edit: nginxast.Edit{Span: nginxast.Span{Start: position, End: position}, Replacement: " ssl"},
		}, false
	}
	return nil, true
}

func renderBindingPlan(
	ctx context.Context,
	project *nginxast.Project,
	mode string,
	refs []ServerRef,
	edits []nginxast.SourceEdit,
) (BindingChangePlan, error) {
	if err := ctx.Err(); err != nil {
		return BindingChangePlan{}, err
	}
	if len(edits) == 0 {
		return BindingChangePlan{}, fmt.Errorf("render certificate binding plan: %w", ErrBindingConflict)
	}
	rendered, err := project.ApplyEdits(edits)
	if err != nil {
		return BindingChangePlan{}, fmt.Errorf("render certificate binding plan: %w", ErrBindingConflict)
	}
	paths := make([]string, 0, len(rendered))
	for sourcePath := range rendered {
		paths = append(paths, sourcePath)
	}
	sort.Strings(paths)
	files := make([]BindingFileChange, 0, len(paths))
	for _, sourcePath := range paths {
		before := project.Documents[sourcePath].Document.Render()
		after := rendered[sourcePath]
		patch, summary, diffErr := config.UnifiedDiff(
			ctx, config.RelativePath(sourcePath), []byte(before), []byte(after), bindingDiffMaxWork,
		)
		if diffErr != nil || summary.Status == "unchanged" {
			return BindingChangePlan{}, fmt.Errorf("render certificate binding plan: %w", ErrBindingConflict)
		}
		files = append(files, BindingFileChange{
			Path: sourcePath, Patch: patch, AddedLines: summary.AddedLines,
			RemovedLines: summary.RemovedLines, Before: before, After: after,
		})
	}
	return BindingChangePlan{Mode: mode, ServerRefs: slices.Clone(refs), Files: files, Edits: edits}, nil
}

func serverSummary(project *nginxast.Project, serverID string) ([]string, []string, bool) {
	names := make([]string, 0)
	listeners := make([]string, 0)
	tlsEnabled := false
	for _, reference := range directServerChildren(project, serverID) {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok {
			continue
		}
		switch directive.Name.Value {
		case "server_name":
			for _, argument := range directive.Arguments {
				value := argument.Value
				if !strings.HasPrefix(value, "~") {
					value = strings.ToLower(value)
				}
				names = append(names, value)
			}
		case "listen":
			values := make([]string, 0, len(directive.Arguments))
			for _, argument := range directive.Arguments {
				value := strings.ToLower(argument.Value)
				values = append(values, value)
				tlsEnabled = tlsEnabled || value == "ssl"
			}
			if len(values) > 0 {
				listeners = append(listeners, strings.Join(values, " "))
			}
		}
	}
	names = sortedUnique(names)
	listeners = sortedUnique(listeners)
	return names, listeners, tlsEnabled
}

func directServerChildren(project *nginxast.Project, serverID string) []*nginxast.NodeRef {
	children := make([]*nginxast.NodeRef, 0)
	for _, reference := range project.Nodes {
		if hasParentPlacement(reference, serverID, nginxast.ContextServer) {
			children = append(children, reference)
		}
	}
	return children
}

func directServerDirectives(project *nginxast.Project, serverID, name string) []*nginxast.NodeRef {
	references := make([]*nginxast.NodeRef, 0)
	for _, reference := range directServerChildren(project, serverID) {
		if directive, ok := reference.Node.(*nginxast.Directive); ok && directive.Name.Value == name {
			references = append(references, reference)
		}
	}
	return references
}

func hasServerPlacement(reference *nginxast.NodeRef, context nginxast.ContextKind) bool {
	for _, placement := range reference.Placements {
		if placement.Context == context {
			return true
		}
	}
	return false
}

func hasParentPlacement(reference *nginxast.NodeRef, parentID string, context nginxast.ContextKind) bool {
	for _, placement := range reference.Placements {
		if placement.ParentID == parentID && placement.Context == context {
			return true
		}
	}
	return false
}

func findServerReference(project *nginxast.Project, reference ServerRef) *nginxast.NodeRef {
	for _, candidate := range project.Nodes {
		block, ok := candidate.Node.(*nginxast.Block)
		if !ok || block.Name.Value != "server" || candidate.Path != reference.Path ||
			!hasServerPlacement(candidate, nginxast.ContextHTTP) {
			continue
		}
		names, listeners, _ := serverSummary(project, candidate.ID)
		if serverFingerprint(candidate.Path, names, listeners) == reference.Fingerprint {
			return candidate
		}
	}
	return nil
}

func serverBlock(reference *nginxast.NodeRef) (*nginxast.Block, bool) {
	if reference == nil || reference.Ambiguous || reference.Instances != 1 {
		return nil, false
	}
	block, ok := reference.Node.(*nginxast.Block)
	return block, ok && block.Name.Value == "server" && len(block.Arguments) == 0
}

func serverFingerprint(sourcePath string, names, listeners []string) string {
	digest := sha256.New()
	_, _ = digest.Write([]byte("nginx-uix-certificate-server-v1\x00"))
	_, _ = digest.Write([]byte(sourcePath))
	_, _ = digest.Write([]byte("\x00" + strings.Join(names, "\x00") + "\x00"))
	_, _ = digest.Write([]byte(strings.Join(listeners, "\x00")))
	return hex.EncodeToString(digest.Sum(nil))
}

func validServerRef(reference ServerRef) bool {
	if reference.Path == "" || strings.HasPrefix(reference.Path, "/") || strings.Contains(reference.Path, "\\") ||
		reference.StartOffset < 0 || !validLowerHex(reference.Fingerprint, 64) ||
		len(reference.ServerNames) > 100 || len(reference.Listeners) > 100 {
		return false
	}
	names := sortedUnique(slices.Clone(reference.ServerNames))
	listeners := sortedUnique(slices.Clone(reference.Listeners))
	return slices.Equal(names, reference.ServerNames) && slices.Equal(listeners, reference.Listeners) &&
		serverFingerprint(reference.Path, names, listeners) == reference.Fingerprint
}

func sortedUnique(values []string) []string {
	filtered := values[:0]
	for _, value := range values {
		if value != "" && len(value) <= 512 && !strings.ContainsRune(value, '\x00') {
			filtered = append(filtered, value)
		}
	}
	sort.Strings(filtered)
	return slices.Compact(filtered)
}

func directiveHasExactStaticArgument(reference *nginxast.NodeRef, value string) bool {
	directive, ok := reference.Node.(*nginxast.Directive)
	return ok && len(directive.Arguments) == 1 && !strings.Contains(directive.Arguments[0].Value, "$") &&
		directive.Arguments[0].Value == value
}

func listenEndpointIs443(value string) bool {
	lower := strings.ToLower(value)
	if strings.Contains(lower, "$") || strings.HasPrefix(lower, "unix:") {
		return false
	}
	return lower == "443" || strings.HasSuffix(lower, ":443")
}
