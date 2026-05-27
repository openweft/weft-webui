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

async function getJSON<T>(path: string): Promise<T> {
  const res = await fetch(`/api${path}`, { headers: { Accept: 'application/json' } });
  if (!res.ok) throw new Error(`${res.status} ${res.statusText}`);
  return res.json() as Promise<T>;
}

export const getResources = () => getJSON<ResourceMeta[]>('/resources');
export const getRows = (id: string) => getJSON<Row[]>(`/resources/${id}`);
export const getSummary = () =>
  getJSON<{ id: string; label: string; count: number }[]>('/summary');

// Upload a container or raw multi-arch image to a registry. The FormData
// carries: type, registry, repository, tag, repeated arch fields, and files.
export async function uploadImage(form: FormData): Promise<Row> {
  return postForm('/api/images/upload', form);
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

async function postForm(path: string, form: FormData): Promise<Row> {
  const res = await fetch(path, { method: 'POST', body: form });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return body as Row;
}
