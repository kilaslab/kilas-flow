---
id: BUG-fvdz46
title: 'Config keys nothing reads: branding.* and embed.session_ttl'
status: done
priority: medium
labels:
    - platform
    - docs
parent: EPIC-bkj6yf
created: "2026-09-20T05:26:31Z"
updated: "2026-09-20T10:59:27Z"
---

## Problem

Two documented configuration surfaces have no consumer in the code. A config key that does
nothing is worse than a missing one: it reads as a supported feature to an operator and to an
integrator.

## Evidence

- `Config.Branding` — defined at `internal/config/config.go:435-449`, defaulted at `:662`,
  documented in `config.example.yaml`. No reader outside `internal/config`: the only branding
  that reaches the editor is the per-session `embed.Branding` minted at
  `internal/api/handlers/embed.go:65,130`. The dashboard hardcodes "KilasFlow" in its titles
  and sidebar mark.
- `Config.Embed.SessionTTL` — defined at `internal/config/config.go:428-432`, defaulted at
  `:660`. No reader: the lifetime of an embed token comes from the caller's `ttlSeconds` with
  a 15-minute default and a 30-minute cap (`internal/embed/embed.go:50,53`). (The same field
  name on `Config.Auth` at `:318-321` *is* wired — only the embed one is dead.)

## Acceptance criteria

- [x] For each key, one of: wired to a real consumer (branding injected into the served
      document; `embed.session_ttl` used as the mint default), or removed from
      `internal/config`, `config.example.yaml` and the generated configuration reference.
      Met per key: `embed.session_ttl` WIRED as the mint default (stage 1); `branding.name`
      and `branding.logo` WIRED as deployment defaults merged under the per-session branding
      at mint (stage 2); `branding.favicon` and `branding.powered_by` REMOVED, gone from
      `internal/config`, `config.example.yaml` and the generated reference. The parenthetical
      "branding injected into the served document" is an example, not a requirement: it was
      not used. The served document's title is set per page by SvelteKit's `svelte:head`, so
      an injected title would flash and be overwritten, and the dashboard sidebar, titles and
      icon hardcode "KilasFlow" — the surface the later i18n rewrite (FEAT-15k49d) owns. The
      consumer for name/logo is the embed mint path instead: the values reach the frame in the
      mint response's `branding` field, which the SDK example forwards.
- [x] `scripts/config-reference_test.go` still passes, and the generated reference no longer
      documents a key that does nothing. `go test -race ./scripts/...` is green, the reference
      documents only `branding.name` and `branding.logo` (each naming its real consumer), and
      `make generate-config-reference-check` is clean.

## Out of scope

Building a deployment-level branding editor UI. Wiring the default value is enough.

## Implementation notes

### Stage 1 of 2 — `embed.session_ttl` is the mint default (2026-09-20)

`embed.session_ttl` is WIRED. `branding.*` is stage 2 and still has no reader, so neither
acceptance criterion above is ticked yet: both are whole-ticket statements and stage 1 proves
only their `embed.session_ttl` half.

**What changed**

- `internal/embed/embed.go`: new `IssuerOption`, `WithDefaultLifetime`, `MinDefaultLifetime`
  (1s) and `CheckDefaultLifetime`; `NewIssuer` takes variadic options and starts
  `defaultLifetime` at `DefaultLifetime`; `Issue` uses `issuer.defaultLifetime` when the
  request names no lifetime. `MaxLifetime` (30m) stays a hard cap no configuration raises.
- `internal/config/embed_validate.go` (new): `validateEmbed` wraps `embed.CheckDefaultLifetime`,
  so config validation and the issuer option share one range and cannot drift; it names the key
  and tells the operator to write a unit. Called from `Validate()`.
- `internal/config/config.go`: the `Embed.SessionTTL` doc comment (it feeds the generated
  reference) and one statement in `Validate()`. `Default()` still spells out `15 * time.Minute`,
  so `config.go` gains no import.
