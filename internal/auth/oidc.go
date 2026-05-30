// oidc.go — OIDC Authorization Code flow handlers.
//
// /api/auth/login    — generate state + PKCE verifier, stash in a
//                      short-lived cookie, redirect to the IdP.
// /api/auth/callback — exchange code for tokens, verify ID token,
//                      mint a session cookie, redirect to return_to.
// /api/auth/logout   — clear the session, redirect home.
// /api/me            — JSON view of the current user (or 401).
//
// The state cookie is signed with the same HMAC key as the session ;
// it carries the PKCE code_verifier and the post-login return_to URL.
// Short max-age (5 min) is enough to make replay impractical without
// needing a server-side store.
package auth

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"
)

// OIDCConfig holds the validated OIDC settings handed to NewOIDC.
type OIDCConfig struct {
	Issuer       string
	ClientID     string
	ClientSecret string
	RedirectURL  string
	Scopes       []string
}

// OIDC bundles the verifier + oauth2 config + session store. Built once
// at startup ; safe for concurrent use.
type OIDC struct {
	cfg       *oauth2.Config
	verifier  *oidc.IDTokenVerifier
	provider  *oidc.Provider
	session   *SessionStore
	stateName string
}

// NewOIDC reaches out to the IdP for its discovery document and builds
// the verifier. Errors here are fatal — the operator wants the daemon
// to refuse to start with a bad config rather than 500 on first login.
func NewOIDC(ctx context.Context, c OIDCConfig, s *SessionStore) (*OIDC, error) {
	if c.Issuer == "" || c.ClientID == "" || c.RedirectURL == "" {
		return nil, errors.New("oidc: issuer, client id, and redirect url are required")
	}
	provider, err := oidc.NewProvider(ctx, c.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc: discovery %s: %w", c.Issuer, err)
	}
	scopes := c.Scopes
	if len(scopes) == 0 {
		scopes = []string{oidc.ScopeOpenID, "email", "profile", "groups"}
	}
	return &OIDC{
		cfg: &oauth2.Config{
			ClientID:     c.ClientID,
			ClientSecret: c.ClientSecret,
			Endpoint:     provider.Endpoint(),
			RedirectURL:  c.RedirectURL,
			Scopes:       scopes,
		},
		verifier:  provider.Verifier(&oidc.Config{ClientID: c.ClientID}),
		provider:  provider,
		session:   s,
		stateName: s.Name + "_oidc_state",
	}, nil
}

// stateBlob is what we stash in the temporary cookie between /login
// and /callback. Signing it with the session key removes the need for
// a server-side state store.
type stateBlob struct {
	Nonce        string `json:"n"`
	State        string `json:"s"`
	CodeVerifier string `json:"v"`
	ReturnTo     string `json:"r"`
	ExpiresAt    int64  `json:"exp"`
}

// LoginHandler implements GET /api/auth/login.
func (o *OIDC) LoginHandler(w http.ResponseWriter, r *http.Request) {
	state, err := randString(24)
	if err != nil {
		http.Error(w, "rand: "+err.Error(), http.StatusInternalServerError)
		return
	}
	nonce, err := randString(24)
	if err != nil {
		http.Error(w, "rand: "+err.Error(), http.StatusInternalServerError)
		return
	}
	verifier, err := randString(48)
	if err != nil {
		http.Error(w, "rand: "+err.Error(), http.StatusInternalServerError)
		return
	}
	challenge := pkceChallenge(verifier)

	returnTo := r.URL.Query().Get("return_to")
	if !isSafeReturn(returnTo) {
		returnTo = "/"
	}

	blob := stateBlob{
		Nonce:        nonce,
		State:        state,
		CodeVerifier: verifier,
		ReturnTo:     returnTo,
		ExpiresAt:    time.Now().Add(5 * time.Minute).Unix(),
	}
	if err := o.setStateCookie(w, &blob); err != nil {
		http.Error(w, "state: "+err.Error(), http.StatusInternalServerError)
		return
	}

	authURL := o.cfg.AuthCodeURL(state,
		oidc.Nonce(nonce),
		oauth2.SetAuthURLParam("code_challenge", challenge),
		oauth2.SetAuthURLParam("code_challenge_method", "S256"),
	)
	http.Redirect(w, r, authURL, http.StatusFound)
}

