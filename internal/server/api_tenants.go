// api_tenants.go — typed endpoints for tenants, projects, quotas,
// and the per-session /api/me lookup.
//
// Authorisation policy is preserved verbatim from the legacy handlers :
//
//   - cluster admin    creates tenants and elects tenant admins
//   - tenant admin     within their tenants : add projects, members,
//                      grant per-project roles, set project quotas
//   - everyone else    read-only ; non-members get 404 (don't-acknowledge)
//
// Live-first across the board ; on Unimplemented we fall back to the
// in-memory store so the affordance keeps working before weft-agent
// catches up.
package server

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/openweft/weft-webui/internal/auth"
	"github.com/openweft/weft-webui/internal/wclient"
)

func mountTenantsAPI(api huma.API, scope Scope) {
	// Cluster-admin only : CreateTenant / AddTenantAdmin (the
	// "promote an OIDC subject to tenant-admin" RPCs).
	if scope.Has(ScopeAdmin) {
		mountTenantsAdminAPI(api)
	}
	// Tenant-admin delegated mutations (add projects, members, grant
	// roles, set per-project quotas) : exposed on the Tenant + Infra
	// portals. The user portal never sees these endpoints.
	if scope.Has(ScopeTenant) || scope.Has(ScopeAdmin) {
		mountTenantsDelegatedAPI(api)
		mountQuotasAPI(api)
	}
	mountTenantsReadAPI(api)
	mountMeAPI(api)
}

// ---- Cluster-admin only ------------------------------------------

func mountTenantsAdminAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "create-tenant",
		Method:        "POST",
		Path:          "/api/tenants",
		Summary:       "Create a tenant (cluster admin)",
		Description:   "Live-first via weft-agent ; falls back to the mock store on Unimplemented so the affordance keeps working before the daemon catches up.",
		Tags:          []string{"tenants"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *createTenantInput) (*createTenantOutput, error) {
		u := auth.UserFromContext(ctx)
		if !isClusterAdmin(u) {
			return nil, huma.Error403Forbidden("cluster admin required")
		}
		if live != nil {
			uuid, err := live.CreateTenant(ctx, in.Body.Name, in.Body.Domain)
			if err == nil {
				return &createTenantOutput{Body: CreateTenantResp{Name: in.Body.Name, UUID: uuid}}, nil
			}
			if !wclient.IsUnimplemented(err) {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if err := tenantsDB.createTenant(in.Body.Name, in.Body.Domain); err != nil {
			return nil, hideHTTPErr(err)
		}
		return &createTenantOutput{Body: CreateTenantResp{Name: in.Body.Name}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "add-tenant-admin",
		Method:      "POST",
		Path:        "/api/tenants/{name}/admins",
		Summary:     "Elect a tenant admin (cluster admin)",
		Tags:        []string{"tenants"},
	}, func(ctx context.Context, in *addTenantAdminInput) (*emailOutput, error) {
		u := auth.UserFromContext(ctx)
		if !isClusterAdmin(u) {
			return nil, huma.Error403Forbidden("cluster admin required")
		}
		if live != nil {
			if uuid := liveLookupTenantUUID(ctx, in.Name); uuid != "" {
				if err := live.AddTenantAdmin(ctx, uuid, in.Body.Email); err == nil {
					return &emailOutput{Body: EmailResp{Email: in.Body.Email}}, nil
				} else if !wclient.IsUnimplemented(err) {
					return nil, huma.Error502BadGateway("live: " + err.Error())
				}
			}
		}
		if err := tenantsDB.addTenantAdmin(in.Name, in.Body.Email); err != nil {
			return nil, hideHTTPErr(err)
		}
		return &emailOutput{Body: EmailResp{Email: in.Body.Email}}, nil
	})
}

// CreateTenantResp echoes the new tenant's name + uuid. UUID is empty
// when the daemon didn't acknowledge it (Unimplemented fallback) ;
// callers treat it as cosmetic.
type CreateTenantResp struct {
	Name string `json:"name"`
	UUID string `json:"uuid,omitempty"`
}
type createTenantOutput struct{ Body CreateTenantResp }

// EmailResp is the minimal acknowledgement for endpoints that just
// confirm a membership grant — add-tenant-admin and similar.
type EmailResp struct {
	Email string `json:"email"`
}
type emailOutput struct{ Body EmailResp }

// ---- Tenant-admin (delegated) ------------------------------------

func mountTenantsDelegatedAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID:   "add-tenant-project",
		Method:        "POST",
		Path:          "/api/tenants/{name}/projects",
		Summary:       "Add a project to a tenant (tenant admin)",
		Tags:          []string{"tenants", "projects"},
		DefaultStatus: 201,
	}, func(ctx context.Context, in *addTenantProjectInput) (*tenantProjectOutput, error) {
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isTenantAdmin(u, in.Name) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		var liveUUID string
		if live != nil {
			uuid, err := live.CreateProject(ctx, in.Body.Name)
			if err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
			liveUUID = uuid
		}
		p, err := tenantsDB.addProject(in.Name, in.Body.Name)
		if err != nil {
			return nil, hideHTTPErr(err)
		}
		if liveUUID != "" {
			tenantsDB.setProjectUUID(p.Name, liveUUID)
			p.UUID = liveUUID
		}
		return &tenantProjectOutput{Body: TenantProjectResp{
			Name: p.Name, UUID: p.UUID, Tenant: p.Tenant, Created: p.Created,
		}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "add-tenant-member",
		Method:      "POST",
		Path:        "/api/tenants/{name}/members",
		Summary:     "Add a member to a tenant (tenant admin)",
		Tags:        []string{"tenants"},
	}, func(ctx context.Context, in *addTenantMemberInput) (*memberOutput, error) {
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isTenantAdmin(u, in.Name) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if live != nil {
			if uuid := liveLookupTenantUUID(ctx, in.Name); uuid != "" {
				if err := live.AddTenantMember(ctx, uuid, in.Body.Email, in.Body.Groups); err == nil {
					return &memberOutput{Body: MemberResp{Email: in.Body.Email, Groups: in.Body.Groups}}, nil
				} else if !wclient.IsUnimplemented(err) {
					return nil, huma.Error502BadGateway("live: " + err.Error())
				}
			}
		}
		if err := tenantsDB.addMember(in.Name, in.Body.Email, in.Body.Groups); err != nil {
			return nil, hideHTTPErr(err)
		}
		return &memberOutput{Body: MemberResp{Email: in.Body.Email, Groups: in.Body.Groups}}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "grant-project-role",
		Method:      "POST",
		Path:        "/api/projects/{name}/roles",
		Summary:     "Grant a per-project role to a user (tenant admin)",
		Description: "The role string is webui-side metadata for now (weft-agent has membership but no per-project role enum yet) ; we mirror it on the store and also call AddProjectMember so weft-agent sees the membership.",
		Tags:        []string{"projects"},
	}, func(ctx context.Context, in *grantProjectRoleInput) (*roleOutput, error) {
		u := auth.UserFromContext(ctx)
		tenant, ok := tenantsDB.projectTenant(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		if !tenantsDB.isTenantAdmin(u, tenant) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if live != nil {
			projUUID, err := live.ProjectUUIDByName(ctx, in.Name)
			if err != nil {
				return nil, huma.Error502BadGateway("live: lookup project: " + err.Error())
			}
			if projUUID == "" {
				return nil, huma.Error404NotFound("project (not in weft-agent)")
			}
			userUUID, err := live.UserUUIDByEmail(ctx, in.Body.Email)
			if err != nil {
				return nil, huma.Error502BadGateway("live: lookup user: " + err.Error())
			}
			if userUUID == "" {
				return nil, huma.Error400BadRequest("user not found in weft-agent: " + in.Body.Email)
			}
			if err := live.AddProjectMember(ctx, projUUID, userUUID); err != nil {
				return nil, huma.Error502BadGateway("live: " + err.Error())
			}
		}
		if err := tenantsDB.grantRole(in.Name, in.Body.Email, in.Body.Role); err != nil {
			return nil, hideHTTPErr(err)
		}
		return &roleOutput{Body: RoleResp{Email: in.Body.Email, Role: in.Body.Role}}, nil
	})
}

