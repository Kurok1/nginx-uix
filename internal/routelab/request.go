/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.4.0
 */

package routelab

import (
	"bytes"
	"fmt"
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"
)

const (
	defaultRequestTimeout = 5 * time.Second
	minimumRequestTimeout = 100 * time.Millisecond
	maximumRequestTimeout = 30 * time.Second
	maximumHeaderCount    = 32
	maximumHeaderBytes    = 16 << 10
	maximumBodyBytes      = 64 << 10
	maximumAssertionBytes = 1024
)

var allowedMethods = map[string]struct{}{
	"GET": {}, "HEAD": {}, "OPTIONS": {}, "POST": {}, "PUT": {}, "PATCH": {}, "DELETE": {},
}

var reservedHeaders = map[string]struct{}{
	"connection": {}, "content-length": {}, "host": {}, "te": {}, "trailer": {},
	"transfer-encoding": {}, "upgrade": {},
}

// ValidateRequest normalizes and bounds a typed route request before persistence or Agent use.
func ValidateRequest(input Request) (ValidatedRequest, error) {
	static, err := validateStaticRequest(input.StaticRequest)
	if err != nil {
		return ValidatedRequest{}, err
	}
	input.StaticRequest = static
	input.Method = strings.ToUpper(strings.TrimSpace(input.Method))
	if input.Method == "" {
		input.Method = "GET"
	}
	if _, exists := allowedMethods[input.Method]; !exists {
		return ValidatedRequest{}, fmt.Errorf("validate route method: %w", ErrInvalidRequest)
	}
	if strings.ContainsAny(input.Query, "#\r\n\x00") {
		return ValidatedRequest{}, fmt.Errorf("validate route query: %w", ErrInvalidRequest)
	}
	if input.Query != "" {
		if _, err := url.ParseQuery(input.Query); err != nil {
			return ValidatedRequest{}, fmt.Errorf("validate route query: %w", ErrInvalidRequest)
		}
	}
	if input.Timeout == 0 {
		input.Timeout = defaultRequestTimeout
	}
	if input.Timeout < minimumRequestTimeout || input.Timeout > maximumRequestTimeout {
		return ValidatedRequest{}, fmt.Errorf("validate route timeout: %w", ErrInvalidRequest)
	}
	if len(input.Headers) > maximumHeaderCount {
		return ValidatedRequest{}, fmt.Errorf("validate route headers: %w", ErrLimitExceeded)
	}
	if len(input.Body) > maximumBodyBytes {
		return ValidatedRequest{}, fmt.Errorf("validate route body: %w", ErrLimitExceeded)
	}
	if input.Assertions.StatusCode < 0 || input.Assertions.StatusCode > 599 ||
		input.Assertions.StatusCode > 0 && input.Assertions.StatusCode < 100 ||
		!validBoundedText(input.Assertions.ContainsText, maximumAssertionBytes) ||
		!validBoundedText(input.Assertions.ForbiddenText, maximumAssertionBytes) {
		return ValidatedRequest{}, fmt.Errorf("validate route assertions: %w", ErrInvalidRequest)
	}

	headerBytes := 0
	seen := make(map[string]struct{}, len(input.Headers))
	sensitive := make([]string, 0)
	for index := range input.Headers {
		header := &input.Headers[index]
		if !validHeaderName(header.Name) || !validHeaderValue(header.Value) {
			return ValidatedRequest{}, fmt.Errorf("validate route header: %w", ErrInvalidRequest)
		}
		lower := strings.ToLower(header.Name)
		if _, duplicate := seen[lower]; duplicate {
			return ValidatedRequest{}, fmt.Errorf("validate duplicate route header: %w", ErrInvalidRequest)
		}
		seen[lower] = struct{}{}
		if _, reserved := reservedHeaders[lower]; reserved || strings.HasPrefix(lower, "proxy-") ||
			strings.HasPrefix(lower, "x-nginx-uix-") {
			return ValidatedRequest{}, fmt.Errorf("validate reserved route header: %w", ErrInvalidRequest)
		}
		headerBytes += len(header.Name) + len(header.Value)
		if headerBytes > maximumHeaderBytes {
			return ValidatedRequest{}, fmt.Errorf("validate route header bytes: %w", ErrLimitExceeded)
		}
		header.Name = canonicalHeaderName(header.Name)
		if sensitiveHeader(lower) {
			sensitive = append(sensitive, header.Name)
		}
	}
	slices.Sort(sensitive)
	input.Body = bytes.Clone(input.Body)
	input.Headers = slices.Clone(input.Headers)

	sideEffecting := len(input.Body) > 0 || input.Method != "GET" && input.Method != "HEAD" && input.Method != "OPTIONS"
	if sideEffecting && input.Confirmation != SideEffectConfirmation {
		return ValidatedRequest{}, fmt.Errorf("validate route confirmation: %w", ErrConfirmationRequired)
	}
	input.Confirmation = ""
	return ValidatedRequest{
		Request: input, SideEffecting: sideEffecting,
		Replayable:           len(input.Body) == 0 && len(sensitive) == 0,
		SensitiveHeaderNames: sensitive,
	}, nil
}

