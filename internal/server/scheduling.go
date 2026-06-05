// scheduling.go — in-memory store for declarative placement rules.
//
// weft-agent does not yet expose RPCs for these (the scheduler reads
// the placement clause directly from the cluster.hcl). The webui
// surfaces them anyway so the operator gets a single dashboard view
// of "what's the scheduler being asked to enforce ?" — when the agent
// grows ListSchedulingRules / CreateSchedulingRule, this store maps
// 1↔1 to those calls.
//
// Concurrency : one RWMutex around a map, snapshot-then-serialise on
// reads, append-or-replace under the write lock on mutations.
package server

import (
	"sort"
	"strings"
	"sync"
)

// SchedulingRule is the in-memory shape. The Placement field is the
// compact joined string the UI surfaces ; the typed AZ/Rack/Host
// directives are kept too so the future RPC mapping stays mechanical.
type SchedulingRule struct {
	// UUID is the opaque handle proto v0.9.0 keys on
	// (Update/Delete take a UUID). Carried alongside Name so the
	// SPA can keep addressing rules by name while live-first paths
	// resolve to UUID before dialling the wclient.
	UUID      string
	Name      string
	Count     int    // desired replicas
	Ready     int    // observed compliant replicas
	Selector  string // label expression
	AZ        string // "different" | "same" | <name> | ""
	Rack      string
	Host      string
	Project   string
	Status    string // compliant | drifting | unschedulable
}

type schedulingStore struct {
	mu    sync.RWMutex
	rules map[string]*SchedulingRule
}

var schedulingDB = func() *schedulingStore {
	s := &schedulingStore{rules: make(map[string]*SchedulingRule)}
	// Seed fixtures — same set the static registry used to hold inline
	// so the table looks identical until the operator mutates anything.
	for _, r := range []*SchedulingRule{
		{Name: "nats-quorum", Count: 3, Ready: 3, Selector: "app=nats",
			AZ: "different", Rack: "different", Host: "different",
			Project: "platform", Status: "compliant"},
		{Name: "etcd-quorum", Count: 3, Ready: 3, Selector: "app=etcd",
			AZ: "different", Rack: "different", Host: "different",
			Project: "platform", Status: "compliant"},
		{Name: "cubefs-meta", Count: 3, Ready: 3, Selector: "app=cubefs-master",
			AZ: "different", Host: "different",
			Project: "platform", Status: "compliant"},
		{Name: "web-tier", Count: 2, Ready: 2, Selector: "project=team-alpha, app=web",
			Host: "different",
			Project: "team-alpha", Status: "compliant"},
		{Name: "research-batch", Count: 5, Ready: 4, Selector: "project=research, kind=batch",
			AZ:      "DC-C",
			Project: "research", Status: "drifting"},
		{Name: "ci-burst", Count: 0, Ready: 0, Selector: "kind=ci-job",
			Project: "team-beta", Status: "compliant"},
	} {
		r.UUID = mockUUID("scheduling-rule", r.Project, r.Name)
		s.rules[r.Name] = r
	}
	return s
}()

// list returns every rule, optionally narrowed by project for the
// user-UI tenant-aggregate view.
func (s *schedulingStore) list(projectFilter string) []map[string]any {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]map[string]any, 0, len(s.rules))
	names := make([]string, 0, len(s.rules))
	for k := range s.rules {
		names = append(names, k)
	}
	sort.Strings(names)
	for _, n := range names {
		r := s.rules[n]
		if projectFilter != "" && r.Project != projectFilter {
			continue
		}
		out = append(out, ruleToRow(r))
	}
	return out
}

// ruleToRow projects a typed rule into the UI's flat row shape.
// `placement` is the compact joined form the table surfaces ; absent
// directives drop out so a fully-unconstrained rule renders as `any`.
func ruleToRow(r *SchedulingRule) map[string]any {
	parts := []string{}
	if r.AZ != "" {
		parts = append(parts, "az="+r.AZ)
	}
	if r.Rack != "" {
		parts = append(parts, "rack="+r.Rack)
	}
	if r.Host != "" {
		parts = append(parts, "host="+r.Host)
	}
	placement := "any"
	if len(parts) > 0 {
		placement = strings.Join(parts, ", ")
	}
	return map[string]any{
		"uuid":      r.UUID,
		"name":      r.Name,
		"count":     fmtCount(r.Ready, r.Count),
		"placement": placement,
		"selector":  r.Selector,
		"project":   r.Project,
		"status":    r.Status,
	}
}

