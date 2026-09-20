---
id: BUG-hm76dq
title: 'Core node condition/type parity: IF/Filter/Switch loose types, v1 ops, empty values, $loop leak'
status: testing
priority: high
labels:
    - nodes
    - n8n-parity
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T01:20:54Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 4 finding(s) from dims: find:core-node-parity.

---
### Set: a typed assignment with an empty or missing value fails the whole execution (n8n writes null or 0) [find:core-node-parity] (high/parity-gap) · area: Set executor type conversion · confidence: high

convertAssignment refuses nil and "" for number and boolean types and stops the whole run. n8n writes null for undefined values and 0 for an empty number. KilasFlow also writes "" where n8n writes null for a missing string.

Evidence: - cases/v3_settypes.json (Set 3.4): number assignment with value "" gives n8n 0; KilasFlow fails 'assignment "emptyNum" is declared a number and "" is not one'.
- v3_settypes2: a number assignment of "={{ $json.nope }}" (absent field) gives n8n null; KilasFlow fails 'assignment "missNum" is declared a number and its value is not one'. Boolean behaves the same.
- v3_settypes3: a missing string gives n8n null, KilasFlow "".
- Successful conversions all match: "12"/"7.5"→number, "false"→false, "[1,2]"→array, JSON→object, number→string, dot-notation.
Code: nodes/assignments.go:110-140.

n8n behavior: Undefined gives null for any type, "" gives 0 for a number, and an error happens only on truly unconvertible values.

Impact: Optional fields in webhook, form and CRM payloads are routine, and one missing value aborts the run.

Suggested fix: Follow n8n's conversion table for null, undefined and "". Fail only on unconvertible values, and only when ignoreConversionErrors is off.

Files: /Users/izzadev/projects/k-flow/nodes/assignments.go

Existing tickets: FEAT-jwhdsy

---
### IF/Filter/Switch 'loose' type validation fails the execution on values n8n converts ('yes', '1', '') [find:core-node-parity] (high/parity-gap) · area: conditions evaluator · confidence: high

asBoolean uses strconv.ParseBool, which rejects yes/no, and asNumber rejects "". In loose mode these values error and fail the whole execution, where n8n converts them or routes them to the false branch.

Evidence: - ifl_bs_true (IF 2.2, loose, boolean 'is true'), values "true","false","yes","1","0": n8n puts [true,yes,1] on true and [false,0] on false. KilasFlow fails 'condition left value "yes" is not a boolean'.
- ifl_ns_gt (number > 4), values "10"," 3 ","5.5","" and a missing field: n8n sends "" and the missing value to false. KilasFlow fails 'condition left value "" is not a number'.
Code: internal/conditions/conditions.go:457-507.

n8n behavior: Loose validation converts common truthy and falsy strings and treats empty values as non-matching rather than as errors.

Impact: Webhook, form and sheet data is text, so one unusual value aborts the run instead of routing it.

Suggested fix: Mirror n8n's loose conversion table: boolean true/false/1/0/yes/no, case-insensitive; empty values do not match. Add table tests driven by live n8n results.

Files: /Users/izzadev/projects/k-flow/internal/conditions/conditions.go

Existing tickets: FEAT-vvwpjw (done; AC 33 claims loose validation parity)

---
### IF v1 conditions: operation names, per-type default operations and combineOperation are not translated [find:core-node-parity] (high/bug) · area: importer / IF v1 · confidence: high

legacyCondition copies v1 operation names unchanged, so larger, equal and isNotEmpty fail at runtime. It hard-codes "equals" when the operation is omitted, but n8n's number default is 'smaller', so rows are silently misrouted. It also ignores combineOperation 'any'. An ifOperators translation map exists in the code but is never used.

Evidence: - if_v1 / ifv1_any / ifv1_empty: KilasFlow fails 'condition operation "larger" is not supported for a number value' (the same happens for number "equal" and string "isNotEmpty"). n8n 2.33.7 runs all of them.
- cases/v2_ifv1_defaults.json, number {value1 n, value2 3} with no operation: n8n true=[n=1]; KilasFlow true=[n=3]. The rows are misrouted with no error.
Code: internal/interop/n8n/parameters.go:241-250 (unused map) and 350-360.

n8n behavior: IF v1 supports these operations, applies its per-type defaults and supports combineOperation any/all.

Impact: IF v1 appears in 3 of the 100 templates (5 nodes) and is common in older community workflows.

Suggested fix: Translate the v1 operation names (equal, notEqual, larger, largerEqual, smaller, smallerEqual, isEmpty, isNotEmpty, notContains, startsWith, endsWith, regex, dateTime after/before). Apply n8n's per-type defaults, map combineOperation any→or, and add fixtures.

Files: /Users/izzadev/projects/k-flow/internal/interop/n8n/parameters.go

Existing tickets: FEAT-vvwpjw

---
### Loop Over Items (SplitInBatches) injects an internal "$loop" object with pending/collected item copies into every item [find:core-node-parity] (high/bug) · area: loop executor · confidence: high

The loop keeps its state inside item JSON under $loop, including full copies of the pending and collected items. That state appears in the loop body's items, so it leaks into Set passthrough, HTTP bodies and stored execution data, and its size grows as O(n²) with the item count.

Evidence: loop_v3_default (5 items, batchSize 1): every loop-output and body item carries "$loop":{"collected":[...],"cursor":N,"iteration":N,"pending":[...remaining items...]}. n8n items stay {"id":1}. Key defined at nodes/loop.go:25 (LoopStateKey).