// ValidateAgentRequest revalidates a normalized request without transporting the user's confirmation text.
func ValidateAgentRequest(input ValidatedRequest) (ValidatedRequest, error) {
	request := input.Request
	if input.SideEffecting {
		request.Confirmation = SideEffectConfirmation
	}
	validated, err := ValidateRequest(request)
	if err != nil {
		return ValidatedRequest{}, err
	}
	if input.UpstreamSideEffect {
		validated.SideEffecting = true
		validated.UpstreamSideEffect = true
	}
	if validated.SideEffecting != input.SideEffecting || validated.Replayable != input.Replayable ||
		validated.UpstreamSideEffect != input.UpstreamSideEffect ||
		!slices.Equal(validated.SensitiveHeaderNames, input.SensitiveHeaderNames) {
		return ValidatedRequest{}, fmt.Errorf("validate normalized route request: %w", ErrInvalidRequest)
	}
	return validated, nil
}

func validateStaticRequest(input StaticRequest) (StaticRequest, error) {
	switch input.Scheme {
	case SchemeHTTP:
		if input.Port == 0 {
			input.Port = 80
		}
		if input.SNI != "" {
			return StaticRequest{}, fmt.Errorf("validate HTTP SNI: %w", ErrInvalidRequest)
		}
	case SchemeHTTPS:
		if input.Port == 0 {
			input.Port = 443
		}
	default:
		return StaticRequest{}, fmt.Errorf("validate route scheme: %w", ErrInvalidRequest)
	}
	if input.Port <= 0 || input.Port > 65535 {
		return StaticRequest{}, fmt.Errorf("validate route port: %w", ErrInvalidRequest)
	}
	host, hostPort, err := normalizeHost(input.Host)
	if err != nil || hostPort != 0 && hostPort != input.Port {
		return StaticRequest{}, fmt.Errorf("validate route host: %w", ErrInvalidRequest)
	}
	input.Host = host
	if input.Scheme == SchemeHTTPS {
		if input.SNI == "" && net.ParseIP(strings.Trim(input.Host, "[]")) == nil {
			input.SNI = input.Host
		}
		if input.SNI != "" {
			sni, sniPort, err := normalizeHost(input.SNI)
			if err != nil || sniPort != 0 || net.ParseIP(strings.Trim(sni, "[]")) != nil {
				return StaticRequest{}, fmt.Errorf("validate route SNI: %w", ErrInvalidRequest)
			}
			input.SNI = sni
		}
	}
	if input.URI == "" {
		input.URI = "/"
	}
	if !strings.HasPrefix(input.URI, "/") || strings.ContainsAny(input.URI, "?#\r\n\x00") || !utf8.ValidString(input.URI) {
		return StaticRequest{}, fmt.Errorf("validate route URI: %w", ErrInvalidRequest)
	}
	parsed, err := url.ParseRequestURI(input.URI)
	if err != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return StaticRequest{}, fmt.Errorf("validate route URI: %w", ErrInvalidRequest)
	}
	return input, nil
}

