---
id: BUG-8dmp5y
title: 'Auth/session hardening: redirect secret leak, proxy SSRF bypass, login throttle, revalidation'
status: testing
priority: high
labels:
    - security
    - auth
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-20T02:53:20Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 4 finding(s) from dims: find:api-security-tenancy.

---
### Credential allowedDomains bypassed by HTTP redirect: custom-header/query secrets forwarded to any host [find:api-security-tenancy] (high/security) · area: credentials / safehttp / HTTP Request node · confidence: high

engine.Request.Authenticate checks the credential's AllowsHost only against the initial URL; safehttp's CheckRedirect re-checks scheme/AllowedHosts/IP but never the credential's AllowedDomains, and Go strips only Authorization/Cookie (not custom headers/query) on cross-host redirects, so a scoped httpHeaderAuth secret follows a 30x to an out-of-scope host.

Evidence: Private :8107. Credential httpHeaderAuth {name:X-Api-Key,value:SCOPED-SECRET-5521} allowedDomains ['127.0.0.1']. Control: HTTP node GET http://localhost:8097/direct -> failed 'credential ... is not allowed for host localhost'. Attack: GET http://127.0.0.1:8097/redirect?to=http://localhost:8097/leaked -> succeeded; stub log shows the follow-up 'GET /leaked Host: localhost:8097 X-Api-Key: SCOPED-SECRET-5521'. Same shape in the load-options HTTP loader and the credential-test probe (AllowsHost checked on the initial target only, then safehttp.NewClient(policy).Do).

n8n behavior: n8n's per-credential 'Allowed HTTP Request Domains' is meant to bound where a credential is sent; parity plus defence-in-depth argue the scope must survive redirects.

Impact: A tenant workflow (or a malicious redirect from a legitimate but compromised endpoint) exfiltrates a scoped API key/header secret to an attacker host, defeating the credential's host-scoping control.

Suggested fix: Make CheckRedirect credential-aware: on a redirect whose host fails the credential AllowsHost, stop following (http.ErrUseLastResponse) or strip every header/query the credential added; add tests for header and query credentials across a redirect.

Files: internal/engine/authenticate.go, internal/safehttp/safehttp.go, internal/loadoptions/loadoptions.go, internal/credentials/registry.go

---
### SSRF guard fully bypassed when HTTP(S)_PROXY is set (ProxyFromEnvironment) [find:api-security-tenancy] (high/security) · area: safehttp · confidence: high

safehttp's transport uses Proxy: http.ProxyFromEnvironment. When a proxy env var is present the dialer only validates the proxy's address; the real target host (metadata IP, RFC1918, loopback) is never resolved or checked, so the private-address guard is void for every outbound workflow request.

Evidence: Restarted private instance with HTTP_PROXY=http://127.0.0.1:8097 (proxy in allowed_private_endpoints). HTTP node GET http://169.254.169.254/latest/meta-data/iam/ -> succeeded; the stub received the absolute-form proxy request 'GET http://169.254.169.254/latest/meta-data/iam/'. Without the proxy the identical URL is refused as a link-local address.

