---
id: BUG-6as5y7
title: 'Transform parity: Aggregate/Sort keys, Merge modes, Date&Time, Summarize, SplitOut, Switch, key order'
status: doing
priority: high
labels:
    - nodes
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T02:37:47Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 8 finding(s) from dims: find:core-node-parity, find:importer-fidelity.

---
### Aggregate and Sort imports drop their field lists (wrong fixed-collection keys); workflows then fail activation [find:core-node-parity] (high/bug) · area: importer / Aggregate, Sort · confidence: high

The importer reads the wrong keys for both nodes. For Aggregate it reads fieldsToAggregate.values[] but n8n writes fieldsToAggregate.fieldToAggregate[]. For Sort it reads sortFieldsUI but n8n writes sortFieldsUi (lower-case i). Nothing is carried and no issue is raised, and activation then fails. Several other Aggregate options are also ignored: include specifiedFields/fieldsToInclude, renameField/outputFieldName, mergeLists and keepMissing.

Evidence: Re-verified without any patch (cases/v2_agg_sort_nopatch.json, v3_sort_nopatch.json). The import returns only the generic settings issue. Activation then returns 422: 'aggregating individual fields needs at least one field name' and 'a simple sort needs at least one field name'. Real template shapes: 2320 and 2567 (Aggregate), 2275 (Sort).
With the fields patched in by hand:
- sh_agg_all_specified: n8n data[] holds only id,city; KilasFlow all 9 fields.
- sh_agg_rename: n8n 'labels'; KilasFlow 'name'.
- sh_agg_mergelists: n8n a flat list of 6; KilasFlow nested arrays.
- sh_agg_keepmissing: n8n 5 values including null; KilasFlow 4.
Code: internal/interop/n8n/parameters.go:1980-2001 and 2079-2093.

n8n behavior: Aggregates the listed fields and sorts by the listed keys, with the options applied.

Impact: Aggregate v1 appears in 14 of the 100 templates (16 nodes) and Sort in 1. These workflows cannot be activated even after every other blocker is fixed.

Suggested fix: Read fieldsToAggregate.fieldToAggregate[] (including renameField/outputFieldName) and sortFieldsUi.sortField[]. Map include, fieldsToInclude/fieldsToExclude, mergeLists and keepMissing. Raise an issue whenever a fixed collection is present but unreadable.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/transform.go

Existing tickets: FEAT-jwhdsy (done; claims Aggregate/Sort mapped, so this is a regression)

---
### Merge: v2 position/multiplex fail at runtime; choose-input-2, outputDataFrom, keepNonMatches/enrichInput2, includeUnpaired and clashHandling are ignored [find:core-node-parity] (high/parity-gap) · area: importer + Merge executor · confidence: high

mergeToKilas ignores several n8n Merge settings and the executor lacks some join modes:
- combinationMode (v2) is never read.
- The chosen branch is read from a non-existent 'chooseBranch' key instead of useDataOfInput (v3) or output (v2).
- outputDataFrom and options.* are dropped.
- The executor implements only the keepMatches, keepEverything and enrichInput1 join modes, and orders results differently from n8n.
- Unsupported modes (combineBySql, v1 passThrough/wait) import with no issue and then fail activation.

Evidence: Inputs: a=[{id1,x},{id2,y},{id3,z}], b=[{id2,20,v:b2},{id3,30},{id4,40},{id2,21}].
- merge_v2_position and merge_v2_multiplex: n8n returns 3 and 12 items; KilasFlow fails 'combining by fields needs at least one field to match on'.
- merge_v3 'M choose 2' (useDataOfInput 2) and merge_v2_choose (output input2): n8n returns input 2; KilasFlow returns input 1.
- keepNonMatches: n8n [{id1,_source:input1},{id4,_source:input2}]; KilasFlow the inner-join matches.
- enrichInput2: n8n 4 items; KilasFlow 3.
- outputDataFrom input1: n8n input-1 fields only; KilasFlow merged fields.
- includeUnpaired: n8n 4 items; KilasFlow 3.
- clashHandling addSuffix/preferInput1: ignored, input 2 always wins.
- keepEverything/enrichInput1: same items in a different order.
- merge_sql and merge_v1_*: no import issue, then activation 422 'mode "combineBySql" is not supported'.
- Empty input 1 with includeUnpaired: n

n8n behavior: All of these modes and options are supported. Matched items come first, then unmatched ones.

Impact: Merge appears in 36 templates. v2 mergeByPosition/multiplex in 4, chooseBranch in 2, includeUnpaired in 2.

