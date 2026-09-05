---
id: FEAT-za118x
title: Generate the API reference into the documentation site
status: todo
priority: high
labels:
    - docs
    - api
deps:
    - FEAT-nxxbs5
    - FEAT-bscygc
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:53:17Z"
updated: "2026-09-05T11:53:17Z"
---

## Scope

KilasFlow serves its own API reference and it is only reachable from a running instance. `internal/api/docs.go` renders an inline `html/template` that loads a vendored Scalar bundle from `/vendor/scalar.js` — 3,736,898 bytes, shipped inside the SPA build and therefore inside the binary — under a strict CSP, with a `#docs-fallback` block that appears and links to the raw specification if the bundle is missing. The rationale is recorded in `README.md` and it is a good one: zero external requests, no CDN dependency, works air-gapped. Huma's built-in renderer was rejected precisely because it loads Scalar from unpkg, and `openAPIConfig` sets `cfg.DocsPath = ""` to disable it.

That reference is excellent for an operator who already has KilasFlow running. It is unreachable for the reader this phase exists to serve: someone evaluating whether to build on the product, who has not installed anything, and who will not install a container to find out whether the API has the endpoint they need.

The generator to feed a public reference already exists and is already trusted. `scripts/openapi-spec.mjs` builds a binary, reserves a free port, boots it with `-config ''`, polls `/api/v1/ready` up to eighty times, fetches `/api/openapi.json`, asserts the document declares OpenAPI 3.1, writes it, and cleans up with SIGTERM then SIGKILL. Both `web/` and `sdk/` already import it, so a third consumer costs nothing and inherits the property that makes the whole chain worth having — the document comes from a real binary, so the reference cannot describe an API the server does not serve.

What has to be decided rather than merely built is which reference is canonical. Two renderings of the same document, one in the product and one on the web, is the normal arrangement and it goes wrong in a specific way: they drift in version. The binary's `/docs` shows whatever that binary serves; the site shows whatever the last docs build produced. A reader comparing them and finding a difference has no way to tell which is stale.

## Acceptance criteria

- [ ] The site carries a browsable API reference covering all 33 operations, generated from the OpenAPI document rather than hand-written.
- [ ] The document is produced by `scripts/openapi-spec.mjs` at build time, so no OpenAPI file is committed and the reference cannot describe an endpoint the server does not serve.
- [ ] The docs build fails when the specification cannot be produced, rather than falling back to a stale copy.
- [ ] Every page of the reference states the server version the document was generated from, so a reader can tell which release they are reading.
- [ ] The relationship between the site's reference and the binary's `/docs` page is stated on both: which is canonical, why the other exists, and what to do when they disagree.
- [ ] The RFC 9457 problem responses and the `WorkflowValidationIssue` payload are documented as first-class content rather than left as bare schemas, since they are how every error in this API is expressed.
- [ ] The nine SSE event types and the `Last-Event-ID` resume behaviour are documented, since an OpenAPI document describes the stream endpoint but not the event vocabulary carried over it.
- [ ] The reference marks which operations an embed session may reach, matching `permits()` and the stability statement from V2-p10-4.

## Implementation Plan

Use `starlight-openapi`, pointed at the file `scripts/openapi-spec.mjs` writes into the docs project's own temp directory. Wire it as a prebuild step so `pnpm build` in `docs/` produces the specification first; a reference generated from a file somebody remembered to refresh is exactly the drift this repository has spent real effort avoiding elsewhere.

Recommend the following answer to the canonical question, stated on both surfaces: **the binary's `/docs` is authoritative for the instance you are talking to, and the site is authoritative for the current release.** That is true, it is useful, and it tells a reader which one to trust in the case where they differ. Add the generated-from version to the site pages so the comparison is possible at all.

Do not replace the in-binary Scalar page with a link to the site. Its air-gapped property is deliberate and load-bearing for the deployment posture this product targets, and the 3.7 MB is already paid.

Two things the OpenAPI document cannot express and which therefore need hand-written pages beside the generated reference. The SSE stream is one: huma registers the endpoint and the nine event types are separate Go types so they appear as schemas, but the sequencing — which events can follow which, that the stream closes on `execution.completed`, `failed` or `cancelled`, how `Last-Event-ID` resumes, that a comment heartbeat arrives every twenty seconds — is behaviour, not shape. The webhook surface is the other: `/webhook/{route}` accepts any method, its route segment is minted opaquely per tenant and workflow, and an unknown, inactive or wrong-method request all answer an identical 404 deliberately. Neither is discoverable from the document.

One trap worth naming: the docs build now needs a Go toolchain, because `scripts/openapi-spec.mjs` compiles a binary. That is a real constraint on the deployment job and on any contributor building the site locally. Say so in the docs project's README, and make the failure message say it too — otherwise a documentation contributor with only Node installed gets an inscrutable build error on a prose change.

## References

- Roadmap plan, p10 section, entry V2-p10-10: `.pine/roadmap.md`.
- `scripts/openapi-spec.mjs` — `dumpOpenAPISpec`, the readiness poll, the 3.1 assertion and the cleanup path.
- `internal/api/docs.go` — `ScalarBundlePath`, the inline template, `docsCSP`, and the `#docs-fallback` block.
- `internal/api/server.go` — `openAPIConfig` and the `cfg.DocsPath = ""` line that disables huma's CDN renderer.
- `README.md` — the section explaining why the docs page is self-hosted.
- `internal/api/handlers/executions.go` — the SSE registration, the nine event types, the heartbeat and `Last-Event-ID`.
- `internal/webhook/webhook.go` and `internal/repository/webhooks.go` — `mintWebhookRoute` and the uniform 404, the behaviour the reference must describe in prose.
- `internal/api/middleware/embed.go` — `permits()`, the embed reachability the reference marks.
- `.pine/tickets/FEAT-bscygc.md` — V2-p10-4, whose stability statement this reference renders.
