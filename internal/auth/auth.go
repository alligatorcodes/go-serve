package auth

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/alligatorcodes/go-serve/internal/config"
	"github.com/alligatorcodes/go-serve/internal/observability"
	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

type Claims struct {
	Subject string    `json:"sub"`
	Email   string    `json:"email,omitempty"`
	Scopes  []string  `json:"scope,omitempty"`
	Expiry  time.Time `json:"exp"`
	Nonce   string    `json:"nonce,omitempty"`
}

type Verifier interface {
	Verify(context.Context, string) (Claims, error)
}

type Middleware struct {
	mode         string
	verifier     Verifier
	oauth        *oauth2.Config
	cookieName   string
	cookieKey    []byte
	secureCookie bool
	states       map[string]state
	mu           sync.Mutex
	clock        func() time.Time
}

type state struct {
	returnPath string
	verifier   string
	nonce      string
	expires    time.Time
}

func New(ctx context.Context, cfg config.AuthConfig) (*Middleware, error) {
	provider, err := oidc.NewProvider(ctx, cfg.IssuerURL)
	if err != nil {
		return nil, fmt.Errorf("discover OIDC provider: %w", err)
	}
	verifier := provider.Verifier(&oidc.Config{ClientID: cfg.ClientID})
	var key []byte
	if cfg.Mode == "oidc" {
		key, err = os.ReadFile(cfg.SessionSecretFile)
		if err != nil {
			return nil, fmt.Errorf("read session secret: %w", err)
		}
		if len(key) < 32 {
			return nil, errors.New("session secret must contain at least 32 bytes")
		}
	} else {
		key = make([]byte, 32)
		if _, err := io.ReadFull(rand.Reader, key); err != nil {
			return nil, fmt.Errorf("create bearer verifier key: %w", err)
		}
	}
	middleware := newMiddleware(cfg.Mode, oidcVerifier{verifier: verifier}, key, cfg.SessionCookieName)
	if cfg.Mode == "oidc" {
		secret, err := os.ReadFile(cfg.ClientSecretFile)
		if err != nil {
			return nil, fmt.Errorf("read OIDC client secret: %w", err)
		}
		middleware.oauth = &oauth2.Config{
			ClientID:     cfg.ClientID,
			ClientSecret: strings.TrimSpace(string(secret)),
			Endpoint:     provider.Endpoint(),
			RedirectURL:  cfg.RedirectURL,
			Scopes:       cfg.AllowedScopes,
		}
		parsed, err := url.Parse(cfg.RedirectURL)
		if err != nil || parsed.Scheme == "" || parsed.Host == "" {
			return nil, errors.New("redirect_url must be an absolute URL")
		}
		if parsed.Scheme != "https" && !cfg.AllowInsecureRedirect {
			return nil, errors.New("OIDC redirect_url must use HTTPS unless insecure redirects are explicitly enabled")
		}
		middleware.secureCookie = parsed.Scheme == "https"
	}
	return middleware, nil
}

func NewForTest(mode string, verifier Verifier, key []byte) *Middleware {
	return newMiddleware(mode, verifier, key, "test_session")
}

func newMiddleware(mode string, verifier Verifier, key []byte, cookieName string) *Middleware {
	return &Middleware{
		mode:       mode,
		verifier:   verifier,
		cookieName: cookieName,
		cookieKey:  append([]byte(nil), key...),
		states:     make(map[string]state),
		clock:      time.Now,
	}
}

func (m *Middleware) Handler(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oauth2/login":
			m.login(response, request)
		case "/oauth2/callback":
			m.callback(response, request)
		case "/oauth2/logout":
			m.logout(response, request)
		default:
			next.ServeHTTP(response, request)
		}
	})
}

func (m *Middleware) Protect(next http.Handler) http.Handler {
	return http.HandlerFunc(func(response http.ResponseWriter, request *http.Request) {
		switch request.URL.Path {
		case "/oauth2/login", "/oauth2/callback", "/oauth2/logout":
			m.Handler(next).ServeHTTP(response, request)
			return
		}
		if m.Authorize(response, request, nil) {
			next.ServeHTTP(response, request)
		}
	})
}

