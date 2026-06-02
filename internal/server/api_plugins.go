// api_plugins.go — *-as-a-service plugin registry (admin).
//
//   GET    /api/plugins                     — full marketplace listing
//   GET    /api/plugins/catalogue           — installable plugins + their input schema
//   GET    /api/plugins/installed           — instances with project + bound VMs
//   POST   /api/plugins/install             — install with form inputs, returns instance_uuid
//   POST   /api/plugins/{id}/install        — flips InstallStatus=installed + Enabled=true
//   POST   /api/plugins/{id}/uninstall      — flips InstallStatus=available + Enabled=false
//   POST   /api/plugins/{id}/enable         — Enabled=true (no-op if uninstalled)
//   POST   /api/plugins/{id}/disable        — Enabled=false (keeps installed)
//
// Mutations are scoped to ScopeAdmin — the user listener still
// serves GET so the panel shows up read-only for plain users.
//
// The /catalogue + /installed + POST /install surface is the new
// "weft plugin install" model (form-driven). The agent-side gRPC for
// this does not yet exist in weft-proto ; canned data lives in
// pluginCatalogueSeed / pluginInstanceStore below and feeds the SPA
// directly. A future weft-agent RPC will replace those helpers
// without changing the wire shape.

package server

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

// errBadRequest builds a sentinel-less plain error the install
// handler maps to a 400. Kept simple — the message string is the
// operator-visible RFC-7807 'detail' field.
func errBadRequest(msg string) error { return errors.New(msg) }

