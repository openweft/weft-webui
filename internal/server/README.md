# `internal/server` — webui HTTP layer

The Go side of weft-webui : OIDC auth, two listeners (user + admin), and a
typed REST surface backed by [huma]. Mounts the Svelte SPA from
[`embed.FS`][embed] under `/` and the JSON API under `/api/`.

[huma]: https://huma.rocks/
[embed]: ../../main.go

```
┌─────────────────────────────────────────────────────────────────┐
│  weft-webui (one Go binary, two listeners)                      │
│                                                                 │
│  http(s)://<host>:8080   ── user listener   (ScopeUser)         │
│  http(s)://<host>:8088   ── admin listener  (ScopeAdmin)        │
│                                                                 │
│  Both share the same code path ; scope drives which huma        │
│  operations get registered (cluster-admin endpoints aren't even │
│  acknowledged on the user listener — 404 instead of 403).       │
└─────────────────────────────────────────────────────────────────┘
                              │
                              │ gRPC over unix socket
                              ▼
                      weft-agent (registries,
                      lifecycle, RBAC)
```

## File layout

The package mixes generations. New code lives in `api_*.go` files (one per
feature area, each registering huma operations). Legacy support code lives in
plain-named files (`tenants.go`, `objectstorage.go`, …) and contains :

* in-memory mock stores (used when no live agent is wired)
* shared helpers (`hideHTTPErr`, `resolveVMProjectCtx`, …)
* domain types referenced by the typed huma bodies

```
api.go                  central mountAPI(mux, scope) ; wires every feature area
api_flavors.go          /api/flavors
api_scripts.go          /api/scripts
api_sshkeys.go          /api/ssh-keys (catalogue + bulk import)
api_microvm_metadata.go /api/microvms/{name}/{properties,uefi-vars,keys}
api_microvms.go         /api/microvms/* (create/start/stop/delete/status/timings/logs)
api_networking.go       /api/{networks,security-groups,floating-ips,routers,
                                  loadbalancers,dns-zones,dns-records,
                                  scheduling-rules,network-topology}
api_storage.go          /api/{volumes,shares,buckets} + browser
api_tenants.go          /api/{tenants,projects,quotas,me,summary}
api_misc.go             /api/{healthz,readyz,resources,registry/upload}

server.go               buildHandler / scopedRowCount / writePage / SPA mount
middleware.go           security headers, request-id, slog, metrics, panic recovery
                        — outermost wrappers, runs before huma's mux
events.go               /api/events (SSE — outside huma)
tenants.go              tenantStore : mock multi-tenancy, TenantDetail/TenantQuotaView
quotas.go               typed Quota/QuotaDim helpers
objectstorage.go        bucket store + S3-style policy evaluator
shares*.go              POSIX share store
flavors.go scripts.go sshkeys_*.go vm_metadata.go sshkeys.go
                        mem stores + live-first wrappers per registry
registry.go             generic resource catalogue (used by /api/resources/{id})
                        — the polymorphic dispatcher
```

## Two patterns to know

### 1. huma operation = typed input + typed output

Every operation lives in some `api_*.go` file and looks like :

```go
huma.Register(api, huma.Operation{
    OperationID: "set-vm-property",
    Method:      "POST",
    Path:        "/api/microvms/{name}/properties",
    Summary:     "…",
    Tags:        []string{"microvms", "properties"},
}, func(ctx context.Context, in *setVMPropertyInput) (*setVMPropertyOutput, error) {
    // … live-first then mem fallback …
})
```

Validation tags (`minLength`, `maxLength`, `enum`, `minimum`, …) on the input
struct surface in the OpenAPI **and** become 422 errors before the handler
runs. Bad path / wrong body shape = compile error on the Svelte side
(`openapi-typescript` ingests the spec).

### 2. live-first → mem fallback

The webui ships with in-memory mock stores so the dashboard works without
a running weft-agent. When `live` (the `wclient.Client`) is wired :

```go
if live != nil {
    rows, _, err := live.ListVMProperties(ctx, in.Name, in.Project, …)
    if err == nil { /* use it */ }
    if !wclient.IsUnimplemented(err) {
        return nil, huma.Error502BadGateway(…)
    }
}
// fall through to the mem store
```

