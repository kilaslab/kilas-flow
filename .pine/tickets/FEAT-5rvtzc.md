---
id: FEAT-5rvtzc
title: Serve node icons and remove the hardcoded editor maps
status: todo
priority: high
labels:
    - registry
    - metadata
    - editor
deps:
    - FEAT-qcm5ec
parent: EPIC-m42s3g
phase: p2
created: "2026-09-05T05:02:38Z"
updated: "2026-09-05T05:02:38Z"
---

## Scope

Three frontend files decide what a node looks like and what it can authenticate with, and the API can reach none of them.

`web/src/lib/workflow-editor/node-visual.ts` holds `ICONS`, a `Record<string, Component>` keyed by the seventeen `kilasflow.*` type strings, resolved as `ICONS[definition.type] ?? Box`; `ACCENTS`, keyed by the five category strings, resolved as `ACCENTS[definition.category] ?? 'var(--muted-foreground)'`; and `nodeSubtitle`, a `switch (definition.type)` over eleven cases with a `default: return null`. `web/src/lib/workflow-editor/credentials.ts` holds `BY_NODE_TYPE`, and `credentialTypesFor` returns `[]` for anything absent from it.

The last of those is not a cosmetic default. `properties-panel.svelte` renders the credential picker inside `{#if activeTab === 'parameters' && credentialTypes.length > 0 && onCredentialChange}`. A node type with no `BY_NODE_TYPE` entry gets **no credential selector at all** — not an empty one, not a disabled one; the block does not render. So a generated WAHA node would appear on the canvas as a grey `Box`, with the muted accent, no subtitle, and no way for the user to attach the API key it needs. Every one of the 124 operations p3 generates would look and behave like that.

This ticket makes all three server-driven and adds the icon route that lets a node ship its own artwork. The definition fields it consumes come from the presentation-metadata ticket (icon, accent, subtitle template) and the credential-registry ticket (a node's declared credential types); this is the ticket that deletes the maps and wires the SPA to the server, so it lands after both.

## Acceptance criteria

- [ ] The SPA derives a node's icon, accent, subtitle and offered credential types entirely from `/api/v1/node-types`; `ICONS`, `ACCENTS`, the `nodeSubtitle` switch and `BY_NODE_TYPE` are deleted, not merely bypassed.
- [ ] A node type the SPA has never seen renders with its own icon, accent and subtitle, and offers its declared credential types, with no frontend change.
- [ ] A Go route serves node icon bytes, answering only for a registered node type that declares a served icon, and 404s for anything else.
- [ ] Served SVG cannot execute script in the editor: the response carries `Content-Type: image/svg+xml`, `X-Content-Type-Options: nosniff` and a restrictive `Content-Security-Policy`, and the SPA renders it through `<img>` and never through `{@html}`.
- [ ] A node that declares no icon still renders a documented fallback glyph rather than an empty box, and the fallback is visibly distinguishable from a real icon.
- [ ] Light and dark icon variants are both served and the editor picks by the active theme.
- [ ] Icon responses are cacheable and immutable per node type and version; the existing `node-visual.test.ts` cases are rewritten against server-supplied metadata rather than deleted.

## Implementation Plan

Server side first. Add the route to `internal/api/handlers/nodes.go` — `GET /node-types/{type}/icon` with a theme parameter is the natural shape next to `list-node-types` — and register it on the v1 group in `internal/api/routes.go`. Where the bytes come from depends on the icon form settled in the presentation-metadata ticket: `builtin:<name>` resolves in the SPA to a lucide component and never reaches this route, while a served icon is bytes the registry holds for that definition. Serve those from the registry, not from disk, so a pack loaded at composition carries its own artwork without an asset-directory convention.

XSS is the real risk and it is easy to get wrong. SVG is an active document format: an inlined `<svg>` can carry `<script>` and event handlers, and the editor is embedded in customer pages behind `internal/embed`, so a stored-XSS in an icon is a cross-tenant problem, not a cosmetic one. Two defences, both required. Serve with `Content-Type: image/svg+xml`, `X-Content-Type-Options: nosniff` and `Content-Security-Policy: default-src 'none'; style-src 'unsafe-inline'; sandbox` — the app sets a CSP today only on the docs route in `internal/api/docs.go`, so this route sets its own. And render through `<img src>` in `node-icon.svelte`, which gives the browser's image sandbox for free. Recommend additionally sanitising on ingest, when a pack registers, rather than on every request: strip `<script>`, `<foreignObject>`, every `on*` attribute and any non-`data:` external reference, and refuse the registration if the document does not parse. A pack that ships a hostile icon should fail to load, not render safely.

Then the SPA. `node-icon.svelte` becomes a component that renders either a lucide glyph (for `builtin:`) or an `<img>`, keeping its current sizing and accent treatment — it is already the single place the canvas, picker and inspector all render through, which is what makes this a contained change. `nodeVisual` loses `ICONS` and `ACCENTS` and reads the definition. `nodeSubtitle` is replaced by the subtitle-template renderer. `credentialTypesFor` is replaced by the definition's declared credential requirements, and `properties-panel.svelte`'s `{#if}` guard should be revisited at the same time: a node that declares a required credential and has none configured deserves a visible prompt, not a missing block.

Keep `nodeShape` exactly as it is. It already derives the silhouette from the declared ports rather than from a list of type names, which is the pattern the rest of this ticket is copying — do not regress it into a server field.

`web/src/lib/workflow-editor/node-visual.test.ts` asserts against the hardcoded switch across roughly a dozen cases. Rewrite them to feed server-shaped definitions in; deleting them loses the only coverage the subtitle rendering has.

## References

- Plan: `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, section "p2 — Node metadata foundation", entry V2-p2-5.
- `web/src/lib/workflow-editor/node-visual.ts` — `ICONS`, `ACCENTS`, `nodeSubtitle`, `nodeVisual`, `nodeShape`.
- `web/src/lib/workflow-editor/credentials.ts` — `BY_NODE_TYPE`, `credentialTypesFor`.
- `web/src/lib/components/workflow-editor/properties-panel.svelte` — the credential-picker guard that hides the block entirely.
- `web/src/lib/components/workflow-editor/node-icon.svelte` — the single render path for every surface.
- `internal/api/handlers/nodes.go`, `internal/api/routes.go` — where the icon route is added.
- `internal/api/docs.go` — the only route that currently sets a `Content-Security-Policy`.
- `web/src/lib/workflow-editor/node-visual.test.ts` — the cases to rewrite.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 02, 08 — per-node icons and the accent treatment the hardcoded frontend maps currently stand in for. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
