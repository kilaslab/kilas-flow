---
id: BUG-b3p8va
title: 'Embedded editor cannot save or run from a cross-origin host page: every write returns 403 ''not allowed from that origin'''
status: done
priority: critical
labels:
    - embed
    - security
    - regression-risk
parent: EPIC-8rbys7
created: "2026-09-23T02:03:02Z"
updated: "2026-09-23T04:23:49Z"
---

# Description

Embedding into another product is KilasFlow's core use case, and it is broken in the documented configuration. The editor iframe is served from KilasFlow's own origin, so its write requests carry `Origin: <kilasflow origin>`. `EmbedAuth` compares that with the session's host-page origin and denies the request. Reads work only because same-origin GETs send no Origin header. The e2e test covers the handshake, never a save or a run.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-1). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a. The documented golden path is "host page on its own origin, iframe from KilasFlow".

# Steps to Reproduce

1. Start KilasFlow with auth on, `KILASFLOW_EMBED_SIGNING_KEY` set, and `KILASFLOW_EMBED_ALLOWED_ORIGINS=http://localhost:4273`. Mint a tenant key with the operator key (`POST /api/v1/tenants/default/api-keys`).
2. Run `sdk/examples/host-page` unchanged, with `PORT=4273`, `KILASFLOW_URL=http://127.0.0.1:18190` and the tenant key. The SDK is copied from `sdk/dist` because it is not on npm.
3. Open http://localhost:4273. The editor mounts and shows `editor: ready`.
4. Click Execute. Rename the Set node and click Save. Click Save again.
5. Replay the run with curl and the same token, varying only the `Origin` header.

# Expected

An editor iframe served from the KilasFlow origin can save and run within its scopes. The per-request Origin check still refuses a token that was copied into a foreign page.

# Actual