// TenantProjectResp is what add-tenant-project echoes : the new
// project's name + UUID (from weft-agent if live, mock store
// otherwise) + the tenant it lives under + its creation timestamp.
type TenantProjectResp struct {
	Name    string `json:"name"`
	UUID    string `json:"uuid"`
	Tenant  string `json:"tenant"`
	Created string `json:"created"`
}
type tenantProjectOutput struct{ Body TenantProjectResp }

// MemberResp is the typed shape add-tenant-member returns : the
// email that was just granted access + the groups it joined.
type MemberResp struct {
	Email  string   `json:"email"`
	Groups []string `json:"groups"`
}
type memberOutput struct{ Body MemberResp }

// RoleResp is the per-project role grant acknowledgement.
type RoleResp struct {
	Email string `json:"email"`
	Role  string `json:"role"`
}
type roleOutput struct{ Body RoleResp }

// ---- Read (detail + quotas) --------------------------------------

func mountTenantsReadAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "tenant-detail",
		Method:      "GET",
		Path:        "/api/tenants/{name}",
		Summary:     "Tenant detail (projects + members + groups + roles)",
		Description: "Non-members get 404 (don't-acknowledge). The response annotates the caller's effective role so the SPA knows which affordances to render — saves a round-trip.",
		Tags:        []string{"tenants"},
	}, func(ctx context.Context, in *tenantNameInput) (*tenantDetailOutput, error) {
		u := auth.UserFromContext(ctx)
		detail, ok := tenantsDB.tenantDetail(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("tenant not found")
		}
		if !isClusterAdmin(u) && !tenantsDB.isMember(u, in.Name) {
			return nil, huma.Error404NotFound("tenant not found")
		}
		detail.Caller = &TenantCaller{
			Email:        emailOf(u),
			ClusterAdmin: isClusterAdmin(u),
			TenantAdmin:  tenantsDB.isTenantAdmin(u, in.Name),
		}
		return &tenantDetailOutput{Body: detail}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tenant-quota",
		Method:      "GET",
		Path:        "/api/tenants/{name}/quota",
		Summary:     "Get a tenant's quota view (cap + allocated + remaining)",
		Tags:        []string{"tenants", "quotas"},
	}, func(ctx context.Context, in *tenantNameInput) (*tenantQuotaOutput, error) {
		u := auth.UserFromContext(ctx)
		if !tenantsDB.isMember(u, in.Name) {
			return nil, huma.Error404NotFound("tenant not found")
		}
		v, ok := tenantsDB.tenantQuotaView(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("tenant not found")
		}
		return &tenantQuotaOutput{Body: v}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-tenant-quota",
		Method:      "PUT",
		Path:        "/api/tenants/{name}/quota",
		Summary:     "Set a tenant's quota (cluster admin)",
		Description: "Rejects a cap below current allocation so tenant admins don't end up with negative remaining.",
		Tags:        []string{"tenants", "quotas"},
	}, func(ctx context.Context, in *setTenantQuotaInput) (*tenantQuotaOutput, error) {
		u := auth.UserFromContext(ctx)
		if !isClusterAdmin(u) {
			return nil, huma.Error403Forbidden("cluster admin required")
		}
		if live != nil {
			if uuid := liveLookupTenantUUID(ctx, in.Name); uuid != "" {
				if err := live.SetTenantQuota(ctx, uuid, quotasMap(in.Body)); err == nil {
					cap, alloc, _ := live.GetTenantQuota(ctx, uuid)
					return &tenantQuotaOutput{Body: TenantQuotaView{
						Cap:       quotasFromMap(cap),
						Allocated: quotasFromMap(alloc),
						Remaining: remainingMapFromMaps(cap, alloc),
					}}, nil
				} else if !wclient.IsUnimplemented(err) {
					return nil, huma.Error502BadGateway("live: " + err.Error())
				}
			}
		}
		if err := tenantsDB.setTenantQuota(in.Name, in.Body); err != nil {
			return nil, hideHTTPErr(err)
		}
		v, _ := tenantsDB.tenantQuotaView(in.Name)
		return &tenantQuotaOutput{Body: v}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-tenant-usage",
		Method:      "GET",
		Path:        "/api/tenants/{name}/usage",
		Summary:     "Live usage roll-up for one tenant (VMs / CPU cores / storage GiB)",
		Description: "Aggregates rows across the microvm + volume registries, keyed on the tenant a project belongs to. Quotas live on the tenant ; usage is computed here so the dashboard renders bars against the cap without a second round-trip. Non-members get 404 (don't-acknowledge).",
		Tags:        []string{"tenants", "quotas"},
	}, func(ctx context.Context, in *tenantNameInput) (*tenantUsageOutput, error) {
		u := auth.UserFromContext(ctx)
		if !isClusterAdmin(u) && !tenantsDB.isMember(u, in.Name) {
			return nil, huma.Error404NotFound("tenant not found")
		}
		// Build a set of projects belonging to this tenant for O(rows)
		// lookup. projectsInTenant gives names ; rows tag themselves
		// with `project` so the join is name == name.
		projects := tenantsDB.projectsInTenant(in.Name)
		if projects == nil {
			return nil, huma.Error404NotFound("tenant not found")
		}
		inTenant := make(map[string]struct{}, len(projects))
		for _, p := range projects {
			inTenant[p] = struct{}{}
		}
		usage := TenantUsageView{Tenant: in.Name}
		if res, ok := resourceByID["microvms"]; ok {
			for _, row := range res.Rows {
				if _, ok := inTenant[str(row["project"])]; !ok {
					continue
				}
				usage.VMs++
				usage.CPUCores += toInt(row["cpu"])
				usage.RAMGiB += toInt(row["mem_mb"]) / 1024
				usage.StorageGiB += toInt(row["disk_gb"])
			}
		}
		if res, ok := resourceByID["volumes"]; ok {
			for _, row := range res.Rows {
				if _, ok := inTenant[str(row["project"])]; !ok {
					continue
				}
				usage.Volumes++
				usage.StorageGiB += toInt(row["size_gib"])
			}
		}
		// Surface the cap alongside the usage so the SPA can colour-code
		// the bars without two round-trips. Cap=zeroed when the tenant
		// has no quota set (quotas aren't enforced yet — the row still
		// renders, just without the comparison).
		if v, ok := tenantsDB.tenantQuotaView(in.Name); ok {
			usage.Cap = &v.Cap
		}
		return &tenantUsageOutput{Body: usage}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "get-project-quota",
		Method:      "GET",
		Path:        "/api/projects/{name}/quota",
		Summary:     "Get a project's quota view",
		Tags:        []string{"projects", "quotas"},
	}, func(ctx context.Context, in *projectNameInput) (*projectQuotaOutput, error) {
		u := auth.UserFromContext(ctx)
		tenant, ok := tenantsDB.projectTenant(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		if !tenantsDB.isMember(u, tenant) {
			return nil, huma.Error404NotFound("project not found")
		}
		v, ok := tenantsDB.projectQuotaView(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		return &projectQuotaOutput{Body: v}, nil
	})

	huma.Register(api, huma.Operation{
		OperationID: "set-project-quota",
		Method:      "PUT",
		Path:        "/api/projects/{name}/quota",
		Summary:     "Set a project's quota (tenant admin)",
		Description: "Validated against the tenant cap : sum(other projects) + new ≤ cap.",
		Tags:        []string{"projects", "quotas"},
	}, func(ctx context.Context, in *setProjectQuotaInput) (*projectQuotaOutput, error) {
		u := auth.UserFromContext(ctx)
		tenant, ok := tenantsDB.projectTenant(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("project not found")
		}
		if !tenantsDB.isTenantAdmin(u, tenant) {
			return nil, huma.Error403Forbidden("tenant admin required")
		}
		if live != nil {
			uuid, lerr := live.ProjectUUIDByName(ctx, in.Name)
			if lerr == nil && uuid != "" {
				if err := live.SetProjectQuota(ctx, uuid, quotasMap(in.Body)); err == nil {
					v, _ := tenantsDB.projectQuotaView(in.Name)
					return &projectQuotaOutput{Body: v}, nil
				} else if !wclient.IsUnimplemented(err) {
					return nil, huma.Error502BadGateway("live: " + err.Error())
				}
			}
		}
		if err := tenantsDB.setProjectQuota(in.Name, in.Body); err != nil {
			return nil, hideHTTPErr(err)
		}
		v, _ := tenantsDB.projectQuotaView(in.Name)
		return &projectQuotaOutput{Body: v}, nil
	})
}