func normalizeHost(value string) (string, int, error) {
	value = strings.TrimSpace(value)
	if value == "" || len(value) > 255 || strings.ContainsAny(value, "/\\@?#\r\n\x00") {
		return "", 0, ErrInvalidRequest
	}
	host := value
	port := 0
	if strings.HasPrefix(value, "[") {
		closing := strings.IndexByte(value, ']')
		if closing < 0 {
			return "", 0, ErrInvalidRequest
		}
		if closing+1 < len(value) {
			parsedHost, portText, err := net.SplitHostPort(value)
			if err != nil {
				return "", 0, ErrInvalidRequest
			}
			host = parsedHost
			parsedPort, err := strconv.Atoi(portText)
			if err != nil || parsedPort <= 0 || parsedPort > 65535 {
				return "", 0, ErrInvalidRequest
			}
			port = parsedPort
		} else {
			host = value[1:closing]
		}
		if net.ParseIP(host) == nil {
			return "", 0, ErrInvalidRequest
		}
		return "[" + strings.ToLower(host) + "]", port, nil
	}
	if strings.Count(value, ":") == 1 {
		parsedHost, portText, err := net.SplitHostPort(value)
		if err == nil {
			host = parsedHost
			parsedPort, parseErr := strconv.Atoi(portText)
			if parseErr != nil || parsedPort <= 0 || parsedPort > 65535 {
				return "", 0, ErrInvalidRequest
			}
			port = parsedPort
		}
	} else if strings.Contains(value, ":") {
		return "", 0, ErrInvalidRequest
	}
	host = strings.TrimSuffix(strings.ToLower(host), ".")
	if net.ParseIP(host) == nil && !validDNSName(host) {
		return "", 0, ErrInvalidRequest
	}
	return host, port, nil
}

func validDNSName(value string) bool {
	if value == "" || len(value) > 253 {
		return false
	}
	for _, label := range strings.Split(value, ".") {
		if label == "" || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return false
		}
		for _, character := range label {
			if character <= unicode.MaxASCII &&
				(character >= 'a' && character <= 'z' || character >= '0' && character <= '9' || character == '-') {
				continue
			}
			return false
		}
	}
	return true
}

func validHeaderName(value string) bool {
	if value == "" || len(value) > 128 {
		return false
	}
	for index := range len(value) {
		character := value[index]
		if character >= 'a' && character <= 'z' || character >= 'A' && character <= 'Z' ||
			character >= '0' && character <= '9' || strings.ContainsRune("!#$%&'*+-.^_`|~", rune(character)) {
			continue
		}
		return false
	}
	return true
}

func validHeaderValue(value string) bool {
	if len(value) > 8<<10 || !utf8.ValidString(value) {
		return false
	}
	for _, character := range value {
		if character == '\t' {
			continue
		}
		if character == '\r' || character == '\n' || character == 0x7f || unicode.IsControl(character) {
			return false
		}
	}
	return true
}

func validBoundedText(value string, maximum int) bool {
	return len(value) <= maximum && utf8.ValidString(value) && !strings.ContainsRune(value, '\x00')
}

func sensitiveHeader(lower string) bool {
	return lower == "authorization" || lower == "cookie" || lower == "proxy-authorization" ||
		strings.Contains(lower, "token") || strings.Contains(lower, "secret") || strings.Contains(lower, "api-key") ||
		strings.HasSuffix(lower, "-key")
}

func canonicalHeaderName(value string) string {
	parts := strings.Split(strings.ToLower(value), "-")
	for index := range parts {
		if parts[index] != "" {
			parts[index] = strings.ToUpper(parts[index][:1]) + parts[index][1:]
		}
	}
	return strings.Join(parts, "-")
}