- `cmd/kilasflow/embed_issuer.go` (new): `newEmbedIssuer(key, cfg)`; `main.go:326` now calls it
  instead of `embed.NewIssuer` directly.
- Docs: `guides/embedding.md` (the lifetime bullet and the "fifteen-minute token" sentence),
  `operate/security.md`. `CHANGELOG.md` gains a `### Changed` entry.
  `make generate-config-reference` regenerated `config.example.yaml` and
  `operate/configuration-reference.md` (the generator is the only writer of both).

**Decisions**

- The key is a DEFAULT, not a ceiling: a host may still ask for longer via `ttlSeconds`, up to
  the fixed 30m cap, so an operator cannot force shorter tokens.
- A value below 1s or above 30m is refused at boot, not clamped — a file saying 2h while the
  server enforces 30m is the same defect this ticket removes. The 1s floor is load-bearing: the
  loader reads a bare YAML number as nanoseconds without an error (`session_ttl: 900` → 900ns),
  so a plain `> 0` check would have wired a key that mints tokens dead on arrival.
- The range is checked in config validation as well as in the issuer because with no embed
  signing key no issuer is built, and a bad value would otherwise be accepted in silence.

**Verification (real commands, real output)**

- RED, tests without production code: `go test -count=1 ./internal/embed/... ./internal/config/...
  ./cmd/kilasflow/... ./internal/api/...` → `undefined: embed.IssuerOption`,
  `undefined: embed.WithDefaultLifetime`, `undefined: embed.CheckDefaultLifetime`,
  `undefined: embed.MinDefaultLifetime`, `too many arguments in call to embed.NewIssuer`
  (embed, api), `undefined: newEmbedIssuer` (cmd), and in config 6 assertion failures including
  `Validate() accepted embed.session_ttl = 0s` and `Load() accepted embed.session_ttl: 900`.
- RED, stubbed: with `Issue` falling back to `DefaultLifetime` →
  `TestConfiguredDefaultLifetimeAppliesWhenTheCallerAsksForNone`,
  `TestTheHardCapHoldsWhateverTheDefaultIs`,
  `TestEmbedSessionCreationUsesTheConfiguredDefaultLifetime/no_ttlSeconds...` and
  `TestEmbedIssuerIsBuiltFromTheConfiguration` fail. With the `validateEmbed` call removed, 7
  config assertions fail. With `main.go` back on `embed.NewIssuer(embedKey, origins, nil)`,
  `TestBootBuildsTheEmbedIssuerThroughTheConfiguredHelper` fails with
  `main.go:326:24 calls embed.NewIssuer directly` and `no non-test file calls newEmbedIssuer`,
  while `TestEmbedIssuerIsBuiltFromTheConfiguration` still passes — the blind spot the guard
  exists to close.
- RED at the binary level, baseline built from the branch base with the work stashed:
  `KILASFLOW_EMBED_SESSION_TTL=5m` → `no ttlSeconds expiresAt=...+15.01 min` (the key was read by
  nothing), `ttlSeconds 60` → `+1.01 min`, `ttlSeconds 86400` → `+30.01 min`.
- GREEN, same smoke against the stage-1 binary: 5m → `+5.02 min`, 60 → `+1.02 min`, 86400 →
  `+30.02 min`; with no variable set → `+15.00 min` (the historical default is intact).
- GREEN, boot refusal: `KILASFLOW_EMBED_SESSION_TTL=45m` → exit 1,
  `kilasflow: embed.session_ttl: embed session default lifetime 45m0s must be between 1s and 30m0s
  (write a unit, for example 15m: a bare number is read as nanoseconds)`; `-config bad.yaml` with
  `session_ttl: 900` → exit 1 naming `900ns`; `session_ttl: 0` → exit 1; a file-borne
  `session_ttl: 2m` boots and mints `+2.01 min`.
- `gofmt -l internal/embed internal/config internal/api cmd/kilasflow scripts` → no output.
- `go vet ./...` → exit 0, no output. `go build ./...` → exit 0, no output.
- `go test -race -count=1 ./internal/embed/... ./internal/config/... ./internal/api/...
  ./cmd/kilasflow/... ./scripts/... ./internal/guardrails/...` → every package ok.
