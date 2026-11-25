package main

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/go-authn/secretsource"
)

const testPass = "smoke-pass-do-not-echo"

func passFile(t *testing.T, mode os.FileMode, content string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "pass")
	if err := os.WriteFile(p, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestResolvePassDefault(t *testing.T) {
	got, err := resolvePass(context.Background(), "")
	if err != nil || got != defaultMockPass {
		t.Fatalf("got %q, %v", got, err)
	}
}

func TestResolvePassBarePathAndFileRef(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("temp DACL refused by secretsource")
	}
	p := passFile(t, 0o600, testPass+"\n")
	for _, ref := range []string{p, "file:" + p} {
		got, err := resolvePass(context.Background(), ref)
		if err != nil || got != testPass {
			t.Fatalf("%q: got %q, %v", ref, got, err)
		}
	}
}

func TestResolvePassRefusals(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX modes")
	}
	loose := passFile(t, 0o644, testPass)
	empty := passFile(t, 0o600, "")
	cases := map[string]string{
		"group/other readable": loose,
		"empty file":           empty,
		"missing file":         filepath.Join(t.TempDir(), "absent"),
		"unknown scheme":       "nosuchscheme:x",
	}
	for name, ref := range cases {
		_, err := resolvePass(context.Background(), ref)
		if err == nil {
			t.Fatalf("%s: no error", name)
		}
		if !strings.Contains(err.Error(), "--pass-from") {
			t.Fatalf("%s: error does not name the flag: %v", name, err)
		}
		if strings.Contains(err.Error(), testPass) {
			t.Fatalf("%s: error echoes the secret", name)
		}
	}
	_, err := resolvePass(context.Background(), empty)
	if !errors.Is(err, secretsource.ErrEmpty) {
		t.Fatalf("empty: %v", err)
	}
}
