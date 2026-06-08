// Thin client for the weft-webui JSON API. Wires every call through
// the typed `client` from ./client.ts, which is itself backed by the
// openapi-typescript types generated from the huma surface.
//
// Public surface is unchanged from the hand-rolled version : same
// names, same call signatures, same backwards-compatible return types.
// What changed under the hood :
//
//   * One source of truth for every wire shape (web/openapi.json,
//     dumped from internal/server/api_*.go).
//   * Bad path / wrong body / missing field → compile error.
//   * 401 → OIDC redirect handled once via middleware, not per-helper.
//   * Errors carry RFC 7807 fields (detail / title / status), unboxed
//     into a plain message string at the helper boundary.
//
// The hand-rolled types (Quota, Me, TenantDetail, …) are kept for
// callers that still import them — narrowing them to the generated
// `components['schemas']['…']` is a follow-up that doesn't change
// call sites.

import {
  client,
  type APIFlavor, type APIScript, type APISSHKey,
  type MeBody, type APIQuota, type APIScopeEntry,
  type APITopoNetwork, type APITopoNode, type APITopologyBody,
  type APIVMInfo, type APIVMTimingEvent, type APIVMLogsResult, type APIMetricsSnapshot,
  type APISecurityRule, type APIImportResult,
  type APITenantDetail, type APITenantMember, type APITenantProjectEntry,
  type APITenantGroup, type APITenantQuotaView, type APIProjectQuotaView,
  type APITenantUsageView,
  type APIQuotas,
  type APIObjectEntry, type APIObjectListing, type APIObjectDetail,
  type APIBucketPolicy, type APIPolicyStatement,
  type APIRegistryRemote, type APIRemoteSearchHit,
  type APIPlugin,
  type APIVolumeMetadata, type APIVolumeProperty,
  type APINetworkMetadata,
  type APIEditableMetadata,
  type APISubnet,
  type APIAuthorizedGroup, type APIEffectiveKey,
  type APIDiagnosis, type APIDiagnosisExample,
  type paths,
} from './client';

// Re-export the typed aliases for callers that want them.
export type { APIFlavor, APIScript, APISSHKey };

// ---- helpers ------------------------------------------------------

// toMsg unboxes huma's RFC 7807 error envelope to a plain string.
// `detail` is the operator-facing field ; `title` falls back when
// the server didn't provide a detail.
function toMsg(e: unknown): string {
  if (e && typeof e === 'object') {
    const o = e as { detail?: string; title?: string; error?: string };
    return o.detail || o.title || o.error || JSON.stringify(e);
  }
  return String(e);
}

// throwErr is the common tail of every helper : if openapi-fetch
// surfaced an error envelope, raise it ; otherwise the call is fine.
function throwErr(error: unknown): never {
  throw new Error(toMsg(error));
}

// ---- /api/resources catalogue listing -----------------------------

export interface Column { key: string; label: string }

export interface ResourceMeta {
  id: string;
  label: string;
  section: string;
  columns: Column[];
  count: number;
}

export type Row = Record<string, unknown>;

export const getResources = async (): Promise<ResourceMeta[]> => {
  const { data, error } = await client.GET('/api/resources');
  if (error) throwErr(error);
  return (data ?? []) as ResourceMeta[];
};

// ---- /api/resources/{id} paginated rows ---------------------------

export interface Page<T> {
  rows: T[];
  next: string;
  total: number;
}

export interface PageOpts {
  limit?: number;
  pageToken?: string;
}

export async function getRowsPage(id: string, opts: PageOpts = {}): Promise<Page<Row>> {
  const { data, error } = await client.GET('/api/resources/{id}', {
    params: { path: { id }, query: { limit: opts.limit, page_token: opts.pageToken } },
  });
  if (error) throwErr(error);
  // /api/resources/{id} is the polymorphic dispatcher ; huma types
  // its body as `unknown`. The wire shape is always {rows, next, total}.
  return data as unknown as Page<Row>;
}

// Convenience wrapper for callers that only want the rows.
export async function getRows(id: string): Promise<Row[]> {
  const p = await getRowsPage(id, { limit: 1000 });
  return p.rows;
}

// getAllRows walks the next-page-token chain until exhausted. Use it
// for views that need the FULL list (the inventory tree / map join VMs
// against hosts ; a single 1000-cap fetch isn't enough for large
// fleets). Caps total iterations at `maxPages` (default 50 = 50 k rows
// at 1 k per page) so a misconfigured server can't pin the dashboard.
export async function getAllRows(
  id: string,
  opts: { perPage?: number; maxPages?: number } = {},
): Promise<Row[]> {
  const perPage = opts.perPage ?? 1000;
  const maxPages = opts.maxPages ?? 50;
  const out: Row[] = [];
  let token: string | undefined;
  for (let i = 0; i < maxPages; i++) {
    const page = await getRowsPage(id, { limit: perPage, pageToken: token });
    if (page.rows && page.rows.length > 0) out.push(...page.rows);
    if (!page.next) return out;
    token = page.next;
  }
  // Hit the safety bound. Better to surface partial results than to
  // hang the dashboard ; callers can detect this case by comparing
  // out.length against the page.total they got earlier.
  return out;
}

// ---- Inventory CRUD (AZ / Rack / Host) ----------------------------
//
// Thin wrappers around the huma CRUD endpoints. Bodies follow the
// generated openapi shapes — no client-side validation duplicated
// since the server's enum / minLength / maxLength tags already run
// at the huma layer.

export type AZBody    = paths['/api/azs']['post']['requestBody']['content']['application/json'];
export type RackBody  = paths['/api/racks']['post']['requestBody']['content']['application/json'];
export type HostBody  = paths['/api/hosts']['post']['requestBody']['content']['application/json'];

export const createAZ = async (body: AZBody): Promise<AZBody> => {
  const { data, error } = await client.POST('/api/azs', { body });
  if (error) throwErr(error);
  return data;
};
export const updateAZ = async (uuid: string, body: AZBody): Promise<AZBody> => {
  const { data, error } = await client.PUT('/api/azs/{uuid}', {
    params: { path: { uuid } },
    body,
  });
  if (error) throwErr(error);
  return data;
};
export const deleteAZ = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/azs/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export const createRack = async (body: RackBody): Promise<RackBody> => {
  const { data, error } = await client.POST('/api/racks', { body });
  if (error) throwErr(error);
  return data;
};
export const updateRack = async (uuid: string, body: RackBody): Promise<RackBody> => {
  const { data, error } = await client.PUT('/api/racks/{uuid}', {
    params: { path: { uuid } },
    body,
  });
  if (error) throwErr(error);
  return data;
};
export const deleteRack = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/racks/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export const createHost = async (body: HostBody): Promise<HostBody> => {
  const { data, error } = await client.POST('/api/hosts', { body });
  if (error) throwErr(error);
  return data;
};
export const updateHost = async (uuid: string, body: HostBody): Promise<HostBody> => {
  const { data, error } = await client.PUT('/api/hosts/{uuid}', {
    params: { path: { uuid } },
    body,
  });
  if (error) throwErr(error);
  return data;
};
export const deleteHost = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/hosts/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

// ---- Audit log read ------------------------------------------------

export type AuditEvent =
  paths['/api/audit-log']['get']['responses']['200']['content']['application/json']['events'] extends (infer T)[] | null
    ? T
    : never;

export interface AuditTailOpts {
  limit?: number;
  action?: string;
  result?: '' | 'ok' | 'error';
}

