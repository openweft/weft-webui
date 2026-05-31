# OIDC smoke test

End-to-end probe of the Dex → weft-webui login flow. Drives the
canonical Authorization Code + PKCE handshake from the command line,
exits non-zero the moment anything diverges from the expected response.

Intentionally a **manual operator drill**, not a CI gate. CI has no live
Dex (the issuer lives in the 3-DC infra cluster, see memory
`weft-up`) so the smoke test is wired into the `Taskfile.yml` rather
than the `.github/workflows/` matrix.

## What it proves

1. `GET /api/auth/login` redirects to the configured Dex issuer's
   authorize endpoint (proves `OIDCConfig.Issuer` + redirect URL are
   wired and reachable).
2. Dex serves its mock-connector login form.
3. POSTing valid mock credentials yields a redirect back to
   `/api/auth/callback?code=...&state=...` (proves the OAuth state +
   PKCE round-trip).
4. The callback exchanges the code, verifies the ID token's signature +
   nonce, and sets a session cookie (proves token exchange, JWKS fetch,
   and HMAC cookie signing).
5. `GET /api/me` with that cookie returns HTTP 200 and a JSON body whose
   `email` field matches the user we logged in as (proves the session
   middleware reads the cookie back and surfaces claims).

## Prerequisites

| Component | Requirement |
| --- | --- |
| `weft-webui` | Running on `$WEBUI_BASE` (default `http://localhost:8080`). NOT in dev mode — `WEBUI_DEV_MODE` must be unset or false, otherwise auth is short-circuited and the test is meaningless. |
| Dex | Reachable at `$DEX_ISSUER` (default `http://localhost:5556/dex`). The mock connector must be enabled (it ships enabled in the default `examples/config-dev.yaml`). |
| Mock creds | `admin@example.com` / `password` are Dex's documented mock-connector defaults. Override with `$OIDC_USER` / `$OIDC_PASS`. |
| Network | The host running the smoke test must reach BOTH the webui listener AND the Dex issuer. Inside the 3-DC cluster that means running from a control-plane VM ; from a laptop, use an SSH port-forward. |

## How to run

### Locally (dev loop)

```sh
# Terminal 1 : Dex with the default config (port 5556)
docker run --rm -p 5556:5556 -v $PWD/examples/config-dev.yaml:/etc/dex/cfg/config.yaml \
    ghcr.io/dexidp/dex:latest dex serve /etc/dex/cfg/config.yaml

# Terminal 2 : weft-webui (NOT run:dual — that's dev-mode no-auth)
WEBUI_OIDC_ISSUER=http://localhost:5556/dex \
WEBUI_OIDC_CLIENT_ID=weft-webui-dev \
WEBUI_OIDC_CLIENT_SECRET=devsecret \
WEBUI_OIDC_REDIRECT_URL=http://localhost:8080/api/auth/callback \
  task build && ./weft-webui

# Terminal 3 : run the smoke test
task oidc-smoke
```

### Against the live 3-DC cluster

```sh
# From a host that can reach both the webui and Dex (e.g. a DC0 control-plane).
export WEBUI_BASE=https://weft.lan
export DEX_ISSUER=https://dex.weft.lan
export OIDC_USER=svc-smoke@weft.lan
export OIDC_PASS='<paste from vault>'
task oidc-smoke
```

## Expected output

```
[step 1] webui=http://localhost:8080 dex=http://localhost:5556/dex user=admin@example.com timeout=15s
[step 1] GET http://localhost:8080/api/auth/login
  → authorize URL: http://localhost:5556/dex/auth?client_id=…&code_challenge=…
[step 2] GET http://localhost:5556/dex/auth?... (follow Dex redirect chain)
  → login form at: http://localhost:5556/dex/auth/mock/login?req=…
[step 3] POST credentials to http://localhost:5556/dex/auth/mock/login?req=…
  → callback URL: http://localhost:8080/api/auth/callback?code=…&state=…
[step 4] GET http://localhost:8080/api/auth/callback?... (token exchange + session cookie)
  → callback set session, redirected to: /
[step 5] GET http://localhost:8080/api/me with session cookie
  → /api/me: sub=… email=admin@example.com name=admin

OK — end-to-end OIDC login succeeded.
```

## Interpreting failures

The first token after `FAIL` is the stage tag — grep this table.

| Stage tag | Likely cause | Where to look |
| --- | --- | --- |
| `login` | Webui's `/api/auth/login` didn't redirect to Dex. Either OIDC isn't wired (check `WEBUI_OIDC_ISSUER`) or webui couldn't reach Dex during discovery at startup. | `journalctl -u weft-webui` for `oidc: discovery` errors. |
| `dex` | Dex didn't render a login form — wrong issuer URL, connector misconfigured, or Dex itself is down. | `curl $DEX_ISSUER/.well-known/openid-configuration` should return 200 JSON. |
| `post` | Mock creds rejected, or callback URL mismatch (Dex returned a non-callback redirect). The most common variant is `redirect_uri did not match` — check `WEBUI_OIDC_REDIRECT_URL` matches the Dex `staticClients[].redirectURIs` entry exactly (scheme + host + port + path). | Dex log for `Invalid redirect_uri`. |
| `callback` | Webui rejected Dex's response — typically ID token verify failure (clock skew > 60s, wrong issuer claim, or JWKS unreachable). | Webui log for `id token verify` / `nonce mismatch`. |
| `me` | Session cookie didn't survive — Secure flag mismatch (running over plaintext HTTP with `Secure=true`), or `/api/me` doesn't recognise the cookie name. | Webui log for `bad session` / `session expired`. |

## When to run

- **After every Dex config change** (new connector, rotated client
  secret, redirect URI edit) — proves the live config still works
  before traffic notices.
- **Before tagging a new weft-webui release** — gate for shipping ;
  catches regressions in the OIDC handlers that the unit suite can't
  see (real JWKS, real cookie behaviour).
- **Whenever an operator suspects login is broken** in production —
  fastest end-to-end probe.
