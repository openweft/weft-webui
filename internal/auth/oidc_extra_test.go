// oidc_extra_test.go — covers the OIDC handlers (login / callback /
// logout) and their helpers (state cookie encode/decode, redirect URL
// allow-list, PKCE challenge, randString).
//
// The full happy-path of CallbackHandler needs a real IdP (discovery +
// JWT verification), so we exercise the validation guards instead :
// missing state cookie, expired state, state mismatch, provider error
// query, and missing code. The token-exchange + ID-token branches are
// covered separately by RefreshSession_* in oidc_test.go.
package auth

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// newBareOIDC builds an *OIDC suitable for testing helpers and handlers
// that don't actually call the IdP. The verifier and provider are nil,
// which is fine for state-cookie / redirect / login-URL exercises.
func newBareOIDC(t *testing.T) *OIDC {
	t.Helper()
	s := NewSessionStore([]byte("hmac-key-bare"), "test_sess", "", false, 3600)
	o := &OIDC{
		session:   s,
		stateName: s.Name + "_oidc_state",
	}
	return o
}

// minimalOauth2Cfg builds an oauth2.Config pointing at the given base URL.
func minimalOauth2Cfg(base string) *oauth2.Config {
	return &oauth2.Config{
		ClientID:     "test-client",
		ClientSecret: "test-secret",
		Endpoint: oauth2.Endpoint{
			AuthURL:  base + "/authorize",
			TokenURL: base + "/token",
		},
		RedirectURL: "https://app.example.com/api/auth/callback",
		Scopes:      []string{"openid", "email", "profile"},
	}
}