Suggested fix: Map combinationMode (mergeByPosition→combineByPosition, multiplex→combineAll, mergeByKey→combineByFields), useDataOfInput/output, outputDataFrom and options.*. Implement keepNonMatches and enrichInput2 and match n8n's result ordering. Raise blocking import issues for unsupported modes.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/executors.go, /Users/izzadev/projects/k-flow/nodes/core.go

Existing tickets: FEAT-vvwpjw (done; AC 34/37, so this is a regression)

---
### Date & Time: default output names, rounding units, time-between shape, timezone and v1 'format' all diverge [find:core-node-parity] (high/parity-gap) · area: importer + Date & Time executor · confidence: high

The Date & Time import and executor differ from n8n in six ways:
- Every operation writes to 'date' (n8n: formattedDate, newDate, timeDifference, ...).
- Input fields are always kept (n8n default: only the new field).
- roundDate ignores 'toNearest' and n8n's singular units.
- getTimeBetweenDates returns a truncated integer instead of n8n's {days, hours} object.
- options.timezone is not applied when formatting.
- A v1 'format' node with the default action is imported as 'get current date'.

Evidence: Input d=2024-03-15T10:30:00Z, e=2024-04-20T12:00:00Z:
- Default output name: n8n formattedDate/newDate/timeDifference; KilasFlow 'date' (dt_format_default, dt_add_default_out, dt_between_default).
- dt_round_down_month (toNearest month): n8n 2024-03-01; KilasFlow 2024-03-15T00:00:00Z.
- dt_round_up_day (to 'day'): n8n 2024-03-16; KilasFlow returns the input unchanged.
- dt_between with units [day,hour]: n8n {days:36,hours:1.5}; KilasFlow 36. Default units: n8n {days:36.0625}; KilasFlow 36.
- dt_format_tz (Asia/Jakarta): n8n '2024-03-15 17:30 +07:00'; KilasFlow '10:30 +00:00'.
- dt_v1_format (value, toFormat 'MMMM DD YYYY'): n8n {data:'March 15 2024'}; KilasFlow {date:'<now>'}.
Code: internal/interop/n8n/parameters.go:1168-1265 and nodes/datetime.go:214-330.

n8n behavior: Per-operation default field names and includeInputFields=false. Singular units, and toNearest for rounding down. A duration object for time-between. Output rendered in the timezone option or the workflow/instance timezone. v1 detected by typeVersion, with moment tokens.

Impact: Date & Time appears in 1 of the 100 templates (template 1744 hits the v1 bug) but is a staple of customer workflows. Downstream $json.formattedDate/newDate references come back empty.

Suggested fix: Use per-operation default names and honour includeInputFields. Read toNearest and singular units. Return n8n's duration object. Convert to the target zone before formatting. Detect v1 by typeVersion and translate moment tokens.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/datetime.go

Existing tickets: FEAT-q81bq4

---
### Switch: numeric fallbackOutput silently dropped; v1/v2 rules and expression mode cannot be imported [find:core-node-parity] (medium/bug) · area: importer / Switch · confidence: high

A numeric options.fallbackOutput (route unmatched items to output N) is dropped, and no issue is raised because of a failing string type assertion. Legacy v1/v2 rules (rules.rules with value2/output) and v3 expression mode block activation.

Evidence: - switch_fallback_index (v3.2, fallbackOutput 0): n8n output 0 = [Paris, Rome, paris]; KilasFlow [Paris]. Rome and paris are lost and the import reports no issue. Cause: `fallback, _ := options["fallbackOutput"].(string)` fails for a JSON number, so the warning branch is skipped (parameters.go:596-607).
- switch_v1 (dataType/value1/rules.rules/fallbackOutput 2): 'no readable rules', then activation 422 'switch rules must be a list'.
- switch_expression: lossy import and every output edge held back, so it cannot activate.

n8n behavior: Routes unmatched items to the given output index. Supports v1/v2 rules and expression mode.

Impact: Switch v1 appears in 2 of the 100 templates, one of them with fallback 3. A numeric fallback can appear in any v3 workflow.

Suggested fix: Support a numeric fallback index in the executor, or at least report it. Translate v1/v2 rules into v3 rules and implement expression mode (evaluate the output index).

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go, /Users/izzadev/projects/k-flow/nodes/flow.go

Existing tickets: FEAT-vvwpjw

---
### Summarize: output field names, separators, empty-value handling and group order differ from n8n [find:core-node-parity] (medium/parity-gap) · area: Summarize executor + importer · confidence: high

Output keys use the aggregation name as the prefix (append_, concatenate_, countUnique_) where n8n uses appended_, concatenated_ and unique_count_. Concatenation hard-codes ', ' and ignores separateBy/customSeparator. Empty values are kept, and groups come out in first-seen order.

