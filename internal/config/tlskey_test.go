package config

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
)

const keyContent = "-----BEGIN PRIVATE KEY-----secretbytes"

func keyFile(t *testing.T, mode os.FileMode) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "key.pem")
	if err := os.WriteFile(p, []byte(keyContent), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(p, mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestTLSKeyFileChecks(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes and symlinks")
	}
	if err := checkTLSKeyFile(keyFile(t, 0o600)); err != nil {
		t.Fatalf("0600 refused: %v", err)
	}
	if err := checkTLSKeyFile(keyFile(t, 0o400)); err != nil {
		t.Fatalf("0400 refused: %v", err)
	}
	for _, mode := range []os.FileMode{0o640, 0o604, 0o644, 0o660, 0o666} {
		p := keyFile(t, mode)
		err := checkTLSKeyFile(p)
		if err == nil {
			t.Fatalf("mode %04o accepted", mode)
		}
		if !strings.Contains(err.Error(), p) || !strings.Contains(err.Error(), "chmod 600") || strings.Contains(err.Error(), "secretbytes") {
			t.Fatalf("mode %04o: err = %v", mode, err)
		}
	}
	dir := t.TempDir()
	if err := checkTLSKeyFile(dir); err == nil || !strings.Contains(err.Error(), "not a regular file") {
		t.Fatalf("directory: err = %v", err)
	}
	link := filepath.Join(t.TempDir(), "link.pem")
	if err := os.Symlink(keyFile(t, 0o600), link); err != nil {
		t.Fatal(err)
	}
	if err := checkTLSKeyFile(link); err == nil || !strings.Contains(err.Error(), "symlink") {
		t.Fatalf("symlink: err = %v", err)
	}
	if err := checkTLSKeyFile(filepath.Join(dir, "missing")); err == nil {
		t.Fatal("missing file accepted")
	}
}

func TestValidateRefusesLooseTLSKey(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	c := &Config{DevMode: true, AuthMode: "none", TLSCert: "cert.pem", TLSKey: keyFile(t, 0o644)}
	if err := c.Validate(); err == nil || !strings.Contains(err.Error(), "0644") {
		t.Fatalf("err = %v", err)
	}
	c.TLSKey = keyFile(t, 0o600)
	if err := c.Validate(); err != nil {
		t.Fatalf("err = %v", err)
	}
}
