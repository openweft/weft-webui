// Thin client for the weft-webui JSON API.
//
// The hand-rolled types below are being migrated to the auto-
// generated `api.gen.ts` (run `npm run gen:api`). New code should
// pull types from `./client.ts` ; the legacy exports here forward
// to the same source where the migration has landed.
//
// Status :
//   * Flavors, Scripts, SSH-keys catalogue, per-VM (props/uefi/keys)
//     — types come from `api.gen.ts` via `client.ts`.
//   * Everything else — still hand-rolled. Migration is mechanical
//     and incremental ; one section per PR.

export interface Column {
  key: string;
  label: string;
}

export interface ResourceMeta {
  id: string;
  label: string;
  section: string;
  columns: Column[];
  count: number;
}

export type Row = Record<string, unknown>;

// 401 from any API call means the session is missing/expired. We send
// the user through the OIDC login flow and ask the backend to bounce
// them back to the page they were on. Done at the api-helper layer so
// every caller (Sidebar, Overview, tables, …) inherits the behaviour.
function handleUnauthorised(): never {
  const back = encodeURIComponent(location.pathname + location.search + location.hash);
  location.assign(`/api/auth/login?return_to=${back}`);
  // Throw so callers awaiting the promise don't try to use the empty
  // body ; the redirect is happening on the next tick.
  throw new Error('unauthenticated');
}

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`/api${path}`, { headers: { Accept: 'application/json' } });
  if (res.status === 401) handleUnauthorised();
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export const getResources = () => getJSON<ResourceMeta[]>('/resources');

// ---- Provisioning scripts catalogue ----
//
// Named, reusable sh bodies pickable in CreateVMModal. Source-of-
// truth lives in weft-agent's etcd-backed store (planned, [[openweft
// _etcd_embedded]]) ; for now it's an in-memory mock served by the
// webui. Same shape as the flavors catalogue : List on both ports,
// write-side admin-gated.
//
// Type now comes from api.gen.ts ; the helpers use the typed client.

import { client, type APIScript } from './client';

// Backwards-compatible alias for callers that still import `Script`.
export type Script = APIScript;

export const listScripts = async (): Promise<Script[]> => {
  const { data, error } = await client.GET('/api/scripts');
  if (error) throw new Error(toMsg(error));
  // openapi-typescript types the array as `T[] | null` because OpenAPI
  // doesn't forbid null. The Go side always returns [], never null —
  // coerce so callers don't have to.
  return data ?? [];
};
export const getScript = async (name: string): Promise<Script> => {
  const { data, error } = await client.GET('/api/scripts/{name}', {
    params: { path: { name } },
  });
  if (error) throw new Error(toMsg(error));
  return data;
};
export const setScript = async (
  s: { name: string; description: string; body: string },
): Promise<Script> => {
  const { data, error } = await client.POST('/api/scripts', {
    body: { ...s, updated_at: '', updated_by: '' },
  });
  if (error) throw new Error(toMsg(error));
  return data;
};
export const deleteScript = async (name: string): Promise<void> => {
  const { error } = await client.DELETE('/api/scripts/{name}', {
    params: { path: { name } },
  });
  if (error) throw new Error(toMsg(error));
};

// toMsg unboxes huma's RFC 7807 error envelope to a plain string the
// existing call sites can throw. detail is the operator-facing field ;
// title falls back when detail is empty.
function toMsg(e: unknown): string {
  if (e && typeof e === 'object') {
    const o = e as { detail?: string; title?: string; error?: string };
    return o.detail || o.title || o.error || JSON.stringify(e);
  }
  return String(e);
}

// /api/resources/:id now returns a {rows, next, total} envelope. Most
// callers don't care about the cursor (modals, search palette, drawers
// that snapshot once) — `getRows` keeps the array-only contract by
// unwrapping. The table component uses `getRowsPage` directly so it can
// surface "Load more" + the running total.
export interface Page<T> {
  rows: T[];
  next: string;  // empty when no further page
  total: number; // total rows on the server-side slice (post-filter)
}

