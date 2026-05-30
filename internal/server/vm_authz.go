// vm_authz.go — per-VM "authorized groups" store + derived effective
// SSH keys.
//
// The legacy flow assigned individual SSH keys to a VM by name
// (vmKeyAssignments). The new flow authorizes one or more groups
// per VM ; the effective key set is then derived as :
//
//   for each authorized group g :
//     for each member of g (across every tenant) :
//       for each catalogue key with owner == member email :
//         include
//
// Both flows can coexist — the drawer surfaces both — and the
// effective key list is the union. weft-microvm-agent receives the
// resolved set on each push, so disabling a group or removing a
// member triggers a re-publish on the next reconcile.

package server

import "sync"

// AuthorizedGroup is one entry in the per-VM authz list. Tenant is
// carried so the resolver can look the group up in the correct
// tenant (group names are namespaced by tenant).
type AuthorizedGroup struct {
	Tenant string `json:"tenant" doc:"Tenant the group lives in" minLength:"1" maxLength:"128"`
	Group  string `json:"group"  doc:"Group name within the tenant"  minLength:"1" maxLength:"128"`
	AddedAt string `json:"added_at" doc:"RFC-3339, server-stamped" readOnly:"true"`
}

var (
	vmAuthzMu     sync.Mutex
	vmAuthorized  = seedVMAuthorizedGroups()
)

func seedVMAuthorizedGroups() map[string][]AuthorizedGroup {
	now := "2026-05-20T14:00:00Z"
	return map[string][]AuthorizedGroup{
		// web-1 is reachable by anyone in the team-alpha "admins" group.
		"web-1": {
			{Tenant: "acme", Group: "admins", AddedAt: now},
		},
	}
}

func listVMAuthorizedGroups(vmName string) []AuthorizedGroup {
	vmAuthzMu.Lock()
	defer vmAuthzMu.Unlock()
	out := make([]AuthorizedGroup, len(vmAuthorized[vmName]))
	copy(out, vmAuthorized[vmName])
	return out
}

// addVMAuthorizedGroup appends if not already present. Returns
// whether the entry was new.
func addVMAuthorizedGroup(vmName string, g AuthorizedGroup) bool {
	vmAuthzMu.Lock()
	defer vmAuthzMu.Unlock()
	for _, e := range vmAuthorized[vmName] {
		if e.Tenant == g.Tenant && e.Group == g.Group {
			return false
		}
	}
	vmAuthorized[vmName] = append(vmAuthorized[vmName], g)
	return true
}

func removeVMAuthorizedGroup(vmName, tenant, group string) bool {
	vmAuthzMu.Lock()
	defer vmAuthzMu.Unlock()
	list := vmAuthorized[vmName]
	for i, e := range list {
		if e.Tenant == tenant && e.Group == group {
			vmAuthorized[vmName] = append(list[:i], list[i+1:]...)
			return true
		}
	}
	return false
}

// resolveGroupMembers returns the email list of users in (tenant, group).
// Walks tenantStore.members keyed by tenant, since the membership map
// is private to tenantStore. Read-only, safe to call concurrently.
func resolveGroupMembers(tenant, group string) []string {
	t, ok := tenantsDB.tenants[tenant]
	if !ok {
		return nil
	}
	var out []string
	for email, groups := range t.Members {
		for _, g := range groups {
			if g == group {
				out = append(out, email)
				break
			}
		}
	}
	return out
}

// effectiveVMKeyNames returns the catalogue key NAMES the VM should
// see — the union of explicit assignments and keys derived from the
// authorized groups. Same name appearing in both paths is deduped.
func effectiveVMKeyNames(vmName string) []string {
	seen := map[string]bool{}
	var out []string

	// Explicit per-VM assignments.
	vmKeysMu.Lock()
	for _, name := range vmKeyAssignments[vmName] {
		if !seen[name] {
			seen[name] = true
			out = append(out, name)
		}
	}
	vmKeysMu.Unlock()

	// Group-derived.
	tenantsDB.mu.RLock()
	emails := map[string]bool{}
	for _, ag := range listVMAuthorizedGroups(vmName) {
		for _, e := range resolveGroupMembers(ag.Tenant, ag.Group) {
			emails[e] = true
		}
	}
	tenantsDB.mu.RUnlock()
	if len(emails) == 0 {
		return out
	}
	// Walk the catalogue once, picking keys whose owner is in the email set.
	ks, _ := sshKeysCatalogue.List(nil)
	for _, k := range ks {
		if k.Owner == "" {
			continue
		}
		if !emails[k.Owner] {
			continue
		}
		if seen[k.Name] {
			continue
		}
		seen[k.Name] = true
		out = append(out, k.Name)
	}
	return out
}