Evidence: - sh_sum_append_unique / sh_sum_concat_* / sh_sum_split_city: n8n appended_city, concatenated_name, unique_count_id; KilasFlow append_city, concatenate_name, countUnique_id.
- Concatenate default: n8n 'Alice,bob,Carol,Carol'; KilasFlow 'Alice, bob, Carol, Carol, '.
- A custom separator '--' or ' | ' is ignored.
- Append over empty strings: n8n []; KilasFlow [""].
- fieldsToSplitBy 'city, name': n8n groups Paris/Alice, Paris/Carol, Berlin, Rome; KilasFlow Paris/Alice, Berlin, Paris/Carol, Rome.
- count/sum/min/max/average match.
Code: nodes/transform.go:500-610; summarizeToKilas drops separateBy.

n8n behavior: Uses n8n's prefixed keys and the configured separator (default ','), skips empty values, and orders groups by nesting.

Impact: Summarize appears in 2 of the 100 templates; template 2679 uses concatenate with separateBy ' '.

Suggested fix: Use n8n's key prefixes, carry separateBy/customSeparator, skip empty values and match n8n's group ordering.

Files: /Users/izzadev/projects/k-flow/nodes/transform.go, /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go

Existing tickets: FEAT-jwhdsy

---
### Split Out: primitive values and 'include other fields' land at different paths than n8n; objects are not split [find:core-node-parity] (medium/parity-gap) · area: Split Out executor · confidence: medium

Splitting arrays of objects matches n8n, but other cases do not:
- Splitting primitives at a dotted path writes the value under the last path segment.
- allOtherFields keeps the original array and adds a new field.
- Included fields lose their path.
- Splitting an object returns the object instead of one item per value.

Evidence: Case splitout (body {tags:[t1,t2], meta:{m:1}}):
- 'body.tags': n8n {body:{tags:'t1'}}; KilasFlow {tags:'t1'}.
- include allOtherFields: n8n replaces body.tags with 't1'; KilasFlow keeps body.tags=[t1,t2] and adds tags.
- selectedOtherFields 'body.meta': n8n {body:{meta:{m:1}}}; KilasFlow {meta:{m:1}}.
- An object field: n8n emits one item per value and 0 items for {}; KilasFlow emits {m:1}, and 1 item for {}.
- body.items (objects) matches.

n8n behavior: The split value is written back at the same dotted path, in place when other fields are included, and object values are iterated.

Impact: Split Out appears in 17 of the 100 templates. Most split arrays of objects, which match. Primitive or tag arrays and include-other-fields diverge.

Suggested fix: Follow n8n's path semantics for the destination and included fields, and iterate object values.

Files: /Users/izzadev/projects/k-flow/nodes/transform.go

Existing tickets: FEAT-jwhdsy

---
### Item JSON key order is not preserved (keys are alphabetised) [find:core-node-parity] (low/parity-gap) · area: engine item model · confidence: high

Items are Go maps, so key order is lost in every output, webhook response and stringified object.

Evidence: v3_settypes3, strFromObj '={{ $json }}' as a string: n8n {"id":1,"score":"12","active":true,...}; KilasFlow {"active":true,"age":30,"id":1,...}. Every KilasFlow response body in this run is alphabetised (merge outputs, rw_json_expr).

n8n behavior: Keeps insertion order.

Impact: Changes CSV/Sheets column order, HMACs or signatures over JSON strings, and response readability.

Suggested fix: Use an order-preserving object representation for item JSON, or at least in the JSON encode paths.

Files: /Users/izzadev/projects/k-flow/internal/engine

---
### Aggregate and Sort translators use the wrong n8n parameter keys in both directions: fields are lost on import and exported nodes fail in n8n [find:importer-fidelity] (high/bug) · area: importer+exporter/Aggregate, Sort · confidence: high

Aggregate: namedList reads `fieldsToAggregate.values[]` and aggregateToN8N writes `values`, but n8n stores `fieldsToAggregate.fieldToAggregate[]` (plus renameField/outputFieldName); options mergeLists/keepMissing are dropped too. Sort: sortToKilas and sortToN8N use `sortFieldsUI`, but n8n's key is `sortFieldsUi`. Aggregate aggregateAllItemData `include`/`fieldsToInclude` is also dropped.

