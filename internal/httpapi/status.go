/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"net/http"
	"slices"
	"sort"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const publicDiagnosticLimit = 256 * 1024

// Agent is the fixed read-only local Agent surface consumed by public HTTP.
type Agent interface {
	Health(ctx context.Context) error
	Status(ctx context.Context) (nginxruntime.Status, error)
	BuildInfo(ctx context.Context) (nginxruntime.BuildInfo, error)
	StartupValidation(ctx context.Context) (nginxruntime.StartupState, error)
	EffectiveConfig(ctx context.Context) (nginxruntime.EffectiveConfig, error)
}

type systemStatusResponse struct {
	SampledAt         time.Time                      `json:"sampled_at"`
	Components        systemStatusComponents         `json:"components"`
	Master            *systemStatusProcess           `json:"master"`
	Workers           []systemStatusProcess          `json:"workers"`
	Build             *systemStatusBuild             `json:"build"`
	StartupValidation *systemStatusStartupValidation `json:"startup_validation"`
	Recovery          *systemStatusRecovery          `json:"recovery"`
	Issues            []string                       `json:"issues"`
}

type systemStatusComponents struct {
	UI    string `json:"ui"`
	Agent string `json:"agent"`
	Nginx string `json:"nginx"`
}

type systemStatusProcess struct {
	PID       int       `json:"pid"`
	Role      string    `json:"role"`
	StartedAt time.Time `json:"started_at"`
}

type systemStatusBuild struct {
	Version            string   `json:"version"`
	ConfigureArguments []string `json:"configure_arguments"`
}

type systemStatusStartupValidation struct {
	Valid      bool      `json:"valid"`
	CheckedAt  time.Time `json:"checked_at"`
	ExitCode   *int      `json:"exit_code"`
	Diagnostic string    `json:"diagnostic"`
}

type systemStatusRecovery struct {
	Count      int    `json:"count"`
	LastResult string `json:"last_result"`
	Permanent  bool   `json:"permanent"`
}

func systemStatus(sessions SessionService, agent Agent) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if !authorizeBusinessGET(writer, request, sessions) {
			return
		}

		if agent == nil {
			writeJSON(writer, http.StatusOK, unavailableSystemStatus(time.Now().UTC()))
			return
		}
		status, err := agent.Status(request.Context())
		if err != nil {
			writeJSON(writer, http.StatusOK, unavailableSystemStatus(time.Now().UTC()))
			return
		}
		writeJSON(writer, http.StatusOK, newSystemStatusResponse(status))
	}
}

func authorizeBusinessGET(writer http.ResponseWriter, request *http.Request, sessions SessionService) bool {
	requestID := requestIDFromContext(request.Context())
	rawToken, ok := readSessionCookie(request)
	if !ok || sessions == nil {
		writeAPIError(writer, requestID, http.StatusUnauthorized, "unauthenticated", "需要登录", nil)
		return false
	}
	if _, err := sessions.Current(request.Context(), rawToken); err != nil {
		(&sessionHandler{service: sessions}).writeAuthError(writer, requestID, err)
		return false
	}
	return true
}

func unavailableSystemStatus(sampledAt time.Time) systemStatusResponse {
	return systemStatusResponse{
		SampledAt: sampledAt,
		Components: systemStatusComponents{
			UI: "healthy", Agent: "unavailable", Nginx: string(nginxruntime.StateUnknown),
		},
		Workers: make([]systemStatusProcess, 0),
		Issues:  []string{"AGENT_UNAVAILABLE"},
	}
}

func newSystemStatusResponse(status nginxruntime.Status) systemStatusResponse {
	sampledAt := status.SampledAt.UTC()
	if status.SampledAt.IsZero() {
		sampledAt = time.Now().UTC()
	}
	state := publicNginxState(status.State)
	response := systemStatusResponse{
		SampledAt: sampledAt,
		Components: systemStatusComponents{
			UI: "healthy", Agent: "healthy", Nginx: string(state),
		},
		Workers: make([]systemStatusProcess, 0, len(status.Workers)),
		Issues:  slices.Clone(status.Issues),
	}
	if status.Master != nil {
		response.Master = newSystemStatusProcess(*status.Master)
	}
	for _, worker := range status.Workers {
		response.Workers = append(response.Workers, *newSystemStatusProcess(worker))
	}
	sort.SliceStable(response.Workers, func(left, right int) bool {
		return response.Workers[left].PID < response.Workers[right].PID
	})
	if status.Build != nil {
		response.Build = &systemStatusBuild{
			Version: status.Build.Version, ConfigureArguments: slices.Clone(status.Build.ConfigureArguments),
		}
	}
	if status.StartupValidation != nil {
		response.StartupValidation = &systemStatusStartupValidation{
			Valid: status.StartupValidation.Valid, CheckedAt: status.StartupValidation.CheckedAt.UTC(),
			ExitCode:   cloneInt(status.StartupValidation.ExitCode),
			Diagnostic: sanitizePublicDiagnostic(status.StartupValidation.Diagnostic),
		}
	}
	if status.Recovery != nil {
		response.Recovery = &systemStatusRecovery{
			Count: status.Recovery.Count, LastResult: string(status.Recovery.LastResult), Permanent: status.Recovery.Permanent,
		}
	}
	sort.Strings(response.Issues)
	response.Issues = slices.Compact(response.Issues)
	if response.Issues == nil {
		response.Issues = make([]string, 0)
	}
	return response
}

func publicNginxState(state nginxruntime.State) nginxruntime.State {
	switch state {
	case nginxruntime.StateRunning, nginxruntime.StateDegraded, nginxruntime.StateStopped, nginxruntime.StateUnknown:
		return state
	default:
		return nginxruntime.StateUnknown
	}
}

func newSystemStatusProcess(process nginxruntime.NginxProcess) *systemStatusProcess {
	return &systemStatusProcess{
		PID: process.PID, Role: string(process.Role), StartedAt: process.StartedAt.UTC(),
	}
}

func cloneInt(value *int) *int {
	if value == nil {
		return nil
	}
	clone := *value
	return &clone
}

func sanitizePublicDiagnostic(value string) string {
	valid := strings.ToValidUTF8(value, "?")
	var sanitized strings.Builder
	sanitized.Grow(min(len(valid), publicDiagnosticLimit))
	for index, character := range valid {
		switch {
		case character == '\r':
			if index+1 < len(valid) && valid[index+1] == '\n' {
				continue
			}
			sanitized.WriteByte('?')
		case character == '\n' || character == '\t':
			sanitized.WriteRune(character)
		case unicode.IsControl(character):
			sanitized.WriteByte('?')
		default:
			sanitized.WriteRune(character)
		}
		if sanitized.Len() >= publicDiagnosticLimit {
			break
		}
	}
	result := sanitized.String()
	if len(result) <= publicDiagnosticLimit {
		return result
	}
	end := publicDiagnosticLimit
	for end > 0 && !utf8.ValidString(result[:end]) {
		end--
	}
	return result[:end]
}
