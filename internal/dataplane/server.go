package dataplane

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"reflect"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/alligatorcodes/go-serve/internal/config"
)

type Server struct {
	runtime atomic.Pointer[runtimeState]
}

type runtimeState struct {
	cfg         config.Config
	routes      []compiledRoute
	maxInFlight chan struct{}
	maxBody     int64
	authorizer  Authorizer
	stop        context.CancelFunc
}

type Authorizer interface {
	Authorize(http.ResponseWriter, *http.Request, []string) bool
}

type compiledRoute struct {
	host        string
	pathPrefix  string
	methods     map[string]struct{}
	headers     map[string]string
	requireAuth bool
	scopes      []string
	upstream    *upstreamPool
}

type upstreamPool struct {
	endpoints       []*url.URL
	next            atomic.Uint64
	requestTimeout  time.Duration
	healthPath      string
	healthInterval  time.Duration
	retryAttempts   int
	transport       http.RoundTripper
	healthTransport *http.Transport
	proxy           *httputil.ReverseProxy
	healthy         []atomic.Bool
	breakers        []*circuitBreaker
}

func NewServer(cfg config.Config, authorizers ...Authorizer) (*Server, error) {
	server := &Server{}
	if err := server.ReconfigureWithAuthorizer(cfg, firstAuthorizer(authorizers)); err != nil {
		return nil, err
	}
	return server, nil
}

func (s *Server) Reconfigure(cfg config.Config) error {
	current := s.runtime.Load()
	if current != nil && (!reflect.DeepEqual(current.cfg.Server, cfg.Server) || !reflect.DeepEqual(current.cfg.Auth, cfg.Auth) || !reflect.DeepEqual(current.cfg.Cluster, cfg.Cluster)) {
		return fmt.Errorf("server listeners, authentication, and cluster settings require restart")
	}
	return s.ReconfigureWithAuthorizer(cfg, currentAuthorizer(current))
}