export const tailAuditLog = async (
  opts: AuditTailOpts = {},
): Promise<{ enabled: boolean; events: AuditEvent[] }> => {
  const { data, error } = await client.GET('/api/audit-log', {
    params: {
      query: {
        limit: opts.limit,
        action: opts.action,
        result: opts.result,
      },
    },
  });
  if (error) throwErr(error);
  return {
    enabled: data.enabled,
    events: (data.events ?? []) as AuditEvent[],
  };
};

// ---- Scripts catalogue --------------------------------------------

// Backwards-compatible alias for callers that still import `Script`.
export type Script = APIScript;

export const listScripts = async (): Promise<Script[]> => {
  const { data, error } = await client.GET('/api/scripts');
  if (error) throwErr(error);
  return data ?? [];
};
export const getScript = async (name: string): Promise<Script> => {
  const { data, error } = await client.GET('/api/scripts/{name}', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};
export const setScript = async (s: { name: string; description: string; body: string }): Promise<Script> => {
  const { data, error } = await client.POST('/api/scripts', {
    body: { ...s, updated_at: '', updated_by: '' },
  });
  if (error) throwErr(error);
  return data;
};
export const deleteScript = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/scripts/{name}', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
};

// ---- Flavors catalogue --------------------------------------------

// Flavor catalogue is exposed on both listeners (CreateVMModal picker).
// The wire shape is {flavors: APIFlavor[]} ; the helper unwraps the
// envelope so callers see a plain array.
export const getFlavors = async (): Promise<APIFlavor[]> => {
  const { data, error } = await client.GET('/api/flavors');
  if (error) throwErr(error);
  return data?.flavors ?? [];
};

// ---- /api/summary scope-aware counts ------------------------------

export const getSummary = async (): Promise<{ id: string; label: string; count: number }[]> => {
  const { data, error } = await client.GET('/api/summary');
  if (error) throwErr(error);
  return data ?? [];
};

// ---- /api/quotas overview -----------------------------------------

// Quota is the typed body of /api/quotas — sourced from api.gen.ts.
// Legacy callers can keep using the local-name `Quota` ; it's now an
// alias over the generated shape.
export type Quota = APIQuota;

export const getQuotas = async (): Promise<Quota[]> => {
  const { data, error } = await client.GET('/api/quotas');
  if (error) throwErr(error);
  return data ?? [];
};

// ---- /api/monitors cross-host respawn HA topology ----------------
//
// One row per healthy weft-agent monitor (one per host). The webui
// reads /weft/coord/hosts/<host_uuid> from etcd and surfaces the set
// here. count vs expected_count drives the badge color on the panel.
//
// expected_count = 0 when the etcd source is offline and no static
// override was pinned ; the panel renders that as "baseline unknown".

export interface MonitorHost {
  host_uuid: string;
  hostname: string;
  hypervisor: string;
  version: string;
  started_at: string;
}

export interface MonitorsSnapshot {
  monitors: MonitorHost[];
  count: number;
  expected_count: number;
}

export const listMonitors = async (): Promise<MonitorsSnapshot> => {
  const { data, error } = await client.GET('/api/monitors');
  if (error) throwErr(error);
  return {
    monitors: (data.monitors ?? []) as MonitorHost[],
    count: data.count ?? 0,
    expected_count: data.expected_count ?? 0,
  };
};

// ---- /api/registry/upload (multipart) -----------------------------

// uploadArtifact uses FormData directly because openapi-fetch's
// multipart handling expects the same shape ; we just hand it through.
export async function uploadArtifact(form: FormData): Promise<Row> {
  // openapi-fetch handles multipart via { body: ..., bodySerializer }
  // but it's simpler to use raw fetch here ; the server type-checks
  // the multipart field schema, so a wrong field name still throws
  // at runtime with a clear 400.
  const res = await fetch('/api/registry/upload', { method: 'POST', body: form });
  if (res.status === 401) handleUnauthorised();
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { detail?: string; error?: string }).detail ?? (body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return body as Row;
}

// handleUnauthorised duplicates the middleware behaviour for the
// raw-fetch paths (uploadArtifact, uploadEntries, deleteEntry,
// createBucket, deleteBucket, setScope, logout). The middleware can't
// cover these because they don't go through `client.GET/POST/...`.
function handleUnauthorised(): never {
  const back = encodeURIComponent(location.pathname + location.search + location.hash);
  location.assign(`/api/auth/login?return_to=${back}`);
  throw new Error('unauthenticated');
}

// ---- File storage (buckets + shares) ------------------------------

export type StorageKind = 'buckets' | 'shares';

// Types now come from api.gen.ts. Coerce nullable arrays at the
// helper boundary so the SPA never has to ternary-guard.
export type ObjectEntry = APIObjectEntry;
export type ObjectListing = Omit<APIObjectListing, 'folders' | 'objects'> & {
  folders: string[];
  objects: ObjectEntry[];
};
export type ObjectDetail = APIObjectDetail;

const coerceListing = (data: APIObjectListing): ObjectListing => ({
  ...data,
  folders: data.folders ?? [],
  objects: data.objects ?? [],
});

export const browse = async (kind: StorageKind, container: string, prefix = ''): Promise<ObjectListing> => {
  if (kind === 'buckets') {
    const { data, error } = await client.GET('/api/buckets/{name}/objects', {
      params: { path: { name: container }, query: { prefix } },
    });
    if (error) throwErr(error);
    return coerceListing(data);
  }
  const { data, error } = await client.GET('/api/shares/{name}/objects', {
    params: { path: { name: container }, query: { prefix } },
  });
  if (error) throwErr(error);
  return coerceListing(data);
};

export const readEntry = async (kind: StorageKind, container: string, key: string): Promise<ObjectDetail> => {
  if (kind === 'buckets') {
    const { data, error } = await client.GET('/api/buckets/{name}/object', {
      params: { path: { name: container }, query: { key } },
    });
    if (error) throwErr(error);
    return data;
  }
  const { data, error } = await client.GET('/api/shares/{name}/object', {
    params: { path: { name: container }, query: { key } },
  });
  if (error) throwErr(error);
  return data;
};

export async function uploadEntries(kind: StorageKind, container: string, form: FormData): Promise<Row> {
  // Multipart — raw fetch ; the route is typed end-to-end on the Go
  // side via huma.MultipartFormFiles.
  const res = await fetch(`/api/${kind}/${container}/objects`, { method: 'POST', body: form });
  if (res.status === 401) handleUnauthorised();
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { detail?: string }).detail ?? `${res.status} ${res.statusText}`);
  return body as Row;
}

export async function deleteEntry(kind: StorageKind, _container: string, key: string): Promise<void> {
  // Only wired for buckets today ; shares have no DELETE route.
  if (kind !== 'buckets') {
    throw new Error('shares have no DELETE — files come and go via the workload itself');
  }
  const { error } = await client.DELETE('/api/buckets/{name}/object', {
    params: { path: { name: _container }, query: { key } },
  });
  if (error) throwErr(error);
}

// ---- Buckets (lifecycle + policy) ---------------------------------

// CreateBucketBody surfaces the live-first wiring fields the
// daemon's CreateBucket RPC needs (endpoint / region / access keys /
// optional initial policy). Legacy mock-mode callers can still pass
// `{ name }` only — the server falls back to the in-memory store
// when the live agent is unwired or returns Unimplemented.
export type CreateBucketBody = {
  name: string;
  endpoint?: string;
  region?: string;
  access_key_id?: string;
  secret_access_key?: string;
  policy?: string;
};

export async function createBucket(body: CreateBucketBody): Promise<void> {
  const { error } = await client.POST('/api/buckets', { body });
  if (error) throwErr(error);
}

