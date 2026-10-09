---
id: FEAT-cf5n4j
title: Agent tool forms of the Gmail, Postgres and Telegram nodes (so n8n gmailTool/postgresTool/telegramTool import)
status: done
priority: medium
created: "2026-10-08T16:58:14Z"
updated: "2026-10-09T13:33:34Z"
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
- [x] A generic mechanism: one entry in `nodes.toolForms` gives a registered node a tool form that the agent runs through the base's registered executor, with no per-node tool code. Adding a form takes one `toolForms` entry plus one importer `toolVariants` line. (`nodes/tool_form.go`; `TestToolFormsDeriveFromTheirBase` is table-driven, one row per form; `internal/interop/n8n/n8n.go` `toolVariants`)
- [x] The tool schema comes from its `$fromAI` calls, which a tool form takes inside expressions only: `$fromAI` in plain text, in any spelling the filler or the evaluator accepts (`$fromai`, `$fromAi`, `$FROMAI`, a space before the parenthesis), is refused when the node is validated and again when the agent builds the tool, so nothing is ever filled into a parameter. The model's values reach the base executor only through `engine.Request.FromAIArguments`. A model value containing `{{ … }}` or an expression marker reaches the service as literal text; each form has a test proving it. (`TestToolFormsDeriveFromTheirBase` and `TestThePostgresToolKeepsTheSQLTheAuthors` spelling rows; `TestExpressionContextCarriesAToolCallsFromAIArguments`; `TestTheGmailToolSendsWhatTheModelAddressesAsText`, `TestTheTelegramToolRunsThePacksOperationWithTheModelsText`, `TestThePostgresToolBindsTheModelsValues`, `TestPostgresToolLive`, `TestAnImportedTelegramToolRuns` all send `{{ $env.SECRET }}` and assert it arrives verbatim; a marker argument is refused by the existing `toolFromAIArguments`)
- [x] Safety rules: `$fromAI` in `resource` or `operation` is refused when the node is validated; the Postgres tool refuses `$fromAI` or an expression in `query` (values go through bound Query Parameters, conditions or column values); a tool's output to the model is capped at 256 KiB, with a note saying it was cut; the agent resolves the base node from the tool type on the server side, never from the descriptor. (`TestToolFormsDeriveFromTheirBase` refusal table, `TestThePostgresToolKeepsTheSQLTheAuthors`, `TestAToolResultIsCappedWithANote`, `TestTheAgentResolvesAToolFormsBaseItselfOrRefuses`, `TestAToolFormsBaseIsResolvedForTheRunningTenant`, `TestADescriptorCannotNameItsExecutor`, `TestAComputedConditionColumnThatResolvesEmptyIsRefused`; resource, operation and the structural keys also refuse plain `{{ }}` text)
- [x] `kilasflow.gmailTool` sends, gets and lists through the Gmail executor. `pack.telegramTool` runs the pack's operations through the routing interpreter. `kilasflow.postgresTool` runs its operations with bound values. Each is proven by an agent test with a scripted model and a stub service; Postgres has a statement-shape test plus a gated live test on `KILASFLOW_TEST_POSTGRES_DSN`. (`TestTheGmailToolSendsWhatTheModelAddressesAsText`, `TestTheGmailToolListsWithTheModelsSearch`, `TestTheGmailToolGetsTheMessageTheModelNames`; `TestTheTelegramToolRunsThePacksOperationWithTheModelsText`; `TestThePostgresToolBindsTheModelsValues`, `TestThePostgresToolQuotesATableTheModelNames`; `TestPostgresToolLive` passed against postgres:16-alpine on 2026-10-09)
- [x] A pack-derived tool form is visible to exactly the tenants its base is, including after `packs.visible_to`. A grant that names the tool form directly is refused, and the message names the base. (`internal/node/tool_form_scope_test.go`, `TestTheTelegramToolFollowsThePacksScope`)
- [x] Import: gmailTool, postgresTool and telegramTool map onto the three forms using the base translator, `toolName` taken from the node name, `toolDescription` (n8n's own; when n8n stored none, a written one, reported as lossy), and nested `=` strings converted to expressions. A postgresTool whose SQL is written by the model is a blocking issue that names the rule. A fixture of each form imports with no blocking issue apart from re-binding credentials, and compiles on an agent. (`internal/interop/n8n/tool_form_import_test.go`: `TestAGmailToolImportsAsTheGmailToolForm`, `TestAnAutomaticToolDescriptionIsWrittenAndReported`, `TestAPostgresToolImportsWithItsValuesBound`, `TestAPostgresToolWhoseModelWritesSQLIsBlocked`, `TestATelegramToolImportsAsThePacksToolForm`)
- [x] Gmail base fidelity: n8n `sendTo` maps to `to` and back. A missing `operation` imports as n8n's default. An operation or resource the node does not perform is a blocking issue. (`TestGmailImportsWithN8NsNamesAndDefaults`; also version 1's `toList` and its `draft` default)
- [x] Export: each form exports as `<n8n base>Tool` at the base's version, with `toolDescription` and `descriptionType: "manual"`. A `toolName` that differs from the node name is reported as lossy. `SupportedMappings` lists the four derived pairs (including httpRequestTool). (`TestToolFormsExportAsN8NsToolOfTheBase`, `TestARenamedToolFormSaysItsNameDoesNotSurviveExport`, `TestSupportedMappingsAreAdvertisedExplicitly`)
- [x] Catalogue upkeep: the docs' node counts, glyph variants, the e2e node-coverage list, and the n8n migration guide's AI table are updated. (`TestTheDocumentedCatalogueSizeIsTheShippedOne`: 65 types / 72 pairs, 61 (66) compiled, 4 (6) from packs; `glyphVariants`; `e2e/tests/node-coverage.spec.ts` VALIDATION_COVERAGE plus a bare-form refusal test, which passed; `guides/n8n-migration.md`, and `guides/tenant-scoped-nodes.md` for the scope rule)

