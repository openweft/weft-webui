// sshkeys.go — per-VM SSH key store + endpoints.
//
// The microVM model is closer to Docker than to a long-lived VM : a
// boot is fast, cloud-init isn't always wired, and users expect to
// push secrets at runtime rather than baking them into a create-time
// SSHPub blob. This file is the dashboard's surface for that flow ;
// the actual key application happens inside the guest's weft-vm-agent,
// which subscribes to a NATS subject and writes authorized_keys
// idempotently (same Subscriber+ApplyFunc pattern as the mesh /
// mounts concerns).
//
// In live mode the dashboard will forward through a gRPC RPC ; until
// that lands, the store here is in-memory + per-VM and seeds a couple
// of demo keys so the drawer's "SSH keys" tab isn't empty.
package server

import (
	"crypto/ed25519"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"net/http"
	"strings"
	"sync"
	"time"
)

// VMSSHKey is the wire shape of one authorized key. Fingerprint is the
// SHA256:<b64> form that ssh-keygen -l emits ; we use it as the stable
// key for DELETE so the operator doesn't have to URL-encode a 400-byte
// base64 blob.
type VMSSHKey struct {
	Fingerprint string `json:"fingerprint"`
	Type        string `json:"type"`        // "ssh-rsa" | "ssh-ed25519" | …
	PublicKey   string `json:"public_key"`  // full "type b64 comment" line
	Comment     string `json:"comment"`     // free-form trailing comment
	AddedAt     string `json:"added_at"`    // RFC3339
}

var (
	vmKeysMu sync.Mutex
	vmKeys   = seedVMKeys()
)

// seedVMKeys — one key on web-1 so the demo drawer shows the UI in
// its non-empty state. The key pair is generated fresh on every server
// start ; nobody holds the private half so it's harmless if it ever
// reached a real authorized_keys file. Using a real key (vs a hand-
// crafted base64 blob) means parseSSHKey runs the same code path the
// add-handler does, and the seed exercises the round-trip on startup.
func seedVMKeys() map[string][]VMSSHKey {
	line := generateDemoEd25519("alice@laptop")
	parsed, ok := parseSSHKey(line)
	if !ok {
		// parseSSHKey would only refuse this if generateDemoEd25519
		// produced something malformed — a code bug, not a runtime
		// condition. Bail to an empty seed rather than panicking.
		return map[string][]VMSSHKey{}
	}
	parsed.AddedAt = "2026-05-20T09:14:00Z"
	return map[string][]VMSSHKey{
		"web-1": {parsed},
	}
}

// generateDemoEd25519 returns a freshly-minted "ssh-ed25519 <b64> <comment>"
// line. The blob inside follows OpenSSH's length-prefixed wire format :
//
//	uint32 len("ssh-ed25519") || "ssh-ed25519" ||
//	uint32 32                  || <32-byte public key>
//
// — exactly what ssh-keygen would write for a real ed25519 entry, so
// the parse + fingerprint paths see realistic input.
func generateDemoEd25519(comment string) string {
	pub, _, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		return "" // parseSSHKey will reject ; caller falls back to empty seed
	}
	const algo = "ssh-ed25519"
	buf := make([]byte, 0, 4+len(algo)+4+len(pub))
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(algo)))
	buf = append(buf, algo...)
	buf = binary.BigEndian.AppendUint32(buf, uint32(len(pub)))
	buf = append(buf, pub...)
	return algo + " " + base64.StdEncoding.EncodeToString(buf) + " " + comment
}

// parseSSHKey validates "type base64 [comment]" and computes the
// SHA256 fingerprint over the decoded blob. Returns ok=false when the
// shape is wrong or the base64 doesn't decode — the handler turns
// that into a 400 with a stable message.
func parseSSHKey(s string) (k VMSSHKey, ok bool) {
	s = strings.TrimSpace(s)
	if s == "" {
		return VMSSHKey{}, false
	}
	parts := strings.Fields(s)
	if len(parts) < 2 {
		return VMSSHKey{}, false
	}
	kind, b64 := parts[0], parts[1]
	switch kind {
	case "ssh-rsa", "ssh-ed25519", "ssh-dss", "ecdsa-sha2-nistp256",
		"ecdsa-sha2-nistp384", "ecdsa-sha2-nistp521":
	default:
		return VMSSHKey{}, false
	}
	raw, err := base64.StdEncoding.DecodeString(b64)
	if err != nil {
		return VMSSHKey{}, false
	}
	sum := sha256.Sum256(raw)
	fp := "SHA256:" + strings.TrimRight(base64.StdEncoding.EncodeToString(sum[:]), "=")
	comment := ""
	if len(parts) > 2 {
		comment = strings.Join(parts[2:], " ")
	}
	return VMSSHKey{
		Fingerprint: fp,
		Type:        kind,
		PublicKey:   s,
		Comment:     comment,
	}, true
}

// handleListVMKeys — GET /api/microvms/{name}/keys
func handleListVMKeys(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	keys := vmKeys[name]
	if keys == nil {
		keys = []VMSSHKey{}
	}
	writeJSON(w, http.StatusOK, keys)
}

// handleAddVMKey — POST /api/microvms/{name}/keys  {"public_key": "..."}.
// Rejects duplicates by fingerprint with 409 so a retried request is
// idempotent (the second call sees the existing key and returns 200
// instead of a confusing "added twice" state). Same-fingerprint with a
// different comment counts as duplicate.
func handleAddVMKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	var body struct {
		PublicKey string `json:"public_key"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeJSON(w, http.StatusBadRequest, map[string]string{"error": "invalid body"})
		return
	}
	parsed, ok := parseSSHKey(body.PublicKey)
	if !ok {
		writeJSON(w, http.StatusBadRequest, map[string]string{
			"error": "expected ssh-keygen format : <type> <base64> [comment]",
		})
		return
	}
	parsed.AddedAt = time.Now().UTC().Format(time.RFC3339)

	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	for _, k := range vmKeys[name] {
		if k.Fingerprint == parsed.Fingerprint {
			writeJSON(w, http.StatusOK, k) // idempotent re-add
			return
		}
	}
	vmKeys[name] = append(vmKeys[name], parsed)
	writeJSON(w, http.StatusCreated, parsed)
}

// handleRemoveVMKey — DELETE /api/microvms/{name}/keys/{fp}
// The fingerprint format is "SHA256:<b64>" ; the SPA URL-encodes it.
func handleRemoveVMKey(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	fp := r.PathValue("fp")
	vmKeysMu.Lock()
	defer vmKeysMu.Unlock()
	keys := vmKeys[name]
	for i, k := range keys {
		if k.Fingerprint == fp {
			vmKeys[name] = append(keys[:i], keys[i+1:]...)
			writeJSON(w, http.StatusOK, map[string]any{"removed": fp})
			return
		}
	}
	writeJSON(w, http.StatusNotFound, map[string]string{"error": "no such key"})
}
