// session_test.go — covers SessionStore.Encode/Decode happy + error paths,
// Set/Clear/Read round-trips, expiry handling, and the
// payloadToUser/userToPayload projection.
package auth

import (
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestNewSessionStore_DefaultsAndCustomName(t *testing.T) {
	// Default name path.
	s := NewSessionStore([]byte("k"), "", "example.com", true, 60)
	if s.Name != "weft_webui_session" {
		t.Errorf("default name = %q, want weft_webui_session", s.Name)
	}
	if s.Path != "/" {
		t.Errorf("default Path = %q, want /", s.Path)
	}
	if s.SameSite != http.SameSiteLaxMode {
		t.Errorf("default SameSite = %v, want Lax", s.SameSite)
	}
	if !s.Secure {
		t.Errorf("Secure = false, want true")
	}

	// Custom name preserved.
	s2 := NewSessionStore([]byte("k"), "my_sess", "", false, 0)
	if s2.Name != "my_sess" {
		t.Errorf("Name = %q, want my_sess", s2.Name)
	}
}

func TestSession_EncodeDecode_RoundTrip(t *testing.T) {
	s := NewSessionStore([]byte("hmac-key-for-tests"), "test_sess", "", false, 3600)
	in := &SessionPayload{
		Subject:      "alice",
		Email:        "alice@example.com",
		Name:         "Alice",
		Groups:       []string{"admins", "ops"},
		Tenant:       "acme",
		Project:      "team-alpha",
		AccessToken:  "at",
		IDToken:      "it",
		RefreshToken: "rt",
		ExpiresAt:    time.Now().Add(1 * time.Hour).Unix(),
	}
	v, err := s.Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := s.Decode(v)
	if err != nil {
		t.Fatalf("Decode: %v", err)
	}
	if out.Subject != in.Subject || out.Email != in.Email || out.Name != in.Name {
		t.Errorf("identity fields differ : got %+v want %+v", out, in)
	}
	if len(out.Groups) != 2 || out.Groups[0] != "admins" || out.Groups[1] != "ops" {
		t.Errorf("Groups = %v", out.Groups)
	}
	if out.Tenant != "acme" || out.Project != "team-alpha" {
		t.Errorf("scope fields lost : tenant=%q project=%q", out.Tenant, out.Project)
	}
	if out.AccessToken != "at" || out.IDToken != "it" || out.RefreshToken != "rt" {
		t.Errorf("token fields lost")
	}
}

func TestSession_Encode_EmptyKey(t *testing.T) {
	s := &SessionStore{Key: nil, Name: "x"}
	if _, err := s.Encode(&SessionPayload{Subject: "x"}); err == nil {
		t.Fatalf("Encode with empty key succeeded ; want error")
	}
}

func TestSession_Decode_EmptyKey(t *testing.T) {
	s := &SessionStore{Key: nil, Name: "x"}
	if _, err := s.Decode("anything.deadbeef"); err == nil {
		t.Fatalf("Decode with empty key succeeded ; want error")
	}
}

func TestSession_Decode_Malformed(t *testing.T) {
	s := NewSessionStore([]byte("k"), "n", "", false, 0)

	cases := []struct {
		name string
		in   string
	}{
		{"empty", ""},
		{"no dot", "abcdef"},
		{"leading dot", ".sig"},
		{"trailing dot", "payload."},
		{"bad base64", "!!not-base64!!.deadbeef"},
		{"bad hex sig", "QQ.zzzz"}, // valid b64 "A" + non-hex sig
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := s.Decode(tc.in); !errors.Is(err, ErrBadSignature) {
				t.Errorf("Decode(%q) err = %v, want ErrBadSignature", tc.in, err)
			}
		})
	}
}