// tenantDetailOutput / tenantQuotaOutput / projectQuotaOutput surface
// the typed views from tenants.go in the OpenAPI.
type tenantDetailOutput struct{ Body TenantDetail }
type tenantQuotaOutput  struct{ Body TenantQuotaView }
type projectQuotaOutput struct{ Body ProjectQuotaView }

// TenantUsageView is the live roll-up returned by /api/tenants/{name}/usage.
// Cap is a pointer so the SPA can tell "no quota set yet" (nil) apart from
// "explicit zero cap" (a Quotas with every field == 0).
type TenantUsageView struct {
	Tenant     string  `json:"tenant"`
	VMs        int     `json:"vms"          doc:"Total microVMs registered against the tenant"`
	CPUCores   int     `json:"cpu_cores"    doc:"Sum of vCPU cores allocated across the tenant's VMs"`
	RAMGiB     int     `json:"ram_gib"      doc:"Sum of RAM GiB allocated across the tenant's VMs"`
	Volumes    int     `json:"volumes"      doc:"Total volumes registered against the tenant"`
	StorageGiB int     `json:"storage_gib"  doc:"Sum of root-disk + volume GiB"`
	Cap        *Quotas `json:"cap,omitempty" doc:"Tenant quota cap (omitted when no quota is set)"`
}