export interface PageOpts {
  limit?: number;
  pageToken?: string;
}

export async function getRowsPage(id: string, opts: PageOpts = {}): Promise<Page<Row>> {
  const q = new URLSearchParams();
  if (opts.limit) q.set('limit', String(opts.limit));
  if (opts.pageToken) q.set('page_token', opts.pageToken);
  const qs = q.toString();
  return getJSON<Page<Row>>(`/resources/${id}${qs ? `?${qs}` : ''}`);
}

// Convenience wrapper for the dozen callers that only want the rows.
// Asks for the maximum page size (1000) ; today's mock datasets fit
// in one page, and a follow-on PR can move noisy callers to the paged
// API as their tables grow.
export async function getRows(id: string): Promise<Row[]> {
  const p = await getRowsPage(id, { limit: 1000 });
  return p.rows;
}

// Flavors catalogue — separate endpoint because the sidebar entry is
// admin-only (so a user UI can't reach /api/resources/flavors) but
// the catalogue itself is needed in CreateVMModal on both UIs.
export const getFlavors = () => getJSON<Row[]>('/flavors');
export const getSummary = () =>
  getJSON<{ id: string; label: string; count: number }[]>('/summary');

// Push any OCI artifact (container image, raw multi-arch disk, chart,
// model blob) to a registry. The FormData carries: type, registry,
// repository, tag, repeated arch fields, and files.
export async function uploadArtifact(form: FormData): Promise<Row> {
  return postForm('/api/registry/upload', form);
}

// ---- File storage (buckets = S3 prefixes, shares = POSIX dirs) ----
// Both browse the same way ; `kind` selects the endpoint family.

export type StorageKind = 'buckets' | 'shares';

export interface ObjectEntry {
  name: string;
  key: string;
  size: number;
  sizeHuman: string;
  modified: string;
  contentType: string;
}

export interface ObjectListing {
  prefix: string;
  folders: string[];
  objects: ObjectEntry[];
}

export interface ObjectDetail {
  key: string;
  size: number;
  sizeHuman: string;
  modified: string;
  contentType: string;
  previewable: boolean;
  content: string;
}

export const browse = (kind: StorageKind, container: string, prefix = '') =>
  getJSON<ObjectListing>(`/${kind}/${container}/objects?prefix=${encodeURIComponent(prefix)}`);

export const readEntry = (kind: StorageKind, container: string, key: string) =>
  getJSON<ObjectDetail>(`/${kind}/${container}/object?key=${encodeURIComponent(key)}`);

export const uploadEntries = (kind: StorageKind, container: string, form: FormData) =>
  postForm(`/api/${kind}/${container}/objects`, form);

// Delete one object by key. Only wired for buckets today ; the server
// returns 404 if you point it at a share (no DELETE route on that side
// yet — share files come and go via the workload itself).
export async function deleteEntry(kind: StorageKind, container: string, key: string): Promise<void> {
  const url = `/api/${kind}/${container}/object?key=${encodeURIComponent(key)}`;
  const res = await fetch(url, { method: 'DELETE' });
  if (res.status === 401) handleUnauthorised();
  if (res.status === 204) return;
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
}

