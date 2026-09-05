package dataplane

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/example/go-serve/internal/config"
)

func TestRoutesAndProxiesRequest(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		response.Header().Set("X-Upstream", "true")
		fmt.Fprintf(response, "%s %s", request.Method, request.URL.RequestURI())
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{upstream.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", PathPrefix: "/api", Upstream: "api"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	request := httptest.NewRequest(http.MethodGet, "http://api.example.com/api/items?active=true", nil)
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	if response.Code != http.StatusOK || response.Header().Get("X-Upstream") != "true" {
		t.Fatalf("proxy response = %d, headers=%v, body=%q", response.Code, response.Header(), response.Body.String())
	}
	if response.Body.String() != "GET /api/items?active=true" {
		t.Fatalf("upstream request = %q", response.Body.String())
	}
}

func TestRouteMethodAndPathRejection(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{upstream.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", PathPrefix: "/api", Methods: []string{"GET"}, Upstream: "api"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	methodResponse := serve(server, http.MethodPost, "api.example.com", "/api/items", "")
	if methodResponse.Code != http.StatusMethodNotAllowed {
		t.Fatalf("method rejection = %d, want %d", methodResponse.Code, http.StatusMethodNotAllowed)
	}
	pathResponse := serve(server, http.MethodGet, "api.example.com", "/other", "")
	if pathResponse.Code != http.StatusNotFound {
		t.Fatalf("path rejection = %d, want %d", pathResponse.Code, http.StatusNotFound)
	}
}

func TestConcurrentRequestsAndBodyLimit(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		_, _ = io.Copy(io.Discard, request.Body)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Limits.MaxBodyBytes = 4
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{upstream.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", Upstream: "api"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	var group sync.WaitGroup
	for index := 0; index < 16; index++ {
		group.Add(1)
		go func() {
			defer group.Done()
			response := serve(server, http.MethodPost, "api.example.com", "/", "ok")
			if response.Code != http.StatusNoContent {
				t.Errorf("concurrent request status = %d", response.Code)
			}
		}()
	}
	group.Wait()

	tooLarge := serve(server, http.MethodPost, "api.example.com", "/", "too-large")
	if tooLarge.Code != http.StatusBadGateway {
		t.Fatalf("oversized body status = %d, want upstream failure after limit", tooLarge.Code)
	}
}

func TestUpstreamRequestTimeout(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		time.Sleep(50 * time.Millisecond)
		response.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "slow", URLs: []string{upstream.URL}, RequestTimeout: 5 * time.Millisecond}}
	cfg.Routes = []config.RouteConfig{{Name: "slow", Host: "slow.example.com", Upstream: "slow"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}

	response := serve(server, http.MethodGet, "slow.example.com", "/", "")
	if response.Code != http.StatusBadGateway {
		t.Fatalf("timeout status = %d, want %d", response.Code, http.StatusBadGateway)
	}
}

func TestRetryTransportRetriesOnlySafeMethods(t *testing.T) {
	calls := 0
	transport := &retryTransport{
		attempts: 1,
		breaker:  &circuitBreaker{threshold: 5, cooldown: time.Minute},
		base: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls++
			if calls == 1 {
				return &http.Response{StatusCode: http.StatusBadGateway, Body: io.NopCloser(strings.NewReader(""))}, nil
			}
			return &http.Response{StatusCode: http.StatusNoContent, Body: io.NopCloser(strings.NewReader(""))}, nil
		}),
	}
	request := httptest.NewRequest(http.MethodGet, "http://upstream.test", nil)
	response, err := transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusNoContent || calls != 2 {
		t.Fatalf("GET retry result = response=%v err=%v calls=%d", response, err, calls)
	}

	calls = 0
	request = httptest.NewRequest(http.MethodPost, "http://upstream.test", strings.NewReader("body"))
	response, err = transport.RoundTrip(request)
	if err != nil || response.StatusCode != http.StatusBadGateway || calls != 1 {
		t.Fatalf("POST retry result = response=%v err=%v calls=%d", response, err, calls)
	}
}

