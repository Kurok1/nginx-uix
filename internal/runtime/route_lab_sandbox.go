/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package runtime

import (
	"bufio"
	"bytes"
	"context"
	"crypto/sha256"
	"crypto/tls"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"math"
	"mime"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
	"unicode/utf8"

	"github.com/kuroky/nginx-uix/internal/config"
	"github.com/kuroky/nginx-uix/internal/routelab"
	"golang.org/x/sys/unix"
)

const (
	routeLabStartupTimeout     = 5 * time.Second
	routeLabShutdownTimeout    = 3 * time.Second
	routeLabPortCloseTimeout   = 2 * time.Second
	routeLabResponseBodyLimit  = 64 << 10
	routeLabResponseSnippet    = 16 << 10
	routeLabAccessLogLimit     = 1 << 20
	routeLabAccessLogLineLimit = 64 << 10
	routeLabProcessOutputLimit = 512 << 10
)

var safeRouteResponseHeaders = map[string]struct{}{
	"allow": {}, "cache-control": {}, "content-length": {}, "content-type": {}, "etag": {},
	"last-modified": {}, "location": {}, "retry-after": {}, "vary": {},
}

type sandboxProcess struct {
	command *exec.Cmd
	done    chan struct{}
	err     error
	cancel  context.CancelFunc
}

type boundedRouteWriter struct {
	lock      sync.Mutex
	contents  []byte
	limit     int
	truncated bool
}

type routeAccessRecord struct {
	Test           string `json:"test"`
	Server         string `json:"server"`
	Route          string `json:"route"`
	URI            string `json:"uri"`
	Status         string `json:"status"`
	Upstream       string `json:"upstream"`
	UpstreamStatus string `json:"upstream_status"`
	RequestTime    string `json:"request_time"`
}

func runRouteLabSandbox(
	ctx context.Context,
	run sandboxRun,
) (routelab.Response, routelab.RuntimeEvidence, routelab.CleanupEvidence, error) {
	if err := validateSandboxRun(run); err != nil {
		return routelab.Response{}, routelab.RuntimeEvidence{}, routelab.CleanupEvidence{}, err
	}
	process, err := startRouteSandboxProcess(ctx, run)
	if err != nil {
		cleanup := routelab.CleanupEvidence{MasterReaped: process == nil}
		var cleanupErr error
		if process != nil {
			cleanup, cleanupErr = stopRouteSandboxProcess(process, run.TargetPort)
		} else {
			cleanup.PortClosed = waitRoutePortClosed(run.TargetPort, routeLabPortCloseTimeout)
		}
		return routelab.Response{}, routelab.RuntimeEvidence{}, cleanup,
			errors.Join(fmt.Errorf("start route sandbox: %w", routelab.ErrSandboxStart), err, cleanupErr)
	}
	if err := waitRouteSandboxReady(ctx, process, run); err != nil {
		cleanup, cleanupErr := stopRouteSandboxProcess(process, run.TargetPort)
		if ctx.Err() != nil {
			return routelab.Response{}, routelab.RuntimeEvidence{}, cleanup, errors.Join(ctx.Err(), cleanupErr)
		}
		return routelab.Response{}, routelab.RuntimeEvidence{}, cleanup,
			errors.Join(fmt.Errorf("wait for route sandbox: %w", routelab.ErrSandboxStart), cleanupErr)
	}

	response, requestErr := executeRouteRequest(ctx, run)
	cleanup, cleanupErr := stopRouteSandboxProcess(process, run.TargetPort)
	if requestErr != nil || cleanupErr != nil {
		return response, routelab.RuntimeEvidence{}, cleanup, errors.Join(requestErr, cleanupErr)
	}
	evidence, err := parseRouteEvidence(run.AccessLogPath, run.TestToken, run.Routes)
	if err != nil {
		return response, routelab.RuntimeEvidence{}, cleanup, err
	}
	return response, evidence, cleanup, nil
}