func TestIsSafeReturn(t *testing.T) {
	cases := []struct {
		in   string
		want bool
	}{
		{"", false},
		{"/", true},
		{"/dashboard", true},
		{"/path/with?query=1", true},
		{"//evil.example.com", false},            // scheme-relative
		{"http://evil.example.com", false},       // absolute
		{"https://evil.example.com/path", false}, // absolute https
		{"javascript:alert(1)", false},           // dangerous scheme
		{"dashboard", false},                     // no leading slash
	}
	for _, tc := range cases {
		t.Run(tc.in, func(t *testing.T) {
			if got := isSafeReturn(tc.in); got != tc.want {
				t.Errorf("isSafeReturn(%q) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

func TestRandString_LengthAndUniqueness(t *testing.T) {
	a, err := randString(24)
	if err != nil {
		t.Fatalf("randString: %v", err)
	}
	b, err := randString(24)
	if err != nil {
		t.Fatalf("randString: %v", err)
	}
	if a == b {
		t.Errorf("two consecutive randString outputs collided ; entropy looks broken")
	}
	// base64.RawURLEncoding of 24 bytes is 32 chars.
	if len(a) != 32 {
		t.Errorf("randString(24) length = %d, want 32", len(a))
	}
}

func TestPKCEChallenge(t *testing.T) {
	const verifier = "the-secret-verifier"
	got := pkceChallenge(verifier)
	sum := sha256.Sum256([]byte(verifier))
	want := base64.RawURLEncoding.EncodeToString(sum[:])
	if got != want {
		t.Errorf("pkceChallenge = %q, want %q", got, want)
	}
	// No padding (RawURLEncoding contract).
	if strings.ContainsRune(got, '=') {
		t.Errorf("pkceChallenge output is padded : %q", got)
	}
}

func TestStateCookie_RoundTrip(t *testing.T) {
	o := newBareOIDC(t)
	in := &stateBlob{
		Nonce:        "n",
		State:        "s",
		CodeVerifier: "v",
		ReturnTo:     "/dash",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	rr := httptest.NewRecorder()
	if err := o.setStateCookie(rr, in); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != o.stateName {
		t.Errorf("cookie name = %q, want %q", c.Name, o.stateName)
	}
	if !c.HttpOnly {
		t.Error("state cookie must be HttpOnly")
	}
	if c.SameSite != http.SameSiteLaxMode {
		t.Errorf("state cookie SameSite = %v, want Lax", c.SameSite)
	}
	if c.Path != "/api/auth/" {
		t.Errorf("state cookie Path = %q", c.Path)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	req.AddCookie(c)
	out, err := o.readStateCookie(req)
	if err != nil {
		t.Fatalf("readStateCookie: %v", err)
	}
	if out.Nonce != "n" || out.State != "s" || out.CodeVerifier != "v" || out.ReturnTo != "/dash" {
		t.Errorf("state round-trip lost fields : %+v", out)
	}
}

func TestStateCookie_Missing(t *testing.T) {
	o := newBareOIDC(t)
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	if _, err := o.readStateCookie(req); err == nil {
		t.Fatalf("readStateCookie(no cookie) succeeded ; want error")
	}
}

func TestStateCookie_Malformed(t *testing.T) {
	o := newBareOIDC(t)

	cases := []struct {
		name  string
		value string
	}{
		{"no dot", "rawvalue"},
		{"empty", ""},
		{"bad base64", "!!!.deadbeef"},
		{"bad signature", "QQ.deadbeef"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
			req.AddCookie(&http.Cookie{Name: o.stateName, Value: tc.value})
			if _, err := o.readStateCookie(req); err == nil {
				t.Errorf("readStateCookie(%q) succeeded ; want error", tc.value)
			}
		})
	}
}

func TestStateCookie_Expired(t *testing.T) {
	o := newBareOIDC(t)
	in := &stateBlob{
		Nonce:        "n",
		State:        "s",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(-1 * time.Second).Unix(),
	}
	rr := httptest.NewRecorder()
	if err := o.setStateCookie(rr, in); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	for _, c := range rr.Result().Cookies() {
		req.AddCookie(c)
	}
	_, err := o.readStateCookie(req)
	if err == nil || !strings.Contains(err.Error(), "expired") {
		t.Errorf("readStateCookie(expired) err = %v, want expired", err)
	}
}

func TestStateCookie_BadJSON(t *testing.T) {
	o := newBareOIDC(t)
	// Build a payload that's correctly signed but isn't a valid stateBlob JSON.
	garbage := []byte("not-json")
	sig, err := signHex(o.session.Key, garbage)
	if err != nil {
		t.Fatalf("signHex: %v", err)
	}
	value := base64.RawURLEncoding.EncodeToString(garbage) + "." + sig

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	req.AddCookie(&http.Cookie{Name: o.stateName, Value: value})
	if _, err := o.readStateCookie(req); err == nil {
		t.Fatalf("readStateCookie(bad json) succeeded ; want error")
	}
}

func TestClearStateCookie(t *testing.T) {
	o := newBareOIDC(t)
	rr := httptest.NewRecorder()
	o.clearStateCookie(rr)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	if cookies[0].MaxAge != -1 {
		t.Errorf("clearStateCookie MaxAge = %d, want -1", cookies[0].MaxAge)
	}
}

func TestLoginHandler_SetsStateAndRedirects(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login?return_to=/dashboard", nil)
	rr := httptest.NewRecorder()
	o.LoginHandler(rr, req)

	if rr.Code != http.StatusFound {
		t.Fatalf("LoginHandler status = %d, want 302", rr.Code)
	}
	loc := rr.Result().Header.Get("Location")
	if !strings.HasPrefix(loc, "https://idp.example.com") {
		t.Errorf("Location = %q, want IdP URL", loc)
	}
	u, err := url.Parse(loc)
	if err != nil {
		t.Fatalf("parse redirect: %v", err)
	}
	q := u.Query()
	if q.Get("code_challenge_method") != "S256" {
		t.Errorf("code_challenge_method = %q, want S256", q.Get("code_challenge_method"))
	}
	if q.Get("code_challenge") == "" {
		t.Error("code_challenge missing")
	}
	if q.Get("nonce") == "" {
		t.Error("nonce missing")
	}
	if q.Get("state") == "" {
		t.Error("state missing")
	}

	// State cookie was set.
	cookies := rr.Result().Cookies()
	var stateCookie *http.Cookie
	for _, c := range cookies {
		if c.Name == o.stateName {
			stateCookie = c
			break
		}
	}
	if stateCookie == nil {
		t.Fatal("no state cookie issued")
	}
	// Decode and verify the ReturnTo we passed in survived.
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	req2.AddCookie(stateCookie)
	blob, err := o.readStateCookie(req2)
	if err != nil {
		t.Fatalf("readStateCookie: %v", err)
	}
	if blob.ReturnTo != "/dashboard" {
		t.Errorf("ReturnTo = %q, want /dashboard", blob.ReturnTo)
	}
	if blob.State != q.Get("state") {
		t.Errorf("state in cookie %q != state in redirect %q", blob.State, q.Get("state"))
	}
}

func TestLoginHandler_RejectsUnsafeReturnTo(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login?return_to=https://evil.example.com", nil)
	rr := httptest.NewRecorder()
	o.LoginHandler(rr, req)

	var stateCookie *http.Cookie
	for _, c := range rr.Result().Cookies() {
		if c.Name == o.stateName {
			stateCookie = c
		}
	}
	if stateCookie == nil {
		t.Fatal("no state cookie")
	}
	req2 := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	req2.AddCookie(stateCookie)
	blob, err := o.readStateCookie(req2)
	if err != nil {
		t.Fatalf("readStateCookie: %v", err)
	}
	if blob.ReturnTo != "/" {
		t.Errorf("unsafe return_to was kept : %q", blob.ReturnTo)
	}
}

func TestCallbackHandler_MissingStateCookie(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	called := ""
	o.OnLogin = func(r string) { called = r }

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?code=x&state=y", nil)
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no state cookie)", rr.Code)
	}
	if called != "failure" {
		t.Errorf("OnLogin called with %q, want failure", called)
	}
}

func TestCallbackHandler_ProviderError(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	blob := &stateBlob{
		State:        "s1",
		Nonce:        "n",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateRR := httptest.NewRecorder()
	if err := o.setStateCookie(stateRR, blob); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}

	called := ""
	o.OnLogin = func(r string) { called = r }

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?error=access_denied&error_description=user_cancelled", nil)
	for _, c := range stateRR.Result().Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401 (provider error)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "access_denied") {
		t.Errorf("body = %q, want to mention access_denied", rr.Body.String())
	}
	if called != "failure" {
		t.Errorf("OnLogin %q, want failure", called)
	}
}

func TestCallbackHandler_StateMismatch(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	blob := &stateBlob{
		State:        "EXPECTED",
		Nonce:        "n",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateRR := httptest.NewRecorder()
	if err := o.setStateCookie(stateRR, blob); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=ATTACKER&code=abc", nil)
	for _, c := range stateRR.Result().Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (state mismatch)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "state mismatch") {
		t.Errorf("body = %q, want to mention state mismatch", rr.Body.String())
	}
}

func TestCallbackHandler_MissingCode(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	blob := &stateBlob{
		State:        "S",
		Nonce:        "n",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateRR := httptest.NewRecorder()
	if err := o.setStateCookie(stateRR, blob); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=S", nil) // no code
	for _, c := range stateRR.Result().Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (no code)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no code") {
		t.Errorf("body = %q, want to mention no code", rr.Body.String())
	}
}

func TestCallbackHandler_TokenExchangeFailure(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "bang", http.StatusInternalServerError)
	}))
	t.Cleanup(srv.Close)

	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg(srv.URL)
	// Point TokenURL at the stub root so the 500 hits on /token equivalent.
	o.cfg.Endpoint.TokenURL = srv.URL

	blob := &stateBlob{
		State:        "S",
		Nonce:        "n",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateRR := httptest.NewRecorder()
	if err := o.setStateCookie(stateRR, blob); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=S&code=abc", nil)
	for _, c := range stateRR.Result().Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (token exchange failed)", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "token exchange") {
		t.Errorf("body = %q, want to mention token exchange", rr.Body.String())
	}
}

