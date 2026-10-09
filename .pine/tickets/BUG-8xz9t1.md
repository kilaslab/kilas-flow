---
id: BUG-8xz9t1
title: 'n8n export: a nested expression marker is written as an object, not as an =-prefixed string'
status: todo
priority: medium
labels:
    - n8n
    - importer
    - export
parent: EPIC-8rbys7
created: "2026-10-09T11:20:59Z"
updated: "2026-10-09T11:20:59Z"
---

# Description

The n8n exporter turns a KilasFlow expression marker back into n8n's `=`-prefixed string at the top level of a parameter, but not when the marker sits inside a nested object or list. `toN8NTree` in `internal/interop/n8n/parameters.go` recurses into every map before it looks at the value, so the marker `{"mode":"expression","value":"..."}` is itself treated as an ordinary map and is walked key by key. It comes out as the same two-key object instead of the string `={{ ... }}`.

Today only the Webhook exporter (`webhookToN8N`: `responseCode` and `options`) calls `toN8NTree`, so any expression stored under a webhook's options, such as a response header value, exports as an object that n8n reads as a literal. The import side (`fromN8NTree`) converts nested strings correctly, so the round trip is lossy exactly where the expression lives deepest. The Google Sheets exporter (FEAT-vntngh) needs the same walker for `columns.value`, locator values and `filtersUI` lookups.

Found while planning FEAT-vntngh.

# Steps to Reproduce

1. Import an n8n Webhook node whose `options.responseHeaders.entries[0].value` is `={{ $json.trace }}` and whose `responseCode` is `={{ $json.code }}`.
2. Export the imported workflow with `n8n.Export`.
3. Read the exported node's `options.responseHeaders.entries[0].value` and `responseCode`.

# Expected

Both come back as the strings `={{ $json.trace }}` and `={{ $json.code }}`, as in the file that was imported.

# Actual

Both come back as `{"mode":"expression","value":"{{ ... }}"}` maps, which n8n treats as literal data, not an expression.

# Acceptance Criteria
- [ ] A nested expression marker under a Webhook's `options` and a top-level `responseCode` marker export as `=`-prefixed strings (regression test)
- [ ] `toN8NTree` checks for the expression marker before it recurses, and nested lists and plain maps without a marker are unchanged
- [ ] Import then export then import of a webhook with nested expressions is stable

# Related Files
- internal/interop/n8n/parameters.go
- internal/interop/n8n/roundtrip_fidelity_test.go