func validateSandboxRun(run sandboxRun) error {
	if run.TargetPort <= 0 || run.TargetPort > 65535 || run.TestToken == "" || len(run.Routes) == 0 ||
		!validRouteRunID(run.RunID) || !validLowerHex(run.OwnerNonce, 32) ||
		run.CandidateDigest == (config.Digest{}) ||
		run.StagePath == "" || !filepath.IsAbs(run.StagePath) || filepath.Clean(run.StagePath) != run.StagePath ||
		run.Executable == "" || !filepath.IsAbs(run.Executable) || filepath.Clean(run.Executable) != run.Executable {
		return routelab.ErrInvalidInstrumentation
	}
	for _, path := range []string{run.EntryPath, run.AccessLogPath, run.ErrorLogPath, run.PIDPath} {
		if !routePathWithin(run.StagePath, path) {
			return routelab.ErrInvalidInstrumentation
		}
	}
	return nil
}

func routePathWithin(root, candidate string) bool {
	if candidate == "" || !filepath.IsAbs(candidate) || filepath.Clean(candidate) != candidate {
		return false
	}
	relative, err := filepath.Rel(root, candidate)
	return err == nil && relative != "." && relative != ".." &&
		!strings.HasPrefix(relative, ".."+string(filepath.Separator))
}

func startRouteSandboxProcess(ctx context.Context, run sandboxRun) (*sandboxProcess, error) {
	commandContext, cancel := context.WithCancel(context.WithoutCancel(ctx))
	stdout := &boundedRouteWriter{limit: routeLabProcessOutputLimit}
	stderr := &boundedRouteWriter{limit: routeLabProcessOutputLimit}
	command := exec.CommandContext( // #nosec G204 -- executable and every argument come from trusted fixed-root Agent state.
		commandContext,
		run.Executable,
		"-p",
		run.StagePath+string(filepath.Separator),
		"-c",
		run.EntryPath,
	)
	command.Dir = run.StagePath
	command.Env = []string{"PATH=/usr/sbin:/usr/bin:/sbin:/bin", "TZ=UTC"}
	command.Stdout = stdout
	command.Stderr = stderr
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := command.Start(); err != nil {
		cancel()
		return nil, err
	}
	process := &sandboxProcess{command: command, done: make(chan struct{}), cancel: cancel}
	go func() {
		process.err = command.Wait()
		close(process.done)
	}()
	if err := writeRouteOwnerMarker(run.StagePath, routeOwnerMarker{
		Version: routeOwnerMarkerVersion, RunID: run.RunID, Nonce: run.OwnerNonce,
		CandidateDigest: run.CandidateDigest.String(), MasterPID: command.Process.Pid,
	}); err != nil {
		cleanup, cleanupErr := stopRouteSandboxProcess(process, run.TargetPort)
		_ = cleanup
		return process, errors.Join(err, cleanupErr)
	}
	return process, nil
}

func waitRouteSandboxReady(ctx context.Context, process *sandboxProcess, run sandboxRun) error {
	deadline := time.NewTimer(routeLabStartupTimeout)
	defer deadline.Stop()
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	for {
		connection, err := net.DialTimeout("tcp4", net.JoinHostPort("127.0.0.1", strconv.Itoa(run.TargetPort)), 100*time.Millisecond)
		if err == nil {
			_ = connection.Close()
			pid, pidErr := readRouteSandboxPID(run.PIDPath)
			if pidErr == nil && pid == process.command.Process.Pid {
				return nil
			}
			if pidErr == nil || !errors.Is(pidErr, fs.ErrNotExist) {
				return routelab.ErrSandboxStart
			}
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-process.done:
			return process.err
		case <-deadline.C:
			return routelab.ErrSandboxStart
		case <-ticker.C:
		}
	}
}

func readRouteSandboxPID(path string) (int, error) {
	information, err := os.Lstat(path)
	if err != nil {
		return 0, err
	}
	if information.Mode()&fs.ModeSymlink != 0 || !information.Mode().IsRegular() ||
		information.Size() <= 0 || information.Size() > 32 {
		return 0, config.ErrPathInvalid
	}
	payload, err := os.ReadFile(path) // #nosec G304 -- path is the fixed PID file inside the validated sandbox stage.
	if err != nil {
		return 0, err
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(payload)))
	if err != nil || pid <= 0 {
		return 0, config.ErrPathInvalid
	}
	return pid, nil
}

