// sshkeys_import.go — bulk-import SSH keys from a forge account's
// public ".keys" endpoint.
//
//   GitHub  : https://github.com/<account>.keys
//   GitLab  : https://gitlab.com/<account>.keys
//   Forgejo : <base>/<account>.keys  (any Gitea / Forgejo instance ;
//             the base URL is operator-provided since there's no
//             central instance to default to)
//
// Each provider returns plain text, one OpenSSH-format line per
// public key. We dedupe by fingerprint against the existing
// catalogue ; entries that aren't already there land as new
// catalogue keys named "<provider>:<account>/<index>" with
// Source=<provider> + SourceAccount=<account> so a future refresh
// flow can find + replace them.
package server

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// importClient is the HTTP client used to hit the forge endpoints.
// Conservative timeout — these endpoints are typically fast (a few
// hundred bytes) but we don't want a hanging upstream to wedge the
// admin request. Capped redirects rule out a misconfigured forge
// from sending us into a redirect chain.
var importClient = &http.Client{
	Timeout: 8 * time.Second,
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) >= 3 {
			return http.ErrUseLastResponse
		}
		return nil
	},
}

// importBody is the JSON shape POSTed by the SPA's Import modal.
type importBody struct {
	Provider     string `json:"provider"`      // "github" | "gitlab" | "forgejo"
	Account      string `json:"account"`       // upstream login
	ForgejoBase  string `json:"forgejo_base"`  // required when provider == "forgejo"
}

// ImportResult is the wire shape returned to the SPA — small summary
// + the names of the newly-stored entries (so the SPA can highlight
// or scroll-to them). Exported so the huma op publishes it as a
// named OpenAPI schema instead of an inline object.
type ImportResult struct {
	Added           int      `json:"added"`
	SkippedExisting int      `json:"skipped_existing"`
	TotalSeen       int      `json:"total_seen"`
	Names           []string `json:"names"`
}

// (Import handler moved to huma — see api_sshkeys.go. importBody +
// importResult + importEndpoint + fetchKeysFile stay here because
// the huma handler reuses them verbatim.)

// importEndpoint maps a (provider, account, forgejo_base) tuple to
// the canonical .keys URL. The Forgejo flavour requires an explicit
// base because there's no central instance ; an operator-defaulted
// base could land here as a config field later if the same instance
// is hit constantly.
func importEndpoint(b importBody) (string, error) {
	switch b.Provider {
	case "github":
		return "https://github.com/" + url.PathEscape(b.Account) + ".keys", nil
	case "gitlab":
		return "https://gitlab.com/" + url.PathEscape(b.Account) + ".keys", nil
	case "forgejo":
		base := strings.TrimRight(strings.TrimSpace(b.ForgejoBase), "/")
		if base == "" {
			return "", fmt.Errorf("forgejo_base is required for forgejo (e.g. https://codeberg.org)")
		}
		if !strings.HasPrefix(base, "https://") && !strings.HasPrefix(base, "http://") {
			return "", fmt.Errorf("forgejo_base must be a full URL (with scheme)")
		}
		return base + "/" + url.PathEscape(b.Account) + ".keys", nil
	default:
		return "", fmt.Errorf("unknown provider %q (want github | gitlab | forgejo)", b.Provider)
	}
}

// fetchKeysFile pulls the .keys file and splits it into trimmed lines.
// Empty lines and comments (starting with #) are dropped.
func fetchKeysFile(ctx context.Context, endpoint string) ([]string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", "weft-webui/sshkeys-import")
	req.Header.Set("Accept", "text/plain")
	resp, err := importClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("upstream returned %d", resp.StatusCode)
	}
	// Cap the body so a misconfigured forge can't hose us with a
	// 100 MB response. Real .keys files are <10 KB.
	body, err := io.ReadAll(io.LimitReader(resp.Body, 256*1024))
	if err != nil {
		return nil, err
	}
	out := []string{}
	for _, line := range strings.Split(string(body), "\n") {
		t := strings.TrimSpace(line)
		if t == "" || strings.HasPrefix(t, "#") {
			continue
		}
		out = append(out, t)
	}
	return out, nil
}