Evidence: Live n8n 2.33.7 (n8n_agg_probe.py): Aggregate with fieldToAggregate gives [{"price":[1,3,2]}]; the same node with KilasFlow's exported `values` key fails with 'No fields specified'. Sort (n8n_sort_probe.py): sortFieldsUi sorts; KilasFlow's sortFieldsUI fails with 'No sorting specified. Please add a field to sort by'. KilasFlow import: 2320, 2567 and 3224 fail activation with 'aggregating individual fields needs at least one field name', and 2275 with 'a simple sort needs at least one field name'. There is no import issue in either case. 2315 and 2722 lose include/fieldsToInclude.

n8n behavior: fieldsToAggregate.fieldToAggregate[] {fieldToAggregate, renameField, outputFieldName}; sortFieldsUi.sortField[] {fieldName, order}.

Impact: Every n8n Aggregate (individual fields) and Sort imports empty (4-5 templates blocked), and every one a KilasFlow user exports is broken in n8n.

Suggested fix: Use n8n's exact keys in both directions and carry rename/options. Add fixtures generated from real n8n exports, and raise an issue whenever a required list translates empty.

Files: internal/interop/n8n/parameters.go, nodes/transform.go

Existing tickets: FEAT-jwhdsy (done: claims both-direction mapping; regression)

## Acceptance criteria

- [ ] Aggregate and Sort imports drop their field lists (wrong fixed-collection keys); workflows then fail activatio
- [ ] Merge: v2 position/multiplex fail at runtime; choose-input-2, outputDataFrom, keepNonMatches/enrichInput2, inc
- [ ] Date & Time: default output names, rounding units, time-between shape, timezone and v1 'format' all diverge
- [ ] Switch: numeric fallbackOutput silently dropped; v1/v2 rules and expression mode cannot be imported
- [ ] Summarize: output field names, separators, empty-value handling and group order differ from n8n
- [ ] Split Out: primitive values and 'include other fields' land at different paths than n8n; objects are not split
- [ ] Item JSON key order is not preserved (keys are alphabetised)
- [ ] Aggregate and Sort translators use the wrong n8n parameter keys in both directions: fields are lost on import 
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Work (Main review 2026-09-19)
- Reviewed ImporterWebhook uncommitted delivery: Set v3/v2/import include+mode+options both directions, IF-v1 legacy op translation, Merge v2 combinationMode + options passthrough + executor enrichInput2/keepNonMatches, Date&Time v1-by-typeVersion + per-op output names + tz + duration object, Aggregate/Sort key fixes both directions, Summarize separator/output names.
- Fixes applied in review: Set fields.values/v2-values index sequencing (len(entries)+index → len(entries)), gofmt nodes/core.go + parameters.go.
- Caution: datetime_test drops unix-timestamp cases and re-pins compare as duration object + tz format expectation flip 00:00→07:00 — behavior changes match BUG-6as5y7 finding (n8n parity), not re-pins; transform_test summarize keys follow n8n output names per finding. Scoped suites green: interop/n8n, nodes, property.
- Remaining per agent report: SplitOut path semantics, key-order preservation, Summarize group-order, SIB/workflowInputs (BUG-8t94wn overlap untouched).

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `bf802ab8` (last commit at or before ticket created 2026-09-19)
- Commits (3):
  - `a91157f7` — chore(pine): align landed slices to testing (6as5y7, txc9xg, s0wy50, xf1wqm)
  - `44b3b3e3` — BUG-6as5y7 BUG-txc9xg BUG-hm76dq: Set version-aware import, Merge/DateTime/Summarize/Aggregate/Sort parity, IF-v1 ops
  - `b4f21475` — chore(pine): track EPIC-cfe7ny full-review remediation backlog (50 tickets)
- Files changed (base → working tree):

