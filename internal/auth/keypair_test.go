package auth

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// signTestAssertion mimics the client's SignAssertion entirely in-test
// so this package stays decoupled from weft-app-core.
func signTestAssertion(t *testing.T, priv ed25519.PrivateKey, claims AssertionClaims) string {
	t.Helper()
	hdr, _ := json.Marshal(map[string]string{"alg": "EdDSA", "typ": "JWT"})
	body, _ := json.Marshal(claims)
	signingInput := base64.RawURLEncoding.EncodeToString(hdr) + "." +
		base64.RawURLEncoding.EncodeToString(body)
	sig := ed25519.Sign(priv, []byte(signingInput))
	return signingInput + "." + base64.RawURLEncoding.EncodeToString(sig)
}

// makeKeypair returns a fresh keypair + the base64-std pubkey.
func makeKeypair(t *testing.T) (ed25519.PrivateKey, ed25519.PublicKey, string) {
	t.Helper()
	pub, priv, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	return priv, pub, base64.StdEncoding.EncodeToString(pub)
}

func writeAllowlistFile(t *testing.T, dir string, entries []KeypairAllowlistEntry) string {
	t.Helper()
	p := filepath.Join(dir, "allowlist.json")
	b, _ := json.Marshal(KeypairAllowlistFile{Entries: entries})
	if err := os.WriteFile(p, b, 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestLoadKeypairAllowlistOK(t *testing.T) {
	_, _, pkA := makeKeypair(t)
	_, _, pkB := makeKeypair(t)
	dir := t.TempDir()
	path := writeAllowlistFile(t, dir, []KeypairAllowlistEntry{
		{Pubkey: pkA, Role: "superadmin", Label: "alice-laptop"},
		{Pubkey: pkB, Role: "tenant-admin", Label: "bob-laptop"},
	})
	list, err := LoadKeypairAllowlist(path)
	if err != nil {
		t.Fatal(err)
	}
	if list.Size() != 2 {
		t.Fatalf("size = %d, want 2", list.Size())
	}
	e, err := list.Lookup(pkA)
	if err != nil || e.Label != "alice-laptop" {
		t.Fatalf("Lookup(A) = %+v, %v", e, err)
	}
	if !e.IsSuperadmin() {
		t.Fatal("alice should be superadmin")
	}
}

func TestLoadKeypairAllowlistEmptyPath(t *testing.T) {
	list, err := LoadKeypairAllowlist("")
	if err == nil {
		t.Fatal("empty path should error")
	}
	// Returned list must still be non-nil so callers can safely call Err().
	if list == nil || list.Err() == nil {
		t.Fatal("list and Err must be populated on failure")
	}
}

func TestLoadKeypairAllowlistMissingFile(t *testing.T) {
	_, err := LoadKeypairAllowlist(filepath.Join(t.TempDir(), "nope.json"))
	if err == nil {
		t.Fatal("missing file should error")
	}
}

func TestLoadKeypairAllowlistRejectsBadEntry(t *testing.T) {
	dir := t.TempDir()
	p := writeAllowlistFile(t, dir, []KeypairAllowlistEntry{
		{Pubkey: "", Role: "superadmin", Label: "broken"},
	})
	if _, err := LoadKeypairAllowlist(p); err == nil {
		t.Fatal("empty pubkey should be rejected")
	}
	p2 := writeAllowlistFile(t, dir, []KeypairAllowlistEntry{
		{Pubkey: "AAA", Role: "superadmin", Label: "broken"},
	})
	if _, err := LoadKeypairAllowlist(p2); err == nil {
		t.Fatal("short pubkey should be rejected")
	}
	_, _, pk := makeKeypair(t)
	p3 := writeAllowlistFile(t, dir, []KeypairAllowlistEntry{
		{Pubkey: pk, Role: "", Label: "broken"},
	})
	if _, err := LoadKeypairAllowlist(p3); err == nil {
		t.Fatal("empty role should be rejected")
	}
}

func TestVerifyAssertionHappyPath(t *testing.T) {
	priv, pub, pkB64 := makeKeypair(t)
	_ = pub
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin", Label: "alice", Email: "alice@dev.local"},
	}))
	const aud = "https://weft.local:8080/api/auth/keypair"
	jws := signTestAssertion(t, priv, AssertionClaims{
		Iss:   "weft-app-osx",
		Sub:   pkB64,
		Aud:   aud,
		Iat:   time.Now().Unix(),
		Exp:   time.Now().Add(time.Minute).Unix(),
		Nonce: "n",
	})
	entry, claims, err := VerifyAssertion(jws, aud, list)
	if err != nil {
		t.Fatal(err)
	}
	if entry.Email != "alice@dev.local" {
		t.Fatalf("email = %q", entry.Email)
	}
	if claims.Aud != aud {
		t.Fatalf("aud = %q", claims.Aud)
	}
}