# Implementation Plan

Plan: Track C of the 2026-10-09 parallel wave. Engine `Request.FromAIArguments` feeds `$fromAI` as data; `nodes/tool_form.go` holds the `toolForms` table, the derived definitions, the `core.toolForm` descriptor executor and the agent-side `formTool`; `internal/node` scopes a pack tool form with its base; `internal/interop/n8n/tool_forms.go` holds the generic `<base>Tool` translators.

# Notes

## n8n behaviour this relies on (read 2026-10-09 from n8n's public source on GitHub, master; described here, nothing copied)

- **Tool forms.** n8n generates a `<type>Tool` for every node whose description declares itself usable as a tool, at the base node's own versions, with no item inputs and one ai_tool output. It adds two parameters when the node has none of its own: a description mode with two choices, set automatically (the default when the node has a resource or an operation) and set manually, and the description text, which is shown only in manual mode and defaults to the node's own description. The model calls the tool by the node's canvas name.
- **Generated descriptions.** In automatic mode n8n builds the description from the chosen operation's action label (or the raw operation and resource values) followed by the node type's default name. When reading a node, n8n uses the stored text unless the mode is automatic or the text is blank. The importer therefore carries any stored text, and writes one plus a lossy note only when none is stored; exporting writes manual mode so n8n keeps the text.
- **Gmail.** Versions 1, 2, 2.1 and 2.2 exist; only the version-2 implementation is usable as a tool. In version 2 the default resource is message and the default operation is send, the recipient parameter is `sendTo`, and the email type (text or HTML) defaults to HTML. Version 1 defaults to the draft resource, its send operation keeps recipients in a `toList` array, and an HTML body is a separate optional field. Attribution is appended by default from 2.1 on.
- **PostgreSQL.** The version-2 implementation covers 2 to 2.7 and is usable as a tool; its default operation is insert. From 2.5, an expression query replacement is resolved one `{{ }}` segment at a time, each segment one bound value, and the text between segments is ignored; a replacement without segments is split on commas. An expression in the query text is substituted into the SQL, not bound.
- **Telegram.** Versions 1, 1.1 and 1.2, usable as a tool; default resource message, default operation sendMessage; `chatId` and `text` become the Bot API's `chat_id` and `text`.

## Decisions

- **Postgres `table` and `schema` take `$fromAI`** (orchestrator decision 2). Every identifier the builder writes goes through `sqlbuild.Dialect.Identifier`, which checks the part and quotes it with pgx's identifier quoting, doubling any embedded quote. `TestThePostgresToolQuotesATableTheModelNames` sends `x"; DROP TABLE y; --` and asserts the whole statement is `SELECT * FROM "public"."x""; DROP TABLE y; --"`; `TestPostgresToolLive` sends a hostile table name to a real server and gets a missing-relation error with the table intact. Structural keys stay `query`, `deleteCommand` and `options`.
- **A tool form runs on the agent's current input item** (review item 3). The agent hands each tool form the item it is answering (`bindToolsToItem`), lineage included, so `$json` and `$input` read it as an n8n sub-node reads its root node's input, and `$('Node')` pairs through it. The model's values still have no route in but `$fromAI` (`TestAToolFormReadsTheAgentsInputItem`). A form with no `$fromAI` takes no arguments (an empty object schema), unlike the HTTP tool, which keeps its legacy `input` object.
- **Plain-text `$fromAI` is refused in tool forms** (review item 1). Nothing that authors a tool form writes one: n8n puts every `$fromAI` inside an `=` expression, which imports as a marker (nested ones through `deepFromN8NValue`), and the editor has no `$fromAI` authoring yet. Refusing it removes the fill path, so the parameters are never rewritten with model text; the detection is a case-insensitive superset of the scanner's match. Structural keys also refuse plain `{{ }}` text (review item 2), which the Google nodes would evaluate as the author's template; for the PostgreSQL tool that includes `query`, so the one legitimate shape it breaks is SQL with a literal `{{` in a string or JSON literal, which has to move into a bound value.
- **A computed SQL condition column that resolves empty is refused** (review item 7) for every node on the PostgreSQL/MySQL operation-set executor, not only tool forms: dropping the condition broadens a select or delete whoever computed the column. A row the author left blank is still skipped.

## Deviations from the plan

- Definitions and runtime landed in one commit (plan steps 4 and 5): `nodes/tool_form.go` holds both, and splitting the file to commit the derivation alone would have committed untested runtime code anyway.
- The definition tests live in `nodes/tool_form_test.go` rather than in `TestToolFamilyDefinitions`, which Track F also edits.
- Added beyond the plan, each with tests: Gmail `send` refuses a line break in `to` or `subject` (a header-injection hole a webhook could already reach and gmailTool would hand to a model); Gmail gains `emailType` (text or html) so an imported n8n send keeps its HTML; `queryReplacementToKilas` binds one value per `{{ }}` segment of an expression replacement (the base PostgreSQL import bound the expressions' source text before, which also broke postgresTool's usual shape) and exports such a list back as segments; `httpToolToKilas` and `httpToolToN8N` now share `withToolFields` and `toolNameLossy` with the tool forms.
- The tool description rule is "carry n8n's stored text; write one and report it when none is stored" rather than "write one whenever descriptionType is auto or absent": n8n itself uses stored text unless the mode is automatic, and in automatic mode the stored text is the one n8n generated.
- The RSS follow-up went onto FEAT-kfmq1z as a note (orchestrator decision 3) instead of a new ticket.
- The Google double-evaluation fix is BUG-prhxdy.

