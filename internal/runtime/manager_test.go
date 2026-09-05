package runtime

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/alligatorcodes/go-serve/internal/config"
)

func TestActivateUpdatesSharedRuntime(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("first")) }))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) { _, _ = response.Write([]byte("second")) }))
	defer second.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{first.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", Upstream: "api"}}
	manager, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()

	updated := cfg.Clone()
	updated.Upstreams[0].URLs = []string{second.URL}
	if err := manager.Activate(updated); err != nil {
		t.Fatal(err)
	}
	if manager.Config().Upstreams[0].URLs[0] != second.URL {
		t.Fatal("runtime manager did not publish candidate config")
	}

	request := httptest.NewRequest(http.MethodGet, "http://api.example.com/", nil)
	response := httptest.NewRecorder()
	manager.DataPlane().Handler().ServeHTTP(response, request)
	if response.Body.String() != "second" {
		t.Fatalf("response = %q", response.Body.String())
	}
}

func TestActivateRejectsRestartOnlyChanges(t *testing.T) {
	cfg := config.Default()
	manager, err := New(cfg, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer manager.Close()
	updated := cfg.Clone()
	updated.Server.PublicAddr = ":18080"
	if err := manager.Activate(updated); err == nil {
		t.Fatal("restart-only change was accepted")
	}
}