// Buckets are user-managed (shares are provisioned via the share lifecycle).
export async function createBucket(name: string): Promise<void> {
  const res = await fetch('/api/buckets', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ name }),
  });
  if (!res.ok) {
    const b = await res.json().catch(() => ({}));
    throw new Error((b as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  }
}

export async function deleteBucket(name: string): Promise<void> {
  const res = await fetch(`/api/buckets/${name}`, { method: 'DELETE' });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
}

// ---- Bucket access policy ----
//
// Trimmed AWS-style policy : flat statement list, one principal +
// action + resource each. The server's vocabulary is closed (the
// values below are the only ones accepted) ; widening the SPA editor
// goes hand-in-hand with the server's validPolicyActions map.

export type PolicyEffect = 'Allow' | 'Deny';
export type PolicyAction =
  | 's3:GetObject'
  | 's3:PutObject'
  | 's3:DeleteObject'
  | 's3:ListBucket';

export interface PolicyStatement {
  effect: PolicyEffect;
  principal: string; // OIDC sub or "*"
  action: PolicyAction;
  resource: string;  // "*" | "prefix/*" | exact key
}

export interface BucketPolicy {
  version: string;
  statements: PolicyStatement[];
}

export const getBucketPolicy = (name: string) =>
  getJSON<BucketPolicy>(`/buckets/${encodeURIComponent(name)}/policy`);

export async function setBucketPolicy(name: string, p: BucketPolicy): Promise<BucketPolicy> {
  return putJSON<BucketPolicy>(`/buckets/${encodeURIComponent(name)}/policy`, p);
}

// ---- Network topology (mesh map) ----

export interface TopoNetwork {
  id: string;
  name: string;
  cidr: string;
  az: string;
  type: string;
}

export interface TopoNode {
  id: string;
  name: string;
  kind: 'microvm' | 'instance' | 'infra';
  network: string;
  status: string;
  project: string;
  host: string;
}

export const getTopology = () =>
  getJSON<{ networks: TopoNetwork[]; nodes: TopoNode[] }>('/network-topology');

// ---- Quotas (overview) ----

export interface Quota {
  id: string;
  label: string;
  icon: string;
  used: number;
  limit: number;
  unit: string;
}

export const getQuotas = () => getJSON<Quota[]>('/quotas');

async function postForm(path: string, form: FormData): Promise<Row> {
  const res = await fetch(path, { method: 'POST', body: form });
  if (res.status === 401) handleUnauthorised();
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return body as Row;
}

// ---- Auth / current user ----

export interface ScopeEntry {
  name: string;        // tenant name
  domain: string;
  status: string;
  projects: string[];  // project names of this tenant
}

export interface Me {
  sub: string;
  email: string;
  name: string;
  groups: string[];
  // Current cascading-topbar selection. Both can be empty :
  // tenant="" project=""  → "(all tenants)" — cluster admin only
  // tenant set, project=""→ tenant-aggregate (all projects of tenant)
  // both set              → project-scoped
  tenant: string;
  project: string;
  initials: string;
  dev: boolean;
  cluster_admin: boolean;
  tenant_admin: boolean;
  // scopes : one entry per tenant the user belongs to, each carrying
  // its project names. Drives the cascading dropdown.
  scopes: ScopeEntry[];
}

// onAdminUI tells the SPA which listener served it. The admin handler
// surfaces "hosts" + "users" + "tenants" (Scope=Admin) in
// /api/resources ; the user handler doesn't. This is the persona
// signal, distinct from the user's *role* — a cluster admin can be
// browsing the user UI, and the Topbar should show "ADMIN" not
// "SUPERADMIN" in that case ("you have admin rights while acting as a
// regular user").
export async function onAdminUI(): Promise<boolean> {
  try {
    const rs = await getResources();
    return rs.some((r) => r.id === 'hosts');
  } catch {
    return false;
  }
}

export const getMe = () => getJSON<Me>('/me');

// setScope sets the session's (tenant, project) pair. Pass empty
// strings to clear either field. The server re-mints the cookie.
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

// ---- Tenant administration ----

export interface TenantMember {
  email: string;
  name: string;
  groups: string[];
  admin: boolean;
}

export interface TenantProject {
  name: string;
  uuid: string;
  created: string;
  roles: Record<string, string>;
}

export interface TenantGroup {
  name: string;
  description: string;
}

export interface TenantDetail {
  name: string;
  domain: string;
  status: string;
  projects: TenantProject[];
  members: TenantMember[];
  groups: TenantGroup[];
  // Set by the server : tells the SPA which affordances to render.
  caller: { email: string; cluster_admin: boolean; tenant_admin: boolean };
}

export const getTenant = (name: string) => getJSON<TenantDetail>(`/tenants/${encodeURIComponent(name)}`);

async function postJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (res.status === 401) handleUnauthorised();
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return data as T;
}

