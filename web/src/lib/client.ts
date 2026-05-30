// Typed REST client backed by openapi-fetch + the openapi-typescript
// types generated from the Go huma surface (api.gen.ts).
//
// Two affordances on top of the raw openapi-fetch client :
//
//   * `apiFetch` re-routes 401 responses through the OIDC login flow,
//     same behaviour the legacy api.ts handlers had.
//   * `client.GET / POST / PUT / DELETE` are typed against the spec —
//     bad path, body or query is a compile-time error.
//
// Callers can pull schema types out of api.gen.ts directly :
//
//   import type { components } from './api.gen';
//   type Flavor = components['schemas']['APIFlavor'];
//
// or via the helper aliases in this file.

import createClient, { type Middleware } from 'openapi-fetch';
import type { paths, components } from './api.gen';

// 401 → OIDC redirect. Lives as a middleware so every call site
// inherits the behaviour ; the legacy api.ts's getJSON / postJSON /
// putJSON / deleteJSON all had this inline.
const unauthorizedRedirect: Middleware = {
  async onResponse({ response }) {
    if (response.status === 401) {
      const back = encodeURIComponent(
        location.pathname + location.search + location.hash,
      );
      location.assign(`/api/auth/login?return_to=${back}`);
    }
    return response;
  },
};

export const client = createClient<paths>({ baseUrl: '' });
client.use(unauthorizedRedirect);

// ---- Convenience aliases for the most-used component schemas ----
//
// openapi-typescript stamps generated names from the Go struct
// (huma reads the Go type name). We re-export them with the friendlier
// names the Svelte components actually want to write. Adding a new
// alias here is cheaper than threading components['schemas']['…']
// through ten files.

export type APIFlavor = components['schemas']['APIFlavor'];
export type APIScript = components['schemas']['APIScript'];
export type APISSHKey = components['schemas']['APISSHKey'];
export type APIVMProperty = components['schemas']['APIVMProperty'];
export type APIUEFIVar = components['schemas']['APIUEFIVar'];
export type APIVMSSHKey = components['schemas']['APIVMSSHKey'];

// Re-export the raw types so call sites can reach for anything not
// aliased above without importing api.gen directly.
export type { paths, components };