func fmtCount(ready, want int) string {
	if want == 0 && ready == 0 {
		return "0/0"
	}
	// "4/5" style.
	return formatInt(ready) + "/" + formatInt(want)
}

func formatInt(n int) string {
	// Avoid importing strconv just for this — fast path for the
	// small numbers we see in scheduling rules.
	if n == 0 {
		return "0"
	}
	b := make([]byte, 0, 4)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		b = append([]byte{byte('0' + n%10)}, b...)
		n /= 10
	}
	if neg {
		b = append([]byte{'-'}, b...)
	}
	return string(b)
}

// create adds a new rule. Rejects on duplicate name.
func (s *schedulingStore) create(r *SchedulingRule) error {
	r.Name = strings.TrimSpace(r.Name)
	if r.Name == "" {
		return errBadReq("name is required")
	}
	if r.Count < 0 {
		return errBadReq("count must be ≥ 0")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, exists := s.rules[r.Name]; exists {
		return errConflict("rule already exists")
	}
	if r.UUID == "" {
		r.UUID = mockUUID("scheduling-rule", r.Project, r.Name)
	}
	// Newly-created rules start "drifting" with ready=0 until the
	// scheduler reports back ; treat count=0 as already-satisfied.
	if r.Count == 0 {
		r.Ready, r.Status = 0, "compliant"
	} else {
		r.Ready, r.Status = 0, "drifting"
	}
	s.rules[r.Name] = r
	return nil
}

// ruleUUID resolves a scheduling-rule name to its opaque UUID handle.
// Used by live-first handlers that need the wclient's UUID-keyed
// Update/Delete RPCs while the SPA still addresses rules by name.
func (s *schedulingStore) ruleUUID(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	r, ok := s.rules[name]
	if !ok {
		return "", false
	}
	return r.UUID, true
}

// delete removes a rule. Returns ErrNotFound on a missing name.
func (s *schedulingStore) delete(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.rules[name]; !ok {
		return errNotFound("scheduling rule")
	}
	delete(s.rules, name)
	return nil
}

// schedulingRulePatch is the typed shape of a PATCH body. Every
// field is a pointer so the handler can distinguish "not provided"
// from "explicit zero / empty". A nil pointer leaves the existing
// value untouched ; a non-nil pointer overwrites it (including with
// "" to clear an axis back to `any`).
type schedulingRulePatch struct {
	Count    *int    `json:"count,omitempty"    minimum:"0"`
	Selector *string `json:"selector,omitempty"`
	AZ       *string `json:"az,omitempty"`
	Rack     *string `json:"rack,omitempty"`
	Host     *string `json:"host,omitempty"`
	Project  *string `json:"project,omitempty"`
}

// update applies a partial patch to an existing rule. Mirrors create's
// "compliant when count=0" projection so the status badge reflects the
// new desired state immediately ; the scheduler reconciles ready
// asynchronously.
func (s *schedulingStore) update(name string, p schedulingRulePatch) (*SchedulingRule, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	r, ok := s.rules[name]
	if !ok {
		return nil, errNotFound("scheduling rule")
	}
	if p.Count != nil {
		if *p.Count < 0 {
			return nil, errBadReq("count must be ≥ 0")
		}
		r.Count = *p.Count
	}
	if p.Selector != nil {
		r.Selector = strings.TrimSpace(*p.Selector)
	}
	if p.AZ != nil {
		r.AZ = strings.TrimSpace(*p.AZ)
	}
	if p.Rack != nil {
		r.Rack = strings.TrimSpace(*p.Rack)
	}
	if p.Host != nil {
		r.Host = strings.TrimSpace(*p.Host)
	}
	if p.Project != nil {
		r.Project = strings.TrimSpace(*p.Project)
	}
	// Re-derive status : count=0 → compliant ; otherwise drifting
	// until the scheduler reports ready==count. Don't overwrite a
	// previously-compliant rule that hasn't moved if count is unchanged
	// and matches Ready.
	if r.Count == 0 {
		r.Ready, r.Status = 0, "compliant"
	} else if r.Ready < r.Count {
		r.Status = "drifting"
	}
	return r, nil
}

// ---- HTTP handlers ------------------------------------------------

// handleCreateSchedulingRule : POST /api/scheduling-rules
//
// Body shape (every field optional except Name and Selector) :
// (Scheduling-rule create/delete handlers moved to huma —
// see api_networking.go.)