// Cluster-admin (admin UI). Returns 403/404 from the user UI.
export const createTenant = (name: string, domain: string) =>
  postJSON<{ name: string }>('/tenants', { Name: name, Domain: domain });

export const addTenantAdmin = (tenant: string, email: string) =>
  postJSON<{ email: string }>(`/tenants/${encodeURIComponent(tenant)}/admins`, { Email: email });

// Tenant-admin (user UI ; cluster admins can also call from admin UI).
export const addTenantProject = (tenant: string, name: string) =>
  postJSON<TenantProject>(`/tenants/${encodeURIComponent(tenant)}/projects`, { Name: name });

export const addTenantMember = (tenant: string, email: string, groups: string[]) =>
  postJSON<{ email: string; groups: string[] }>(
    `/tenants/${encodeURIComponent(tenant)}/members`,
    { Email: email, Groups: groups },
  );

export const grantProjectRole = (project: string, email: string, role: string) =>
  postJSON<{ email: string; role: string }>(
    `/projects/${encodeURIComponent(project)}/roles`,
    { Email: email, Role: role },
  );

// ---- Quotas ----

export interface Quotas {
  vcpu: number;
  ram_gib: number;
  gpus: number;
  volumes: number;
  volumes_gib: number;
  shares: number;
  shares_gib: number;
  buckets: number;
  buckets_gib: number;
  registry_gib: number;
  floating_ips: number;
  projects: number;
}

export interface QuotaDim { used: number; cap: number; free: number }
export type QuotaBars = Record<string, QuotaDim>;

export interface TenantQuotaView {
  cap: Quotas;
  allocated: Quotas;
  remaining: QuotaBars;
}

export interface ProjectQuotaView {
  project: Quotas;
  tenant_cap: Quotas;
  siblings_total: Quotas;
  tenant_remaining: QuotaBars;
}

export const getTenantQuota = (name: string) =>
  getJSON<TenantQuotaView>(`/tenants/${encodeURIComponent(name)}/quota`);

export const getProjectQuota = (name: string) =>
  getJSON<ProjectQuotaView>(`/projects/${encodeURIComponent(name)}/quota`);

async function putJSON<T>(path: string, body: unknown): Promise<T> {
  const res = await fetch(`/api${path}`, {
    method: 'PUT',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify(body),
  });
  if (res.status === 401) handleUnauthorised();
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return data as T;
}

export const setTenantQuota = (name: string, q: Quotas) =>
  putJSON<TenantQuotaView>(`/tenants/${encodeURIComponent(name)}/quota`, q);

export const setProjectQuota = (name: string, q: Quotas) =>
  putJSON<ProjectQuotaView>(`/projects/${encodeURIComponent(name)}/quota`, q);

// ---- Resource lifecycle (live gRPC) ----
//
// Each helper hits a /api/<kind>/... route that is wired straight to
// weft-agent ; a 503 means "no live daemon" (the operator launched in mock
// mode), a 502 means the daemon refused the call. Both surface via
// the thrown Error message so the UI can show them in a toast.

async function deleteJSON(path: string): Promise<void> {
  const res = await fetch(`/api${path}`, { method: 'DELETE' });
  if (res.status === 401) handleUnauthorised();
  if (res.status === 204) return;
  const data = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((data as { error?: string }).error ?? `${res.status} ${res.statusText}`);
}

// VM lifecycle. Project is implied by the session scope ; an
// unscoped call (no tenant/project selected) returns 400.
export const startVM  = (name: string) => postJSON<{ name: string }>(`/microvms/${encodeURIComponent(name)}/start`, {});
export const stopVM   = (name: string) => postJSON<{ name: string }>(`/microvms/${encodeURIComponent(name)}/stop`,  {});
export const deleteVM = (name: string) => deleteJSON(`/microvms/${encodeURIComponent(name)}`);