export async function deleteBucket(name: string): Promise<void> {
  const { error } = await client.DELETE('/api/buckets/{name}', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
}

// Types from api.gen.ts. Effect / Action carry their narrow enums
// because the Go PolicyStatement struct has 'enum:' tags (server-
// side validation against the closed vocabulary).
export type PolicyEffect = PolicyStatement['effect'];
export type PolicyAction = PolicyStatement['action'];
export type PolicyStatement = APIPolicyStatement;
export type BucketPolicy = Omit<APIBucketPolicy, 'statements'> & { statements: PolicyStatement[] };

export const getBucketPolicy = async (name: string): Promise<BucketPolicy> => {
  const { data, error } = await client.GET('/api/buckets/{name}/policy', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return { ...data, statements: data.statements ?? [] };
};

export async function setBucketPolicy(name: string, p: BucketPolicy): Promise<BucketPolicy> {
  const { data, error } = await client.PUT('/api/buckets/{name}/policy', {
    params: { path: { name } },
    body: p,
  });
  if (error) throwErr(error);
  return { ...data, statements: data.statements ?? [] };
}

// ---- Network topology ---------------------------------------------

// TopoNetwork / TopoNode / topology body shape now come from the
// generated client. Legacy aliases preserved for existing callers.
export type TopoNetwork = APITopoNetwork;
export type TopoNode = APITopoNode;

export const getTopology = async (): Promise<{ networks: TopoNetwork[]; nodes: TopoNode[] }> => {
  const { data, error } = await client.GET('/api/network-topology');
  if (error) throwErr(error);
  return {
    networks: data.networks ?? [],
    nodes: data.nodes ?? [],
  };
};

// ---- Session (me / setScope / logout) -----------------------------

// Me + ScopeEntry come from the generated client. The legacy names
// stay as aliases so the dozen consumer components don't have to be
// touched. We override the nullable-array fields with non-null
// versions because `getMe()` coerces nulls away at the boundary.
export type ScopeEntry = Omit<APIScopeEntry, 'projects'> & { projects: string[] };
export type Me = Omit<MeBody, 'scopes'> & { scopes: ScopeEntry[] };

export const getMe = async (): Promise<Me> => {
  const { data, error } = await client.GET('/api/me');
  if (error) throwErr(error);
  // openapi-typescript types nullable arrays as `T[] | null` because
  // OpenAPI doesn't forbid null serialisation. The Go side always
  // emits `[]`, but normalise anyway so callers don't have to.
  return {
    ...data,
    scopes: (data.scopes ?? []).map((s) => ({ ...s, projects: s.projects ?? [] })),
  };
};

// onAdminUI : "did this listener register the admin-only resources?"
// The user listener doesn't surface `hosts` in /api/resources ; we
// check that to distinguish.
export async function onAdminUI(): Promise<boolean> {
  try {
    const rs = await getResources();
    return rs.some((r) => r.id === 'hosts');
  } catch {
    return false;
  }
}

// /api/session/scope and /api/auth/logout stay on raw fetch — they're
// the only two routes still on the legacy mux (auth concern, not part
// of the huma JSON CRUD surface).
export async function setScope(tenant: string, project: string): Promise<void> {
  const res = await fetch('/api/session/scope', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ tenant, project }),
  });
  if (res.status === 401) handleUnauthorised();
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
}

export async function logout(): Promise<void> {
  await fetch('/api/auth/logout', { method: 'POST' }).catch(() => {});
  location.assign('/');
}

// ---- Tenant administration ----------------------------------------

// Types now come from api.gen.ts. The TS aliases override the
// generated nullable arrays + the optional caller field with non-
// null versions ; the API layer always initialises slices + always
// sets caller before returning, so callers don't need to guard.
export type TenantMember = Omit<APITenantMember, 'groups'> & { groups: string[] };
export type TenantProject = APITenantProjectEntry;
export type TenantGroup = APITenantGroup;
// Caller is always set by the API layer (tenant-detail handler in
// api_tenants.go) — the generated optional flag is overridden here.
export type TenantCaller = NonNullable<APITenantDetail['caller']>;
export type TenantDetail = Omit<APITenantDetail, 'projects' | 'members' | 'groups' | 'caller'> & {
  projects: TenantProject[];
  members: TenantMember[];
  groups: TenantGroup[];
  caller: TenantCaller;
};

