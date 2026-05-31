// middleware_extra_test.go — covers MeHandler, SetScopeHandler,
// writeAuthErr, the dev-mode short-circuit (incl. ?as_user / ?as_groups
// impersonation), and the signHex / verifyHex helpers.
package auth

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// --- MeHandler ---------------------------------------------------------

func TestMeHandler_NoSession(t *testing.T) {
	mw := &Middleware{Mode: ModeOIDC, Sessions: NewSessionStore([]byte("k"), "n", "", false, 0)}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	rr := httptest.NewRecorder()
	mw.MeHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no session") {
		t.Errorf("body = %q, want 'no session'", rr.Body.String())
	}
}

func TestMeHandler_WithUser(t *testing.T) {
	mw := &Middleware{Mode: ModeOIDC, Sessions: NewSessionStore([]byte("k"), "n", "", false, 0)}
	u := &User{Subject: "alice@example.com", Email: "alice@example.com", Name: "Alice Smith", Groups: []string{"admin"}, Project: "proj-1"}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req = req.WithContext(WithUser(req.Context(), u))
	rr := httptest.NewRecorder()
	mw.MeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200", rr.Code)
	}
	var body map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &body); err != nil {
		t.Fatalf("body unmarshal: %v ; raw = %q", err, rr.Body.String())
	}
	if body["sub"] != "alice@example.com" {
		t.Errorf("sub = %v, want alice@example.com", body["sub"])
	}
	if body["initials"] != "AS" {
		t.Errorf("initials = %v, want AS", body["initials"])
	}
	if body["dev"] != false {
		t.Errorf("dev = %v, want false", body["dev"])
	}
	groups, ok := body["groups"].([]any)
	if !ok || len(groups) != 1 || groups[0] != "admin" {
		t.Errorf("groups = %v", body["groups"])
	}
}

// --- SetScopeHandler ---------------------------------------------------

func TestSetScopeHandler_BadJSON(t *testing.T) {
	mw := &Middleware{Mode: ModeOIDC, Sessions: NewSessionStore([]byte("k"), "n", "", false, 0)}
	req := httptest.NewRequest(http.MethodPost, "/api/session/scope", strings.NewReader("not-json"))
	rr := httptest.NewRecorder()
	mw.SetScopeHandler(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Errorf("status = %d, want 400 (bad json)", rr.Code)
	}
}

