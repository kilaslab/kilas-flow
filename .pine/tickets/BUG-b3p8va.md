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

Closed by `pine close --evidence` on 2026-09-23.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (2):
  - `874d83fe` — BUG-b3p8va: the embedded editor saves and runs from a host page on another origin
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          | Bin 0 -> 119127 bytes
 .pine/memory/code-node.md                          |   3 +-
 .pine/memory/licensing.md                          |   2 +-
 .pine/memory/n8n-reference.md                      |   4 +-
 .pine/roadmap.md                                   |   8 +-
 .pine/tickets/BUG-0xv7bg.md                        |  54 +++
 .pine/tickets/BUG-15st2k.md                        | 162 +++++++
 .pine/tickets/BUG-2eryxn.md                        |  49 +++
 .pine/tickets/BUG-2mes2k.md                        |  54 +++
 .pine/tickets/BUG-2n4rfz.md                        |  51 +++
 .pine/tickets/BUG-2z8geh.md                        |  52 +++
 .pine/tickets/BUG-3k12ky.md                        | 163 +++++++
 .pine/tickets/BUG-3qxx0j.md                        |  59 +++
 .pine/tickets/BUG-56qqgx.md                        |  52 +++
 .pine/tickets/BUG-5bgx5c.md                        |  59 +++
 .pine/tickets/BUG-605n21.md                        | 131 ++++++
 .pine/tickets/BUG-66fhea.md                        |  97 +++++
 .pine/tickets/BUG-6d6wbg.md                        |  98 +++++
 .pine/tickets/BUG-6gkd12.md                        | 107 +++++
 .pine/tickets/BUG-719gaz.md                        |  88 ++++
 .pine/tickets/BUG-9dw5me.md                        |  53 +++
 .pine/tickets/BUG-9pmv8y.md                        |  61 +++
 .pine/tickets/BUG-b3p8va.md                        |  76 ++++
 .pine/tickets/BUG-b4cb1c.md                        | 119 +++++
 .pine/tickets/BUG-b8bwhw.md                        |  55 +++
 .pine/tickets/BUG-bcahaj.md                        | 100 +++++
 .pine/tickets/BUG-bw2zc1.md                        |  55 +++
 .pine/tickets/BUG-dstsg9.md                        |  54 +++
 .pine/tickets/BUG-e7dwpk.md                        |  51 +++
 .pine/tickets/BUG-ecbq28.md                        | 111 +++++
 .pine/tickets/BUG-epy2se.md                        | 122 ++++++
 .pine/tickets/BUG-g7ffj1.md                        |  50 +++
 .pine/tickets/BUG-hmp85t.md                        |  99 +++++
 .pine/tickets/BUG-j7qrp2.md                        |  52 +++
 .pine/tickets/BUG-mzk0xn.md                        |  35 ++
 .pine/tickets/BUG-n6p7qy.md                        | 101 +++++
 .pine/tickets/BUG-n9a6bz.md                        |  54 +++
 .pine/tickets/BUG-namghh.md                        |  48 +++
 .pine/tickets/BUG-nbymq4.md                        |  52 +++
 .pine/tickets/BUG-ngt25j.md                        |  52 +++
 .pine/tickets/BUG-nn74ph.md                        |  51 +++
 .pine/tickets/BUG-nzy3pa.md                        | 186 ++++++++
 .pine/tickets/BUG-p334yw.md                        |  53 +++
 .pine/tickets/BUG-p3j233.md                        |  53 +++
 .pine/tickets/BUG-phv0r9.md                        |  56 +++
 .pine/tickets/BUG-ppvyzr.md                        |  58 +++
 .pine/tickets/BUG-pzkpfr.md                        |  53 +++
 .pine/tickets/BUG-q6b75c.md                        |  52 +++
 .pine/tickets/BUG-r1m83f.md                        |  52 +++
 .pine/tickets/BUG-rbask0.md                        | 140 ++++++
 .pine/tickets/BUG-rh7mpa.md                        |  52 +++
 .pine/tickets/BUG-rs0xq1.md                        |  46 ++
 .pine/tickets/BUG-rytwy7.md                        |  55 +++
 .pine/tickets/BUG-sgrxhh.md                        |  53 +++
 .pine/tickets/BUG-t12ffz.md                        |  57 +++
 .pine/tickets/BUG-t3p92b.md                        |  94 ++++
 .pine/tickets/BUG-txafja.md                        |  55 +++
 .pine/tickets/BUG-v8ksv8.md                        |  49 +++
 .pine/tickets/BUG-vsmnby.md                        |  36 ++
 .pine/tickets/BUG-x28fsx.md                        | 142 ++++++
 .pine/tickets/BUG-x6gyc1.md                        |  54 +++
 .pine/tickets/BUG-xam6t8.md                        | 179 ++++++++
 .pine/tickets/BUG-y38bss.md                        |  54 +++
 .pine/tickets/BUG-ywbvfa.md                        |  36 ++
 .pine/tickets/BUG-z0s4zg.md                        | 100 +++++
 .pine/tickets/BUG-zf4pnj.md                        |  55 +++
 .pine/tickets/EPIC-3en6xr.md                       |  88 ++++
 .pine/tickets/EPIC-62zt4j.md                       | 110 +++++
 .pine/tickets/EPIC-7c3ry9.md                       |  44 ++
 .pine/tickets/EPIC-8rbys7.md                       | 192 +++++++++
 .pine/tickets/EPIC-m42s3g.md                       |   2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 478 +++++++++++++++++++++
 .pine/tickets/FEAT-02cj1g.md                       | 102 +++++
 .pine/tickets/FEAT-02zdcq.md                       | 169 ++++++++
 .pine/tickets/FEAT-0hdfzd.md                       | 106 +++++
 .pine/tickets/FEAT-0xsc1s.md                       |  35 ++
 .pine/tickets/FEAT-1ge0xc.md                       |  31 ++
 .pine/tickets/FEAT-1mxtsn.md                       | 104 +++++
 .pine/tickets/FEAT-274c4p.md                       |  68 +++
 .pine/tickets/FEAT-27g2za.md                       |  33 ++
 .pine/tickets/FEAT-2kx0hx.md                       | 260 +++++++++++
 .pine/tickets/FEAT-2m24nh.md                       |  53 +++
 .pine/tickets/FEAT-2m4yvz.md                       | 101 +++++
 .pine/tickets/FEAT-38je8w.md                       |  35 ++
 .pine/tickets/FEAT-39ttf6.md                       |  32 ++
 .pine/tickets/FEAT-3t112f.md                       |  53 +++
 .pine/tickets/FEAT-3ykb4v.md                       |  37 ++
 .pine/tickets/FEAT-4bjfny.md                       | 100 +++++
 .pine/tickets/FEAT-4bvcrb.md                       |  38 ++
 .pine/tickets/FEAT-4e376e.md                       |  56 +++
 .pine/tickets/FEAT-4jhtny.md                       |  30 ++
 .pine/tickets/FEAT-4pz9fn.md                       |  37 ++
 .pine/tickets/FEAT-53pa9a.md                       |  52 +++
 .pine/tickets/FEAT-5fx926.md                       |  57 +++
 .pine/tickets/FEAT-5g42rz.md                       |  30 ++
 .pine/tickets/FEAT-5kv1jq.md                       |   8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |  99 +++++
 .pine/tickets/FEAT-6m295t.md                       |  38 ++
 .pine/tickets/FEAT-6qzza1.md                       |  58 +++
 .pine/tickets/FEAT-6r663e.md                       |  32 ++
 .pine/tickets/FEAT-70j6dn.md                       |  55 +++
 .pine/tickets/FEAT-7cg0cd.md                       |   6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  40 ++
 .pine/tickets/FEAT-7t0xks.md                       |  31 ++
 .pine/tickets/FEAT-8752vx.md                       |  54 +++
 .pine/tickets/FEAT-8zgwp6.md                       |  32 ++
 .pine/tickets/FEAT-9ep5pw.md                       |  31 ++
 .pine/tickets/FEAT-a3dwj2.md                       |  52 +++
 .pine/tickets/FEAT-afkx3k.md                       |  37 ++
 .pine/tickets/FEAT-bfrkyk.md                       |  54 +++
 .pine/tickets/FEAT-c81kp3.md                       |  59 +++
 .pine/tickets/FEAT-cgm1y3.md                       |   2 +-
 .pine/tickets/FEAT-csqgg5.md                       |   6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |  59 +++
 .pine/tickets/FEAT-edzr73.md                       | 121 ++++++
 .pine/tickets/FEAT-egm8bf.md                       |  37 ++
 .pine/tickets/FEAT-eqzpzq.md                       | 136 ++++++
 .pine/tickets/FEAT-ez6xtm.md                       |  55 +++
 .pine/tickets/FEAT-f045nj.md                       | 131 ++++++
 .pine/tickets/FEAT-f3hx3a.md                       |  37 ++
 .pine/tickets/FEAT-fpqg78.md                       |  52 +++
 .pine/tickets/FEAT-fqmh01.md                       |  97 +++++
 .pine/tickets/FEAT-fs3pjr.md                       | 205 +++++++++
 .pine/tickets/FEAT-gzd32h.md                       |  31 ++
 .pine/tickets/FEAT-hxztwz.md                       |  37 ++
 .pine/tickets/FEAT-je4f4t.md                       |   4 +-
 .pine/tickets/FEAT-jwhdsy.md                       |   2 +-
 .pine/tickets/FEAT-kcdrcy.md                       | 130 ++++++
 .pine/tickets/FEAT-kfmq1z.md                       |  53 +++
 .pine/tickets/FEAT-kpn0m3.md                       |  37 ++
 .pine/tickets/FEAT-ktasef.md                       | 103 +++++
 .pine/tickets/FEAT-ky75b5.md                       |  52 +++
 .pine/tickets/FEAT-m1fdn4.md                       |  56 +++
 .pine/tickets/FEAT-m7aw75.md                       |  54 +++
 .pine/tickets/FEAT-mammrz.md                       |  35 ++
 .pine/tickets/FEAT-mccadj.md                       |  38 ++
 .pine/tickets/FEAT-mh4e8g.md                       |  32 ++
 .pine/tickets/FEAT-mj2nek.md                       |  98 +++++
 .pine/tickets/FEAT-mngmn1.md                       |  32 ++
 .pine/tickets/FEAT-mq412g.md                       |  58 +++
 .pine/tickets/FEAT-mxmjt7.md                       | 129 ++++++
 .pine/tickets/FEAT-n010f0.md                       |  33 ++
 .pine/tickets/FEAT-n12211.md                       |  34 ++
 .pine/tickets/FEAT-nch9dg.md                       |   6 +-
 .pine/tickets/FEAT-npc3ge.md                       |  37 ++
 .pine/tickets/FEAT-nq1vsx.md                       |  53 +++
 .pine/tickets/FEAT-p01rcw.md                       |  98 +++++
 .pine/tickets/FEAT-p75n7j.md                       |  38 ++
 .pine/tickets/FEAT-pfwjzk.md                       |  30 ++
 .pine/tickets/FEAT-ppnetz.md                       | 141 ++++++
 .pine/tickets/FEAT-pqnxx4.md                       |  37 ++
 .pine/tickets/FEAT-prw1hw.md                       |  56 +++
 .pine/tickets/FEAT-pt6ge9.md                       |  34 ++
 .pine/tickets/FEAT-pxcbqj.md                       |  39 ++
 .pine/tickets/FEAT-q81bq4.md                       |   2 +-
 .pine/tickets/FEAT-qf0hsa.md                       |  53 +++
 .pine/tickets/FEAT-r267jj.md                       |  35 ++
 .pine/tickets/FEAT-r8ph93.md                       |  38 ++
 .pine/tickets/FEAT-rdfjh1.md                       |  32 ++
 .pine/tickets/FEAT-re138f.md                       |  54 +++
 .pine/tickets/FEAT-rkj8ry.md                       |  37 ++
 .pine/tickets/FEAT-s3sfx5.md                       |  31 ++
 .pine/tickets/FEAT-s99vdp.md                       | 155 +++++++
 .pine/tickets/FEAT-sc3qrq.md                       |  54 +++
 .pine/tickets/FEAT-sz4ddp.md                       |  57 +++
 .pine/tickets/FEAT-t26rt7.md                       |   2 +-
 .pine/tickets/FEAT-t38djq.md                       |  56 +++
 .pine/tickets/FEAT-t58m89.md                       |  32 ++
 .pine/tickets/FEAT-t672pv.md                       |  57 +++
 .pine/tickets/FEAT-tjcr13.md                       |  52 +++
 .pine/tickets/FEAT-v2nenc.md                       |  58 +++
 .pine/tickets/FEAT-vjjs8t.md                       |  36 ++
 .pine/tickets/FEAT-vntngh.md                       |  64 +++
 .pine/tickets/FEAT-vvwpjw.md                       |   2 +-
 .pine/tickets/FEAT-w7n7x6.md                       | 131 ++++++
 .pine/tickets/FEAT-w9kqeg.md                       |  10 +-
 .pine/tickets/FEAT-wcr6en.md                       |  52 +++
 .pine/tickets/FEAT-wzfz3d.md                       |  59 +++
 .pine/tickets/FEAT-x9gq0s.md                       |  37 ++
 .pine/tickets/FEAT-xj5tv6.md                       |  38 ++
 .pine/tickets/FEAT-xr75b9.md                       |  58 +++
 .pine/tickets/FEAT-xzdn35.md                       |  56 +++
 .pine/tickets/FEAT-ybm2pd.md                       |   2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |  32 ++
 .pine/tickets/FEAT-ys734v.md                       |  36 ++
 .pine/tickets/FEAT-yxhgeh.md                       |  38 ++
 .pine/tickets/FEAT-yyjfjq.md                       |   2 +-
 .pine/tickets/FEAT-z90r5a.md                       |  32 ++
 .pine/tickets/FEAT-zhdxc4.md                       |  38 ++
 .pine/tickets/FEAT-zjrw76.md                       |  37 ++
 .pine/tickets/FEAT-zm3wh2.md                       |  99 +++++
 .pine/tickets/FEAT-zn5rqy.md                       | 103 +++++
 .pine/tickets/FEAT-zwpvbf.md                       |  60 +++
 CHANGELOG.md                                       |  25 ++
 CONTRIBUTING.md                                    |  22 +
 README.md                                          | 450 +++++--------------
 config.example.yaml                                |  13 +-
 docs/src/content/docs/concepts/architecture.md     |  84 ++++
 docs/src/content/docs/concepts/execution-model.md  |  15 +-
 docs/src/content/docs/concepts/node-registry.md    |   5 +-
 .../src/content/docs/concepts/safety-boundaries.md |   2 +-
 .../content/docs/concepts/tenancy-and-embedding.md |  16 +-
 docs/src/content/docs/concepts/webhooks.md         |  33 ++
 docs/src/content/docs/guides/embedding.md          |  19 +-
 .../content/docs/operate/acceptance-capstone.md    |   3 +-
 .../docs/operate/configuration-reference.md        |  13 +-
 docs/src/content/docs/reference/api-contract.md    |  15 +-
 docs/src/content/docs/reference/api.md             |   2 +-
 docs/src/content/docs/reference/api/events.md      |  13 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |  15 +-
 e2e/helpers/seed.ts                                |  15 +
 e2e/helpers/stub.ts                                |   7 +-
 e2e/tests/editor-chat.spec.ts                      |  68 ++-
 e2e/tests/smoke.spec.ts                            |  86 ++++
 gflow-prd-v1.md                                    |   8 +-
 internal/ai/openai.go                              |  77 +++-
 internal/ai/openai_test.go                         |  66 +++
 internal/api/embed_test.go                         | 106 ++++-
 internal/api/handlers/admin_admin_test.go          |   2 +-
 internal/api/handlers/executions.go                |  46 +-
 internal/api/handlers/executions_events_test.go    |  64 +++
 internal/api/handlers/oauth.go                     |  17 +-
 internal/api/handlers/resume_test.go               |   2 +-
 internal/api/middleware/embed.go                   |  43 +-
 internal/api/middleware/embed_test.go              |  78 ++++
 internal/api/server.go                             |   3 +-
 internal/api/workflows_test.go                     |   8 +
 internal/config/config.go                          |  15 +-
 internal/config/config_test.go                     |  22 +
 internal/embed/embed.go                            |  29 ++
 internal/embed/embed_test.go                       |  60 +++
 internal/engine/approval.go                        |   2 +-
 nodes/ai.go                                        |  75 ++--
 nodes/ai_test.go                                   |  59 +++
 scripts/generate-api-reference.mjs                 |  28 +-
 sdk/src/generated/models.ts                        | 250 +++++++++++
 sidecar/runner_test.go                             |  15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   2 +-
 web/messages/en/editor.json                        |  20 +-
 web/messages/id/editor.json                        |  20 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIAgentFailedEvent.ts |  24 ++
 .../api/generated/models/aIModelCompletedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |  24 ++
 .../api/generated/models/aIModelStartedEvent.ts    |  24 ++
 .../api/generated/models/aIToolCompletedEvent.ts   |  24 ++
 .../lib/api/generated/models/aIToolFailedEvent.ts  |  24 ++
 .../lib/api/generated/models/aIToolStartedEvent.ts |  24 ++
 web/src/lib/api/generated/models/index.ts          |  10 +
 web/src/lib/api/generated/models/otherEvent.ts     |  24 ++
 .../models/streamExecutionEvents200Item.ts         |  90 ++++
 .../api/generated/models/webhookResponseEvent.ts   |  24 ++
 web/src/lib/api/http.ts                            |  32 +-
 .../workflow-editor/canvas-chat-panel.svelte       | 381 +++++++++++++---
 .../workflow-editor/chat-markdown.svelte           |  38 ++
 .../workflow-editor/workflow-editor.svelte         |  54 ++-
 web/src/lib/embed/session.svelte.ts                |   6 +-
 web/src/lib/embed/session.test.ts                  |  47 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  | 110 +++++
 web/src/lib/workflow-editor/chat-markdown.ts       | 211 +++++++++
 web/src/lib/workflow-editor/chat-stream.test.ts    |  70 +++
 web/src/lib/workflow-editor/chat-stream.ts         | 112 +++++
 web/src/lib/workflow-editor/chat.test.ts           |  54 ++-
 web/src/lib/workflow-editor/chat.ts                |  78 +++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |  44 +-
 .../lib/workflow-editor/execution-watch.test.ts    |  58 +++
 web/src/lib/workflow-editor/execution-watch.ts     |  56 +++
 web/src/lib/workflow-editor/validation.ts          |   5 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  36 +-
 269 files changed, 15355 insertions(+), 580 deletions(-)
```
