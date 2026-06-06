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
	// Plugins surface is hidden from the user portal entirely — a
	// regular user has no business seeing the catalogue, and we want
	// the user listener to 404 on /api/plugins/* (not 200 with an
	// empty list). Tenant + Infra portals get the read endpoints ;
	// the mutation surface stays Infra-only.
	if !scope.Has(ScopeTenant) && !scope.Has(ScopeAdmin) {
		return
	}
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

	if !scope.Has(ScopeAdmin) {
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
		return staticPluginCatalogue()
	}
	rows, err := live.ListPluginCatalogue(ctx)
	if err != nil {
		// Live agent unreachable / Unimplemented (older binary) :
		// fall back to the static catalogue so the SPA still renders
		// the install drawer in preview / detached mode.
		return staticPluginCatalogue()
	}
	mapped := mapCatalogueRows(rows)
	if len(mapped) == 0 {
		// Empty live response : the agent answered but the cluster
		// hasn't loaded any plugins yet. Use the static list as a
		// preview ; the install drawer will still 400 because the
		// install endpoint hits the live RPC.
		return staticPluginCatalogue()
	}
	return mapped
}

// staticPluginCatalogue mirrors the HCL plugins shipped under
// `weft/catalogue/` so the superadmin dashboard's Plugins panel
// still renders entries when the live weft-agent isn't wired (dev
// mode, preview, or a freshly-installed agent that hasn't loaded
// the catalogue yet).
//
// Keep this list in lock-step with `weft/catalogue/*/plugin.hcl`.
// The inputs surfaced here are the minimal set operators care
// about for the install drawer ; the full schema comes from the
// live agent's `pluginstore.Manager` when wired. The install
// drawer always POSTs into the live RPC, so an unconnected agent
// surfaces "plugin install requires a wired weft-agent" — the
// catalogue still being visible is the point.
func staticPluginCatalogue() []PluginCatalogueEntry {
	return []PluginCatalogueEntry{
		{
			Name:        "caddy-edge",
			Kind:        "edge-proxy",
			Description: "Three Caddy replicas at the cluster edge for north-south L7 ingress, ACME-managed TLS, one per DC.",
			Inputs: []PluginInput{
				{Name: "domains", Label: "Domains", Type: "string", Required: true, Description: "Comma-separated FQDNs Caddy will serve + provision TLS for."},
				{Name: "acme_email", Label: "ACME contact", Type: "string", Required: true, Description: "Let's Encrypt account email."},
			},
		},
		{
			Name:        "github-runners-ha",
			Kind:        "runner-farm",
			Description: "Three GitHub Actions runner replicas with hard anti-affinity across DCs.",
			Inputs: []PluginInput{
				{Name: "github_pat", Label: "GitHub PAT", Type: "secret", Required: true, Description: "PAT with admin:org (org runners) or repo:admin (repo runners)."},
				{Name: "github_url", Label: "Org or repo URL", Type: "string", Required: true, Description: "https://github.com/<org> or https://github.com/<org>/<repo>."},
				{Name: "labels", Label: "Runner labels", Type: "string", Default: "weft,self-hosted,linux,x64", Description: "Comma-separated runner labels."},
				{Name: "replicas", Label: "Replicas", Type: "number", Default: "3", Description: "Number of runner replicas (default 3, one per DC)."},
			},
		},
		{
			Name:        "gitlab-runners-ha",
			Kind:        "runner-farm",
			Description: "Three GitLab CI runner replicas with hard anti-affinity across DCs.",
			Inputs: []PluginInput{
				{Name: "gitlab_url", Label: "GitLab URL", Type: "string", Default: "https://gitlab.com", Description: "GitLab instance base URL."},
				{Name: "registration_token", Label: "Registration token", Type: "secret", Required: true, Description: "Runner registration token from GitLab admin or group settings."},
				{Name: "description", Label: "Description", Type: "string", Default: "weft microVM runner", Description: "Description shown in the GitLab runner UI."},
			},
		},
		{
			Name:        "forgejo-runners-ha",
			Kind:        "runner-farm",
			Description: "Three Forgejo (act_runner) replicas with hard anti-affinity across DCs.",
			Inputs: []PluginInput{
				{Name: "forgejo_url", Label: "Forgejo URL", Type: "string", Required: true, Description: "Forgejo base URL (e.g. https://codeberg.org or self-hosted instance)."},
				{Name: "registration_token", Label: "Registration token", Type: "secret", Required: true, Description: "Runner token minted in Forgejo admin → Actions → Runners."},
			},
		},
		{
			Name:        "forgejo-ha",
			Kind:        "git-forge",
			Description: "Three Forgejo Git-forge replicas behind Caddy with shared Postgres + S3 storage, one per DC. Managed by the weft-ha-forgejo Go agent (install bootstrap, secret sync, health probe).",
			Inputs: []PluginInput{
				{Name: "domain", Label: "Public domain", Type: "string", Required: true, Description: "Public hostname Forgejo serves (e.g. git.example.com). Must resolve to the Caddy edge listener."},
				{Name: "admin_username", Label: "Admin username", Type: "string", Required: true, Description: "Bootstrap admin username."},
				{Name: "admin_password", Label: "Admin password", Type: "secret", Required: true, Description: "Bootstrap admin password. Rotate via the web UI after install."},
				{Name: "admin_email", Label: "Admin email", Type: "string", Required: true, Description: "Bootstrap admin email."},
				{Name: "db_host", Label: "Catalog DB host", Type: "string", Required: true, Description: "Catalog Postgres host (typically `postgres-ha-<short>.weft`)."},
				{Name: "db_password", Label: "Catalog DB password", Type: "secret", Required: true, Description: "Catalog Postgres password for the `forgejo` user."},
				{Name: "s3_endpoint", Label: "S3 endpoint", Type: "string", Description: "Object storage endpoint for attachments + LFS (e.g. https://versitygw-ha-<short>.weft:7070). Empty falls back to local disk (NOT HA)."},
			},
		},
		{
			Name:        "irods-ha",
			Kind:        "data-management",
			Description: "Three iRODS catalog providers on a shared Postgres catalog, one per DC. Managed by the weft-ha-irods Go agent (zone bootstrap, key sync, health probe).",
			Inputs: []PluginInput{
				{Name: "zone_name", Label: "Zone name", Type: "string", Default: "weftZone", Description: "iRODS zone (namespace clients address). Immutable once bootstrapped."},
				{Name: "admin_password", Label: "rodsadmin password", Type: "secret", Required: true, Description: "Bootstrap password for the rodsadmin user. Rotate via `iadmin moduser` once the zone is live."},
				{Name: "icat_db_host", Label: "Catalog DB host", Type: "string", Required: true, Description: "Catalog Postgres host (typically `postgres-ha-<short>.weft`). Postgres must already be installed."},
				{Name: "icat_db_password", Label: "Catalog DB password", Type: "secret", Required: true, Description: "Catalog Postgres password for the `irods` user."},
			},
		},
		{
			Name:        "postgres-ha",
			Kind:        "database",
			Description: "Three-member PostgreSQL cluster managed by weft-ha-postgresql (etcd DCS + VMFencer + Caddy routing).",
			Inputs: []PluginInput{
				{Name: "superuser_password", Label: "Superuser password", Type: "secret", Required: true, Description: "Password for the `postgres` superuser."},
				{Name: "replication_password", Label: "Replication password", Type: "secret", Required: true, Description: "Password used by replicas to stream WAL from the primary."},
				{Name: "database_name", Label: "Initial database", Type: "string", Default: "app", Description: "Initial database created on bootstrap."},
				{Name: "synchronous_commit", Label: "synchronous_commit", Type: "string", Default: "on", Description: "`on` waits for one off-DC replica ack (RPO 0 on DC outage) ; `off` = async."},
			},
		},
		{
			Name:        "redis-ha",
			Kind:        "cache",
			Description: "Three-node Redis with Sentinel sidecars for automatic failover, one per DC.",
			Inputs: []PluginInput{
				{Name: "auth_password", Label: "Auth password", Type: "secret", Required: true, Description: "Redis AUTH password — required even for in-cluster clients."},
				{Name: "max_memory_mb", Label: "Max memory per node (MB)", Type: "number", Default: "2048", Description: "Per-node Redis maxmemory."},
			},
		},
		{
			Name:        "versitygw-ha",
			Kind:        "object-storage",
			Description: "Three-node versitygw (Apache-2.0) S3 gateway, one per DC ; durability via weft-block-replicated volumes.",
			Inputs: []PluginInput{
				{Name: "root_access_key", Label: "Root access key", Type: "string", Required: true, Description: "S3 access-key-id used by the first `aws configure`."},
				{Name: "root_secret_key", Label: "Root secret key", Type: "secret", Required: true, Description: "S3 secret-access-key. Stored in the cluster secret store."},
				{Name: "backend", Label: "Backend", Type: "string", Default: "block", Description: "`block` = per-replica weft-block volumes ; `cubefs` = shared CubeFS mount."},
				{Name: "volumes_per_node", Label: "Volumes per node", Type: "number", Default: "4", Description: "Block volumes per replica when backend=block."},
			},
		},
		{
			Name:        "loki-ha",
			Kind:        "logs",
			Description: "Three Loki replicas in simple-scalable mode, one per DC, with S3 chunk + index storage.",
			Inputs: []PluginInput{
				{Name: "s3_endpoint", Label: "S3 endpoint", Type: "string", Required: true, Description: "S3 endpoint URL (e.g. https://versitygw-ha-<short>.weft:7070)."},
				{Name: "s3_access_key", Label: "S3 access key", Type: "string", Required: true, Description: "S3 access-key-id."},
				{Name: "s3_secret_key", Label: "S3 secret key", Type: "secret", Required: true, Description: "S3 secret-access-key."},
				{Name: "s3_bucket", Label: "S3 bucket", Type: "string", Default: "loki", Description: "Bucket name for chunk + index storage."},
				{Name: "retention_days", Label: "Retention (days)", Type: "number", Default: "30", Description: "Log retention before chunks are dropped."},
			},
		},
		{
			Name:        "prometheus-ha",
			Kind:        "metrics",
			Description: "Three federated Prometheus replicas, one per DC, with TSDB persistence and optional remote_write.",
			Inputs: []PluginInput{
				{Name: "retention_days", Label: "Retention (days)", Type: "number", Default: "15", Description: "Local TSDB retention."},
				{Name: "remote_write_url", Label: "remote_write URL", Type: "string", Description: "Optional Cortex/Mimir/Thanos remote-write endpoint for long-term storage."},
			},
		},
		{
			Name:        "grafana-ha",
			Kind:        "dashboards",
			Description: "Three Grafana replicas behind Caddy with sticky sessions, OIDC auth, postgres-ha-backed state.",
			Inputs: []PluginInput{
				{Name: "oidc_issuer", Label: "OIDC issuer", Type: "string", Required: true, Description: "OIDC discovery URL (e.g. https://login.example.com/realms/weft)."},
				{Name: "oidc_client_id", Label: "OIDC client ID", Type: "string", Required: true, Description: "OAuth2 client ID registered with the IdP."},
				{Name: "oidc_client_secret", Label: "OIDC client secret", Type: "secret", Required: true, Description: "OAuth2 client secret."},
				{Name: "admin_password", Label: "Bootstrap admin password", Type: "secret", Required: true, Description: "Initial admin user password (rotate via Grafana after first login)."},
			},
		},
		{
			Name:        "vault-ha",
			Kind:        "secrets",
			Description: "Three Vault members with Raft HA and KMS auto-unseal, one per DC.",
			Inputs: []PluginInput{
				{Name: "kms_provider", Label: "KMS provider", Type: "string", Default: "awskms", Description: "Auto-unseal provider : awskms / gcpckms / azurekeyvault / transit."},
				{Name: "kms_key_id", Label: "KMS key ID", Type: "string", Required: true, Description: "Provider-specific key identifier (ARN / resource name)."},
			},
		},
		{
			Name:        "jupyterhub-ha",
			Kind:        "portal",
			Description: "JupyterHub HA portal — per-user microVM notebooks, 3-DC controllers, OIDC auth.",
			Inputs: []PluginInput{
				{Name: "oidc_issuer", Label: "OIDC issuer", Type: "string", Required: true, Description: "OIDC discovery URL."},
				{Name: "oidc_client_id", Label: "OIDC client ID", Type: "string", Required: true},
				{Name: "oidc_client_secret", Label: "OIDC client secret", Type: "secret", Required: true},
				{Name: "notebook_image", Label: "Notebook image", Type: "string", Default: "ghcr.io/openweft/jupyter-base:v0.1.0", Description: "Per-user notebook microVM image."},
			},
		},
	}
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