// microVM = flavor + image + scheduling policy + network + (optional)
// public ingress. CPU / RAM / disk are properties of the flavor and
// never set independently. SSH keys are pushed at runtime via the
// drawer's "SSH keys" tab — not a create-time field.
export type VMIngressKind = 'none' | 'floating_ip' | 'loadbalancer';

// First-boot provisioning : pull a payload (git repo or OCI artifact),
// run a sh script against it. Materialised on the VM as reserved
// "weft.boot/*" properties so the in-guest weft-vm-agent (which already
// subscribes to the Properties surface) just reads them on its first
// boot, performs the pull / extract, and executes the script through
// mvdan.cc/sh/v3 — POSIX sh in Go, no /bin/sh dependency.
export type VMProvisioningSourceKind = 'none' | 'git' | 'oci';

export interface VMProvisioning {
  source_kind: VMProvisioningSourceKind;
  source_url: string;   // git URL or OCI reference (registry/repo:tag)
  source_ref: string;   // branch / tag / commit SHA / OCI digest ; empty = default
  script: string;       // sh script, executed in the payload's CWD post-pull
}

export interface CreateVMBody {
  Name: string;
  Image: string;
  Flavor: string;          // name from the flavor catalogue
  SchedulingRule?: string; // name of the scheduling rule to honour, empty = no constraint
  Network?: string;        // private network name to attach the NIC to, empty = project default
  IngressKind?: VMIngressKind;
  IngressFloatingIP?: string;   // FIP uuid when Kind=floating_ip ; empty = allocate
  IngressLoadBalancer?: string; // LB uuid when Kind=loadbalancer ; required in that mode
  Provisioning?: VMProvisioning;
}
export const createVM = (b: CreateVMBody) => postJSON<{ name: string; project: string }>('/microvms', b);

export interface CreateVolumeBody {
  Name: string;
  SizeGiB: number;
  Format?: string;
}
export const createVolume = (b: CreateVolumeBody) =>
  postJSON<{ name: string; project: string; size_gib: number }>('/volumes', b);
export const deleteVolume = (uuid: string) => deleteJSON(`/volumes/${encodeURIComponent(uuid)}`);

export const attachVolume = (uuid: string, vmUUID: string) =>
  postJSON<{ volume: string; vm: string }>(`/volumes/${encodeURIComponent(uuid)}/attach`, { VMUUID: vmUUID });

export const detachVolume = (uuid: string) =>
  postJSON<unknown>(`/volumes/${encodeURIComponent(uuid)}/detach`, {});

// ---- Network controller resources (weft-network) ----

export interface CreateRouterBody {
  Name: string;
  Kind: 'peer' | 'egress';
  Backend?: string;             // empty → "wireguard" for peer, "vyos" for egress
  Networks?: string[];
  External?: string;
}
export const createRouter = (b: CreateRouterBody) =>
  postJSON<{ name: string; uuid: string }>('/routers', b);
export const deleteRouter = (uuid: string) =>
  deleteJSON(`/routers/${encodeURIComponent(uuid)}`);

export interface CreateLoadBalancerBody {
  Name: string;
  Mode: 'L4' | 'L7';
  Port: number;
  Backends?: string[];
  AZ?: string;                  // empty → "multi"
}
export const createLoadBalancer = (b: CreateLoadBalancerBody) =>
  postJSON<{ name: string; uuid: string }>('/loadbalancers', b);
export const deleteLoadBalancer = (uuid: string) =>
  deleteJSON(`/loadbalancers/${encodeURIComponent(uuid)}`);
export const setLoadBalancerBackends = (uuid: string, backends: string[]) =>
  putJSON<{ backends: number }>(`/loadbalancers/${encodeURIComponent(uuid)}/backends`, backends);

