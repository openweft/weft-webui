// oidc_audit_test.go — pins the OnAuthEvent hook : every failure
// branch in CallbackHandler must surface a distinct `reason` tag,
// and LoginHandler / LogoutHandler must emit their lifecycle events.

package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

type capturedAuthEvent struct {
	action, result, reason, remoteIP, subject string
}

// captureAuth returns a recorder slice + the hook that appends to it.
// Use *[]capturedAuthEvent so test assertions read off the same slice.
func captureAuth() (*[]capturedAuthEvent, func(action, result, reason, remoteIP, subject string)) {
	events := []capturedAuthEvent{}
	hook := func(action, result, reason, remoteIP, subject string) {
		events = append(events, capturedAuthEvent{action, result, reason, remoteIP, subject})
	}
	return &events, hook
}

func TestOnAuthEvent_LoginStartEmits(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")

	captured, hook := captureAuth()
	o.OnAuthEvent = hook

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	req.RemoteAddr = "1.2.3.4:5678"
	rr := httptest.NewRecorder()
	o.LoginHandler(rr, req)

	if len(*captured) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(*captured))
	}
	ev := (*captured)[0]
	if ev.action != "login.start" || ev.result != "ok" {
		t.Errorf("event = %+v, want login.start/ok", ev)
	}
	if ev.remoteIP != "1.2.3.4" {
		t.Errorf("remoteIP = %q, want 1.2.3.4", ev.remoteIP)
	}
}

func TestOnAuthEvent_CallbackFailedTagsReason(t *testing.T) {
	// The various failure branches each set a distinct `reason`. Walk
	// the easy-to-reach ones : missing state cookie, provider error,
	// state mismatch.
	cases := []struct {
		name       string
		setup      func(t *testing.T, o *OIDC, req *http.Request)
		wantReason string
		wantStatus int
	}{
		{
			name:       "missing state cookie",
			setup:      func(t *testing.T, o *OIDC, req *http.Request) {},
			wantReason: "state_cookie_invalid",
			wantStatus: http.StatusBadRequest,
		},
		{
			name: "idp_error returned",
			setup: func(t *testing.T, o *OIDC, req *http.Request) {
				blob := &stateBlob{State: "s1", Nonce: "n", CodeVerifier: "v",
					ReturnTo: "/", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
				stateRR := httptest.NewRecorder()
				if err := o.setStateCookie(stateRR, blob); err != nil {
					t.Fatal(err)
				}
				for _, c := range stateRR.Result().Cookies() {
					req.AddCookie(c)
				}
				q := req.URL.Query()
				q.Set("error", "access_denied")
				req.URL.RawQuery = q.Encode()
			},
			wantReason: "idp_error:access_denied",
			wantStatus: http.StatusUnauthorized,
		},
		{
			name: "state mismatch",
			setup: func(t *testing.T, o *OIDC, req *http.Request) {
				blob := &stateBlob{State: "s1", Nonce: "n", CodeVerifier: "v",
					ReturnTo: "/", ExpiresAt: time.Now().Add(5 * time.Minute).Unix()}
				stateRR := httptest.NewRecorder()
				if err := o.setStateCookie(stateRR, blob); err != nil {
					t.Fatal(err)
				}
				for _, c := range stateRR.Result().Cookies() {
					req.AddCookie(c)
				}
				q := req.URL.Query()
				q.Set("state", "different")
				q.Set("code", "x")
				req.URL.RawQuery = q.Encode()
			},
			wantReason: "state_mismatch",
			wantStatus: http.StatusBadRequest,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			o := newBareOIDC(t)
			o.cfg = minimalOauth2Cfg("https://idp.example.com")
			captured, hook := captureAuth()
			o.OnAuthEvent = hook

			req := httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
			req.RemoteAddr = "10.0.0.1:1234"
			tc.setup(t, o, req)

			rr := httptest.NewRecorder()
			o.CallbackHandler(rr, req)

			if rr.Code != tc.wantStatus {
				t.Errorf("status = %d, want %d", rr.Code, tc.wantStatus)
			}
			if len(*captured) != 1 {
				t.Fatalf("want 1 audit event, got %d", len(*captured))
			}
			ev := (*captured)[0]
			if ev.action != "callback.failed" || ev.result != "error" {
				t.Errorf("action/result = %s/%s, want callback.failed/error", ev.action, ev.result)
			}
			if !strings.HasPrefix(ev.reason, strings.SplitN(tc.wantReason, ":", 2)[0]) {
				t.Errorf("reason = %q, want prefix %q", ev.reason, tc.wantReason)
			}
			if ev.remoteIP != "10.0.0.1" {
				t.Errorf("remoteIP = %q, want 10.0.0.1", ev.remoteIP)
			}
		})
	}
}

func TestOnAuthEvent_LogoutEmits(t *testing.T) {
	o := newBareOIDC(t)
	captured, hook := captureAuth()
	o.OnAuthEvent = hook

	req := httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	req.RemoteAddr = "172.16.0.9:1024"
	rr := httptest.NewRecorder()
	o.LogoutHandler(rr, req)

	if len(*captured) != 1 {
		t.Fatalf("want 1 audit event, got %d", len(*captured))
	}
	ev := (*captured)[0]
	if ev.action != "logout" || ev.result != "ok" {
		t.Errorf("event = %+v, want logout/ok", ev)
	}
	if ev.remoteIP != "172.16.0.9" {
		t.Errorf("remoteIP = %q, want 172.16.0.9", ev.remoteIP)
	}
}

func TestOnAuthEvent_NilHookSafe(t *testing.T) {
	o := newBareOIDC(t)
	o.cfg = minimalOauth2Cfg("https://idp.example.com")
	// OnAuthEvent left nil — must not crash on any handler.

	req := httptest.NewRequest(http.MethodGet, "/api/auth/login", nil)
	o.LoginHandler(httptest.NewRecorder(), req)

	req = httptest.NewRequest(http.MethodGet, "/api/auth/callback", nil)
	o.CallbackHandler(httptest.NewRecorder(), req)

	req = httptest.NewRequest(http.MethodPost, "/api/auth/logout", nil)
	o.LogoutHandler(httptest.NewRecorder(), req)
	// success = no panic
}
