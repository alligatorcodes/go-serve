package dataplane

import (
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

func serve(server *Server, method, host, requestPath, body string) *httptest.ResponseRecorder {
	request := httptest.NewRequest(method, "http://"+host+requestPath, strings.NewReader(body))
	response := httptest.NewRecorder()
	server.Handler().ServeHTTP(response, request)
	return response
}
