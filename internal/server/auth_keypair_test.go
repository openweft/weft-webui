package server

import (
	"bytes"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
)

// makeKP returns a fresh keypair + base64-std pubkey for the handler
// tests. Mirrors the helper in internal/auth's keypair_test.go but
// kept local to avoid an unnecessary cross-package coupling.
func makeKP(t *testing.T) (ed25519.PrivateKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, base64.StdEncoding.EncodeToString(pub)
}

func signTestAssertionForHandler(t *testing.T, priv ed25519.PrivateKey, pubB64, aud string, iat, exp int64) string {
	t.Helper()
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	body, _ := json.Marshal(map[string]any{
		"iss":   "weft-app-osx",
		"sub":   pubB64,
		"aud":   aud,
		"iat":   iat,
		"exp":   exp,
		"nonce": "n",
	})
	signingInput := base64.RawURLEncoding.EncodeToString(hdr) + "." +
		base64.RawURLEncoding.EncodeToString(body)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

func writeAllowlistTestFile(t *testing.T, entries []auth.KeypairAllowlistEntry) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "allowlist.json")
	b, _ := json.Marshal(auth.KeypairAllowlistFile{Entries: entries})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func newKeypairTestDeps(t *testing.T, entries []auth.KeypairAllowlistEntry, aud string) keypairHandlerDeps {
	t.Helper()
	path := writeAllowlistTestFile(t, entries)
	list, err := auth.LoadKeypairAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	key := make([]byte, 32)
	_, _ = rand.Read(key)
	return keypairHandlerDeps{
		Allowlist:     list,
		Sessions:      auth.NewSessionStore(key, "weft_test_session", "", false, 3600),
		Audience:      aud,
		SessionMaxAge: time.Hour,
	}
}

func TestKeypairHandlerHappyPath(t *testing.T) {
	priv, pkB64 := makeKP(t)
	const aud = "https://w.test/api/auth/keypair"
	deps := newKeypairTestDeps(t, []auth.KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin", Label: "alice"},
	}, aud)
	jws := signTestAssertionForHandler(t, priv, pkB64, aud, time.Now().Unix(), time.Now().Add(time.Minute).Unix())

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/keypair", bytes.NewReader([]byte(jws)))
	req.Header.Set("Content-Type", "application/jose")
	keypairAuthHandler(deps).ServeHTTP(rr, req)

	if rr.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 ; body=%s", rr.Code, rr.Body.String())
	}
	var resp keypairResponse
	if err := json.Unmarshal(rr.Body.Bytes(), &resp); err != nil {
		t.Fatal(err)
	}
	if resp.IDToken == "" {
		t.Fatal("id_token must be populated")
	}
	if resp.Kind != "keypair" {
		t.Fatalf("kind = %q, want keypair", resp.Kind)
	}
	if resp.ExpiresAtUnix == 0 {
		t.Fatal("expires_at_unix must be set")
	}
	// Set-Cookie must be emitted so a browser-driven debug exchange
	// gets a usable session for follow-up /api/* requests.
	if got := rr.Header().Get("Set-Cookie"); !strings.Contains(got, "weft_test_session=") {
		t.Fatalf("expected Set-Cookie ; got %q", got)
	}
}

func TestKeypairHandlerRejectsBadSignature(t *testing.T) {
	priv, pkB64 := makeKP(t)
	const aud = "https://w.test/api/auth/keypair"
	deps := newKeypairTestDeps(t, []auth.KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}, aud)
	jws := signTestAssertionForHandler(t, priv, pkB64, aud, time.Now().Unix(), time.Now().Add(time.Minute).Unix())
	parts := strings.Split(jws, ".")
	parts[2] = parts[2][:len(parts[2])-2] + "AA"
	bad := strings.Join(parts, ".")

	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/keypair", bytes.NewReader([]byte(bad)))
	keypairAuthHandler(deps).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestKeypairHandlerRejectsMalformed(t *testing.T) {
	_, pkB64 := makeKP(t)
	deps := newKeypairTestDeps(t, []auth.KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}, "https://w.test/api/auth/keypair")
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/keypair", bytes.NewReader([]byte("garbage")))
	keypairAuthHandler(deps).ServeHTTP(rr, req)
	if rr.Code != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", rr.Code)
	}
}

