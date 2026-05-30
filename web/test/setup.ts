// Global Vitest setup — runs before every test file.
//
// Wires @testing-library/jest-dom matchers (toBeInTheDocument,
// toHaveTextContent, …) onto Vitest's expect, so component tests
// can assert on the DOM the same way they would in Jest projects.

import '@testing-library/jest-dom/vitest';
import { afterEach } from 'vitest';
import { cleanup } from '@testing-library/svelte';

// @testing-library/svelte mounts components into document.body —
// cleanup() removes them between tests so a stale node from
// the previous test can't leak into the next assertion.
afterEach(() => {
  cleanup();
});