func TestCallbackHandler_NoIDToken(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "at",
			"token_type":   "Bearer",
			"expires_in":   60,
			// no id_token
		})
	}))
	t.Cleanup(srv.Close)

	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg(srv.URL)
	o.cfg.Endpoint.TokenURL = srv.URL

	blob := &stateBlob{
		State:        "S",
		Nonce:        "n",
		CodeVerifier: "v",
		ReturnTo:     "/",
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	stateRR := httptest.NewRecorder()
	if err := o.setStateCookie(stateRR, blob); err != nil {
		t.Fatalf("setStateCookie: %v", err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/auth/callback?state=S&code=abc", nil)
	for _, c := range stateRR.Result().Cookies() {
		req.AddCookie(c)
	}
	rr := httptest.NewRecorder()
	o.CallbackHandler(rr, req)

	if rr.Code != http.StatusBadGateway {
		t.Errorf("status = %d, want 502 (no id_token)", rr.Code)
	}
}

func TestLogoutHandler_GetRedirects(t *testing.T) {
	o := newBareOIDC(t)

	req := httptest.NewRequest(http.MethodGet, "/api/auth/logout", nil)
	rr := httptest.NewRecorder()
	o.LogoutHandler(rr, req)

	if rr.Code != http.StatusFound {
		t.Errorf("status = %d, want 302", rr.Code)
	}
	if loc := rr.Result().Header.Get("Location"); loc != "/" {
		t.Errorf("Location = %q, want /", loc)
	}
	// Session cookie was cleared.
	var cleared bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == o.session.Name && c.MaxAge == -1 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("LogoutHandler did not clear the session cookie")
	}
}

func TestLogoutHandler_PostNoContent(t *testing.T) {
	o := newBareOIDC(t)
	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	rr := httptest.NewRecorder()
	o.LogoutHandler(rr, req)
	if rr.Code != http.StatusNoContent {
		t.Errorf("status = %d, want 204", rr.Code)
	}
}

func TestNewOIDC_ValidationErrors(t *testing.T) {
	cases := []struct {
		name string
		cfg  OIDCConfig
	}{
		{"missing issuer", OIDCConfig{ClientID: "c", RedirectURL: "r"}},
		{"missing client id", OIDCConfig{Issuer: "i", RedirectURL: "r"}},
		{"missing redirect url", OIDCConfig{Issuer: "i", ClientID: "c"}},
	}
	store := NewSessionStore([]byte("k"), "n", "", false, 0)
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewOIDC(context.Background(), tc.cfg, store)
			if err == nil {
				t.Fatalf("NewOIDC(%+v) succeeded ; want validation error", tc.cfg)
			}
		})
	}
}

func TestNewOIDC_DiscoveryFailure(t *testing.T) {
	// Unreachable issuer URL → discovery error wrapped properly.
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "no discovery", http.StatusNotFound)
	}))
	t.Cleanup(srv.Close)

	store := NewSessionStore([]byte("k"), "n", "", false, 0)
	_, err := NewOIDC(context.Background(), OIDCConfig{
		Issuer:      srv.URL,
		ClientID:    "c",
		RedirectURL: "https://app.example.com/api/auth/callback",
	}, store)
	if err == nil {
		t.Fatal("NewOIDC succeeded against a 404 discovery endpoint ; want error")
	}
	if !strings.Contains(err.Error(), "discovery") {
		t.Errorf("err = %v, want it to mention discovery", err)
	}
}
