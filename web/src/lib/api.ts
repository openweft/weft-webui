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

// ---- Object storage (CubeFS S3) ----

export interface ObjectEntry {
  name: string;
  key: string;
  size: number;
  sizeHuman: string;
  modified: string;
  contentType: string;
}

export interface ObjectListing {
  bucket: string;
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

export const listObjects = (bucket: string, prefix = '') =>
  getJSON<ObjectListing>(`/buckets/${bucket}/objects?prefix=${encodeURIComponent(prefix)}`);

export const getObject = (bucket: string, key: string) =>
  getJSON<ObjectDetail>(`/buckets/${bucket}/object?key=${encodeURIComponent(key)}`);

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

export const uploadObjects = (bucket: string, form: FormData) =>
  postForm(`/api/buckets/${bucket}/objects`, form);

async function postForm(path: string, form: FormData): Promise<Row> {
  const res = await fetch(path, { method: 'POST', body: form });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return body as Row;
}
