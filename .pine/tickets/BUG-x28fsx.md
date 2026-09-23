---
id: BUG-x28fsx
title: 'Skills bundle content: 10 of 13 skills describe verbs as unshipped, no copy-paste JSON shapes, no AI/loop/binary coverage'
status: todo
priority: high
labels:
    - skills
    - docs
    - agents
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T03:06:00Z"
---

# Description

The skills are the product's instructions to coding agents, and they are stale. They tell agents to work around verbs that now exist, never show the document shapes an agent has to write, and don't cover agent workflows at all. The FEAT-bb4s6e drift gates check structure, not claims.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-3, CLI-5, CLI-17). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## CLI-3: The skills bundle says the guarded verbs, the skills verbs, `--skills-used`, `debug eval`, `exec retry`, `workflow validate`, `duplicate` and `run --revision` do not exist

*docs · high · agent-skills*

**n8n:** n8n's official skills describe the MCP tools that exist.

**Steps to reproduce:**

1. `kilasflow skills show using-kilasflow-skills` (and the others). 2. Compare the `## Not shipped yet` sections and `kilasflow_not_shipped` frontmatter with `kilasflow help`.

**Actual:**

10 of 13 skills carry claims that are now false. Examples:
- using-kilasflow-skills:34-36, 212-214: "No skills verbs … do not exist yet", "No guarded verbs … there is no --yes guard to lean on", "No --skills-used flag".
- kilasflow-workflow-lifecycle:37, 96-97 and non-negotiable 3: "no `workflow activate`, `workflow deactivate` or `workflow delete` … There is also no separate validate verb … `workflow duplicate` and `run --revision` do not exist either".
- kilasflow-triggers:35/113, credentials:28/36/95, datastore:37/45/108, embedding:35/101, operations:29-30/89-90: "No guarded write verbs … credential create, … datastore create … have no verbs yet". Datastore non-negotiable 2 says "the four read verbs are the whole datastore CLI".
- debugging:32-33/101-102, error-handling:25/85, expressions:32/94: "exec retry is not shipped", "debug eval is not shipped".
The router's own generated command table (lines 118-186) lists all of these verbs, so the router contradicts itself. `skills/index.json` exports the same false `notShipped[]`, and a harness reads that file without parsing markdown. Following the skills sends an agent to `kilasflow api activate-workflow`, `api create-credential` and `api delete-workflow`, which skip the `--yes` guard entirely (see CLI-4). No skill mentions a guarded verb or `confirm`.

**Expected:**

The skills describe the binary they ship in. A drift gate compares each `not_shipped` entry, and any verb path mentioned in prose, against `help --json`.

**Suggested fix:**

Rewrite the Not-shipped and non-negotiable sections around the guarded verbs (`--yes` / `confirm`), `skills *`, `--skills-used`, `debug eval`, `exec retry`, `workflow validate|duplicate`, `run --revision`. Add a skills-check rule that fails when a `not_shipped` entry names a verb present in the registry.

**Evidence:**

