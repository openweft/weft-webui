// sshkeys_catalogue.go — named SSH-key catalogue. Operators define
// keys ONCE here (or import them from GitHub / GitLab / Forgejo) and
// then attribute them to VMs by name from the drawer. Same shape +
// migration path as the flavors / scripts catalogues : in-memory mock
// today behind a sshKeyCatalogue interface, etcd-backed by weft-agent
// later.
//
// Wire to the guest is unchanged : when the host publishes a VM's
// SSH-key set on weft.sshkeys.<vmID>, the catalogue names are
// resolved to OpenSSH lines first. The in-guest weft-microvm-agent never
// sees a name — same `KeySet { Keys [{public_key: ...}] }` shape it
// already speaks (see openweft/weft-microvm-agent commit 032f346).
package server

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"strings"
	"sync"
)

// SSHKey is one named entry in the catalogue. Fingerprint is
// computed server-side from PublicKey on every Set ; the operator
// never types it.
//
// Source tracks provenance : "manual" for direct entry, "github" /
// "gitlab" / "forgejo" for imported keys. SourceAccount is the
// upstream username when imported. Useful both for audit and for
// the refresh flow (re-fetch + reconcile when an account changes
// keys upstream).
type SSHKey struct {
	// UUID is the opaque handle proto v0.9.0 keys on
	// (RemoveSSHKeyCatalogue takes a UUID). The mock continues to
	// look entries up by name ; the live-first paths resolve
	// name → uuid before dialling the wclient. Server-side
	// fingerprint is authoritative when the live RPC succeeds —
	// the mock recomputes it client-side for the same field.
	UUID          string `json:"uuid,omitempty"`
	Name          string `json:"name"`
	PublicKey     string `json:"public_key"`
	Description   string `json:"description"`
	Source        string `json:"source"`              // "manual" | "github" | "gitlab" | "forgejo"
	SourceAccount string `json:"source_account"`      // upstream login, when imported
	Fingerprint   string `json:"fingerprint"`         // "SHA256:<b64>"
	Owner         string `json:"owner,omitempty"`     // email of the user who owns the key (drives group-based authz)
	UpdatedAt     string `json:"updated_at"`
	UpdatedBy     string `json:"updated_by"`
}

// sshKeyCatalogue is the contract every consumer goes through.
type sshKeyCatalogue interface {
	List(ctx context.Context) ([]SSHKey, error)
	Get(ctx context.Context, name string) (SSHKey, bool)
	Set(ctx context.Context, k SSHKey) error
	Delete(ctx context.Context, name string) error
}

type memSSHKeyCatalogue struct {
	mu   sync.Mutex
	keys []SSHKey
}

func newMemSSHKeyCatalogue() *memSSHKeyCatalogue {
	return &memSSHKeyCatalogue{keys: seedSSHKeys()}
}

// seedSSHKeys mints two demo entries with real ed25519 key pairs.
// Reuses generateDemoEd25519 from sshkeys.go so the seed exercises
// the same path operator-set keys go through (parse + fingerprint).
// Private halves are discarded ; nobody holds them, so even if one
// reached a real authorized_keys file it'd be harmless.
func seedSSHKeys() []SSHKey {
	now := "2026-05-20T12:00:00Z"
	out := []SSHKey{}
	demo := []struct{ name, comment, descr, owner string }{
		{"alice-laptop", "alice@laptop", "Alice's laptop key — primary admin access", "alice@weft.local"},
		{"ci-deploy", "ci@deploy", "CI deploy key — used by the deploy pipeline only", ""},
		{"bob-laptop", "bob@laptop", "Bob's laptop key", "bob@weft.local"},
	}
	for _, d := range demo {
		line := generateDemoEd25519(d.comment)
		_, _, fp, ok := parseSSHLine(line)
		if !ok {
			continue // skip silently — bad generation means an empty catalogue, not a panic
		}
		out = append(out, SSHKey{
			UUID: mockUUID("ssh-key", d.name),
			Name: d.name, PublicKey: line, Description: d.descr,
			Source: "manual", Fingerprint: fp,
			Owner:     d.owner,
			UpdatedAt: now, UpdatedBy: "dev@weft.local",
		})
	}
	return out
}

