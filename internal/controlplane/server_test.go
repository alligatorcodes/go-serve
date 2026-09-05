package controlplane

import (
	"bytes"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/alligatorcodes/go-serve/internal/config"
)

func TestControlPlaneBootstrapStatus(t *testing.T) {
	server := NewServer(config.Default())
	response := request(t, server.Handler(), http.MethodGet, "/api/v1/config/status", "", nil)

	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}
	var status map[string]any
	decode(t, response, &status)
	if status["state"] != "bootstrap" || status["version"] != float64(0) {
		t.Fatalf("unexpected bootstrap status: %#v", status)
	}
}

func TestValidateDoesNotActivateConfiguration(t *testing.T) {
	server := NewServer(config.Default())
	candidate := config.Default()
	candidate.ControlPlane.UI = true

	response := requestJSON(t, server.Handler(), http.MethodPost, "/api/v1/config/validate", candidate, nil)
	if response.Code != http.StatusOK {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusOK)
	}

	status := request(t, server.Handler(), http.MethodGet, "/api/v1/config/status", "", nil)
	var body map[string]any
	decode(t, status, &body)
	if body["version"] != float64(0) || body["ui"] != false {
		t.Fatalf("validation changed active state: %#v", body)
	}
}

func TestReplaceConfigRequiresCurrentVersionAndSupportsRollback(t *testing.T) {
	server := NewServer(config.Default())
	candidate := config.Default()
	candidate.ControlPlane.UI = true

	response := requestJSON(t, server.Handler(), http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
	if response.Code != http.StatusOK || response.Header().Get("ETag") != `"1"` {
		t.Fatalf("replace response = %d, etag %q", response.Code, response.Header().Get("ETag"))
	}

	stale := requestJSON(t, server.Handler(), http.MethodPut, "/api/v1/config", config.Default(), map[string]string{"If-Match": `"0"`})
	if stale.Code != http.StatusConflict {
		t.Fatalf("stale replace status = %d, want %d", stale.Code, http.StatusConflict)
	}

	rollback := request(t, server.Handler(), http.MethodPost, "/api/v1/config/rollback", "", map[string]string{"If-Match": `"1"`})
	if rollback.Code != http.StatusOK || rollback.Header().Get("ETag") != `"2"` {
		t.Fatalf("rollback response = %d, etag %q", rollback.Code, rollback.Header().Get("ETag"))
	}

	status := request(t, server.Handler(), http.MethodGet, "/api/v1/config/status", "", nil)
	var body map[string]any
	decode(t, status, &body)
	if body["version"] != float64(2) || body["ui"] != false {
		t.Fatalf("unexpected rollback status: %#v", body)
	}
}

func TestConfigReadRedactsSecrets(t *testing.T) {
	server := NewServer(config.Default())
	candidate := config.Default()
	candidate.Auth.ClientSecretFile = "/run/secrets/client"
	response := requestJSON(t, server.Handler(), http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
	if response.Code != http.StatusOK {
		t.Fatalf("replace status = %d", response.Code)
	}

	response = request(t, server.Handler(), http.MethodGet, "/api/v1/config", "", nil)
	var body map[string]any
	decode(t, response, &body)
	auth := body["auth"].(map[string]any)
	if _, exists := auth["client_secret_file"]; exists {
		t.Fatal("config response exposed client_secret_file")
	}
}

func TestInvalidConfigurationIsRejected(t *testing.T) {
	server := NewServer(config.Default())
	invalid := config.Default()
	invalid.Auth.Mode = "unknown"
	response := requestJSON(t, server.Handler(), http.MethodPost, "/api/v1/config/validate", invalid, nil)
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
}

func TestSwaggerUIIsControlledByConfiguration(t *testing.T) {
	server := NewServer(config.Default())
	response := request(t, server.Handler(), http.MethodGet, "/api/docs", "", nil)
	if response.Code != http.StatusNotFound {
		t.Fatalf("disabled UI status = %d, want %d", response.Code, http.StatusNotFound)
	}

	candidate := config.Default()
	candidate.ControlPlane.UI = true
	response = requestJSON(t, server.Handler(), http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
	if response.Code != http.StatusOK {
		t.Fatalf("enable UI status = %d", response.Code)
	}
	response = request(t, server.Handler(), http.MethodGet, "/api/docs", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), "Go Serve Control Plane") {
		t.Fatalf("enabled UI response = %d, body %q", response.Code, response.Body.String())
	}
}

func TestOpenAPIDocumentIsServed(t *testing.T) {
	server := NewServer(config.Default())
	response := request(t, server.Handler(), http.MethodGet, "/api/openapi.json", "", nil)
	if response.Code != http.StatusOK || !strings.Contains(response.Body.String(), `"openapi": "3.0.3"`) {
		t.Fatalf("OpenAPI response = %d, body %q", response.Code, response.Body.String())
	}
}

func TestConcurrentStatusReads(t *testing.T) {
	server := NewServer(config.Default())
	handler := server.Handler()
	var group sync.WaitGroup
	for index := 0; index < 32; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			response := request(t, handler, http.MethodGet, "/api/v1/config/status", "", nil)
			if response.Code != http.StatusOK {
				t.Errorf("status = %d, want %d", response.Code, http.StatusOK)
			}
		}()
	}
	group.Wait()
}

