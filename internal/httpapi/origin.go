/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package httpapi

import (
	"net"
	"net/http"
	"net/url"
	"strings"
)

func originMatches(request *http.Request, publicURL *url.URL) bool {
	origins := request.Header.Values("Origin")
	if len(origins) != 1 {
		return false
	}
	provided, ok := canonicalOrigin(origins[0])
	if !ok {
		return false
	}
	if publicURL != nil {
		trusted, ok := canonicalOrigin(publicURL.String())
		return ok && provided == trusted
	}
	scheme := "http"
	if request.TLS != nil {
		scheme = "https"
	}
	trusted, ok := canonicalOrigin(scheme + "://" + request.Host)
	return ok && provided == trusted
}

func canonicalOrigin(raw string) (string, bool) {
	parsed, err := url.Parse(raw)
	if err != nil || (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return "", false
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" || parsed.Opaque != "" {
		return "", false
	}
	hostname := strings.ToLower(parsed.Hostname())
	port := parsed.Port()
	if (parsed.Scheme == "http" && port == "80") || (parsed.Scheme == "https" && port == "443") {
		port = ""
	}
	host := hostname
	if strings.Contains(hostname, ":") {
		host = "[" + hostname + "]"
	}
	if port != "" {
		host = net.JoinHostPort(hostname, port)
	}
	return parsed.Scheme + "://" + host, true
}
