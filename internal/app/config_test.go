/**
 * @author hanchao <hanchao@66yunlian.com>
 * @since 0.1.0
 */
package app

import (
	"testing"
)

func TestLoadConfigDefaultsAndFixedPaths(t *testing.T) {
	for _, name := range documentedEnvironmentVariables {
		t.Setenv(name, "")
	}
	t.Setenv("NGINX_UIX_NGINX_BINARY", "/tmp/not-nginx")
	t.Setenv("NGINX_UIX_NGINX_CONFIG", "/tmp/not-nginx.conf")
	t.Setenv("NGINX_UIX_AGENT_SOCKET", "/tmp/not-agent.sock")
	t.Setenv("NGINX_UIX_WORKSPACE_ROOT", "/tmp/not-workspaces")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got, want := config.ListenAddr, "0.0.0.0:9000"; got != want {
		t.Errorf("ListenAddr = %q, want %q", got, want)
	}
	if got, want := config.DatabasePath, "/var/lib/nginx-uix/nginx-uix.db"; got != want {
		t.Errorf("DatabasePath = %q, want %q", got, want)
	}
	if got, want := config.AgentSocketPath, "/run/nginx-uix/agent.sock"; got != want {
		t.Errorf("AgentSocketPath = %q, want %q", got, want)
	}
	if got, want := config.WorkspaceRoot, "/var/lib/nginx-uix/workspaces"; got != want {
		t.Errorf("WorkspaceRoot = %q, want %q", got, want)
	}
	if got, want := config.NginxBinary, "/usr/sbin/nginx"; got != want {
		t.Errorf("NginxBinary = %q, want %q", got, want)
	}
	if got, want := config.NginxConfigPath, "/etc/nginx/nginx.conf"; got != want {
		t.Errorf("NginxConfigPath = %q, want %q", got, want)
	}
}

func TestLoadConfigReadsOnlyDocumentedEnvironment(t *testing.T) {
	t.Setenv("NGINX_UIX_LISTEN_ADDR", "127.0.0.1:19000")
	t.Setenv("NGINX_UIX_PUBLIC_URL", "https://admin.example.test")
	t.Setenv("NGINX_UIX_ADMIN_USERNAME", "operator")
	t.Setenv("NGINX_UIX_ADMIN_PASSWORD_FILE", "/run/secrets/admin_password")
	t.Setenv("NGINX_UIX_ADMIN_PASSWORD", "fallback-password")

	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}

	if got, want := config.ListenAddr, "127.0.0.1:19000"; got != want {
		t.Errorf("ListenAddr = %q, want %q", got, want)
	}
	if got, want := config.PublicURL.String(), "https://admin.example.test"; got != want {
		t.Errorf("PublicURL = %q, want %q", got, want)
	}
	if got, want := config.AdminUsername, "operator"; got != want {
		t.Errorf("AdminUsername = %q, want %q", got, want)
	}
	if got, want := config.AdminPasswordFile, "/run/secrets/admin_password"; got != want {
		t.Errorf("AdminPasswordFile = %q, want %q", got, want)
	}
	if got, want := config.AdminPassword, "fallback-password"; got != want {
		t.Errorf("AdminPassword = %q, want %q", got, want)
	}
}

func TestLoadConfigRejectsInvalidNetworkSettings(t *testing.T) {
	tests := []struct {
		name      string
		listen    string
		publicURL string
	}{
		{name: "missing listen port", listen: "127.0.0.1", publicURL: ""},
		{name: "invalid listen port", listen: "127.0.0.1:not-a-port", publicURL: ""},
		{name: "relative public URL", listen: "127.0.0.1:9000", publicURL: "/admin"},
		{name: "unsupported public URL scheme", listen: "127.0.0.1:9000", publicURL: "ftp://admin.example.test"},
		{name: "public URL with credentials", listen: "127.0.0.1:9000", publicURL: "https://user:pass@admin.example.test"},
		{name: "public URL with path", listen: "127.0.0.1:9000", publicURL: "https://admin.example.test/panel"},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("NGINX_UIX_LISTEN_ADDR", test.listen)
			t.Setenv("NGINX_UIX_PUBLIC_URL", test.publicURL)

			if _, err := LoadConfig(); err == nil {
				t.Fatal("LoadConfig() error = nil, want validation error")
			}
		})
	}
}

func TestLoadConfigAcceptsPublicOriginWithRootSlash(t *testing.T) {
	t.Setenv("NGINX_UIX_PUBLIC_URL", "https://admin.example.test/")
	config, err := LoadConfig()
	if err != nil {
		t.Fatalf("LoadConfig() error = %v", err)
	}
	if got, want := config.PublicURL.String(), "https://admin.example.test"; got != want {
		t.Fatalf("PublicURL = %q, want normalized %q", got, want)
	}
}

var documentedEnvironmentVariables = []string{
	"NGINX_UIX_LISTEN_ADDR",
	"NGINX_UIX_PUBLIC_URL",
	"NGINX_UIX_ADMIN_USERNAME",
	"NGINX_UIX_ADMIN_PASSWORD_FILE",
	"NGINX_UIX_ADMIN_PASSWORD",
}
