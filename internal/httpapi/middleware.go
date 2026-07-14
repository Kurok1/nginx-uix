/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"io"
	"log/slog"
	"net/http"
	"regexp"
	"sync"
	"time"
)

var requestIDPattern = regexp.MustCompile(`^[A-Za-z0-9._-]{1,64}$`)

type requestIDContextKey struct{}

type requestIDGenerator struct {
	reader io.Reader
	mu     sync.Mutex
}

func newRequestIDGenerator(reader io.Reader) *requestIDGenerator {
	if reader == nil {
		reader = rand.Reader
	}
	return &requestIDGenerator{reader: reader}
}

func (g *requestIDGenerator) Generate() (string, error) {
	bytes := make([]byte, 16)
	g.mu.Lock()
	defer g.mu.Unlock()
	if _, err := io.ReadFull(g.reader, bytes); err != nil {
		return "", err
	}
	return hex.EncodeToString(bytes), nil
}

func requestBoundary(next http.Handler, logger *slog.Logger, generator *requestIDGenerator) http.Handler {
	if logger == nil {
		logger = slog.New(slog.DiscardHandler)
	}
	return http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		startedAt := time.Now()
		requestID := request.Header.Get("X-Request-ID")
		if !requestIDPattern.MatchString(requestID) {
			generated, err := generator.Generate()
			if err != nil {
				writeAPIError(writer, "unknown", http.StatusInternalServerError, "internal_error", "服务暂时不可用", nil)
				return
			}
			requestID = generated
		}

		writer.Header().Set("X-Request-ID", requestID)
		writer.Header().Set("Content-Security-Policy", "default-src 'self'; object-src 'none'; base-uri 'self'; frame-ancestors 'none'")
		writer.Header().Set("X-Frame-Options", "DENY")
		writer.Header().Set("X-Content-Type-Options", "nosniff")
		writer.Header().Set("Referrer-Policy", "no-referrer")
		writer.Header().Set("Permissions-Policy", "camera=(), microphone=(), geolocation=()")

		capture := &statusCapture{ResponseWriter: writer, status: http.StatusOK}
		ctx := context.WithValue(request.Context(), requestIDContextKey{}, requestID)
		next.ServeHTTP(capture, request.WithContext(ctx))
		logger.InfoContext(
			ctx,
			"http request completed",
			"component", "httpapi",
			"action", request.Method+" "+request.URL.Path,
			"result", capture.status,
			"duration_ms", time.Since(startedAt).Milliseconds(),
			"request_id", requestID,
		)
	})
}

func requestIDFromContext(ctx context.Context) string {
	requestID, ok := ctx.Value(requestIDContextKey{}).(string)
	if !ok || requestID == "" {
		return "unknown"
	}
	return requestID
}

type statusCapture struct {
	http.ResponseWriter
	status      int
	wroteHeader bool
}

func (c *statusCapture) WriteHeader(status int) {
	if c.wroteHeader {
		return
	}
	c.wroteHeader = true
	c.status = status
	c.ResponseWriter.WriteHeader(status)
}

func (c *statusCapture) Write(payload []byte) (int, error) {
	if !c.wroteHeader {
		c.WriteHeader(http.StatusOK)
	}
	return c.ResponseWriter.Write(payload)
}

func (c *statusCapture) Unwrap() http.ResponseWriter {
	return c.ResponseWriter
}
