package observability

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

const maxMetricSeries = 256

type RequestSignals struct {
	Route       string
	Upstream    string
	AuthOutcome string
	Retries     uint64
	Rejections  uint64
}

type requestSeries struct {
	route    string
	upstream string
	method   string
	status   int
}

type signalsContextKey struct{}

type Metrics struct {
	requests       atomic.Uint64
	responses      atomic.Uint64
	serverErrors   atomic.Uint64
	activeRequests atomic.Int64
	retries        atomic.Uint64
	rejections     atomic.Uint64
	authOutcomes   sync.Map
	healthFailures atomic.Uint64
	circuitChanges atomic.Uint64
	activations    atomic.Uint64
	configVersion  atomic.Int64
	nodeIdentity   atomic.Value
	seriesMu       sync.Mutex
	series         map[requestSeries]uint64
}

func New() *Metrics {
	metrics := &Metrics{series: make(map[requestSeries]uint64)}
	metrics.nodeIdentity.Store("")
	return metrics
}

func (metrics *Metrics) SetConfigVersion(version int64) { metrics.configVersion.Store(version) }
func (metrics *Metrics) SetNodeIdentity(identity string) {
	metrics.nodeIdentity.Store(boundedLabel(identity))
}

func WithSignals(request *http.Request) *http.Request {
	*request = *request.WithContext(contextWithSignals(request.Context(), &RequestSignals{}))
	return request
}

func Signals(request *http.Request) *RequestSignals { return signalsFromContext(request.Context()) }

func SetRoute(request *http.Request, route, upstream string) {
	signals := Signals(request)
	if signals == nil {
		return
	}
	signals.Route = route
	signals.Upstream = upstream
}

func SetAuthOutcome(request *http.Request, outcome string) {
	if signals := Signals(request); signals != nil {
		signals.AuthOutcome = outcome
	}
}

func AddRetry(request *http.Request) {
	if signals := Signals(request); signals != nil {
		signals.Retries++
	}
}

func AddRejection(request *http.Request) {
	if signals := Signals(request); signals != nil {
		signals.Rejections++
	}
}

func (metrics *Metrics) Middleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		WithSignals(request)
		started := time.Now()
		metrics.requests.Add(1)
		metrics.activeRequests.Add(1)
		writer := &statusWriter{ResponseWriter: response}
		defer func() {
			status := writer.status
			if status == 0 {
				status = http.StatusOK
			}
			signals := Signals(request)
			metrics.activeRequests.Add(-1)
			metrics.responses.Add(1)
			if status >= http.StatusInternalServerError {
				metrics.serverErrors.Add(1)
			}
			if signals != nil {
				metrics.recordSeries(requestSeries{route: boundedLabel(signals.Route), upstream: boundedLabel(signals.Upstream), method: boundedLabel(request.Method), status: status})
				metrics.retries.Add(signals.Retries)
				metrics.rejections.Add(signals.Rejections)
				if signals.AuthOutcome != "" {
					value, _ := metrics.authOutcomes.LoadOrStore(boundedLabel(signals.AuthOutcome), new(atomic.Uint64))
					value.(*atomic.Uint64).Add(1)
				}
			}
			slog.Info("http request", "method", request.Method, "path", request.URL.Path, "status", status, "duration_ms", time.Since(started).Milliseconds(), "request_id", request.Header.Get("X-Request-ID"), "route", label(signals, true), "upstream", label(signals, false), "config_version", metrics.configVersion.Load(), "node_id", metrics.nodeIdentity.Load())
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
		_, _ = fmt.Fprintf(response, "# TYPE go_serve_requests_total counter\ngo_serve_requests_total %d\n# TYPE go_serve_responses_total counter\ngo_serve_responses_total %d\n# TYPE go_serve_server_errors_total counter\ngo_serve_server_errors_total %d\n# TYPE go_serve_active_requests gauge\ngo_serve_active_requests %d\n# TYPE go_serve_retries_total counter\ngo_serve_retries_total %d\n# TYPE go_serve_rejections_total counter\ngo_serve_rejections_total %d\n# TYPE go_serve_health_failures_total counter\ngo_serve_health_failures_total %d\n# TYPE go_serve_circuit_transitions_total counter\ngo_serve_circuit_transitions_total %d\n# TYPE go_serve_config_activations_total counter\ngo_serve_config_activations_total %d\n", metrics.requests.Load(), metrics.responses.Load(), metrics.serverErrors.Load(), metrics.activeRequests.Load(), metrics.retries.Load(), metrics.rejections.Load(), metrics.healthFailures.Load(), metrics.circuitChanges.Load(), metrics.activations.Load())
		metrics.seriesMu.Lock()
		for series, value := range metrics.series {
			_, _ = fmt.Fprintf(response, "go_serve_requests_by_route_total{route=%q,upstream=%q,method=%q,status=%q} %d\n", series.route, series.upstream, series.method, strconv.Itoa(series.status), value)
		}
		metrics.seriesMu.Unlock()
		metrics.authOutcomes.Range(func(key, value any) bool {
			_, _ = fmt.Fprintf(response, "go_serve_auth_outcomes_total{outcome=%q} %d\n", key.(string), value.(*atomic.Uint64).Load())
			return true
		})
	})
}

func (metrics *Metrics) RecordHealthFailure()     { metrics.healthFailures.Add(1) }
func (metrics *Metrics) RecordCircuitTransition() { metrics.circuitChanges.Add(1) }
func (metrics *Metrics) RecordActivation()        { metrics.activations.Add(1) }

func (metrics *Metrics) recordSeries(series requestSeries) {
	metrics.seriesMu.Lock()
	defer metrics.seriesMu.Unlock()
	if _, exists := metrics.series[series]; !exists && len(metrics.series) >= maxMetricSeries {
		return
	}
	metrics.series[series]++
}

func boundedLabel(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return "unknown"
	}
	if len(value) > 64 {
		return value[:64]
	}
	return value
}

func label(signals *RequestSignals, route bool) string {
	if signals == nil {
		return "unknown"
	}
	if route {
		return boundedLabel(signals.Route)
	}
	return boundedLabel(signals.Upstream)
}

func contextWithSignals(ctx context.Context, signals *RequestSignals) context.Context {
	return context.WithValue(ctx, signalsContextKey{}, signals)
}

func signalsFromContext(ctx context.Context) *RequestSignals {
	signals, _ := ctx.Value(signalsContextKey{}).(*RequestSignals)
	return signals
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