func stopRouteSandboxProcess(process *sandboxProcess, port int) (routelab.CleanupEvidence, error) {
	cleanup := routelab.CleanupEvidence{}
	if process == nil || process.command == nil || process.command.Process == nil {
		return cleanup, fmt.Errorf("%w: route sandbox process is unavailable", routelab.ErrCleanupFailed)
	}
	defer process.cancel()
	processGroup := -process.command.Process.Pid
	if err := unix.Kill(processGroup, unix.SIGTERM); err != nil && !errors.Is(err, unix.ESRCH) {
		return cleanup, fmt.Errorf("%w: terminate route sandbox", routelab.ErrCleanupFailed)
	}
	masterReaped := waitRouteProcess(process, routeLabShutdownTimeout)
	groupGone := masterReaped && waitRouteProcessGroupGone(process.command.Process.Pid)
	if !masterReaped || !groupGone {
		if err := unix.Kill(processGroup, unix.SIGKILL); err != nil && !errors.Is(err, unix.ESRCH) {
			return cleanup, fmt.Errorf("%w: kill route sandbox", routelab.ErrCleanupFailed)
		}
		if !waitRouteProcess(process, routeLabShutdownTimeout) ||
			!waitRouteProcessGroupGone(process.command.Process.Pid) {
			return cleanup, fmt.Errorf("%w: reap route sandbox process group", routelab.ErrCleanupFailed)
		}
	}
	cleanup.MasterReaped = true
	if waitRoutePortClosed(port, routeLabPortCloseTimeout) {
		cleanup.PortClosed = true
		return cleanup, nil
	}
	_ = unix.Kill(processGroup, unix.SIGKILL)
	if waitRoutePortClosed(port, routeLabPortCloseTimeout) {
		cleanup.PortClosed = true
		return cleanup, nil
	}
	return cleanup, fmt.Errorf("%w: route sandbox listener remains open", routelab.ErrCleanupFailed)
}

func waitRouteProcessGroupGone(processGroup int) bool {
	deadline := time.Now().Add(routeLabShutdownTimeout)
	for {
		err := unix.Kill(-processGroup, 0)
		if errors.Is(err, unix.ESRCH) {
			return true
		}
		if err != nil && !errors.Is(err, unix.EPERM) {
			return false
		}
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func waitRouteProcess(process *sandboxProcess, timeout time.Duration) bool {
	select {
	case <-process.done:
		return true
	default:
	}
	timer := time.NewTimer(timeout)
	defer timer.Stop()
	select {
	case <-process.done:
		return true
	case <-timer.C:
		return false
	}
}

func waitRoutePortClosed(port int, timeout time.Duration) bool {
	deadline := time.Now().Add(timeout)
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	for {
		connection, err := net.DialTimeout("tcp4", address, 50*time.Millisecond)
		if err != nil {
			return true
		}
		_ = connection.Close()
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(25 * time.Millisecond)
	}
}

func executeRouteRequest(ctx context.Context, run sandboxRun) (routelab.Response, error) {
	request := run.Request
	requestContext, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(run.TargetPort))
	requestURI := request.URI
	if request.Query != "" {
		requestURI += "?" + request.Query
	}
	target := string(request.Scheme) + "://" + address + requestURI
	httpRequest, err := http.NewRequestWithContext(requestContext, request.Method, target, bytes.NewReader(request.Body))
	if err != nil {
		return routelab.Response{}, fmt.Errorf("build route request: %w", routelab.ErrInvalidRequest)
	}
	httpRequest.Host = request.Host
	httpRequest.Close = true
	for _, header := range request.Headers {
		httpRequest.Header.Set(header.Name, header.Value)
	}
	httpRequest.Header.Set("X-Nginx-UIX-Test-ID", run.TestToken)

	transport := &http.Transport{
		Proxy:                 nil,
		DialContext:           fixedRouteDialer(run.TargetPort),
		ForceAttemptHTTP2:     true,
		DisableKeepAlives:     true,
		DisableCompression:    true,
		MaxConnsPerHost:       1,
		ResponseHeaderTimeout: request.Timeout,
		TLSHandshakeTimeout:   request.Timeout,
		TLSClientConfig: &tls.Config{
			MinVersion: tls.VersionTLS12,
			ServerName: request.SNI,
			// The sandbox intentionally tests the candidate's certificate and SNI routing without trusting it.
			InsecureSkipVerify: true, // #nosec G402
		},
	}
	defer transport.CloseIdleConnections()
	client := &http.Client{
		Transport: transport,
		CheckRedirect: func(*http.Request, []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}
	started := time.Now()
	httpResponse, err := client.Do(httpRequest)
	duration := time.Since(started)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) || errors.Is(requestContext.Err(), context.DeadlineExceeded) {
			return routelab.Response{Duration: duration}, routelab.ErrRequestTimeout
		}
		if ctx.Err() != nil {
			return routelab.Response{Duration: duration}, ctx.Err()
		}
		return routelab.Response{Duration: duration}, fmt.Errorf("perform route request: %w", routelab.ErrEvidenceIncomplete)
	}
	response, err := captureRouteResponse(httpResponse, request.Assertions)
	response.Duration = duration
	if closeErr := httpResponse.Body.Close(); closeErr != nil {
		err = errors.Join(err, fmt.Errorf("close route response: %w", routelab.ErrEvidenceIncomplete))
	}
	return response, err
}

