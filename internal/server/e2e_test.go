package server

import (
	"bytes"
	"encoding/json"
	"io"
	"io/fs"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"

	"github.com/openweft/weft-webui/internal/auth"
)

// staticFakeFS is a minimal embed.FS replacement so buildHandler's
// SPA mount doesn't blow up. We never serve anything from / in
// the tests, so an empty in-memory FS is fine.
type staticFakeFS struct{}

func (staticFakeFS) Open(name string) (fs.File, error) { return nil, os.ErrNotExist }

// newE2EHandler returns an http.Handler wired in dev-mode : no OIDC,
// a synthetic 'dev@weft.local' user with cluster_admin via groups,
// no live gRPC client (every endpoint falls through to the mock
// store), and a no-op static FS. Use httptest.NewServer over the
// returned handler.
func newE2EHandler(t *testing.T, scope Scope) http.Handler {
	t.Helper()
	d := Deps{
		Logger: slog.New(slog.NewTextHandler(io.Discard, nil)),
		Static: staticFakeFS{},
		Auth: &auth.Middleware{
			Mode: auth.ModeNone,
			MockUser: auth.User{
				Subject: "dev:alice", Email: "alice@weft.local",
				Name: "Alice Dev", Groups: []string{"admin"},
				DevMode: true,
			},
		},
		DevMode: true,
	}
	if scope == ScopeAdmin {
		return NewAdmin(d)
	}
	return New(d)
}

// hit is the smallest readable wrapper around httptest. Returns the
// status + decoded JSON body. Caller passes a pointer for typed
// decode ; nil body means "ignore the body".
func hit(t *testing.T, srv *httptest.Server, method, path string, body any, out any) int {
	t.Helper()
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			t.Fatalf("marshal: %v", err)
		}
		rdr = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, srv.URL+path, rdr)
	if err != nil {
		t.Fatalf("new request: %v", err)
	}
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	// withOriginCheck rejects mutating requests without an Origin or
	// Referer. httptest.Server.URL is the same-origin we want to
	// pass, so just forward it for any non-GET method.
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		req.Header.Set("Origin", srv.URL)
	}
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatalf("do: %v", err)
	}
	defer resp.Body.Close()
	if out != nil && resp.ContentLength != 0 {
		if err := json.NewDecoder(resp.Body).Decode(out); err != nil && err != io.EOF {
			t.Fatalf("decode %s %s: %v", method, path, err)
		}
	}
	return resp.StatusCode
}