export interface CreateDNSZoneBody {
  Name: string;
  Role?: 'primary' | 'secondary' | 'forward';
  TTLDefault?: number;
  PushTarget?: string;
}
export const createDNSZone = (b: CreateDNSZoneBody) =>
  postJSON<{ name: string; uuid: string }>('/dns-zones', b);
export const deleteDNSZone = (uuid: string) =>
  deleteJSON(`/dns-zones/${encodeURIComponent(uuid)}`);

export interface CreateDNSRecordBody {
  ZoneUUID: string;
  Name: string;
  Type: string;
  Value: string;
  TTL?: number;
}
export const createDNSRecord = (b: CreateDNSRecordBody) =>
  postJSON<{ uuid: string; name: string; type: string }>('/dns-records', b);
export const deleteDNSRecord = (uuid: string) =>
  deleteJSON(`/dns-records/${encodeURIComponent(uuid)}`);

export const deleteNetwork = (uuid: string) => deleteJSON(`/networks/${encodeURIComponent(uuid)}`);

export interface CreateNetworkBody {
  Name: string;
  CIDR: string;
  Gateway?: string;
  Type?: string;           // "nat" | "overlay" | "wireguard" — empty defaults to "nat" on the daemon
  DNSServers?: string[];
}
export const createNetwork = (b: CreateNetworkBody) =>
  postJSON<{ name: string; project: string; cidr: string }>('/networks', b);

// ---- Scheduling rules (mock store ; no daemon RPC yet) ----

export interface CreateSchedulingRuleBody {
  Name: string;
  Selector: string;
  Count: number;
  AZ?: string;
  Rack?: string;
  Host?: string;
  Project?: string;
}
export const createSchedulingRule = (b: CreateSchedulingRuleBody) =>
  postJSON<Row>('/scheduling-rules', b);
export const deleteSchedulingRule = (name: string) =>
  deleteJSON(`/scheduling-rules/${encodeURIComponent(name)}`);

// ---- Shares (tenant-admin gated) ----

export interface CreateShareBody {
  Name: string;
  Project?: string; // defaults to the session's selected project
  Backend?: string; // empty → "cubefs"
  SizeGB: number;
  ReadOnly?: boolean;
}
export const createShare = (b: CreateShareBody) => postJSON<Row>('/shares', b);
export const deleteShare = (name: string) =>
  deleteJSON(`/shares/${encodeURIComponent(name)}`);

// ---- Security groups ----

export interface SecurityRule {
  direction: 'ingress' | 'egress';
  protocol: 'tcp' | 'udp' | 'icmp' | 'any';
  port_min: number;
  port_max: number;
  remote_cidr: string;
  remote_group_uuid: string;
}
export interface CreateSecurityGroupBody {
  Name: string;
  Description?: string;
  Rules: SecurityRule[];
}
export const createSecurityGroup = (b: CreateSecurityGroupBody) =>
  postJSON<{ name: string; project: string; uuid: string; rules: number }>('/security-groups', b);
export const deleteSecurityGroup = (uuid: string) =>
  deleteJSON(`/security-groups/${encodeURIComponent(uuid)}`);

export const getSecurityGroupRules = (uuid: string) =>
  getJSON<SecurityRule[]>(`/security-groups/${encodeURIComponent(uuid)}/rules`);

export const setSecurityGroupRules = (uuid: string, rules: SecurityRule[]) =>
  putJSON<{ uuid: string; rules: number }>(`/security-groups/${encodeURIComponent(uuid)}/rules`, rules);

// ---- Floating IPs ----

export interface AllocateFloatingIPBody {
  Network: string;
}
export const allocateFloatingIP = (b: AllocateFloatingIPBody) =>
  postJSON<{ uuid: string; address: string; network: string; project: string }>('/floating-ips', b);

export const releaseFloatingIP = (uuid: string) =>
  deleteJSON(`/floating-ips/${encodeURIComponent(uuid)}`);

