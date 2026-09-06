---
id: LRN-jrxe9h
scope: ticket
ticket: FEAT-n19dch
source_agent: manual
created: "2026-09-06T05:27:46Z"
---

FEAT-n19dch (Batch-6, DatastoreAgentTool): read-only datastore agent tool landed. kilasflow.datastoreTool via toolVariantOf(datastoreNode) reusing builtin:table glyph (no node-visual change); core.datastoreTool executor = NewDatastoreToolExecutor struct (node side binds table tenant-scoped + freezes id/name/columns into id-only $ai descriptor); agent side datastoreToolFrom + AgentExecutor.datastore via WithDatastoreStore (nil = install-message refusal, WithVectorStore precedent). Closed enum schema (columns + 10 ops), Invoke re-validates before any statement, ignores unknown args incl. smuggled datastoreId; row cap 50/200 + 256KB size cap. Tests (nodes/datastore_tool_test.go, stub DatastoreStore): e2e vs stub incl. hostile-row-as-data, duplicate-name, cross-tenant by-name+by-id, exact schema, bad column/operator (zero store hits), foreign-id ignored, caps. go test ./nodes/... ./internal/ai/... green. Footprint: core.go +1, executors.go +1/-1 (approved 2-line), ai.go kind+field+option+dispatch, ai_tools_test.go family line.