// TestE2E_FullStack walks the server's full middleware chain on a
// representative sample of operations — public routes, authenticated
// reads, OIDC-redirect stubs, typed huma endpoints, mock-store
// writes. Smoke for "did anything regress end-to-end?" — finer
// behaviour stays on the per-package tests.
func TestE2E_FullStack(t *testing.T) {
	resetMockState()

	srv := httptest.NewServer(newE2EHandler(t, ScopeAdmin))
	t.Cleanup(srv.Close)

	t.Run("healthz returns 200 without auth", func(t *testing.T) {
		var body struct {
			OK bool `json:"ok"`
		}
		if code := hit(t, srv, "GET", "/api/healthz", nil, &body); code != 200 {
			t.Errorf("status = %d", code)
		}
		if !body.OK {
			t.Errorf("ok = %v", body.OK)
		}
	})

	t.Run("/api/openapi.json is served", func(t *testing.T) {
		var spec map[string]any
		if code := hit(t, srv, "GET", "/api/openapi.json", nil, &spec); code != 200 {
			t.Errorf("status = %d", code)
		}
		if _, ok := spec["paths"]; !ok {
			t.Errorf("spec has no paths key")
		}
	})

	t.Run("me returns the synthetic dev user", func(t *testing.T) {
		var me MeBody
		if code := hit(t, srv, "GET", "/api/me", nil, &me); code != 200 {
			t.Errorf("status = %d", code)
		}
		if me.Email != "alice@weft.local" {
			t.Errorf("email = %q", me.Email)
		}
		if !me.ClusterAdmin {
			t.Errorf("cluster_admin should be true (groups=admin)")
		}
	})

	t.Run("flavors catalogue list", func(t *testing.T) {
		var body struct {
			Flavors []APIFlavor `json:"flavors"`
		}
		if code := hit(t, srv, "GET", "/api/flavors", nil, &body); code != 200 {
			t.Errorf("status = %d", code)
		}
		if len(body.Flavors) == 0 {
			t.Errorf("seed should populate flavors")
		}
	})

	t.Run("scripts catalogue list", func(t *testing.T) {
		var body []APIScript
		if code := hit(t, srv, "GET", "/api/scripts", nil, &body); code != 200 {
			t.Errorf("status = %d", code)
		}
		if len(body) == 0 {
			t.Errorf("seed should populate scripts")
		}
	})

	t.Run("script set + delete round-trip", func(t *testing.T) {
		// Create.
		newS := APIScript{Name: "e2e-test", Description: "from e2e", Body: "echo hi"}
		var saved APIScript
		if code := hit(t, srv, "POST", "/api/scripts", newS, &saved); code != 200 {
			t.Errorf("set status = %d", code)
		}
		if saved.Name != "e2e-test" {
			t.Errorf("saved name = %q", saved.Name)
		}
		if saved.UpdatedAt == "" {
			t.Errorf("UpdatedAt should be server-stamped")
		}
		// Read.
		var got APIScript
		if code := hit(t, srv, "GET", "/api/scripts/e2e-test", nil, &got); code != 200 {
			t.Errorf("get status = %d", code)
		}
		if got.Body != "echo hi" {
			t.Errorf("body = %q", got.Body)
		}
		// Delete.
		var del struct {
			Deleted string `json:"deleted"`
		}
		if code := hit(t, srv, "DELETE", "/api/scripts/e2e-test", nil, &del); code != 200 {
			t.Errorf("delete status = %d", code)
		}
		if del.Deleted != "e2e-test" {
			t.Errorf("deleted = %q", del.Deleted)
		}
	})

	t.Run("vm property mem-store round-trip", func(t *testing.T) {
		// The seed has web-1/owner. Set a new key + read back.
		body := APIVMProperty{Key: "e2e-owner", Value: "team-test", GuestReadable: true}
		var saved APIVMProperty
		if code := hit(t, srv, "POST", "/api/microvms/web-1/properties", body, &saved); code != 200 {
			t.Errorf("set status = %d", code)
		}
		if saved.Key != "e2e-owner" || !saved.GuestReadable {
			t.Errorf("saved fields wrong : %+v", saved)
		}
		// List should include both seed entries + the new one.
		var props []APIVMProperty
		if code := hit(t, srv, "GET", "/api/microvms/web-1/properties", nil, &props); code != 200 {
			t.Errorf("list status = %d", code)
		}
		var found bool
		for _, p := range props {
			if p.Key == "e2e-owner" {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("e2e-owner not in listing : %+v", props)
		}
	})

	t.Run("404 errors are RFC 7807", func(t *testing.T) {
		var errBody map[string]any
		code := hit(t, srv, "GET", "/api/scripts/does-not-exist", nil, &errBody)
		if code != 404 {
			t.Errorf("status = %d, want 404", code)
		}
		// huma RFC 7807 carries detail/title/status fields.
		if errBody["detail"] == nil && errBody["title"] == nil {
			t.Errorf("error body has neither detail nor title : %+v", errBody)
		}
	})

	t.Run("422 from path-param validation", func(t *testing.T) {
		// scripts/{name} declares maxLength=128 ; 200 'a's should 422.
		long := bytes.Repeat([]byte("a"), 200)
		code := hit(t, srv, "GET", "/api/scripts/"+string(long), nil, nil)
		if code != 422 {
			t.Errorf("overlong name should 422, got %d", code)
		}
	})

	t.Run("session/scope POST round-trips through middleware", func(t *testing.T) {
		// dev-mode session scope, no signed cookie.
		body := map[string]string{"tenant": "acme", "project": "web"}
		code := hit(t, srv, "POST", "/api/session/scope", body, nil)
		if code != 204 && code != 200 {
			t.Errorf("status = %d", code)
		}
	})
}

// TestE2E_UserListenerHidesAdminOps confirms the scope filter : the
// user listener returns 404 on admin-only routes (don't-acknowledge
// pattern). A stale SPA build can't accidentally probe the admin
// surface from a regular user's session.
func TestE2E_UserListenerHidesAdminOps(t *testing.T) {
	resetMockState()

	srv := httptest.NewServer(newE2EHandler(t, ScopeUser))
	t.Cleanup(srv.Close)

	// admin-only : /api/tenants POST + /api/tenants/{name}/admins POST
	// + /api/network-topology — should all 404 on the user listener.
	for _, path := range []string{
		"/api/network-topology",
	} {
		var body map[string]any
		code := hit(t, srv, "GET", path, nil, &body)
		if code != 404 {
			t.Errorf("%s should 404 on user listener, got %d body=%+v", path, code, body)
		}
	}

	// POST /api/scripts is admin-only — should 404 (the user listener
	// just doesn't register the route).
	body := APIScript{Name: "x", Description: "y", Body: "z"}
	code := hit(t, srv, "POST", "/api/scripts", body, nil)
	if code != 404 && code != 405 {
		t.Errorf("admin-only POST /api/scripts should 404/405 on user listener, got %d", code)
	}
}

// resetMockState rewinds the package-global mem stores so tests
// running in sequence don't see each other's mutations.
func resetMockState() {
	flavorsCatalogue = newMemFlavorCatalogue()
	scriptsCatalogue = newMemScriptCatalogue()
	sshKeysCatalogue = newMemSSHKeyCatalogue()
	vmProps = seedVMProperties()
	uefiVars = seedUEFIVars()
	vmKeyAssignments = seedVMKeyAssignments()
	vmKeyAddedAt = map[string]map[string]string{}
	buckets = seedBuckets()
	policies = seedPolicies()
	shareFiles = map[string][]s3object{}
	// re-seed shareFiles with the same data
	for k, v := range seedShareFiles() {
		shareFiles[k] = v
	}
}

// seedShareFiles is exposed only for the e2e reset path. The real
// store seeds inline at package init ; if we ever drop that
// pattern, this helper becomes the single source of truth.
func seedShareFiles() map[string][]s3object {
	return map[string][]s3object{
		"team-data": {
			obj("README.md", "# team-data (share)\n"),
		},
	}
}
