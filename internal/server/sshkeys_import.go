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
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/openweft/weft-webui/internal/auth"
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

// importResult is the wire shape returned to the SPA — small summary
// + the names of the newly-stored entries (so the SPA can highlight
// or scroll-to them).
type importResult struct {
	Added           int      `json:"added"`
	SkippedExisting int      `json:"skipped_existing"`
	TotalSeen       int      `json:"total_seen"`
	Names           []string `json:"names"`
}

// handleImportSSHKeys — POST /api/ssh-keys/import. Admin-gated by
// route registration (only mounted on the admin port).
func handleImportSSHKeys(w http.ResponseWriter, r *http.Request) {
	var body importBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	body.Account = strings.TrimSpace(body.Account)
	if body.Account == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "account is required"})
		return
	}
	endpoint, err := importEndpoint(body)
	if err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": err.Error()})
		return
	}

	lines, err := fetchKeysFile(r.Context(), endpoint)
	if err != nil {
		writeJSON(w, http.StatusBadGateway, map[string]string{
			"error": "fetch " + endpoint + ": " + err.Error(),
		})
		return
	}

	// Build a fingerprint→entry index from the existing catalogue so
	// we can dedup against keys the operator already has under any
	// name (manual or from a previous import).
	existing, _ := sshKeysCatalogue.List(r.Context())
	byFp := map[string]SSHKey{}
	for _, k := range existing {
		if k.Fingerprint != "" {
			byFp[k.Fingerprint] = k
		}
	}

	now := time.Now().UTC().Format(time.RFC3339)
	editor := ""
	if u := auth.UserFromContext(r.Context()); u != nil {
		editor = u.Email
		if editor == "" {
			editor = u.Subject
		}
	}

	res := importResult{TotalSeen: len(lines)}
	for i, line := range lines {
		_, comment, fp, ok := parseSSHLine(line)
		if !ok {
			continue
		}
		if _, dup := byFp[fp]; dup {
			res.SkippedExisting++
			continue
		}
		name := fmt.Sprintf("%s:%s/%d", body.Provider, body.Account, i)
		descr := comment
		if descr == "" {
			descr = fmt.Sprintf("imported from %s/%s", body.Provider, body.Account)
		}
		entry := SSHKey{
			Name: name, PublicKey: line, Description: descr,
			Source: body.Provider, SourceAccount: body.Account,
			Fingerprint: fp, UpdatedAt: now, UpdatedBy: editor,
		}
		if err := sshKeysCatalogue.Set(r.Context(), entry); err != nil {
			writeJSON(w, http.StatusInternalServerError, map[string]string{
				"error": "set " + name + ": " + err.Error(),
			})
			return
		}
		res.Added++
		res.Names = append(res.Names, name)
		byFp[fp] = entry
	}
	writeJSON(w, http.StatusOK, res)
}

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
