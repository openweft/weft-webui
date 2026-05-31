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
import { endpointsEnabled, activeBase, rotate, noteSuccess, endpointCount } from './endpoints';

// Gateway/transport statuses that mean "this DC's webui is not
// answering right now" — worth failing over rather than surfacing as
// an application error. 5xx from the app itself (500) is NOT here : a
// genuine handler error would just repeat on the next DC.
const FAILOVER_STATUS = new Set([502, 503, 504]);

// failoverFetch is the fetch the typed client runs on. In a plain
// browser (endpointsEnabled === false) it is just `fetch`, so nothing
// changes. Inside a native shell it rewrites each request onto the
// active DC origin and, on a transport failure or gateway status,
// rotates to the next healthy DC and retries — up to one attempt per
// known endpoint. The request body is buffered once so POST/PUT can be
// safely replayed across attempts.
async function failoverFetch(input: Request): Promise<Response> {
  if (!endpointsEnabled) return fetch(input);

  const url = new URL(input.url);
  const path = url.pathname + url.search;
  const method = input.method;
  const headers = input.headers;
  const hasBody = method !== 'GET' && method !== 'HEAD';
  const body = hasBody ? await input.clone().arrayBuffer() : undefined;

  let lastErr: unknown;
  // At most one attempt per endpoint; rotate() advances past
  // quarantined ones and returns false when there is nowhere new.
  for (let attempt = 0; attempt < endpointCount(); attempt++) {
    const base = activeBase();
    const req = new Request(base.replace(/\/$/, '') + path, {
      method,
      headers,
      body,
      credentials: input.credentials,
      mode: input.mode,
      redirect: input.redirect,
      signal: input.signal,
    });
    try {
      const res = await fetch(req);
      if (FAILOVER_STATUS.has(res.status) && rotate()) continue;
      noteSuccess(base);
      return res;
    } catch (e) {
      lastErr = e;
      if (rotate()) continue;
      break; // nowhere healthy left — give up and surface the error
    }
  }
  throw lastErr ?? new Error('all weft datacenters unreachable');
}

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

export const client = createClient<paths>({ baseUrl: '', fetch: failoverFetch });
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

// Narrowed bodies (former passthrough). Renamed at re-export so the
// existing api.ts call sites can keep their Me / Quota / Topology
// type aliases.
export type MeBody = components['schemas']['MeBody'];
export type APIQuota = components['schemas']['Quota'];
export type APIScopeEntry = components['schemas']['ScopeEntry'];
export type APITopoNetwork = components['schemas']['TopoNetwork'];
export type APITopoNode = components['schemas']['TopoNode'];
export type APITopologyBody = components['schemas']['TopologyBody'];

// VM inspect — wclient-side typed shapes.
export type APIVMInfo = components['schemas']['VMInfo'];
export type APIVMTimingEvent = components['schemas']['VMTimingEvent'];
export type APIVMLogsResult = components['schemas']['VMLogsResult'];
export type APISecurityRule = components['schemas']['SecurityRule'];
export type APIImportResult = components['schemas']['ImportResult'];

// Tenant / project store views — typed shapes for the quota +
// detail endpoints. Replaces the legacy hand-rolled aliases in
// api.ts.
export type APITenantDetail = components['schemas']['TenantDetail'];
export type APITenantMember = components['schemas']['TenantMember'];
export type APITenantProjectEntry = components['schemas']['TenantProjectEntry'];
export type APITenantGroup = components['schemas']['TenantGroup'];
export type APITenantCaller = components['schemas']['TenantCaller'];
export type APITenantQuotaView = components['schemas']['TenantQuotaView'];
export type APIProjectQuotaView = components['schemas']['ProjectQuotaView'];
export type APITenantUsageView  = components['schemas']['TenantUsageView'];
export type APIQuotas = components['schemas']['Quotas'];

// Storage browser — bucket + share share the same shapes.
export type APIObjectEntry   = components['schemas']['ObjectEntry'];
export type APIObjectListing = components['schemas']['ObjectListing'];
export type APIObjectDetail  = components['schemas']['ObjectDetail'];
export type APIBucketPolicy  = components['schemas']['BucketPolicy'];
export type APIPolicyStatement = components['schemas']['PolicyStatement'];

// Registries — remote-registry catalogue (proxy / replica federation).
export type APIRegistryRemote = components['schemas']['RegistryRemote'];
export type APIRemoteSearchHit = components['schemas']['RemoteSearchHit'];

// Plugins — *-as-a-service modules the cluster can host.
export type APIPlugin = components['schemas']['Plugin'];

// Volumes — editable metadata layer + property bag (orchestration tags).
export type APIVolumeMetadata = components['schemas']['VolumeMetadata'];
export type APIVolumeProperty = components['schemas']['VolumeProperty'];

// Networks — editable metadata layer (description + DNS servers).
export type APINetworkMetadata = components['schemas']['NetworkMetadata'];

// Generic editable metadata (description only) used by routers,
// floating-ips, scheduling-rules — anything whose drawer just needs
// rename + description.
export type APIEditableMetadata = components['schemas']['EditableMetadata'];

// Subnets — per-network sub-resource.
export type APISubnet = components['schemas']['Subnet'];

// Per-VM authorized groups + the derived effective key set.
export type APIAuthorizedGroup = components['schemas']['AuthorizedGroup'];
export type APIEffectiveKey    = components['schemas']['EffectiveKey'];

// Re-export the raw types so call sites can reach for anything not
// aliased above without importing api.gen directly.
export type { paths, components };
