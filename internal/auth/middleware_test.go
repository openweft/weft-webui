// middleware_test.go — covers the refresh-on-expiry wiring in
// Middleware.Wrap. The Refresher is stubbed so we don't need a real IdP.
package auth

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// fakeRefresher is a programmable stub for Refresher. Counts calls so
// tests can assert it was (or wasn't) invoked.
type fakeRefresher struct {
	calls int
	out   *SessionPayload
	err   error
	saw   *SessionPayload
}

func (f *fakeRefresher) RefreshSession(_ context.Context, p *SessionPayload) (*SessionPayload, error) {
	f.calls++
	f.saw = p
	if f.err != nil {
		return nil, f.err
	}
	return f.out, nil
}

// newTestMW builds a Middleware + a SessionStore + a recording handler.
// `seed` is what gets baked into the session cookie before the request.
func newTestMW(t *testing.T, seed *SessionPayload, refresher Refresher) (*Middleware, *SessionStore, http.Handler) {
	t.Helper()
	store := NewSessionStore([]byte("test-key-0123456789abcdef"), "test_sess", "", false, 3600)
	mw := &Middleware{
		Mode:      ModeOIDC,
		Sessions:  store,
		Refresher: refresher,
		Logger:    slog.New(slog.NewTextHandler(io.Discard, nil)),
	}
	handler := mw.Wrap(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		u := UserFromContext(r.Context())
		if u == nil {
			http.Error(w, "no user", http.StatusInternalServerError)
			return
		}
		_, _ = w.Write([]byte(u.AccessToken))
	}))
	return mw, store, handler
}

// signedCookie returns the Set-Cookie value for the given payload.
func signedCookie(t *testing.T, s *SessionStore, p *SessionPayload) string {
	t.Helper()
	v, err := s.Encode(p)
	if err != nil {
		t.Fatalf("encode: %v", err)
	}
	return v
}

func TestMiddleware_RefreshSuccess(t *testing.T) {
	seed := &SessionPayload{
		Subject:      "alice",
		AccessToken:  "old-access",
		RefreshToken: "good-refresh",
		ExpiresAt:    time.Now().Add(10 * time.Second).Unix(), // within leeway
	}
	refreshed := &SessionPayload{
		Subject:      "alice",
		AccessToken:  "new-access",
		RefreshToken: "rotated-refresh",
		ExpiresAt:    time.Now().Add(3600 * time.Second).Unix(),
	}
	fr := &fakeRefresher{out: refreshed}
	_, store, h := newTestMW(t, seed, fr)

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: signedCookie(t, store, seed)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %q", rr.Code, rr.Body.String())
	}
	if got := rr.Body.String(); got != "new-access" {
		t.Errorf("downstream saw AccessToken = %q, want new-access", got)
	}
	if fr.calls != 1 {
		t.Errorf("Refresher called %d times, want 1", fr.calls)
	}
	// Cookie was re-issued with the new payload.
	var setCookie string
	for _, c := range rr.Result().Cookies() {
		if c.Name == store.Name {
			setCookie = c.Value
			break
		}
	}
	if setCookie == "" {
		t.Fatal("no Set-Cookie for the session ; refresh path forgot to persist")
	}
	got, err := store.Decode(setCookie)
	if err != nil {
		t.Fatalf("decode re-issued cookie: %v", err)
	}
	if got.AccessToken != "new-access" || got.RefreshToken != "rotated-refresh" {
		t.Errorf("re-issued cookie has wrong payload : %+v", got)
	}
}

func TestMiddleware_RefreshSkippedWhenNotExpiring(t *testing.T) {
	seed := &SessionPayload{
		Subject:      "alice",
		AccessToken:  "still-valid",
		RefreshToken: "ignored",
		ExpiresAt:    time.Now().Add(10 * time.Minute).Unix(),
	}
	fr := &fakeRefresher{}
	_, store, h := newTestMW(t, seed, fr)

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: signedCookie(t, store, seed)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if fr.calls != 0 {
		t.Errorf("Refresher called %d times for a fresh session, want 0", fr.calls)
	}
	if got := rr.Body.String(); got != "still-valid" {
		t.Errorf("AccessToken = %q, want still-valid", got)
	}
}

func TestMiddleware_RefreshFailureFallsThrough(t *testing.T) {
	seed := &SessionPayload{
		Subject:      "alice",
		AccessToken:  "old-access",
		RefreshToken: "revoked",
		ExpiresAt:    time.Now().Add(5 * time.Second).Unix(), // within leeway
	}
	fr := &fakeRefresher{err: errors.New("invalid_grant")}
	_, store, h := newTestMW(t, seed, fr)

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: signedCookie(t, store, seed)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Per the spec : refresh failure logs + falls through. The session
	// is still valid (not yet expired) so the request succeeds with the
	// existing access token. A 401 will hit on the NEXT request once
	// the cookie's ExpiresAt has actually passed.
	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (fall-through)", rr.Code)
	}
	if got := rr.Body.String(); got != "old-access" {
		t.Errorf("AccessToken = %q, want old-access (refresh failed, kept original)", got)
	}
	if fr.calls != 1 {
		t.Errorf("Refresher called %d times, want 1", fr.calls)
	}
	// No Set-Cookie : we didn't persist anything new.
	for _, c := range rr.Result().Cookies() {
		if c.Name == store.Name {
			t.Errorf("session cookie re-issued after failed refresh ; should have been left alone")
		}
	}
}

func TestMiddleware_RefreshSkippedWhenNoRefreshToken(t *testing.T) {
	seed := &SessionPayload{
		Subject:     "alice",
		AccessToken: "old-access",
		// RefreshToken intentionally empty.
		ExpiresAt: time.Now().Add(5 * time.Second).Unix(),
	}
	fr := &fakeRefresher{}
	_, store, h := newTestMW(t, seed, fr)

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: signedCookie(t, store, seed)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if fr.calls != 0 {
		t.Errorf("Refresher called %d times despite empty RefreshToken, want 0", fr.calls)
	}
}

func TestMiddleware_NoRefresherIsNoOp(t *testing.T) {
	seed := &SessionPayload{
		Subject:      "alice",
		AccessToken:  "old-access",
		RefreshToken: "would-refresh-if-wired",
		ExpiresAt:    time.Now().Add(5 * time.Second).Unix(),
	}
	_, store, h := newTestMW(t, seed, nil) // no Refresher

	req := httptest.NewRequest(http.MethodGet, "/api/anything", nil)
	req.AddCookie(&http.Cookie{Name: store.Name, Value: signedCookie(t, store, seed)})
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d", rr.Code)
	}
	if got := rr.Body.String(); got != "old-access" {
		t.Errorf("AccessToken = %q, want old-access", got)
	}
}

func TestMiddleware_PublicPathBypassesRefresh(t *testing.T) {
	fr := &fakeRefresher{out: &SessionPayload{AccessToken: "x"}}
	_, _, h := newTestMW(t, nil, fr)

	req := httptest.NewRequest(http.MethodGet, "/api/healthz", nil)
	// No cookie at all — public path mustn't try to refresh.
	rr := httptest.NewRecorder()
	h.ServeHTTP(rr, req)

	// Handler writes the access token, but there is no user in context
	// on a public path → falls through to the "no user" branch with 500.
	// We only care that Refresher was NOT invoked.
	if fr.calls != 0 {
		t.Errorf("Refresher called on public path, want 0 (got %d)", fr.calls)
	}
	if strings.Contains(rr.Body.String(), "panic") {
		t.Errorf("unexpected panic: %s", rr.Body.String())
	}
}
