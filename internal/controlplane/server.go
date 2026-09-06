package controlplane

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/pprof"
	"strconv"
	"strings"
	"sync"

	"github.com/alligatorcodes/go-serve/internal/config"
)

type Server struct {
	mu         sync.RWMutex
	config     *config.Snapshot
	previous   *config.Snapshot
	version    int64
	state      string
	lastErr    string
	activate   func(config.Config) error
	readyState bool
	readiness  func() (bool, string)
	metrics    interface{ SetConfigVersion(int64) }
}

func NewServer(cfg config.Config, activators ...func(config.Config) error) *Server {
	var activate func(config.Config) error
	if len(activators) > 0 {
		activate = activators[0]
	}
	return &Server{config: config.NewSnapshot(cfg), state: "bootstrap", activate: activate, readyState: true}
}

func (s *Server) SetDraining() {
	s.mu.Lock()
	s.readyState = false
	s.state = "draining"
	s.mu.Unlock()
}

func (s *Server) SetReadinessCheck(check func() (bool, string)) {
	s.mu.Lock()
	s.readiness = check
	s.mu.Unlock()
}

func (s *Server) SetMetrics(metrics interface{ SetConfigVersion(int64) }) {
	s.mu.Lock()
	s.metrics = metrics
	s.mu.Unlock()
}

func (s *Server) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /health", s.health)
	mux.HandleFunc("GET /ready", s.ready)
	mux.HandleFunc("GET /api/v1/config", s.getConfig)
	mux.HandleFunc("PUT /api/v1/config", s.replaceConfig)
	mux.HandleFunc("POST /api/v1/config/validate", s.validateConfig)
	mux.HandleFunc("POST /api/v1/config/rollback", s.rollbackConfig)
	mux.HandleFunc("GET /api/v1/config/status", s.configStatus)
	mux.HandleFunc("GET /api/v1/servers", s.servers)
	mux.HandleFunc("GET /api/v1/routes", s.routes)
	mux.HandleFunc("GET /api/openapi.json", s.openapi)
	mux.HandleFunc("GET /api/docs", s.docs)
	mux.HandleFunc("GET /debug/pprof/", pprof.Index)
	mux.HandleFunc("GET /debug/pprof/cmdline", pprof.Cmdline)
	mux.HandleFunc("GET /debug/pprof/profile", pprof.Profile)
	mux.HandleFunc("GET /debug/pprof/symbol", pprof.Symbol)
	mux.HandleFunc("GET /debug/pprof/trace", pprof.Trace)
	return mux
}

func (s *Server) health(response http.ResponseWriter, _ *http.Request) {
	writeJSON(response, http.StatusOK, map[string]string{"status": "ok"})
}

func (s *Server) ready(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	ready := s.readyState
	state := s.state
	check := s.readiness
	s.mu.RUnlock()
	if !ready {
		writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": state})
		return
	}
	if check != nil {
		dependencyReady, dependencyState := check()
		if !dependencyReady {
			writeJSON(response, http.StatusServiceUnavailable, map[string]string{"status": dependencyState})
			return
		}
	}
	writeJSON(response, http.StatusOK, map[string]string{"status": "ready"})
}

func (s *Server) configStatus(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	s.writeStatus(response)
}

func (s *Server) servers(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	writeJSON(response, http.StatusOK, []map[string]any{{
		"name":               "control-plane",
		"state":              s.state,
		"active_connections": 0,
		"in_flight_requests": 0,
	}})
}

func (s *Server) routes(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	value := s.config.Config()
	routes := make([]map[string]any, 0, len(value.Routes))
	for _, route := range value.Routes {
		routes = append(routes, map[string]any{
			"name":              route.Name,
			"upstream":          route.Upstream,
			"healthy_endpoints": 0,
			"requests_total":    0,
		})
	}
	writeJSON(response, http.StatusOK, routes)
}

func (s *Server) openapi(response http.ResponseWriter, _ *http.Request) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(openAPIDocument))
}

func (s *Server) docs(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	uiEnabled := s.config.Config().ControlPlane.UI
	s.mu.RUnlock()
	if !uiEnabled {
		http.NotFound(response, nil)
		return
	}
	response.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'unsafe-inline'")
	response.Header().Set("Content-Type", "text/html; charset=utf-8")
	response.WriteHeader(http.StatusOK)
	_, _ = response.Write([]byte(swaggerUIHTML))
}

func (s *Server) getConfig(response http.ResponseWriter, _ *http.Request) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	response.Header().Set("ETag", etag(s.version))
	writeJSON(response, http.StatusOK, redactConfig(s.config.Config()))
}

