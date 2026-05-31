// Command oidc-smoke drives the canonical OIDC Authorization Code flow
// end-to-end against a running weft-webui + Dex. It is an operator
// drill — not a CI gate (CI has no Dex). See
// docs/operations/oidc-smoke-test.md for the full operator handbook.
//
// Steps :
//
//  1. GET <webui>/api/auth/login           — expect 302 to Dex authorize URL
//  2. GET that authorize URL on Dex        — expect 200 with the mock-connector
//                                            login form (or 302 to it)
//  3. POST the form with the mock creds    — Dex redirects back to webui's
//                                            /api/auth/callback?code=...&state=...
//  4. GET the callback URL                 — webui exchanges the code, mints a
//                                            session cookie, redirects to "/"
//  5. GET <webui>/api/me with the cookie   — expect 200 + a JSON body whose
//                                            "email" field matches the user we
//                                            logged in as
//
// Env vars (all optional except DEX_ISSUER for clarity in logs) :
//
//	WEBUI_BASE      default http://localhost:8080   — user-facing listener
//	DEX_ISSUER      default http://localhost:5556/dex
//	OIDC_USER       default admin@example.com       — mock-connector username
//	OIDC_PASS       default password                — mock-connector password
//	HTTP_TIMEOUT    default 15s                     — per-request timeout
//
// Exit status : 0 = the whole flow succeeded ; non-zero = the step name
// printed last is the one that failed (stderr carries the cause).
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"regexp"
	"strings"
	"time"
)