## Progress (2026-10-09)

- Engine: `engine.Request.FromAIArguments`, carried into every executor's expression context by `Request.ExpressionContext`.
- Registry: `node.Definition.ToolFormOf`; a tool form must be registered after its base and with its base's scope; `ApplyVisibility` rescopes a base's tool forms and refuses a grant naming a tool form, naming the base.
- Nodes: `nodes/tool_form.go` (`toolForms`, `toolFormDefinition`, `registerToolForms`, `RegisterPackToolForms`, `executeToolForm` bound as `core.toolForm`, `WithAgentToolForms`, `toolFormFrom`, `formTool`, `bindToolsToItem`, `checkToolFormParameters`, `validatePostgresToolQuery`). `nodes/ai.go` gained one field and one `toolFrom` case. `RegisterAll` registers the built-in forms; `cmd/kilasflow/main.go` passes `WithNodeCatalog(nodeRegistry)` and calls `RegisterPackToolForms` after the packs and before `packs.visible_to`.
- Importer: `internal/interop/n8n/tool_forms.go` (`toolFormToKilas`, `toolFormToN8N`, `withToolFields`, `deepFromN8NValue`, `deepToN8NValue`, `postgresToolToKilas`, `gmailToKilas`, `gmailToN8N`); `toolVariants` gained the three forms; `toolVariantMapping` uses the base's version; `byKilasType` falls back to `toolVariantExportMapping`; `SupportedMappings` lists the derived pairs; the Gmail mapping publishes 2, 2.1 and 2.2.
- Test harnesses that register the packs now register their tool forms too (`n8n_test.go` `registry`, `corpus/scoreboard_test.go`, `cmd/kilasflow/catalogue_docs_test.go`).
- Not done here: the corpus is not materialised in this worktree, so `make corpus-check` skipped and no baseline was rescored.

## Left for later

- Track B's `googleSheetsTool`: one `toolForms` row, one `toolVariants` line, one row in `toolFormRows`, a glyph variant, an e2e entry and new counts.
- `$input` in a tool form holds only the item being answered, where an n8n sub-node's `$input` is its root node's whole input.

## Review fixes (2026-10-09)

- Merged main; the catalogue size is regenerated by `make generate-docs-facts` from `internal/shippedcatalogue`, whose `Register` now calls `RegisterPackToolForms` as `run()` does (66 types / 73 pairs after the merge: 62 (67) compiled, 4 (6) from packs). `nodes.WithNodeCatalog` is named in the drift test as a call that only reads the registry.
- 1 (critical): plain-text `$fromAI` in any spelling is refused in tool forms (see Decisions); rows for `$fromai(`, `$fromAi(`, `$fromAI (` and `$FROMAI(` in both refusal tables.
- 2: resource, operation and structural keys refuse plain `{{ }}` text.
- 3: tool forms run on the agent's current input item.
- 4: `TestADescriptorCannotNameItsExecutor` gives a valid descriptor an `executorId` and asserts the PostgreSQL base ran.
- 5: per-segment query replacements are faithful from PostgreSQL 2.5; earlier PostgreSQL and MySQL bind per segment with a lossy note; a fixed value beside expressions exports as one whole segment (braces in strings as `\u` escapes, structural braces spaced).
- 6: a Gmail send exports `options.appendAttribution: false`; an imported `false` is not reported.
- 7: a computed SQL condition column that resolves empty is refused.
- 8: BUG-vqghpm (Stop and Error re-evaluated its resolved message) filed and fixed; the other evaluation sites are listed on that ticket.

## Re-review fixes (2026-10-09)

- Merged main again (credentials hardening, design tokens); no conflicts.
- BUG-vqghpm's first fix still evaluated an author-written error object's expression fields after resolving them; completed (see that ticket).
- The computed-condition refusal now runs inside `postgresConditions`, so only select and delete apply it and a stale `where` no longer stops insert, update, upsert or a query; a computed row and a computed `where` that do not resolve to conditions with columns are refused too (`TestComputedConditionsAreRefusedWhereTheyAreReadAndOnlyThere`).
- The Gmail node skips its item fallbacks (recipient, subject, body, message id) when it runs as a tool form, so a Gmail tool never mails an address taken from the agent's input (`TestTheGmailToolDoesNotFillMailFieldsFromTheAgentsItem`); a json `$fromAI` value's braces stay text in a getAll filter (`TestTheGmailToolKeepsAJSONArgumentsBracesAsText`).
- The tool-form comment and the migration guide's `$input` sentence say what is true.

## Second re-review fixes (2026-10-09)