func TestSession_Decode_BadSignature(t *testing.T) {
	s := NewSessionStore([]byte("k1"), "n", "", false, 0)
	other := NewSessionStore([]byte("DIFFERENT-KEY"), "n", "", false, 0)

	// Sign with the other key, try to decode with s — must fail.
	v, err := other.Encode(&SessionPayload{Subject: "x", ExpiresAt: time.Now().Add(time.Hour).Unix()})
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := s.Decode(v); !errors.Is(err, ErrBadSignature) {
		t.Errorf("Decode with wrong key err = %v, want ErrBadSignature", err)
	}
}

func TestSession_Decode_BadJSON(t *testing.T) {
	// Construct a value where the signature is valid (so we get past the
	// HMAC check) but the payload is not valid JSON — exercises the
	// json.Unmarshal failure branch in Decode.
	s := NewSessionStore([]byte("k"), "n", "", false, 0)
	garbage := []byte("not-json-at-all")
	sig, err := signHex(s.Key, garbage)
	if err != nil {
		t.Fatalf("signHex: %v", err)
	}
	value := base64.RawURLEncoding.EncodeToString(garbage) + "." + sig
	if _, err := s.Decode(value); !errors.Is(err, ErrBadSignature) {
		t.Errorf("Decode(corrupt json) err = %v, want ErrBadSignature", err)
	}
}

func TestSession_Decode_Expired(t *testing.T) {
	s := NewSessionStore([]byte("k"), "n", "", false, 0)
	p := &SessionPayload{Subject: "x", ExpiresAt: time.Now().Add(-1 * time.Hour).Unix()}
	v, err := s.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	out, err := s.Decode(v)
	if !errors.Is(err, ErrExpired) {
		t.Fatalf("Decode(expired) err = %v, want ErrExpired", err)
	}
	if out == nil || out.Subject != "x" {
		t.Errorf("expired Decode should still return payload (got %+v) for diagnostics", out)
	}
}

func TestSession_Decode_NoExpiryAllowed(t *testing.T) {
	// ExpiresAt = 0 means legacy / unbounded session — must NOT return ErrExpired.
	s := NewSessionStore([]byte("k"), "n", "", false, 0)
	p := &SessionPayload{Subject: "legacy", ExpiresAt: 0}
	v, err := s.Encode(p)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	if _, err := s.Decode(v); err != nil {
		t.Errorf("Decode(no expiry) err = %v, want nil", err)
	}
}

func TestSession_Set_IssuesCookie(t *testing.T) {
	s := NewSessionStore([]byte("k"), "test_sess", ".example.com", true, 1800)
	rr := httptest.NewRecorder()
	p := &SessionPayload{Subject: "x", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	if err := s.Set(rr, p); err != nil {
		t.Fatalf("Set: %v", err)
	}
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.Name != "test_sess" {
		t.Errorf("Name = %q", c.Name)
	}
	if !c.HttpOnly {
		t.Error("HttpOnly must be true")
	}
	if !c.Secure {
		t.Error("Secure must be true (we built the store with secure=true)")
	}
	// net/http strips the leading dot when parsing Domain back ; compare
	// the stripped form.
	if c.Domain != "example.com" && c.Domain != ".example.com" {
		t.Errorf("Domain = %q, want example.com or .example.com", c.Domain)
	}
	if c.MaxAge != 1800 {
		t.Errorf("MaxAge = %d", c.MaxAge)
	}
}

func TestSession_Set_EncodeError(t *testing.T) {
	// Empty key triggers Encode's error path, which Set must propagate.
	s := &SessionStore{Key: nil, Name: "x"}
	rr := httptest.NewRecorder()
	if err := s.Set(rr, &SessionPayload{Subject: "x"}); err == nil {
		t.Fatalf("Set with empty key succeeded ; want error")
	}
}

func TestSession_Clear_IssuesExpiredCookie(t *testing.T) {
	s := NewSessionStore([]byte("k"), "test_sess", "", false, 1800)
	rr := httptest.NewRecorder()
	s.Clear(rr)
	cookies := rr.Result().Cookies()
	if len(cookies) != 1 {
		t.Fatalf("got %d cookies, want 1", len(cookies))
	}
	c := cookies[0]
	if c.MaxAge != -1 {
		t.Errorf("Clear set MaxAge = %d, want -1", c.MaxAge)
	}
	if c.Value != "" {
		t.Errorf("Clear set Value = %q, want empty", c.Value)
	}
	if c.Name != "test_sess" {
		t.Errorf("Name = %q", c.Name)
	}
}

func TestSession_Read_MissingCookie(t *testing.T) {
	s := NewSessionStore([]byte("k"), "test_sess", "", false, 0)
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	out, err := s.Read(req)
	if !errors.Is(err, ErrNoSession) {
		t.Errorf("Read(no cookie) err = %v, want ErrNoSession", err)
	}
	if out != nil {
		t.Errorf("Read(no cookie) payload = %+v, want nil", out)
	}
}

func TestSession_Read_RoundTrip(t *testing.T) {
	s := NewSessionStore([]byte("k"), "test_sess", "", false, 0)
	in := &SessionPayload{Subject: "bob", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	v, err := s.Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: s.Name, Value: v})
	out, err := s.Read(req)
	if err != nil {
		t.Fatalf("Read: %v", err)
	}
	if out.Subject != "bob" {
		t.Errorf("Subject = %q, want bob", out.Subject)
	}
}