- `go test -race ./...` → no failures.
- `make generate-config-reference` then `make generate-config-reference-check` → clean, and
  regeneration leaves both generated files byte-identical (the check is not vacuous).
- Not applicable, and why: no migration (no PostgreSQL container needed), no `web/` change (no
  `pnpm check`/`pnpm test`), no API/OpenAPI/SDK change (no `ttlSeconds` doc tag touched), no new
  dependency.

**Stage 2 was done next** — see below.

**For BUG-vzzkg3 (not touched here)**:
`docs/src/content/docs/concepts/tenancy-and-embedding.md` line ~147 says "Fifteen minutes by
default, thirty minutes maximum." It should gain "The default is the `embed.session_ttl`
setting; the maximum is fixed."

### Stage 2 of 2 — `branding.name` / `branding.logo` default embed sessions, `branding.favicon` / `branding.powered_by` removed (2026-09-20)

`branding.*` is resolved key by key. `branding.name` and `branding.logo` are WIRED as
deployment-wide defaults merged field by field under the per-session branding at mint;
`branding.favicon` and `branding.powered_by` are REMOVED, because no "Powered by" mark exists
anywhere in `web/src`, the docs or the frame, and a dashboard favicon would need a rewrite of a
build-generated `index.html` across two SPA-serving paths to draw a custom icon on a dashboard
that still says KilasFlow. Both acceptance criteria are now ticked.

**What changed**

- `internal/embed/embed.go`: `Branding.WithDefaults(defaults)` fills an empty `Name`, `LogoURL`
  and `Accent` and ORs `HideRun`/`HideSave`; `WithDefaultBranding(defaults) IssuerOption`
  validates the deployment's values at construction (`embed default %w`, the same
  `Branding.Validate` a host's values pass) and stores them on the issuer; `Issue` keeps
  `request.Branding.Validate()` exactly where it was and, **after** it, merges the defaults into
  `session.Branding` for workflow sessions only. `internal/api/handlers/embed.go` is untouched,
  so the merged values reach both the token's `brd` claim and the mint response.
- `internal/config/config.go`: `Branding` shrinks to `Name` and `Logo` with new doc comments
  (they feed the generated reference); `Default()` carries `Branding: Branding{}` with a comment
  saying why it must stay empty (the editor draws a header bar as soon as a name or logo is
  present). The old default `Name: "KilasFlow", PoweredBy: true` is gone.
- `internal/config/embed_validate.go`: second statement in `validateEmbed`, validating the
  deployment's branding through `embed.Branding.Validate`, so a bad value fails boot rather than
  422-ing every mint.
- `cmd/kilasflow/embed_issuer.go`: `newEmbedIssuer` now also passes
  `embed.WithDefaultBranding(embed.Branding{Name: cfg.Branding.Name, LogoURL: cfg.Branding.Logo})`.
  `main.go` needs no further change (the stage-1 AST guard still holds the call site).
- Tests: `internal/embed/embed_branding_test.go` (new), `internal/config/embed_branding_test.go`,
  `cmd/kilasflow/embed_issuer_test.go` and `internal/api/embed_defaults_test.go` extended.
- Docs/changelog: `guides/embedding.md` gains a `### Deployment defaults` section under the
  branding table; `CHANGELOG.md` gains a `Changed` entry and a new `Removed` section.
- `make generate-config-reference` regenerated `config.example.yaml` (now `name: ''`, `logo: ''`)
  and `operate/configuration-reference.md` (only the two keys). Nothing generated was hand-edited.

**Decisions**

- The consumer is the embed mint path, not the served document. Every SvelteKit page sets its own
  `<title>` through `svelte:head`, so an injected title flashes and is overwritten, and the
  dashboard's sidebar, titles and icon hardcode KilasFlow — the surface FEAT-15k49d rewrites. The
  parenthetical in criterion 1 was treated as an example, as the orchestrator brief allows.