type tenantUsageOutput struct{ Body TenantUsageView }

// ---- /api/quotas (overview scope-aware) --------------------------

func mountQuotasAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-quotas",
		Method:      "GET",
		Path:        "/api/quotas",
		Summary:     "Scope-aware quota overview",
		Description: "When the session carries a tenant scope, returns the tenant's quotas mapped to the 12 cluster-overview dimensions. Otherwise falls back to the static cluster-wide demo numbers.",
		Tags:        []string{"quotas"},
	}, func(ctx context.Context, in *quotasInput) (*quotasOutput, error) {
		tenant := in.Tenant
		if tenant == "" {
			if u := auth.UserFromContext(ctx); u != nil {
				tenant = u.Tenant
			}
		}
		if tenant != "" {
			if view, ok := tenantsDB.tenantQuotaView(tenant); ok {
				out := make([]Quota, 0, len(tenantQuotaDims))
				for _, d := range tenantQuotaDims {
					out = append(out, Quota{
						ID: d.ID, Label: d.Label, Icon: d.Icon, Unit: d.Unit,
						Used: d.Get(view.Allocated), Limit: d.Get(view.Cap),
					})
				}
				return &quotasOutput{Body: out}, nil
			}
		}
		return &quotasOutput{Body: quotas}, nil
	})
}

// quotasOutput is the typed body for /api/quotas — Quota is defined
// in quotas.go (the cluster-wide overview row).
type quotasOutput struct {
	Body []Quota
}

// ---- /api/me -----------------------------------------------------

