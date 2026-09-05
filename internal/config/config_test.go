package config

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestParseTOMLUsesDefaultsAndOverridesValues(t *testing.T) {
	value, err := ParseTOML([]byte(`[server]
admin_addr = "127.0.0.1:9999"
read_timeout = "7s"

[control_plane]
ui = true
`))
	if err != nil {
		t.Fatal(err)
	}
	if value.Server.AdminAddr != "127.0.0.1:9999" || value.Server.PublicAddr != ":8080" || value.Server.ReadTimeout != 7*time.Second {
		t.Fatalf("unexpected server config: %#v", value.Server)
	}
	if !value.ControlPlane.UI || value.Limits.MaxInFlight != 1000 {
		t.Fatalf("defaults were not preserved: %#v", value)
	}
}

func TestParseTOMLRejectsUnknownKeys(t *testing.T) {
	_, err := ParseTOML([]byte(`[server]
public_adrr = ":8080"
`))
	if err == nil || !strings.Contains(err.Error(), "unknown configuration keys") {
		t.Fatalf("error = %v, want unknown-key error", err)
	}
}

func TestLoadFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.toml")
	if err := os.WriteFile(path, []byte(`[auth]
mode = "bearer"
issuer_url = "https://issuer.example.com"
client_id = "gateway"
`), 0o600); err != nil {
		t.Fatal(err)
	}
	value, err := LoadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if value.Auth.Mode != "bearer" {
		t.Fatalf("auth mode = %q, want bearer", value.Auth.Mode)
	}
}

func TestSnapshotDoesNotShareMutableConfiguration(t *testing.T) {
	value := Default()
	value.Routes = []RouteConfig{{
		Name:    "api",
		Methods: []string{"GET"},
		Headers: map[string]string{"X-Trusted": "true"},
	}}
	snapshot := NewSnapshot(value)
	value.Routes[0].Methods[0] = "POST"
	value.Routes[0].Headers["X-Trusted"] = "false"

	copy := snapshot.Config()
	copy.Routes[0].Methods[0] = "DELETE"
	copy.Routes[0].Headers["X-Trusted"] = "false"
	stored := snapshot.Config()
	if stored.Routes[0].Methods[0] != "GET" || stored.Routes[0].Headers["X-Trusted"] != "true" {
		t.Fatalf("snapshot was mutated through a caller-owned value: %#v", stored.Routes[0])
	}
}

func TestTLSFilesMustBeConfiguredTogether(t *testing.T) {
	value := Default()
	value.Server.TLSCertFile = "/etc/go-serve/tls.crt"
	if err := value.Validate(); err == nil || !strings.Contains(err.Error(), "configured together") {
		t.Fatalf("error = %v, want paired TLS-file error", err)
	}
}

func TestClusterConfigurationRequiresConsensusFields(t *testing.T) {
	value := Default()
	value.Cluster.Enabled = true
	if err := value.Validate(); err == nil || !strings.Contains(err.Error(), "cluster requires") {
		t.Fatalf("error = %v, want cluster field validation", err)
	}
}