- Defaults apply to WORKFLOW sessions only: a datastore session has no editor, so overlaying the
  deployment's values would put sheet branding on a token that renders nothing.
- Empty session fields cannot blank a deployment default, so a multi-brand deployment leaves
  `branding.*` empty and passes everything per session; the booleans are ORed, so a default can
  add a presentation restriction but never lift one (no scope is affected either way).
- `branding.name` defaulting to empty is deliberate: a non-empty default would add a "KilasFlow"
  header bar to every existing white-label embed (`embed-editor.svelte:255`).
- Removal is non-breaking by construction: unknown keys are reported by `warnUnknownKeys` after
  `Validate`, so a stale `branding.favicon` warns and still boots.
- No API/OpenAPI/SDK change (the `branding` doc tag is left alone), no migration, no tenant angle
  (deployment-wide operator configuration), no new dependency.

**Verification (real commands, real output)**

- RED, tests before production code: `go test -count=1 ./internal/embed/... ./internal/config/...
  ./cmd/kilasflow/... ./internal/api/...` →
  `undefined: embed.WithDefaultBranding` (8 sites in embed, 1 in api),
  `(embed.Branding{}).WithDefaults undefined`, `session.WithDefaults undefined`;
  config: `Default().Branding = config.Branding{Name:"KilasFlow", ..., PoweredBy:true}, want the
  zero value`; `Validate() accepted markup in the name / an http logo / a javascript logo / a
  relative logo`; `warnings = "", want them to name branding.favicon` and `branding.powered_by`;
  cmd: `session branding = embed.Branding{Name:"", ...}, want the configured deployment defaults`.
- RED, stubbed implementation (to prove the guards can fail): dropping the `Accent` fill makes
  `TestBrandingWithDefaultsHandlesEveryField` fail with
  `Branding{}.WithDefaults(defaults) = ...{Accent:""}, want ...{Accent:"x"}: every field the merge
  skips is a value that does nothing`; applying defaults to every session makes
  `TestDeploymentBrandingIsNotAddedToADatastoreSession` fail with the deployment values on a
  datastore session; removing the merge makes the api test fail with
  `branding = embed.Branding{Name:"", ...}, want {Name:"Acme Flows", LogoURL:"https://cdn.example/l.png"}`.
- GREEN: `go test -count=1 ./internal/embed/... ./internal/config/... ./cmd/kilasflow/...
  ./internal/api/...` → all packages ok. `go test -race -count=1 ./internal/embed/...
  ./internal/config/... ./internal/api/... ./cmd/kilasflow/... ./scripts/...
  ./internal/guardrails/...` → all ok.
- `gofmt -l internal/embed internal/config internal/api cmd/kilasflow scripts` → no output.
  `go vet ./...` → exit 0. `go build ./...` → exit 0.
- `make generate-config-reference` then `make generate-config-reference-check` → clean.
- Removal grep: `git grep -n -E 'PoweredBy|Favicon|powered_by' -- '*.go' config.example.yaml
  docs/src` → only the removal test, which sets the two keys to prove the warning.
