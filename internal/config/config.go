package config

import (
	"fmt"
	"os"
	"time"

	"github.com/BurntSushi/toml"
)

// Config is the validated desired state for both listeners and runtime policy.
type Config struct {
	Server       ServerConfig       `toml:"server" json:"server"`
	ControlPlane ControlPlaneConfig `toml:"control_plane" json:"control_plane"`
	Auth         AuthConfig         `toml:"auth" json:"auth"`
	Limits       LimitsConfig       `toml:"limits" json:"limits"`
	Routes       []RouteConfig      `toml:"routes" json:"routes"`
	Upstreams    []UpstreamConfig   `toml:"upstreams" json:"upstreams"`
}

type ServerConfig struct {
	PublicAddr        string        `toml:"public_addr" json:"public_addr"`
	AdminAddr         string        `toml:"admin_addr" json:"admin_addr"`
	ReadHeaderTimeout time.Duration `toml:"read_header_timeout" json:"read_header_timeout"`
	ReadTimeout       time.Duration `toml:"read_timeout" json:"read_timeout"`
	WriteTimeout      time.Duration `toml:"write_timeout" json:"write_timeout"`
	IdleTimeout       time.Duration `toml:"idle_timeout" json:"idle_timeout"`
	ShutdownTimeout   time.Duration `toml:"shutdown_timeout" json:"shutdown_timeout"`
	TLSCertFile       string        `toml:"tls_cert_file" json:"-"`
	TLSKeyFile        string        `toml:"tls_key_file" json:"-"`
}

type ControlPlaneConfig struct {
	UI          bool   `toml:"ui" json:"ui"`
	OpenAPIPath string `toml:"openapi_path" json:"openapi_path"`
}

type AuthConfig struct {
	Mode              string   `toml:"mode" json:"mode"`
	IssuerURL         string   `toml:"issuer_url" json:"issuer_url"`
	ClientID          string   `toml:"client_id" json:"client_id"`
	ClientSecretFile  string   `toml:"client_secret_file" json:"-"`
	SessionSecretFile string   `toml:"session_secret_file" json:"-"`
	RedirectURL       string   `toml:"redirect_url" json:"redirect_url"`
	SessionCookieName string   `toml:"session_cookie_name" json:"session_cookie_name"`
	AllowedScopes     []string `toml:"allowed_scopes" json:"allowed_scopes"`
}

type LimitsConfig struct {
	MaxConnections int64 `toml:"max_connections" json:"max_connections"`
	MaxInFlight    int64 `toml:"max_in_flight" json:"max_in_flight"`
	MaxHeaderBytes int   `toml:"max_header_bytes" json:"max_header_bytes"`
	MaxBodyBytes   int64 `toml:"max_body_bytes" json:"max_body_bytes"`
}

type RouteConfig struct {
	Name        string            `toml:"name" json:"name"`
	Host        string            `toml:"host" json:"host"`
	PathPrefix  string            `toml:"path_prefix" json:"path_prefix"`
	Methods     []string          `toml:"methods" json:"methods"`
	Upstream    string            `toml:"upstream" json:"upstream"`
	RequireAuth bool              `toml:"require_auth" json:"require_auth"`
	Scopes      []string          `toml:"scopes" json:"scopes"`
	Headers     map[string]string `toml:"headers" json:"headers"`
}

type UpstreamConfig struct {
	Name           string        `toml:"name" json:"name"`
	URLs           []string      `toml:"urls" json:"urls"`
	HealthPath     string        `toml:"health_path" json:"health_path"`
	DialTimeout    time.Duration `toml:"dial_timeout" json:"dial_timeout"`
	RequestTimeout time.Duration `toml:"request_timeout" json:"request_timeout"`
	HealthInterval time.Duration `toml:"health_interval" json:"health_interval"`
}

// Snapshot is an immutable copy of a validated configuration. Config returns
// another deep copy so callers cannot mutate the active runtime state.
type Snapshot struct {
	value Config
}

func NewSnapshot(value Config) *Snapshot {
	return &Snapshot{value: value.Clone()}
}

func (s *Snapshot) Config() Config {
	return s.value.Clone()
}

func LoadFile(path string) (Config, error) {
	contents, err := os.ReadFile(path)
	if err != nil {
		return Config{}, fmt.Errorf("read config %q: %w", path, err)
	}
	return ParseTOML(contents)
}

