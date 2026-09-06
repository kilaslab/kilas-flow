---
id: LRN-t016v0
scope: ticket
ticket: FEAT-ed6wdy
source_agent: manual
created: "2026-09-06T04:58:47Z"
---

FEAT-ed6wdy done: internal/nodepack/convert.go (new, ~700 lines) converts an operator-transcribed declarative node (DeclarativeNode JSON: sourceType, displayName, requestDefaults, credentials, hasExecute, properties with show/routing) into a Pack with provenance {tool nodepack-convert, source type, input SHA-256}. Refuses execute() nodes, missing resource cascade, reserved kilasflow. prefix, and all-excluded packs with named errors and nil pack. Excludes (never emits broken): preSend, function/unknown postReceive, non-offset pagination, static request fragments, URL-less ops, request-level property routing. Degrades to JSON+note: unmapped kinds (collection/resourceLocator/etc). Values byte-for-byte; Convert self-checks via Load + routing Registry.Register. Report.Markdown follows REPORT-*.md. Tests: convert, round-trip ConvertDocument->Encode->WritePackDir->LoadDir->Register->run vs httptest stub (AllowedPrivateEndpoints pin; X-Api-Key via httpHeaderAuth; path/query/body asserts; excluded pair gets 'no request is declared'), plus refusal table. go test ./internal/nodepack/... and ./internal/guardrails/ green. No edits to author/loader/interop; no new deps.