func TestSession_Read_TamperedCookie(t *testing.T) {
	s := NewSessionStore([]byte("k"), "test_sess", "", false, 0)
	in := &SessionPayload{Subject: "mallory", ExpiresAt: time.Now().Add(time.Hour).Unix()}
	v, err := s.Encode(in)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	// Flip a byte in the signature part.
	dot := strings.LastIndexByte(v, '.')
	if dot < 0 {
		t.Fatalf("malformed encoded value")
	}
	tampered := v[:dot+1] + flipFirstHex(v[dot+1:])
	req := httptest.NewRequest(http.MethodGet, "/api/me", nil)
	req.AddCookie(&http.Cookie{Name: s.Name, Value: tampered})
	_, err = s.Read(req)
	if !errors.Is(err, ErrBadSignature) {
		t.Errorf("Read(tampered) err = %v, want ErrBadSignature", err)
	}
}

// flipFirstHex inverts the first hex character so the signature stops matching.
func flipFirstHex(sig string) string {
	if sig == "" {
		return sig
	}
	swap := map[byte]byte{'0': '1', '1': '0', 'a': 'b', 'b': 'a'}
	b := []byte(sig)
	if r, ok := swap[b[0]]; ok {
		b[0] = r
	} else {
		b[0] = '0'
	}
	return string(b)
}

func TestPayloadToUser_AndBack(t *testing.T) {
	p := &SessionPayload{
		Subject:      "s",
		Email:        "e@e",
		Name:         "n",
		Groups:       []string{"g1"},
		Tenant:       "t",
		Project:      "p",
		AccessToken:  "at",
		IDToken:      "it",
		RefreshToken: "rt",
		ExpiresAt:    42,
	}
	u := payloadToUser(p)
	if u == nil || u.Subject != "s" || u.Email != "e@e" || u.Name != "n" {
		t.Fatalf("payloadToUser missed basic fields : %+v", u)
	}
	if u.Tenant != "t" || u.Project != "p" {
		t.Errorf("scope not projected : %+v", u)
	}
	if u.AccessToken != "at" || u.IDToken != "it" || u.Refresh != "rt" {
		t.Errorf("tokens not projected")
	}
	if u.ExpiresAt != 42 {
		t.Errorf("ExpiresAt = %d", u.ExpiresAt)
	}
	if len(u.Groups) != 1 || u.Groups[0] != "g1" {
		t.Errorf("Groups = %v", u.Groups)
	}

	back := userToPayload(u)
	if back.Subject != p.Subject || back.RefreshToken != p.RefreshToken || back.ExpiresAt != p.ExpiresAt {
		t.Errorf("userToPayload round-trip lost fields : %+v vs %+v", back, p)
	}
}