export const mapFloatingIP = (uuid: string, targetKind: 'vm' | 'lb', targetName: string) =>
  postJSON<{ uuid: string; target: string }>(`/floating-ips/${encodeURIComponent(uuid)}/map`,
    { TargetKind: targetKind, TargetName: targetName });

export const unmapFloatingIP = (uuid: string) =>
  postJSON<unknown>(`/floating-ips/${encodeURIComponent(uuid)}/unmap`, {});

// ---- microVM inspect (status / timings / logs) ----

export interface VMStatus {
  name: string;
  image: string;
  status: string;
  os: string;
  cpu: number;
  mem_mb: number;
  disk_gb: number;
  ip: string;
}
export interface VMTimingEvent {
  name: string;            // e.g. "registered", "vz.state.Running"
  ts: string;              // RFC-3339
  meta: Record<string, string>;
}
export interface VMLogs {
  contents: string;
  total_bytes: number;
}

export const getVMStatus  = (name: string) => getJSON<VMStatus>(`/microvms/${encodeURIComponent(name)}/status`);
export const getVMTimings = (name: string) => getJSON<VMTimingEvent[]>(`/microvms/${encodeURIComponent(name)}/timings`);
export const getVMLogs    = (name: string, tail = 65536) =>
  getJSON<VMLogs>(`/microvms/${encodeURIComponent(name)}/logs?tail=${tail}`);

// ---- SSH-keys catalogue ----
//
// Named, reusable SSH public keys. VMs reference them by name. Source
// tracks provenance ("manual" / "github" / "gitlab" / "forgejo") for
// future import + refresh flows.

export interface SSHKeyEntry {
  name: string;
  public_key: string;
  description: string;
  source: string;
  source_account: string;
  fingerprint: string;
  updated_at: string;
  updated_by: string;
}

export const listSSHKeyCatalogue = () =>
  getJSON<SSHKeyEntry[]>('/ssh-keys');

export const getSSHKeyCatalogue = (name: string) =>
  getJSON<SSHKeyEntry>(`/ssh-keys/${encodeURIComponent(name)}`);

export const setSSHKeyCatalogue = (k: {
  name: string;
  public_key: string;
  description?: string;
  source?: string;
  source_account?: string;
}) => postJSON<SSHKeyEntry>('/ssh-keys', k);

export const deleteSSHKeyCatalogue = (name: string) =>
  deleteJSON(`/ssh-keys/${encodeURIComponent(name)}`);

// Bulk-import : fetch <provider>/<account>.keys server-side, dedupe
// against existing fingerprints, store new entries as
// <provider>:<account>/<index>. forgejoBase is required for forgejo.
export interface ImportSSHKeysResult {
  added: number;
  skipped_existing: number;
  total_seen: number;
  names: string[];
}

export const importSSHKeys = (b: { provider: 'github' | 'gitlab' | 'forgejo'; account: string; forgejo_base?: string }) =>
  postJSON<ImportSSHKeysResult>('/ssh-keys/import', b);

// ---- Per-VM SSH-keys assignments ----
//
// VMs reference catalogue entries by NAME. The server resolves the
// name on read so the SPA gets the full VMSSHKey shape (fingerprint,
// type, public_key, comment) ready to render. Catalogue changes
// propagate : if the operator deletes a catalogue entry, every VM
// referencing it loses access on the next host publish.
//
// Wire to the in-guest weft-vm-agent (NATS subscriber) is unchanged
// — only the SPA + host store handle names.

export interface VMSSHKey {
  name: string;         // catalogue name (new ; was absent in the raw-blob era)
  fingerprint: string;
  type: string;
  public_key: string;
  comment: string;
  added_at: string;     // when this VM was assigned the key
}

export const listVMKeys = (vmName: string) =>
  getJSON<VMSSHKey[]>(`/microvms/${encodeURIComponent(vmName)}/keys`);