func mountMeAPI(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "me",
		Method:      "GET",
		Path:        "/api/me",
		Summary:     "Current session : user + role flags + reachable scopes",
		Description: "Returns 401 with a login URL when there's no session so the SPA's api.ts can trigger the OIDC redirect uniformly.",
		Tags:        []string{"session"},
	}, func(ctx context.Context, _ *struct{}) (*meOutput, error) {
		u := auth.UserFromContext(ctx)
		if u == nil {
			return nil, huma.NewError(http.StatusUnauthorized, "no session", &meLoginHint{Login: "/api/auth/login"})
		}
		return &meOutput{Body: MeBody{
			Sub:          u.Subject,
			Email:        u.Email,
			Name:         u.Name,
			Groups:       u.Groups,
			Tenant:       u.Tenant,
			Project:      u.Project,
			Initials:     u.Initials(),
			Dev:          u.DevMode,
			ClusterAdmin: isClusterAdmin(u),
			TenantAdmin:  tenantsDB.isAnyTenantAdmin(u.Email),
			Scopes:       tenantsDB.userScopes(u),
		}}, nil
	})
}

// MeBody is the typed shape of /api/me. Exported so the openapi-
// typescript codegen surfaces it as a real schema instead of `unknown`.
type MeBody struct {
	Sub          string       `json:"sub" doc:"OIDC subject"`
	Email        string       `json:"email"`
	Name         string       `json:"name"`
	Groups       []string     `json:"groups" doc:"OIDC group claims"`
	Tenant       string       `json:"tenant" doc:"Currently scoped tenant (empty = all)"`
	Project      string       `json:"project" doc:"Currently scoped project"`
	Initials     string       `json:"initials" doc:"1–2 char avatar label"`
	Dev          bool         `json:"dev" doc:"True when running in dev mode (synthetic session)"`
	ClusterAdmin bool         `json:"cluster_admin"`
	TenantAdmin  bool         `json:"tenant_admin" doc:"True when the user is admin of at least one tenant"`
	Scopes       []ScopeEntry `json:"scopes" doc:"Tenants reachable by the user, each with its projects"`
}

type meOutput struct {
	Body MeBody
}

// meLoginHint surfaces the login URL on the 401 response so the SPA
// can redirect without a separate code path.
type meLoginHint struct {
	Login string `json:"login"`
}

func (m *meLoginHint) Error() string { return "no session" }

// liveLookupTenantUUID resolves a tenant name to UUID via live. Returns
// "" when not found / live unwired. Used to bridge name-keyed routes to
// the UUID-keyed live RPCs.
func liveLookupTenantUUID(ctx context.Context, name string) string {
	if live == nil {
		return ""
	}
	rows, err := live.ListTenants(ctx)
	if err != nil {
		return ""
	}
	for _, t := range rows {
		if t["name"] == name {
			uuid, _ := t["uuid"].(string)
			return uuid
		}
	}
	return ""
}

// ---- inputs ------------------------------------------------------

type createTenantInput struct {
	Body struct {
		Name   string `json:"name" minLength:"1" maxLength:"128"`
		Domain string `json:"domain,omitempty"`
	}
}

type tenantNameInput struct {
	Name string `path:"name" doc:"Tenant name" minLength:"1" maxLength:"128"`
}

type projectNameInput struct {
	Name string `path:"name" doc:"Project name" minLength:"1" maxLength:"128"`
}

type addTenantAdminInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body struct {
		Email string `json:"email" minLength:"1"`
	}
}

type addTenantProjectInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body struct {
		Name string `json:"name" minLength:"1" maxLength:"128"`
	}
}

type addTenantMemberInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body struct {
		Email  string   `json:"email" minLength:"1"`
		Groups []string `json:"groups,omitempty"`
	}
}

type grantProjectRoleInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body struct {
		Email string `json:"email" minLength:"1"`
		Role  string `json:"role" minLength:"1"`
	}
}

type setTenantQuotaInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body Quotas
}

type setProjectQuotaInput struct {
	Name string `path:"name" minLength:"1" maxLength:"128"`
	Body Quotas
}

type quotasInput struct {
	Tenant string `query:"tenant" doc:"Override the session tenant"`
}