func TestVerifyAssertionBadSignature(t *testing.T) {
	priv, _, pkB64 := makeKeypair(t)
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}))
	const aud = "https://weft.local/api/auth/keypair"
	jws := signTestAssertion(t, priv, AssertionClaims{
		Iss: "weft-app-osx", Sub: pkB64, Aud: aud,
		Iat: time.Now().Unix(), Exp: time.Now().Add(time.Minute).Unix(), Nonce: "n",
	})
	parts := strings.Split(jws, ".")
	parts[2] = parts[2][:len(parts[2])-2] + "AA"
	bad := strings.Join(parts, ".")
	_, _, err := VerifyAssertion(bad, aud, list)
	if !errors.Is(err, ErrKPBadSignature) && err == nil {
		t.Fatalf("want signature error, got %v", err)
	}
}

func TestVerifyAssertionExpired(t *testing.T) {
	priv, _, pkB64 := makeKeypair(t)
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}))
	const aud = "https://w/api/auth/keypair"
	jws := signTestAssertion(t, priv, AssertionClaims{
		Iss: "weft-app-osx", Sub: pkB64, Aud: aud,
		Iat: time.Now().Add(-2 * time.Hour).Unix(),
		Exp: time.Now().Add(-time.Hour).Unix(),
	})
	_, _, err := VerifyAssertion(jws, aud, list)
	if !errors.Is(err, ErrKPExpired) {
		t.Fatalf("want expired, got %v", err)
	}
}

func TestVerifyAssertionFutureIat(t *testing.T) {
	priv, _, pkB64 := makeKeypair(t)
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}))
	const aud = "https://w/api/auth/keypair"
	jws := signTestAssertion(t, priv, AssertionClaims{
		Iss: "weft-app-osx", Sub: pkB64, Aud: aud,
		Iat: time.Now().Add(time.Hour).Unix(),
		Exp: time.Now().Add(2 * time.Hour).Unix(),
	})
	_, _, err := VerifyAssertion(jws, aud, list)
	if !errors.Is(err, ErrKPFutureIat) {
		t.Fatalf("want future-iat, got %v", err)
	}
}

func TestVerifyAssertionAudienceMismatch(t *testing.T) {
	priv, _, pkB64 := makeKeypair(t)
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkB64, Role: "superadmin"},
	}))
	jws := signTestAssertion(t, priv, AssertionClaims{
		Iss: "weft-app-osx", Sub: pkB64, Aud: "wrong",
		Iat: time.Now().Unix(), Exp: time.Now().Add(time.Minute).Unix(),
	})
	_, _, err := VerifyAssertion(jws, "right", list)
	if !errors.Is(err, ErrKPAudienceMismatch) {
		t.Fatalf("want aud-mismatch, got %v", err)
	}
}

func TestVerifyAssertionKeyNotAllowed(t *testing.T) {
	// Allowlist has key A ; the assertion is signed by key B.
	_, _, pkA := makeKeypair(t)
	privB, _, pkB := makeKeypair(t)
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), []KeypairAllowlistEntry{
		{Pubkey: pkA, Role: "superadmin"},
	}))
	const aud = "https://w/api/auth/keypair"
	jws := signTestAssertion(t, privB, AssertionClaims{
		Iss: "weft-app-osx", Sub: pkB, Aud: aud,
		Iat: time.Now().Unix(), Exp: time.Now().Add(time.Minute).Unix(),
	})
	_, _, err := VerifyAssertion(jws, aud, list)
	if !errors.Is(err, ErrKPKeyNotAllowed) {
		t.Fatalf("want key-not-allowed, got %v", err)
	}
}

func TestVerifyAssertionMalformed(t *testing.T) {
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), nil))
	if _, _, err := VerifyAssertion("only.two", "aud", list); !errors.Is(err, ErrKPMalformed) {
		t.Fatalf("want malformed, got %v", err)
	}
	if _, _, err := VerifyAssertion("a.b.c", "aud", list); !errors.Is(err, ErrKPMalformed) {
		t.Fatalf("want malformed for bad base64, got %v", err)
	}
}

func TestLookupOnEmptyAllowlistReturnsSentinel(t *testing.T) {
	list, _ := LoadKeypairAllowlist(writeAllowlistFile(t, t.TempDir(), nil))
	_, err := list.Lookup("anything")
	if !errors.Is(err, ErrKPAllowlistEmpty) {
		t.Fatalf("want allowlist-empty, got %v", err)
	}
}

func TestEntryEmailFallbacks(t *testing.T) {
	if (KeypairAllowlistEntry{}).EntryEmail() != "anonymous@keypair.local" {
		t.Fatal("blank entry should fall back to anonymous")
	}
	if (KeypairAllowlistEntry{Label: "alice"}).EntryEmail() != "alice@keypair.local" {
		t.Fatal("label-only entry should compose <label>@keypair.local")
	}
	if (KeypairAllowlistEntry{Email: "x@y"}).EntryEmail() != "x@y" {
		t.Fatal("explicit email should win")
	}
}

func TestNilAllowlistLookupSafe(t *testing.T) {
	var a *KeypairAllowlist
	if _, err := a.Lookup("k"); !errors.Is(err, ErrKPAllowlistEmpty) {
		t.Fatalf("nil allowlist should return ErrKPAllowlistEmpty, got %v", err)
	}
	if a.Size() != 0 {
		t.Fatal("nil allowlist size must be 0")
	}
}