The editor shows "Run failed: 403 — This embed session is not allowed from that origin." and "Save failed: 403 — This embed session is not allowed from that origin." The server log shows `POST …/run status=403` and `PUT /api/v1/workflows/wf_… status=403` twice. The curl matrix gives `Origin: http://127.0.0.1:18190` (the iframe's own origin) → 403, `Origin: http://localhost:4273` → 202, and no Origin header → 202. GET requests work because browsers omit `Origin` on same-origin GETs. The iframe's POST and PUT requests always carry the KilasFlow origin, and the middleware compares that origin with the host page's origin stored in the session.

# Acceptance Criteria
- [x] An embed session minted for host origin H saves and runs a workflow from the KilasFlow iframe mounted on H (the origin check accepts the editor's own origin for requests carrying the session token, or it validates the frame ancestor instead)
- [x] A token copied into a *third* origin is still refused (the security intent of the per-request check is preserved)
- [x] A Playwright e2e test with the host page on a different port than the server saves, runs and receives `execution-finished`; it fails on the old behaviour
- [x] `sdk/examples/host-page` works end to end

# Implementation Plan

Also accept the server's own origin (`server.public_url`, or the request's own scheme and host) in the per-request check, or check `Sec-Fetch-Site: same-origin` from the frame. Add an e2e test that saves and runs through a cross-origin host page. Until the fix lands, the embed quickstart cannot be documented truthfully.

# Notes

Related tickets: BUG-8h4yy1, FEAT-900msn

Related (from the audit): FEAT-900msn (done; its browser proof only checked that the Save/Run controls were present), BUG-8h4yy1 (done; the reporter notes it was "NOT verified end-to-end"). This is not a regression; the behaviour was never proven.

## Progress — the frame names its verified parent (2026-09-23)

The origin check now has a third branch. Origin absent passes, and the session's own host origin passes, as before. KilasFlow's own origin passes only when the frame's `X-KilasFlow-Embed-Parent` header names the session's host and `Sec-Fetch-Site`, if present, is `same-origin`. Everything else gets the same 403 sentence.

- Server: `originPermitted` in `internal/api/middleware/embed.go`. `EmbedAuth(verifier, publicURL)` is wired from `cfg.Server.PublicURL` in `server.go`. The own origin is `embed.SelfOrigin(r, publicURL)`, which is built on `embed.SelfURL`. The OAuth redirect now uses `SelfURL` too, so its behaviour is unchanged, public_url path included.
- Frame: `setEmbedToken(token, parent)` stores the `event.origin` that `acceptEmbedSession` already verified. `apiFetch` and `apiDownload` send it through one `attachEmbedHeaders` helper.
- CORS is unchanged. The header matters only when `Origin` is KilasFlow's own, and a host page's request never has that. A preflight echoes whatever headers it names anyway.
- Docs: the embedding guide covers the frame's origin and has a `KILASFLOW_SERVER_PUBLIC_URL`-behind-a-proxy checklist item. The tenancy concept page gets the same rule. The `server.public_url` comment has a paragraph on it, and the configuration reference was regenerated.

Proof:
- Go: `TestTheEditorFrameSavesAndRunsFromItsOwnOrigin`, `TestTheEditorFrameIsRefusedUnlessItsParentIsTheSessionsHost` and `TestTheEditorFramesOriginIsThePublicURLWhenOneIsSet` in `internal/api/embed_test.go`. The middleware table `TestEmbedAuthAdmitsTheEditorsOwnOriginOnlyForTheSessionsHost` has 15 cases. `TestSelfOrigin…`/`TestSelfURL…` are in `internal/embed`.
- Web: `session.test.ts` checks that the parent rides beside the token on requests and downloads, is cleared with the token, and is absent on a refusal.
- e2e: `smoke.spec.ts` › "the embedded editor saves and runs from a host page on another origin". On the old code it fails with `PUT … status=403` and "Save failed: 403 — This embed session is not allowed from that origin." On the fix it passes, and the run's Set output carries the saved edit.
- End to end: the unchanged `sdk/examples/host-page` (PORT=4273, packed SDK, real binary with auth on, tenant key minted with the operator key) now saves twice (200, 200), runs (202), receives `execution-finished` and streams `execution.completed`. Curl matrix with the same token: no Origin → 202; host → 202; iframe origin with no parent → 403; iframe + parent host → 202; + `Sec-Fetch-Site: same-origin` → 202; + `cross-site` → 403; iframe + parent evil → 403; `Origin: evil` → 403; evil + parent host → 403.

## Progress — final review fixes (2026-09-23)

- **A foreign frame cannot read with the host's token either.** The frame's reads carry no `Origin`, so `originPermitted` passed them before it looked at the parent: another allowlisted page that framed the editor and handed it host H's token could not save or run, but could read. `originPermitted` now refuses, with the same 403 sentence, any request whose `X-KilasFlow-Embed-Parent` names an origin other than the session's, before the no-`Origin` pass. Only the frame sends that header, and on every request (`attachEmbedHeaders`), so the dashboard, event streams and non-browser clients are unaffected.
- Proof: `TestEmbedAuthAdmitsTheEditorsOwnOriginOnlyForTheSessionsHost` gains three rows (no Origin + another parent → 403; no Origin + the host as parent → pass; no Origin + an unparseable parent → 403; the existing no Origin + no parent row still passes), and every row now runs as a GET and as a PUT. The two refused rows failed on both methods before the change and pass after. `e2e/tests/smoke.spec.ts` passes.
- Docs: the embedding guide's "One exact origin" item and the tenancy concept page now say the frame names its parent on every request, reads included, and that a request skips the per-request check only when it carries neither header. The operator checklist gains a line: set `KILASFLOW_SERVER_PUBLIC_URL` for any deployment that browsers reach but the internet does not (with it empty, a DNS-rebinding page's `Host` reads as KilasFlow's own origin).

# Related Files

`evidence/embed-save-403.png`, `evidence/embed-403-lines.txt`, `evidence/kf-auth-server.log`. Code: `internal/api/middleware/embed.go:52-58` (the check), `internal/embed/embed.go:200-205` (`MatchesOrigin` compares against the host origin only). The e2e test covers only the handshake and read, not save or run (`e2e/tests/smoke.spec.ts:78-108`).

# Attachments

- ![Save and Run fail with 403 in the reference host page](../attachments/BUG-b3p8va/embed-save-403.png)

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23. Rewritten on 2026-09-23 to list only this ticket's own commits (`git log --grep "BUG-b3p8va" 2f8e9b1..5830b4e`) and the files exactly those commits changed.

- Range: `2f8e9b1..5830b4e`
- Commits (3):
  - `0ff9501b` — BUG-b3p8va: a frame another page completed the handshake with cannot read with the host's token either
  - `b456c5a8` — chore(pine): close BUG-b3p8va with its landing evidence
  - `874d83fe` — BUG-b3p8va: the embedded editor saves and runs from a host page on another origin
- Merged by (1):
  - `bd609d4d` — merge: the embedded editor saves and runs from a host page on another origin (BUG-b3p8va)
- Files changed by those commits (merge commits excluded):

```
 .pine/tickets/BUG-b3p8va.md                        | 320 ++++++++++++++++++++-
 config.example.yaml                                |   5 +
 .../content/docs/concepts/tenancy-and-embedding.md |  40 ++-
 docs/src/content/docs/guides/embedding.md          |  37 ++-
 .../docs/operate/configuration-reference.md        |   5 +
 e2e/helpers/stub.ts                                |   7 +-
 e2e/tests/smoke.spec.ts                            |  86 ++++++
 internal/api/embed_test.go                         | 106 +++++++-
 internal/api/handlers/admin_admin_test.go          |   2 +-
 internal/api/handlers/oauth.go                     |  17 +-
 internal/api/handlers/resume_test.go               |   2 +-
 internal/api/middleware/embed.go                   |  60 ++++-
 internal/api/middleware/embed_test.go              | 138 ++++++++--
 internal/api/server.go                             |   3 +-
 internal/api/workflows_test.go                     |   8 +
 internal/config/config.go                          |   5 +
 internal/embed/embed.go                            |  29 ++
 internal/embed/embed_test.go                       |  60 ++++
 web/src/lib/api/http.ts                            |  32 ++-
 web/src/lib/embed/session.svelte.ts                |   6 +-
 web/src/lib/embed/session.test.ts                  |  47 +++-
 21 files changed, 926 insertions(+), 89 deletions(-)
```
