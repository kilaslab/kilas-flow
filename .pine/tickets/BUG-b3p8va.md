---
id: BUG-b3p8va
title: 'Embedded editor cannot save or run from a cross-origin host page: every write returns 403 ''not allowed from that origin'''
status: todo
priority: critical
labels:
    - embed
    - security
    - regression-risk
parent: EPIC-8rbys7
created: "2026-09-23T02:03:02Z"
updated: "2026-09-23T02:03:02Z"
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
- [ ] An embed session minted for host origin H saves and runs a workflow from the KilasFlow iframe mounted on H (the origin check accepts the editor's own origin for requests carrying the session token, or it validates the frame ancestor instead)
- [ ] A token copied into a *third* origin is still refused (the security intent of the per-request check is preserved)
- [ ] A Playwright e2e test with the host page on a different port than the server saves, runs and receives `execution-finished`; it fails on the old behaviour
- [ ] `sdk/examples/host-page` works end to end

# Implementation Plan

Also accept the server's own origin (`server.public_url`, or the request's own scheme and host) in the per-request check, or check `Sec-Fetch-Site: same-origin` from the frame. Add an e2e test that saves and runs through a cross-origin host page. Until the fix lands, the embed quickstart cannot be documented truthfully.

# Notes

Related tickets: BUG-8h4yy1, FEAT-900msn

Related (from the audit): FEAT-900msn (done; its browser proof only checked that the Save/Run controls were present), BUG-8h4yy1 (done; the reporter notes it was "NOT verified end-to-end"). This is not a regression; the behaviour was never proven.

# Related Files

`evidence/embed-save-403.png`, `evidence/embed-403-lines.txt`, `evidence/kf-auth-server.log`. Code: `internal/api/middleware/embed.go:52-58` (the check), `internal/embed/embed.go:200-205` (`MatchesOrigin` compares against the host origin only). The e2e test covers only the handshake and read, not save or run (`e2e/tests/smoke.spec.ts:78-108`).

# Attachments

- ![Save and Run fail with 403 in the reference host page](../attachments/BUG-b3p8va/embed-save-403.png)