func (m *Middleware) Authorize(response http.ResponseWriter, request *http.Request, requiredScopes []string) bool {
	identity, ok := m.authenticate(request)
	if !ok {
		observability.SetAuthOutcome(request, "failure")
		if m.mode == "oidc" && request.Method == http.MethodGet && acceptsHTML(request) {
			m.redirectToLogin(response, request)
		} else {
			writeUnauthorized(response)
		}
		return false
	}
	if !hasScopes(identity.Scopes, requiredScopes) {
		observability.SetAuthOutcome(request, "forbidden")
		http.Error(response, "insufficient scope", http.StatusForbidden)
		return false
	}
	stripIdentityHeaders(request)
	observability.SetAuthOutcome(request, "success")
	request.Header.Set("X-Authenticated-Subject", identity.Subject)
	if identity.Email != "" {
		request.Header.Set("X-Authenticated-Email", identity.Email)
	}
	request.Header.Set("X-Authenticated-Scopes", strings.Join(identity.Scopes, " "))
	return true
}

func (m *Middleware) authenticate(request *http.Request) (Claims, bool) {
	if cookie, err := request.Cookie(m.cookieName); err == nil {
		if claims, ok := m.openSession(cookie.Value); ok {
			return claims, true
		}
	}
	if m.mode == "bearer" || request.Header.Get("Authorization") != "" {
		const prefix = "Bearer "
		header := request.Header.Get("Authorization")
		if !strings.HasPrefix(header, prefix) || m.verifier == nil {
			return Claims{}, false
		}
		claims, err := m.verifier.Verify(request.Context(), strings.TrimSpace(strings.TrimPrefix(header, prefix)))
		if err != nil || !validClaims(claims, m.clock()) {
			return Claims{}, false
		}
		return claims, true
	}
	return Claims{}, false
}

func (m *Middleware) login(response http.ResponseWriter, request *http.Request) {
	if m.oauth == nil {
		http.Error(response, "OIDC login is not configured", http.StatusNotFound)
		return
	}
	stateToken, err := randomToken(32)
	if err != nil {
		http.Error(response, "unable to create login state", http.StatusInternalServerError)
		return
	}
	verifier, err := randomToken(32)
	if err != nil {
		http.Error(response, "unable to create PKCE verifier", http.StatusInternalServerError)
		return
	}
	nonce, err := randomToken(32)
	if err != nil {
		http.Error(response, "unable to create OIDC nonce", http.StatusInternalServerError)
		return
	}
	returnPath := request.URL.Query().Get("return")
	if !safeReturnPath(returnPath) {
		returnPath = "/"
	}
	m.mu.Lock()
	m.states[stateToken] = state{returnPath: returnPath, verifier: verifier, nonce: nonce, expires: m.clock().Add(10 * time.Minute)}
	m.mu.Unlock()
	http.Redirect(response, request, m.oauth.AuthCodeURL(stateToken, oauth2.S256ChallengeOption(verifier), oauth2.SetAuthURLParam("nonce", nonce)), http.StatusFound)
}

func (m *Middleware) callback(response http.ResponseWriter, request *http.Request) {
	if m.oauth == nil {
		http.Error(response, "OIDC login is not configured", http.StatusNotFound)
		return
	}
	stateToken := request.URL.Query().Get("state")
	m.mu.Lock()
	loginState, ok := m.states[stateToken]
	delete(m.states, stateToken)
	m.mu.Unlock()
	if !ok || loginState.expires.Before(m.clock()) {
		http.Error(response, "invalid login state", http.StatusBadRequest)
		return
	}
	code := request.URL.Query().Get("code")
	token, err := m.oauth.Exchange(request.Context(), code, oauth2.VerifierOption(loginState.verifier))
	if err != nil {
		http.Error(response, "authorization code exchange failed", http.StatusUnauthorized)
		return
	}
	rawIDToken, ok := token.Extra("id_token").(string)
	if !ok {
		http.Error(response, "identity token missing", http.StatusUnauthorized)
		return
	}
	claims, err := m.verifier.Verify(request.Context(), rawIDToken)
	if err != nil || !validClaims(claims, m.clock()) || !validNonce(claims.Nonce, loginState.nonce) {
		http.Error(response, "identity token invalid", http.StatusUnauthorized)
		return
	}
	value, err := m.sealSession(claims)
	if err != nil {
		http.Error(response, "unable to create session", http.StatusInternalServerError)
		return
	}
	http.SetCookie(response, &http.Cookie{Name: m.cookieName, Value: value, Path: "/", HttpOnly: true, Secure: m.secureCookie, SameSite: http.SameSiteLaxMode, Expires: claims.Expiry})
	http.Redirect(response, request, loginState.returnPath, http.StatusFound)
}

