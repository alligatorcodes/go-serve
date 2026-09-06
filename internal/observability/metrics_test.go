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

func TestMetricsRecordsBoundedOperationalSignals(t *testing.T) {
	metrics := New()
	metrics.SetConfigVersion(7)
	metrics.SetNodeIdentity("node-a")
	handler := metrics.Endpoint(metrics.Middleware(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		SetRoute(request, "api", "backend")
		SetAuthOutcome(request, "success")
		AddRetry(request)
		AddRejection(request)
		response.WriteHeader(http.StatusBadGateway)
	})))

	response := httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/api", nil))
	metrics.RecordHealthFailure()
	metrics.RecordCircuitTransition()
	metrics.RecordActivation()
	response = httptest.NewRecorder()
	handler.ServeHTTP(response, httptest.NewRequest(http.MethodGet, "/metrics", nil))

	body := response.Body.String()
	for _, metric := range []string{
		`go_serve_requests_by_route_total{route="api",upstream="backend",method="GET",status="502"} 1`,
		"go_serve_retries_total 1",
		"go_serve_rejections_total 1",
		`go_serve_auth_outcomes_total{outcome="success"} 1`,
		"go_serve_health_failures_total 1",
		"go_serve_circuit_transitions_total 1",
		"go_serve_config_activations_total 1",
	} {
		if !strings.Contains(body, metric) {
			t.Fatalf("metrics missing %q in %q", metric, body)
		}
	}
}