func fixedRouteDialer(port int) func(context.Context, string, string) (net.Conn, error) {
	dialer := &net.Dialer{}
	address := net.JoinHostPort("127.0.0.1", strconv.Itoa(port))
	return func(ctx context.Context, _, _ string) (net.Conn, error) {
		return dialer.DialContext(ctx, "tcp4", address)
	}
}

func captureRouteResponse(response *http.Response, assertions routelab.Assertions) (routelab.Response, error) {
	content, err := io.ReadAll(io.LimitReader(response.Body, routeLabResponseBodyLimit+1))
	if err != nil {
		return routelab.Response{StatusCode: response.StatusCode}, fmt.Errorf("read route response: %w", routelab.ErrEvidenceIncomplete)
	}
	digest := sha256.Sum256(content)
	result := routelab.Response{
		StatusCode: response.StatusCode,
		Headers:    sanitizedRouteResponseHeaders(response.Header),
		BodyBytes:  int64(len(content)),
		BodyDigest: hex.EncodeToString(digest[:]),
	}
	visible := content
	if len(visible) > routeLabResponseBodyLimit {
		result.BodyTruncated = true
		visible = visible[:routeLabResponseBodyLimit]
	}
	if routeResponseIsText(response.Header.Get("Content-Type")) {
		snippet := visible
		if len(snippet) > routeLabResponseSnippet {
			snippet = snippet[:routeLabResponseSnippet]
		}
		if utf8.Valid(snippet) {
			result.BodySnippet = string(snippet)
		} else {
			result.SnippetOmitted = true
		}
	} else if len(visible) > 0 {
		result.SnippetOmitted = true
	}
	result.Assertions = routelab.EvaluateAssertions(assertions, response.StatusCode, visible, result.BodyTruncated)
	return result, nil
}

func sanitizedRouteResponseHeaders(headers http.Header) []routelab.Header {
	result := make([]routelab.Header, 0, len(safeRouteResponseHeaders))
	total := 0
	names := make([]string, 0, len(headers))
	for name := range headers {
		names = append(names, name)
	}
	slices.SortFunc(names, func(left, right string) int {
		return strings.Compare(strings.ToLower(left), strings.ToLower(right))
	})
	for _, name := range names {
		lower := strings.ToLower(name)
		_, explicitlyAllowed := safeRouteResponseHeaders[lower]
		if !explicitlyAllowed && !safeRouteExtensionHeader(lower) {
			continue
		}
		values := slices.Clone(headers.Values(name))
		slices.Sort(values)
		for _, value := range values {
			value = sanitizeRouteResponseHeader(lower, value)
			if value == "" || len(value) > 2048 || strings.ContainsAny(value, "\r\n\x00") || total+len(name)+len(value) > 32<<10 {
				continue
			}
			result = append(result, routelab.Header{Name: http.CanonicalHeaderKey(name), Value: value})
			total += len(name) + len(value)
			if len(result) == 64 {
				break
			}
		}
		if len(result) == 64 {
			break
		}
	}
	slices.SortFunc(result, func(left, right routelab.Header) int {
		if compared := strings.Compare(left.Name, right.Name); compared != 0 {
			return compared
		}
		return strings.Compare(left.Value, right.Value)
	})
	return result
}

func safeRouteExtensionHeader(lower string) bool {
	if !strings.HasPrefix(lower, "x-") || strings.HasPrefix(lower, "x-nginx-uix-") {
		return false
	}
	for _, sensitive := range []string{"authorization", "auth", "cookie", "credential", "token", "secret", "api-key", "key"} {
		if strings.Contains(lower, sensitive) {
			return false
		}
	}
	return true
}

