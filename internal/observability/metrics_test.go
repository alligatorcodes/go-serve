package observability

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestMetricsMiddlewareAndEndpoint(t *testing.T) {
	metrics := New()
	handler := metrics.Endpoint(metrics.Middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/fail" {
			http.Error(response, "failed", http.StatusBadGateway)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	})))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/ok", nil))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/fail", nil))
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := response.Body.String()
	for _, metric := range []string{
		"go_serve_requests_total 2",
		"go_serve_responses_total 2",
		"go_serve_server_errors_total 1",
		"go_serve_active_requests 0",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics missing %q in %q", metric, body)
		}
	}
}