- A PostgreSQL tool form refuses automatic column mapping for insert, update and upsert: the tool-form validator and the agent's re-check refuse it, the SQL executor refuses it on any tool call (the MySQL operation set shares that executor), and the importer reports an n8n `postgresTool` using `autoMapInputData` as blocking (`TestThePostgresToolRefusesAutomaticMapping`, `TestAPostgresToolThatMapsColumnsAutomaticallyIsBlocked`). The migration guide says so.
- Conditions: an authored row with an expression anywhere inside it counts as computed and must resolve to a condition naming a column (by a string); a `where` in a shape the node does not read is refused instead of running unfiltered; truncate and drop no longer read a leftover `where`.
- Found while checking which producers write a non-list `where`: the importer never does (it translates n8n's `{values: [...]}`), but the editor's conditions control writes every conditions property in its filter shape, `{combinator, conditions: [{leftValue, operator: {operation}, rightValue}]}`, and the executor read only lists, so a PostgreSQL or MySQL select whose conditions were edited in the editor ran unfiltered (and a row delete was refused for having none). That shape is now translated, combinator included (`TestConditionShapesAreReadOrRefusedNeverIgnored`). Pre-existing on main; not filed separately because it is fixed here.
- Stop and Error (BUG-vqghpm) follow-ups are recorded on that ticket.

# Related Files
- nodes/tool_form.go — toolForms table, derivation, agent runtime
- nodes/ai.go — the toolFrom case and the AgentExecutor field
- nodes/gmail.go — header-line refusal and emailType
- internal/engine/runner.go — Request.FromAIArguments
- internal/engine/authenticate.go — ExpressionContext carries it
- internal/node/registry.go — Definition.ToolFormOf
- internal/node/visibility.go — a tool form shares its base's scope
- internal/interop/n8n/tool_forms.go — generic <base>Tool translators and the Gmail translator
- internal/interop/n8n/n8n.go — toolVariants, export fallback, SupportedMappings
- internal/interop/n8n/parameters.go — query replacements bound per segment

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `a261f8b2` (last commit at or before ticket created 2026-10-08)
- Commits (27):
  - `d2d30efe` — merge: Gmail, PostgreSQL and Telegram run as agent tools through one generic tool-form table, model values reach executors only as data, and the Google nodes and Stop and Error never evaluate a resolved value again (FEAT-cf5n4j, BUG-b8bwhw, BUG-prhxdy, BUG-vqghpm)
  - `e96687d0` — chore(pine): FEAT-cf5n4j records the second re-review fixes and the editor-conditions finding
  - `a3e5338c` — FEAT-cf5n4j: a PostgreSQL tool never maps the agent's input as a row, and the editor's condition rows are read rather than ignored
  - `09809e63` — chore(pine): FEAT-cf5n4j records the re-review fixes
  - `ed6fe3e0` — FEAT-cf5n4j: the Gmail tool reads only what its author set, not mail fields from the agent's input item
  - `57a21530` — FEAT-cf5n4j: computed SQL conditions are checked where conditions are read, and a computed row or where counts too
  - `90dad357` — chore(pine): FEAT-cf5n4j's criteria and decisions say what is true after the review fixes, and the migration guide says how a tool form reads $fromAI and $json
  - `930c22fc` — FEAT-cf5n4j: a SQL condition whose column an expression computes is refused when it resolves empty, not dropped
  - `d4a7c37c` — FEAT-cf5n4j: a Gmail send exports with appendAttribution false, so n8n adds no attribution line this server never sent
  - `67de95c5` — FEAT-cf5n4j: per-segment query replacements are n8n's reading from PostgreSQL 2.5, and a fixed value exports as one whole segment
  - `e711c816` — FEAT-cf5n4j: a tool form runs on the item the agent is answering, as an n8n sub-node reads its root's input
  - `da8fb809` — FEAT-cf5n4j: a tool form refuses $fromAI in plain text in every spelling, and template text in what it calls
  - `56c5d241` — FEAT-cf5n4j: merge main; the shipped catalogue registers the packs' tool forms as run() does
  - `3e17609d` — chore(pine): FEAT-cf5n4j names the nested-braces refusal test as evidence
  - `a719a43a` — FEAT-cf5n4j: a tool refuses a json $fromAI value that holds {{ at any depth
  - `28cfed8a` — chore(pine): FEAT-cf5n4j and BUG-b8bwhw record the tool forms' evidence, and memory notes n8n's tool-form facts
  - `a3827cf2` — FEAT-cf5n4j: the Gmail tool gets the message the model names, escaped into one path segment
  - `76fb2abf` — FEAT-cf5n4j: an imported telegramTool runs through an agent and sends the model's text as text
  - `60a68aca` — FEAT-cf5n4j: the node-coverage suite and the guides name the three tool forms
  - `3e7e32bd` — FEAT-cf5n4j: n8n's gmailTool, postgresTool and telegramTool import as the tool forms and export back
  - `2dda535b` — FEAT-cf5n4j: Gmail, PostgreSQL and Telegram have agent-tool forms that run their own executors
  - `60653aa7` — FEAT-cf5n4j: Gmail send refuses a line break in a header value and can send HTML
  - `bf68b8c8` — FEAT-cf5n4j: a tool form shares its base node's tenant scope, and a grant naming the tool form is refused
  - `8df4943b` — FEAT-cf5n4j: every executor's expression context reads a tool call's $fromAI arguments from the request
  - `215e0728` — chore(pine): FEAT-cf5n4j gets its description and acceptance criteria
  - `ce0645fb` — chore(pine): the 2026-10-09 parallel wave moves its tickets to doing
  - `a261f8b2` — chore(pine): BUG-b8bwhw's remaining tool variants wait on their tool nodes, ticketed as a feature
- Files changed (base → working tree):

```
 .env.example                                       |    8 +-
 .github/workflows/ci.yml                           |   57 +-
 .pine/memory/docs.md                               |    5 +-
 .pine/memory/e2e.md                                |    3 +
 .pine/memory/n8n-reference.md                      |    4 +-
 .pine/memory/persistence.md                        |    5 +-
 .pine/memory/web-editor.md                         |    2 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-6gkd12.md                        | 1053 +++++++++++++++-
 .pine/tickets/BUG-9pmv8y.md                        | 1074 +++++++++++++++-
 .pine/tickets/BUG-b4cb1c.md                        | 1117 ++++++++++++++++-
 .pine/tickets/BUG-b8bwhw.md                        |   15 +-
 .pine/tickets/BUG-f85r5h.md                        |  412 +++++++
 .pine/tickets/BUG-f8b8gh.md                        |   47 +
 .pine/tickets/BUG-jx94q9.md                        |  417 +++++++
 .pine/tickets/BUG-k4tzc8.md                        |   46 +
 .pine/tickets/BUG-kkkrmx.md                        |  158 +++
 .pine/tickets/BUG-n6p7qy.md                        | 1177 +++++++++++++++++-
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pdmkn7.md                        |  419 +++++++
 .pine/tickets/BUG-prhxdy.md                        |   60 +
 .pine/tickets/BUG-vbw239.md                        |  197 +++
 .pine/tickets/BUG-vqghpm.md                        |   64 +
 .pine/tickets/BUG-x28fsx.md                        | 1219 +++++++++++++++++-
 .pine/tickets/BUG-ywbvfa.md                        | 1294 ++++++++++++++++++-
 .pine/tickets/EPIC-brpz48.md                       |    7 +-
 .pine/tickets/EPIC-hkypt5.md                       |    8 +-
 .pine/tickets/FEAT-8752vx.md                       |   10 +-
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-cf5n4j.md                       |  102 +-
 .pine/tickets/FEAT-e898nz.md                       |  195 ++-
 .pine/tickets/FEAT-egm8bf.md                       |    2 +
 .pine/tickets/FEAT-gcq50s.md                       |   17 +-
 .pine/tickets/FEAT-kfmq1z.md                       |    2 +
 .pine/tickets/FEAT-p75n7j.md                       | 1299 +++++++++++++++++++-
 .pine/tickets/FEAT-qae4sh.md                       |  956 +++++++++++++-
 .pine/tickets/FEAT-r87gtj.md                       |  956 +++++++++++++-
 .pine/tickets/FEAT-r8ph93.md                       |    3 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-vntngh.md                       |    4 +-
 .pine/tickets/FEAT-xj5tv6.md                       |    5 +
 .pine/tickets/FEAT-zm3wh2.md                       | 1126 ++++++++++++++++-
 CHANGELOG.md                                       |  121 ++
 CONTRIBUTING.md                                    |   13 +
 Makefile                                           |   41 +
 cmd/kilasflow/catalogue_docs_test.go               |  132 +-
 cmd/kilasflow/main.go                              |  226 +++-
 cmd/kilasflow/reseal_test.go                       |  219 ++++
 cmd/kilasflow/secrets_boot_test.go                 |  102 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  228 ++++
 config.example.yaml                                |   24 +-
 docs/src/content/docs/concepts/credentials.md      |   86 +-
 docs/src/content/docs/concepts/node-registry.md    |   20 +-
 docs/src/content/docs/guides/embedding.md          |   19 +-
 docs/src/content/docs/guides/n8n-migration.md      |   97 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |    6 +
 .../docs/operate/configuration-reference.md        |   36 +-
 docs/src/content/docs/operate/configuration.md     |   14 +-
 docs/src/content/docs/operate/key-rotation.md      |  158 +++
 docs/src/content/docs/operate/security.md          |   19 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +
 docs/src/content/docs/reference/api/credentials.md |    3 +-
 docs/src/content/docs/reference/api/workflows.md   |    1 +
 docs/src/content/docs/reference/cli.md             |  225 ++--
 docs/src/content/docs/start/install.md             |    5 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   15 +-
 docs/src/styles/kilasflow.css                      |    2 +-
 e2e/.gitignore                                     |    4 +
 e2e/fixtures/error-form-nodes.ts                   |    7 +-
 e2e/global-setup.ts                                |   26 +-
 e2e/helpers/focus-ring.ts                          |  125 ++
 e2e/helpers/stub.ts                                |    6 +-
 e2e/package.json                                   |    3 +-
 e2e/playwright.visual.config.ts                    |   60 +
 e2e/tests/embed-branding.spec.ts                   |  163 +++
 e2e/tests/focus-tab-order.spec.ts                  |  155 +++
 e2e/tests/n8n-compare.spec.ts                      |    3 +-
 e2e/tests/node-coverage.spec.ts                    |   29 +-
 e2e/tests/workflow-tool-inputs.spec.ts             |  406 ++++++
 e2e/visual/fixtures.ts                             |   71 ++
 e2e/visual/focus-ring.spec.ts                      |   50 +
 e2e/visual/force-state.ts                          |   34 +
 e2e/visual/state-matrix.spec.ts                    |   25 +
 e2e/visual/theme-subtree.spec.ts                   |   47 +
 internal/ai/agent.go                               |    4 +-
 internal/ai/ai_test.go                             |   32 +
 internal/ai/fromai.go                              |   28 +
 internal/ai/fromai_whole_test.go                   |   47 +
 internal/ai/memory.go                              |   26 +-
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   25 +-
 internal/api/credential_audit_test.go              |  271 ++++
 internal/api/credential_usage_test.go              |  169 +++
 internal/api/handlers/actor.go                     |   33 +
 internal/api/handlers/credentials.go               |  182 ++-
 internal/api/handlers/nodes.go                     |   32 +-
 internal/api/handlers/oauth.go                     |   29 +-
 internal/api/handlers/workflows.go                 |   55 +-
 internal/api/node_types_schema_expression_test.go  |   81 ++
 internal/api/oauth_external_test.go                |   69 ++
 internal/api/subworkflow_activation_test.go        |  219 ++++
 internal/api/workflow_tags_test.go                 |  342 ++++++
 internal/cli/docs_examples_test.go                 |   70 ++
 internal/cli/exit.go                               |  136 ++
 internal/cli/exit_reference_test.go                |  286 +++++
 internal/cli/skills_not_shipped_test.go            |  307 ++++-
 internal/cli/verbs_credential.go                   |   35 +-
 internal/cli/verbs_credential_test.go              |  197 +--
 internal/cli/verbs_node.go                         |   91 +-
 internal/cli/verbs_node_options_test.go            |  333 +++++
 internal/cli/verbs_workflow.go                     |   38 +-
 internal/cli/verbs_workflow_subworkflow_test.go    |  144 +++
 internal/cli/verbs_workflow_tag_test.go            |   84 ++
 internal/config/boot_strictness_test.go            |   76 ++
 internal/config/config.go                          |   51 +-
 internal/credentials/binding_guard_test.go         |   79 ++
 internal/credentials/cipher.go                     |  330 +++++
 internal/credentials/cipher_internal_test.go       |   35 +
 internal/credentials/cipher_test.go                |  388 ++++++
 internal/credentials/credentials.go                |   78 +-
 internal/credentials/credentials_test.go           |   25 +-
 internal/credentials/external.go                   |   41 +-
 internal/credentials/keysource.go                  |   63 +-
 internal/credentials/keysource_test.go             |  103 ++
 internal/credentials/oauth.go                      |    7 +
 .../database/credential_events_migration_test.go   |   61 +
 internal/database/migrate_test.go                  |    2 +
 internal/docexamples/docexamples.go                |  318 +++++
 internal/docexamples/docexamples_test.go           |  209 ++++
 internal/engine/authenticate.go                    |    3 +
 internal/engine/expression_context_test.go         |   24 +
 internal/engine/missing_credential_test.go         |   49 +
 internal/engine/runner.go                          |    8 +
 internal/engine/service.go                         |   19 +
 internal/interop/n8n/chatmodel_import_test.go      |    3 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |    6 +-
 internal/interop/n8n/importer_tail_test.go         |   15 +-
 internal/interop/n8n/n8n.go                        |  119 +-
 internal/interop/n8n/n8n_test.go                   |  119 +-
 internal/interop/n8n/parameters.go                 |  184 ++-
 internal/interop/n8n/rag_import_test.go            |   33 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |   26 +
 internal/interop/n8n/tags.go                       |  182 +++
 internal/interop/n8n/tags_test.go                  |  312 +++++
 internal/interop/n8n/template6270_shape_test.go    |  305 +++++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   10 +-
 internal/interop/n8n/tool_form_import_test.go      |  559 +++++++++
 internal/interop/n8n/tool_forms.go                 |  398 ++++++
 internal/interop/n8n/tool_variant_import_test.go   |   16 +-
 internal/interop/n8n/vectorstore.go                |  153 +++
 internal/interop/n8n/vectorstore_import_test.go    |  281 +++++
 internal/interop/n8n/workflow_tool_inputs.go       |  241 ++++
 internal/interop/n8n/workflow_tool_inputs_test.go  |  319 +++++
 internal/loadoptions/workflows.go                  |   79 ++
 internal/loadoptions/workflows_test.go             |  158 +++
 internal/node/registry.go                          |    9 +
 internal/node/tool_form_scope_test.go              |   99 ++
 internal/node/visibility.go                        |   75 +-
 internal/property/mapper.go                        |    5 +
 internal/property/mapper_test.go                   |   22 +
 internal/repository/credential_events.go           |  130 ++
 internal/repository/credential_events_test.go      |  122 ++
 internal/repository/credential_reseal_test.go      |  218 ++++
 internal/repository/credential_seal_test.go        |  133 ++
 internal/repository/credential_usage.go            |  204 +++
 internal/repository/credential_usage_test.go       |  194 +++
 internal/repository/credentials.go                 |   30 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/export_reseal_test.go          |    7 +
 internal/repository/models.go                      |   27 +
 internal/repository/polls_test.go                  |    8 +
 internal/repository/postgres_execution_test.go     |    2 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/refresh.go                     |    6 +-
 internal/repository/refresh_external_test.go       |   86 ++
 internal/repository/reseal.go                      |  153 +++
 internal/repository/subworkflow_activation_test.go |  116 ++
 internal/repository/table_names_test.go            |   12 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/webhook_state.go               |    7 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 ++
 internal/repository/workflow_tags_test.go          |  283 +++++
 internal/repository/workflows.go                   |  132 +-
 internal/shippedcatalogue/shippedcatalogue.go      |  167 +++
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 ++
 internal/skills/check.go                           |  146 +++
 internal/skills/check_test.go                      |   59 +
 internal/skills/examples.go                        |  287 +++++
 internal/skills/examples_coverage_test.go          |   49 +
 internal/skills/examples_shape_test.go             |  121 ++
 internal/skills/examples_test.go                   |  642 ++++++++++
 internal/skills/skill.go                           |   11 +
 internal/skills/skills_test.go                     |  254 +++-
 .../skills/kilasflow-fixture/SKILL.md              |   32 +
 internal/tenantpurge/harness_test.go               |   34 +-
 internal/tenantpurge/purge.go                      |    3 +-
 internal/tenantpurge/purge_test.go                 |    4 +-
 internal/workflow/document.go                      |    9 +
 internal/workflow/tags.go                          |  113 ++
 internal/workflow/tags_test.go                     |  188 +++
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 .../postgres/000026_credential_events.down.sql     |    4 +
 .../postgres/000026_credential_events.up.sql       |   36 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 .../sqlite/000026_credential_events.down.sql       |    4 +
 migrations/sqlite/000026_credential_events.up.sql  |   35 +
 nodes/ai.go                                        |  170 ++-
 nodes/core.go                                      |    5 +-
 nodes/error_workflow.go                            |   81 +-
 nodes/executors.go                                 |   15 +-
 nodes/glyphs_test.go                               |    5 +
 nodes/gmail.go                                     |   40 +-
 nodes/google.go                                    |   54 +-
 nodes/google_template_test.go                      |  191 +++
 nodes/openai_credential_url_test.go                |   18 +-
 nodes/pgvector.go                                  |   36 +-
 nodes/pgvector_customer.go                         |   26 +-
 nodes/pgvector_test.go                             |    9 +
 nodes/postgres_v2.go                               |  160 ++-
 nodes/rag.go                                       |   13 +
 nodes/stop_and_error_leak_test.go                  |  115 ++
 nodes/subworkflow.go                               |   55 +
 nodes/subworkflow_inputs_test.go                   |   54 +
 nodes/tool_form.go                                 |  486 ++++++++
 nodes/tool_form_postgres_live_test.go              |   96 ++
 nodes/tool_form_run_test.go                        |  977 +++++++++++++++
 nodes/tool_form_test.go                            |  236 ++++
 nodes/vectorstore_retrieve.go                      |   84 ++
 nodes/vectorstore_retrieve_test.go                 |  250 ++++
 nodes/vectorstore_tool.go                          |  325 +++++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++++
 nodes/vectorstore_tool_test.go                     |  565 +++++++++
 nodes/workflow_tool_inputs_test.go                 |  263 ++++
 schemas/workflow-v1.schema.json                    |    8 +-
 scripts/cli-examples/main.go                       |  583 +++++++++
 scripts/cli-examples/main_test.go                  |  215 ++++
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 ++
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  330 +++++
 scripts/cli-examples/server.go                     |  183 +++
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/docs-facts/main.go                         |  177 +++
 scripts/docs-facts/main_test.go                    |  278 +++++
 scripts/internal/splice/splice.go                  |  149 +++
 scripts/internal/splice/splice_test.go             |  112 ++
 scripts/skills-command-reference/main.go           |  130 +-
 sdk/CHANGELOG.md                                   |   27 +
 sdk/README.md                                      |   20 +-
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |   59 +-
 sdk/src/server.ts                                  |   17 +
 sdk/test/operation-coverage.test.mjs               |   21 +-
 sdk/test/operations.test.ts                        |   19 +
 skills/index.json                                  |  158 ++-
 skills/kilasflow-ai-agents/SKILL.md                |  114 ++
 skills/kilasflow-ai-agents/references/EXAMPLE.md   |  146 +++
 skills/kilasflow-ai-agents/references/MODELS.md    |  105 ++
 skills/kilasflow-ai-agents/references/RAG.md       |  188 +++
 skills/kilasflow-credentials/SKILL.md              |   47 +-
 .../references/CREDENTIAL_TYPES.md                 |   28 +-
 skills/kilasflow-credentials/references/EXAMPLE.md |   97 ++
 skills/kilasflow-datastore/SKILL.md                |   52 +-
 skills/kilasflow-datastore/references/EXAMPLE.md   |  130 ++
 skills/kilasflow-datastore/references/FILTERS.md   |   40 +-
 skills/kilasflow-debugging/SKILL.md                |   34 +-
 skills/kilasflow-debugging/references/EXAMPLE.md   |   70 ++
 .../references/TRACE_READING.md                    |   43 +-
 skills/kilasflow-embedding/SKILL.md                |   57 +-
 .../references/EMBED_HANDSHAKE.md                  |   15 +-
 skills/kilasflow-embedding/references/EXAMPLE.md   |   82 ++
 .../references/SESSION_AUTHORITY.md                |    4 +-
 skills/kilasflow-error-handling/SKILL.md           |   39 +-
 .../kilasflow-error-handling/references/EXAMPLE.md |  117 ++
 skills/kilasflow-expressions/SKILL.md              |   40 +-
 skills/kilasflow-expressions/references/EXAMPLE.md |  134 ++
 .../references/EXPRESSION_ROOTS.md                 |   61 +-
 skills/kilasflow-flow-patterns/SKILL.md            |  105 ++
 .../references/BINARY_DATA.md                      |  116 ++
 .../kilasflow-flow-patterns/references/EXAMPLE.md  |  134 ++
 .../references/SUB_WORKFLOWS.md                    |  173 +++
 skills/kilasflow-import-export/SKILL.md            |   34 +-
 .../kilasflow-import-export/references/EXAMPLE.md  |  108 ++
 .../references/MAPPING_LIMITS.md                   |   55 +-
 skills/kilasflow-node-configuration/SKILL.md       |   44 +-
 .../references/EXAMPLE.md                          |  141 +++
 .../references/LOAD_OPTIONS.md                     |   65 +-
 .../references/PROPERTY_KINDS.md                   |   67 +-
 skills/kilasflow-node-packs/SKILL.md               |   38 +-
 skills/kilasflow-node-packs/references/EXAMPLE.md  |   75 ++
 .../references/MODULE_AND_SIDECAR.md               |   32 +-
 .../kilasflow-node-packs/references/PACK_FORMAT.md |   28 +-
 skills/kilasflow-operations/SKILL.md               |   17 +-
 skills/kilasflow-operations/references/EXAMPLE.md  |   86 ++
 .../references/UPGRADE_ORDER.md                    |   15 +-
 skills/kilasflow-triggers/SKILL.md                 |   48 +-
 skills/kilasflow-triggers/references/EXAMPLE.md    |   80 ++
 .../references/WEBHOOK_DELIVERY.md                 |   33 +-
 skills/kilasflow-workflow-lifecycle/SKILL.md       |   36 +-
 .../references/EXAMPLE.md                          |  111 ++
 .../references/NAMING_AND_DESCRIPTIONS.md          |   25 +-
 .../references/VALIDATION_CHECKLIST.md             |   29 +-
 skills/using-kilasflow-skills/SKILL.md             |   43 +-
 .../using-kilasflow-skills/references/EXAMPLE.md   |   65 +
 web/messages/en/credentials.json                   |    5 +
 web/messages/en/properties.json                    |    8 +
 web/messages/en/workflows.json                     |   15 +-
 web/messages/id/credentials.json                   |    5 +
 web/messages/id/properties.json                    |    8 +
 web/messages/id/workflows.json                     |   15 +-
 web/package.json                                   |    1 +
 web/src/app.css                                    |  688 ++++++++---
 web/src/app.d.ts                                   |    3 +
 .../lib/api/generated/credentials/credentials.ts   |   26 +-
 .../lib/api/generated/models/credentialResource.ts |    6 +
 .../generated/models/credentialUsageResource.ts    |   19 +
 .../api/generated/models/deleteCredentialParams.ts |   14 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 web/src/lib/api/generated/models/index.ts          |    2 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 .../generated/models/resourceMapperDeclaration.ts  |    1 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 .../lib/components/dashboard/dashboard-nav.svelte  |   10 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 web/src/lib/components/ui/button/button.svelte     |   14 +-
 web/src/lib/components/ui/checkbox/checkbox.svelte |    2 +-
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../dropdown-menu-checkbox-item.svelte             |    2 +-
 .../ui/dropdown-menu/dropdown-menu-content.svelte  |    2 +-
 .../ui/dropdown-menu/dropdown-menu-item.svelte     |    2 +-
 .../dropdown-menu/dropdown-menu-radio-item.svelte  |    2 +-
 .../dropdown-menu/dropdown-menu-sub-content.svelte |    2 +-
 .../dropdown-menu/dropdown-menu-sub-trigger.svelte |    2 +-
 web/src/lib/components/ui/input/input.svelte       |    4 +-
 web/src/lib/components/ui/list-row/index.ts        |    8 +
 web/src/lib/components/ui/list-row/list-row.svelte |   29 +
 .../components/ui/popover/popover-content.svelte   |    2 +-
 .../components/ui/scroll-area/scroll-area.svelte   |    2 +-
 .../lib/components/ui/select/select-content.svelte |    2 +-
 .../lib/components/ui/select/select-item.svelte    |    2 +-
 .../lib/components/ui/select/select-trigger.svelte |    2 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    2 +-
 web/src/lib/components/ui/sonner/sonner.svelte     |    2 +-
 web/src/lib/components/ui/switch/switch.svelte     |    4 +-
 web/src/lib/components/ui/tabs/tabs-content.svelte |    2 +-
 web/src/lib/components/ui/tabs/tabs-trigger.svelte |    2 +-
 web/src/lib/components/ui/textarea/textarea.svelte |    2 +-
 .../workflow-editor/canvas-chat-panel.svelte       |    4 +-
 .../components/workflow-editor/canvas-node.svelte  |   12 +-
 .../components/workflow-editor/code-editor.svelte  |    2 +-
 .../components/workflow-editor/node-picker.svelte  |    6 +-
 .../workflow-editor/properties-panel.svelte        |    2 +-
 .../workflow-editor/property-field.svelte          |  230 +++-
 .../workflow-editor/property-field.test.ts         |  164 +++
 .../workflow-editor/version-panel.svelte           |    2 +-
 .../workflow-editor/workflow-editor.svelte         |   16 +-
 web/src/lib/dashboard/credential-usage.test.ts     |   46 +
 web/src/lib/dashboard/credential-usage.ts          |   38 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 ++-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/design-system/app-css.ts               |   15 +
 web/src/lib/design-system/class-scan.ts            |  245 ++++
 web/src/lib/design-system/color.test.ts            |  120 ++
 web/src/lib/design-system/color.ts                 |  513 ++++++++
 web/src/lib/design-system/contrast-pairs.ts        |   84 ++
 web/src/lib/design-system/css-tokens.test.ts       |  110 ++
 web/src/lib/design-system/css-tokens.ts            |  236 ++++
 web/src/lib/design-system/focus-lint.test.ts       |  143 +++
 web/src/lib/design-system/focus-lint.ts            |  236 ++++
 web/src/lib/design-system/state-matrix.svelte      |  335 +++++
 web/src/lib/design-system/theme-color.test.ts      |   23 +
 web/src/lib/design-system/theme-color.ts           |   34 +
 web/src/lib/design-system/tokens.contrast.test.ts  |  116 ++
 web/src/lib/design-system/tokens.groups.test.ts    |  129 ++
 web/src/lib/design-system/tokens.hue.test.ts       |  177 +++
 web/src/lib/embed/brand-root.test.ts               |   44 +
 web/src/lib/embed/brand-root.ts                    |   24 +
 web/src/lib/workflow-editor/document.test.ts       |   18 +-
 web/src/lib/workflow-editor/document.ts            |   14 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |   13 +-
 web/src/lib/workflow-editor/node-visual.ts         |    8 +-
 .../lib/workflow-editor/resource-mapper.test.ts    |  262 +++-
 web/src/lib/workflow-editor/resource-mapper.ts     |  172 ++-
 web/src/lib/workflow-editor/shortcuts.test.ts      |   37 +
 web/src/lib/workflow-editor/shortcuts.ts           |   23 +
 web/src/lib/workflow-editor/validation.test.ts     |   24 +
 web/src/lib/workflow-editor/validation.ts          |   14 +-
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   11 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   48 +-
 web/src/routes/+page.svelte                        |   34 +-
 web/src/routes/design/states/+page.svelte          |    5 +
 web/src/routes/design/states/+page.ts              |   16 +
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/vite.config.ts                                 |   10 +
 408 files changed, 44332 insertions(+), 1963 deletions(-)
```
