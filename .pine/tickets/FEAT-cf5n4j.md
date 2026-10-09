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
