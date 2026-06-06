import { defineConfig } from 'vite';
import { svelte } from '@sveltejs/vite-plugin-svelte';
import tailwindcss from '@tailwindcss/vite';

// resolveRel produces an absolute path relative to this config file
// without pulling in @types/node — we only need the leaf name +
// fileURLToPath via import.meta. Avoids dragging stdlib type packages
// into the typecheck path.
function resolveRel(p: string): string {
  // import.meta.url is a file:// URL with percent-escaped spaces ; the
  // ?.pathname leaves them escaped so Rollup's entry resolver fails on
  // paths under "My Shared Files". decodeURIComponent restores the
  // human-readable path.
  const here = decodeURIComponent(new URL('.', import.meta.url).pathname);
  return here.endsWith('/') ? here + p : here + '/' + p;
}

// Three-portal split : one Vite build that emits THREE separate
// per-portal entry points + a shared assets/ pool.
//
// Layout :
//   web/user/index.html    → dist/user/index.html
//   web/tenant/index.html  → dist/tenant/index.html
//   web/infra/index.html   → dist/infra/index.html
//   src/portals/user.ts    → dist/assets/user-<hash>.js
//   src/portals/tenant.ts  → dist/assets/tenant-<hash>.js
//   src/portals/infra.ts   → dist/assets/infra-<hash>.js
//
// Each shell statically imports only the pages relevant to its
// portal so Rollup tree-shakes the rest. Verified at build time :
// the user entry chunk (~2 kB) is strictly smaller than the tenant
// entry (~3 kB) which is strictly smaller than the infra entry
// (~80 kB) because the infra shell pulls in Plugins / Federation /
// Inventory / NetworkTopology.
//
// The Go binary embeds the whole web/dist tree via //go:embed and
// each listener serves its own sub-FS — see
// internal/server/portals.go : assetsForPortal(). The shared
// assets/ folder lives at the dist root ; the spa.go handler walks
// up one level from the portal subdir to satisfy /assets/* requests
// so the index.html's `/assets/<hash>.js` references resolve to the
// shared pool.
//
// Dev (`npm run dev`) keeps serving the legacy single-page entry
// (web/index.html → src/main.ts → App.svelte) so iterating with HMR +
// the Vite proxy stays a 1-process loop. The portal split only fires
// at production build time.
export default defineConfig(({ command }) => {
  const isDev = command === 'serve';
  return {
    plugins: [svelte(), tailwindcss()],
    server: {
      port: 5173,
      proxy: {
        '/api': 'http://localhost:8080',
      },
    },
    build: isDev
      ? { outDir: 'dist', emptyOutDir: true }
      : {
          outDir: 'dist',
          emptyOutDir: true,
          rollupOptions: {
            input: {
              user: resolveRel('user/index.html'),
              tenant: resolveRel('tenant/index.html'),
              infra: resolveRel('infra/index.html'),
            },
          },
        },
  };
});