func (s *Server) validateConfig(response http.ResponseWriter, request *http.Request) {
	var candidate config.Config
	if err := decodeConfig(request, &candidate); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	if err := candidate.Validate(); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	writeJSON(response, http.StatusOK, map[string]any{"valid": true})
}

func (s *Server) replaceConfig(response http.ResponseWriter, request *http.Request) {
	var candidate config.Config
	if err := decodeConfig(request, &candidate); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}
	if err := candidate.Validate(); err != nil {
		writeError(response, http.StatusUnprocessableEntity, err)
		return
	}

	s.mu.Lock()
	if err := s.requireVersionLocked(request); err != nil {
		s.mu.Unlock()
		writeError(response, http.StatusConflict, err)
		return
	}
	if s.activate != nil {
		if err := s.activate(candidate); err != nil {
			s.lastErr = err.Error()
			s.state = "failed"
			s.mu.Unlock()
			writeError(response, http.StatusUnprocessableEntity, fmt.Errorf("activate configuration: %w", err))
			return
		}
	}
	previous := s.config
	s.previous = previous
	s.config = config.NewSnapshot(candidate)
	s.version++
	if s.metrics != nil {
		s.metrics.SetConfigVersion(s.version)
	}
	s.state = "active"
	s.lastErr = ""
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	response.Header().Set("ETag", etag(s.version))
	s.writeStatus(response)
}

func (s *Server) rollbackConfig(response http.ResponseWriter, request *http.Request) {
	s.mu.Lock()
	if err := s.requireVersionLocked(request); err != nil {
		s.mu.Unlock()
		writeError(response, http.StatusConflict, err)
		return
	}
	if s.previous == nil {
		s.mu.Unlock()
		writeError(response, http.StatusNotFound, errors.New("no rollback target exists"))
		return
	}
	if s.activate != nil {
		if err := s.activate(s.previous.Config()); err != nil {
			s.lastErr = err.Error()
			s.state = "failed"
			s.mu.Unlock()
			writeError(response, http.StatusUnprocessableEntity, fmt.Errorf("activate rollback: %w", err))
			return
		}
	}
	current := s.config
	s.config = s.previous
	s.previous = current
	s.version++
	if s.metrics != nil {
		s.metrics.SetConfigVersion(s.version)
	}
	s.state = "active"
	s.lastErr = ""
	s.mu.Unlock()

	s.mu.RLock()
	defer s.mu.RUnlock()
	response.Header().Set("ETag", etag(s.version))
	s.writeStatus(response)
}

func (s *Server) requireVersionLocked(request *http.Request) error {
	value := strings.Trim(strings.TrimSpace(request.Header.Get("If-Match")), "\"")
	if value == "" {
		return errors.New("If-Match header is required")
	}
	want, err := strconv.ParseInt(value, 10, 64)
	if err != nil {
		return errors.New("If-Match must contain a configuration version")
	}
	if want != s.version {
		return fmt.Errorf("configuration version conflict: expected %d, got %d", s.version, want)
	}
	return nil
}

func (s *Server) writeStatus(response http.ResponseWriter) {
	status := map[string]any{
		"version": s.version,
		"state":   s.state,
		"ui":      s.config.Config().ControlPlane.UI,
	}
	if s.lastErr != "" {
		status["error"] = s.lastErr
	}
	writeJSON(response, http.StatusOK, status)
}

func decodeConfig(request *http.Request, target *config.Config) error {
	decoder := json.NewDecoder(io.LimitReader(request.Body, 2<<20))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(target); err != nil {
		return fmt.Errorf("invalid configuration JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return errors.New("request body must contain exactly one JSON object")
	}
	return nil
}

func redactConfig(value config.Config) map[string]any {
	encoded, _ := json.Marshal(value)
	var result map[string]any
	_ = json.Unmarshal(encoded, &result)
	if auth, ok := result["auth"].(map[string]any); ok {
		delete(auth, "client_secret_file")
	}
	return result
}

func etag(version int64) string {
	return `"` + strconv.FormatInt(version, 10) + `"`
}

func writeError(response http.ResponseWriter, status int, err error) {
	writeJSON(response, status, map[string]any{
		"error": map[string]string{"message": err.Error()},
	})
}

func writeJSON(response http.ResponseWriter, status int, value any) {
	response.Header().Set("Content-Type", "application/json")
	response.WriteHeader(status)
	_ = json.NewEncoder(response).Encode(value)
}