func (m *Middleware) logout(response http.ResponseWriter, request *http.Request) {
	http.SetCookie(response, &http.Cookie{Name: m.cookieName, Value: "", Path: "/", MaxAge: -1, HttpOnly: true, Secure: m.secureCookie, SameSite: http.SameSiteLaxMode})
	http.Redirect(response, request, "/", http.StatusFound)
}

func (m *Middleware) redirectToLogin(response http.ResponseWriter, request *http.Request) {
	path := request.URL.RequestURI()
	http.Redirect(response, request, "/oauth2/login?return="+url.QueryEscape(path), http.StatusFound)
}

func (m *Middleware) sealSession(claims Claims) (string, error) {
	block, err := aes.NewCipher(keyBytes(m.cookieKey))
	if err != nil {
		return "", err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return "", err
	}
	payload, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	nonce := make([]byte, gcm.NonceSize())
	if _, err := io.ReadFull(rand.Reader, nonce); err != nil {
		return "", err
	}
	sealed := gcm.Seal(nonce, nonce, payload, nil)
	return base64.RawURLEncoding.EncodeToString(sealed), nil
}

func (m *Middleware) openSession(value string) (Claims, bool) {
	sealed, err := base64.RawURLEncoding.DecodeString(value)
	if err != nil {
		return Claims{}, false
	}
	block, err := aes.NewCipher(keyBytes(m.cookieKey))
	if err != nil {
		return Claims{}, false
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil || len(sealed) < gcm.NonceSize() {
		return Claims{}, false
	}
	claimsBytes, err := gcm.Open(nil, sealed[:gcm.NonceSize()], sealed[gcm.NonceSize():], nil)
	if err != nil {
		return Claims{}, false
	}
	var claims Claims
	if json.Unmarshal(claimsBytes, &claims) != nil || !validClaims(claims, m.clock()) {
		return Claims{}, false
	}
	return claims, true
}

func keyBytes(value []byte) []byte {
	hash := sha256.Sum256(value)
	return hash[:]
}

func validClaims(claims Claims, now time.Time) bool {
	return claims.Subject != "" && claims.Expiry.After(now)
}

func validNonce(actual, expected string) bool {
	return expected != "" && actual != "" && actual == expected
}

func hasScopes(granted, required []string) bool {
	set := make(map[string]struct{}, len(granted))
	for _, scope := range granted {
		set[scope] = struct{}{}
	}
	for _, scope := range required {
		if _, ok := set[scope]; !ok {
			return false
		}
	}
	return true
}

func stripIdentityHeaders(request *http.Request) {
	request.Header.Del("X-Authenticated-Subject")
	request.Header.Del("X-Authenticated-Email")
	request.Header.Del("X-Authenticated-Scopes")
}

func acceptsHTML(request *http.Request) bool {
	return strings.Contains(request.Header.Get("Accept"), "text/html")
}

func safeReturnPath(value string) bool {
	return value == "" || (strings.HasPrefix(value, "/") && !strings.HasPrefix(value, "//"))
}

func randomToken(size int) (string, error) {
	value := make([]byte, size)
	if _, err := io.ReadFull(rand.Reader, value); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(value), nil
}

func writeUnauthorized(response http.ResponseWriter) {
	response.Header().Set("WWW-Authenticate", `Bearer realm="go-serve"`)
	http.Error(response, "authentication required", http.StatusUnauthorized)
}

type oidcVerifier struct {
	verifier *oidc.IDTokenVerifier
}

func (v oidcVerifier) Verify(ctx context.Context, raw string) (Claims, error) {
	token, err := v.verifier.Verify(ctx, raw)
	if err != nil {
		return Claims{}, err
	}
	var value struct {
		Subject string `json:"sub"`
		Email   string `json:"email"`
		Scope   string `json:"scope"`
		Expiry  int64  `json:"exp"`
		Nonce   string `json:"nonce"`
	}
	if err := token.Claims(&value); err != nil {
		return Claims{}, err
	}
	return Claims{Subject: value.Subject, Email: value.Email, Scopes: strings.Fields(value.Scope), Expiry: time.Unix(value.Expiry, 0), Nonce: value.Nonce}, nil
}
