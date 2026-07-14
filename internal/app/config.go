/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"fmt"
	"log/slog"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	defaultListenAddr = "0.0.0.0:9000"
	databasePath      = "/var/lib/nginx-uix/nginx-uix.db"
	agentSocketPath   = "/run/nginx-uix/agent.sock"
	nginxBinary       = "/usr/sbin/nginx"
	nginxConfigPath   = "/etc/nginx/nginx.conf"
)

// Config contains the trusted process configuration. Nginx paths are fixed and
// deliberately cannot be supplied through the environment.
type Config struct {
	ListenAddr        string
	PublicURL         *url.URL
	AdminUsername     string
	AdminPasswordFile string
	AdminPassword     string
	DatabasePath      string
	AgentSocketPath   string
	NginxBinary       string
	NginxConfigPath   string
	ShutdownTimeout   time.Duration
	Logger            *slog.Logger
}

// LoadConfig reads the five documented environment variables and applies
// trusted fixed paths for all privileged resources.
func LoadConfig() (Config, error) {
	listenAddr := strings.TrimSpace(os.Getenv("NGINX_UIX_LISTEN_ADDR"))
	if listenAddr == "" {
		listenAddr = defaultListenAddr
	}
	if err := validateListenAddr(listenAddr); err != nil {
		return Config{}, err
	}

	publicURL, err := parsePublicURL(strings.TrimSpace(os.Getenv("NGINX_UIX_PUBLIC_URL")))
	if err != nil {
		return Config{}, err
	}

	return Config{
		ListenAddr:        listenAddr,
		PublicURL:         publicURL,
		AdminUsername:     os.Getenv("NGINX_UIX_ADMIN_USERNAME"),
		AdminPasswordFile: os.Getenv("NGINX_UIX_ADMIN_PASSWORD_FILE"),
		AdminPassword:     os.Getenv("NGINX_UIX_ADMIN_PASSWORD"),
		DatabasePath:      databasePath,
		AgentSocketPath:   agentSocketPath,
		NginxBinary:       nginxBinary,
		NginxConfigPath:   nginxConfigPath,
		ShutdownTimeout:   10 * time.Second,
	}, nil
}

func validateListenAddr(address string) error {
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return fmt.Errorf("validate listen address: %w", err)
	}
	if strings.ContainsAny(host, "\r\n") {
		return fmt.Errorf("validate listen address: invalid host")
	}
	port, err := strconv.ParseUint(portText, 10, 16)
	if err != nil || port == 0 {
		return fmt.Errorf("validate listen address: invalid port")
	}
	return nil
}

func parsePublicURL(raw string) (*url.URL, error) {
	if raw == "" {
		return nil, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return nil, fmt.Errorf("parse public URL: %w", err)
	}
	if (parsed.Scheme != "http" && parsed.Scheme != "https") || parsed.Host == "" {
		return nil, fmt.Errorf("validate public URL: absolute HTTP(S) URL required")
	}
	if parsed.User != nil || (parsed.Path != "" && parsed.Path != "/") || parsed.RawQuery != "" || parsed.Fragment != "" {
		return nil, fmt.Errorf("validate public URL: origin URL required")
	}
	parsed.Path = ""
	return parsed, nil
}
