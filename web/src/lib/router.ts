import { readable } from 'svelte/store';

// Minimal hash router : the route is whatever follows "#/". An empty route
// is the overview. Links are plain <a href="#/resource-id"> ; refresh and
// deep-links work because the Go server falls back to index.html.
function current(): string {
  return decodeURIComponent(location.hash.replace(/^#\/?/, ''));
}

export const route = readable<string>(current(), (set) => {
  const update = () => set(current());
  addEventListener('hashchange', update);
  return () => removeEventListener('hashchange', update);
});

export function go(id: string) {
  location.hash = id ? `#/${id}` : '#/';
}
