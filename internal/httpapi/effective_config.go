/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"net/http"
	"time"

	nginxruntime "github.com/kuroky/nginx-uix/internal/runtime"
)

const effectiveConfigEntryPath = "/etc/nginx/nginx.conf"

type effectiveConfigResponse struct {
	GeneratedAt     time.Time                   `json:"generated_at"`
	NginxVersion    string                      `json:"nginx_version"`
	EntryConfigPath string                      `json:"entry_config_path"`
	DisplayMode     string                      `json:"display_mode"`
	OccurrenceCount int                         `json:"occurrence_count"`
	Occurrences     []effectiveConfigOccurrence `json:"occurrences"`
	RawContent      *string                     `json:"raw_content"`
	Warnings        []string                    `json:"warnings"`
}

type effectiveConfigOccurrence struct {
	ID        string `json:"id"`
	LoadOrder int    `json:"load_order"`
	Path      string `json:"path"`
	Content   string `json:"content"`
}

func effectiveConfig(sessions SessionService, agent Agent) http.HandlerFunc {
	return func(writer http.ResponseWriter, request *http.Request) {
		writer.Header().Set("Cache-Control", "no-store")
		if !authorizeBusinessGET(writer, request, sessions) {
			return
		}
		if agent == nil {
			writeAgentAPIError(writer, requestIDFromContext(request.Context()), errAgentUnavailable)
			return
		}

		configuration, err := agent.EffectiveConfig(request.Context())
		if err != nil {
			writeAgentAPIError(writer, requestIDFromContext(request.Context()), err)
			return
		}
		build, err := agent.BuildInfo(request.Context())
		if err != nil {
			writeAgentAPIError(writer, requestIDFromContext(request.Context()), err)
			return
		}
		writeJSON(writer, http.StatusOK, newEffectiveConfigResponse(time.Now().UTC(), build, configuration))
	}
}

func newEffectiveConfigResponse(
	generatedAt time.Time,
	build nginxruntime.BuildInfo,
	configuration nginxruntime.EffectiveConfig,
) effectiveConfigResponse {
	response := effectiveConfigResponse{
		GeneratedAt: generatedAt, NginxVersion: build.Version, EntryConfigPath: effectiveConfigEntryPath,
		DisplayMode:     string(configuration.DisplayMode),
		OccurrenceCount: len(configuration.Occurrences),
		Occurrences:     make([]effectiveConfigOccurrence, 0, len(configuration.Occurrences)),
		Warnings:        make([]string, 0, len(configuration.Warnings)),
	}
	for _, occurrence := range configuration.Occurrences {
		response.Occurrences = append(response.Occurrences, effectiveConfigOccurrence{
			ID: occurrence.ID, LoadOrder: occurrence.LoadOrder, Path: occurrence.Path, Content: occurrence.Content,
		})
	}
	for _, warning := range configuration.Warnings {
		response.Warnings = append(response.Warnings, string(warning))
	}
	if configuration.DisplayMode == nginxruntime.EffectiveConfigDisplayModeRaw {
		response.RawContent = &configuration.RawContent
	}
	return response
}