func (s *Server) ReconfigureWithAuthorizer(cfg config.Config, authorizer Authorizer) error {
	if err := cfg.Validate(); err != nil {
		return err
	}
	upstreams := make(map[string]*upstreamPool, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		if _, exists := upstreams[upstream.Name]; exists {
			return fmt.Errorf("duplicate upstream %q", upstream.Name)
		}
		if len(upstream.URLs) == 0 {
			return fmt.Errorf("upstream %q has no URLs", upstream.Name)
		}
		endpoints := make([]*url.URL, 0, len(upstream.URLs))
		for _, rawURL := range upstream.URLs {
			target, err := url.Parse(rawURL)
			if err != nil || target.Scheme == "" || target.Host == "" {
				return fmt.Errorf("upstream %q has invalid URL %q", upstream.Name, rawURL)
			}
			endpoints = append(endpoints, target)
		}
		dialTimeout := upstream.DialTimeout
		if dialTimeout <= 0 {
			dialTimeout = 5 * time.Second
		}
		requestTimeout := upstream.RequestTimeout
		if requestTimeout <= 0 {
			requestTimeout = 30 * time.Second
		}
		healthInterval := upstream.HealthInterval
		if healthInterval <= 0 {
			healthInterval = 10 * time.Second
		}
		retryAttempts := upstream.RetryAttempts
		if retryAttempts < 0 {
			retryAttempts = 0
		}
		threshold := upstream.CircuitBreakerThreshold
		if threshold <= 0 {
			threshold = 5
		}
		cooldown := upstream.CircuitBreakerCooldown
		if cooldown <= 0 {
			cooldown = 10 * time.Second
		}
		healthy := make([]atomic.Bool, len(endpoints))
		breakers := make([]*circuitBreaker, len(endpoints))
		for index := range healthy {
			healthy[index].Store(true)
			breakers[index] = &circuitBreaker{threshold: threshold, cooldown: cooldown}
		}
		rawTransport := &http.Transport{
			Proxy:                 http.ProxyFromEnvironment,
			DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          256,
			MaxIdleConnsPerHost:   32,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: 1 * time.Second,
		}
		pool := &upstreamPool{
			endpoints:       endpoints,
			requestTimeout:  requestTimeout,
			healthPath:      upstream.HealthPath,
			healthInterval:  healthInterval,
			retryAttempts:   retryAttempts,
			healthy:         healthy,
			breakers:        breakers,
			healthTransport: rawTransport,
			transport:       rawTransport,
		}
		pool.transport = &retryTransport{base: rawTransport, attempts: retryAttempts, breakers: breakers}
		pool.proxy = newProxy(pool)
		upstreams[upstream.Name] = pool
	}

	routes := make([]compiledRoute, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		upstream, ok := upstreams[route.Upstream]
		if !ok {
			return fmt.Errorf("route %q references unknown upstream %q", route.Name, route.Upstream)
		}
		prefix := route.PathPrefix
		if prefix == "" {
			prefix = "/"
		}
		methods := make(map[string]struct{}, len(route.Methods))
		for _, method := range route.Methods {
			methods[strings.ToUpper(method)] = struct{}{}
		}
		routes = append(routes, compiledRoute{
			host:        normalizeHost(route.Host),
			pathPrefix:  prefix,
			methods:     methods,
			requireAuth: route.RequireAuth,
			scopes:      append([]string(nil), route.Scopes...),
			headers:     cloneHeaders(route.Headers),
			upstream:    upstream,
		})
	}

	serverContext, stop := context.WithCancel(context.Background())
	state := &runtimeState{
		cfg:         cfg.Clone(),
		routes:      routes,
		maxInFlight: make(chan struct{}, cfg.Limits.MaxInFlight),
		maxBody:     cfg.Limits.MaxBodyBytes,
		authorizer:  authorizer,
		stop:        stop,
	}
	for _, upstream := range upstreams {
		if upstream.healthPath != "" {
			go upstream.healthLoop(serverContext)
		}
	}
	previous := s.runtime.Swap(state)
	if previous != nil && previous.stop != nil {
		previous.stop()
	}
	return nil
}

func (s *Server) Close() {
	current := s.runtime.Load()
	if current != nil && current.stop != nil {
		current.stop()
	}
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		runtime := s.runtime.Load()
		if runtime == nil {
			http.Error(response, "data plane is unavailable", http.StatusServiceUnavailable)
			return
		}
		select {
		case runtime.maxInFlight <- struct{}{}:
			defer func() { <-runtime.maxInFlight }()
		default:
			http.Error(response, "server is busy", http.StatusServiceUnavailable)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, runtime.maxBody)
		stripTrustedHeaders(request)
		route, methodAllowed := match(runtime.routes, request)
		if route == nil {
			if methodAllowed {
				response.Header().Set("Allow", "GET, HEAD, POST, PUT, DELETE, PATCH, OPTIONS")
				http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			http.NotFound(response, request)
			return
		}
		if route.requireAuth {
			if runtime.authorizer == nil || !runtime.authorizer.Authorize(response, request, route.scopes) {
				if runtime.authorizer == nil {
					http.Error(response, "authentication is not configured", http.StatusServiceUnavailable)
				}
				return
			}
		}
		if !route.upstream.hasHealthyEndpoint() {
			http.Error(response, "upstream unavailable", http.StatusServiceUnavailable)
			return
		}
		applyRouteHeaders(request, route.headers)
		ctx, cancel := context.WithTimeout(request.Context(), route.upstream.requestTimeout)
		defer cancel()
		route.upstream.proxy.ServeHTTP(response, request.WithContext(ctx))
	})
}

func currentAuthorizer(runtime *runtimeState) Authorizer {
	if runtime == nil {
		return nil
	}
	return runtime.authorizer
}

func firstAuthorizer(authorizers []Authorizer) Authorizer {
	if len(authorizers) == 0 {
		return nil
	}
	return authorizers[0]
}