// Assign one catalogue entry by name (idempotent).
export const addVMKey = (vmName: string, catalogueName: string) =>
  postJSON<VMSSHKey>(`/microvms/${encodeURIComponent(vmName)}/keys`, { name: catalogueName });

// Replace the entire assignment set ; used by the multi-select picker.
export const setVMKeys = (vmName: string, catalogueNames: string[]) =>
  putJSON<VMSSHKey[]>(`/microvms/${encodeURIComponent(vmName)}/keys`, { names: catalogueNames });

// Remove one assignment by catalogue name.
export const removeVMKey = (vmName: string, catalogueName: string) =>
  deleteJSON(`/microvms/${encodeURIComponent(vmName)}/keys/${encodeURIComponent(catalogueName)}`);

// ---- Per-VM properties (host-set annotations) ----
//
// Free-form key/value bag on a VM. The `guest_readable` flag opts the
// entry into the read-side surface the in-guest weft-vm-agent exposes
// over NATS — host-only metadata (cost-center, security label, …)
// leaves it off.

export interface VMProperty {
  key: string;
  value: string;
  guest_readable: boolean;
  updated_at: string;
}

export const listVMProperties = (name: string) =>
  getJSON<VMProperty[]>(`/microvms/${encodeURIComponent(name)}/properties`);

export const setVMProperty = (name: string, p: { key: string; value: string; guest_readable: boolean }) =>
  postJSON<VMProperty>(`/microvms/${encodeURIComponent(name)}/properties`, p);

export const removeVMProperty = (name: string, key: string) =>
  deleteJSON(`/microvms/${encodeURIComponent(name)}/properties/${encodeURIComponent(key)}`);

// ---- UEFI NVRAM variables ----
//
// Per-VM firmware variables. Keyed by (namespace GUID, name). The
// editor surfaces value as hex — the byte semantics depend on the
// variable (uint16 LE for BootOrder, a UTF-16 + flags blob for
// Boot####, etc.). Empty namespace defaults to the EFI Global GUID
// server-side.

export interface UEFIVar {
  namespace: string;   // GUID
  name: string;
  value_hex: string;
  attributes: string[];
  updated_at: string;
}

export const listUEFIVars = (name: string) =>
  getJSON<UEFIVar[]>(`/microvms/${encodeURIComponent(name)}/uefi-vars`);

export const setUEFIVar = (name: string, v: { namespace?: string; name: string; value_hex: string; attributes: string[] }) =>
  postJSON<UEFIVar>(`/microvms/${encodeURIComponent(name)}/uefi-vars`, v);

export const removeUEFIVar = (vmName: string, namespace: string, varName: string) =>
  deleteJSON(`/microvms/${encodeURIComponent(vmName)}/uefi-vars/${encodeURIComponent(namespace)}/${encodeURIComponent(varName)}`);

// Quota dimension metadata for UI labels + units. Order matters : it
// drives the visual layout. Mirrors internal/server/tenants.go.
export interface QuotaDimMeta {
  key: keyof Quotas;
  label: string;
  unit?: string;
  tenantOnly?: boolean;
}
export const QUOTA_DIMS: QuotaDimMeta[] = [
  { key: 'vcpu',         label: 'vCPU' },
  { key: 'ram_gib',      label: 'RAM',       unit: 'GiB' },
  { key: 'gpus',         label: 'GPUs' },
  { key: 'volumes',      label: 'Volumes' },
  { key: 'volumes_gib',  label: 'Volume capacity', unit: 'GiB' },
  { key: 'shares',       label: 'Shares' },
  { key: 'shares_gib',   label: 'Share capacity',  unit: 'GiB' },
  { key: 'buckets',      label: 'Buckets' },
  { key: 'buckets_gib',  label: 'Bucket capacity', unit: 'GiB' },
  { key: 'registry_gib', label: 'Registry',  unit: 'GiB' },
  { key: 'floating_ips', label: 'Floating IPs' },
  { key: 'projects',     label: 'Projects',  tenantOnly: true },
];