func TestConcurrentReplacementsHonorVersionCheck(t *testing.T) {
	server := NewServer(config.Default())
	handler := server.Handler()
	var group sync.WaitGroup
	statuses := make(chan int, 2)
	for index := 0; index < 2; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			candidate := config.Default()
			candidate.ControlPlane.UI = true
			response := requestJSON(t, handler, http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
			statuses <- response.Code
		}()
	}
	group.Wait()
	close(statuses)

	var success, conflict int
	for status := range statuses {
		switch status {
		case http.StatusOK:
			success++
		case http.StatusConflict:
			conflict++
		}
	}
	if success != 1 || conflict != 1 {
		t.Fatalf("concurrent replacement statuses: success=%d conflict=%d", success, conflict)
	}
}

func TestReplaceConfigActivatesDataPlaneBeforePublishing(t *testing.T) {
	activated := 0
	server := NewServer(config.Default(), func(candidate config.Config) error {
		activated++
		if candidate.ControlPlane.UI {
			return nil
		}
		return nil
	})
	candidate := config.Default()
	candidate.ControlPlane.UI = true
	response := requestJSON(t, server.Handler(), http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
	if response.Code != http.StatusOK || activated != 1 {
		t.Fatalf("activation response = %d, activations = %d", response.Code, activated)
	}

	failing := NewServer(config.Default(), func(config.Config) error { return errors.New("runtime compile failed") })
	response = requestJSON(t, failing.Handler(), http.MethodPut, "/api/v1/config", candidate, map[string]string{"If-Match": `"0"`})
	if response.Code != http.StatusUnprocessableEntity {
		t.Fatalf("failed activation status = %d, want %d", response.Code, http.StatusUnprocessableEntity)
	}
	status := request(t, failing.Handler(), http.MethodGet, "/api/v1/config/status", "", nil)
	var body map[string]any
	decode(t, status, &body)
	if body["version"] != float64(0) || body["state"] != "failed" {
		t.Fatalf("failed activation changed published state: %#v", body)
	}
}

func request(t *testing.T, handler http.Handler, method, path, body string, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(method, path, strings.NewReader(body))
	for key, value := range headers {
		request.Header.Set(key, value)
	}
	response := httptest.NewRecorder()
	handler.ServeHTTP(response, request)
	return response
}

func requestJSON(t *testing.T, handler http.Handler, method, path string, value any, headers map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	body, err := json.Marshal(value)
	if err != nil {
		t.Fatal(err)
	}
	return request(t, handler, method, path, string(body), headers)
}

func decode(t *testing.T, response *httptest.ResponseRecorder, target any) {
	t.Helper()
	if err := json.NewDecoder(bytes.NewReader(response.Body.Bytes())).Decode(target); err != nil {
		t.Fatalf("decode response: %v", err)
	}
}