func match(routes []compiledRoute, request *http.Request) (*compiledRoute, bool) {
	host := normalizeHost(request.Host)
	var best *compiledRoute
	methodAllowed := false
	for index := range routes {
		route := &routes[index]
		if route.host != "" && route.host != host {
			continue
		}
		if !pathMatches(route.pathPrefix, request.URL.Path) {
			continue
		}
		if len(route.methods) > 0 {
			if _, ok := route.methods[request.Method]; !ok {
				methodAllowed = true
				continue
			}
		}
		if best == nil || len(route.pathPrefix) > len(best.pathPrefix) {
			best = route
		}
	}
	return best, methodAllowed
}

func newProxy(u *upstreamPool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			target, index := u.pick()
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			request.Host = target.Host
			joinProxyPath(target, request.URL)
			*request = *request.WithContext(context.WithValue(request.Context(), endpointIndexKey{}, index))
		},
		Transport: u.transport,
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, err error) {
			http.Error(response, "upstream unavailable", http.StatusBadGateway)
		},
		FlushInterval: 100 * time.Millisecond,
	}
}

func (u *upstreamPool) pick() (*url.URL, int) {
	for index := uint64(0); index < uint64(len(u.endpoints)); index++ {
		candidate := (u.next.Add(1) + index) % uint64(len(u.endpoints))
		if u.healthy[candidate].Load() && u.breakers[candidate].allow() {
			return u.endpoints[candidate], int(candidate)
		}
	}
	return u.endpoints[0], 0
}

type retryTransport struct {
	base     http.RoundTripper
	attempts int
	breakers []*circuitBreaker
}

type endpointIndexKey struct{}

func (transport *retryTransport) RoundTrip(request *http.Request) (*http.Response, error) {
	index, ok := request.Context().Value(endpointIndexKey{}).(int)
	if !ok || index < 0 || index >= len(transport.breakers) || !transport.breakers[index].allow() {
		return nil, errors.New("upstream circuit is open")
	}
	safe := request.Method == http.MethodGet || request.Method == http.MethodHead || request.Method == http.MethodOptions || request.Method == http.MethodPut || request.Method == http.MethodDelete
	for attempt := 0; ; attempt++ {
		response, err := transport.base.RoundTrip(request)
		if err == nil && response != nil && response.StatusCode < http.StatusInternalServerError {
			transport.breakers[index].success()
			return response, nil
		}
		transport.breakers[index].failure()
		if attempt >= transport.attempts || !safe {
			return response, err
		}
		if response != nil {
			response.Body.Close()
		}
		if request.Body != nil && request.Body != http.NoBody {
			if request.GetBody == nil {
				return nil, err
			}
			body, bodyErr := request.GetBody()
			if bodyErr != nil {
				return nil, bodyErr
			}
			request.Body = body
		}
	}
}

type circuitBreaker struct {
	mu        sync.Mutex
	threshold int
	cooldown  time.Duration
	failures  int
	openUntil time.Time
	halfOpen  bool
}

func (breaker *circuitBreaker) allow() bool {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	if breaker.openUntil.IsZero() {
		return true
	}
	if time.Now().Before(breaker.openUntil) || breaker.halfOpen {
		return false
	}
	breaker.halfOpen = true
	return true
}

func (breaker *circuitBreaker) success() {
	breaker.mu.Lock()
	breaker.failures = 0
	breaker.openUntil = time.Time{}
	breaker.halfOpen = false
	breaker.mu.Unlock()
}

func (breaker *circuitBreaker) failure() {
	breaker.mu.Lock()
	defer breaker.mu.Unlock()
	breaker.failures++
	if breaker.failures >= breaker.threshold {
		breaker.openUntil = time.Now().Add(breaker.cooldown)
		breaker.halfOpen = false
	}
}

func (u *upstreamPool) hasHealthyEndpoint() bool {
	for index := range u.healthy {
		if u.healthy[index].Load() {
			return true
		}
	}
	return false
}

