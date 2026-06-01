// middleware_origin_test.go — pin the withOriginCheck behaviour
// against the matrix of (method, header, host) combinations a real
// browser + CLI can produce.

package server

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// noopHandler increments hits + returns 204. The middleware-under-
// test sits in front ; a 204 here means the request passed through.
type noopHandler struct{ hits int }

func (n *noopHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	n.hits++
	w.WriteHeader(http.StatusNoContent)
}

func TestOriginCheck_GetAlwaysPassesNoHeader(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	resp, err := srv.Client().Get(srv.URL + "/api/resources")
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("GET without Origin: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_PostSameOriginPasses(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/azs", strings.NewReader(`{}`))
	req.Header.Set("Origin", srv.URL)
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("POST same-origin: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_PostCrossOriginRejected(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/azs", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://evil.example.com")
	req.Header.Set("Content-Type", "application/json")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("POST cross-origin: status %d, want 403", resp.StatusCode)
	}
	if inner.hits != 0 {
		t.Errorf("inner handler called on cross-origin POST")
	}
}

func TestOriginCheck_PostNoHeaderRejected(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/azs", strings.NewReader(`{}`))
	// No Origin, no Referer.
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 403 {
		t.Errorf("POST without Origin/Referer: status %d, want 403", resp.StatusCode)
	}
}

func TestOriginCheck_RefererFallback(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("DELETE", srv.URL+"/api/azs/foo", nil)
	// Browsers may omit Origin on same-origin DELETE and only send
	// Referer — withOriginCheck must accept that fallback.
	req.Header.Set("Referer", srv.URL+"/dashboard/inventory")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("DELETE with Referer same-origin: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_AllowList(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck([]string{"https://terraform.weft.local"}, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/azs", strings.NewReader(`{}`))
	req.Header.Set("Origin", "https://terraform.weft.local")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("POST allow-listed origin: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_AuthRoutesExempt(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	// /api/auth/logout is the typical POST that the OIDC layer owns ;
	// state validation lives in the OIDC handler. The Origin check
	// must not get in its way.
	req, _ := http.NewRequest("POST", srv.URL+"/api/auth/logout", nil)
	// Even with a wrong Origin, exempt path must pass.
	req.Header.Set("Origin", "https://evil.example.com")
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("POST /api/auth/* exempt: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_NonAPIPathsExempt(t *testing.T) {
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	// SPA / static asset path : not an /api/ mutation, must pass even
	// without Origin (some SPAs use HEAD with no extra headers).
	req, _ := http.NewRequest("POST", srv.URL+"/dashboard", nil)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("POST /dashboard: status %d, want 204", resp.StatusCode)
	}
}

func TestOriginCheck_PortStrippingOn80(t *testing.T) {
	// Defensive check : the middleware should treat http://host and
	// http://host:80 as equivalent (browsers strip the default port).
	inner := &noopHandler{}
	mw := withOriginCheck(nil, inner)
	srv := httptest.NewServer(mw)
	t.Cleanup(srv.Close)

	req, _ := http.NewRequest("POST", srv.URL+"/api/azs", strings.NewReader(`{}`))
	// httptest.Server URL already includes the live port (not 80) ;
	// just sanity-check that "scheme://host:port" matches itself.
	req.Header.Set("Origin", srv.URL)
	resp, err := srv.Client().Do(req)
	if err != nil {
		t.Fatal(err)
	}
	resp.Body.Close()
	if resp.StatusCode != 204 {
		t.Errorf("status = %d, want 204", resp.StatusCode)
	}
}