cs/show/*.md (output of `skills show` for every skill and reference), `grep -n "not shipped\|No guarded\|No activation verb" skills/*/SKILL.md`. The gate checks structure only: internal/skills/check.go:376-386 (`not-shipped-commands` only forbids fenced commands).

**Related:**

FEAT-bb4s6e (drift gates G1-G5, done), FEAT-x5qqpm, FEAT-c72set. Those tickets are done, but the drift they were meant to prevent is back.


## CLI-5: The skills never show the JSON shapes an agent must write, and `kilasflow api` can't print an operation's schema

*gap · medium · agent-skills / workflow-authoring*

**n8n:** The n8n skills pack shows full node JSON, and n8n's MCP has `get_node_types` with examples and `validate_workflow` fix hints.

**Steps to reproduce:**

1. Author a Webhook → Set → Respond document from kilasflow-workflow-lifecycle and PROPERTY_KINDS.md. 2. `workflow validate`. 3. Create an httpHeaderAuth credential from kilasflow-credentials. 4. Configure `kilasflow.datastore` insert.

**Actual:**

- The lifecycle skill says "every node carries … `position`", but not its shape. `[0,0]` → 422 `expected object … body.nodes[0].position`.
- PROPERTY_KINDS says assignment "Rows are `{name,type,value}`", but a bare array → `assignments must be a non-empty object`. The accepted shape is `{"assignments":{"assignments":[…]}}`.
- The resourceMapper's stored shape is not given anywhere. I guessed n8n's `{mappingMode:"defineBelow",value:{…}}` and it worked.
- The credential body `{name,type,fields}` is in no skill. `data` → 422.
- `kilasflow api <op> --help` shows only generic flags. There is no `--describe` or `--schema`, so a wrong body is found by trial.
4 of the 6 tasks hit a 422 before succeeding.

**Expected:**

Each skill carries one minimal, complete, validated example: a canonical document with two connected nodes and the stored shape of every property kind (assignmentCollection, resourceLocator, resourceMapper, keyValue, fixedCollection). The credential and datastore bodies are shown too. `kilasflow api <op> --describe` prints the method, path, parameters and body schema from /api/openapi.json.

**Suggested fix:**

Add `references/DOCUMENT_EXAMPLES.md` to workflow-lifecycle and node-configuration, generated and checked by `workflow validate` in the skills gate. Add `api --describe <op>`.

**Evidence:**

cs/a-wf.json (final shape), cs/cmdlog.txt 09:07:42 validate rc=2, the 422 bodies in the transcript, cs/show/kilasflow-node-configuration--PROPERTY_KINDS.md:105-112, cs/c-wf.json.

**Related:**

BUG-rs0xq1 (published JSON schema is incomplete, a different surface)


## CLI-17: No skill covers AI/agent workflows, loops, binary data or sub-workflows, and the skills cite source files an installed agent can't open

*gap · medium · agent-skills*

**n8n:** The official n8n pack ships agents, loops, binary-and-data, subworkflows, code-nodes, data-tables, error-handling and expressions skills.

**Steps to reproduce:**

`kilasflow skills list`. `grep -l "kilasflow.agent\|chatModel\|memoryBuffer" skills/*/SKILL.md` finds only the connection-kind spelling rule in workflow-lifecycle.

**Actual:**

17 of the 68 node types are AI (`agent`, `chatModel`, `lmChatOpenAi`, `memoryBuffer`, tools, `vectorStore*`, `embeddings`, `documentLoader`, `outputParser`, …). No skill explains how to wire `ai_languageModel`/`ai_tool`/`ai_memory` ports, choose a model credential, or build RAG. Loops (`kilasflow.loop`), binary (`extractFromFile`) and sub-workflow patterns are only touched in passing. The skills are also full of repository citations (`internal/engine/runner.go`, `nodes/http.go`) that don't exist where the bundle is installed from the binary.

**Expected:**

A `kilasflow-ai-agents` skill (ports, models, tools, memory, RAG, with one validated example), plus loops/binary and sub-workflow coverage. Citations go to docs URLs or `kilasflow skills show … --reference`, not to Go files.

**Suggested fix:**

Author the AI skill from the n8n-agents skill's outline, validated by the skills gate against `workflow validate`.

**Evidence:**

cs/show/, /private/tmp/…/scratchpad/node-types.json (category AI).

**Related:**

none


# Acceptance Criteria
- [ ] Every `not_shipped`/"not yet" claim and every verb path mentioned in the prose is checked against `kilasflow help --json` by a drift gate in `go test ./...`
- [ ] Each skill carries one minimal, complete, validated example (canonical document plus stored property shapes), validated in CI through `workflow validate`
- [ ] `kilasflow api <op> --schema` (or similar) prints an operation's request schema
- [ ] A `kilasflow-ai-agents` skill (ports, models, tools, memory, RAG) plus coverage of loops, binary data and sub-workflows; citations point to docs or `skills show --reference`, not repo source paths

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-rs0xq1, FEAT-bb4s6e, FEAT-c72set, FEAT-x5qqpm

# Related Files

# Attachments