func (u *upstreamPool) healthLoop(ctx context.Context) {
	u.checkHealth(ctx)
	ticker := time.NewTicker(u.healthInterval)
	defer ticker.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			u.checkHealth(ctx)
		}
	}
}

func (u *upstreamPool) checkHealth(parent context.Context) {
	client := &http.Client{Transport: u.healthTransport, Timeout: u.requestTimeout}
	for index, endpoint := range u.endpoints {
		requestContext, cancel := context.WithTimeout(parent, u.requestTimeout)
		target := *endpoint
		target.Path = joinURLPath(target.Path, u.healthPath)
		request, err := http.NewRequestWithContext(requestContext, http.MethodGet, target.String(), nil)
		if err == nil {
			response, requestErr := client.Do(request)
			if requestErr == nil {
				u.healthy[index].Store(response.StatusCode >= 200 && response.StatusCode < 400)
				response.Body.Close()
			} else {
				u.healthy[index].Store(false)
			}
		} else {
			u.healthy[index].Store(false)
		}
		cancel()
	}
}

func pathMatches(prefix, requestPath string) bool {
	if prefix == "/" {
		return true
	}
	return requestPath == prefix || strings.HasPrefix(requestPath, strings.TrimSuffix(prefix, "/")+"/")
}

func normalizeHost(value string) string {
	if host, _, err := net.SplitHostPort(value); err == nil {
		value = host
	}
	return strings.ToLower(strings.TrimSuffix(value, "."))
}

func joinURLPath(base, requestPath string) string {
	if base == "" {
		return requestPath
	}
	if requestPath == "" {
		return base
	}
	if strings.HasSuffix(base, "/") && strings.HasPrefix(requestPath, "/") {
		return base + requestPath[1:]
	}
	if !strings.HasSuffix(base, "/") && !strings.HasPrefix(requestPath, "/") {
		return base + "/" + requestPath
	}
	return base + requestPath
}

func joinProxyPath(target *url.URL, requestURL *url.URL) {
	base := target.EscapedPath()
	requestPath := requestURL.EscapedPath()
	joined := joinURLPath(base, requestPath)
	decoded, err := url.PathUnescape(joined)
	if err != nil {
		return
	}
	requestURL.Path = decoded
	if decoded == joined {
		requestURL.RawPath = ""
	} else {
		requestURL.RawPath = joined
	}
}

func cloneHeaders(headers map[string]string) map[string]string {
	clone := make(map[string]string, len(headers))
	for key, value := range headers {
		clone[key] = value
	}
	return clone
}

func applyRouteHeaders(request *http.Request, headers map[string]string) {
	for key, value := range headers {
		canonical := http.CanonicalHeaderKey(key)
		switch canonical {
		case "X-Authenticated-Subject", "X-Authenticated-Email", "X-Authenticated-Scopes", "Authorization", "Cookie":
			continue
		default:
			request.Header.Set(canonical, value)
		}
	}
}

func stripTrustedHeaders(request *http.Request) {
	request.Header.Del("X-Authenticated-Subject")
	request.Header.Del("X-Authenticated-Email")
	request.Header.Del("X-Authenticated-Scopes")
}

// LimitListener applies an admission limit to active connections. Accepted
// connections release their slot when closed by net/http during shutdown.
func LimitListener(listener net.Listener, limit int64) net.Listener {
	if limit < 1 {
		return listener
	}
	return &limitedListener{Listener: listener, slots: make(chan struct{}, limit)}
}

type limitedListener struct {
	net.Listener
	slots chan struct{}
}

func (listener *limitedListener) Accept() (net.Conn, error) {
	listener.slots <- struct{}{}
	connection, err := listener.Listener.Accept()
	if err != nil {
		<-listener.slots
		return nil, err
	}
	return &limitedConn{Conn: connection, release: func() { <-listener.slots }}, nil
}

type limitedConn struct {
	net.Conn
	release func()
}

func (connection *limitedConn) Close() error {
	err := connection.Conn.Close()
	if connection.release != nil {
		connection.release()
		connection.release = nil
	}
	return err
}
