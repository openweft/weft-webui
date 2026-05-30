// api_plugins.go — *-as-a-service plugin registry (admin).
//
//   GET    /api/plugins
//   POST   /api/plugins/{id}/install        — flips InstallStatus=installed + Enabled=true
//   POST   /api/plugins/{id}/uninstall      — flips InstallStatus=available + Enabled=false
//   POST   /api/plugins/{id}/enable         — Enabled=true (no-op if uninstalled)
//   POST   /api/plugins/{id}/disable        — Enabled=false (keeps installed)
//
// Mutations are scoped to ScopeAdmin — the user listener still
// serves GET so the panel shows up read-only for plain users.

package server

import (
	"context"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
)

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

	if scope != ScopeAdmin {
		return
	}

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
