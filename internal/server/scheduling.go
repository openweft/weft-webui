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
	"net/http"
	"sort"
	"strings"
	"sync"
)

// SchedulingRule is the in-memory shape. The Placement field is the
// compact joined string the UI surfaces ; the typed AZ/Rack/Host
// directives are kept too so the future RPC mapping stays mechanical.
type SchedulingRule struct {
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

// ---- HTTP handlers ------------------------------------------------

// handleCreateSchedulingRule : POST /api/scheduling-rules
//
// Body shape (every field optional except Name and Selector) :
//
//   { Name, Count, Selector, AZ, Rack, Host, Project }
//
// Project defaults to the session's tenant-scoped project when set
// (so the operator doesn't have to repeat themselves).
func handleCreateSchedulingRule(w http.ResponseWriter, r *http.Request) {
	var body struct {
		Name, Selector, AZ, Rack, Host, Project string
		Count                                   int
	}
	if err := decodeJSON(r, &body); err != nil {
		writeErr(w, errBadReq("invalid body: "+err.Error()))
		return
	}
	if body.Project == "" {
		_, body.Project = scopeFromRequest(r)
	}
	if body.Project == "" {
		body.Project = "platform"
	}
	rule := &SchedulingRule{
		Name: body.Name, Count: body.Count, Selector: body.Selector,
		AZ: body.AZ, Rack: body.Rack, Host: body.Host,
		Project: body.Project,
	}
	if err := schedulingDB.create(rule); err != nil {
		writeErr(w, err)
		return
	}
	userAction(r, "scheduling-rule.create")
	writeJSON(w, http.StatusCreated, ruleToRow(rule))
}

// handleDeleteSchedulingRule : DELETE /api/scheduling-rules/{name}
func handleDeleteSchedulingRule(w http.ResponseWriter, r *http.Request) {
	if err := schedulingDB.delete(r.PathValue("name")); err != nil {
		writeErr(w, err)
		return
	}
	userAction(r, "scheduling-rule.delete")
	w.WriteHeader(http.StatusNoContent)
}