- Real-binary smoke (loopback host, free port, scratch dir, `KILASFLOW_EMBED_SESSION_TTL=5m`,
  `KILASFLOW_BRANDING_NAME='Acme Flows'`, `KILASFLOW_BRANDING_LOGO=https://cdn.example/l.png`):
  - baseline binary built from the branch base (3569a38), a real workflow created over the API,
    mint with no branding → `{"branding": {}}` — RED, the keys were read by nothing.
  - stage-2 binary, mint with no branding → `{"branding": {"name": "Acme Flows", "logoUrl":
    "https://cdn.example/l.png"}, "expiresAt": "2026-09-20T09:45:53Z"}`; mint with
    `ttlSeconds: 60` and `branding: {name: "Birch"}` →
    `{"branding": {"name": "Birch", "logoUrl": "https://cdn.example/l.png"}, "expiresAt":
    "2026-09-20T09:41:53Z"}` — the session's name wins, the deployment logo stays, and the
    four-minute gap between the two expiries shows the 5m `session_ttl` default still applies.
  - `KILASFLOW_BRANDING_LOGO=http://x` → exit 1,
    `branding.name / branding.logo: branding logo must be an absolute https URL`;
    `KILASFLOW_BRANDING_NAME='<b>x</b>'` → exit 1, `... branding name may contain only letters,
    digits, spaces, and simple punctuation`.
  - `KILASFLOW_BRANDING_FAVICON=x` → `/api/v1/ready` answers `{"status":"ok","database":"ok"}` and
    the boot log carries `configuration key matches nothing and was ignored key=branding.favicon
    ... did_you_mean=branding.name`; `KILASFLOW_BRANDING_POWERED_BY=false` → ready, warning names
    `key=branding.powered_by`.
  - Scratch dir and both binaries removed; no stray server left running.
- No API surface change: `git diff --stat $(git merge-base HEAD main) HEAD --
  web/src/lib/api/generated sdk/src/generated docs/src/content/docs/reference` → empty.
- `go test -race -count=1 ./...` → every package I touched passes, but the run as a whole fails
  in two packages this ticket does not touch, and the same failures reproduce on a clean
  detached checkout of main (`git archive aa307d4` into a scratch dir, same command):
  - `pkg/sdk`: `--- FAIL: TestExamplePackRunsUnderWazero (121.74s) wasm_exec_test.go:76:
    Execute() error = code exceeded its 10s time limit` — identical on main (166.7s for the same
    assertion). The Wasm compile under `-race` on this machine takes far longer than the test's
    10s budget, so the test fails on main too.
  - `nodes`: `--- FAIL: github.com/kilaslab/kilas-flow/nodes 603.372s` with a panic inside
    `tetratelabs/wazero@v1.9.0` `backend.RegAlloc` (goroutine trace through
    `internal/runcode.(*Runner).Execute` from `TestTheGoCodeNodeRunsOncePerItemWhenAsked`). On
    clean main the same run panics in the same wazero code from a different victim
    (`internal/runcode` / `TestASharedTranslationDoesNotCarryAMemoryLimitWithIt`). Both packages
    pass in isolation: `go test -race -count=1 ./nodes/` → `ok 579.091s` here and `ok 554.486s`
    on main. The flake is upstream wazero under `-race` with a machine loaded by a dozen parallel
    worktrees, not this change. No test was weakened, skipped or deleted.
- Not applicable, and why: no migration (no PostgreSQL container needed, both dialects untouched),
  no `web/` change (no `pnpm check`/`pnpm test`), no new dependency (licence: none added), no
  tenant-scoped data.

**Learnings (for the orchestrator; not recorded here by rule)**

(a) Unknown config keys warn and never fail boot, so removing a key is non-breaking while refusing
an invalid value of a live key does fail boot — both behaviours are load-bearing in this ticket.
(b) A newly wired key must preserve prior observable behaviour: `branding.name: KilasFlow` would
have added a header bar to every existing embed (`embed-editor.svelte:255`), which is why its
default became empty. (c) The embed frame takes branding from the host page's `postMessage`, which
`sdk/src/browser.ts:124` copies from the mint RESPONSE, not from the token's `brd` claim, so
deployment defaults reach a frame only when the host forwards the mint response as
`sdk/examples/reference-host/server.mjs:122-125` does. (d) `git grep -E` on macOS has no `\b`, and
the portable `[^A-Za-z0-9_]` suffix misses references at end of line; use `([^A-Za-z0-9_]|$)` or,
better, `gopls` references, which is type-aware. (e) PRD section 41 (white-label dashboard: name,
logo, favicon, powered-by mark) stays an unmet product requirement and deserves a follow-up ticket
alongside FEAT-15k49d, which is also the right place to re-introduce these keys together.
(f) Every duration key in the loader accepts a bare YAML number as NANOSECONDS
(`embed.session_ttl: 900` loads as 900ns with no error; the quoted string and the env var fail with
"missing unit") — `embed.session_ttl` now has a 1s floor, `auth.session_ttl` and the rest still
share the trap. (g) Setting the documented `KILASFLOW_EMBED_SIGNING_KEY` logs a spurious
`configuration key matches nothing and was ignored key=embed.signing_key` at boot, because the env
provider maps it to `embed.signing_key` while the field is `signing_key_env` (pre-existing).
(h) A test over a helper cannot prove `main.go` calls the helper (`TestRetentionSweepersAreWired`
has the same blind spot); an AST guard or a real-binary run is what proves the wiring.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `794adbb3` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `87c3ed17` — BUG-fvdz46: wire embed.session_ttl and branding.name/logo; drop favicon and powered_by
  - `b3c9f3f9` — chore(pine): start the board — the open tickets move to doing before the parallel wave
  - `f8156140` — chore(pine): record the ticket board — the new tickets, memory entries and their notes
