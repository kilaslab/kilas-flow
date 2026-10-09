---
id: FEAT-cf5n4j
title: Agent tool forms of the Gmail, Postgres and Telegram nodes (so n8n gmailTool/postgresTool/telegramTool import)
status: doing
priority: medium
created: "2026-10-08T16:58:14Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

KilasFlow ships Gmail, PostgreSQL and Telegram nodes, but no agent-tool forms of them. So n8n's gmailTool (16 templates), postgresTool (7) and telegramTool (3) import as blocking placeholders, and BUG-b8bwhw (template 6270 and the other `*Tool` imports) stays open.

This adds a generic *tool form* mechanism:
- One table entry derives a tool node from any registered node that has an executor. It keeps the base node's parameters and credentials, adds an ai_tool output, `toolName` and `toolDescription`, and is filed under AI › Tools.
- The agent runs the base node's own registered executor. The model's `$fromAI` arguments go into the expression context as data, never as code.
- The importer maps n8n `<base>Tool` through one `toolVariants` line.
- Three forms ship: `kilasflow.gmailTool` (v1), `kilasflow.postgresTool` (v2) and `pack.telegramTool` (v1).
- `googleSheetsTool` (FEAT-vntngh) needs only one entry in each table.

Prerequisite: BUG-prhxdy (the Google nodes evaluated a resolved value a second time, which would have run model text as an expression).

# Acceptance Criteria
- [ ] A generic mechanism: one entry in `nodes.toolForms` gives a registered node a tool form that the agent runs through the base's registered executor, with no per-node tool code. Adding a form takes one `toolForms` entry plus one importer `toolVariants` line.
- [ ] The tool schema comes from its `$fromAI` calls. The model's values reach the base executor only through `engine.Request.FromAIArguments`. A model value containing `{{ … }}` or an expression marker reaches the service as literal text; each form has a test proving it.
- [ ] Safety rules: `$fromAI` in `resource` or `operation` is refused when the node is validated; the Postgres tool refuses `$fromAI` or an expression in `query` (values go through bound Query Parameters, conditions or column values); a tool's output to the model is capped at 256 KiB, with a note saying it was cut; the agent resolves the base node from the tool type on the server side, never from the descriptor.
- [ ] `kilasflow.gmailTool` sends, gets and lists through the Gmail executor. `pack.telegramTool` runs the pack's operations through the routing interpreter. `kilasflow.postgresTool` runs its operations with bound values. Each is proven by an agent test with a scripted model and a stub service; Postgres has a statement-shape test plus a gated live test on `KILASFLOW_TEST_POSTGRES_DSN`.
- [ ] A pack-derived tool form is visible to exactly the tenants its base is, including after `packs.visible_to`. A grant that names the tool form directly is refused, and the message names the base.
- [ ] Import: gmailTool, postgresTool and telegramTool map onto the three forms using the base translator, `toolName` taken from the node name, `toolDescription` (n8n's own; if `descriptionType` is auto or absent, a written one, reported as lossy), and nested `=` strings converted to expressions. A postgresTool whose SQL is written by the model is a blocking issue that names the rule. A fixture of each form imports with no blocking issue apart from re-binding credentials, and compiles on an agent.
- [ ] Gmail base fidelity: n8n `sendTo` maps to `to` and back. A missing `operation` imports as n8n's default. An operation or resource the node does not perform is a blocking issue.
- [ ] Export: each form exports as `<n8n base>Tool` at the base's version, with `toolDescription` and `descriptionType: "manual"`. A `toolName` that differs from the node name is reported as lossy. `SupportedMappings` lists the four derived pairs (including httpRequestTool).
- [ ] Catalogue upkeep: the docs' node counts, glyph variants, the e2e node-coverage list, and the n8n migration guide's AI table are updated.

# Implementation Plan

Plan: Track C of the 2026-10-09 parallel wave. Engine `Request.FromAIArguments` feeds `$fromAI` as data; `nodes/tool_form.go` holds the `toolForms` table, the derived definitions, the `core.toolForm` descriptor executor and the agent-side `formTool`; `internal/node` scopes a pack tool form with its base; `internal/interop/n8n/tool_forms.go` holds the generic `<base>Tool` translators.

# Notes

# Related Files
- nodes/tool_form.go — toolForms table, derivation, agent runtime
- nodes/ai.go — the toolFrom case and the AgentExecutor field
- internal/engine/runner.go — Request.FromAIArguments
- internal/engine/authenticate.go — ExpressionContext carries it
- internal/node/registry.go — Definition.ToolFormOf
- internal/node/visibility.go — a tool form shares its base's scope
- internal/interop/n8n/tool_forms.go — generic <base>Tool translators
- internal/interop/n8n/n8n.go — toolVariants, export fallback, SupportedMappings

# Attachments