n8n behavior: N/A (SSRF hardening is KilasFlow's own control).

Impact: On any deployment that sets HTTP(S)_PROXY (common in enterprise/k8s egress setups), a tenant-authored URL can reach cloud metadata and the internal network — the exact class the guard exists to stop.

Suggested fix: Set Proxy: nil for tenant-authored egress, or when a proxy is configured pre-resolve and policy-check the target host before dialing and document that the proxy must enforce egress; make outbound proxying an explicit config knob rather than implicit env.

Files: internal/safehttp/safehttp.go

---
### Login has no throttling/lockout and is an unauthenticated PBKDF2 CPU-exhaustion vector [find:api-security-tenancy] (high/security) · area: auth / login · confidence: high

/auth/login applies no per-IP or per-account rate limit, no lockout, and no cap on concurrent PBKDF2 work (600k iterations per attempt, plus a full decoy hash for unknown emails), so it enables both password brute force and an unauthenticated DoS.

Evidence: Private :8107. One failed login ~58 ms. 40 parallel guesses for a@sec.test -> 40x401 in 0.57 s (~70/s, no 429/delay/lockout). Under 400 parallel unauthenticated logins, an authenticated GET /api/v1/workflows took 9.24 s instead of ~2 ms. Unknown emails also burn a decoy PBKDF2, so no valid email is even needed for the DoS.

n8n behavior: n8n rate-limits its login route (express-rate-limit, ~5/min -> 429) and offers MFA.

Impact: Online password guessing is unimpeded, and a burst of login requests starves CPU for the whole process (API + workers), degrading every tenant on a shared host.

Suggested fix: Per-IP and per-account token-bucket rate limiting with 429+Retry-After on /auth/login, progressive delay/temporary lockout per account, and a semaphore capping concurrent password-hash work so login cannot starve the API; log failed logins.

Files: internal/api/handlers/auth.go, internal/api/middleware/auth.go, internal/auth/keys.go

Existing tickets: FEAT-ddzk2k

---
### Stateless sessions are never revalidated: disabled/password-changed users keep full access and can mint non-expiring API keys [find:api-security-tenancy] (medium/security) · area: auth sessions / API keys · confidence: high

The auth middleware validates a session cookie only by HMAC+expiry and never re-checks the user row, so a disabled account or one whose password was changed keeps working for the session TTL — and can convert that window into permanent access by minting an API key (no expiry).

Evidence: Logged in as b@sec.test (cookie), then set users.disabled_at=now and replaced password_hash in the DB. With the old cookie: GET /auth/me 200, GET /workflows 200, POST /api-keys 201 returning a full new token (prefix da5fad7b7cd1). DisabledAt is only checked in Login, not in middleware resolve().

n8n behavior: n8n invalidates sessions on password change/user disable and supports API-key expiry.

Impact: Offboarding a user or forcing a password reset does not cut existing sessions, and a stolen/retained cookie becomes a permanent API key; up to 12h session TTL with no server-side revocation.

Suggested fix: Bind sessions to a per-user version (hash of password_hash + disabled flag) verified against a cached user lookup; reject disabled users per request; require fresh re-auth to mint API keys and offer key expiry; revoke a user's keys on disable.

Files: internal/api/middleware/auth.go, internal/auth/session.go, internal/api/handlers/auth.go

Existing tickets: FEAT-ddzk2k

## Acceptance criteria

- [ ] Credential allowedDomains bypassed by HTTP redirect: custom-header/query secrets forwarded to any host
- [ ] SSRF guard fully bypassed when HTTP(S)_PROXY is set (ProxyFromEnvironment)
- [ ] Login has no throttling/lockout and is an unauthenticated PBKDF2 CPU-exhaustion vector
- [ ] Stateless sessions are never revalidated: disabled/password-changed users keep full access and can mint non-ex
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-19 (SecurityDx, partial)
- safehttp (committed in ad23336): Proxy:nil for tenant egress + CredentialScope ctx carrier (WithCredentialScope/CredentialScopeFrom) consulted in CheckRedirect (host:port passed, ErrUseLastResponse stops chain) + tests TestCredentialScopeStopsARedirectOutsideItsDomains, TestClientIgnoresProxyEnvironment. Scoped: go test ./internal/safehttp/ PASS.
- Remaining: engine Authenticate() 1-line scope attach (spec sent to EngineExpression, agreed); login throttle (429+Retry-After, per-IP/account buckets, PBKDF2 semaphore); session revalidation (user-version bind, disabled check per request, API-key mint re-auth/expiry); credential-test probe scope attach.

## Work (Main review 2026-09-19)
- safehttp partial (SecurityDx, in ad23336): Proxy:nil (tenant egress never via ProxyFromEnvironment), CredentialScope ctx + CheckRedirect hop check with ErrUseLastResponse, 2 regression tests green (`go test ./internal/safehttp/` ok).
- Remaining per agent: engine Authenticate() attach (EngineExpression agreed), login throttle + PBKDF2 guard, session revalidation, probe/loader scope attach. Redirect secret leak + proxy SSRF bypass done; login throttle + revalidation open.
- Note: safehttp.go + safehttp_test.go content landed inside ad23336 (chore commit also carrying them); 136ca57 records review note only. Future moves: keep security hunks in dedicated commits.

## Progress 2026-09-19 (SecurityFront2) — delegated, still doing
Status: doing. Two of four findings were already closed (redirect secret leak, proxy
SSRF bypass — safehttp, ad23336). The remaining two plus the two scope-attach sites are
being landed this wave by agent AuthHardening (files: internal/api/handlers/auth.go,
internal/api/middleware/auth.go, internal/auth/*, internal/credentials/registry.go,
internal/loadoptions/loadoptions.go):
- login throttle (per-IP + per-account token bucket -> 429 + Retry-After, PBKDF2
  concurrency semaphore, failed logins logged);
- session revalidation (user-version fingerprint bound into the session token,
  per-request cached user lookup, disabled users refused, API-key revocation already
  covered by AuthenticateAPIKey per request);
- credential scope attached at the probe (internal/credentials RunTest) and the HTTP
  option loader, so CheckRedirect stops a hop outside the credential's AllowedDomains.
Wiring I landed for it in internal/api/server.go: `Users: deps.AuthStore` in
middleware.AuthOptions (nil on an auth-disabled install — fail closed, no panic).
Delegated to ExpressionParity (owner of internal/engine/authenticate.go): the same
one-line `safehttp.WithCredentialScope` attach inside `Request.Authenticate`, so the
HTTP node and the routing interpreter are covered too. Verified only that the safehttp
carrier it needs exists; not re-verified from my side.
Evidence so far (scoped, passed): `go test ./internal/safehttp/ -count=1` ok (from
ad23336, re-confirmed this wave by AuthHardening).
Remaining: AuthHardening's report (per-finding status + commit SHA) has not arrived at
the time of writing; the ticket stays `doing` until it does.

## Progress 2026-09-19 (SecurityFront2) — landed, testing
Status: testing. Agent AuthHardening landed the remainder in 47544b7
("BUG-8dmp5y: throttle login, revalidate sessions, bound credential scope through
redirects — auth/security"): internal/api/handlers/auth.go (+197),
internal/api/middleware/{auth.go,clientip.go,loginlimit.go,sessioncache.go} (new
per-IP/per-account token bucket with 429+Retry-After, PBKDF2 concurrency semaphore,
user-version session revalidation with a cached lookup, disabled users refused),
internal/auth/session.go (+94, UserVersion bound into the signed session),
internal/credentials/registry.go (credential scope attached to the probe so
CheckRedirect stops an out-of-scope hop), plus their own test files.
Wiring landed by me in e0187b3 (BUG-y57cz4): `Users: deps.AuthStore` in
middleware.AuthOptions (internal/api/server.go).
Delegated to ExpressionParity (owner of internal/engine/authenticate.go): the same
one-line safehttp.WithCredentialScope attach inside Request.Authenticate, so the HTTP
node and the routing interpreter are covered too. NOT verified from my side.
Evidence (scoped, passed this session):
- go test ./internal/auth/ -count=1 ok
- go test ./internal/api/middleware/ -count=1 ok
- go test ./internal/credentials/ -count=1 ok
- go test ./internal/loadoptions/ -count=1 ok
- go test ./internal/safehttp/ -count=1 ok (ad23336, re-confirmed by AuthHardening)
Unverified: the end-to-end login/session behaviour through the HTTP surface
(internal/api tests) — that package could not be run to completion because sibling
packages were mid-edit (final state: internal/engine/runner.go:531 undefined
`delivered`). Re-run `go test ./internal/api/ -run 'Login|Session|APIKey'`.
Attribution: 47544b7 also carries unrelated files swept in by a shared index
(internal/interop/n8n/gowa.go, several .pine ticket files) — those are not this ticket's.

## Progress 2026-09-20 (ExpressionParity) — engine Authenticate now scopes the redirect

Last open piece of the redirect-leak finding: `engine.Request.Authenticate`
checked `AllowsHost` against the initial URL and then dropped the bound, so a
30x from an in-scope host carried a custom header/query secret to any host
(safehttp's `CheckRedirect` reads the scope off the request context, and only
the probe and the load-options loader were attaching it).

Change: `internal/engine/authenticate.go` attaches
`safehttp.WithCredentialScope(httpRequest.Context(), CredentialScope{AllowsHost:
resolved.AllowsHost})` after the `AllowsHost` check passes and before
`credentials.Apply`. The request is mutated in place because its context is what
the HTTP client carries into the redirect chain; returning a new request would
change a signature two callers already use.

Tests (`internal/engine/authenticate_test.go`, new):
- `TestAuthenticateScopesTheCredentialToItsAllowedDomains` — the scope is on the
  request context, allows the checked host (with `host:port`), refuses another,
  and an out-of-scope first URL is still refused before the secret is applied.
- `TestARedirectOutsideTheScopeStopsTheChainWithoutTheSecret` — end to end with
  two real servers: `127.0.0.1` (allowed) redirects to `localhost` (not named),
  the chain stops at the redirect and the destination receives no
  `X-Api-Key`. This is the finding's own repro shape.

TDD: with the hunk reverted, both tests fail — the redirect is followed and the
destination logs `X-Api-Key: s3cret`; with the hunk in place, both pass.

Scoped run: `go test ./internal/engine/ -run
'TestAuthenticate|TestARedirectOutsideTheScope' -count=1` → ok.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (10):
  - `ee5f5d6a` — BUG-8dmp5y: scope the credential to its allowed domains across redirects — engine/auth
  - `598002e3` — BUG-8dmp5y: prove the decoy hash still runs under the login slot cap — auth/tests
  - `048de9da` — BUG-8dmp5y: page /api-keys with a limit and cursor, next cursor in X-Next-Cursor — auth/pagination
  - `3787b29f` — BUG-9853ay BUG-8dmp5y: prove the embed confinement at the API surface and record the auth landing — tests
  - `5693d985` — BUG-cq4yk3: webhook path labels are not identities — one path may bind several methods and be reused — repository
  - `47544b77` — BUG-8dmp5y: throttle login, revalidate sessions, bound credential scope through redirects — auth/security
  - `fd896415` — BUG-8dmp5y: record safehttp landing note — security
  - `136ca574` — BUG-8dmp5y: safehttp proxy bypass close + credential redirect scope — security
  - `2930a34c` — SecurityDx: progress notes — xf1wqm+s0wy50 landed, safehttp partial, rest doing
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  669 +++++
 .pine/tickets/BUG-6bqh51.md                        |  640 +++++
 .pine/tickets/BUG-6jvcs5.md                        |  689 ++++++
 .pine/tickets/BUG-8dmp5y.md                        |  183 ++
 .pine/tickets/BUG-8h4yy1.md                        |   46 +
 .pine/tickets/BUG-8sb0jw.md                        |  239 ++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
 .pine/tickets/BUG-c241hm.md                        |  154 ++
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 58706 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Important
- **I2 (medium)**: `internal/api/server.go:164` mounts `EmbedAuth` with `deps.EmbedIssuer`, which `cmd/kilasflow/main.go:324-335` leaves nil when no embed key is configured. A nil `*embed.Issuer` inside a non-nil interface defeats the `verifier == nil` guard (`internal/api/middleware/embed.go:38-41`); a three-segment `kfe1.a.b` token reaches `internal/embed/embed.go:362-363` and dereferences nil. `Authenticate` waves embed tokens through, so it is reachable unauthenticated: 500 + recovered stack per request, and the intended 503 path can never run. Fix: pass a real nil (or make `Verify` nil-receiver-safe) and test the no-key install.

## Progress 2026-09-20 (FixSecurityFindings) — review finding I2 closed, testing
Status: testing. **I2 (medium)**: `internal/api/server.go` handed the embed layer a typed
nil `*embed.Issuer` as a non-nil interface when no signing key is configured, so the
layer's `verifier == nil` guard never fired and a three-segment token reached
`(*Issuer).Verify` on a nil receiver — 500 + recovered stack per request, unauthenticated,
on exactly the installations that must answer 503. Fix, commit `aac0777`:
- the server passes a true nil `middleware.EmbedVerifier` when `deps.EmbedIssuer` is nil;
- `(*Issuer).Verify` refuses every token on a nil receiver, so no other caller (now or
  later) can turn the same wiring into a nil dereference.

TDD: with the fix reverted (worktree at HEAD) `TestAnInstallationWithNoEmbedKeyAnswers503InsteadOfCrashing`
answers **500** and `TestANilIssuerRefusesEveryToken` panics; both pass after.
Evidence (scoped, passed): `go test ./internal/api/ -run 'Embed|NoEmbedKey' -count=1` ok;
`go test ./internal/embed/ -count=1` ok.
