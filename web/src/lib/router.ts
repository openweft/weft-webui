import { readable, derived } from 'svelte/store';

// Minimal hash router : the route is everything between "#/" and any "?".
// A trailing "?key=val&…" carries view-state (e.g. ?detail=<row-key> to
// auto-open a drawer on the resource page). Links can still be plain
// <a href="#/resource-id"> when no parameters are needed.

function rawHash(): string {
  return decodeURIComponent(location.hash.replace(/^#\/?/, ''));
}

function splitPath(h: string): { path: string; params: Record<string, string> } {
  const q = h.indexOf('?');
  if (q < 0) return { path: h, params: {} };
  const path = h.slice(0, q);
  const params: Record<string, string> = {};
  const sp = new URLSearchParams(h.slice(q + 1));
  sp.forEach((v, k) => { params[k] = v; });
  return { path, params };
}

// Internal store : full {path, params} parsed once per hashchange.
const parsed = readable<{ path: string; params: Record<string, string> }>(
  splitPath(rawHash()),
  (set) => {
    const update = () => set(splitPath(rawHash()));
    addEventListener('hashchange', update);
    return () => removeEventListener('hashchange', update);
  },
);

// `route` stays a string for the existing call sites that only care
// about the active resource id. `routeParams` exposes the query bag
// for views that need it (e.g. ResourcePage's detail= drawer).
export const route        = derived(parsed, ($p) => $p.path);
export const routeParams  = derived(parsed, ($p) => $p.params);

export function go(id: string, params?: Record<string, string>) {
  const qs = params
    ? new URLSearchParams(params).toString()
    : '';
  location.hash = id
    ? (qs ? `#/${id}?${qs}` : `#/${id}`)
    : '#/';
}