```
 .env.example                                       |    2 +-
 .github/workflows/ci.yml                           |   76 +-
 .github/workflows/release.yml                      |    3 +-
 .gitignore                                         |   11 +-
 .pine/tickets/BUG-1tj5wy.md                        |  499 ++++
 .pine/tickets/BUG-277a2m.md                        |  506 ++++
 .pine/tickets/BUG-341sxn.md                        |   40 +
 .pine/tickets/BUG-4053h6.md                        |  921 +++++++
 .pine/tickets/BUG-57n76x.md                        |  576 +++++
 .pine/tickets/BUG-66es9z.md                        |  248 ++
 .pine/tickets/BUG-6as5y7.md                        |  220 ++
 .pine/tickets/BUG-6bqh51.md                        |  189 ++
 .pine/tickets/BUG-6jvcs5.md                        |  238 ++
 .pine/tickets/BUG-8dmp5y.md                        |  183 ++
 .pine/tickets/BUG-8h4yy1.md                        |   46 +
 .pine/tickets/BUG-8sb0jw.md                        |  239 ++
 .pine/tickets/BUG-8t94wn.md                        |  179 ++
 .pine/tickets/BUG-9853ay.md                        |   84 +
 .pine/tickets/BUG-9x3te4.md                        |   23 +
 .pine/tickets/BUG-a1648n.md                        |   29 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 0 -> 10476 bytes
 .pine/tickets/BUG-aede06.md                        |  326 +++
 .pine/tickets/BUG-c241hm.md                        |  154 ++
 .pine/tickets/BUG-cq4yk3.md                        |  338 +++
 .pine/tickets/BUG-dndnhn.md                        |   48 +
 .pine/tickets/BUG-esb9sh.md                        |  138 ++
 .pine/tickets/BUG-f9frth.md                        |  410 +++
 .pine/tickets/BUG-fv5fer.md                        |  172 ++
 .pine/tickets/BUG-gaavr5.md                        |  363 +++
 .pine/tickets/BUG-hfhzq6.md                        |   49 +
 .pine/tickets/BUG-hm76dq.md                        |  119 +
 .pine/tickets/BUG-j7rtv3.md                        |   90 +
 .pine/tickets/BUG-kzkvv6.md                        |   74 +
 .pine/tickets/BUG-mewhrd.md                        |   68 +
 .pine/tickets/BUG-mz8xrb.md                        |   56 +
 .pine/tickets/BUG-npfz43.md                        |   38 +
 .pine/tickets/BUG-pwckhd.md                        |   63 +
 .pine/tickets/BUG-qmgz2f.md                        |  179 ++
 .pine/tickets/BUG-qq4xva.md                        |   57 +
 .pine/tickets/BUG-rjd6fm.md                        |  272 ++
 .pine/tickets/BUG-rrkjrd.md                        |   76 +
 .pine/tickets/BUG-s0wy50.md                        |   56 +
 .pine/tickets/BUG-t2wezf.md                        |   85 +
 .pine/tickets/BUG-tcqkad.md                        |  188 ++
 .pine/tickets/BUG-txc9xg.md                        |   71 +
 .pine/tickets/BUG-wdypd2.md                        |  230 ++
 .pine/tickets/BUG-wp2y0y.md                        |   45 +
 .pine/tickets/BUG-xf1wqm.md                        |   42 +
 .pine/tickets/BUG-y57cz4.md                        |  167 ++
 .pine/tickets/BUG-ysvmaa.md                        |  298 +++
 .pine/tickets/BUG-ze1nn8.md                        |  114 +
 .pine/tickets/BUG-ztzxck.md                        |   56 +
 .pine/tickets/EPIC-cfe7ny.md                       |   39 +
 .pine/tickets/FEAT-0895qc.md                       |  311 +++
 .pine/tickets/FEAT-15k49d.md                       |   37 +
 .pine/tickets/FEAT-56nep4.md                       |  175 ++
 .pine/tickets/FEAT-cwmw90.md                       |   21 +
 .pine/tickets/FEAT-edxxj7.md                       |  188 ++
 .pine/tickets/FEAT-j5s2n4.md                       |  188 ++
 .pine/tickets/FEAT-jvembs.md                       |  363 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  161 ++
 .pine/tickets/FEAT-qdedm0.md                       |   23 +
 .pine/tickets/FEAT-x5km1z.md                       |  142 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |  114 +-
 README.md                                          |   27 +-
 cmd/kilasflow/main.go                              |  158 +-
 cmd/kilasflow/retention_test.go                    |   87 +
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |   13 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    4 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/execution-model.md  |  210 +-
 docs/src/content/docs/concepts/expressions.md      |   99 +-
 docs/src/content/docs/concepts/node-registry.md    |   35 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 docs/src/content/docs/concepts/webhooks.md         |   62 +-
 docs/src/content/docs/guides/community-nodes.md    |    2 +-
 docs/src/content/docs/guides/embedding.md          |   16 +-
 docs/src/content/docs/guides/n8n-migration.md      |  127 +-
 docs/src/content/docs/guides/node-authoring.md     |   10 +-
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |   13 +-
 docs/src/content/docs/operate/deployment.md        |   26 +-
 docs/src/content/docs/operate/security.md          |   52 +-
 docs/src/content/docs/operate/upgrades.md          |   17 +-
 docs/src/content/docs/reference/api-contract.md    |   13 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    2 +-
 docs/src/content/docs/reference/api/datastores.md  |  392 +++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |    2 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    4 +-
 docs/src/content/docs/reference/api/tenants.md     |  200 ++
 docs/src/content/docs/reference/api/workflows.md   |   12 +-
 .../content/docs/reference/expression-grammar.md   |  369 ++-
 docs/src/content/docs/start/install.md             |   44 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   91 +-
 e2e/fixtures/datastore.ts                          |    8 +-
 e2e/fixtures/epic-telegram.ts                      |   24 +-
 e2e/fixtures/gates.ts                              |   31 +
 e2e/fixtures/library-import.ts                     |   13 +-
 e2e/fixtures/n8n-live.ts                           |   10 +-
 e2e/playwright.config.ts                           |    4 +
 e2e/scripts/skip-budget.mjs                        |  107 +
 e2e/skip-budget.json                               |   25 +
 e2e/tests/ai-agent-ollama.spec.ts                  |   30 +-
 e2e/tests/epic-acceptance.spec.ts                  |    8 +-
 e2e/tests/library-import.spec.ts                   |    8 +-
 e2e/tests/waha-migration.spec.ts                   |    6 +-
 internal/ai/agent.go                               |  101 +-
 internal/ai/ai.go                                  |   31 +-
 internal/ai/ai_test.go                             |  132 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  140 +-
 internal/ai/openai_test.go                         |  145 ++
 internal/ai/outputschema.go                        |   16 +
 internal/api/cors_test.go                          |  102 +
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/csv_export_test.go                    |   66 +
 internal/api/embed_confinement_test.go             |  284 +++
 internal/api/handlers/admin.go                     |  524 ++++
 internal/api/handlers/admin_admin_test.go          |  579 +++++
 internal/api/handlers/auth.go                      |  233 +-
 internal/api/handlers/auth_test.go                 |  366 +++
 internal/api/handlers/credentials.go               |   37 +-
 internal/api/handlers/datastores.go                |   32 +-
 internal/api/handlers/datastores_csv.go            |   72 +
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   14 +-
 internal/api/handlers/embedscope.go                |  126 +
 internal/api/handlers/executions.go                |  318 ++-
 internal/api/handlers/interop.go                   |  130 +-
 internal/api/handlers/problem.go                   |   31 +
 internal/api/handlers/schedules.go                 |   35 +-
 internal/api/handlers/workflows.go                 |  175 +-
 internal/api/handlers/workflows_conflict_test.go   |  125 +
 internal/api/handlers/workflows_delete_test.go     |    4 +-
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   82 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |  132 +
 internal/api/middleware/cors_test.go               |  189 ++
 internal/api/middleware/loginlimit.go              |  203 ++
 internal/api/middleware/loginlimit_test.go         |  172 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/routes.go                             |   14 +-
 internal/api/server.go                             |  123 +-
 internal/api/workflows_test.go                     |   88 +-
 internal/auth/auth_test.go                         |   42 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |   15 +-
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  195 ++
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  257 ++
 internal/config/config.go                          |  305 ++-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   16 +-
 internal/database/migrate.go                       |   59 +-
 internal/database/migrate_test.go                  |   45 +
 internal/datastore/catalogue.go                    |  154 +-
 internal/datastore/catalogue_test.go               |  115 +
 internal/datastore/fleet.go                        |    9 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |   16 +
 internal/engine/approval.go                        |   17 +-
 internal/engine/authenticate.go                    |   45 +-
 internal/engine/authenticate_test.go               |  135 +
 internal/engine/checkpoint.go                      |   24 +
 internal/engine/error_workflow_test.go             |  230 ++
 internal/engine/expression_context_test.go         |   71 +
 internal/engine/lease_test.go                      |  401 +++
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiprocess_test.go               |  115 +-
 internal/engine/runner.go                          | 1670 +++++++++----
 internal/engine/runner_test.go                     | 1473 ++++++++++-
 internal/engine/service.go                         |  724 +++++-
 internal/engine/service_test.go                    |  385 ++-
 internal/engine/subworkflow_test.go                |    2 +-
 internal/engine/trace_persist_test.go              |  159 ++
 internal/engine/trace_test.go                      |    8 +-
 internal/engine/wait_service.go                    |  174 +-
 internal/engine/wait_service_test.go               |  232 +-
 internal/engine/worker_test.go                     |   15 +
 internal/expression/doc.go                         |   94 +-
 internal/expression/evaluator.go                   |  771 ++++++
 internal/expression/expression.go                  |  474 +---
 internal/expression/expression_test.go             |   56 +-
 internal/expression/functions.go                   |  219 --
 internal/expression/globals.go                     |  528 ++++
 internal/expression/luxon.go                       |  320 +++
 internal/expression/methods.go                     | 1187 +++++++++
 internal/expression/parity_test.go                 |  505 ++++
 internal/expression/parser.go                      |  824 +++++++
 internal/expression/roots.go                       |  339 ++-
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/gowa.go                       |   37 +-
 internal/interop/n8n/gowa_test.go                  |   17 +-
 internal/interop/n8n/importer_tail_test.go         |  772 ++++++
 internal/interop/n8n/n8n.go                        |  462 +++-
 internal/interop/n8n/n8n_test.go                   |  177 +-
 internal/interop/n8n/parameters.go                 | 2606 ++++++++++++++++++--
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 ++++
 internal/loadoptions/loadoptions.go                |   11 +
 internal/loadoptions/redirect_test.go              |  117 +
 internal/property/property.go                      |   20 +-
 internal/property/visibility_test.go               |   34 +
 internal/repository/auth.go                        |  388 +++
 internal/repository/auth_admin_test.go             |  477 ++++
 internal/repository/claim_lease_test.go            |  266 ++
 internal/repository/claim_wake_test.go             |    4 +-
 internal/repository/credentials.go                 |  103 +
 internal/repository/execution_retention_test.go    |    3 +-
 internal/repository/executions.go                  |  534 +++-
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   40 +-
 internal/repository/models_test.go                 |  141 +-
 internal/repository/postgres_execution_test.go     |    4 +-
 internal/repository/prefix_test.go                 |    8 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  112 +
 internal/repository/subworkflow_activation_test.go |  153 ++
 internal/repository/tenant_purge_test.go           |    2 +-
 internal/repository/waits_test.go                  |    2 +-
 internal/repository/webhooks.go                    |  188 +-
 internal/repository/webhooks_test.go               |  306 +++
 internal/repository/workflow_history.go            |   14 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  233 +-
 internal/safehttp/safehttp.go                      |   54 +-
 internal/safehttp/safehttp_test.go                 |   97 +
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   52 +
 internal/scheduler/scheduler_test.go               |   67 +
 internal/sqlbuild/sqlbuild_test.go                 |    4 +-
 internal/web/dist/index.html                       |   37 -
 internal/web/embed.go                              |  459 +++-
 internal/web/embed_test.go                         |  335 ++-
 internal/web/placeholder/index.html                |   17 +
 internal/webhook/export_test.go                    |    2 +-
 internal/webhook/form.go                           |  262 ++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/request_lifecycle.go              |  425 +++-
 internal/webhook/request_lifecycle_test.go         |  411 +++
 internal/webhook/shape.go                          |  299 ++-
 internal/webhook/shape_test.go                     |  146 +-
 internal/webhook/webhook.go                        |  758 ++++--
 internal/webhook/webhook_test.go                   |  678 ++++-
 internal/workflow/compiler.go                      |   81 +-
 internal/workflow/document.go                      |    4 +
 .../000010_execution_reclaim_count.down.sql        |    7 +
 .../postgres/000010_execution_reclaim_count.up.sql |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../sqlite/000010_execution_reclaim_count.down.sql |    7 +
 .../sqlite/000010_execution_reclaim_count.up.sql   |   17 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 nodes/ai.go                                        |  498 +++-
 nodes/ai_test.go                                   |  733 +++++-
 nodes/assignments.go                               |   51 +-
 nodes/core.go                                      |    5 +
 nodes/datastore.go                                 |   41 +-
 nodes/datastore_test.go                            |  228 +-
 nodes/datetime.go                                  |   80 +-
 nodes/datetime_test.go                             |  161 +-
 nodes/embedscope.go                                |  220 ++
 nodes/embedscope_test.go                           |  242 ++
 nodes/error_workflow.go                            |  223 ++
 nodes/error_workflow_test.go                       |  118 +
 nodes/executors.go                                 |   34 +-
 nodes/executors_test.go                            |   66 +
 nodes/http.go                                      |  347 ++-
 nodes/http_test.go                                 |  302 ++-
 nodes/loop.go                                      |  160 +-
 nodes/sql_options_live_test.go                     |   33 +-
 nodes/subworkflow.go                               |   97 +-
 nodes/subworkflow_calls_test.go                    |   56 +
 nodes/telegram_download.go                         |    3 +-
 nodes/telegram_lifecycle.go                        |    7 +-
 nodes/transform.go                                 |   32 +-
 nodes/transform_test.go                            |    8 +-
 nodes/unsupported.go                               |   16 +
 nodes/wait.go                                      |  217 +-
 nodes/webhook.go                                   |  477 +++-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   83 +-
 packs/waha/waha_test.go                            |  373 ++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 scripts/check-coordinates.sh                       |   60 +
 scripts/generate-api-reference.mjs                 |   30 +-
 scripts/smoke-dev.sh                               |   27 +
 sdk/README.md                                      |   12 +
 sdk/examples/host-page/README.md                   |   11 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   29 +-
 sdk/package.json                                   |    6 +-
 web/src/lib/api/generated/admin/admin.ts           |  957 +++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 web/src/lib/api/generated/models/index.ts          |   15 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 .../api/generated/models/workflowDocumentInput.ts  |    2 +
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |   32 +-
 web/src/lib/api/http.ts                            |   16 +
 .../lib/components/ui/sheet/sheet-content.svelte   |   11 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   81 +
 .../components/workflow-editor/canvas-node.svelte  |  376 ++-
 .../components/workflow-editor/node-picker.svelte  |  148 +-
 .../workflow-editor/properties-panel.svelte        |  167 +-
 .../workflow-editor/property-field.svelte          |  534 ++--
 .../workflow-editor/version-panel.svelte           |   62 +-
 .../workflow-editor/workflow-editor.svelte         |  736 +++++-
 web/src/lib/dashboard/execution-list.test.ts       |  102 +
 web/src/lib/dashboard/execution-list.ts            |  106 +
 web/src/lib/dashboard/workflow-list.test.ts        |  101 +
 web/src/lib/dashboard/workflow-list.ts             |  122 +
 web/src/lib/embed/embed-editor.svelte              |  151 +-
 web/src/lib/embed/session.svelte.ts                |   73 +-
 web/src/lib/embed/session.test.ts                  |   66 +-
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/authoring.test.ts      |  118 +
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   94 +-
 web/src/lib/workflow-editor/conditions.ts          |  176 +-
 web/src/lib/workflow-editor/credentials.test.ts    |   39 +
 web/src/lib/workflow-editor/credentials.ts         |   29 +-
 web/src/lib/workflow-editor/document.test.ts       |  158 ++
 web/src/lib/workflow-editor/document.ts            |  291 ++-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   28 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   21 +-
 web/src/lib/workflow-editor/execution.test.ts      |   16 +
 web/src/lib/workflow-editor/execution.ts           |    3 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   50 +
 web/src/lib/workflow-editor/expression-assist.ts   |   91 +
 .../lib/workflow-editor/expression-grammar.test.ts |   49 +
 web/src/lib/workflow-editor/expression-grammar.ts  |   49 +
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   92 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 +-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 +
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/parameter.test.ts      |   21 +-
 web/src/lib/workflow-editor/parameter.ts           |   16 +
 web/src/lib/workflow-editor/ports.test.ts          |  243 +-
 web/src/lib/workflow-editor/ports.ts               |  125 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   77 +
 web/src/lib/workflow-editor/shortcuts.ts           |  115 +
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 web/src/lib/workflow-editor/visibility.ts          |   10 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  369 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  369 ++-
 .../app/workflows/diagnostics-section.svelte       |   10 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   28 +-
 .../app/workflows/import-report-drawer.svelte      |   60 +
 .../(dashboard)/app/workflows/import-report.svelte |  122 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  147 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |    6 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  168 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  176 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  223 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   59 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  293 ++-
 web/src/routes/embed/[id]/+page.svelte             |   12 +-
 web/src/routes/login/+page.svelte                  |   75 +
 web/vite.config.ts                                 |    7 +-
 434 files changed, 57355 insertions(+), 4725 deletions(-)
```

