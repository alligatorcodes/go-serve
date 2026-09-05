package auth

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type fakeVerifier struct {
	claims Claims
	err    error
}

func (fake fakeVerifier) Verify(context.Context, string) (Claims, error) {
	return fake.claims, fake.err
}

func TestBearerAuthorizationSetsTrustedIdentityHeaders(t *testing.T) {
	middleware := NewForTest("bearer", fakeVerifier{claims: Claims{
		Subject: "user-123",
		Email:   "user@example.com",
		Scopes:  []string{"api:read", "profile"},
		Expiry:  time.Now().Add(time.Hour),
	}}, []byte("a sufficiently long test secret for AES"))
	request := httptest.NewRequest(http.MethodGet, "http://example.test/api", nil)
	request.Header.Set("Authorization", "Bearer token")
	request.Header.Set("X-Authenticated-Subject", "spoofed")
	response := httptest.NewRecorder()

	if !middleware.Authorize(response, request, []string{"api:read"}) {
		t.Fatal("authorization failed")
	}
	if request.Header.Get("X-Authenticated-Subject") != "user-123" || request.Header.Get("X-Authenticated-Email") != "user@example.com" {
		t.Fatalf("unexpected identity headers: %v", request.Header)
	}
}

func TestBearerAuthorizationRejectsMissingScope(t *testing.T) {
	middleware := NewForTest("bearer", fakeVerifier{claims: Claims{
		Subject: "user-123",
		Scopes:  []string{"profile"},
		Expiry:  time.Now().Add(time.Hour),
	}}, []byte("a sufficiently long test secret for AES"))
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.Header.Set("Authorization", "Bearer token")
	response := httptest.NewRecorder()

	if middleware.Authorize(response, request, []string{"api:read"}) {
		t.Fatal("authorization unexpectedly succeeded")
	}
	if response.Code != http.StatusForbidden {
		t.Fatalf("status = %d, want %d", response.Code, http.StatusForbidden)
	}
}

func TestSessionCookieIsEncryptedAndExpires(t *testing.T) {
	middleware := NewForTest("oidc", fakeVerifier{}, []byte("a sufficiently long test secret for AES"))
	claims := Claims{Subject: "user-123", Expiry: time.Now().Add(time.Hour)}
	value, err := middleware.sealSession(claims)
	if err != nil {
		t.Fatal(err)
	}
	request := httptest.NewRequest(http.MethodGet, "/", nil)
	request.AddCookie(&http.Cookie{Name: middleware.cookieName, Value: value})
	response := httptest.NewRecorder()
	if !middleware.Authorize(response, request, nil) {
		t.Fatal("valid session was rejected")
	}

	middleware.clock = func() time.Time { return time.Now().Add(2 * time.Hour) }
	expiredResponse := httptest.NewRecorder()
	if middleware.Authorize(expiredResponse, request, nil) {
		t.Fatal("expired session was accepted")
	}
	if expiredResponse.Code != http.StatusUnauthorized {
		t.Fatalf("expired status = %d, want %d", expiredResponse.Code, http.StatusUnauthorized)
	}
}

func TestBrowserRequestsRedirectToLoginWithSafeReturnPath(t *testing.T) {
	middleware := NewForTest("oidc", fakeVerifier{}, []byte("a sufficiently long test secret for AES"))
	request := httptest.NewRequest(http.MethodGet, "https://example.test/private?next=1", nil)
	request.Header.Set("Accept", "text/html")
	response := httptest.NewRecorder()

	if middleware.Authorize(response, request, nil) {
		t.Fatal("unauthenticated request was authorized")
	}
	location := response.Header().Get("Location")
	if response.Code != http.StatusFound || !strings.HasPrefix(location, "/oauth2/login?return=") || strings.Contains(location, "https://") {
		t.Fatalf("unsafe login redirect: status=%d location=%q", response.Code, location)
	}
}

func TestProtectRequiresAuthenticationForAdminRoutes(t *testing.T) {
	middleware := NewForTest("bearer", fakeVerifier{}, []byte("a sufficiently long test secret for AES"))
	next := http.HandlerFunc(func(response http.ResponseWriter, _ *http.Request) {
		response.WriteHeader(http.StatusNoContent)
	})
	request := httptest.NewRequest(http.MethodGet, "/api/docs", nil)
	response := httptest.NewRecorder()
	middleware.Protect(next).ServeHTTP(response, request)
	if response.Code != http.StatusUnauthorized {
		t.Fatalf("protected status = %d, want %d", response.Code, http.StatusUnauthorized)
	}
}

func TestOIDCNonceMustMatchLoginState(t *testing.T) {
	if !validNonce("nonce-a", "nonce-a") {
		t.Fatal("matching nonce was rejected")
	}
	for _, pair := range [][2]string{{"", "nonce-a"}, {"nonce-a", ""}, {"nonce-a", "nonce-b"}} {
		if validNonce(pair[0], pair[1]) {
			t.Fatalf("invalid nonce pair accepted: %#v", pair)
		}
	}
}
