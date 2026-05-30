// Thin client for the weft-webui JSON API. The Go backend currently serves
// mock data ; the shapes here match what it will return once wired to the
// real weft gRPC API, so the UI won't change when that lands.

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
export const getRows = (id: string) => getJSON<Row[]>(`/resources/${id}`);
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

export interface Me {
  sub: string;
  email: string;
  name: string;
  groups: string[];
  project: string;
  initials: string;
  dev: boolean;
  // cluster_admin : OIDC group claim "admin"/"admins" — SUPERADMIN.
  // tenant_admin  : present in at least one Tenant.Admins set —
  //                 ADMIN (delegated, scoped to the user's tenants).
  // Both flags can be true ; the SPA shows SUPERADMIN preferentially.
  cluster_admin: boolean;
  tenant_admin: boolean;
}

// (isAdminUI used to be a heuristic based on which resources the
// listener surfaces. /api/me now carries cluster_admin / tenant_admin
// flags directly — use those instead.)

export const getMe = () => getJSON<Me>('/me');

export async function setProject(project: string): Promise<void> {
  const res = await fetch('/api/session/project', {
    method: 'POST',
    headers: { 'Content-Type': 'application/json' },
    body: JSON.stringify({ project }),
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
