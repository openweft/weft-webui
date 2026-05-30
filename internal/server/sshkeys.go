// sshkeys.go — per-VM SSH-key ASSIGNMENT store + endpoints.
//
// Operators define keys once in the catalogue (sshkeys_catalogue.go)
// and assign them to VMs BY NAME from the drawer. This file owns the
// assignment map and the resolution at read time. The wire to the
// in-guest weft-vm-agent is unchanged : the host resolves names to
// OpenSSH lines before publishing on weft.sshkeys.<vmID>, the guest
// still sees the same KeySet shape (openweft/weft-vm-agent commit
// 032f346).
//
// Storage shape :
//
//	vmKeyAssignments map[vmName][]catalogueName
//
// Read path : iterate the names, GET from catalogue, project to the
// existing VMSSHKey response shape (so the SPA's render code didn't
// have to change). A name that no longer resolves (catalogue entry
// deleted) is skipped + a warning logged — VMs lose access on the
// next publish, which is the intended semantic.
package server

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"sync"
	"time"
)

func nowRFC3339() string { return time.Now().UTC().Format(time.RFC3339) }

// VMSSHKey is the wire shape returned for one assigned key. Fields
// other than Name are resolved from the catalogue at read time ; the
// SPA already renders this shape.
type VMSSHKey struct {
	Name        string `json:"name"`        // catalogue name (new)
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

// ---- handlers ----

// handleListVMKeys — GET /api/microvms/{name}/keys
// Returns the resolved assignment list. Names that no longer resolve
// (catalogue entry deleted) are silently skipped — the VM's effective
// key set updates on the next host publish, no stale entries leak.
func handleListVMKeys(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmKeysMu.Lock()
	names := append([]string(nil), vmKeyAssignments[name]...)
	addedMap := map[string]string{}
	for k, v := range vmKeyAddedAt[name] {
		addedMap[k] = v
	}
	vmKeysMu.Unlock()

	out := make([]VMSSHKey, 0, len(names))
	for _, cn := range names {
		k, ok := resolveVMKey(nil, cn, addedMap[cn])
		if !ok {
			continue
		}
		out = append(out, k)
	}
	writeJSON(w, http.StatusOK, out)
}

// handleAddVMKey — POST /api/microvms/{name}/keys  {"name": "alice-laptop"}.
// Assigns the named catalogue entry to this VM. Rejects unknown names
// with a 400 + the catalogue's name in the error so the operator can
// see the typo. Idempotent : re-assigning a name that's already on
// the VM is a no-op (200, no duplicate stamp).
func handleAddVMKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Name string `json:"name"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	if body.Name == "" {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "name is required (the catalogue entry to assign)",
		})
		return
	}
	if _, ok := sshKeysCatalogue.Get(r.Context(), body.Name); !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "no such key in catalogue : " + body.Name + " — create it on the SSH Keys page first",
		})
		return
	}

	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	for _, existing := range vmKeyAssignments[name] {
		if existing == body.Name {
			// idempotent : already assigned
			addedAt := ""
			if vmKeyAddedAt[name] != nil {
				addedAt = vmKeyAddedAt[name][body.Name]
			}
			k, _ := resolveVMKey(nil, body.Name, addedAt)
			writeJSON(w, http.StatusOK, k)
			return
		}
	}
	vmKeyAssignments[name] = append(vmKeyAssignments[name], body.Name)
	if vmKeyAddedAt[name] == nil {
		vmKeyAddedAt[name] = map[string]string{}
	}
	now := nowRFC3339()
	vmKeyAddedAt[name][body.Name] = now
	k, _ := resolveVMKey(nil, body.Name, now)
	writeJSON(w, http.StatusCreated, k)
}

// handleSetVMKeyAssignments — PUT /api/microvms/{name}/keys  {"names": [...]}
// Replace-set semantics. Unknown names cause the whole call to fail
// with the offender in the error message — partial saves would leave
// the VM in a state the operator didn't pick.
func handleSetVMKeyAssignments(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		Names []string `json:"names"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	for _, n := range body.Names {
		if _, ok := sshKeysCatalogue.Get(r.Context(), n); !ok {
			writeJSON(w, http.StatusBadRequest, map[string]string{
				"error": "no such key in catalogue : " + n,
			})
			return
		}
	}

	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	// Preserve AddedAt for names that were already assigned ; stamp
	// new ones with now.
	old := map[string]string{}
	if vmKeyAddedAt[name] != nil {
		old = vmKeyAddedAt[name]
	}
	fresh := map[string]string{}
	now := nowRFC3339()
	for _, n := range body.Names {
		if t, ok := old[n]; ok {
			fresh[n] = t
		} else {
			fresh[n] = now
		}
	}
	vmKeyAssignments[name] = append([]string(nil), body.Names...)
	vmKeyAddedAt[name] = fresh

	out := make([]VMSSHKey, 0, len(body.Names))
	for _, n := range body.Names {
		if k, ok := resolveVMKey(nil, n, fresh[n]); ok {
			out = append(out, k)
		}
	}
	writeJSON(w, http.StatusOK, out)
}

// handleRemoveVMKey — DELETE /api/microvms/{name}/keys/{key_name}.
// {key_name} is the catalogue name (not the fingerprint anymore). The
// underlying SPA helper takes care of URL-encoding for names that
// carry "/" (e.g. "gh:alice/0" from a GitHub import).
func handleRemoveVMKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	keyName := r.PathValue("key_name")
	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	assigned := vmKeyAssignments[name]
	for i, n := range assigned {
		if n == keyName {
			vmKeyAssignments[name] = append(assigned[:i], assigned[i+1:]...)
			if vmKeyAddedAt[name] != nil {
				delete(vmKeyAddedAt[name], keyName)
			}
			writeJSON(w, http.StatusOK, map[string]any{"removed": keyName})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such assignment"})
}