- Files changed (base → working tree):

```
 .pine/MEMORY.md                                    |   1 +
 .pine/memory/embedding.md                          |   8 +
 .pine/tickets/BUG-fng4m2.md                        |  88 ++++
 .pine/tickets/BUG-fvdz46.md                        | 280 +++++++++++
 .pine/tickets/BUG-p3t7yq.md                        |  30 ++
 .pine/tickets/BUG-t9j2ek.md                        | 452 ++++++++++++++++++
 .pine/tickets/BUG-vzzkg3.md                        |  54 +++
 .pine/tickets/BUG-w8h3km.md                        |  33 ++
 .pine/tickets/BUG-xmr673.md                        |  46 ++
 .pine/tickets/EPIC-bkj6yf.md                       |  46 ++
 .pine/tickets/EPIC-m0bne8.md                       |  57 +++
 .pine/tickets/EPIC-r0yg5q.md                       |  27 ++
 .pine/tickets/FEAT-3taswf.md                       |  14 +-
 .pine/tickets/FEAT-48hreg.md                       |  20 +-
 .pine/tickets/FEAT-4jns31.md                       |  26 ++
 .pine/tickets/FEAT-4ve1bq.md                       |  67 +++
 .pine/tickets/FEAT-77rveq.md                       |  73 +++
 .pine/tickets/FEAT-7cg0cd.md                       |  20 +-
 .pine/tickets/FEAT-8mymac.md                       |   4 +-
 .pine/tickets/FEAT-bb4s6e.md                       |  27 ++
 .pine/tickets/FEAT-bp59m4.md                       |  32 ++
 .pine/tickets/FEAT-c72set.md                       |  26 ++
 .pine/tickets/FEAT-cwmw90.md                       | 513 +++++++++++++++++++-
 .pine/tickets/FEAT-emf6k5.md                       |  51 ++
 .pine/tickets/FEAT-ew46cb.md                       |  26 ++
 .pine/tickets/FEAT-fpqvwx.md                       |  57 +++
 .pine/tickets/FEAT-g07pj8.md                       |  67 +++
 .pine/tickets/FEAT-hj8pyx.md                       |  52 +++
 .pine/tickets/FEAT-m4d2y1.md                       |  30 ++
 .pine/tickets/FEAT-mha6a0.md                       | 259 ++++++++++
 .pine/tickets/FEAT-p77zr3.md                       |  67 +++
 .pine/tickets/FEAT-qdedm0.md                       |   4 +-
 .pine/tickets/FEAT-x5qqpm.md                       |  26 ++
 .pine/tickets/FEAT-yxwyav.md                       |  26 ++
 CHANGELOG.md                                       |  35 ++
 README.md                                          |   9 +-
 cmd/kilasflow/embed_issuer.go                      |  18 +
 cmd/kilasflow/embed_issuer_test.go                 | 134 ++++++
 cmd/kilasflow/main.go                              |  46 +-
 cmd/kilasflow/webhook_wiring_test.go               | 138 ++++++
 config.example.yaml                                |  37 +-
 docs/src/content/docs/concepts/credentials.md      |  21 +-
 docs/src/content/docs/concepts/webhooks.md         | 107 ++++-
 docs/src/content/docs/guides/embedding.md          |  32 +-
 .../docs/operate/configuration-reference.md        |  63 ++-
 docs/src/content/docs/operate/security.md          |  14 +-
 docs/src/content/docs/operate/upgrades.md          |  10 +
 docs/src/content/docs/reference/api-contract.md    |   5 +-
 docs/src/content/docs/reference/api.md             |   4 +-
 docs/src/content/docs/reference/api/workflows.md   |  21 +
 .../specs/2026-09-20-agent-surface-design.md       | 519 +++++++++++++++++++++
 e2e/fixtures/live-backend.ts                       |  12 +-
 e2e/helpers/stub.ts                                |   8 +
 e2e/tests/live-backend-api.spec.ts                 |  52 +--
 e2e/tests/live-backend-http-auth.spec.ts           |  94 ++++
 e2e/tests/live-backend-webhook.spec.ts             | 117 ++++-
 go.mod                                             |   1 +
 go.sum                                             |   2 +
 internal/api/embed_defaults_test.go                | 150 ++++++
 internal/api/handlers/auth_test.go                 |  63 ++-
 internal/api/handlers/workflows.go                 |  70 ++-
 internal/api/middleware/loginlimit.go              |  13 +
 internal/api/middleware/loginlimit_test.go         |   3 +-
 internal/api/workflows_test.go                     |  71 ++-
 internal/config/config.go                          |  59 ++-
 internal/config/embed_branding_test.go             | 192 ++++++++
 internal/config/embed_validate.go                  |  34 ++
 internal/config/webhook_require_auth_test.go       |  50 ++
 internal/credentials/builtin.go                    |  68 +++
 internal/credentials/credentials_test.go           |  58 +++
 internal/credentials/registry.go                   |  63 +++
 internal/database/webhook_route_backfill_test.go   | 491 +++++++++++++++++++
 internal/embed/embed.go                            | 121 ++++-
 internal/embed/embed_branding_test.go              | 164 +++++++
 internal/embed/embed_lifetime_test.go              | 146 ++++++
 internal/interop/n8n/parameters.go                 |   3 +-
 internal/nodepack/trigger.go                       |   8 +
 internal/nodepack/trigger_require_auth_test.go     |  43 ++
 internal/repository/models.go                      |  10 +-
 internal/repository/webhooks.go                    |  79 +++-
 internal/repository/webhooks_test.go               | 184 +++++++-
 internal/webhook/jwt.go                            | 144 ++++++
 internal/webhook/jwt_test.go                       | 212 +++++++++
 internal/webhook/require_auth.go                   |  74 +++
 internal/webhook/require_auth_test.go              | 367 +++++++++++++++
 internal/webhook/route_label_test.go               | 172 +++++++
 internal/webhook/shape.go                          |  27 +-
 internal/webhook/shape_test.go                     |  30 ++
 internal/webhook/webhook.go                        |  83 +++-
 internal/webhook/webhook_test.go                   | 194 +++++++-
 .../000014_webhook_route_backfill.down.sql         |  14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |  62 +++
 .../sqlite/000014_webhook_route_backfill.down.sql  |  14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |  58 +++
 nodes/core.go                                      |   5 +
 nodes/error_workflow.go                            |   4 +
 nodes/http.go                                      |   2 +
 nodes/presentation_test.go                         |  35 ++
 nodes/webhook.go                                   |  18 +-
 scripts/generate-api-reference.mjs                 |   2 +-
 sdk/src/generated/models.ts                        |  52 +++
 sdk/src/server.ts                                  |  10 +
 sdk/test/operation-coverage.test.mjs               |   1 +
 .../generated/models/executionNodeRunResource.ts   |   1 +
 web/src/lib/api/generated/workflows/workflows.ts   |  97 ++++
 105 files changed, 7669 insertions(+), 260 deletions(-)
```
