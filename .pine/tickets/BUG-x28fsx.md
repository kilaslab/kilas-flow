---
id: BUG-x28fsx
title: 'Skills bundle content: 10 of 13 skills describe verbs as unshipped, no copy-paste JSON shapes, no AI/loop/binary coverage'
status: done
priority: high
labels:
    - skills
    - docs
    - agents
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-10-09T12:47:02Z"
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
- [x] Every `not_shipped`/"not yet" claim and every verb path mentioned in the prose is checked against `kilasflow help --json` by a drift gate in `go test ./...`
- [x] Each skill carries one minimal, complete, validated example (canonical document plus stored property shapes), validated in CI through `workflow validate` (all 15 skills carry `references/EXAMPLE.md`; `TestEverySkillCarriesAnExample`, `TestEverySkillPointsAtItsExample` and `TestEverySkillExampleValidates` in internal/skills run in `go test ./...` and in `make skills-check`; `TestNodeConfigurationExampleStoresEveryComplexKind` reads the stored property shapes against the live definitions)
- [x] `kilasflow api <op> --schema` (or similar) prints an operation's request schema
- [x] A `kilasflow-ai-agents` skill (ports, models, tools, memory, RAG) plus coverage of loops, binary data and sub-workflows; citations point to docs or `skills show --reference`, not repo source paths (`kilasflow-ai-agents` and `kilasflow-flow-patterns` are in the bundle and the router; the checker's `source-citations` rule fails on a repository path in any skill, and `TestSkillCitationsResolve` resolves every `(docs: <slug>)` and `--reference` citation; the bundle has zero findings)

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: BUG-rs0xq1, FEAT-bb4s6e, FEAT-c72set, FEAT-x5qqpm

# Related Files

# Attachments

## Progress (2026-10-08)

Two of the stale `not_shipped` claims are corrected by hand. Criterion 1's drift gate is still to build.
- `debug eval` ships (`kilasflow debug eval --execution <id> --node <nodeId> [--run n] [--item n] '<expression>'`, BUG-j7qrp2). Its "not shipped" entry is removed from kilasflow-debugging and kilasflow-expressions, frontmatter and body both, and each skill now says what the verb does.
- `exec retry` ships but reruns the whole workflow. kilasflow-debugging and kilasflow-error-handling keep "no single-node retry verb" and now say that `exec retry` reruns the whole workflow with the same input and trigger.
- `skills/index.json` is regenerated. `go test ./internal/skills/`, `make generate-skills-index-check` and `make skills-check` pass.

Gate, partly (criterion 1): `TestNoSkillCallsAShippedVerbUnshipped` (internal/cli/skills_not_shipped_test.go) fails when any skill's `kilasflow_not_shipped` entry, or its '## Not shipped yet' section, calls a verb of the live command tree unshipped. Run against the skills from before this fix, it names all six stale claims. `TestEveryVerbASkillQuotesExists` checks every `kilasflow <verb>` that a skill's prose or reference files quote against the same tree. It passes today, and a planted `kilasflow exec rewind` made it fail.

`kilasflow api <op> --schema` (criterion 3) prints the operation's method, path template, parameters and JSON request-body schema, with every `$ref` resolved, from the served OpenAPI document. It calls nothing, so it needs no confirmation (`operationSchema` in internal/cli/openapi.go). `TestAPISchemaPrintsAnOperationsParametersAndBodyWithoutCallingIt` failed first. Against a live server, `api create-workflow --schema` resolves the nested node schema.

Still open: validated examples per skill (2) and the AI-agents skill (4).

List-form claims (2026-10-08, 23:56): `TestNoSkillListsAShippedVerbAsMissing` checks every verb path written before "have no verb(s)" in a `not_shipped` entry. It found that kilasflow-credentials, -embedding and -triggers (and earlier -datastore and -operations) denied the shipped credential, datastore and tenant-delete verbs. All five now name only what is absent: schedule writes, pack install and tenant create. The skills tests, the index check and `skills-check` pass.

Criterion 4 measured (2026-10-08, 23:58): the SKILL.md files still cite 230 repo source paths (`internal/…`, `nodes/…`, `cmd/…`, `web/src/…`), in kilasflow-expressions, kilasflow-datastore, kilasflow-operations' references and others. Each has to be swapped for a docs page or a `skills show --reference` file before the AI-agents skill is written.

## Progress (2026-10-09)

Criterion 1 had regressed: the 2026-10-08 gates only matched two exact wordings, and the bundle still denied shipped verbs. Criteria 2 and 4 are now met.

Criterion 1 (the regression). `internal/cli/skills_not_shipped_test.go` gained four gates, and each failed on the pre-fix bundle before any skill was edited:
- `TestNoNotShippedSentenceNegatesAShippedVerb` reads every sentence of a `kilasflow_not_shipped` entry and of the `## Not shipped yet` section and fails when a shipped multi-word verb path sits next to a negation (a one-word verb counts only in the denial forms "no X verb" and "X does not exist");
- `TestNoSkillDeniesAFlagTheBinaryRegisters` fails on "No --x flag" for a registered flag (it caught "No --skills-used flag") and on a verb's own flag next to a negation (`run --revision`);
- `TestNoSkillSendsAShippedVerbsOperationThroughTheEscapeHatch` fails when an operation id a verb drives (`activate-workflow`) is "reached through the escape hatch";
- `TestNoSkillDeniesAWholeVerbFamily` fails on "No skills verbs: list, show, install ...";
- `TestNoSkillProseDeniesAShippedVerb` walks the whole bundle's prose for "no <verb>" and "<verb> do not exist" and caught "There is no debug eval" in a non-negotiable.
`TestEveryVerbASkillQuotesExists` now understands three-word verbs (`datastore columns add`). The router's `kilasflow_not_shipped` is `[]` and its section is gone; the other skills' sections name only what is absent (no update/import/restore verb, no schedule-write, pack-install or tenant-create verb, no idempotency flag, no webhook discovery verb, no OCR). The guarded writes, `workflow validate|duplicate|activate|deactivate|delete`, `run --revision`, the `skills` verbs, `--skills-used` and `api <op> --schema` are described as the binary has them. The embed text that said a session can never activate was also wrong (`workflow:activate` allows it) and is corrected.

Criterion 2. Examples are fenced as `json kilasflow-workflow` or `json kilasflow-request <operation-id>` inside `references/EXAMPLE.md` (and a few other references: MODELS.md, RAG.md, BINARY_DATA.md, SUB_WORKFLOWS.md). `internal/skills/examples.go` extracts them and `CheckExampleShape` checks the shape (top-level members, no server-owned `id`, at least two nodes with a `{x, y}` position, at least one connection with kind and endpoints). `internal/skills/examples_test.go` boots an in-process API server (SQLite in a temp directory, the whole shipped catalogue registered, a credential store, a datastore engine) and drives `cli.Run`: a workflow through `workflow validate` (valid, no diagnostics, no warnings), a request body through `api <operation> --body @file --yes`. Planted failures prove the harness can fail: a model wired through a `main` connection is refused at `/connections/1/kind`, and a `create-credential` body with `data` instead of `fields` is refused. `TestImportExportExampleRoundTripsThroughN8n` creates the import-export example, exports it, requires `lossy[]` empty, imports it back and compares node types and names. Examples were also run against real servers (SQLite, and PostgreSQL with pgvector for the retrieval ingest graph, with a stub OpenAI-compatible endpoint for the agent and embeddings).

Criterion 4. New skills `kilasflow-ai-agents` (EXAMPLE.md chat agent, MODELS.md, RAG.md) and `kilasflow-flow-patterns` (EXAMPLE.md loop, BINARY_DATA.md, SUB_WORKFLOWS.md). The AI skill never lists tools: it gives the `node list --json | jq` filter on `outputs[].kind == "ai_tool"` (excluding the `Imported` placeholder). `RuleSourceCitations` refuses a tree path (`internal/`, `nodes/`, `cmd/`, `web/src/`, `sdk/`, `scripts/`, `e2e/`, `docs/src/`) or a bare `.go`/`.ts`/`.svelte` name outside a fence; it has a planted fixture (`testdata/planted/source-path-citation`) and a table test. About 450 citations were rewritten or removed across 27 files, one commit per skill; PROPERTY_KINDS.md's `## Source` section is gone. `TestSkillCitationsResolve` resolves `(docs: <slug>)` against `docs/src/content/docs` and `kilasflow skills show <skill> --reference X.md` against the skill's references. `TestTheRouterNamesEverySkill` fails when a skill is missing from the router's tree or table.

Deviations from the plan, and why:
- `workflow validate`'s diagnostics carry a `field` path and a `reason`, not the compiler's code, so the planted-agent test asserts the diagnostic on `/connections/1/kind` rather than the string `port.incompatible`; the AI skill states both the message and the code.
- `skills.Examples` returns `([]Example, error)` so a malformed fence is a failure, not a skipped example.
- After review: `TestEverySkillExampleWritesOnlyDeclaredKeys` fails when an example writes a parameter or setting its node does not declare (the compiler stores an undeclared key and ignores it, so a typo validates); it has one allow-list entry, embeddings `mode`, which the node reads for its ports. The source-citation rule flags a path under `nodes/`, `sdk/`, `scripts/`, `e2e/`, `packs/` or `migrations/` only when it names a file or a glob (so "the nodes/connections of a document" passes), keeps `internal/`, `cmd/`, `web/src/`, `sdk/src/` and `docs/src/` as citations whatever follows, and does not read `pkg.go.dev` as a file. The whole-bundle prose gate reads only "no <verb>" as a denial (`without`/`nor` stay in the not-shipped sentence gate). PROPERTY_KINDS.md and the channel list state no count, and `TestPropertyKindsReferenceListsEveryKindTheProductHas` and `TestConnectionChannelsReferenceListsEveryChannelTheProductHas` pin them to `property.KnownKinds()` and `workflow.ConnectionKinds()`.
- `nodes.VectorUnavailableReason` and the boot probe said both the embeddings and the vector store nodes need pgvector; only the vector store does (embeddings call an OpenAI-compatible HTTP API), so both messages now name the vector store alone and a test pins it.
- The G3 gate and the harness register the catalogue through `shippedcatalogue.Register` (internal/shippedcatalogue, from the docs-facts work). Before main carried it they used a local copy of `run()`'s registration calls; that copy is gone.
- The router's command-reference generator (`scripts/skills-command-reference`) parsed the router alone, so a router with a reference table could not be regenerated; it now reads `references/` beside the router (`skills.LoadReferences`), and its generated header no longer names a repo script.
- The empty-token credential the 2026-09-06 memory note describes for a local model is refused by the credential API today ("credential field token is required"); the AI skill says to store any non-empty placeholder.

Findings left for others:
- Activating a parent whose child workflow is inactive is refused, but the API answers a generic 500 "workflow operation failed"; only the server log says which child. The triggers skill claims the refusal names the node.
- The n8n export writes `settings.executionOrder`, which the importer reports back as a `dropped` `settings` issue, so an export is not quite a fixed point. The import-export example test allows exactly that entry.
- The credentials skill's "Writes are one call each through the escape hatch" bullet still describes `create-credential` and `delete-credential` through `kilasflow api`; Track D owns that sentence and should fold the guarded `credential create|update|delete` verbs (added just above it) into it.

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (45):
  - `98ea5939` — merge: every skill carries a validated example, two skills cover AI agents and flow patterns, and the skills cite docs instead of repository source (BUG-x28fsx)
  - `aef67f8d` — chore(pine): BUG-x28fsx records the review fixes
  - `8838c8b0` — BUG-x28fsx: the credentials skill has one bullet for writes: the guarded verbs, and the same operations through the escape hatch
  - `5247c463` — BUG-x28fsx: the skills stop counting kinds and channels, and tests pin the lists that remain to the product's
  - `7e39404d` — BUG-x28fsx: the whole-bundle prose gate reads only "no <verb>" as a denial, and the skills gate helpers carry a skillsGate prefix
  - `e813155a` — BUG-x28fsx: the source-citation rule stops flagging ordinary words and hostnames and starts flagging packs/ and migrations/ files
  - `287d3260` — BUG-x28fsx: a test fails when an example writes a parameter or setting its node does not declare
  - `e4b342a0` — BUG-x28fsx: the vector-store unavailable message names the vector store alone, and the AI skill says embeddings run on SQLite
  - `bfe9465b` — BUG-x28fsx: the credentials example says a type with no probe answers untestable
  - `d0bdd548` — BUG-x28fsx: the AI skill says model lists load through the escape hatch, and the lifecycle skill names the fields validate prints
  - `292efb1c` — chore(pine): BUG-x28fsx ticks criteria 2 and 4 with their evidence and records the example convention
  - `ebb2b350` — BUG-x28fsx: the changelog records the two skills, the validated examples, the citation rule and the skills text that stopped denying shipped verbs
  - `5858f6bc` — BUG-x28fsx: a skill that cites repository source fails the checker, and a citation that does not resolve fails a test
  - `fc077a15` — BUG-x28fsx: the operations skill's upgrade reference cites no repository source
  - `b4db8942` — BUG-x28fsx: the embedding skill and its references cite docs pages and the SDK package, not repository source
  - `bb0b9589` — BUG-x28fsx: the node-packs skill and its two references cite docs pages, not repository source
  - `4b21be0e` — BUG-x28fsx: the import-export skill and its mapping reference cite docs pages, not repository source
  - `89fcfc99` — BUG-x28fsx: the datastore skill and its filter reference cite no repository source
  - `c1172e53` — BUG-x28fsx: the credentials skill points to the live credential-type list instead of a count and cites no repository source
  - `4b886507` — BUG-x28fsx: the node-configuration skill cites docs pages, not repository source
  - `e5742b06` — BUG-x28fsx: the debugging skill and its trace reference cite no repository source
  - `f0ce065c` — BUG-x28fsx: the triggers skill and its webhook reference cite docs pages, not repository source
  - `7d1b41da` — BUG-x28fsx: the error-handling skill cites no repository source
  - `421c21cd` — BUG-x28fsx: the expressions skill cites docs pages and its own reference, not repository source
  - `5bddb2cd` — BUG-x28fsx: the lifecycle skill's references cite the docs and the validate verb, not repository source
  - `77f75f52` — BUG-x28fsx: the router's command-reference generator checks the router with its reference files, and stops citing a repo script
  - `35f7e634` — BUG-x28fsx: two new skills, kilasflow-ai-agents and kilasflow-flow-patterns, and a test that the router names every skill
  - `a2652a51` — BUG-x28fsx: every skill ships a validated example, and a test fails when one does not
  - `28c04751` — BUG-x28fsx: kilasflow-node-packs and kilasflow-embedding ship validated documents, and the embedding text stops saying a session can never activate
  - `850c9392` — BUG-x28fsx: kilasflow-import-export ships a document a test round-trips through the n8n export
  - `8ce1be7e` — BUG-x28fsx: kilasflow-datastore ships a validated create body and a workflow that fills and reads the table
  - `ef4cd38f` — BUG-x28fsx: kilasflow-debugging and kilasflow-credentials ship validated examples
  - `b733671c` — BUG-x28fsx: kilasflow-triggers and kilasflow-error-handling ship validated documents
  - `4b9d3b37` — BUG-x28fsx: kilasflow-expressions ships a validated document with the marker, a node lookup and a literal brace string
  - `eb934f06` — BUG-x28fsx: kilasflow-node-configuration ships a validated document that stores every complex property kind
  - `fecde310` — BUG-x28fsx: a skill's JSON example is compiled by workflow validate in go test, and the first one ships in the lifecycle skill
  - `024b68e5` — BUG-x28fsx: the not-shipped gate refuses a shipped verb, flag or operation in any wording, and the bundle stops denying them
  - `ce0645fb` — chore(pine): the 2026-10-09 parallel wave moves its tickets to doing
  - `3470d86f` — chore(pine): BUG-x28fsx records how many repo source paths the skills still cite
  - `b7b84f38` — BUG-x28fsx: a test catches a skill listing a shipped verb as missing, and three more skills stop denying the guarded write verbs
  - `d02c33c0` — BUG-x28fsx: kilasflow api <op> --schema prints what an operation takes, its body schema resolved, and calls nothing
  - `20786031` — BUG-x28fsx: a test fails when a skill quotes a kilasflow verb the binary does not have
  - `c4600f00` — BUG-x28fsx: a test fails when a skill calls a verb the binary ships 'not shipped'
  - `2345fbe3` — BUG-x28fsx: the debugging, expressions and error-handling skills no longer call debug eval or exec retry unshipped
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   61 +-
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    1 +
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |    8 +-
 .pine/memory/persistence.md                        |    3 +-
 .pine/memory/testing.md                            |    3 +-
 .pine/memory/web-editor.md                         |    5 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |  464 +++
 .pine/tickets/BUG-0grt9g.md                        |  427 +++
 .pine/tickets/BUG-0xv7bg.md                        |   68 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  198 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   83 +
 .pine/tickets/BUG-2n4rfz.md                        |   58 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   51 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  187 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   70 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-49vf3j.md                        |  428 +++
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   88 +
 .pine/tickets/BUG-5bgx5c.md                        |   81 +
 .pine/tickets/BUG-5dn8hr.md                        |   39 +
 .pine/tickets/BUG-5fhcx7.md                        |   50 +
 .pine/tickets/BUG-5xkexq.md                        |   63 +
 .pine/tickets/BUG-605n21.md                        |  154 +
 .pine/tickets/BUG-66fhea.md                        |  112 +
 .pine/tickets/BUG-6d6wbg.md                        |  115 +
 .pine/tickets/BUG-6gkd12.md                        | 1183 ++++++
 .pine/tickets/BUG-719gaz.md                        |  117 +
 .pine/tickets/BUG-9296bf.md                        |  532 +++
 .pine/tickets/BUG-9dw5me.md                        |   66 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        | 1148 ++++++
 .pine/tickets/BUG-a7p6c8.md                        |  487 +++
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-argnka.md                        |   34 +
 .pine/tickets/BUG-asdh5q.md                        |  607 +++
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        | 1252 +++++++
 .pine/tickets/BUG-b8bwhw.md                        |   67 +
 .pine/tickets/BUG-bcahaj.md                        |  113 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   60 +
 .pine/tickets/BUG-c73h98.md                        |   51 +
 .pine/tickets/BUG-c7s5ss.md                        |   33 +
 .pine/tickets/BUG-cmnsfz.md                        |  457 +++
 .pine/tickets/BUG-cnhq1f.md                        |   54 +
 .pine/tickets/BUG-cqbq25.md                        |   41 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   69 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-e8ytyq.md                        |   58 +
 .pine/tickets/BUG-ecbq28.md                        |  132 +
 .pine/tickets/BUG-epy2se.md                        |  131 +
 .pine/tickets/BUG-f85r5h.md                        |   32 +
 .pine/tickets/BUG-fthahg.md                        |  244 ++
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-g9zf51.md                        |  480 +++
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hmp85t.md                        |  116 +
 .pine/tickets/BUG-hnvn3r.md                        |   55 +
 .pine/tickets/BUG-j7qrp2.md                        |   71 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |  527 +++
 .pine/tickets/BUG-jx94q9.md                        |   33 +
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kkkrmx.md                        |  158 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   63 +
 .pine/tickets/BUG-n4e97f.md                        |   51 +
 .pine/tickets/BUG-n6p7qy.md                        | 1314 +++++++
 .pine/tickets/BUG-n9a6bz.md                        |   67 +
 .pine/tickets/BUG-namghh.md                        |  118 +
 .pine/tickets/BUG-nbymq4.md                        |   82 +
 .pine/tickets/BUG-ngt25j.md                        |  739 ++++
 .pine/tickets/BUG-nn74ph.md                        |   60 +
 .pine/tickets/BUG-nzy3pa.md                        |  228 ++
 .pine/tickets/BUG-p334yw.md                        |   65 +
 .pine/tickets/BUG-p3j233.md                        |   71 +
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pder07.md                        |  430 +++
 .pine/tickets/BUG-pdmkn7.md                        |   34 +
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   67 +
 .pine/tickets/BUG-ppvyzr.md                        |   65 +
 .pine/tickets/BUG-pzkpfr.md                        |   76 +
 .pine/tickets/BUG-q6b75c.md                        |   70 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  158 +
 .pine/tickets/BUG-rh7mpa.md                        |   62 +
 .pine/tickets/BUG-rs0xq1.md                        |   52 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   68 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |  107 +
 .pine/tickets/BUG-t9e3k5.md                        |   34 +
 .pine/tickets/BUG-tpyg0q.md                        |   65 +
 .pine/tickets/BUG-txafja.md                        |  762 ++++
 .pine/tickets/BUG-v8ksv8.md                        |   79 +
 .pine/tickets/BUG-vbw239.md                        |  197 +
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w9k234.md                        |   61 +
 .pine/tickets/BUG-x28fsx.md                        |  189 +
 .pine/tickets/BUG-x2sxzt.md                        |  525 +++
 .pine/tickets/BUG-x6gyc1.md                        |   77 +
 .pine/tickets/BUG-x94b3d.md                        |   46 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-xkz7qx.md                        |  499 +++
 .pine/tickets/BUG-xpr3jj.md                        |  416 +++
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        |   38 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   66 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-brpz48.md                       |   77 +
 .pine/tickets/EPIC-hkypt5.md                       |    8 +-
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 1234 +++++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   43 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   38 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   40 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   39 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3taswf.md                       |   16 +-
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |   35 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fhj6p.md                       |    2 +-
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   38 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   40 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   39 +
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   37 +
 .pine/tickets/FEAT-9ep5pw.md                       |   38 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cf5n4j.md                       |   21 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-e898nz.md                       |  210 ++
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gcq50s.md                       |   17 +-
 .pine/tickets/FEAT-gzd32h.md                       |   36 +
 .pine/tickets/FEAT-hxztwz.md                       |   45 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jrrqs2.md                       |   21 +
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   44 +
 .pine/tickets/FEAT-mh4e8g.md                       |   38 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   39 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   40 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   44 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qae4sh.md                       |   23 +
 .pine/tickets/FEAT-qdwm1k.md                       |   50 +
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   41 +
 .pine/tickets/FEAT-r87gtj.md                       |   26 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   42 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   36 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   37 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   37 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   38 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       | 1209 ++++++
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  510 ++-
 CONTRIBUTING.md                                    |   25 +
 Makefile                                           |   65 +
 README.md                                          |  450 +--
 cmd/kilasflow/boot_logger_test.go                  |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |   84 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  204 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  227 ++
 cmd/kilasflow/sqlite_files_test.go                 |   50 +
 config.example.yaml                                |  102 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/credentials.md      |  305 +-
 docs/src/content/docs/concepts/execution-model.md  |   52 +-
 docs/src/content/docs/concepts/expressions.md      |   20 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 docs/src/content/docs/concepts/node-registry.md    |   15 +-
 .../src/content/docs/concepts/safety-boundaries.md |  277 +-
 .../content/docs/concepts/tenancy-and-embedding.md |  100 +-
 docs/src/content/docs/concepts/webhooks.md         |  109 +-
 docs/src/content/docs/guides/code-javascript.md    |  426 +++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |  106 +-
 docs/src/content/docs/guides/n8n-migration.md      |  240 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  184 +-
 docs/src/content/docs/operate/configuration.md     |   16 +
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/security.md          |   47 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/reference/api-contract.md    |   25 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/credentials.md |    6 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |   11 +-
 docs/src/content/docs/reference/cli.md             |  335 +-
 .../content/docs/reference/expression-grammar.md   |   13 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |   20 +-
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |   12 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/datastore-pg.spec.ts                     |    4 +-
 e2e/tests/datastore.spec.ts                        |  115 +-
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/google-oauth.spec.ts                     |   28 +
 e2e/tests/js-code.spec.ts                          |  702 ++++
 e2e/tests/live-backend-api.spec.ts                 |   72 +-
 e2e/tests/n8n-compare.spec.ts                      |   36 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/node-coverage.spec.ts                    |   93 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 +++
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/agent.go                               |   64 +-
 internal/ai/ai.go                                  |   30 +-
 internal/ai/ai_test.go                             |  131 +
 internal/ai/fromai.go                              |  159 +-
 internal/ai/fromai_test.go                         |  121 +-
 internal/ai/fromai_whole_test.go                   |   47 +
 internal/ai/openai.go                              |  247 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  587 ++-
 internal/ai/outputschema_test.go                   |  229 ++
 internal/ai/reasoning_test.go                      |  245 ++
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   28 +
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/credential_confinement_test.go        |  273 ++
 internal/api/credential_testable_test.go           |   62 +
 internal/api/credentials_pagination_test.go        |   58 +
 internal/api/credentials_probe_scope_test.go       |  235 ++
 internal/api/credentials_test.go                   |   81 +-
 internal/api/credentials_update_test.go            |  206 ++
 internal/api/datastores_csv_test.go                |   34 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   45 +-
 internal/api/docs.go                               |   10 +-
 internal/api/embed_confinement_test.go             |    9 +-
 internal/api/embed_publish_test.go                 |  160 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/events_test.go                        |  104 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    6 +-
 internal/api/handlers/credentials.go               |  348 +-
 internal/api/handlers/credentials_probe_test.go    |   75 +
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/datastores_csv.go            |   27 +-
 internal/api/handlers/embed.go                     |    2 +-
 internal/api/handlers/embedscope.go                |  156 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |   59 +-
 internal/api/handlers/execution_waiting_test.go    |   82 +
 internal/api/handlers/executions.go                |  139 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/nodes.go                     |   32 +-
 internal/api/handlers/oauth.go                     |  199 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/system.go                    |   22 +-
 internal/api/handlers/workflow_validate.go         |   95 +-
 internal/api/handlers/workflow_validate_test.go    |  171 +
 internal/api/handlers/workflows.go                 |  107 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_publish_test.go      |   87 +
 internal/api/middleware/embed_sentences_test.go    |   24 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/middleware/scope.go                   |   48 +-
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/node_types_schema_expression_test.go  |   81 +
 internal/api/oauth_test.go                         |  364 +-
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/routes.go                             |   14 +-
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    8 +-
 internal/api/server_test.go                        |   40 +
 internal/api/workflow_tags_test.go                 |  342 ++
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  143 +-
 internal/cli/command_test.go                       |    4 +-
 internal/cli/docs_examples_test.go                 |  122 +
 internal/cli/document_flag_help_test.go            |   31 +
 internal/cli/exit.go                               |  136 +
 internal/cli/exit_reference_test.go                |  286 ++
 internal/cli/exit_test.go                          |   26 +
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |  134 +-
 internal/cli/openapi_contract_test.go              |   43 +-
 internal/cli/skills_not_shipped_test.go            |  410 +++
 internal/cli/sse.go                                |   27 +-
 internal/cli/verbs_api.go                          |   83 +-
 internal/cli/verbs_api_test.go                     |  324 +-
 internal/cli/verbs_credential.go                   |    4 +-
 internal/cli/verbs_credential_test.go              |   71 +
 internal/cli/verbs_datastore.go                    |    5 +-
 internal/cli/verbs_debug.go                        |   67 +-
 internal/cli/verbs_debug_test.go                   |   82 +
 internal/cli/verbs_exec.go                         |   16 +
 internal/cli/verbs_exec_test.go                    |   43 +
 internal/cli/verbs_skills.go                       |   94 +-
 internal/cli/verbs_skills_test.go                  |  119 +
 internal/cli/verbs_workflow.go                     |  110 +-
 internal/cli/verbs_workflow_tag_test.go            |   84 +
 internal/cli/verbs_workflow_test.go                |   59 +
 internal/config/boot_strictness_test.go            |  216 ++
 internal/config/config.go                          |  364 +-
 internal/config/config_test.go                     |  142 +
 internal/config/sqlite_root_test.go                |   51 +
 internal/credentials/builtin.go                    |   62 +-
 internal/credentials/credentials.go                |  143 +-
 internal/credentials/credentials_test.go           |  164 +-
 internal/credentials/external.go                   |   13 +-
 internal/credentials/oauth.go                      |  117 +-
 internal/credentials/oauth_redirect_test.go        |   41 +
 internal/credentials/oauth_test.go                 |   95 +-
 internal/credentials/probe_test.go                 |  181 +
 internal/credentials/registry.go                   |   99 +-
 internal/credentials/scope.go                      |  102 +
 internal/credentials/scope_test.go                 |  215 ++
 internal/credentials/scrub.go                      |  107 +
 internal/credentials/scrub_test.go                 |   61 +
 internal/credentials/vault.go                      |   10 +-
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    2 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/docexamples/docexamples.go                |  318 ++
 internal/docexamples/docexamples_test.go           |  209 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   87 +-
 internal/embed/embed_publish_test.go               |  113 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  511 +++
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |  155 +-
 internal/engine/authenticate_test.go               |   74 +
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/batch_nodes_tolerance_test.go      |  108 +
 internal/engine/checkpoint.go                      |   94 +-
 internal/engine/checkpoint_test.go                 |  101 +
 internal/engine/console_test.go                    |  397 ++
 internal/engine/error_workflow_test.go             |  312 +-
 internal/engine/eval.go                            |  287 +-
 internal/engine/eval_test.go                       |  269 +-
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  131 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |  157 +
 internal/engine/node_branches.go                   |   87 +
 internal/engine/node_timing_test.go                |  211 ++
 internal/engine/per_item_suspend_test.go           |  438 +++
 internal/engine/required_inputs_test.go            |  198 +
 internal/engine/resume_failure_test.go             |  174 +
 internal/engine/run_presentation_test.go           |   77 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          | 1204 +++++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/secret_scrub.go                    |  159 +
 internal/engine/secret_scrub_test.go               |  134 +
 internal/engine/service.go                         |  216 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  259 +-
 internal/engine/wait_error_output_test.go          |  181 +
 internal/engine/wait_links_test.go                 |  159 +
 internal/engine/wait_service.go                    |   87 +-
 internal/engine/wait_service_test.go               |  641 +++-
 internal/engine/worker_test.go                     |    8 +
 internal/events/events.go                          |   37 +-
 internal/events/events_test.go                     |   68 +
 internal/execution/records.go                      |    7 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   38 +
 internal/expression/expression_test.go             |   66 +
 internal/expression/globals.go                     |   22 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/idempotency/once.go                       |   64 +
 internal/idempotency/once_test.go                  |   56 +
 internal/interop/n8n/chatmodel_import_test.go      |  332 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/connection_order_test.go      |   52 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/extract_import_test.go        |   83 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/importer_tail_test.go         |   64 +-
 internal/interop/n8n/legacy_agent_test.go          |  120 +
 internal/interop/n8n/loose.go                      |  252 ++
 internal/interop/n8n/loose_json_test.go            |  239 ++
 internal/interop/n8n/n8n.go                        |  544 ++-
 internal/interop/n8n/n8n_test.go                   |  597 ++-
 internal/interop/n8n/parameters.go                 |  979 ++++-
 internal/interop/n8n/rag_import_test.go            |  215 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |  145 +
 internal/interop/n8n/set_null_import_test.go       |  150 +
 internal/interop/n8n/switch_import_test.go         |  371 ++
 internal/interop/n8n/tags.go                       |  182 +
 internal/interop/n8n/tags_test.go                  |  312 ++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   12 +-
 internal/interop/n8n/testdata/n8n_rag_3647.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_4799.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_5908.json    |    2 +-
 internal/interop/n8n/tool_variant_import_test.go   |  111 +
 internal/interop/n8n/topology_import_test.go       |  147 +
 internal/interop/n8n/vectorstore.go                |  153 +
 internal/interop/n8n/vectorstore_import_test.go    |  281 ++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 ++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 ++
 internal/interop/n8n/workflow_tool_self_test.go    |   93 +
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  330 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  230 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  295 ++
 internal/jsrun/js/runtime.js                       | 1726 +++++++++
 internal/jsrun/jsrun.go                            |  247 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  152 +
 internal/jsrun/roots_test.go                       |  584 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  304 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  250 ++
 internal/jsworker/doc.go                           |   78 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  952 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/loadoptions/loadoptions.go                |   14 +-
 internal/loadoptions/loadoptions_test.go           |   30 +
 internal/loadoptions/redirect_test.go              |   30 +
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   21 +-
 internal/loadoptions/workflows.go                  |   79 +
 internal/loadoptions/workflows_test.go             |  158 +
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   60 +-
 internal/nodepack/trigger.go                       |   77 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/property/editor_test.go                   |   51 +
 internal/property/mapper.go                        |    5 +
 internal/property/mapper_test.go                   |   22 +
 internal/property/property.go                      |   52 +
 internal/repository/auth.go                        |    9 +-
 internal/repository/credentials.go                 |  137 +-
 internal/repository/credentials_page_test.go       |   69 +
 internal/repository/executions.go                  |  262 +-
 internal/repository/models.go                      |   11 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   22 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/waits.go                       |    4 +
 internal/repository/waits_test.go                  |   68 +
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 +
 internal/repository/workflow_tags_test.go          |  283 ++
 internal/repository/workflows.go                   |   44 +-
 internal/routing/executor.go                       |   13 +-
 internal/runcode/diagnostic_internal_test.go       |   38 +
 internal/runcode/runcode.go                        |   58 +-
 internal/runcode/runcode_test.go                   |   80 +
 internal/runcode/sandbox.go                        |   73 +
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/redact.go                        |   82 +
 internal/safehttp/redact_test.go                   |  108 +
 internal/safehttp/redirect_scope_test.go           |  141 +
 internal/safehttp/safehttp.go                      |   62 +-
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/shippedcatalogue/shippedcatalogue.go      |  162 +
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 +
 internal/skills/bundle_test.go                     |   35 +
 internal/skills/check.go                           |  146 +
 internal/skills/check_test.go                      |   59 +
 internal/skills/examples.go                        |  287 ++
 internal/skills/examples_coverage_test.go          |   49 +
 internal/skills/examples_shape_test.go             |  121 +
 internal/skills/examples_test.go                   |  646 ++++
 internal/skills/skill.go                           |   35 +
 internal/skills/skills_test.go                     |  254 +-
 .../skills/kilasflow-fixture/SKILL.md              |   32 +
 internal/sqlbuild/sqlbuild_test.go                 |    5 +-
 internal/sqlnode/export_test.go                    |   32 +-
 internal/sqlnode/guard_test.go                     |    2 +-
 internal/sqlnode/internal_test.go                  |    1 +
 internal/sqlnode/sqlite.go                         |  302 ++
 internal/sqlnode/sqlite_bounded_test.go            |  242 ++
 internal/sqlnode/sqlite_confinement_test.go        |  300 ++
 internal/sqlnode/sqlite_unix_test.go               |  116 +
 internal/sqlnode/sqlnode.go                        |  100 +-
 internal/sqlnode/sqlnode_test.go                   |   37 +-
 internal/sqlnode/tls_test.go                       |  200 +
 internal/tenantpurge/harness_test.go               |   20 +-
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/wasmpack/hostcalls_http.go                |   24 +-
 internal/wasmpack/probe_test.go                    |    4 +-
 internal/web/embed.go                              |   43 +-
 internal/web/embed_test.go                         |   95 +
 internal/webhook/header_auth_redaction_test.go     |   72 +
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_redaction_test.go       |   74 +
 internal/webhook/lifecycle_scope_test.go           |  129 +
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  581 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 ++
 internal/webhook/secret_record_test.go             |   66 +
 internal/webhook/webhook.go                        |   69 +-
 internal/webhook/webhook_test.go                   |   86 +
 internal/workflow/compiler.go                      |  247 +-
 internal/workflow/compiler_test.go                 |   85 +
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |   18 +
 internal/workflow/document_test.go                 |   44 +
 internal/workflow/shape.go                         |   43 +
 internal/workflow/tags.go                          |  113 +
 internal/workflow/tags_test.go                     |  188 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 nodes/ai.go                                        |  792 +++-
 nodes/ai_fromai_data_test.go                       |  392 ++
 nodes/ai_scope_test.go                             |   52 +
 nodes/ai_test.go                                   |  418 ++-
 nodes/ai_tools_test.go                             |  334 ++
 nodes/annotation.go                                |    1 +
 nodes/batch_shape_test.go                          |  157 +
 nodes/chat.go                                      |    2 +-
 nodes/code.go                                      |   46 +-
 nodes/code_test.go                                 |   48 +
 nodes/core.go                                      |   26 +-
 nodes/credential_redaction_test.go                 |   76 +
 nodes/credential_redirect_test.go                  |  145 +
 nodes/credentialscope_test.go                      |  131 +
 nodes/database.go                                  |   14 +-
 nodes/database_test.go                             |   69 +-
 nodes/datastore.go                                 |  626 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_test.go                            |  127 +-
 nodes/datastore_tool_test.go                       | 1366 ++++++-
 nodes/embedscope.go                                |  141 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   37 +-
 nodes/executors_test.go                            |    2 +
 nodes/extract.go                                   |  250 +-
 nodes/extract_test.go                              |   90 +
 nodes/flow.go                                      |   21 +-
 nodes/glyphs_test.go                               |  151 +
 nodes/google.go                                    |    2 +-
 nodes/http.go                                      |   96 +-
 nodes/http_error_test.go                           |  235 ++
 nodes/item_inputs_test.go                          |  112 +
 nodes/jscode.go                                    |  319 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  207 ++
 nodes/jscode_roots.go                              |   49 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  361 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/model_redirect_internal_test.go              |  189 +
 nodes/mysql_v2.go                                  |    2 +-
 nodes/openai_credential_url_test.go                |   54 +
 nodes/pgvector.go                                  |   54 +-
 nodes/pgvector_customer.go                         |   30 +-
 nodes/pgvector_test.go                             |   13 +-
 nodes/postgres_v2.go                               |    2 +-
 nodes/rag.go                                       |   13 +
 nodes/rag_test.go                                  |    6 +-
 nodes/sidecar_egress.go                            |   28 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/sqlite_attach_test.go                        |    4 +-
 nodes/subworkflow.go                               |  102 +-
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/telegram_download.go                         |   15 +-
 nodes/telegram_lifecycle.go                        |   92 +-
 nodes/testdata/n8n_chat_model_options.json         |    3 +-
 nodes/tool_fromai.go                               |  151 +
 nodes/transform.go                                 |  127 +-
 nodes/vectorstore_retrieve.go                      |   84 +
 nodes/vectorstore_retrieve_test.go                 |  250 ++
 nodes/vectorstore_tool.go                          |  325 ++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++
 nodes/vectorstore_tool_test.go                     |  565 +++
 nodes/webhook.go                                   |    3 +
 nodes/workflow_tool_inputs_test.go                 |  263 ++
 nodes/workflow_tool_locator_test.go                |   50 +
 packs/telegram/default_base_url_test.go            |   32 +
 packs/telegram/redaction_test.go                   |   43 +
 packs/waha/waha_test.go                            |   55 +-
 schemas/workflow-v1.schema.json                    |   24 +-
 scripts/cli-examples/main.go                       |  583 +++
 scripts/cli-examples/main_test.go                  |  187 +
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 +
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  275 ++
 scripts/cli-examples/server.go                     |  183 +
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/docs-facts/main.go                         |  177 +
 scripts/docs-facts/main_test.go                    |  278 ++
 scripts/generate-api-reference.mjs                 |   51 +-
 scripts/internal/splice/splice.go                  |  149 +
 scripts/internal/splice/splice_test.go             |  112 +
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 scripts/skills-command-reference/main.go           |  130 +-
 scripts/smoke-cli.sh                               |   10 +-
 sdk/CHANGELOG.md                                   |   13 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |  501 ++-
 sdk/src/server.ts                                  |   37 +-
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |  163 +-
 skills/kilasflow-ai-agents/SKILL.md                |  114 +
 skills/kilasflow-ai-agents/references/EXAMPLE.md   |  146 +
 skills/kilasflow-ai-agents/references/MODELS.md    |  105 +
 skills/kilasflow-ai-agents/references/RAG.md       |  188 +
 skills/kilasflow-credentials/SKILL.md              |   47 +-
 .../references/CREDENTIAL_TYPES.md                 |   48 +-
 skills/kilasflow-credentials/references/EXAMPLE.md |   97 +
 skills/kilasflow-datastore/SKILL.md                |   51 +-
 skills/kilasflow-datastore/references/EXAMPLE.md   |  130 +
 skills/kilasflow-datastore/references/FILTERS.md   |   42 +-
 skills/kilasflow-debugging/SKILL.md                |   38 +-
 skills/kilasflow-debugging/references/EXAMPLE.md   |   70 +
 .../references/TRACE_READING.md                    |   43 +-
 skills/kilasflow-embedding/SKILL.md                |   56 +-
 .../references/EMBED_HANDSHAKE.md                  |   17 +-
 skills/kilasflow-embedding/references/EXAMPLE.md   |   82 +
 .../references/SESSION_AUTHORITY.md                |   10 +-
 skills/kilasflow-error-handling/SKILL.md           |   39 +-
 .../kilasflow-error-handling/references/EXAMPLE.md |  117 +
 skills/kilasflow-expressions/SKILL.md              |   50 +-
 skills/kilasflow-expressions/references/EXAMPLE.md |  134 +
 .../references/EXPRESSION_ROOTS.md                 |   63 +-
 skills/kilasflow-flow-patterns/SKILL.md            |  105 +
 .../references/BINARY_DATA.md                      |  116 +
 .../kilasflow-flow-patterns/references/EXAMPLE.md  |  134 +
 .../references/SUB_WORKFLOWS.md                    |  169 +
 skills/kilasflow-import-export/SKILL.md            |   41 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |  107 +
 .../references/MAPPING_LIMITS.md                   |   85 +-
 skills/kilasflow-node-configuration/SKILL.md       |   38 +-
 .../references/EXAMPLE.md                          |  141 +
 .../references/LOAD_OPTIONS.md                     |   49 +-
 .../references/PROPERTY_KINDS.md                   |   67 +-
 skills/kilasflow-node-packs/SKILL.md               |   38 +-
 skills/kilasflow-node-packs/references/EXAMPLE.md  |   75 +
 .../references/MODULE_AND_SIDECAR.md               |   32 +-
 .../kilasflow-node-packs/references/PACK_FORMAT.md |   28 +-
 skills/kilasflow-operations/SKILL.md               |   17 +-
 skills/kilasflow-operations/references/EXAMPLE.md  |   86 +
 .../references/UPGRADE_ORDER.md                    |   15 +-
 skills/kilasflow-triggers/SKILL.md                 |   48 +-
 skills/kilasflow-triggers/references/EXAMPLE.md    |   80 +
 .../references/WEBHOOK_DELIVERY.md                 |   33 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |   36 +-
 .../references/EXAMPLE.md                          |  111 +
 .../references/NAMING_AND_DESCRIPTIONS.md          |   25 +-
 .../references/VALIDATION_CHECKLIST.md             |   29 +-
 skills/using-kilasflow-skills/SKILL.md             |   41 +-
 .../using-kilasflow-skills/references/EXAMPLE.md   |   65 +
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/auth.json                          |    2 +
 web/messages/en/credentials.json                   |    3 +
 web/messages/en/datastores.json                    |   48 +-
 web/messages/en/editor.json                        |   49 +-
 web/messages/en/executions.json                    |  193 +-
 web/messages/en/nav.json                           |    2 +-
 web/messages/en/properties.json                    |   17 +-
 web/messages/en/settings.json                      |    9 +-
 web/messages/en/workflows.json                     |   25 +-
 web/messages/id/auth.json                          |    2 +
 web/messages/id/credentials.json                   |    3 +
 web/messages/id/datastores.json                    |   48 +-
 web/messages/id/editor.json                        |   59 +-
 web/messages/id/executions.json                    |  193 +-
 web/messages/id/nav.json                           |    2 +-
 web/messages/id/properties.json                    |   17 +-
 web/messages/id/settings.json                      |    9 +-
 web/messages/id/workflows.json                     |   25 +-
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   52 +
 .../lib/api/generated/credentials/credentials.ts   |    6 +-
 web/src/lib/api/generated/executions/executions.ts |    4 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/credentialBody.ts |    4 +-
 .../api/generated/models/credentialBodyFields.ts   |    2 +-
 .../lib/api/generated/models/credentialResource.ts |    5 +-
 .../api/generated/models/credentialTypeResource.ts |    9 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |    2 +-
 .../generated/models/evalExpressionInputBody.ts    |   12 +-
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    3 +
 .../lib/api/generated/models/healthOutputBody.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   16 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../generated/models/resourceMapperDeclaration.ts  |    1 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../lib/api/generated/models/testPayloadBody.ts    |    4 +-
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../generated/models/validateWorkflowResource.ts   |    6 +
 .../lib/api/generated/models/validationWarning.ts  |   13 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 web/src/lib/api/http.ts                            |   32 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../workflow-editor/canvas-bridge.svelte           |    3 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../components/workflow-editor/canvas-node.svelte  |  110 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/code-editor.svelte  |   92 +
 .../workflow-editor}/diagnostics-section.svelte    |    0
 .../workflow-editor/execution-canvas-node.svelte   |   45 +-
 .../workflow-editor/execution-canvas.svelte        |    6 +-
 .../workflow-editor}/import-report.svelte          |    0
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../components/workflow-editor/node-picker.svelte  |   22 +-
 .../workflow-editor/panel-query-harness.svelte     |   19 +
 .../workflow-editor/paste-report-sheet.svelte      |   45 +
 .../workflow-editor/properties-panel.svelte        |  180 +-
 .../workflow-editor/properties-panel.test.ts       |  174 +
 .../workflow-editor/property-field.svelte          |  258 +-
 .../workflow-editor/property-field.test.ts         |  214 ++
 .../workflow-editor/version-panel.svelte           |   15 +-
 .../workflow-editor/webhook-address.svelte         |   91 +
 .../workflow-editor/webhook-address.test.ts        |   63 +
 .../workflow-editor/workflow-editor.svelte         |  464 ++-
 web/src/lib/dashboard/auth-mode.test.ts            |   65 +
 web/src/lib/dashboard/auth-mode.ts                 |   79 +
 web/src/lib/dashboard/credential-scope.test.ts     |   34 +
 web/src/lib/dashboard/credential-scope.ts          |   31 +
 web/src/lib/dashboard/credential-test.test.ts      |   45 +
 web/src/lib/dashboard/credential-test.ts           |   27 +
 web/src/lib/dashboard/sidebar.test.ts              |   34 +
 web/src/lib/dashboard/sidebar.ts                   |   35 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 +-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/datastore/columns.test.ts              |   36 +
 web/src/lib/datastore/columns.ts                   |   30 +
 web/src/lib/datastore/transfer.test.ts             |   33 +
 web/src/lib/datastore/transfer.ts                  |   39 +-
 web/src/lib/embed/embed-editor.svelte              |   94 +-
 web/src/lib/embed/session.svelte.ts                |   68 +-
 web/src/lib/embed/session.test.ts                  |  118 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/canvas-actions.ts      |    2 +
 web/src/lib/workflow-editor/canvas-chrome.test.ts  |   96 +
 web/src/lib/workflow-editor/canvas-chrome.ts       |   62 +
 .../lib/workflow-editor/canvas-projection.test.ts  |  147 +
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   69 +-
 web/src/lib/workflow-editor/chat.ts                |   86 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 ++
 web/src/lib/workflow-editor/document.test.ts       |   73 +-
 web/src/lib/workflow-editor/document.ts            |  265 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   79 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   41 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |  169 +-
 web/src/lib/workflow-editor/execution.ts           |  181 +-
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 web/src/lib/workflow-editor/glyphs.json            |   57 +
 web/src/lib/workflow-editor/layout.ts              |    9 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |  126 +-
 web/src/lib/workflow-editor/node-visual.ts         |  165 +-
 web/src/lib/workflow-editor/picker-rows.test.ts    |  100 +
 web/src/lib/workflow-editor/picker-rows.ts         |   38 +
 web/src/lib/workflow-editor/ports.test.ts          |   37 +
 web/src/lib/workflow-editor/ports.ts               |   38 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 +-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 +-
 web/src/lib/workflow-editor/save-feedback.test.ts  |   52 +
 web/src/lib/workflow-editor/save-feedback.ts       |   41 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   94 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   87 +-
 web/src/lib/workflow-editor/sticky.test.ts         |   45 +-
 web/src/lib/workflow-editor/sticky.ts              |   87 +-
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../lib/workflow-editor/webhook-address.test.ts    |  151 +
 web/src/lib/workflow-editor/webhook-address.ts     |   82 +
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   40 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  131 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   58 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   88 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  245 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |   32 +-
 web/src/routes/+page.svelte                        |    6 +-
 web/src/routes/approve/[token]/+page.svelte        |    8 +
 web/src/routes/login/+page.svelte                  |   70 +-
 1123 files changed, 151357 insertions(+), 5280 deletions(-)
```
