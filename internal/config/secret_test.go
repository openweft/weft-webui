package config

import (
	"bytes"
	"flag"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const testSecret = "s3cr3t-value-do-not-echo"

func loadWith(t *testing.T) (*Config, string, error) {
	t.Helper()
	var buf bytes.Buffer
	old := Stderr
	Stderr = &buf
	t.Cleanup(func() { Stderr = old })
	cfg, err := Load(flag.NewFlagSet("t", flag.ContinueOnError))
	return cfg, buf.String(), err
}

func secretFile(t *testing.T, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "secret")
	if err := os.WriteFile(p, []byte(content), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestOIDCClientSecretFromRef(t *testing.T) {
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", "")
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "file:"+secretFile(t, testSecret+"\n"))
	cfg, warn, err := loadWith(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCClientSecret != testSecret {
		t.Fatalf("secret = %q", cfg.OIDCClientSecret)
	}
	if warn != "" {
		t.Fatalf("unexpected warning %q", warn)
	}
}

func TestOIDCClientSecretPlaintextDeprecated(t *testing.T) {
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", testSecret)
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "")
	cfg, warn, err := loadWith(t)
	if err != nil {
		t.Fatal(err)
	}
	if cfg.OIDCClientSecret != testSecret {
		t.Fatalf("secret = %q", cfg.OIDCClientSecret)
	}
	if !strings.Contains(warn, "deprecated") || !strings.Contains(warn, "WEBUI_OIDC_CLIENT_SECRET_FROM") {
		t.Fatalf("warning = %q", warn)
	}
	if strings.Contains(warn, testSecret) {
		t.Fatal("warning echoes the secret")
	}
	if v, ok := os.LookupEnv("WEBUI_OIDC_CLIENT_SECRET"); ok {
		t.Fatalf("plaintext variable still set: %q", v)
	}
}

func TestOIDCClientSecretBothSetIsError(t *testing.T) {
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", testSecret)
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "file:"+secretFile(t, testSecret))
	_, _, err := loadWith(t)
	if err == nil || !strings.Contains(err.Error(), "both set") {
		t.Fatalf("err = %v", err)
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatal("error echoes the secret")
	}
}

func TestOIDCClientSecretUnresolvableRef(t *testing.T) {
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", "")
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "file:"+filepath.Join(t.TempDir(), "missing"))
	_, _, err := loadWith(t)
	if err == nil || !strings.Contains(err.Error(), "WEBUI_OIDC_CLIENT_SECRET_FROM") {
		t.Fatalf("err = %v", err)
	}
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "bogus-scheme:"+testSecret)
	_, _, err = loadWith(t)
	if err == nil {
		t.Fatal("malformed reference accepted")
	}
	if strings.Contains(err.Error(), testSecret) {
		t.Fatal("error echoes the value given as reference")
	}
}

func TestOIDCClientSecretLooseFileModeRefused(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	p := secretFile(t, testSecret)
	if err := os.Chmod(p, 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", "")
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "file:"+p)
	_, _, err := loadWith(t)
	if err == nil || strings.Contains(err.Error(), testSecret) {
		t.Fatalf("err = %v", err)
	}
}

const hexKey = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestSessionKeyPaths(t *testing.T) {
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET", "")
	t.Setenv("WEBUI_OIDC_CLIENT_SECRET_FROM", "")

	t.Setenv("WEBUI_SESSION_KEY", "")
	t.Setenv("WEBUI_SESSION_KEY_FROM", "file:"+secretFile(t, hexKey+"\n"))
	cfg, warn, err := loadWith(t)
	if err != nil || len(cfg.SessionKey) != 32 || warn != "" {
		t.Fatalf("ref: key=%d warn=%q err=%v", len(cfg.SessionKey), warn, err)
	}

	t.Setenv("WEBUI_SESSION_KEY_FROM", "")
	t.Setenv("WEBUI_SESSION_KEY", hexKey)
	cfg, warn, err = loadWith(t)
	if err != nil || len(cfg.SessionKey) != 32 {
		t.Fatalf("plain: err=%v", err)
	}
	if !strings.Contains(warn, "WEBUI_SESSION_KEY_FROM") || strings.Contains(warn, hexKey) {
		t.Fatalf("warning = %q", warn)
	}
	if _, ok := os.LookupEnv("WEBUI_SESSION_KEY"); ok {
		t.Fatal("plaintext session key still in environment")
	}

	t.Setenv("WEBUI_SESSION_KEY", hexKey)
	t.Setenv("WEBUI_SESSION_KEY_FROM", "file:"+secretFile(t, hexKey))
	if _, _, err = loadWith(t); err == nil {
		t.Fatal("both set accepted")
	}

	t.Setenv("WEBUI_SESSION_KEY", "")
	t.Setenv("WEBUI_SESSION_KEY_FROM", "file:"+filepath.Join(t.TempDir(), "missing"))
	if _, _, err = loadWith(t); err == nil {
		t.Fatal("unresolvable ref accepted")
	}
}