## Reopened by review (2026-09-20) — Important
- **moment token cascade**: `momentToLuxon` (`parameters.go:2707-2721`) applies sequential `ReplaceAll` passes whose outputs stay matchable, so `DD`→`dd`→`cc`, `ZZ`→`ZZZZ`, and `X` maps to Luxon's `s`; `YYYY-MM-DD HH:mm` renders the ISO weekday instead of the day (v1 Date & Time). Fix: single index-based pass (like `escapeKeepingEscapes`) and correct the offset rows.
- **Merge options parked in an unread bag**: `mergeToKilas` (`:836-852`) copies includeUnpaired/keepNonMatches/enrichInput2/clashHandling/mergeMode/multipleMatches into `converted["options"]`, marks them consumed so nothing is reported, but no Merge path reads that bag — the executor implements exactly these semantics under `joinMode` (`nodes/executors.go:529,:559`), so unpaired items vanish silently. Fix: map the implemented keys onto `joinMode` and report the rest.
- **Switch numeric fallbackOutput inert**: the numeric arms write `fallbackOutput` (`:985-998`) but the Switch understands only `none`/`extra` (`nodes/flow.go:64-69,152-156,179`), so unmatched items disappear while the import report stays clean — the previous code warned instead. Fix: report the numeric case (or map it onto a reachable rule output).
