/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"encoding/json"
	"io/fs"
	"net/http"
)

// Dependencies contains explicit HTTP boundary dependencies.
type Dependencies struct {
	Assets fs.FS
}

// NewHandler creates the public HTTP surface.
func NewHandler(dependencies Dependencies) http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health/live", live)
	mux.Handle("/", spaFallback(dependencies.Assets))
	return mux
}

func live(writer http.ResponseWriter, _ *http.Request) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(http.StatusOK)
	_ = json.NewEncoder(writer).Encode(struct {
		Status string `json:"status"`
	}{Status: "ok"})
}

func spaFallback(assets fs.FS) http.Handler {
	if assets == nil {
		return http.NotFoundHandler()
	}
	return http.FileServer(http.FS(assets))
}
