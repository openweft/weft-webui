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
  const res = await fetch('/api/images/upload', { method: 'POST', body: form });
  const body = await res.json().catch(() => ({}));
  if (!res.ok) throw new Error((body as { error?: string }).error ?? `${res.status} ${res.statusText}`);
  return body as Row;
}