`Unimplemented` is the daemon saying "I don't speak this RPC yet" — silent
fallback to the seed lets the SPA stay functional through staged rollouts.
Any other gRPC error is surfaced (502).

## OpenAPI pipeline

The OpenAPI 3.1 spec is the contract between Go and TypeScript. Everything
flows from the `api_*.go` operations :

```
internal/server/api_*.go
        │ huma reflects the typed structs
        ▼
internal/server/api.go : mountAPI(mux, ScopeAdmin)
        │ tools/dump-openapi marshals the spec
        ▼
web/openapi.json                          ← committed snapshot (linguist-generated)
        │ npm run gen:api → openapi-typescript
        ▼
web/src/lib/api.gen.ts                    ← committed (linguist-generated)
        │ openapi-fetch
        ▼
web/src/lib/client.ts                     ← typed client + schema aliases
        │
        ▼
web/src/lib/api.ts                        ← helpers Svelte components import
```

The drift guard (`task check:drift`) regenerates the two committed files and
fails when they diverge from `internal/server/api_*.go`. Catches the "forgot
to commit the regenerated TS client" class of bug at PR time.

## Routes that DON'T go through huma

Documented in [api.go]'s header. Summary :

* `/api/auth/{login,callback,logout}` — OIDC 302 redirects
* `/api/events` — SSE stream (huma's streaming story is heavier than what
  we need here)
* `/api/session/scope` — handler exported by the auth package
* `/metrics` — Prometheus handler (opaque)
* `/` and below — SPA static (embedded build)

[api.go]: ./api.go

## Tests

Two flavours :

* **Unit / per-package** : implicit via the broader Go test suite.
* **E2E** ([e2e_test.go]) : stands up the full middleware chain on
  `httptest.NewServer` and hits a representative sample of the surface.
  Catches regressions a typo in middleware ordering or scope-gated
  registration would slip past unit tests. Runs in ~30 ms.

[e2e_test.go]: ./e2e_test.go

## Adding a new endpoint — workflow

1. Pick the right `api_<area>.go` file (or add a new one and wire it from
   `api.go`'s `mountAPI`).

2. Define `Input` + `Output` struct types (validation via tags). Use
   typed bodies — `passthroughOutput` is a transitional escape hatch
   that should NOT be the first reach.

3. Write the handler. Live-first if the registry has a wclient method ;
   fall back to the mem store.

4. Run `task gen-api` to regenerate `web/openapi.json` +
   `web/src/lib/api.gen.ts`. Or just run `task check` — it triggers
   gen-api as a dep.

5. Add a Svelte helper in `web/src/lib/api.ts` using the typed client :

   ```ts
   import { client } from './client';
   export const fooBar = async (x: string) => {
     const { data, error } = await client.GET('/api/foo/{x}', { params: { path: { x } } });
     if (error) throw new Error(toMsg(error));
     return data;  // typed, no cast
   };
   ```

6. Commit. The drift guard verifies the snapshot+client are in sync.

## Common gotchas

* **Nullable arrays from openapi-typescript** : `T[] | null` because OpenAPI
  doesn't forbid null. Coerce at the helper boundary
  (`return data ?? []`) ; or override the type in `api.ts` with
  `Omit<APIFoo, 'items'> & { items: T[] }`.

* **PascalCase legacy bodies** : the codebase used to translate
  `{ Name: 'x' }` → `{ name: 'x' }` at the wire. That's gone now ;
  the wire is snake_case end-to-end and `CreateXBody` types in
  `api.ts` carry the snake_case field names directly.

* **`$schema` field leaks** : openapi-typescript adds a `$schema?: string`
  optional field to every component. When you do `keyof Quotas`, that field
  leaks in. Strip it with `Omit<APIQuotas, '$schema'>` on the alias.

* **scope-gated registration** : admin-only operations are registered only
  when `scope == ScopeAdmin`. Don't sprinkle 403s inside the handler when
  the right gate is "the user listener doesn't even publish the operation."