// CallbackHandler implements GET /api/auth/callback.
func (o *OIDC) CallbackHandler(w http.ResponseWriter, r *http.Request) {
	blob, err := o.readStateCookie(r)
	if err != nil {
		http.Error(w, "auth: missing or invalid state cookie", http.StatusBadRequest)
		return
	}
	o.clearStateCookie(w)

	if e := r.URL.Query().Get("error"); e != "" {
		desc := r.URL.Query().Get("error_description")
		http.Error(w, "auth: provider returned "+e+": "+desc, http.StatusUnauthorized)
		return
	}
	if got := r.URL.Query().Get("state"); got != blob.State {
		http.Error(w, "auth: state mismatch", http.StatusBadRequest)
		return
	}
	code := r.URL.Query().Get("code")
	if code == "" {
		http.Error(w, "auth: no code", http.StatusBadRequest)
		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	tok, err := o.cfg.Exchange(ctx, code, oauth2.SetAuthURLParam("code_verifier", blob.CodeVerifier))
	if err != nil {
		http.Error(w, "auth: token exchange: "+err.Error(), http.StatusBadGateway)
		return
	}
	rawIDToken, _ := tok.Extra("id_token").(string)
	if rawIDToken == "" {
		http.Error(w, "auth: no id_token in response", http.StatusBadGateway)
		return
	}
	idTok, err := o.verifier.Verify(ctx, rawIDToken)
	if err != nil {
		http.Error(w, "auth: id token verify: "+err.Error(), http.StatusUnauthorized)
		return
	}
	if idTok.Nonce != blob.Nonce {
		http.Error(w, "auth: nonce mismatch", http.StatusUnauthorized)
		return
	}

	var claims struct {
		Sub    string   `json:"sub"`
		Email  string   `json:"email"`
		Name   string   `json:"name"`
		Groups []string `json:"groups"`
	}
	if err := idTok.Claims(&claims); err != nil {
		http.Error(w, "auth: claims: "+err.Error(), http.StatusBadGateway)
		return
	}

	exp := idTok.Expiry.Unix()
	if tok.Expiry.After(idTok.Expiry) {
		exp = tok.Expiry.Unix()
	}
	payload := &SessionPayload{
		Subject:      claims.Sub,
		Email:        claims.Email,
		Name:         claims.Name,
		Groups:       claims.Groups,
		AccessToken:  tok.AccessToken,
		IDToken:      rawIDToken,
		RefreshToken: tok.RefreshToken,
		ExpiresAt:    exp,
	}
	if err := o.session.Set(w, payload); err != nil {
		http.Error(w, "session: "+err.Error(), http.StatusInternalServerError)
		return
	}
	http.Redirect(w, r, blob.ReturnTo, http.StatusFound)
}

// LogoutHandler clears the session cookie. Optionally redirects via
// the IdP's end_session_endpoint when the provider advertises one.
func (o *OIDC) LogoutHandler(w http.ResponseWriter, r *http.Request) {
	o.session.Clear(w)
	// We could redirect to the IdP's end_session_endpoint here for
	// single-logout ; for now just bounce back to /.
	if r.Method == http.MethodGet {
		http.Redirect(w, r, "/", http.StatusFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

// --- state cookie helpers ---

func (o *OIDC) setStateCookie(w http.ResponseWriter, b *stateBlob) error {
	raw, err := json.Marshal(b)
	if err != nil {
		return err
	}
	sig, err := signHex(o.session.Key, raw)
	if err != nil {
		return err
	}
	value := base64.RawURLEncoding.EncodeToString(raw) + "." + sig
	http.SetCookie(w, &http.Cookie{
		Name:     o.stateName,
		Value:    value,
		Path:     "/api/auth/",
		Domain:   o.session.Domain,
		MaxAge:   300,
		Secure:   o.session.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
	return nil
}

func (o *OIDC) readStateCookie(r *http.Request) (*stateBlob, error) {
	c, err := r.Cookie(o.stateName)
	if err != nil {
		return nil, err
	}
	dot := strings.LastIndexByte(c.Value, '.')
	if dot <= 0 {
		return nil, errors.New("malformed")
	}
	raw, err := base64.RawURLEncoding.DecodeString(c.Value[:dot])
	if err != nil {
		return nil, err
	}
	sig := c.Value[dot+1:]
	if !verifyHex(o.session.Key, raw, sig) {
		return nil, errors.New("bad signature")
	}
	var b stateBlob
	if err := json.Unmarshal(raw, &b); err != nil {
		return nil, err
	}
	if time.Now().Unix() > b.ExpiresAt {
		return nil, errors.New("expired")
	}
	return &b, nil
}

func (o *OIDC) clearStateCookie(w http.ResponseWriter) {
	http.SetCookie(w, &http.Cookie{
		Name:     o.stateName,
		Value:    "",
		Path:     "/api/auth/",
		Domain:   o.session.Domain,
		MaxAge:   -1,
		Secure:   o.session.Secure,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	})
}

// --- small crypto helpers (avoid pulling all of crypto/hmac into the caller) ---

func randString(n int) (string, error) {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return base64.RawURLEncoding.EncodeToString(b), nil
}

func pkceChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// isSafeReturn keeps `return_to` confined to the same origin (path
// only). Rejects scheme-relative or absolute URLs to prevent open
// redirects after login.
func isSafeReturn(s string) bool {
	if s == "" {
		return false
	}
	if !strings.HasPrefix(s, "/") {
		return false
	}
	if strings.HasPrefix(s, "//") {
		return false
	}
	if _, err := url.Parse(s); err != nil {
		return false
	}
	return true
}
