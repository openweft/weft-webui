// vitest config kept separate from vite.config.ts so the SPA build
// pipeline stays minimal and tests opt-in only when running `npm run
// test`. We extend the same plugin chain (Svelte + Tailwind) so test
// files can import components without compile errors.

import { defineConfig } from 'vitest/config';
import { svelte } from '@sveltejs/vite-plugin-svelte';

export default defineConfig({
  plugins: [svelte({ hot: false })],
  test: {
    environment: 'jsdom',
    globals: true,
    setupFiles: ['./test/setup.ts'],
    include: ['src/**/*.test.ts', 'test/**/*.test.ts'],
    // Coverage off by default — keep test runs fast. Opt in with
    // `npm run test -- --coverage` when you want a number.
    coverage: {
      reporter: ['text', 'html'],
      include: ['src/lib/**/*.{ts,svelte}'],
      exclude: ['src/lib/api.gen.ts'],
    },
  },
  resolve: {
    // The svelte plugin needs the "browser" conditional resolved so
    // tests + production agree on the same Svelte runtime.
    conditions: ['browser'],
  },
});