func TestKeypairHandlerKeyNotAllowed(t *testing.T) {
	// Allowlist has A ; assertion is signed by B.
	_, pkA := makeKP(t)
	privB, pkB := makeKP(t)
	const aud = "https://w.test/api/auth/keypair"
	deps := newKeypairTestDeps(t, []auth.KeypairAllowlistEntry{
		{Pubkey: pkA, Role: "superadmin"},
	}, aud)
	jws := signTestAssertionForHandler(t, privB, pkB, aud, time.Now().Unix(), time.Now().Add(time.Minute).Unix())
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/keypair", bytes.NewReader([]byte(jws)))
	keypairAuthHandler(deps).ServeHTTP(rr, req)
	if rr.Code != http.StatusUnauthorized {
		t.Fatalf("status = %d, want 401", rr.Code)
	}
}

func TestRegisterKeypairAuthSkipsWhenNoAllowlist(t *testing.T) {
	mux := http.NewServeMux()
	ok := registerKeypairAuth(mux, keypairHandlerDeps{
		Allowlist: nil,
		Audience:  "https://w.test/api/auth/keypair",
	})
	if ok {
		t.Fatal("must not register when Allowlist=nil")
	}
	// Probe : the route must not be mounted.
	rr := httptest.NewRecorder()
	req := httptest.NewRequest(http.MethodPost, "/api/auth/keypair", bytes.NewReader([]byte("x")))
	mux.ServeHTTP(rr, req)
	if rr.Code != http.StatusNotFound {
		t.Fatalf("status = %d, want 404 (route not mounted)", rr.Code)
	}
}

func TestRegisterKeypairAuthSkipsWhenEmptyEntries(t *testing.T) {
	list, _ := auth.LoadKeypairAllowlist(writeAllowlistTestFile(t, nil))
	mux := http.NewServeMux()
	if registerKeypairAuth(mux, keypairHandlerDeps{
		Allowlist: list,
		Audience:  "https://w.test/api/auth/keypair",
	}) {
		t.Fatal("must not register when allowlist is empty")
	}
}

func TestStatusForVerifyErrCoverage(t *testing.T) {
	cases := []struct {
		err    error
		status int
	}{
		{auth.ErrKPMalformed, http.StatusBadRequest},
		{auth.ErrKPBadSignature, http.StatusUnauthorized},
		{auth.ErrKPAudienceMismatch, http.StatusUnauthorized},
		{auth.ErrKPExpired, http.StatusUnauthorized},
		{auth.ErrKPFutureIat, http.StatusUnauthorized},
		{auth.ErrKPKeyNotAllowed, http.StatusUnauthorized},
		{auth.ErrKPAllowlistEmpty, http.StatusServiceUnavailable},
		{errors.New("something else"), http.StatusInternalServerError},
	}
	for _, c := range cases {
		if got := statusForVerifyErr(c.err); got != c.status {
			t.Errorf("statusForVerifyErr(%v) = %d, want %d", c.err, got, c.status)
		}
	}
}

func TestKeypairAudienceForTrimsTrailingSlash(t *testing.T) {
	if got := keypairAudienceFor("https://w.test/"); got != "https://w.test/api/auth/keypair" {
		t.Fatalf("got %q", got)
	}
	if got := keypairAudienceFor("https://w.test"); got != "https://w.test/api/auth/keypair" {
		t.Fatalf("got %q", got)
	}
}

func TestNonceProducesBase64(t *testing.T) {
	n := nonce()
	if _, err := base64.StdEncoding.DecodeString(n); err != nil {
		t.Fatalf("nonce() must produce base64-std, got %q", n)
	}
}
