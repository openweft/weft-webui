package config

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"

	"github.com/go-authn/secretsource"
)

// Stderr receives the deprecation warnings. Tests replace it.
var Stderr io.Writer = os.Stderr

// secretEnv resolves one secret from either plainKey (deprecated, the value
// itself) or fromKey (a github.com/go-authn/secretsource reference such as
// "systemd-creds:NAME" or "file:/run/secrets/x"). Setting both is an error.
// The plaintext variable is removed from the process environment once read so
// child processes do not inherit it. Errors never contain the secret value.
func secretEnv(ctx context.Context, plainKey, fromKey string) (string, error) {
	plain, ref := os.Getenv(plainKey), os.Getenv(fromKey)
	if plain != "" && ref != "" {
		return "", fmt.Errorf("%s and %s are both set ; use only %s", plainKey, fromKey, fromKey)
	}
	if plain != "" {
		_ = os.Unsetenv(plainKey)
		fmt.Fprintf(Stderr, "weft-webui: WARNING: %s is deprecated (a plaintext secret in the environment is readable from /proc/<pid>/environ) ; use %s with a secretsource reference such as systemd-creds:NAME\n", plainKey, fromKey)
		return plain, nil
	}
	if ref == "" {
		return "", nil
	}
	s, err := secretsource.Open(ctx, ref)
	if err != nil {
		// err names the reference and the cause, never the secret.
		return "", fmt.Errorf("%s: %w", fromKey, err)
	}
	defer s.Close()
	if len(s.Bytes()) == 0 {
		return "", fmt.Errorf("%s: %w", fromKey, errors.New("the secret is empty"))
	}
	return string(s.Bytes()), nil
}
