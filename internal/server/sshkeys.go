// sshkeys.go — per-VM SSH-key ASSIGNMENT store + resolver.
//
// The HTTP handlers moved to api_microvm_metadata.go (huma) ; this
// file owns the in-memory assignment map + the name→catalogue
// resolver used at read time.
//
// Operators define keys once in the catalogue (sshkeys_catalogue.go)
// and assign them to VMs BY NAME from the drawer. The wire to the
// in-guest weft-microvm-agent is unchanged : the host resolves names to
// OpenSSH lines before publishing on weft.sshkeys.<vmID>, the guest
// still sees the same KeySet shape (openweft/weft-microvm-agent commit
// 032f346).
//
// Storage shape :
//
//	vmKeyAssignments map[vmName][]catalogueName
//	vmKeyAddedAt     map[vmName]map[catalogueName]RFC3339
//
// Read path : iterate the names, GET from catalogue, project to the
// existing VMSSHKey response shape (so the SPA's render code didn't
// have to change). A name that no longer resolves (catalogue entry
// deleted) is skipped silently — the VM's effective key set updates
// on the next host publish, no stale entries leak.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"sync"
	"time"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// VMSSHKey is the wire shape returned for one assigned key. Fields
// other than Name + AddedAt are resolved from the catalogue at read
// time ; the SPA already renders this shape.
type VMSSHKey struct {
	Name        string `json:"name"`        // catalogue name
	Fingerprint string `json:"fingerprint"` // resolved from catalogue
	Type        string `json:"type"`        // resolved
	PublicKey   string `json:"public_key"`  // resolved
	Comment     string `json:"comment"`     // resolved (description fallback)
	AddedAt     string `json:"added_at"`    // when this VM was assigned the key
}

var (
	vmKeysMu         sync.Mutex
	vmKeyAssignments = seedVMKeyAssignments()
	vmKeyAddedAt     = map[string]map[string]string{} // vmName -> {catName -> RFC3339}
)

// seedVMKeyAssignments — web-1 references "alice-laptop" so the demo
// drawer is non-empty. The reference is by name ; the actual key
// comes from the catalogue (which seeds alice-laptop too — see
// sshkeys_catalogue.go).
func seedVMKeyAssignments() map[string][]string {
	return map[string][]string{
		"web-1": {"alice-laptop"},
	}
}

// generateDemoEd25519 returns a freshly-minted "ssh-ed25519 <b64>
// <comment>" line. Reused by the catalogue's seed (sshkeys_catalogue.go).
// The blob inside follows OpenSSH's length-prefixed wire format so
// any parser (the catalogue's parseSSHLine, or a real sshd) sees
// realistic input.
func generateDemoEd25519(comment string) string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return ""
	}
	const algo = "ssh-ed25519"
	buf := make([]byte, 0, 4+len(algo)+4+len(pub))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(algo)))
	buf = append(buf, algo...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(pub)))
	buf = append(buf, pub...)
	return algo + " " + base64.StdEncoding.EncodeToString(buf) + " " + comment
}

// resolveVMKey looks up one catalogue entry and projects it to the
// VMSSHKey wire shape. ok=false when the name doesn't resolve
// (catalogue entry deleted) — the read handler skips silently.
func resolveVMKey(ctx interface{}, name, addedAt string) (VMSSHKey, bool) {
	_ = ctx
	// Use a background context — the catalogue's mem impl doesn't
	// require a real one, and the alternative (passing r.Context()
	// from the handler) would require touching every call site.
	k, ok := sshKeysCatalogue.Get(context.Background(), name)
	if !ok {
		return VMSSHKey{}, false
	}
	// Tease the type out of the line for the response (the catalogue
	// doesn't store it broken out, but parseSSHLine recomputes).
	typ, comment, _, _ := parseSSHLine(k.PublicKey)
	return VMSSHKey{
		Name:        k.Name,
		Fingerprint: k.Fingerprint,
		Type:        typ,
		PublicKey:   k.PublicKey,
		Comment:     comment,
		AddedAt:     addedAt,
	}, true
}
