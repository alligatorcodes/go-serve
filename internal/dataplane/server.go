package dataplane
package dataplane

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"path"
	"strings"
	"sync/atomic"
	"time"

	"github.com/example/go-serve/internal/config"
)

type Server struct {
	routes     []compiledRoute
	maxInFlight chan struct{}
	maxBody    int64
}

type compiledRoute struct {
	host       string
	pathPrefix string
	methods    map[string]struct{}
	upstream   *upstreamPool
}

type upstreamPool struct {
	endpoints       []*url.URL
	next            atomic.Uint64
	requestTimeout  time.Duration
	transport       *http.Transport
}

func NewServer(cfg config.Config) (*Server, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	upstreams := make(map[string]*upstreamPool, len(cfg.Upstreams))
	for _, upstream := range cfg.Upstreams {
		if _, exists := upstreams[upstream.Name]; exists {
			return nil, fmt.Errorf("duplicate upstream %q", upstream.Name)
		}
		if len(upstream.URLs) == 0 {
			return nil, fmt.Errorf("upstream %q has no URLs", upstream.Name)
		}
		endpoints := make([]*url.URL, 0, len(upstream.URLs))
		for _, rawURL := range upstream.URLs {
			target, err := url.Parse(rawURL)
			if err != nil || target.Scheme == "" || target.Host == "" {
				return nil, fmt.Errorf("upstream %q has invalid URL %q", upstream.Name, rawURL)
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
		upstreams[upstream.Name] = &upstreamPool{
			endpoints:      endpoints,
			requestTimeout: requestTimeout,
			transport: &http.Transport{
				Proxy:                 http.ProxyFromEnvironment,
				DialContext:           (&net.Dialer{Timeout: dialTimeout, KeepAlive: 30 * time.Second}).DialContext,
				ForceAttemptHTTP2:     true,
				MaxIdleConns:          256,
				MaxIdleConnsPerHost:   32,
				IdleConnTimeout:       90 * time.Second,
				TLSHandshakeTimeout:   10 * time.Second,
				ExpectContinueTimeout: 1 * time.Second,
			},
		}
	}

	routes := make([]compiledRoute, 0, len(cfg.Routes))
	for _, route := range cfg.Routes {
		upstream, ok := upstreams[route.Upstream]
		if !ok {
			return nil, fmt.Errorf("route %q references unknown upstream %q", route.Name, route.Upstream)
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
			host:       normalizeHost(route.Host),
			pathPrefix: prefix,
			methods:    methods,
			upstream:   upstream,
		})
	}

	return &Server{
		routes:      routes,
		maxInFlight: make(chan struct{}, cfg.Limits.MaxInFlight),
		maxBody:     cfg.Limits.MaxBodyBytes,
	}, nil
}

func (s *Server) Handler() http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		select {
		case s.maxInFlight <- struct{}{}:
			defer func() { <-s.maxInFlight }()
		default:
			http.Error(response, "server is busy", http.StatusServiceUnavailable)
			return
		}
		request.Body = http.MaxBytesReader(response, request.Body, s.maxBody)
		route, methodAllowed := s.match(request)
		if route == nil {
			if methodAllowed {
				response.Header().Set("Allow", "GET, HEAD, POST, PUT, DELETE, PATCH, OPTIONS")
				http.Error(response, "method not allowed", http.StatusMethodNotAllowed)
				return
			}
			http.NotFound(response, request)
			return
		}
		ctx, cancel := context.WithTimeout(request.Context(), route.upstream.requestTimeout)
		defer cancel()
		route.upstream.proxy().ServeHTTP(response, request.WithContext(ctx))
	})
}

func (s *Server) match(request *http.Request) (*compiledRoute, bool) {
	host := normalizeHost(request.Host)
	var best *compiledRoute
	methodAllowed := false
	for index := range s.routes {
		route := &s.routes[index]
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

func (u *upstreamPool) proxy() *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Director: func(request *http.Request) {
			target := u.endpoints[u.next.Add(1)%uint64(len(u.endpoints))]
			request.URL.Scheme = target.Scheme
			request.URL.Host = target.Host
			request.Host = target.Host
			request.URL.Path = joinURLPath(target.Path, request.URL.Path)
			request.URL.RawPath = ""
		},
		Transport: u.transport,
		ErrorHandler: func(response http.ResponseWriter, _ *http.Request, err error) {
			http.Error(response, "upstream unavailable", http.StatusBadGateway)
		},
		FlushInterval: 100 * time.Millisecond,
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
	return path.Join(base, requestPath)
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

var _ io.Reader = (*limitedConn)(nil)
