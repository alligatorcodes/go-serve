package observability

import (
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"sync/atomic"
	"time"
)

type Metrics struct {
	requests       atomic.Uint64
	responses      atomic.Uint64
	serverErrors   atomic.Uint64
	activeRequests atomic.Int64
}

func New() *Metrics { return &Metrics{} }

func (metrics *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		started := time.Now()
		metrics.requests.Add(1)
		metrics.activeRequests.Add(1)
		writer := &statusWriter{ResponseWriter: response}
		defer func() {
			metrics.activeRequests.Add(-1)
			metrics.responses.Add(1)
			if writer.status >= http.StatusInternalServerError {
				metrics.serverErrors.Add(1)
			}
			slog.Info("http request", "method", request.Method, "path", request.URL.Path, "status", writer.status, "duration_ms", time.Since(started).Milliseconds(), "request_id", request.Header.Get("X-Request-ID"))
		}()
		next.ServeHTTP(writer, request)
	})
}

func (metrics *Metrics) Endpoint(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path != "/metrics" {
			next.ServeHTTP(response, request)
			return
		}
		response.Header().Set("Content-Type", "text/plain; version=0.0.4")
		response.WriteHeader(http.StatusOK)
		_, _ = fmt.Fprintf(response, "# TYPE go_serve_requests_total counter\ngo_serve_requests_total %d\n# TYPE go_serve_responses_total counter\ngo_serve_responses_total %d\n# TYPE go_serve_server_errors_total counter\ngo_serve_server_errors_total %d\n# TYPE go_serve_active_requests gauge\ngo_serve_active_requests %d\n", metrics.requests.Load(), metrics.responses.Load(), metrics.serverErrors.Load(), metrics.activeRequests.Load())
	})
}

type statusWriter struct {
	http.ResponseWriter
	status int
}

func (writer *statusWriter) WriteHeader(status int) {
	writer.status = status
	writer.ResponseWriter.WriteHeader(status)
}

func (writer *statusWriter) Write(body []byte) (int, error) {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	return writer.ResponseWriter.Write(body)
}

func (writer *statusWriter) Flush() {
	if writer.status == 0 {
		writer.WriteHeader(http.StatusOK)
	}
	if flusher, ok := writer.ResponseWriter.(http.Flusher); ok {
		flusher.Flush()
	}
}

func (writer *statusWriter) Unwrap() http.ResponseWriter { return writer.ResponseWriter }

func (writer *statusWriter) HeaderStatus() string { return strconv.Itoa(writer.status) }