func mountPluginsAPI(api huma.API, scope Scope) {
	huma.Register(api, huma.Operation{
		OperationID: "list-plugins",
		Method:      "GET",
		Path:        "/api/plugins",
		Summary:     "List installable plugins (*-as-a-service modules)",
		Tags:        []string{"plugins"},
	}, func(_ context.Context, _ *struct{}) (*listPluginsOutput, error) {
		out := &listPluginsOutput{}
		out.Body = listPlugins()
		return out, nil
	})

	// ---- new "weft plugin install" surface : form-driven --------
	//
	// Read endpoints stay on every scope so the user UI can show a
	// read-only catalogue. The POST stays admin-only.

	huma.Register(api, huma.Operation{
		OperationID: "list-plugin-catalogue",
		Method:      "GET",
		Path:        "/api/plugins/catalogue",
		Summary:     "List installable plugin definitions with their input schema",
		Description: "Each catalogue entry advertises the inputs the operator must provide on install — name, kind, type (string|number|bool|secret), required flag, default. The SPA renders a form from this schema and POSTs back to /api/plugins/install.",
		Tags:        []string{"plugins"},
	}, func(_ context.Context, _ *struct{}) (*listPluginCatalogueOutput, error) {
		out := &listPluginCatalogueOutput{}
		out.Body = listPluginCatalogue()
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-plugin-instances",
		Method:      "GET",
		Path:        "/api/plugins/installed",
		Summary:     "List installed plugin instances",
		Description: "Surfaces each instance the operator has provisioned via `weft plugin install` (or via the dashboard's plugin install drawer). Includes the bound VMs the instance manages, the install timestamp, and a status flag.",
		Tags:        []string{"plugins"},
	}, func(_ context.Context, _ *struct{}) (*listPluginInstancesOutput, error) {
		out := &listPluginInstancesOutput{}
		out.Body = listPluginInstances()
		return out, nil
	})

	if scope != ScopeAdmin {
		return
	}

	huma.Register(api, huma.Operation{
		OperationID:   "install-plugin-with-inputs",
		Method:        "POST",
		Path:          "/api/plugins/install",
		Summary:       "Install a plugin with form inputs — returns the new instance UUID",
		Description:   "Body carries the catalogue plugin name, the target project, and a map of input values. The agent provisions the underlying resources (database / cache / topic set / …) and returns the instance UUID the operator can reference in future `weft plugin` commands. Mock implementation : the instance is stored in-memory.",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *installPluginWithInputsInput) (*installPluginWithInputsOutput, error) {
		email := ""
		if u := auth.UserFromContext(ctx); u != nil {
			email = u.Email
			if email == "" {
				email = u.Subject
			}
		}
		inst, err := installPluginInstance(in.Body.Name, in.Body.Project, in.Body.Inputs, email)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &installPluginWithInputsOutput{Body: installPluginResultBody{InstanceUUID: inst.InstanceUUID}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "install-plugin",
		Method:        "POST",
		Path:          "/api/plugins/{id}/install",
		Summary:       "Install a plugin (cluster-admin) — exposes its resources in the sidebar",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *pluginIDInput) (*pluginOutput, error) {
		now := time.Now().UTC().Format(time.RFC3339)
		email := ""
		if u := auth.UserFromContext(ctx); u != nil {
			email = u.Email
			if email == "" {
				email = u.Subject
			}
		}
		p, ok := mutatePlugin(in.ID, func(p *Plugin) {
			p.InstallStatus = "installed"
			p.Enabled = true
			p.InstalledAt = now
			p.InstalledBy = email
		})
		if !ok {
			return nil, huma.Error404NotFound("plugin not found: " + in.ID)
		}
		return &pluginOutput{Body: *p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "uninstall-plugin",
		Method:        "POST",
		Path:          "/api/plugins/{id}/uninstall",
		Summary:       "Uninstall a plugin (cluster-admin) — its resources disappear from the sidebar",
		Description:   "Resource rows belonging to the uninstalled plugin remain in the mock store ; reinstalling restores visibility instantly. Live wiring would drain workloads first.",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *pluginIDInput) (*pluginOutput, error) {
		p, ok := mutatePlugin(in.ID, func(p *Plugin) {
			p.InstallStatus = "available"
			p.Enabled = false
			p.InstalledAt = ""
			p.InstalledBy = ""
		})
		if !ok {
			return nil, huma.Error404NotFound("plugin not found: " + in.ID)
		}
		return &pluginOutput{Body: *p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "enable-plugin",
		Method:        "POST",
		Path:          "/api/plugins/{id}/enable",
		Summary:       "Enable an installed plugin (cluster-admin)",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *pluginIDInput) (*pluginOutput, error) {
		p, ok := mutatePlugin(in.ID, func(p *Plugin) {
			if p.InstallStatus == "installed" {
				p.Enabled = true
			}
		})
		if !ok {
			return nil, huma.Error404NotFound("plugin not found: " + in.ID)
		}
		return &pluginOutput{Body: *p}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID:   "disable-plugin",
		Method:        "POST",
		Path:          "/api/plugins/{id}/disable",
		Summary:       "Temporarily disable an installed plugin (cluster-admin) — hides its sidebar entry without uninstalling",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(_ context.Context, in *pluginIDInput) (*pluginOutput, error) {
		p, ok := mutatePlugin(in.ID, func(p *Plugin) {
			p.Enabled = false
		})
		if !ok {
			return nil, huma.Error404NotFound("plugin not found: " + in.ID)
		}
		return &pluginOutput{Body: *p}, nil
	})
}

type pluginIDInput struct {
	ID string `path:"id" doc:"Plugin id" minLength:"1" maxLength:"64"`
}

type listPluginsOutput struct {
	Body []*Plugin
}

type pluginOutput struct {
	Body Plugin
}

// ---- new "weft plugin install" surface --------------------------

// PluginInput describes one field of the install form. The SPA
// renders an input element from this schema (`type=secret` →
// <input type="password">, `required=true` → form validation gate,
// `default` → pre-filled placeholder). Kept flat — no nested
// schemas — so the dashboard form generator stays trivial.
type PluginInput struct {
	Name        string `json:"name"             doc:"Input identifier, used as the form-field name and the key in the POST body's 'inputs' map"`
	Label       string `json:"label"            doc:"Human-readable label rendered next to the input"`
	Type        string `json:"type"             doc:"'string' | 'number' | 'bool' | 'secret'" enum:"string,number,bool,secret"`
	Required    bool   `json:"required"         doc:"When true, the SPA blocks submit until the input is non-empty"`
	Default     string `json:"default,omitempty" doc:"Optional default — pre-filled in the form ; ignored when Type == 'secret'"`
	Description string `json:"description,omitempty" doc:"Inline hint shown below the input"`
}

// PluginCatalogueEntry is one row of /api/plugins/catalogue. Shape
// kept narrow on purpose : the UI's left-pane cards only need name +
// kind + description + the inputs schema. The richer Plugin struct
// (Vendor / Version / Resources) stays on the legacy /api/plugins
// surface for the marketplace page.
type PluginCatalogueEntry struct {
	Name        string        `json:"name"        doc:"Stable plugin slug, e.g. 'vitess-dbaas'"`
	Kind        string        `json:"kind"        doc:"Category, e.g. 'database', 'cache', 'streaming', 'storage'"`
	Description string        `json:"description" doc:"One-paragraph description ; shown in the install drawer header"`
	Inputs      []PluginInput `json:"inputs"      doc:"Form schema the operator must fill on install"`
}

// PluginInstance is one row of /api/plugins/installed. VMs is the
// list of microVM UUIDs the instance currently manages (empty when
// the install is still bootstrapping). Status is "running" |
// "provisioning" | "degraded" | "failed" so the SPA can color-code
// the row without inventing a per-plugin enum.
type PluginInstance struct {
	Name         string   `json:"name"          doc:"Plugin slug from the catalogue (links the instance back to its definition)"`
	InstanceUUID string   `json:"instance_uuid" doc:"Cluster-unique instance identifier — returned by POST /api/plugins/install"`
	Project      string   `json:"project"       doc:"Project the instance was installed under"`
	VMs          []string `json:"vms"           doc:"microVM UUIDs the instance currently manages"`
	InstalledAt  string   `json:"installed_at"  doc:"RFC-3339 timestamp of the install"`
	InstalledBy  string   `json:"installed_by"  doc:"OIDC email of the installing operator"`
	Status       string   `json:"status"        doc:"'running' | 'provisioning' | 'degraded' | 'failed'" enum:"running,provisioning,degraded,failed"`
}

type listPluginCatalogueOutput struct {
	Body []PluginCatalogueEntry
}

type listPluginInstancesOutput struct {
	Body []PluginInstance
}

type installPluginWithInputsBody struct {
	Name    string            `json:"name"    doc:"Plugin slug from /api/plugins/catalogue" minLength:"1" maxLength:"64"`
	Project string            `json:"project" doc:"Project to install the instance under" minLength:"1" maxLength:"64"`
	Inputs  map[string]string `json:"inputs"  doc:"Map of input name → value, matching the catalogue entry's inputs schema. Secret inputs are passed through here too — the agent persists them encrypted."`
}

type installPluginWithInputsInput struct {
	Body installPluginWithInputsBody
}

type installPluginResultBody struct {
	InstanceUUID string `json:"instance_uuid" doc:"Cluster-unique id of the freshly installed instance"`
}

type installPluginWithInputsOutput struct {
	Body installPluginResultBody
}

// ---- mock catalogue + instance store -----------------------------
//
// The store is in-memory ; restarts wipe the instance list. Live
// wiring lives in weft-agent (see project_driver_plugins memory ;
// the *-as-a-service plugin install path is the same go-plugin
// mechanism that backs hypervisor drivers, just at a higher altitude).

var (
	pluginInstancesMu sync.Mutex
	pluginInstances   = seedPluginInstances()
)

func pluginCatalogueSeed() []PluginCatalogueEntry {
	return []PluginCatalogueEntry{
		{
			Name:        "vitess-dbaas",
			Kind:        "database",
			Description: "Sharded MySQL via a Vitess cluster. Provisions a keyspace per instance and exposes a MySQL-compatible endpoint.",
			Inputs: []PluginInput{
				{Name: "keyspace", Label: "Keyspace name", Type: "string", Required: true, Description: "Logical database identifier ; becomes the MySQL schema name."},
				{Name: "shards", Label: "Shard count", Type: "number", Required: true, Default: "2", Description: "How many shards to split the keyspace across."},
				{Name: "replication_password", Label: "Replication password", Type: "secret", Required: true, Description: "Used between primary and replicas ; never displayed back to the operator."},
				{Name: "enable_backups", Label: "Enable nightly backups", Type: "bool", Required: false, Default: "true"},
			},
		},
		{
			Name:        "valkey-cache",
			Kind:        "cache",
			Description: "Managed Valkey (BSD-licensed Redis fork). One instance maps to one isolated Valkey cluster.",
			Inputs: []PluginInput{
				{Name: "memory_mb", Label: "Memory (MiB)", Type: "number", Required: true, Default: "512"},
				{Name: "persistence", Label: "Enable persistence (AOF)", Type: "bool", Required: false, Default: "false", Description: "Disable for pure-cache workloads ; enable when you need restart-survival."},
				{Name: "auth_password", Label: "AUTH password", Type: "secret", Required: false, Description: "Optional — leave empty to disable client authentication (NOT recommended outside private networks)."},
			},
		},
		{
			Name:        "kafka-streaming",
			Kind:        "streaming",
			Description: "Event streaming via Apache Kafka. Provisions a fresh cluster per instance with the requested broker count.",
			Inputs: []PluginInput{
				{Name: "brokers", Label: "Broker count", Type: "number", Required: true, Default: "3"},
				{Name: "retention_hours", Label: "Default retention (hours)", Type: "number", Required: false, Default: "168"},
				{Name: "tls_keystore", Label: "TLS keystore (PEM)", Type: "secret", Required: false, Description: "Optional client-cert keystore for mTLS between brokers and clients."},
			},
		},
		{
			Name:        "clickhouse-analytics",
			Kind:        "analytics",
			Description: "Column-oriented analytics database. Each instance is a single replicated ClickHouse cluster.",
			Inputs: []PluginInput{
				{Name: "cluster_name", Label: "Cluster name", Type: "string", Required: true, Description: "Used as the ClickHouse on-cluster identifier."},
				{Name: "replicas", Label: "Replicas", Type: "number", Required: true, Default: "2"},
				{Name: "admin_password", Label: "Admin password", Type: "secret", Required: true},
			},
		},
	}
}

func seedPluginInstances() map[string]*PluginInstance {
	return map[string]*PluginInstance{
		"inst-7f2e": {
			Name:         "vitess-dbaas",
			InstanceUUID: "inst-7f2e",
			Project:      "billing",
			VMs:          []string{"vm-vitess-primary-0", "vm-vitess-replica-0", "vm-vitess-replica-1"},
			InstalledAt:  "2026-05-12T14:22:00Z",
			InstalledBy:  "alice@weft.local",
			Status:       "running",
		},
		"inst-c14a": {
			Name:         "valkey-cache",
			InstanceUUID: "inst-c14a",
			Project:      "platform",
			VMs:          []string{"vm-valkey-0"},
			InstalledAt:  "2026-05-30T09:00:00Z",
			InstalledBy:  "bob@weft.local",
			Status:       "provisioning",
		},
	}
}

func listPluginCatalogue() []PluginCatalogueEntry {
	return pluginCatalogueSeed()
}

func findCatalogueEntry(name string) (PluginCatalogueEntry, bool) {
	for _, e := range pluginCatalogueSeed() {
		if e.Name == name {
			return e, true
		}
	}
	return PluginCatalogueEntry{}, false
}

func listPluginInstances() []PluginInstance {
	pluginInstancesMu.Lock()
	defer pluginInstancesMu.Unlock()
	out := make([]PluginInstance, 0, len(pluginInstances))
	for _, inst := range pluginInstances {
		out = append(out, *inst)
	}
	return out
}

// installPluginInstance validates the body against the catalogue
// schema, allocates an instance UUID, and stores the new row. Errors
// surface as plain errors ; the huma handler maps them to 400.
func installPluginInstance(name, project string, inputs map[string]string, email string) (*PluginInstance, error) {
	name = strings.TrimSpace(name)
	project = strings.TrimSpace(project)
	if name == "" {
		return nil, errBadRequest("plugin name is required")
	}
	if project == "" {
		return nil, errBadRequest("project is required")
	}
	entry, ok := findCatalogueEntry(name)
	if !ok {
		return nil, errBadRequest("unknown plugin: " + name)
	}
	// Validate every required input is present and non-empty.
	for _, in := range entry.Inputs {
		if !in.Required {
			continue
		}
		v, ok := inputs[in.Name]
		if !ok || strings.TrimSpace(v) == "" {
			return nil, errBadRequest("required input missing: " + in.Name)
		}
	}
	pluginInstancesMu.Lock()
	defer pluginInstancesMu.Unlock()
	inst := &PluginInstance{
		Name:         name,
		InstanceUUID: "inst-" + randomHex(4),
		Project:      project,
		VMs:          nil,
		InstalledAt:  time.Now().UTC().Format(time.RFC3339),
		InstalledBy:  email,
		Status:       "provisioning",
	}
	pluginInstances[inst.InstanceUUID] = inst
	return inst, nil
}

func randomHex(n int) string {
	b := make([]byte, n)
	if _, err := rand.Read(b); err != nil {
		// crypto/rand failure is unrecoverable at this altitude ; fall
		// back to a time-derived suffix so the install still goes
		// through with a unique-enough id.
		return hex.EncodeToString([]byte(time.Now().UTC().Format("150405")))
	}
	return hex.EncodeToString(b)
}