func TestSetScopeHandler_NoSession(t *testing.T) {
	mw := &Middleware{Mode: ModeOIDC, Sessions: NewSessionStore([]byte("k"), "n", "", false, 0)}
	body := bytes.NewBufferString(`{"tenant":"acme","project":"team-alpha"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/scope", body)
	rr := httptest.NewRecorder()
	mw.SetScopeHandler(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
}

func TestSetScopeHandler_OIDC_ReissuesCookie(t *testing.T) {
	store := NewSessionStore([]byte("k"), "test_sess", "", false, 3600)
	mw := &Middleware{Mode: ModeOIDC, Sessions: store}

	u := &User{Subject: "alice", Email: "alice@x"}
	body := bytes.NewBufferString(`{"tenant":"  acme  ","project":"team-alpha"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/scope", body)
	req = req.WithContext(WithUser(req.Context(), u))
	rr := httptest.NewRecorder()
	mw.SetScopeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	// The handler trims whitespace from the input tenant.
	var out map[string]string
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}
	if out["tenant"] != "acme" {
		t.Errorf("tenant = %q, want acme (trimmed)", out["tenant"])
	}
	if out["project"] != "team-alpha" {
		t.Errorf("project = %q", out["project"])
	}

	// Cookie was re-issued.
	var found bool
	for _, c := range rr.Result().Cookies() {
		if c.Name == store.Name {
			found = true
			p, err := store.Decode(c.Value)
			if err != nil {
				t.Fatalf("decode reissued cookie: %v", err)
			}
			if p.Tenant != "acme" || p.Project != "team-alpha" {
				t.Errorf("cookie payload = %+v", p)
			}
		}
	}
	if !found {
		t.Error("no session cookie was re-issued")
	}
}

func TestSetScopeHandler_OIDC_SetError(t *testing.T) {
	// Empty key triggers Encode error inside Sessions.Set.
	store := &SessionStore{Key: nil, Name: "n"}
	mw := &Middleware{Mode: ModeOIDC, Sessions: store}
	u := &User{Subject: "alice"}

	body := bytes.NewBufferString(`{"tenant":"acme","project":"team-alpha"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/scope", body)
	req = req.WithContext(WithUser(req.Context(), u))
	rr := httptest.NewRecorder()
	mw.SetScopeHandler(rr, req)

	if rr.Code != http.StatusInternalServerError {
		t.Errorf("status = %d, want 500 (Set error)", rr.Code)
	}
}

func TestSetScopeHandler_DevMode(t *testing.T) {
	store := NewSessionStore([]byte("k"), "test_sess", "", false, 3600)
	mw := &Middleware{Mode: ModeNone, Sessions: store, MockUser: User{Subject: "dev"}}
	u := &User{Subject: "dev"}

	body := bytes.NewBufferString(`{"tenant":"t1","project":"p1"}`)
	req := httptest.NewRequest(http.MethodPost, "/api/session/scope", body)
	req = req.WithContext(WithUser(req.Context(), u))
	rr := httptest.NewRecorder()
	mw.SetScopeHandler(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	// In dev mode we stash on the middleware, NOT in a cookie.
	for _, c := range rr.Result().Cookies() {
		if c.Name == store.Name {
			t.Errorf("dev mode should not issue a session cookie")
		}
	}
	if mw.devTenant != "t1" || mw.devProject != "p1" {
		t.Errorf("dev scope not stashed : tenant=%q project=%q", mw.devTenant, mw.devProject)
	}
}

// --- writeAuthErr ------------------------------------------------------

func TestWriteAuthErr_Variants(t *testing.T) {
	cases := []struct {
		name    string
		err     error
		wantMsg string
		wantSt  int
	}{
		{"no session", ErrNoSession, "no session", http.StatusUnauthorized},
		{"expired", ErrExpired, "session expired", http.StatusUnauthorized},
		{"bad sig", ErrBadSignature, "bad session", http.StatusUnauthorized},
		{"generic", io.EOF, "auth required", http.StatusUnauthorized},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			rr := httptest.NewRecorder()
			writeAuthErr(rr, tc.err)
			if rr.Code != tc.wantSt {
				t.Errorf("status = %d, want %d", rr.Code, tc.wantSt)
			}
			if !strings.Contains(rr.Body.String(), tc.wantMsg) {
				t.Errorf("body = %q, want to contain %q", rr.Body.String(), tc.wantMsg)
			}
			if rr.Result().Header.Get("WWW-Authenticate") == "" {
				t.Error("WWW-Authenticate header must be set on auth errors")
			}
		})
	}
}

// --- Wrap: missing / bad session paths ---------------------------------

func TestWrap_NoCookie(t *testing.T) {
	_, _, h := newTestMW(t, nil, nil)
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "no session") {
		t.Errorf("body = %q, want 'no session'", rr.Body.String())
	}
}

func TestWrap_ExpiredCookie(t *testing.T) {
	store := NewSessionStore([]byte("k"), "test_sess", "", false, 3600)
	mw := &Middleware{
		Mode:     ModeOIDC,
		Sessions: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	expired := &SessionPayload{Subject: "x", ExpiresAt: time.Now().Add(-1 * time.Hour).Unix()}
	v, err := store.Encode(expired)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: v})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "expired") {
		t.Errorf("body = %q, want 'expired'", rr.Body.String())
	}
}

func TestWrap_BadSignature(t *testing.T) {
	store := NewSessionStore([]byte("k"), "test_sess", "", false, 3600)
	mw := &Middleware{
		Mode:     ModeOIDC,
		Sessions: store,
		Logger:   slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: "garbage.deadbeef"})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusUnauthorized {
		t.Errorf("status = %d, want 401", rr.Code)
	}
	if !strings.Contains(rr.Body.String(), "bad session") {
		t.Errorf("body = %q, want 'bad session'", rr.Body.String())
	}
}

// --- Wrap: dev mode short-circuit --------------------------------------

func TestWrap_DevMode_MockUser(t *testing.T) {
	mw := &Middleware{
		Mode:     ModeNone,
		Sessions: NewSessionStore([]byte("k"), "test_sess", "", false, 0),
		MockUser: User{Subject: "dev-user", Name: "Dev"},
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "no user", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(u.Subject + "|" + u.Name + "|" + boolStr(u.DevMode)))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if rr.Body.String() != "dev-user|Dev|true" {
		t.Errorf("body = %q, want dev-user|Dev|true", rr.Body.String())
	}
}

func TestWrap_DevMode_ImpersonationAsUser(t *testing.T) {
	mw := &Middleware{
		Mode:     ModeNone,
		Sessions: NewSessionStore([]byte("k"), "test_sess", "", false, 0),
		MockUser: User{Subject: "admin", Groups: []string{"admin"}},
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		body, _ := json.Marshal(map[string]any{
			"sub":    u.Subject,
			"email":  u.Email,
			"groups": u.Groups,
		})
		_, _ = w.Write(body)
	}))

	// as_user without as_groups → impersonated identity with NO groups (non-admin path).
	req := httptest.NewRequest(http.MethodGet, "/api/anything?as_user=carol%40example.com", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got map[string]any
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v ; raw = %q", err, rr.Body.String())
	}
	if got["sub"] != "carol@example.com" {
		t.Errorf("sub = %v, want carol@example.com", got["sub"])
	}
	if got["groups"] != nil {
		t.Errorf("groups = %v, want nil (default impersonation drops groups)", got["groups"])
	}
}

func TestWrap_DevMode_ImpersonationAsGroups(t *testing.T) {
	mw := &Middleware{
		Mode:     ModeNone,
		Sessions: NewSessionStore([]byte("k"), "test_sess", "", false, 0),
		MockUser: User{Subject: "admin"},
	}
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		body, _ := json.Marshal(u.Groups)
		_, _ = w.Write(body)
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/anything?as_user=bob&as_groups=ops,viewers", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	var got []string
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
		t.Fatalf("unmarshal: %v ; raw = %q", err, rr.Body.String())
	}
	if len(got) != 2 || got[0] != "ops" || got[1] != "viewers" {
		t.Errorf("groups = %v, want [ops viewers]", got)
	}
}

func TestWrap_DevMode_ScopeStashed(t *testing.T) {
	mw := &Middleware{
		Mode:     ModeNone,
		Sessions: NewSessionStore([]byte("k"), "test_sess", "", false, 0),
		MockUser: User{Subject: "dev"},
	}
	mw.devTenant = "stashed-tenant"
	mw.devProject = "stashed-project"
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		_, _ = w.Write([]byte(u.Tenant + "|" + u.Project))
	}))

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if rr.Body.String() != "stashed-tenant|stashed-project" {
		t.Errorf("body = %q", rr.Body.String())
	}
}

// --- Wrap: non-API paths bypass auth -----------------------------------

func TestWrap_NonAPIPassthrough(t *testing.T) {
	store := NewSessionStore([]byte("k"), "test_sess", "", false, 0)
	mw := &Middleware{Mode: ModeOIDC, Sessions: store}
	downstream := false
	h := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		downstream = true
		w.WriteHeader(http.StatusOK)
	}))
	req := httptest.NewRequest(http.MethodGet, "/index.html", nil)
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)
	if !downstream {
		t.Error("non-API path didn't reach downstream handler")
	}
	if rr.Code != http.StatusOK {
		t.Errorf("status = %d", rr.Code)
	}
}

func TestPublicPath(t *testing.T) {
	cases := map[string]bool{
		"/api/healthz":         true,
		"/api/readyz":          true,
		"/api/auth/login":      true,
		"/api/auth/callback":   true,
		"/api/auth/logout":     true,
		"/api/me":              false,
		"/api/vms":             false,
		"/api/session/scope":   false,
	}
	for p, want := range cases {
		if got := publicPath(p); got != want {
			t.Errorf("publicPath(%q) = %v, want %v", p, got, want)
		}
	}
}

// --- log() helper ------------------------------------------------------

func TestMiddleware_LogDefault(t *testing.T) {
	mw := &Middleware{}
	if mw.log() == nil {
		t.Errorf("log() returned nil with no explicit logger ; want slog.Default")
	}
}

// --- signHex / verifyHex ----------------------------------------------

func TestSignVerifyHex_RoundTrip(t *testing.T) {
	key := []byte("test-key")
	raw := []byte("the brown fox")
	sig, err := signHex(key, raw)
	if err != nil {
		t.Fatalf("signHex: %v", err)
	}
	if _, err := hex.DecodeString(sig); err != nil {
		t.Errorf("sig is not hex : %v", err)
	}
	if !verifyHex(key, raw, sig) {
		t.Error("verifyHex rejected a freshly-signed payload")
	}
}

func TestVerifyHex_Negatives(t *testing.T) {
	key := []byte("k")
	raw := []byte("data")
	sig, _ := signHex(key, raw)

	if verifyHex([]byte("different-key"), raw, sig) {
		t.Error("verifyHex accepted signature under a different key")
	}
	if verifyHex(key, []byte("tampered"), sig) {
		t.Error("verifyHex accepted signature over tampered payload")
	}
	if verifyHex(key, raw, "not-hex") {
		t.Error("verifyHex accepted non-hex signature")
	}
}

func boolStr(b bool) string {
	if b {
		return "true"
	}
	return "false"
}
