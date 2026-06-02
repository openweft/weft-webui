// tls_test.go — pins StrictTLSConfig + the WEBUI_TLS_MIN_VERSION
// parser. End-to-end handshake tests (real TLS server + client) are
// expensive ; here we assert the cipher whitelist + min version are
// what we expect.

package config

import (
	"crypto/tls"
	"flag"
	"strings"
	"testing"
)

func TestStrictTLSConfig_DefaultsTo12(t *testing.T) {
	c := &Config{}
	tc := c.StrictTLSConfig()
	if tc.MinVersion != tls.VersionTLS12 {
		t.Errorf("MinVersion = %d, want %d (TLS 1.2)", tc.MinVersion, tls.VersionTLS12)
	}
}

func TestStrictTLSConfig_HonoursTLS13(t *testing.T) {
	c := &Config{TLSMinVersion: tls.VersionTLS13}
	tc := c.StrictTLSConfig()
	if tc.MinVersion != tls.VersionTLS13 {
		t.Errorf("MinVersion = %d, want %d (TLS 1.3)", tc.MinVersion, tls.VersionTLS13)
	}
}

func TestStrictTLSConfig_CipherWhitelistIsAEADOnly(t *testing.T) {
	c := &Config{}
	tc := c.StrictTLSConfig()
	allowed := map[uint16]bool{
		tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256: true,
		tls.TLS_ECDHE_RSA_WITH_AES_128_GCM_SHA256:   true,
		tls.TLS_ECDHE_ECDSA_WITH_AES_256_GCM_SHA384: true,
		tls.TLS_ECDHE_RSA_WITH_AES_256_GCM_SHA384:   true,
		tls.TLS_ECDHE_ECDSA_WITH_CHACHA20_POLY1305:  true,
		tls.TLS_ECDHE_RSA_WITH_CHACHA20_POLY1305:    true,
	}
	for _, s := range tc.CipherSuites {
		if !allowed[s] {
			t.Errorf("cipher suite 0x%04x is in the whitelist but not on the AEAD allow-list", s)
		}
	}
	got := map[uint16]bool{}
	for _, s := range tc.CipherSuites {
		got[s] = true
	}
	for s := range allowed {
		if !got[s] {
			t.Errorf("cipher suite 0x%04x missing from the whitelist", s)
		}
	}
}

func TestStrictTLSConfig_CurvesArePinned(t *testing.T) {
	c := &Config{}
	tc := c.StrictTLSConfig()
	want := []tls.CurveID{tls.X25519, tls.CurveP256, tls.CurveP384}
	if len(tc.CurvePreferences) != len(want) {
		t.Fatalf("len(CurvePreferences) = %d, want %d", len(tc.CurvePreferences), len(want))
	}
	for i, w := range want {
		if tc.CurvePreferences[i] != w {
			t.Errorf("CurvePreferences[%d] = %v, want %v", i, tc.CurvePreferences[i], w)
		}
	}
}

func TestLoad_TLSMinVersion_Default(t *testing.T) {
	t.Setenv("WEBUI_TLS_MIN_VERSION", "")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c, err := Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSMinVersion != tls.VersionTLS12 {
		t.Errorf("default = %d, want %d (TLS 1.2)", c.TLSMinVersion, tls.VersionTLS12)
	}
}

func TestLoad_TLSMinVersion_13(t *testing.T) {
	t.Setenv("WEBUI_TLS_MIN_VERSION", "1.3")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	c, err := Load(fs)
	if err != nil {
		t.Fatal(err)
	}
	if c.TLSMinVersion != tls.VersionTLS13 {
		t.Errorf("1.3 = %d, want %d", c.TLSMinVersion, tls.VersionTLS13)
	}
}

func TestLoad_TLSMinVersion_RejectsBadVersion(t *testing.T) {
	t.Setenv("WEBUI_TLS_MIN_VERSION", "1.1")
	fs := flag.NewFlagSet("test", flag.ContinueOnError)
	_, err := Load(fs)
	if err == nil {
		t.Fatal("Load: want error for TLS 1.1, got nil")
	}
	if !strings.Contains(err.Error(), "WEBUI_TLS_MIN_VERSION") {
		t.Errorf("err = %q, want to mention the env var", err)
	}
}
