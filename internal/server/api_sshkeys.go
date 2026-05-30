// api_sshkeys.go — typed SSH-key catalogue endpoints + import.
// Visible on both listeners (every user pushes their own keys) ;
// the write surface is server-side gated to tenant-admin (or
// cluster-admin) inside requireSSHKeyWriter, so non-admins see the
// same 403 the SPA would never surface.

package server

import (
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

// APISSHKey is the typed wire shape exposed by /api/ssh-keys/*. The
// fingerprint is computed server-side from PublicKey — the client
// can't pre-fill it.
type APISSHKey struct {
	Name          string `json:"name" doc:"Operator-chosen unique name" example:"alice@laptop" minLength:"1" maxLength:"128"`
	PublicKey     string `json:"public_key" doc:"OpenSSH-format line : '<type> <b64> [comment]'" example:"ssh-ed25519 AAAA… alice@laptop" minLength:"1"`
	Description   string `json:"description" doc:"Short description ; falls back to the OpenSSH comment when empty" example:"alice's laptop"`
	Source        string `json:"source" doc:"Provenance" example:"manual" enum:"manual,github,gitlab,forgejo"`
	SourceAccount string `json:"source_account" doc:"Upstream login when imported" example:"alice"`
	Fingerprint   string `json:"fingerprint" doc:"SHA256:<base64-no-pad>, server-computed" example:"SHA256:abc…" readOnly:"true"`
	UpdatedAt     string `json:"updated_at" doc:"RFC3339, server-stamped" readOnly:"true"`
	UpdatedBy     string `json:"updated_by" doc:"OIDC sub / email of the last editor" readOnly:"true"`
}

func toAPISSHKey(k SSHKey) APISSHKey {
	return APISSHKey{
		Name: k.Name, PublicKey: k.PublicKey, Description: k.Description,
		Source: k.Source, SourceAccount: k.SourceAccount,
		Fingerprint: k.Fingerprint, UpdatedAt: k.UpdatedAt, UpdatedBy: k.UpdatedBy,
	}
}

func fromAPISSHKey(k APISSHKey) SSHKey {
	return SSHKey{
		Name: k.Name, PublicKey: k.PublicKey, Description: k.Description,
		Source: k.Source, SourceAccount: k.SourceAccount,
		Fingerprint: k.Fingerprint, UpdatedAt: k.UpdatedAt, UpdatedBy: k.UpdatedBy,
	}
}

func mountSSHKeysCatalogueAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-ssh-keys",
		Method:      "GET",
		Path:        "/api/ssh-keys",
		Summary:     "List the SSH-key catalogue",
		Description: "Cluster-wide named SSH keys. Operators push their own keys here ; per-VM key assignments reference these by name.",
		Tags:        []string{"ssh-keys"},
	}, func(ctx context.Context, _ *struct{}) (*listSSHKeysOutput, error) {
		ks, err := sshKeysCatalogue.List(ctx)
		if err != nil {
			return nil, huma.Error500InternalServerError("list ssh-keys", err)
		}
		out := &listSSHKeysOutput{}
		out.Body = make([]APISSHKey, 0, len(ks))
		for _, k := range ks {
			out.Body = append(out.Body, toAPISSHKey(k))
		}
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-ssh-key",
		Method:      "GET",
		Path:        "/api/ssh-keys/{name}",
		Summary:     "Get one SSH key by name",
		Tags:        []string{"ssh-keys"},
	}, func(ctx context.Context, in *sshKeyNameInput) (*getSSHKeyOutput, error) {
		k, ok := sshKeysCatalogue.Get(ctx, in.Name)
		if !ok {
			return nil, huma.Error404NotFound("no such key: " + in.Name)
		}
		return &getSSHKeyOutput{Body: toAPISSHKey(k)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-ssh-key",
		Method:      "POST",
		Path:        "/api/ssh-keys",
		Summary:     "Create or update an SSH key (tenant-admin or cluster-admin)",
		Description: "PublicKey is parsed server-side ; the algorithm whitelist is closed (ssh-ed25519, ssh-rsa, ssh-dss, ecdsa-sha2-nistp{256,384,521}). The fingerprint is computed from the decoded blob and overwrites whatever the client sent.",
		Tags:        []string{"ssh-keys"},
	}, func(ctx context.Context, in *setSSHKeyInput) (*setSSHKeyOutput, error) {
		u := auth.UserFromContext(ctx)
		if err := requireSSHKeyWriterCtx(u); err != nil {
			return nil, err
		}
		body := fromAPISSHKey(in.Body)
		body.Name = strings.TrimSpace(body.Name)
		if body.Name == "" {
			return nil, huma.Error400BadRequest("name is required")
		}
		_, comment, fp, ok := parseSSHLine(body.PublicKey)
		if !ok {
			return nil, huma.Error400BadRequest("public_key must be '<type> <base64> [comment]' with a known algorithm")
		}
		if body.Description == "" {
			body.Description = comment
		}
		if body.Source == "" {
			body.Source = "manual"
		}
		body.Fingerprint = fp
		body.UpdatedAt = time.Now().UTC().Format(time.RFC3339)
		if u != nil {
			body.UpdatedBy = u.Email
			if body.UpdatedBy == "" {
				body.UpdatedBy = u.Subject
			}
		}
		if err := sshKeysCatalogue.Set(ctx, body); err != nil {
			return nil, huma.Error500InternalServerError("set ssh-key", err)
		}
		return &setSSHKeyOutput{Body: toAPISSHKey(body)}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "delete-ssh-key",
		Method:      "DELETE",
		Path:        "/api/ssh-keys/{name}",
		Summary:     "Delete an SSH key (tenant-admin or cluster-admin) — idempotent",
		Tags:        []string{"ssh-keys"},
	}, func(ctx context.Context, in *sshKeyNameInput) (*deleteSSHKeyOutput, error) {
		if err := requireSSHKeyWriterCtx(auth.UserFromContext(ctx)); err != nil {
			return nil, err
		}
		if err := sshKeysCatalogue.Delete(ctx, in.Name); err != nil {
			return nil, huma.Error500InternalServerError("delete ssh-key", err)
		}
		out := &deleteSSHKeyOutput{}
		out.Body.Deleted = in.Name
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "import-ssh-keys",
		Method:      "POST",
		Path:        "/api/ssh-keys/import",
		Summary:     "Bulk-import an upstream account's SSH keys (tenant-admin or cluster-admin)",
		Description: "Hits the provider's .keys endpoint (github/gitlab/forgejo) and stores every key not already present (dedup by fingerprint). Returns a summary of added/skipped counts.",
		Tags:        []string{"ssh-keys"},
	}, func(ctx context.Context, in *importSSHKeysInput) (*importSSHKeysOutput, error) {
		u := auth.UserFromContext(ctx)
		if err := requireSSHKeyWriterCtx(u); err != nil {
			return nil, err
		}
		body := importBody{
			Provider:    in.Body.Provider,
			Account:     strings.TrimSpace(in.Body.Account),
			ForgejoBase: in.Body.ForgejoBase,
		}
		if body.Account == "" {
			return nil, huma.Error400BadRequest("account is required")
		}
		endpoint, err := importEndpoint(body)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		lines, err := fetchKeysFile(ctx, endpoint)
		if err != nil {
			return nil, huma.Error502BadGateway("fetch " + endpoint + ": " + err.Error())
		}

		existing, _ := sshKeysCatalogue.List(ctx)
		byFp := map[string]SSHKey{}
		for _, k := range existing {
			if k.Fingerprint != "" {
				byFp[k.Fingerprint] = k
			}
		}

		now := time.Now().UTC().Format(time.RFC3339)
		editor := ""
		if u != nil {
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
			if err := sshKeysCatalogue.Set(ctx, entry); err != nil {
				return nil, huma.Error500InternalServerError("set "+name, err)
			}
			res.Added++
			res.Names = append(res.Names, name)
			byFp[fp] = entry
		}
		return &importSSHKeysOutput{Body: res}, nil
	})
}