// fingerprintForLine computes "SHA256:<b64>" over the decoded blob,
// matching ssh-keygen -l. Returns "" on a malformed input — the
// caller decides whether that's a 400 or a stored placeholder.
func fingerprintForLine(line string) string {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 {
		return ""
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(raw)
	return "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
}

// validSSHKeyTypes — same closed set as the guest-side validator
// (openweft/weft-microvm-agent pkg/sshkeys/sshkeys.go).
var validSSHKeyTypes = map[string]bool{
	"ssh-rsa": true, "ssh-ed25519": true, "ssh-dss": true,
	"ecdsa-sha2-nistp256": true, "ecdsa-sha2-nistp384": true,
	"ecdsa-sha2-nistp521": true,
}

func parseSSHLine(line string) (typ, comment, fingerprint string, ok bool) {
	parts := strings.Fields(strings.TrimSpace(line))
	if len(parts) < 2 {
		return "", "", "", false
	}
	if !validSSHKeyTypes[parts[0]] {
		return "", "", "", false
	}
	raw, err := base64.StdEncoding.DecodeString(parts[1])
	if err != nil {
		return "", "", "", false
	}
	sum := sha256.Sum256(raw)
	fp := "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
	c := ""
	if len(parts) > 2 {
		c = strings.Join(parts[2:], " ")
	}
	return parts[0], c, fp, true
}

func (m *memSSHKeyCatalogue) List(ctx context.Context) ([]SSHKey, error) {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]SSHKey, len(m.keys))
	copy(out, m.keys)
	return out, nil
}

func (m *memSSHKeyCatalogue) Get(ctx context.Context, name string) (SSHKey, bool) {
	m.mu.Lock()
	defer m.mu.Unlock()
	for _, k := range m.keys {
		if k.Name == name {
			return k, true
		}
	}
	return SSHKey{}, false
}

func (m *memSSHKeyCatalogue) Set(ctx context.Context, k SSHKey) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, x := range m.keys {
		if x.Name == k.Name {
			// Preserve the existing UUID on update : the SPA
			// addresses keys by name but the wire keeps the UUID
			// stable across edits.
			if k.UUID == "" {
				k.UUID = x.UUID
			}
			m.keys[i] = k
			return nil
		}
	}
	if k.UUID == "" {
		k.UUID = mockUUID("ssh-key", k.Name)
	}
	m.keys = append(m.keys, k)
	return nil
}

func (m *memSSHKeyCatalogue) Delete(ctx context.Context, name string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	for i, k := range m.keys {
		if k.Name == name {
			m.keys = append(m.keys[:i], m.keys[i+1:]...)
			return nil
		}
	}
	return nil // idempotent
}

// sshKeysCatalogue is the process-wide singleton. Today the in-
// memory impl ; the live wrapper lands when weft-agent ships its
// own ListSSHKeys / SetSSHKey / DeleteSSHKey RPCs.
var sshKeysCatalogue sshKeyCatalogue = newMemSSHKeyCatalogue()

// sshKeyUUID resolves a catalogue entry's name to its opaque UUID
// handle. Used by live-first handlers that need the wclient's
// UUID-keyed Remove RPC while the SPA still addresses keys by name.
func sshKeyUUID(ctx context.Context, name string) (string, bool) {
	k, ok := sshKeysCatalogue.Get(ctx, name)
	if !ok {
		return "", false
	}
	return k.UUID, true
}

// sshKeyRows projects the catalogue to the map[string]any shape the
// generic /api/resources/{id} catch-all expects. Same indirection
// as flavorRows / scriptRows.
func sshKeyRows(ctx context.Context) []map[string]any {
	ks, err := sshKeysCatalogue.List(ctx)
	if err != nil || len(ks) == 0 {
		return []map[string]any{}
	}
	out := make([]map[string]any, 0, len(ks))
	for _, k := range ks {
		out = append(out, map[string]any{
			"uuid":           k.UUID,
			"name":           k.Name,
			"description":    k.Description,
			"fingerprint":    k.Fingerprint,
			"source":         k.Source,
			"source_account": k.SourceAccount,
			"updated_at":     k.UpdatedAt,
			"updated_by":     k.UpdatedBy,
		})
	}
	return out
}

// (Catalogue handlers moved to huma — see api_sshkeys.go. The
// write-gate logic survives as requireSSHKeyWriterCtx there.)

// ErrUnknownSSHKey is what assignVMKeys returns when a referenced
// name doesn't resolve. Exported because the per-VM assignment
// handler unwraps it for a clean 400 message.
var ErrUnknownSSHKey = errors.New("unknown ssh-key name")
