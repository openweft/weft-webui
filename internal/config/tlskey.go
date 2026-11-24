package config

import (
	"fmt"
	"os"
	"runtime"
)

// checkTLSKeyFile refuses a TLS private key that other local users could
// read: it must be a regular file (not a symlink, which could be swapped) with
// no group or world permission bits. Errors name the path and the mode, never
// the content. POSIX modes are not meaningful on Windows, where only the
// file-type check applies.
func checkTLSKeyFile(path string) error {
	fi, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("WEBUI_TLS_KEY %s: %w", path, err)
	}
	if fi.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("WEBUI_TLS_KEY %s is a symlink ; point it at the key file itself", path)
	}
	if !fi.Mode().IsRegular() {
		return fmt.Errorf("WEBUI_TLS_KEY %s is not a regular file (mode %v)", path, fi.Mode())
	}
	if runtime.GOOS != "windows" && fi.Mode().Perm()&0o077 != 0 {
		return fmt.Errorf("WEBUI_TLS_KEY %s has mode %04o, readable by group or others ; run: chmod 600 %s", path, fi.Mode().Perm(), path)
	}
	return nil
}
