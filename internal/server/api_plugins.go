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
// /catalogue + /installed + POST /install proxy into the agent's gRPC
// surface (ListPluginCatalogue / ListInstalledPlugins / InstallPlugin
// landed in weft-proto v0.5.0). When live is wired the rows come
// straight from `pluginstore.Manager` ; when the agent returns
// Unimplemented (older binary) or is unset, the handler falls back to
// an empty list so the SPA renders "no plugins installed" rather than
// dripping stale canned fixtures.
//
// /{id}/install / uninstall / enable / disable still drive the legacy
// marketplace mock (Plugin struct in plugins.go) — that surface gets
// its own agent-side wiring in a follow-up.

package server

import (
	"context"
	"errors"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
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
	}, func(ctx context.Context, _ *struct{}) (*listPluginCatalogueOutput, error) {
		out := &listPluginCatalogueOutput{}
		out.Body = listPluginCatalogue(ctx)
		return out, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "list-plugin-instances",
		Method:      "GET",
		Path:        "/api/plugins/installed",
		Summary:     "List installed plugin instances",
		Description: "Surfaces each instance the operator has provisioned via `weft plugin install` (or via the dashboard's plugin install drawer). Includes the bound VMs the instance manages, the install timestamp, and a status flag.",
		Tags:        []string{"plugins"},
	}, func(ctx context.Context, _ *struct{}) (*listPluginInstancesOutput, error) {
		out := &listPluginInstancesOutput{}
		out.Body = listPluginInstances(ctx)
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
		Description:   "Body carries the catalogue plugin name, the target project, and a map of input values. The agent provisions the underlying resources (database / cache / topic set / …) and returns the instance UUID the operator can reference in future `weft plugin` commands. When the agent is wired the call drives `pluginstore.Manager.Install` ; otherwise the request fails with 400.",
		Tags:          []string{"plugins"},
		DefaultStatus: 200,
	}, func(ctx context.Context, in *installPluginWithInputsInput) (*installPluginWithInputsOutput, error) {
		_ = auth.UserFromContext(ctx) // identity audit handled upstream
		uuid, err := installPluginInstance(ctx, in.Body.Name, in.Body.Project, in.Body.Inputs)
		if err != nil {
			return nil, huma.Error400BadRequest(err.Error())
		}
		return &installPluginWithInputsOutput{Body: installPluginResultBody{InstanceUUID: uuid}}, nil
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

// ---- live-agent adapters -----------------------------------------
//
// listPluginCatalogue / listPluginInstances / installPluginInstance
// route through the live gRPC client when wired. Without a live
// agent the read endpoints return empty slices ; the install endpoint
// returns a 400 — the dashboard's install drawer is non-functional
// in detached / preview mode by design (no resources to provision).

func listPluginCatalogue(ctx context.Context) []PluginCatalogueEntry {
	if live == nil {
		return nil
	}
	rows, err := live.ListPluginCatalogue(ctx)
	if err != nil {
		return nil
	}
	return mapCatalogueRows(rows)
}

func mapCatalogueRows(in []wclient.PluginCatalogueRow) []PluginCatalogueEntry {
	out := make([]PluginCatalogueEntry, 0, len(in))
	for _, r := range in {
		entry := PluginCatalogueEntry{
			Name:        r.Name,
			Kind:        r.Kind,
			Description: r.Description,
			Inputs:      make([]PluginInput, 0, len(r.Inputs)),
		}
		for _, inp := range r.Inputs {
			entry.Inputs = append(entry.Inputs, PluginInput{
				Name:        inp.Name,
				Label:       inp.Name, // manifest schema doesn't carry a label ; mirror the name
				Type:        mapPluginInputType(inp.Type, inp.Secret),
				Required:    inp.Required,
				Default:     inp.Default,
				Description: inp.Help,
			})
		}
		out = append(out, entry)
	}
	return out
}

// mapPluginInputType folds the manifest's flat type column +
// `secret = true` flag into the SPA's enum (string|number|bool|secret).
// Secret beats the declared type — a secret string still renders
// <input type=password>.
func mapPluginInputType(declared string, secret bool) string {
	if secret {
		return "secret"
	}
	switch declared {
	case "int", "number":
		return "number"
	case "bool", "boolean":
		return "bool"
	case "", "string":
		return "string"
	default:
		return declared
	}
}

func listPluginInstances(ctx context.Context) []PluginInstance {
	if live == nil {
		return nil
	}
	rows, err := live.ListInstalledPlugins(ctx)
	if err != nil {
		return nil
	}
	out := make([]PluginInstance, 0, len(rows))
	for _, r := range rows {
		installedAt := ""
		if r.InstalledAtUnixNS > 0 {
			installedAt = time.Unix(0, r.InstalledAtUnixNS).UTC().Format(time.RFC3339)
		}
		status := r.Status
		if status == "" {
			status = "running"
		}
		out = append(out, PluginInstance{
			Name:         r.Name,
			InstanceUUID: r.InstanceUUID,
			Project:      r.Project,
			VMs:          append([]string(nil), r.VMs...),
			InstalledAt:  installedAt,
			Status:       status,
		})
	}
	return out
}

// installPluginInstance proxies into the agent's Install RPC. Returns
// the freshly-minted (or re-used, on idempotent retry) instance UUID.
// Reports the operator-facing error verbatim — pluginstore's
// ValidateInputs / Install already produce 400-ready messages.
func installPluginInstance(ctx context.Context, name, project string, inputs map[string]string) (string, error) {
	if name == "" {
		return "", errBadRequest("plugin name is required")
	}
	if project == "" {
		return "", errBadRequest("project is required")
	}
	if live == nil {
		return "", errBadRequest("plugin install requires a wired weft-agent")
	}
	uuid, err := live.InstallPlugin(ctx, name, project, inputs)
	if err != nil {
		return "", err
	}
	return uuid, nil
}
