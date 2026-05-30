// Package auth implements OIDC login, signed-cookie sessions, and the
// context plumbing that carries the authenticated User (and their
// access token) down to the gRPC layer.
//
// The cookie is a base64-encoded JSON payload signed with HMAC-SHA256
// — no external store, no rotation pain, deliberately small. Tokens
// are kept in the cookie so that a webui restart doesn't kick every
// user out ; an attacker who steals the cookie has the user's token
// and that's it (same blast radius as a stolen bearer token).
package auth

import "context"

// User is what middleware injects into the request context after
// validating a session cookie. AccessToken is the OIDC access token,
// suitable for forwarding to vzd over gRPC. IDToken is kept so that
// the frontend can introspect groups / claims via /api/me if needed.
type User struct {
	Subject     string   `json:"sub"`
	Email       string   `json:"email,omitempty"`
	Name        string   `json:"name,omitempty"`
	Groups      []string `json:"groups,omitempty"`
	Project     string   `json:"project,omitempty"`
	AccessToken string   `json:"-"` // never serialised to JSON responses
	IDToken     string   `json:"-"`
	Refresh     string   `json:"-"`
	ExpiresAt   int64    `json:"-"` // unix seconds
	DevMode     bool     `json:"-"` // synthesised in dev mode
}

// Initials returns a 1–2 char avatar label derived from Name (or
// Email, or Subject as last resort).
func (u *User) Initials() string {
	src := u.Name
	if src == "" {
		src = u.Email
	}
	if src == "" {
		src = u.Subject
	}
	out := []rune{}
	prev := ' '
	for _, r := range src {
		if (prev == ' ' || prev == '.' || prev == '-' || prev == '@') && r != ' ' {
			out = append(out, r)
			if len(out) == 2 {
				break
			}
		}
		prev = r
	}
	if len(out) == 0 {
		return "?"
	}
	return string(out)
}

type ctxKey int

const userKey ctxKey = 0

// WithUser returns a copy of ctx that carries u.
func WithUser(ctx context.Context, u *User) context.Context {
	return context.WithValue(ctx, userKey, u)
}

// UserFromContext extracts the user, or nil if there is none. Handlers
// that require auth should be wrapped by Middleware (which both injects
// the user and rejects requests that lack one) ; this helper exists for
// downstream packages like wclient that need to read the token.
func UserFromContext(ctx context.Context) *User {
	if ctx == nil {
		return nil
	}
	u, _ := ctx.Value(userKey).(*User)
	return u
}

// BearerFromContext is a thin shortcut for the gRPC interceptor.
func BearerFromContext(ctx context.Context) string {
	u := UserFromContext(ctx)
	if u == nil {
		return ""
	}
	return u.AccessToken
}