func main() {
	var (
		webui    = envOr("WEBUI_BASE", "http://localhost:8080")
		dex      = envOr("DEX_ISSUER", "http://localhost:5556/dex")
		user     = envOr("OIDC_USER", "admin@example.com")
		pass     = envOr("OIDC_PASS", "password")
		timeoutS = envOr("HTTP_TIMEOUT", "15s")
	)
	flag.StringVar(&webui, "webui", webui, "weft-webui base URL (user listener)")
	flag.StringVar(&dex, "dex", dex, "Dex issuer URL")
	flag.StringVar(&user, "user", user, "mock-connector username")
	flag.StringVar(&pass, "pass", pass, "mock-connector password")
	flag.StringVar(&timeoutS, "timeout", timeoutS, "per-request HTTP timeout (Go duration)")
	flag.Parse()

	timeout, err := time.ParseDuration(timeoutS)
	if err != nil {
		fatal("config", fmt.Errorf("bad HTTP_TIMEOUT %q: %w", timeoutS, err))
	}

	step(1, "webui=%s dex=%s user=%s timeout=%s", webui, dex, user, timeout)

	jar, err := cookiejar.New(nil)
	if err != nil {
		fatal("cookiejar", err)
	}
	// We follow redirects manually so each hop is visible in the log
	// (and so we can stop just before a cross-origin auto-follow into a
	// browser-only Dex page). The jar still threads cookies for us.
	client := &http.Client{
		Timeout: timeout,
		Jar:     jar,
		CheckRedirect: func(req *http.Request, via []*http.Request) error {
			return http.ErrUseLastResponse
		},
	}

	ctx, cancel := context.WithTimeout(context.Background(), timeout*6)
	defer cancel()

	// --- step 1 : kick off the flow at the webui ---------------------
	step(1, "GET %s/api/auth/login", webui)
	authzURL := getRedirect(ctx, client, webui+"/api/auth/login", "login")
	if !strings.HasPrefix(authzURL, dex) {
		fatal("login", fmt.Errorf("redirect %q does not point at DEX_ISSUER %q", authzURL, dex))
	}
	fmt.Fprintf(os.Stdout, "  → authorize URL: %s\n", authzURL)

	// --- step 2 : reach Dex's authorize endpoint --------------------
	// Dex typically responds with a 302 to /dex/auth/mock/login?req=...
	// when only one connector is configured ; with multiple connectors
	// it returns a connector-picker HTML page. We handle both : follow
	// any same-origin redirect chain until we land on something that
	// looks like a login form (status 200 + an <input name="login"> or
	// name="password">).
	step(2, "GET %s (follow Dex redirect chain)", authzURL)
	loginFormURL, loginFormHTML := walkToLoginForm(ctx, client, authzURL, dex)
	fmt.Fprintf(os.Stdout, "  → login form at: %s\n", loginFormURL)

	// --- step 3 : POST the mock-connector credentials ---------------
	step(3, "POST credentials to %s", loginFormURL)
	form := url.Values{}
	// Dex's mock + password connectors accept either {login,password}
	// (mock) or {login,password,connector_id} (password DB) ; sending
	// both keys is harmless and lets one script cover both setups.
	form.Set("login", user)
	form.Set("password", pass)
	// Carry over any hidden fields the form rendered (Dex has none on
	// the mock connector today, but a future Dex version might add a
	// CSRF token, so cover that ahead of time).
	for k, v := range extractHidden(loginFormHTML) {
		if form.Get(k) == "" {
			form.Set(k, v)
		}
	}
	callbackURL := postFormFollow(ctx, client, loginFormURL, form, webui+"/api/auth/callback")
	fmt.Fprintf(os.Stdout, "  → callback URL: %s\n", callbackURL)

	// --- step 4 : hit the webui callback ----------------------------
	step(4, "GET %s (token exchange + session cookie)", callbackURL)
	finalLoc := getRedirect(ctx, client, callbackURL, "callback")
	fmt.Fprintf(os.Stdout, "  → callback set session, redirected to: %s\n", finalLoc)

	// --- step 5 : verify /api/me returns the user we logged in as ---
	meURL := webui + "/api/me"
	step(5, "GET %s with session cookie", meURL)
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, meURL, nil)
	if err != nil {
		fatal("me", err)
	}
	req.Header.Set("Accept", "application/json")
	resp, err := client.Do(req)
	if err != nil {
		fatal("me", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != http.StatusOK {
		fatal("me", fmt.Errorf("status %d, body: %s", resp.StatusCode, truncate(body, 400)))
	}
	var me struct {
		Sub   string `json:"sub"`
		Email string `json:"email"`
		Name  string `json:"name"`
	}
	if err := json.Unmarshal(body, &me); err != nil {
		fatal("me", fmt.Errorf("decode %s: %w", truncate(body, 200), err))
	}
	if me.Email == "" {
		fatal("me", fmt.Errorf("response missing email field: %s", truncate(body, 200)))
	}
	fmt.Fprintf(os.Stdout, "  → /api/me: sub=%s email=%s name=%s\n", me.Sub, me.Email, me.Name)
	fmt.Fprintf(os.Stdout, "\nOK — end-to-end OIDC login succeeded.\n")
}

// --- helpers --------------------------------------------------------

// step prints a numbered progress line to stdout.
func step(n int, format string, a ...any) {
	fmt.Fprintf(os.Stdout, "[step %d] "+format+"\n", append([]any{n}, a...)...)
}

// fatal prints a stage-tagged error to stderr and exits non-zero. The
// stage tag is the same identifier the docs reference in the troubleshoot
// table, so an operator can grep straight to the right row.
func fatal(stage string, err error) {
	fmt.Fprintf(os.Stderr, "FAIL [%s]: %v\n", stage, err)
	os.Exit(1)
}

func envOr(k, def string) string {
	if v := os.Getenv(k); v != "" {
		return v
	}
	return def
}

func truncate(b []byte, n int) string {
	if len(b) <= n {
		return string(b)
	}
	return string(b[:n]) + "…"
}

// getRedirect issues GET and expects a 3xx with a Location header.
// Returns the absolute Location ; fatals otherwise.
func getRedirect(ctx context.Context, c *http.Client, target, stage string) string {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		fatal(stage, err)
	}
	resp, err := c.Do(req)
	if err != nil {
		fatal(stage, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 300 || resp.StatusCode >= 400 {
		body, _ := io.ReadAll(resp.Body)
		fatal(stage, fmt.Errorf("expected 3xx, got %d ; body: %s", resp.StatusCode, truncate(body, 400)))
	}
	loc, err := resp.Location()
	if err != nil {
		fatal(stage, fmt.Errorf("no Location header: %w", err))
	}
	return loc.String()
}

// walkToLoginForm follows Dex's same-origin redirects until it lands on
// a 200 OK response whose body looks like a login form. Returns the
// final URL (where the form should be POSTed — usually the same URL)
// and the rendered HTML so the caller can pluck hidden inputs.
func walkToLoginForm(ctx context.Context, c *http.Client, start, dexOrigin string) (string, string) {
	cur := start
	for hop := 0; hop < 6; hop++ {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, cur, nil)
		if err != nil {
			fatal("dex", err)
		}
		resp, err := c.Do(req)
		if err != nil {
			fatal("dex", err)
		}
		body, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		switch {
		case resp.StatusCode >= 300 && resp.StatusCode < 400:
			loc, lerr := resp.Location()
			if lerr != nil {
				fatal("dex", fmt.Errorf("redirect with no Location: %w", lerr))
			}
			next := loc.String()
			if !strings.HasPrefix(next, dexOrigin) && !strings.HasPrefix(next, "/") {
				fatal("dex", fmt.Errorf("redirect %q escapes Dex origin %q", next, dexOrigin))
			}
			cur = loc.String()
		case resp.StatusCode == http.StatusOK:
			html := string(body)
			if looksLikeLoginForm(html) {
				// Resolve the form action against cur ; bare action="" means same URL.
				action := extractFormAction(html)
				if action == "" {
					return cur, html
				}
				base, _ := url.Parse(cur)
				ref, perr := url.Parse(action)
				if perr != nil {
					return cur, html
				}
				return base.ResolveReference(ref).String(), html
			}
			fatal("dex", fmt.Errorf("Dex returned 200 but no login form ; first 400 bytes:\n%s", truncate(body, 400)))
		default:
			fatal("dex", fmt.Errorf("Dex returned %d ; body: %s", resp.StatusCode, truncate(body, 400)))
		}
	}
	fatal("dex", fmt.Errorf("too many redirects starting at %s", start))
	return "", ""
}

// postFormFollow POSTs the form and follows redirects until it lands on
// a URL beginning with wantPrefix (the webui callback). Returns that URL.
func postFormFollow(ctx context.Context, c *http.Client, target string, form url.Values, wantPrefix string) string {
	cur := target
	body := strings.NewReader(form.Encode())
	method := http.MethodPost
	for hop := 0; hop < 8; hop++ {
		req, err := http.NewRequestWithContext(ctx, method, cur, body)
		if err != nil {
			fatal("post", err)
		}
		if method == http.MethodPost {
			req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
		}
		resp, err := c.Do(req)
		if err != nil {
			fatal("post", err)
		}
		respBody, _ := io.ReadAll(resp.Body)
		resp.Body.Close()

		if resp.StatusCode >= 300 && resp.StatusCode < 400 {
			loc, lerr := resp.Location()
			if lerr != nil {
				fatal("post", fmt.Errorf("redirect with no Location: %w", lerr))
			}
			next := loc.String()
			if strings.HasPrefix(next, wantPrefix) {
				return next
			}
			cur = next
			method = http.MethodGet
			body = nil
			continue
		}
		if resp.StatusCode == http.StatusOK {
			// Some Dex versions render an "approval" page after auth.
			// Re-POST any form we find on it.
			html := string(respBody)
			if looksLikeLoginForm(html) {
				// We submitted bad creds — Dex re-rendered the form.
				fatal("post", fmt.Errorf("Dex re-rendered the login form (likely wrong OIDC_USER/OIDC_PASS) ; first 400 bytes:\n%s", truncate(respBody, 400)))
			}
			fatal("post", fmt.Errorf("Dex returned 200 with no redirect — unexpected page ; first 400 bytes:\n%s", truncate(respBody, 400)))
		}
		fatal("post", fmt.Errorf("Dex returned %d ; body: %s", resp.StatusCode, truncate(respBody, 400)))
	}
	fatal("post", fmt.Errorf("too many redirects after POST"))
	return ""
}

// looksLikeLoginForm returns true when the body contains BOTH a login
// and a password input — defensive against Dex skinning changes.
var (
	reLoginInput    = regexp.MustCompile(`(?i)<input[^>]+name=["']?login["']?`)
	rePasswordInput = regexp.MustCompile(`(?i)<input[^>]+name=["']?password["']?`)
	reFormAction    = regexp.MustCompile(`(?is)<form[^>]+action=["']([^"']+)["']`)
	reHiddenInput   = regexp.MustCompile(`(?is)<input[^>]+type=["']?hidden["']?[^>]*>`)
	reInputName     = regexp.MustCompile(`(?i)name=["']?([^"' >]+)["']?`)
	reInputValue    = regexp.MustCompile(`(?i)value=["']([^"']*)["']`)
)

func looksLikeLoginForm(html string) bool {
	return reLoginInput.MatchString(html) && rePasswordInput.MatchString(html)
}

func extractFormAction(html string) string {
	m := reFormAction.FindStringSubmatch(html)
	if len(m) < 2 {
		return ""
	}
	return m[1]
}

func extractHidden(html string) map[string]string {
	out := map[string]string{}
	for _, tag := range reHiddenInput.FindAllString(html, -1) {
		name := reInputName.FindStringSubmatch(tag)
		value := reInputValue.FindStringSubmatch(tag)
		if len(name) < 2 {
			continue
		}
		v := ""
		if len(value) >= 2 {
			v = value[1]
		}
		out[name[1]] = v
	}
	return out
}
