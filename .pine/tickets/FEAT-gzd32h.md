---
id: FEAT-gzd32h
title: Input and output contract for workflows used as tools
status: done
priority: low
labels:
    - saas
    - api
    - ai-tools
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-11-27T08:51:28Z"
---

# Description

A host's AI agent calls workflows as synchronous tools and needs a machine-readable input schema. The Execute Workflow Trigger's "Expected fields" are not exposed as JSON Schema.

# Acceptance Criteria
- [x] `GET /workflows/{id}/contract` returns the input JSON Schema (from the trigger fields) and the declared output.
- [x] Aligned with FEAT-nq1vsx (MCP Server Trigger) and per-tenant authentication.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Valid. No `/workflows/{id}/contract` route. Source data exists: `kilasflow.executeWorkflowTrigger` `inputSource` + `workflowInputs` (nodes/subworkflow.go:118-147); the only JSON-Schema derivation today is `$fromAI` (nodes/ai.go:1984-1990).
- Underspecified: nothing declares a workflow's output today — define where "the declared output" comes from before implementing. FEAT-nq1vsx is still todo.

# Related Files

# Attachments

## Progress

2026-11-27, on `feat/epic-sweep-2026-11-27`:

- `get-workflow-contract` (internal/api/handlers/workflow_contract.go) describes the active revision, or the latest draft when the workflow is inactive (`source`). `entry` names the sub-workflow trigger a call starts from (null without one).
- Input: the trigger's declared fields through `nodes.WorkflowInputsSchema`. This is the same declaration the AI Agent's workflow tool offers a model, so the contract and the in-product tool cannot drift. The schema is `{"type":"object","properties":…,"additionalProperties":true}`, because declared fields are a check, never a filter. A passthrough trigger is `declared: false` with `{"type":"object"}`.
- Output: the document's `settings.outputSchema` when it is a JSON object (`declared: true`); otherwise `declared: false`. Nothing else in a document states an output.
- Auth: tenant-scoped like every workflow read; a scoped key needs `workflow:read`; an embed session or a bound key may read its own workflow's contract (scope test row added). Alignment with FEAT-nq1vsx: the MCP Server Trigger, when built, should publish tools from `nodes.WorkflowInputsSchema` too.
- Docs: saas-playbook.md "Reading a declared contract", api-contract.md, and the generated reference. Tests: handlers unit tests (active vs draft, typed properties, undeclared sides, no trigger) and an API route test (404 across tenants).
- Not done: no editor UI for `settings.outputSchema`.

## Work Evidence

Closed by `pine close --evidence` on 2026-11-27.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (4):
  - `b7d430b5` — FEAT-m7aw75, FEAT-gzd32h: cover variables in the tenant purge, the SDK and the test gates
  - `38931eee` — FEAT-gzd32h: GET /workflows/{id}/contract describes a workflow's input and output
  - `bf841006` — chore(pine): EPIC-7c3ry9 records its dependencies and audit notes, and files four infra tickets
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .env.example                      |    8 +-
 .github/assets/editor.png         |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml          |  108 +++-
 .github/workflows/code-corpus.yml |   54 ++
 .gitignore                        |    4 +
 .pine/MEMORY.md                   |    2 +
 .pine/memory/code-node.md         |   30 +-
 .pine/memory/docs.md              |    5 +-
 .pine/memory/e2e.md               |    3 +
 .pine/memory/licensing.md         |   15 +-
 .pine/memory/n8n-reference.md     |   10 +-
 .pine/memory/persistence.md       |    6 +-
 .pine/memory/testing.md           |    4 +-
 .pine/memory/web-editor.md        |    7 +-
 .pine/roadmap.md                  |    8 +-
 .pine/tickets/BUG-0592hz.md       |   62 ++
 .pine/tickets/BUG-0bzsa1.md       |  464 +++++++++++++++
 .pine/tickets/BUG-0grt9g.md       |  427 +++++++++++++
 .pine/tickets/BUG-0xv7bg.md       |   68 +++
 .pine/tickets/BUG-14gp8r.md       |  198 +++++++
 .pine/tickets/BUG-15st2k.md       |  198 +++++++
 .pine/tickets/BUG-1vh6k6.md       |   51 ++
 .pine/tickets/BUG-2d1pdx.md       |   49 ++
 .pine/tickets/BUG-2eryxn.md       |   95 +++
 .pine/tickets/BUG-2mes2k.md       |   83 +++
 .pine/tickets/BUG-2n4rfz.md       |   58 ++
 .pine/tickets/BUG-2vcwjf.md       |  126 ++++
 .pine/tickets/BUG-2xrz6c.md       |   51 ++
 .pine/tickets/BUG-2z8geh.md       |  136 +++++
 .pine/tickets/BUG-3k12ky.md       |  187 ++++++
 .pine/tickets/BUG-3mem9s.md       |  105 ++++
 .pine/tickets/BUG-3qxx0j.md       |   70 +++
 .pine/tickets/BUG-46g75c.md       |  219 +++++++
 .pine/tickets/BUG-49vf3j.md       |  428 ++++++++++++++
 .pine/tickets/BUG-4ch186.md       |   85 +++
 .pine/tickets/BUG-548bk9.md       |  179 ++++++
 .pine/tickets/BUG-56qqgx.md       |   88 +++
 .pine/tickets/BUG-5bgx5c.md       |   81 +++
 .pine/tickets/BUG-5dn8hr.md       |   39 ++
 .pine/tickets/BUG-5fhcx7.md       |   50 ++
 .pine/tickets/BUG-5xkexq.md       |   63 ++
 .pine/tickets/BUG-605n21.md       |  154 +++++
 .pine/tickets/BUG-66fhea.md       |  112 ++++
 .pine/tickets/BUG-6d6wbg.md       |  115 ++++
 .pine/tickets/BUG-6gkd12.md       | 1183 +++++++++++++++++++++++++++++++++++++
 .pine/tickets/BUG-719gaz.md       |  117 ++++
 .pine/tickets/BUG-745mh2.md       |   60 ++
 .pine/tickets/BUG-8xz9t1.md       |  511 ++++++++++++++++
 .pine/tickets/BUG-9296bf.md       |  532 +++++++++++++++++
 .pine/tickets/BUG-9dw5me.md       |   66 +++
 ...
 1646 files changed, 275984 insertions(+), 7906 deletions(-)
```
