// user_test.go — Initials derivation + context helpers.
package auth

import (
	"context"
	"testing"
)

func TestUser_Initials(t *testing.T) {
	cases := []struct {
		name string
		u    User
		want string
	}{
		{"two words → first letters", User{Name: "Alice Bob"}, "AB"},
		{"single word → one letter", User{Name: "Alice"}, "A"},
		{"name split on dot", User{Name: "alice.bob"}, "ab"},
		{"name split on dash", User{Name: "alice-bob"}, "ab"},
		{"empty name falls back to email", User{Email: "carol@example.com"}, "ce"},
		{"empty name+email falls back to subject", User{Subject: "dave"}, "d"},
		{"all empty → ?", User{}, "?"},
		{"unicode runes", User{Name: "Éloïse"}, "É"},
		{"truncates to 2 initials", User{Name: "A B C D"}, "AB"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.u.Initials(); got != tc.want {
				t.Errorf("Initials(%+v) = %q, want %q", tc.u, got, tc.want)
			}
		})
	}
}

func TestUserFromContext_Nil(t *testing.T) {
	if u := UserFromContext(nil); u != nil {
		t.Errorf("UserFromContext(nil ctx) = %+v, want nil", u)
	}
	if u := UserFromContext(context.Background()); u != nil {
		t.Errorf("UserFromContext(empty ctx) = %+v, want nil", u)
	}
}

func TestUserFromContext_RoundTrip(t *testing.T) {
	in := &User{Subject: "eve", AccessToken: "at"}
	ctx := WithUser(context.Background(), in)
	out := UserFromContext(ctx)
	if out == nil || out.Subject != "eve" {
		t.Fatalf("UserFromContext = %+v, want eve", out)
	}
}

func TestBearerFromContext(t *testing.T) {
	if got := BearerFromContext(context.Background()); got != "" {
		t.Errorf("BearerFromContext(empty) = %q, want empty", got)
	}
	ctx := WithUser(context.Background(), &User{AccessToken: "tok-123"})
	if got := BearerFromContext(ctx); got != "tok-123" {
		t.Errorf("BearerFromContext = %q, want tok-123", got)
	}
}
