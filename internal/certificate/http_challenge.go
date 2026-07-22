/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.5.0
 */

package certificate

import (
	"context"
	"fmt"
	"slices"
	"sort"
	"strings"

	"github.com/kuroky/nginx-uix/internal/nginxast"
)

const (
	maxHTTPChallengeValue = 1024
)

// HTTPChallengeConfigPath returns the fixed managed-root-relative fragment path for one task.
func HTTPChallengeConfigPath(taskID TaskID) string {
	return "nginx-uix-acme-" + string(taskID) + ".conf"
}

// RenderHTTPChallengeFragment renders only validated exact ACME challenge locations.
func RenderHTTPChallengeFragment(responses []HTTPChallengeResponse) (string, error) {
	if len(responses) == 0 || len(responses) > maxCertificateIdentifiers {
		return "", fmt.Errorf("render HTTP challenge fragment: %w", ErrIdentifierInvalid)
	}
	ordered := slices.Clone(responses)
	sort.Slice(ordered, func(left, right int) bool { return ordered[left].Token < ordered[right].Token })
	var fragment strings.Builder
	previousToken := ""
	for _, response := range ordered {
		identifier, wildcard, err := normalizeDNSIdentifier(response.Identifier)
		if err != nil || wildcard || identifier != response.Identifier ||
			!validACMEBase64URL(response.Token, 256, false) ||
			!validACMEBase64URL(response.KeyAuthorization, maxHTTPChallengeValue, true) ||
			response.Token == previousToken {
			return "", fmt.Errorf("render HTTP challenge fragment: %w", ErrIdentifierInvalid)
		}
		previousToken = response.Token
		fragment.WriteString("location = /.well-known/acme-challenge/")
		fragment.WriteString(response.Token)
		fragment.WriteString(" {\n    default_type text/plain;\n    return 200 \"")
		fragment.WriteString(response.KeyAuthorization)
		fragment.WriteString("\";\n}\n")
	}
	return fragment.String(), nil
}

// PlanHTTPChallengeProvision appends one exact task-owned include to each selected server.
func PlanHTTPChallengeProvision(
	ctx context.Context,
	project *nginxast.Project,
	refs []ServerRef,
	taskID TaskID,
) (BindingChangePlan, error) {
	if ctx == nil || parseOpaqueID(string(taskID)) != nil {
		return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge provision: %w", ErrBindingConflict)
	}
	resolved, err := ResolveServerRefs(project, refs)
	if err != nil {
		return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge provision: %w", err)
	}
	includePath := "/etc/nginx/" + HTTPChallengeConfigPath(taskID)
	edits := make([]nginxast.SourceEdit, 0, len(resolved))
	currentRefs := make([]ServerRef, 0, len(resolved))
	for _, candidate := range resolved {
		reference := findServerReference(project, candidate.Ref)
		block, ok := serverBlock(reference)
		if !ok {
			return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge provision: %w", ErrServerNotFound)
		}
		for _, include := range directServerDirectives(project, reference.ID, "include") {
			if directiveHasExactStaticArgument(include, includePath) {
				return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge provision: %w", ErrBindingConflict)
			}
		}
		document := project.Documents[reference.Path].Document
		edit, appendErr := document.AppendToBlock(block, "include "+includePath+";")
		if appendErr != nil {
			return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge provision: %w", ErrBindingConflict)
		}
		edits = append(edits, nginxast.SourceEdit{Path: reference.Path, Edit: edit})
		currentRefs = append(currentRefs, candidate.Ref)
	}
	return renderBindingPlan(ctx, project, "http_challenge_provision", currentRefs, edits)
}

// PlanHTTPChallengeCleanup removes every exact include for one task from the latest source snapshot.
func PlanHTTPChallengeCleanup(
	ctx context.Context,
	project *nginxast.Project,
	taskID TaskID,
) (BindingChangePlan, error) {
	if ctx == nil || project == nil || parseOpaqueID(string(taskID)) != nil {
		return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge cleanup: %w", ErrBindingConflict)
	}
	includePath := "/etc/nginx/" + HTTPChallengeConfigPath(taskID)
	edits := make([]nginxast.SourceEdit, 0)
	seen := make(map[string]bool)
	for _, reference := range project.Nodes {
		directive, ok := reference.Node.(*nginxast.Directive)
		if !ok || directive.Name.Value != "include" || !hasServerPlacement(reference, nginxast.ContextServer) ||
			!directiveHasExactStaticArgument(reference, includePath) {
			continue
		}
		key := reference.Path + "\x00" + reference.ID
		if seen[key] {
			continue
		}
		seen[key] = true
		document := project.Documents[reference.Path].Document
		span, err := document.StatementDeleteSpan(directive)
		if err != nil {
			return BindingChangePlan{}, fmt.Errorf("plan HTTP challenge cleanup: %w", ErrBindingConflict)
		}
		edits = append(edits, nginxast.SourceEdit{Path: reference.Path, Edit: nginxast.Edit{Span: span}})
	}
	if len(edits) == 0 {
		return BindingChangePlan{Mode: "http_challenge_cleanup"}, nil
	}
	return renderBindingPlan(ctx, project, "http_challenge_cleanup", nil, edits)
}

func validACMEBase64URL(value string, limit int, allowDot bool) bool {
	if value == "" || len(value) > limit {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || character == '-' || character == '_' || allowDot && character == '.' {
			continue
		}
		return false
	}
	return true
}
