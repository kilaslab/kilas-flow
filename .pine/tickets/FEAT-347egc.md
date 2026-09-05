---
id: FEAT-347egc
title: Map n8n LangChain workflows onto the native AI nodes
status: todo
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-cgm1y3
    - FEAT-96p7m3
    - FEAT-mvegj5
    - FEAT-096vs9
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:03:35Z"
updated: "2026-09-05T05:03:35Z"
---

## Scope

The importer knows ten node types, all of them `n8n-nodes-base.*` — `SupportedMappings()` in `internal/interop/n8n/n8n.go` lists them and the `mappings` table below it defines them. Nothing from `@n8n/n8n-nodes-langchain` is mapped, so every AI node in an imported workflow becomes `kilasflow.unsupported`, a placeholder whose validator always fails and which therefore blocks activation of the whole workflow.

The edges fare no better. `importConnections` drops every connection whose kind is not `main`, reporting `connection kind %q is outside the supported subset and was dropped`, behind a comment stating that the ai_* node family is outside the advertised subset. That comment has been wrong since V1: `internal/workflow/document.go` has carried `ai_languageModel`, `ai_memory` and `ai_tool` from the beginning. p1-13 fixes the edges; this ticket maps the nodes that hang off them.

The volume justifies the work. Of 2,377 node instances across the 100 most-viewed n8n.io templates, 472 are `@n8n/n8n-nodes-langchain` — the second-largest family after `n8n-nodes-base`, and the one every modern WhatsApp or Telegram assistant template is built from.

## Acceptance criteria

- [ ] The AI Agent, Basic LLM Chain, OpenAI and OpenRouter chat models, buffer-window memory, HTTP Request Tool, Workflow Tool and structured output parser all map onto their native equivalents, and every mapping is listed by `SupportedMappings()`.
- [ ] AI edges land on the correct slots with the sub-node as the connection source, and an export restores that direction.
- [ ] Every parameter with no native equivalent produces a named diagnostic carrying the node name and the parameter key; nothing is dropped silently.
- [ ] Credential references are rebound rather than trusted: an n8n credential `{id, name}` is instance-local and meaningless here, so the import reports which node needs which credential type instead of carrying the reference across.
- [ ] A workflow built only from AI nodes imports, compiles and activates, measured against the p0 corpus with `imported / activatable / executable` counts.
- [ ] Node type strings are matched exactly and never case-normalised.
- [ ] Round-trip fixtures cover an agent with a model, a memory, two tools and an output parser, and assert both what survives and what is intentionally lossy.

## Implementation Plan

The `mappings` table in `internal/interop/n8n/n8n.go` is the whole extension point: each entry is `{n8nType, kilasType, kilasVersion, exportTypeVersion, toKilas, …}`, so the AI family is more table rows and a parameter converter each, not a new code path. Land this only after p1-13 has made non-`main` connection kinds importable — mapped nodes with every edge dropped would import, fail to compile, and look like this ticket did not work.

The trap is the type version. `int(node.TypeVersion)` truncates n8n's float `typeVersion` and collapses every import to version 1, which p1-11 fixes. The LangChain nodes are versioned in fractions precisely where it matters: the memory node's session auto-scoping changed at v1.4, so a truncated version loses exactly the distinction p5-5 depends on to decide whether a `__<node name>` suffix belongs on the key.

Second trap: capitalisation. n8n type strings are case-sensitive and inconsistent between packages, and a helpful `strings.ToLower` anywhere in the lookup path will map a node onto the wrong executor rather than reporting it as unsupported. The existing `byN8NType` does an exact match; keep it that way.

Parameter names must be read from `packages/@n8n/nodes-langchain` in the reference checkout rather than recalled — p0-1 adds that package to the sparse checkout and it is not present today. Read them there, copy no source, and record the version the names were read at, because the mapping table is the one place in this repository where being wrong is silent.

One decision. A LangChain node with no native equivalent — a vector store, a document loader, a text splitter — can either become the lossless unsupported capsule from p1-9, or be refused at import. Recommend the capsule: it keeps the node visible with its original identity and parameters so a user can see what needs replacing, and p6-6's pgvector work will later map some of them for real.

## References

- Plan `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, p5 entry V2-p5-8; p1 entries V2-p1-13 (AI edges), V2-p1-11 (float typeVersion), V2-p1-9 (unsupported capsule); p0 entries V2-p0-1 and V2-p0-2.
- `internal/interop/n8n/n8n.go` (`SupportedMappings`, `mappings`, `byN8NType`, `importConnections`, the unsupported placeholder), `internal/workflow/document.go`.
- V1 interop ticket FEAT-chxkvq for the advertised-subset conventions this extends.
