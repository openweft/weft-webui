package main

import (
	"context"
	"fmt"
	"strings"

	"github.com/go-authn/secretsource"
)

// defaultMockPass is the static password of the Dex mock connector shipped in
// deploy/ ; it protects nothing and is used only when --pass-from is absent.
const defaultMockPass = "password"

// resolvePass turns the --pass-from value into the password. An empty ref
// selects the mock default. A ref that starts with "/" or "./" or "../" is a
// file path and is read as "file:<path>", which secretsource refuses when
// the file is readable by group or others. Errors name the reference and the
// cause, never the secret.
func resolvePass(ctx context.Context, ref string) (string, error) {
	if ref == "" {
		return defaultMockPass, nil
	}
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "../") {
		ref = "file:" + ref
	}
	s, err := secretsource.Open(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("--pass-from: %w", err)
	}
	defer s.Close()
	return string(s.Bytes()), nil
}