func sanitizeRouteResponseHeader(name, value string) string {
	if name != "location" {
		return value
	}
	parsed, err := url.Parse(value)
	if err != nil || parsed.User != nil {
		return ""
	}
	parsed.RawQuery = ""
	parsed.ForceQuery = false
	parsed.Fragment = ""
	return parsed.String()
}

func routeResponseIsText(contentType string) bool {
	mediaType, _, err := mime.ParseMediaType(contentType)
	if err != nil {
		return false
	}
	return strings.HasPrefix(mediaType, "text/") || mediaType == "application/json" ||
		mediaType == "application/javascript" || mediaType == "application/xml" ||
		mediaType == "application/x-www-form-urlencoded" || strings.HasSuffix(mediaType, "+json") ||
		strings.HasSuffix(mediaType, "+xml")
}

func parseRouteEvidence(
	path string,
	testToken string,
	routes []routelab.RouteDefinition,
) (_ routelab.RuntimeEvidence, returnErr error) {
	file, err := os.Open(path) // #nosec G304 -- path is the trusted access-log path generated inside the validated sandbox stage.
	if err != nil {
		return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
	}
	defer func() {
		if err := file.Close(); err != nil {
			returnErr = errors.Join(returnErr, routelab.ErrEvidenceIncomplete)
		}
	}()
	reader := io.LimitReader(file, routeLabAccessLogLimit+1)
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 4096), routeLabAccessLogLineLimit)
	matches := make([]routeAccessRecord, 0, 1)
	readBytes := 0
	for scanner.Scan() {
		readBytes += len(scanner.Bytes()) + 1
		if readBytes > routeLabAccessLogLimit {
			return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
		}
		var record routeAccessRecord
		if err := json.Unmarshal(scanner.Bytes(), &record); err != nil {
			continue
		}
		if record.Test == testToken {
			matches = append(matches, record)
			if len(matches) > 1 {
				return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
			}
		}
	}
	if scanner.Err() != nil || len(matches) != 1 {
		return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
	}
	return routeEvidenceFromRecord(matches[0], routes)
}

func routeEvidenceFromRecord(
	record routeAccessRecord,
	routes []routelab.RouteDefinition,
) (routelab.RuntimeEvidence, error) {
	status, err := strconv.Atoi(record.Status)
	requestSeconds, durationErr := strconv.ParseFloat(record.RequestTime, 64)
	if err != nil || durationErr != nil || status < 100 || status > 599 || requestSeconds < 0 ||
		math.IsInf(requestSeconds, 0) || math.IsNaN(requestSeconds) || !strings.HasPrefix(record.URI, "/") {
		return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
	}
	kinds := make(map[string]routelab.RouteKind, len(routes))
	for _, route := range routes {
		kinds[route.RouteID] = route.Kind
	}
	if kinds[record.Server] != routelab.RouteServer ||
		kinds[record.Route] != routelab.RouteServer && kinds[record.Route] != routelab.RouteLocation {
		return routelab.RuntimeEvidence{}, routelab.ErrEvidenceIncomplete
	}
	return routelab.RuntimeEvidence{
		ServerRouteID:  record.Server,
		RouteID:        record.Route,
		FinalURI:       record.URI,
		Upstream:       emptyRouteLogDash(record.Upstream),
		UpstreamStatus: emptyRouteLogDash(record.UpstreamStatus),
		StatusCode:     status,
		RequestTime:    time.Duration(requestSeconds * float64(time.Second)),
	}, nil
}

func emptyRouteLogDash(value string) string {
	if value == "-" {
		return ""
	}
	return value
}

func (writer *boundedRouteWriter) Write(content []byte) (int, error) {
	writer.lock.Lock()
	defer writer.lock.Unlock()
	written := len(content)
	remaining := writer.limit - len(writer.contents)
	if remaining <= 0 {
		writer.truncated = writer.truncated || written > 0
		return written, nil
	}
	if len(content) > remaining {
		writer.contents = append(writer.contents, content[:remaining]...)
		writer.truncated = true
		return written, nil
	}
	writer.contents = append(writer.contents, content...)
	return written, nil
}