func TestCircuitBreakerOpensAfterFailures(t *testing.T) {
	calls := 0
	transport := &retryTransport{
		breaker: &circuitBreaker{threshold: 2, cooldown: time.Hour},
		base: roundTripFunc(func(_ *http.Request) (*http.Response, error) {
			calls++
			return nil, errors.New("connection failed")
		}),
	}
	request := httptest.NewRequest(http.MethodGet, "http://upstream.test", nil)
	_, _ = transport.RoundTrip(request)
	_, _ = transport.RoundTrip(request)
	_, err := transport.RoundTrip(request)
	if err == nil || calls != 2 {
		t.Fatalf("circuit result = err=%v calls=%d", err, calls)
	}
}

type roundTripFunc func(*http.Request) (*http.Response, error)

func (function roundTripFunc) RoundTrip(request *http.Request) (*http.Response, error) {
	return function(request)
}

func TestHealthChecksSelectOnlyHealthyEndpoints(t *testing.T) {
	healthy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.URL.Path == "/health" {
			response.WriteHeader(http.StatusNoContent)
			return
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer healthy.Close()
	unhealthy := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusServiceUnavailable)
	}))
	defer unhealthy.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{unhealthy.URL, healthy.URL}, HealthPath: "/health"}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", Upstream: "api"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	pool := server.runtime.Load().routes[0].upstream
	pool.checkHealth(context.Background())
	if pool.healthy[0].Load() || !pool.healthy[1].Load() {
		t.Fatalf("health states = [%v, %v]", pool.healthy[0].Load(), pool.healthy[1].Load())
	}
	response := serve(server, http.MethodGet, "api.example.com", "/", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("healthy failover status = %d", response.Code)
	}
}

type testAuthorizer struct {
	allow bool
}

func (authorizer testAuthorizer) Authorize(response http.ResponseWriter, request *http.Request, scopes []string) bool {
	if !authorizer.allow {
		http.Error(response, "denied", http.StatusUnauthorized)
		return false
	}
	request.Header.Set("X-Authenticated-Subject", "trusted-user")
	return true
}

func TestProtectedRouteUsesAuthorizer(t *testing.T) {
	upstream := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		if request.Header.Get("X-Authenticated-Subject") != "trusted-user" {
			t.Error("trusted identity header was not forwarded")
		}
		response.WriteHeader(http.StatusNoContent)
	}))
	defer upstream.Close()

	cfg := config.Default()
	cfg.Auth.Mode = "bearer"
	cfg.Auth.IssuerURL = "https://issuer.example.com"
	cfg.Auth.ClientID = "gateway"
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{upstream.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "private", Host: "private.example.com", RequireAuth: true, Upstream: "api"}}
	server, err := NewServer(cfg, testAuthorizer{allow: true})
	if err != nil {
		t.Fatal(err)
	}
	response := serve(server, http.MethodGet, "private.example.com", "/", "")
	if response.Code != http.StatusNoContent {
		t.Fatalf("protected route status = %d", response.Code)
	}

	withoutAuthorizer, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	response = serve(withoutAuthorizer, http.MethodGet, "private.example.com", "/", "")
	if response.Code != http.StatusServiceUnavailable {
		t.Fatalf("missing authorizer status = %d, want %d", response.Code, http.StatusServiceUnavailable)
	}
}

func TestReconfigureAtomicallyChangesUpstream(t *testing.T) {
	first := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("first"))
	}))
	defer first.Close()
	second := httptest.NewServer(http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		_, _ = response.Write([]byte("second"))
	}))
	defer second.Close()

	cfg := config.Default()
	cfg.Upstreams = []config.UpstreamConfig{{Name: "api", URLs: []string{first.URL}}}
	cfg.Routes = []config.RouteConfig{{Name: "api", Host: "api.example.com", Upstream: "api"}}
	server, err := NewServer(cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	if response := serve(server, http.MethodGet, "api.example.com", "/", ""); response.Body.String() != "first" {
		t.Fatalf("initial response = %q", response.Body.String())
	}

	updated := cfg.Clone()
	updated.Upstreams[0].URLs = []string{second.URL}
	if err := server.Reconfigure(updated); err != nil {
		t.Fatal(err)
	}
	if response := serve(server, http.MethodGet, "api.example.com", "/", ""); response.Body.String() != "second" {
		t.Fatalf("reconfigured response = %q", response.Body.String())
	}
}

func serve(server *Server, method, host, requestPath, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+host+requestPath, strings.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
