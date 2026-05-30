// tenant_handlers.go — empty since the migration to huma. All
// handlers moved to api_tenants.go ; the emailOf helper went with
// them (kept inline next to the operations that use it).
package server

import "github.com/openweft/weft-webui/internal/auth"

// emailOf returns the user's email or "" for a nil user. Used by the
// tenant-detail huma op when annotating the caller's role.
func emailOf(u *auth.User) string {
	if u == nil {
		return ""
	}
	return u.Email
}
