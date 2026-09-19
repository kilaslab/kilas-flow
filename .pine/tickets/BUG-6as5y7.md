---
id: BUG-6as5y7
title: 'Transform parity: Aggregate/Sort keys, Merge modes, Date&Time, Summarize, SplitOut, Switch, key order'
status: todo
priority: high
labels:
    - nodes
    - importer
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-19T12:06:10Z"
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