n8n behavior: Items pass through unchanged. Loop state lives in node context.

Impact: SplitInBatches v3 appears in 13 of the 100 templates (19 nodes). Large batches cause quadratic growth in memory and execution storage, and third-party APIs receive extra fields.

Suggested fix: Move loop state out of item JSON into the engine or run state and never serialise pending/collected into items. Add a regression test for body items.

Files: /Users/izzadev/projects/k-flow/nodes/loop.go

Existing tickets: FEAT-sar60r, FEAT-vvwpjw

## Acceptance criteria

- [x] Set: a typed assignment with an empty or missing value fails the whole execution (n8n writes null or 0)
- [x] IF/Filter/Switch 'loose' type validation fails the execution on values n8n converts ('yes', '1', '')
- [x] IF v1 conditions: operation names, per-type default operations and combineOperation are not translated
- [x] Loop Over Items (SplitInBatches) injects an internal "$loop" object with pending/collected item copies into ev
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Work (Main review 2026-09-19)
- IF v1 legacyCondition op-name translation + per-type defaults landed. Loose-type conversion (internal/conditions) + $loop leak + Set coerce table remain for EngineExpression/owner coordination.

## Work (ConditionsParity 2026-09-20) — commit 6f846e7
All four findings are fixed and each one has a bite-checked test. Scoped proof:
`go test ./internal/conditions/ ./internal/interop/n8n/ ./nodes/ -count=1` ok;
`go test ./internal/engine/ -run 'TestLoop|TestSwitch|TestFilter|TestIF|TestCondition' -count=1` ok.

- **Set null/'' (nodes/assignments.go).** n8n's own table (`validateFieldType`, Set >= 3.2): undefined and null are written as null for every declared type, `""`/`"  "` is 0 for a number, `true` -> 1, text carrying the value is converted, and only a genuinely unconvertible value is an error (and only without `ignoreConversionErrors`). The number/boolean conversions are now `conditions.Number`/`conditions.Boolean` rather than a second table. Bite check: `TestSetWritesN8nsValuesForEmptyAndTextualInput` fails pre-fix on the empty/blank/missing number, the missing boolean and the boolean-as-number rows.
- **Loose types (internal/conditions).** n8n's filter passes null/undefined through unconverted and calls both "no value"; conversions are JavaScript's (`Number('')` is 0, `tryToParseBoolean` with a truthiness fallback so `"yes"` is true, `Boolean('')` false), text carrying JSON is that JSON, NaN is not `exists`, and an absent value is not a conversion error in loose *or* strict mode. `TestLooseConversionFollowsN8nsOwnTable` (47 rows) + `TestLooseConversionStillNamesAnUnconvertibleValue`; bite check: 20 rows fail pre-fix.
- **IF/Switch v1 operations.** ImporterTail's translation landed but emitted the v1 names unchanged, one misspelt `largerEquals` — and `internal/conditions` only knew n8n v2's `gt/gte/lt/lte`, so `if_v1` still died with `condition operation "larger" is not supported for a number value`. Fixed on both sides: the evaluator accepts both spellings (the web editor, `property-field.svelte`, offers the v1 names too, and saved documents carry them), and `legacyOperations` emits the editor's (`largerEqual`, `smallerEqual`). Bite checks: `TestBothSpellingsOfTheCountComparisonsRun` and the seam test `TestImportTranslatesIFV1CountOperationsIntoRunnableOnes` (import -> evaluate) both fail pre-fix, the second with all four operations rejected.
- **$loop leak (nodes/loop.go, EngineFlow's BUG-dndnhn).** Verified, not re-implemented: state lives in runner-owned `Request.NodeState`, and `$loop`/`LoopStateKey`/`stripItems` are gone. `internal/engine/loopstate_test.go` (new) inspects every item the loop emits and every item its body is handed, through a passthrough body. Bite check: in a worktree at the commit before the fix it reproduces the finding verbatim — `loop port 1 item 0 carries $loop = {"collected":[],"cursor":1,"iteration":1,"pending":[{"id":2},{"id":3}]}`.

### Bounds and hand-offs
- **No live adversarial re-verify**: nothing listens on 5678/8101 here and `scratchpad/work` is gone (EngineFlow recorded the same for its slice). The conversion table was taken from n8n's own source in the read-only reference checkout (`packages/workflow/src/type-validation.ts`, `node-parameters/filter-parameter.ts`, `nodes/Set/v2/helpers/utils.ts`, `nodes/If/V1/IfV1.node.ts`, `nodes/If/V2/utils.ts`) and every row agrees with the live evidence in the finding. The version split inside `parseSingleFilterValue` is not modelled: this evaluator has no per-node filter version ladder and implements the table the evidence was taken from (IF 2.2 / Filter 2.2, where `Number('')` is 0). For n8n 2.3+ an empty string became null there, which only differs for `equals 0`.
- **Handed to Main as its own ticket (web owner)**: `web/src/lib/workflow-editor/conditions.ts` has no `gt/gte/lt/lte` in `KNOWN_OPERATIONS`, so an imported n8n-v2 number condition is read as `equals` and saved back rewritten — silent comparison corruption, found while fixing the v1 names.
- **Not implemented** (documented in code): n8n's JavaScript-literal JSON repair (`{a: 1}`), JavaScript's hex number spelling (`'0x10'`), and single-element lists read as numbers (`[5]` -> 5).