export const getTenant = async (name: string): Promise<TenantDetail> => {
  const { data, error } = await client.GET('/api/tenants/{name}', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  // The Go side always initialises slices + sets `caller` before
  // returning ; we normalise nullable arrays here so consumers
  // don't have to ternary-guard. caller is asserted non-null since
  // the API handler always fills it post-fetch.
  return {
    ...data,
    projects: data.projects ?? [],
    members: (data.members ?? []).map((m) => ({ ...m, groups: m.groups ?? [] })),
    groups: data.groups ?? [],
    caller: data.caller!,
  };
};

export const createTenant = async (name: string, domain: string) => {
  const { data, error } = await client.POST('/api/tenants', {
    body: { name, domain },
  });
  if (error) throwErr(error);
  return data;
};

export const addTenantAdmin = async (tenant: string, email: string) => {
  const { data, error } = await client.POST('/api/tenants/{name}/admins', {
    params: { path: { name: tenant } },
    body: { email },
  });
  if (error) throwErr(error);
  return data;
};

export const addTenantProject = async (tenant: string, name: string) => {
  const { data, error } = await client.POST('/api/tenants/{name}/projects', {
    params: { path: { name: tenant } },
    body: { name },
  });
  if (error) throwErr(error);
  return data;
};

export const addTenantMember = async (tenant: string, email: string, groups: string[]) => {
  const { data, error } = await client.POST('/api/tenants/{name}/members', {
    params: { path: { name: tenant } },
    body: { email, groups },
  });
  if (error) throwErr(error);
  return data;
};

export const grantProjectRole = async (project: string, email: string, role: string) => {
  const { data, error } = await client.POST('/api/projects/{name}/roles', {
    params: { path: { name: project } },
    body: { email, role },
  });
  if (error) throwErr(error);
  return data;
};

// ---- Quotas (typed views) -----------------------------------------

// Strip $schema (an openapi-typescript convenience that bleeds into
// keyof Quotas and breaks Record<keyof Quotas, number> usages).
export type Quotas = Omit<APIQuotas, '$schema'>;
export interface QuotaDim { used: number; cap: number; free: number }
export type QuotaBars = Record<string, QuotaDim>;
export type TenantQuotaView = APITenantQuotaView;
export type ProjectQuotaView = APIProjectQuotaView;

export const getTenantQuota = async (name: string): Promise<TenantQuotaView> => {
  const { data, error } = await client.GET('/api/tenants/{name}/quota', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

// ---- Tenant usage (live roll-up) ----------------------------------

export type TenantUsageView = APITenantUsageView;

export const getTenantUsage = async (name: string): Promise<TenantUsageView> => {
  const { data, error } = await client.GET('/api/tenants/{name}/usage', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

export const getProjectQuota = async (name: string): Promise<ProjectQuotaView> => {
  const { data, error } = await client.GET('/api/projects/{name}/quota', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

export const setTenantQuota = async (name: string, q: Quotas): Promise<TenantQuotaView> => {
  const { data, error } = await client.PUT('/api/tenants/{name}/quota', {
    params: { path: { name } },
    body: q,
  });
  if (error) throwErr(error);
  return data;
};

export const setProjectQuota = async (name: string, q: Quotas): Promise<ProjectQuotaView> => {
  const { data, error } = await client.PUT('/api/projects/{name}/quota', {
    params: { path: { name } },
    body: q,
  });
  if (error) throwErr(error);
  return data;
};

// ---- VM lifecycle -------------------------------------------------

export const startVM = async (name: string) => {
  const { data, error } = await client.POST('/api/microvms/{name}/start', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

export const stopVM = async (name: string) => {
  const { data, error } = await client.POST('/api/microvms/{name}/stop', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

export const deleteVM = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/microvms/{name}', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
};

export type VMIngressKind = 'none' | 'floating_ip' | 'loadbalancer';
export type VMProvisioningSourceKind = 'none' | 'git' | 'oci';

export interface VMProvisioning {
  source_kind: VMProvisioningSourceKind;
  source_url: string;
  source_ref: string;
  script: string;
}

export interface CreateVMBody {
  name: string;
  image: string;
  flavor: string;
  scheduling_rule?: string;
  network?: string;
  ingress_kind?: VMIngressKind;
  ingress_floating_ip?: string;
  ingress_load_balancer?: string;
  provisioning?: VMProvisioning;
}

export const createVM = async (b: CreateVMBody) => {
  const { data, error } = await client.POST('/api/microvms', { body: b });
  if (error) throwErr(error);
  return data;
};

// ---- Volumes ------------------------------------------------------

export interface CreateVolumeBody {
  name: string;
  size_gib: number;
  format?: string;
}

export const createVolume = async (b: CreateVolumeBody) => {
  const { data, error } = await client.POST('/api/volumes', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteVolume = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/volumes/{uuid}', {
    params: { path: { uuid } },
  });
  if (error) throwErr(error);
};

export const attachVolume = async (uuid: string, vmUUID: string) => {
  const { data, error } = await client.POST('/api/volumes/{uuid}/attach', {
    params: { path: { uuid } },
    body: { vm_uuid: vmUUID },
  });
  if (error) throwErr(error);
  return data;
};

export const detachVolume = async (uuid: string) => {
  const { error } = await client.POST('/api/volumes/{uuid}/detach', {
    params: { path: { uuid } },
  });
  if (error) throwErr(error);
};

// ---- Volume snapshots ---------------------------------------------
//
// Snapshots are project-scoped point-in-time copies of a volume. The
// agent dispatches on the parent's backend : file-backed → reflink
// CoW clone ; block-backed (weft-block) → controller snapshot. The UI
// doesn't model the backend choice — at snapshot time the parent's
// row already encodes it.
//
// Revert is block-only ; file parents reject server-side with a 502
// bubbling the agent's "block-only" message. The drawer surfaces a
// disabled revert button when the parent's backend isn't block.

export type VolumeSnapshotRow = _genComponents['schemas']['VolumeSnapshotRow'];

export const listVolumeSnapshots = async (
  volumeUUID: string,
  project?: string,
): Promise<VolumeSnapshotRow[]> => {
  const { data, error } = await client.GET('/api/volumes/{uuid}/snapshots', {
    params: { path: { uuid: volumeUUID }, query: project ? { project } : {} },
  });
  if (error) throwErr(error);
  return data ?? [];
};

export const createVolumeSnapshot = async (
  volumeUUID: string,
  name: string,
  project?: string,
): Promise<VolumeSnapshotRow> => {
  const { data, error } = await client.POST('/api/volumes/{uuid}/snapshots', {
    params: { path: { uuid: volumeUUID }, query: project ? { project } : {} },
    body: { name },
  });
  if (error) throwErr(error);
  return data;
};

export const restoreVolumeSnapshot = async (
  snapshotUUID: string,
  newVolumeName: string,
  project?: string,
): Promise<{ snapshot_uuid: string; new_volume_name: string }> => {
  const { data, error } = await client.POST('/api/snapshots/{uuid}/restore', {
    params: { path: { uuid: snapshotUUID } },
    body: {
      new_volume_name: newVolumeName,
      ...(project ? { project } : {}),
    },
  });
  if (error) throwErr(error);
  return data;
};

// revertVolumeSnapshot rolls the parent BLOCK volume back to the
// snapshot's state. The volume should be detached first ; the agent
// enforces this at the driver layer and surfaces a clear error if
// not. File-backend parents reject — the UI gates the affordance
// based on the parent volume's `backend` column.
export const revertVolumeSnapshot = async (snapshotUUID: string): Promise<void> => {
  const { error } = await client.POST('/api/snapshots/{uuid}/revert', {
    params: { path: { uuid: snapshotUUID } },
  });
  if (error) throwErr(error);
};

export const deleteVolumeSnapshot = async (snapshotUUID: string): Promise<void> => {
  const { error } = await client.DELETE('/api/snapshots/{uuid}', {
    params: { path: { uuid: snapshotUUID } },
  });
  if (error) throwErr(error);
};

// ---- Volume backups (block-only) ----------------------------------
//
// Backups are off-host : the agent ships the snapshot's bytes to a
// target URL through weft-block. Targets the UI accepts :
//
//   oci://<registry>/<repo>:<tag>       — recommended (content-addressed)
//   s3://<bucket>@<region>/<prefix>     — versitygw / CubeFS objectnode
//   sftp://<user>@<host>:<port>/<path>  — sftpgo
//   fs:///<absolute_path>               — dev / tests only
//
// Encryption + incremental chains live entirely inside weft-block ;
// the UI never sees a passphrase. The operator sets
// WEFT_BACKUP_PASSPHRASE on the daemon and forgets about it from the
// dashboard's perspective.

export type VolumeBackupRow = _genComponents['schemas']['VolumeBackupRow'];

export const createVolumeBackup = async (
  snapshotUUID: string,
  target: string,
  project?: string,
): Promise<VolumeBackupRow> => {
  const { data, error } = await client.POST('/api/backups', {
    body: {
      snapshot_uuid: snapshotUUID,
      target,
      ...(project ? { project } : {}),
    },
  });
  if (error) throwErr(error);
  return data;
};

export const listVolumeBackups = async (
  target: string,
  volumeUUID?: string,
  project?: string,
): Promise<VolumeBackupRow[]> => {
  const { data, error } = await client.GET('/api/backups', {
    params: {
      query: {
        target,
        ...(volumeUUID ? { volume_uuid: volumeUUID } : {}),
        ...(project ? { project } : {}),
      },
    },
  });
  if (error) throwErr(error);
  return data ?? [];
};

export const deleteVolumeBackup = async (url: string): Promise<void> => {
  const { error } = await client.DELETE('/api/backups', {
    params: { query: { url } },
  });
  if (error) throwErr(error);
};

export const restoreVolumeBackup = async (
  url: string,
  newVolumeName: string,
  project: string,
): Promise<{ url: string; new_volume_name: string; project: string }> => {
  const { data, error } = await client.POST('/api/backups/restore', {
    body: { url, new_volume_name: newVolumeName, project },
  });
  if (error) throwErr(error);
  return data;
};

// ---- Network controller (routers / LBs / DNS) ---------------------

export interface CreateRouterBody {
  name: string;
  kind: 'peer' | 'egress';
  backend?: string;
  networks?: string[];
  external?: string;
}

export const createRouter = async (b: CreateRouterBody) => {
  const { data, error } = await client.POST('/api/routers', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteRouter = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/routers/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export interface CreateLoadBalancerBody {
  name: string;
  mode: 'L4' | 'L7';
  port: number;
  backends?: string[];
  az?: string;
}

export const createLoadBalancer = async (b: CreateLoadBalancerBody) => {
  const { data, error } = await client.POST('/api/loadbalancers', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteLoadBalancer = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/loadbalancers/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export const setLoadBalancerBackends = async (uuid: string, backends: string[]) => {
  const { data, error } = await client.PUT('/api/loadbalancers/{uuid}/backends', {
    params: { path: { uuid } },
    body: backends,
  });
  if (error) throwErr(error);
  return data;
};

export interface CreateDNSZoneBody {
  name: string;
  role?: 'primary' | 'secondary' | 'forward';
  ttl_default?: number;
  push_target?: string;
}

export const createDNSZone = async (b: CreateDNSZoneBody) => {
  const { data, error } = await client.POST('/api/dns-zones', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteDNSZone = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/dns-zones/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export interface CreateDNSRecordBody {
  zone_uuid: string;
  name: string;
  type: string;
  value: string;
  ttl?: number;
}

export const createDNSRecord = async (b: CreateDNSRecordBody) => {
  const { data, error } = await client.POST('/api/dns-records', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteDNSRecord = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/dns-records/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

// ---- Networks -----------------------------------------------------

export interface CreateNetworkBody {
  name: string;
  cidr: string;
  gateway?: string;
  type?: string;
  dns_servers?: string[];
}

export const createNetwork = async (b: CreateNetworkBody) => {
  const { data, error } = await client.POST('/api/networks', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteNetwork = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/networks/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

// ---- Scheduling rules ---------------------------------------------

export interface CreateSchedulingRuleBody {
  name: string;
  selector: string;
  count: number;
  az?: string;
  rack?: string;
  host?: string;
  project?: string;
}

export const createSchedulingRule = async (b: CreateSchedulingRuleBody) => {
  const { data, error } = await client.POST('/api/scheduling-rules', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteSchedulingRule = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/scheduling-rules/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
};

// PATCH /api/scheduling-rules/{name} — every field optional ; a non-null
// "" on AZ/Rack/Host clears the axis. count must be ≥ 0.
export interface UpdateSchedulingRuleBody {
  count?: number;
  selector?: string;
  az?: string;
  rack?: string;
  host?: string;
  project?: string;
}

export const updateSchedulingRule = async (
  name: string,
  body: UpdateSchedulingRuleBody,
) => {
  const { data, error } = await client.PATCH('/api/scheduling-rules/{name}', {
    params: { path: { name } },
    body,
  });
  if (error) throwErr(error);
  return data;
};

// ---- Shares (lifecycle) -------------------------------------------

export interface CreateShareBody {
  name: string;
  project?: string;
  backend?: string;
  size_gb: number;
  read_only?: boolean;
}

export const createShare = async (b: CreateShareBody) => {
  const { data, error } = await client.POST('/api/shares', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteShare = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/shares/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
};

export const resizeShare = async (name: string, sizeGB: number, readOnly: boolean): Promise<void> => {
  const { error } = await client.PUT('/api/shares/{name}', {
    params: { path: { name } },
    body: { size_gb: sizeGB, read_only: readOnly },
  });
  if (error) throwErr(error);
};

// ---- Security groups ----------------------------------------------

// Sourced from api.gen.ts — direction + protocol carry their narrow
// enum types now that the Go struct has 'enum:' tags.
export type SecurityRule = APISecurityRule;

export interface CreateSecurityGroupBody {
  name: string;
  description?: string;
  rules: SecurityRule[];
}

export const createSecurityGroup = async (b: CreateSecurityGroupBody) => {
  const { data, error } = await client.POST('/api/security-groups', { body: b });
  if (error) throwErr(error);
  return data;
};

export const deleteSecurityGroup = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/security-groups/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export const updateSecurityGroup = async (uuid: string, patch: {
  name?: string;
  description?: string;
  enabled?: boolean;
}): Promise<void> => {
  // `enabled` is a tri-state on the wire (omitted = leave alone) so
  // only forward it when the caller actually passed a value.
  const body: { name: string; description: string; enabled?: boolean } = {
    name: patch.name ?? '',
    description: patch.description ?? '',
  };
  if (patch.enabled !== undefined) body.enabled = patch.enabled;
  const { error } = await client.PUT('/api/security-groups/{uuid}', {
    params: { path: { uuid } },
    body,
  });
  if (error) throwErr(error);
};

// isEnabled treats a missing `enabled` field as true — the seed rows
// don't all carry the column yet and we want the UI to default to the
// permissive state.
export function isEnabled(row: Row | { enabled?: boolean } | null | undefined): boolean {
  if (!row) return false;
  return (row as { enabled?: boolean }).enabled !== false;
}

export const getSecurityGroupRules = async (uuid: string): Promise<SecurityRule[]> => {
  const { data, error } = await client.GET('/api/security-groups/{uuid}/rules', { params: { path: { uuid } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const setSecurityGroupRules = async (uuid: string, rules: SecurityRule[]) => {
  const { data, error } = await client.PUT('/api/security-groups/{uuid}/rules', {
    params: { path: { uuid } },
    body: rules,
  });
  if (error) throwErr(error);
  return data;
};

// ---- Floating IPs -------------------------------------------------

export interface AllocateFloatingIPBody { network: string }

export const allocateFloatingIP = async (b: AllocateFloatingIPBody) => {
  const { data, error } = await client.POST('/api/floating-ips', { body: b });
  if (error) throwErr(error);
  return data;
};

export const releaseFloatingIP = async (uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/floating-ips/{uuid}', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

export const mapFloatingIP = async (uuid: string, targetKind: 'vm' | 'lb', targetName: string) => {
  const { data, error } = await client.POST('/api/floating-ips/{uuid}/map', {
    params: { path: { uuid } },
    body: { target_kind: targetKind, target_name: targetName },
  });
  if (error) throwErr(error);
  return data;
};

export const unmapFloatingIP = async (uuid: string): Promise<void> => {
  const { error } = await client.POST('/api/floating-ips/{uuid}/unmap', { params: { path: { uuid } } });
  if (error) throwErr(error);
};

// ---- VM inspect (status / timings / logs) -------------------------

// Types now come from api.gen.ts (wclient defines the canonical Go
// shapes ; huma surfaces them in the OpenAPI). Legacy names kept as
// aliases so existing component imports don't break.
export type VMStatus = APIVMInfo;
export type VMTimingEvent = APIVMTimingEvent;
export type VMLogs = APIVMLogsResult;

export const getVMStatus = async (name: string): Promise<VMStatus> => {
  const { data, error } = await client.GET('/api/microvms/{name}/status', { params: { path: { name } } });
  if (error) throwErr(error);
  return data;
};

export const getVMTimings = async (name: string): Promise<VMTimingEvent[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/timings', { params: { path: { name } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const getVMLogs = async (name: string, tail = 65536): Promise<VMLogs> => {
  const { data, error } = await client.GET('/api/microvms/{name}/logs', {
    params: { path: { name }, query: { tail } },
  });
  if (error) throwErr(error);
  return data;
};

// ---- VM metrics (poll-driven time series) -------------------------

// MetricsSnapshot mirrors the server's huma type. Until weft-proto
// gains a GetMicroVMMetrics RPC, the server returns a synthetic
// sample with `mock: true` ; the SPA renders a badge so operators
// don't mistake the curves for real data. See
// internal/server/api_metrics.go.
export type MetricsSnapshot = APIMetricsSnapshot;

export const getMicroVMMetrics = async (name: string): Promise<MetricsSnapshot> => {
  const { data, error } = await client.GET('/api/microvms/{name}/metrics', {
    params: { path: { name } },
  });
  if (error) throwErr(error);
  return data;
};

// ---- SSH-keys catalogue -------------------------------------------

// Backwards-compatible alias.
export type SSHKeyEntry = APISSHKey;

export const listSSHKeyCatalogue = async (): Promise<SSHKeyEntry[]> => {
  const { data, error } = await client.GET('/api/ssh-keys');
  if (error) throwErr(error);
  return data ?? [];
};

export const getSSHKeyCatalogue = async (name: string): Promise<SSHKeyEntry> => {
  const { data, error } = await client.GET('/api/ssh-keys/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
  return data;
};

export const setSSHKeyCatalogue = async (k: {
  name: string;
  public_key: string;
  description?: string;
  source?: string;
  source_account?: string;
}): Promise<SSHKeyEntry> => {
  const { data, error } = await client.POST('/api/ssh-keys', {
    body: {
      name: k.name, public_key: k.public_key,
      description: k.description ?? '',
      // The server enforces source ∈ {manual,github,gitlab,forgejo}.
      // Callers pass an open string for ergonomic reasons (the SSH-keys
      // page builds it from a free-form input) ; cast at the boundary
      // and let the server 400 if it's something unexpected.
      source: (k.source ?? 'manual') as 'manual' | 'github' | 'gitlab' | 'forgejo',
      source_account: k.source_account ?? '',
      fingerprint: '', updated_at: '', updated_by: '',
    },
  });
  if (error) throwErr(error);
  return data;
};

export const deleteSSHKeyCatalogue = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/ssh-keys/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
};

// Override names from the generated 'string[] | null' to non-null —
// the helper coerces null → [] so callers never see the null branch.
export type ImportSSHKeysResult = Omit<APIImportResult, 'names'> & { names: string[] };

export const importSSHKeys = async (b: {
  provider: 'github' | 'gitlab' | 'forgejo';
  account: string;
  forgejo_base?: string;
}): Promise<ImportSSHKeysResult> => {
  const { data, error } = await client.POST('/api/ssh-keys/import', {
    body: { provider: b.provider, account: b.account, forgejo_base: b.forgejo_base ?? '' },
  });
  if (error) throwErr(error);
  return { ...data, names: data.names ?? [] };
};

// ---- Per-VM SSH-key assignments -----------------------------------

// Backwards-compatible alias for the resolved shape.
import type { APIVMSSHKey, APIVMProperty, APIUEFIVar } from './client';
export type VMSSHKey = APIVMSSHKey;

export const listVMKeys = async (vmName: string): Promise<VMSSHKey[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/keys', { params: { path: { name: vmName } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const addVMKey = async (vmName: string, catalogueName: string): Promise<VMSSHKey> => {
  const { data, error } = await client.POST('/api/microvms/{name}/keys', {
    params: { path: { name: vmName } },
    body: { name: catalogueName },
  });
  if (error) throwErr(error);
  return data;
};

export const setVMKeys = async (vmName: string, catalogueNames: string[]): Promise<VMSSHKey[]> => {
  const { data, error } = await client.PUT('/api/microvms/{name}/keys', {
    params: { path: { name: vmName } },
    body: { names: catalogueNames },
  });
  if (error) throwErr(error);
  return (data ?? []) as VMSSHKey[];
};

export const removeVMKey = async (vmName: string, catalogueName: string): Promise<void> => {
  const { error } = await client.DELETE('/api/microvms/{name}/keys/{key_name}', {
    params: { path: { name: vmName, key_name: catalogueName } },
  });
  if (error) throwErr(error);
};

// ---- Per-VM properties --------------------------------------------

export type VMProperty = APIVMProperty;

export const listVMProperties = async (name: string): Promise<VMProperty[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/properties', { params: { path: { name } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const setVMProperty = async (
  name: string,
  p: { key: string; value: string; guest_readable: boolean },
): Promise<VMProperty> => {
  const { data, error } = await client.POST('/api/microvms/{name}/properties', {
    params: { path: { name } },
    body: { key: p.key, value: p.value, guest_readable: p.guest_readable, updated_at: '' },
  });
  if (error) throwErr(error);
  return data;
};

export const removeVMProperty = async (name: string, key: string): Promise<void> => {
  const { error } = await client.DELETE('/api/microvms/{name}/properties/{key}', {
    params: { path: { name, key } },
  });
  if (error) throwErr(error);
};

// ---- UEFI NVRAM variables -----------------------------------------

export type UEFIVar = APIUEFIVar;

export const listUEFIVars = async (name: string): Promise<UEFIVar[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/uefi-vars', { params: { path: { name } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const setUEFIVar = async (
  name: string,
  v: { namespace?: string; name: string; value_hex: string; attributes: string[] },
): Promise<UEFIVar> => {
  const { data, error } = await client.POST('/api/microvms/{name}/uefi-vars', {
    params: { path: { name } },
    body: {
      namespace: v.namespace ?? '',
      name: v.name,
      value_hex: v.value_hex,
      attributes: v.attributes,
      updated_at: '',
    },
  });
  if (error) throwErr(error);
  return data;
};

export const removeUEFIVar = async (vmName: string, namespace: string, varName: string): Promise<void> => {
  const { error } = await client.DELETE('/api/microvms/{name}/uefi-vars/{ns}/{varname}', {
    params: { path: { name: vmName, ns: namespace, varname: varName } },
  });
  if (error) throwErr(error);
};

// ---- Quota dimension metadata (frontend constant) -----------------

export interface QuotaDimMeta {
  key: keyof Quotas;
  label: string;
  unit?: string;
  tenantOnly?: boolean;
  // resource = the resource ID this dim depends on. If the resource
  // is plugin-gated and no contributing plugin is installed+enabled,
  // the dim disappears from QuotaBars (filterQuotaDims below).
  resource?: string;
}

export const QUOTA_DIMS: QuotaDimMeta[] = [
  { key: 'vcpu',         label: 'vCPU' },
  { key: 'ram_gib',      label: 'RAM',       unit: 'GiB' },
  { key: 'gpus',         label: 'GPUs' },
  { key: 'volumes',      label: 'Volumes' },
  { key: 'volumes_gib',  label: 'Volume capacity', unit: 'GiB' },
  { key: 'shares',       label: 'Shares',                       resource: 'shares' },
  { key: 'shares_gib',   label: 'Share capacity',  unit: 'GiB', resource: 'shares' },
  { key: 'buckets',      label: 'Buckets',                      resource: 'buckets' },
  { key: 'buckets_gib',  label: 'Bucket capacity', unit: 'GiB', resource: 'buckets' },
  { key: 'registry_gib', label: 'Registry',        unit: 'GiB', resource: 'registries' },
  { key: 'floating_ips', label: 'Floating IPs' },
  { key: 'projects',     label: 'Projects',  tenantOnly: true },
];

// ---- Registries : remote-registry catalogue (proxy / replica) -----

export type RegistryRemote = APIRegistryRemote;

export const listRegistryRemotes = async (): Promise<RegistryRemote[]> => {
  const { data, error } = await client.GET('/api/registries/remotes');
  if (error) throwErr(error);
  return data ?? [];
};

export const getRegistryRemote = async (name: string): Promise<RegistryRemote> => {
  const { data, error } = await client.GET('/api/registries/remotes/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
  return data;
};

export const setRegistryRemote = async (r: {
  name: string;
  url: string;
  kind: 'proxy' | 'replica';
  enabled: boolean;
  username?: string;
}): Promise<RegistryRemote> => {
  const { data, error } = await client.POST('/api/registries/remotes', {
    body: {
      name: r.name, url: r.url, kind: r.kind, enabled: r.enabled,
      username: r.username ?? '',
      // last_sync is server-owned ; updated_at/by are stamped by the
      // server. Send empty strings so the validation passes.
      last_sync: '', updated_at: '', updated_by: '',
    },
  });
  if (error) throwErr(error);
  return data;
};

export const deleteRegistryRemote = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/registries/remotes/{name}', { params: { path: { name } } });
  if (error) throwErr(error);
};

export type RemoteSearchHit = APIRemoteSearchHit;

export const searchRegistryRemote = async (name: string, q: string): Promise<RemoteSearchHit[]> => {
  const { data, error } = await client.GET('/api/registries/remotes/{name}/search', {
    params: { path: { name }, query: { q } },
  });
  if (error) throwErr(error);
  return data ?? [];
};

// ---- Volumes : rename + editable metadata + property bag ---------

export const renameVolumeByKey = async (key: string, newName: string): Promise<{ name: string }> => {
  const { data, error } = await client.PUT('/api/volumes/{key}', {
    params: { path: { key } },
    body: { new_name: newName },
  });
  if (error) throwErr(error);
  return data;
};


export type VolumeMetadata = APIVolumeMetadata;
export type VolumeProperty = APIVolumeProperty;

export const getVolumeMetadata = async (key: string): Promise<VolumeMetadata> => {
  const { data, error } = await client.GET('/api/volumes/{key}/metadata', { params: { path: { key } } });
  if (error) throwErr(error);
  return data;
};

export const setVolumeMetadata = async (key: string, m: {
  description: string;
  mount_point: string;
  filesystem: '' | 'ext4' | 'xfs' | 'btrfs' | 'ext3' | 'zfs';
}): Promise<VolumeMetadata> => {
  const { data, error } = await client.PUT('/api/volumes/{key}/metadata', {
    params: { path: { key } },
    body: {
      description: m.description, mount_point: m.mount_point, filesystem: m.filesystem,
      updated_at: '', updated_by: '',
    },
  });
  if (error) throwErr(error);
  return data;
};

export const listVolumeProperties = async (key: string): Promise<VolumeProperty[]> => {
  const { data, error } = await client.GET('/api/volumes/{key}/properties', { params: { path: { key } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const setVolumeProperty = async (key: string, propKey: string, value: string): Promise<VolumeProperty> => {
  const { data, error } = await client.POST('/api/volumes/{key}/properties', {
    params: { path: { key } },
    body: { key: propKey, value, updated_at: '' },
  });
  if (error) throwErr(error);
  return data;
};

export const deleteVolumeProperty = async (key: string, propKey: string): Promise<void> => {
  const { error } = await client.DELETE('/api/volumes/{key}/properties/{prop_key}', {
    params: { path: { key, prop_key: propKey } },
  });
  if (error) throwErr(error);
};

// ---- Networks : editable metadata + rename -------------------------

export type NetworkMetadata = Omit<APINetworkMetadata, 'dns_servers'> & {
  // openapi-typescript stamps `string[] | null` for nullable arrays ;
  // the helper coerces null → [] so callers never see the null branch.
  dns_servers: string[];
};

export const getNetworkMetadata = async (key: string): Promise<NetworkMetadata> => {
  const { data, error } = await client.GET('/api/networks/{key}/metadata', { params: { path: { key } } });
  if (error) throwErr(error);
  return { ...data, dns_servers: data.dns_servers ?? [] };
};

export const setNetworkMetadata = async (key: string, m: {
  description: string;
  dns_servers: string[];
}): Promise<NetworkMetadata> => {
  const { data, error } = await client.PUT('/api/networks/{key}/metadata', {
    params: { path: { key } },
    body: {
      description: m.description, dns_servers: m.dns_servers,
      updated_at: '', updated_by: '',
    },
  });
  if (error) throwErr(error);
  return { ...data, dns_servers: data.dns_servers ?? [] };
};

export const renameNetworkByKey = async (key: string, newName: string): Promise<{ name: string }> => {
  const { data, error } = await client.PUT('/api/networks/{key}', {
    params: { path: { key } },
    body: { new_name: newName },
  });
  if (error) throwErr(error);
  return data;
};

// ---- DNS : edit zones + records (mock-backed for now) -----------

export const updateDNSZone = async (uuid: string, patch: {
  name?: string;
  role?: '' | 'primary' | 'secondary' | 'forward';
  ttl_default?: number;
  backend?: string;
  push_target?: string;
  enabled?: boolean;
}): Promise<void> => {
  const body: {
    name: string;
    role: 'primary' | 'secondary' | 'forward' | '';
    ttl_default: number;
    backend: string;
    push_target: string;
    enabled?: boolean;
  } = {
    name: patch.name ?? '',
    role: (patch.role ?? '') as 'primary' | 'secondary' | 'forward' | '',
    ttl_default: patch.ttl_default ?? 0,
    backend: patch.backend ?? '',
    push_target: patch.push_target ?? '',
  };
  if (patch.enabled !== undefined) body.enabled = patch.enabled;
  const { error } = await client.PUT('/api/dns-zones/{uuid}', {
    params: { path: { uuid } }, body,
  });
  if (error) throwErr(error);
};

export const updateDNSRecord = async (uuid: string, patch: {
  name?: string;
  type?: string;
  value?: string;
  ttl?: number;
  enabled?: boolean;
}): Promise<void> => {
  const body: {
    name: string; type: string; value: string; ttl: number; enabled?: boolean;
  } = {
    name: patch.name ?? '',
    type: patch.type ?? '',
    value: patch.value ?? '',
    ttl: patch.ttl ?? 0,
  };
  if (patch.enabled !== undefined) body.enabled = patch.enabled;
  const { error } = await client.PUT('/api/dns-records/{uuid}', {
    params: { path: { uuid } }, body,
  });
  if (error) throwErr(error);
};

// ---- Generic editable metadata (routers / fips / scheduling-rules) -

export type EditableMetadata = APIEditableMetadata;

type EditableResource = 'routers' | 'floating-ips' | 'scheduling-rules';

export const getEditableMetadata = async (
  res: EditableResource, key: string,
): Promise<EditableMetadata> => {
  // The generated client doesn't union the path between the three
  // resources (each has its own typed entry), so the helper dispatches
  // by literal. Same payload shape across all three.
  switch (res) {
    case 'routers': {
      const { data, error } = await client.GET('/api/routers/{key}/metadata', { params: { path: { key } } });
      if (error) throwErr(error);
      return data;
    }
    case 'floating-ips': {
      const { data, error } = await client.GET('/api/floating-ips/{key}/metadata', { params: { path: { key } } });
      if (error) throwErr(error);
      return data;
    }
    case 'scheduling-rules': {
      const { data, error } = await client.GET('/api/scheduling-rules/{key}/metadata', { params: { path: { key } } });
      if (error) throwErr(error);
      return data;
    }
  }
};

export const setEditableMetadata = async (
  res: EditableResource, key: string, description: string,
): Promise<EditableMetadata> => {
  const body = { description, updated_at: '', updated_by: '' };
  switch (res) {
    case 'routers': {
      const { data, error } = await client.PUT('/api/routers/{key}/metadata', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
    case 'floating-ips': {
      const { data, error } = await client.PUT('/api/floating-ips/{key}/metadata', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
    case 'scheduling-rules': {
      const { data, error } = await client.PUT('/api/scheduling-rules/{key}/metadata', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
  }
};

export const renameEditableRow = async (
  res: EditableResource, key: string, newName: string,
): Promise<{ name: string }> => {
  const body = { new_name: newName };
  switch (res) {
    case 'routers': {
      const { data, error } = await client.PUT('/api/routers/{key}', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
    case 'floating-ips': {
      const { data, error } = await client.PUT('/api/floating-ips/{key}', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
    case 'scheduling-rules': {
      const { data, error } = await client.PUT('/api/scheduling-rules/{key}', { params: { path: { key } }, body });
      if (error) throwErr(error);
      return data;
    }
  }
};

// ---- Subnets (network sub-resource) -------------------------------

export type Subnet = APISubnet;

export const listSubnets = async (networkKey: string): Promise<Subnet[]> => {
  const { data, error } = await client.GET('/api/networks/{key}/subnets', { params: { path: { key: networkKey } } });
  if (error) throwErr(error);
  return data ?? [];
};

export const setSubnet = async (networkKey: string, s: {
  uuid?: string;
  name: string;
  cidr: string;
  gateway?: string;
  enabled?: boolean;
}): Promise<Subnet> => {
  const { data, error } = await client.POST('/api/networks/{key}/subnets', {
    params: { path: { key: networkKey } },
    body: {
      uuid: s.uuid ?? '',
      name: s.name, cidr: s.cidr,
      gateway: s.gateway ?? '',
      enabled: s.enabled ?? true,
      updated_at: '', updated_by: '',
    },
  });
  if (error) throwErr(error);
  return data;
};

export const deleteSubnet = async (networkKey: string, uuid: string): Promise<void> => {
  const { error } = await client.DELETE('/api/networks/{key}/subnets/{uuid}', {
    params: { path: { key: networkKey, uuid } },
  });
  if (error) throwErr(error);
};

// ---- microVMs by scheduling-rule (the 'deployment' view) ---------

export const listSchedulingRuleMicroVMs = async (key: string): Promise<Row[]> => {
  const { data, error } = await client.GET('/api/scheduling-rules/{key}/microvms', {
    params: { path: { key } },
  });
  if (error) throwErr(error);
  return data ?? [];
};

// ---- Per-VM authorized-groups + effective keys --------------------

export type AuthorizedGroup = APIAuthorizedGroup;
export type EffectiveKey    = APIEffectiveKey;

export const listVMAuthorizedGroups = async (vmName: string): Promise<AuthorizedGroup[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/authorized-groups', {
    params: { path: { name: vmName } },
  });
  if (error) throwErr(error);
  return data ?? [];
};

export const addVMAuthorizedGroup = async (vmName: string, tenant: string, group: string): Promise<AuthorizedGroup> => {
  const { data, error } = await client.POST('/api/microvms/{name}/authorized-groups', {
    params: { path: { name: vmName } },
    body: { tenant, group, added_at: '' },
  });
  if (error) throwErr(error);
  return data;
};

export const removeVMAuthorizedGroup = async (vmName: string, tenant: string, group: string): Promise<void> => {
  const { error } = await client.DELETE('/api/microvms/{name}/authorized-groups/{tenant}/{group}', {
    params: { path: { name: vmName, tenant, group } },
  });
  if (error) throwErr(error);
};

export const listVMEffectiveKeys = async (vmName: string): Promise<EffectiveKey[]> => {
  const { data, error } = await client.GET('/api/microvms/{name}/effective-keys', {
    params: { path: { name: vmName } },
  });
  if (error) throwErr(error);
  return data ?? [];
};

// ---- Plugins (*-as-a-service marketplace) -------------------------

export type Plugin = APIPlugin;

export const listPlugins = async (): Promise<Plugin[]> => {
  const { data, error } = await client.GET('/api/plugins');
  if (error) throwErr(error);
  return data ?? [];
};

export const installPlugin = async (id: string): Promise<Plugin> => {
  const { data, error } = await client.POST('/api/plugins/{id}/install', { params: { path: { id } } });
  if (error) throwErr(error);
  return data;
};

export const uninstallPlugin = async (id: string): Promise<Plugin> => {
  const { data, error } = await client.POST('/api/plugins/{id}/uninstall', { params: { path: { id } } });
  if (error) throwErr(error);
  return data;
};

export const enablePlugin = async (id: string): Promise<Plugin> => {
  const { data, error } = await client.POST('/api/plugins/{id}/enable', { params: { path: { id } } });
  if (error) throwErr(error);
  return data;
};

export const disablePlugin = async (id: string): Promise<Plugin> => {
  const { data, error } = await client.POST('/api/plugins/{id}/disable', { params: { path: { id } } });
  if (error) throwErr(error);
  return data;
};

// ---- Federation peers (federation-lite, /cluster-info pull) -------
//
// Mirrors the `weft federation list` table. Status is one of
// 'live' | 'stale' | 'unreachable' — derived server-side from
// LastSeen + LastError + StaleTTL on each call so the SPA never has
// to re-classify.

import type { components as _genComponents } from './api.gen';
export type FederationPeer = _genComponents['schemas']['FederationPeer'];

export const listFederationPeers = async (): Promise<FederationPeer[]> => {
  const { data, error } = await client.GET('/api/federation/peers');
  if (error) throwErr(error);
  return data ?? [];
};

// ---- "weft plugin install" surface (form-driven) ------------------
//
// catalogue → the SPA renders a card per entry, click opens the
// install drawer which generates a form from entry.inputs.
// installed → the SPA's right pane lists running instances + their
// bound VMs.

export type PluginCatalogueEntry = _genComponents['schemas']['PluginCatalogueEntry'];
export type PluginInput = _genComponents['schemas']['PluginInput'];
export type PluginInstance = _genComponents['schemas']['PluginInstance'];

export const listPluginCatalogue = async (): Promise<PluginCatalogueEntry[]> => {
  const { data, error } = await client.GET('/api/plugins/catalogue');
  if (error) throwErr(error);
  return data ?? [];
};

export const listPluginInstances = async (): Promise<PluginInstance[]> => {
  const { data, error } = await client.GET('/api/plugins/installed');
  if (error) throwErr(error);
  return data ?? [];
};

// installPluginWithInputs POSTs the form values + returns the
// freshly-allocated instance UUID. The drawer surfaces it back to
// the operator so they can copy-paste it into a `weft plugin status
// <uuid>` lookup.
export const installPluginWithInputs = async (
  name: string,
  project: string,
  inputs: Record<string, string>,
): Promise<string> => {
  const { data, error } = await client.POST('/api/plugins/install', {
    body: { name, project, inputs },
  });
  if (error) throwErr(error);
  return data!.instance_uuid;
};

// filterQuotaDims hides dimensions whose backing resource is
// plugin-gated and not currently available. The resource catalogue
// (already plugin-filtered server-side) is the source of truth.
//
// Pass the catalogue you've already fetched via getResources(). Empty
// catalogue (e.g. before App.svelte's first refresh) returns the full
// list — the UI shows nothing rather than wrongly filtering.
export function filterQuotaDims(catalogue: ResourceMeta[]): QuotaDimMeta[] {
  if (catalogue.length === 0) return QUOTA_DIMS;
  const available = new Set(catalogue.map((r) => r.id));
  return QUOTA_DIMS.filter((d) => !d.resource || available.has(d.resource));
}

// ---- Diagnoses (admin-only, Infra portal) -------------------------
//
// Snapshot fed by weft-doctor via NATS → in-process cache. The wire
// shape is { items: Diagnosis[] } ; the helper unwraps the envelope
// and coerces the nullable arrays at the boundary so the SPA never
// has to ternary-guard.

export type DiagnosisExample = APIDiagnosisExample;
export type Diagnosis = Omit<APIDiagnosis, 'examples'> & {
  examples: DiagnosisExample[];
};

const coerceDiagnosis = (d: APIDiagnosis): Diagnosis => ({
  ...d,
  examples: (d.examples ?? []) as DiagnosisExample[],
});

export const listDiagnoses = async (): Promise<Diagnosis[]> => {
  const { data, error } = await client.GET('/api/diagnoses');
  if (error) throwErr(error);
  return (data.items ?? []).map(coerceDiagnosis);
};
