// oidc_test.go — unit tests for RefreshSession + NeedsRefresh.
//
// We can't easily spin up a real OIDC provider in a unit test, so we
// stub the token endpoint with httptest.Server and feed its URL into
// oauth2.Config.Endpoint. That covers RefreshSession end-to-end (the
// only IdP interaction is the token exchange — we deliberately skip the
// id_token re-verification branch by having the stub return no
// id_token, then unit-test the verification path conceptually via the
// NoRefreshToken / RevokedToken cases).
package auth

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

// newTestOIDC returns an *OIDC wired against a stub token endpoint. The
// handler argument decides what the endpoint replies with — pass nil
// for the happy-path default.
func newTestOIDC(t *testing.T, handler http.HandlerFunc) (*OIDC, *httptest.Server) {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	o := &OIDC{
		cfg: &oauth2.Config{
			ClientID:     "test-client",
			ClientSecret: "test-secret",
			Endpoint: oauth2.Endpoint{
				AuthURL:  srv.URL + "/authorize",
				TokenURL: srv.URL,
			},
			Scopes: []string{"openid"},
		},
	}
	return o, srv
}

func TestRefreshSession_HappyPath(t *testing.T) {
	const newAccess = "fresh-access-token"
	const newRefresh = "rotated-refresh-token"

	handler := func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Fatalf("parse form: %v", err)
		}
		if got := r.PostForm.Get("grant_type"); got != "refresh_token" {
			t.Errorf("grant_type = %q, want refresh_token", got)
		}
		if got := r.PostForm.Get("refresh_token"); got != "old-refresh" {
			t.Errorf("refresh_token sent = %q, want old-refresh", got)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token":  newAccess,
			"refresh_token": newRefresh,
			"token_type":    "Bearer",
			"expires_in":    3600,
		})
	}
	o, _ := newTestOIDC(t, handler)

	in := &SessionPayload{
		Subject:      "alice",
		Groups:       []string{"admin"},
		AccessToken:  "stale-access",
		RefreshToken: "old-refresh",
		ExpiresAt:    time.Now().Add(-5 * time.Second).Unix(), // expired
	}
	out, err := o.RefreshSession(context.Background(), in)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if out == in {
		t.Errorf("RefreshSession returned the same pointer ; want a fresh payload")
	}
	if in.AccessToken != "stale-access" {
		t.Errorf("input mutated : AccessToken = %q", in.AccessToken)
	}
	if out.AccessToken != newAccess {
		t.Errorf("AccessToken = %q, want %q", out.AccessToken, newAccess)
	}
	if out.RefreshToken != newRefresh {
		t.Errorf("RefreshToken = %q, want %q", out.RefreshToken, newRefresh)
	}
	want := time.Now().Add(3600 * time.Second).Unix()
	if delta := out.ExpiresAt - want; delta < -5 || delta > 5 {
		t.Errorf("ExpiresAt = %d, want ~%d (delta=%d)", out.ExpiresAt, want, delta)
	}
	// Claims unchanged — no id_token in the stub response.
	if out.Subject != "alice" || len(out.Groups) != 1 || out.Groups[0] != "admin" {
		t.Errorf("claims clobbered : sub=%q groups=%v", out.Subject, out.Groups)
	}
}

func TestRefreshSession_HappyPath_KeepsOldRefreshWhenIdPDoesNotRotate(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		// No refresh_token in the response — common with Auth0 default config.
		_ = json.NewEncoder(w).Encode(map[string]any{
			"access_token": "fresh",
			"token_type":   "Bearer",
			"expires_in":   60,
		})
	}
	o, _ := newTestOIDC(t, handler)

	in := &SessionPayload{RefreshToken: "still-good", ExpiresAt: 1}
	out, err := o.RefreshSession(context.Background(), in)
	if err != nil {
		t.Fatalf("RefreshSession: %v", err)
	}
	if out.RefreshToken != "still-good" {
		t.Errorf("RefreshToken = %q, want still-good (no rotation case)", out.RefreshToken)
	}
}

func TestRefreshSession_RevokedToken(t *testing.T) {
	handler := func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"refresh token revoked"}`))
	}
	o, _ := newTestOIDC(t, handler)

	in := &SessionPayload{RefreshToken: "revoked", ExpiresAt: 1}
	out, err := o.RefreshSession(context.Background(), in)
	if err == nil {
		t.Fatalf("RefreshSession succeeded ; want error")
	}
	if out != nil {
		t.Errorf("expected nil payload on error, got %+v", out)
	}
	if !strings.Contains(err.Error(), "refresh token exchange") {
		t.Errorf("error %q missing wrapping prefix", err.Error())
	}
}

func TestRefreshSession_NoRefreshToken(t *testing.T) {
	o, _ := newTestOIDC(t, func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("token endpoint hit ; want short-circuit on empty refresh token")
	})
	in := &SessionPayload{Subject: "bob", RefreshToken: ""}
	out, err := o.RefreshSession(context.Background(), in)
	if !errors.Is(err, ErrNoRefreshToken) {
		t.Fatalf("err = %v, want ErrNoRefreshToken", err)
	}
	if out != nil {
		t.Errorf("expected nil payload, got %+v", out)
	}
}

func TestRefreshSession_NilSession(t *testing.T) {
	o, _ := newTestOIDC(t, nil)
	out, err := o.RefreshSession(context.Background(), nil)
	if err == nil {
		t.Fatalf("RefreshSession(nil) succeeded ; want error")
	}
	if out != nil {
		t.Errorf("expected nil payload, got %+v", out)
	}
}

func TestSession_NeedsRefresh(t *testing.T) {
	now := time.Date(2026, 5, 31, 12, 0, 0, 0, time.UTC)

	cases := []struct {
		name      string
		expiresAt int64
		want      bool
	}{
		{"expired now", now.Unix(), true},
		{"within leeway (59s)", now.Add(59 * time.Second).Unix(), true},
		{"just past leeway (61s)", now.Add(61 * time.Second).Unix(), false},
		{"already past expiry", now.Add(-30 * time.Second).Unix(), true},
		{"missing expiry (legacy session)", 0, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p := &SessionPayload{ExpiresAt: tc.expiresAt}
			if got := p.NeedsRefresh(now); got != tc.want {
				t.Errorf("NeedsRefresh = %v, want %v", got, tc.want)
			}
		})
	}
}

func TestSession_NeedsRefresh_NilReceiver(t *testing.T) {
	var p *SessionPayload
	if p.NeedsRefresh(time.Now()) {
		t.Errorf("nil session shouldn't claim it needs refresh")
	}
}