func ParseTOML(contents []byte) (Config, error) {
	value := Default()
	metadata, err := toml.Decode(string(contents), &value)
	if err != nil {
		return Config{}, fmt.Errorf("decode TOML: %w", err)
	}
	if undecoded := metadata.Undecoded(); len(undecoded) > 0 {
		return Config{}, fmt.Errorf("unknown configuration keys: %v", undecoded)
	}
	if err := value.Validate(); err != nil {
		return Config{}, err
	}
	return value, nil
}

func (c Config) Clone() Config {
	clone := c
	clone.Auth.AllowedScopes = append([]string(nil), c.Auth.AllowedScopes...)
	clone.Routes = append([]RouteConfig(nil), c.Routes...)
	clone.Upstreams = append([]UpstreamConfig(nil), c.Upstreams...)
	for index := range clone.Routes {
		clone.Routes[index].Methods = append([]string(nil), c.Routes[index].Methods...)
		clone.Routes[index].Scopes = append([]string(nil), c.Routes[index].Scopes...)
		clone.Routes[index].Headers = cloneStringMap(c.Routes[index].Headers)
	}
	for index := range clone.Upstreams {
		clone.Upstreams[index].URLs = append([]string(nil), c.Upstreams[index].URLs...)
	}
	return clone
}

func cloneStringMap(value map[string]string) map[string]string {
	if value == nil {
		return nil
	}
	clone := make(map[string]string, len(value))
	for key, item := range value {
		clone[key] = item
	}
	return clone
}

func Default() Config {
	return Config{
		Server: ServerConfig{
			PublicAddr:        ":8080",
			AdminAddr:         "127.0.0.1:9901",
			ReadHeaderTimeout: 5 * time.Second,
			ReadTimeout:       30 * time.Second,
			WriteTimeout:      30 * time.Second,
			IdleTimeout:       60 * time.Second,
			ShutdownTimeout:   15 * time.Second,
		},
		ControlPlane: ControlPlaneConfig{
			UI:          false,
			OpenAPIPath: "/api/openapi.json",
		},
		Auth: AuthConfig{Mode: "disabled", SessionCookieName: "go_serve_session"},
		Limits: LimitsConfig{
			MaxConnections: 10000,
			MaxInFlight:    1000,
			MaxHeaderBytes: 1 << 20,
			MaxBodyBytes:   10 << 20,
		},
	}
}

func (c Config) Validate() error {
	if c.Server.PublicAddr == "" || c.Server.AdminAddr == "" {
		return fmt.Errorf("server public_addr and admin_addr are required")
	}
	if (c.Server.TLSCertFile == "") != (c.Server.TLSKeyFile == "") {
		return fmt.Errorf("tls_cert_file and tls_key_file must be configured together")
	}
	if c.ControlPlane.OpenAPIPath == "" || c.ControlPlane.OpenAPIPath[0] != '/' {
		return fmt.Errorf("control_plane.openapi_path must be an absolute URL path")
	}
	if c.Auth.Mode != "disabled" && c.Auth.Mode != "oidc" && c.Auth.Mode != "bearer" {
		return fmt.Errorf("auth.mode must be disabled, oidc, or bearer")
	}
	if (c.Auth.Mode == "oidc" || c.Auth.Mode == "bearer") && (c.Auth.IssuerURL == "" || c.Auth.ClientID == "") {
		return fmt.Errorf("%s requires issuer_url and client_id", c.Auth.Mode)
	}
	if c.Auth.Mode == "oidc" && (c.Auth.RedirectURL == "" || c.Auth.ClientSecretFile == "" || c.Auth.SessionSecretFile == "") {
		return fmt.Errorf("oidc requires redirect_url, client_secret_file, and session_secret_file")
	}
	for _, route := range c.Routes {
		if route.RequireAuth && c.Auth.Mode == "disabled" {
			return fmt.Errorf("route %q requires authentication but auth.mode is disabled", route.Name)
		}
	}
	if c.Limits.MaxConnections < 1 || c.Limits.MaxInFlight < 1 || c.Limits.MaxHeaderBytes < 1024 || c.Limits.MaxBodyBytes < 1 {
		return fmt.Errorf("limits must be positive and max_header_bytes must be at least 1024")
	}
	return nil
}