// requireSSHKeyWriterCtx is the huma-flavored gate (returns a huma
// error instead of writing the 403 itself). Mirrors requireSSHKeyWriter
// from sshkeys_catalogue.go ; kept side-by-side until the legacy paths
// are gone.
func requireSSHKeyWriterCtx(u *auth.User) error {
	if u == nil {
		return huma.Error403Forbidden("ssh-keys writes require an authenticated user")
	}
	if isClusterAdmin(u) || tenantsDB.isAnyTenantAdmin(u.Email) {
		return nil
	}
	return huma.Error403Forbidden("ssh-keys writes are restricted to tenant admins (or cluster admins) ; ask yours to add the key, or import via your own tenant's account.")
}

type listSSHKeysOutput struct {
	Body []APISSHKey
}

type sshKeyNameInput struct {
	Name string `path:"name" doc:"SSH-key name" minLength:"1" maxLength:"128"`
}

type getSSHKeyOutput struct {
	Body APISSHKey
}

type setSSHKeyInput struct {
	Body APISSHKey
}

type setSSHKeyOutput struct {
	Body APISSHKey
}

type deleteSSHKeyOutput struct {
	Body struct {
		Deleted string `json:"deleted"`
	}
}

type importSSHKeysInput struct {
	Body struct {
		Provider    string `json:"provider" doc:"Upstream provider" example:"github" enum:"github,gitlab,forgejo"`
		Account     string `json:"account" doc:"Upstream login" example:"alice" minLength:"1"`
		ForgejoBase string `json:"forgejo_base,omitempty" doc:"Required when provider == forgejo (https://codeberg.org)"`
	}
}

type importSSHKeysOutput struct {
	Body importResult
}
