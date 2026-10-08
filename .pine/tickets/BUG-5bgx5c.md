---
id: BUG-5bgx5c
title: 'Switch v1/v2 rules misread on import: every rule dropped and branches 2+ cut (BUG-6as5y7 regression)'
status: done
priority: high
labels:
    - importer
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:35:50Z"
updated: "2026-10-08T15:55:00Z"
---

# Description

Templates 1934 (Telegram AI chatbot, 168K views) and 1534 lose every branch after the first. The code comment claims this was fixed in BUG-6as5y7.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: n8n-templates; finding ids: TPL-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Switch v1/v2 (rules mode) stores `dataType` and `value1` at node level. Each `rules.rules[]` row holds only `operation`, `value2` and an optional `output` index (n8n `packages/nodes-base/nodes/Switch/V1/SwitchV1.node.ts`: `value1` at l.121, `rules.rules[].value2`/`output` at l.173-181). Templates 1934 "Telegram AI chatbot" (168K views) and 1534 "Back up your n8n workflows to GitHub" (80K views) use it. So do 5 of the top 198.

# Steps to Reproduce

1. Run `python3 agents/n8n-templates/minimal_repros.py` (case `switch_v1`). It imports Manual → Switch(v1, `dataType:"string"`, `value1:"={{ $json.cmd }}"`, `rules.rules:[{operation:"startsWith",value2:"/a"},{operation:"startsWith",value2:"/b",output:1}]`) → A / B.
2. Import template 1934 (or 1534) and open it in the editor.

# Expected

The legacy rows import as v3 rules. `value1` and `dataType` come from the node, `operation`/`value2` from each row, and each row routes to its `output` index. All branches are kept.

# Actual

- The import reports: blocking "this Switch's rules could not be read in either the current or the legacy shape; rebuild them before running the workflow", then lossy "this Switch has no readable rules", then lossy "the \"main\" connection from \"CheckCommand\" to \"Greeting\" was held back because \"CheckCommand\" declares no main port for it", once per branch after the first.
- The node is stored with no parameters. `validate` says: "switch rules must be a list" and "requires parameter \"rules\"".
- In 1934, the Greeting, Create-an-image and Send-error-message branches are gone from the canvas.

# Acceptance Criteria
- [x] `value1` and `dataType` are read from node-level parameters, and `operation`/`value2` from each row
- [x] Each row routes to its `output` index (default 0); all branches and edges survive
- [x] A regression test built from the real 1934 Switch node, not a hand-written shape

# Implementation Plan

Read `value1`/`dataType` from `node.Parameters` and pass them into each row's condition. Route by `row["output"]` (default 0), producing one rule per output in output order. Then add a test built from the real 1934 node, not a hand-written shape.

# Notes

Related tickets: BUG-6as5y7

Related (from the audit): BUG-6as5y7 (done) lists "Switch: … v1/v2 rules … cannot be imported". Its acceptance item is still unchecked, and the code comment at parameters.go:1009 claims the fix. This is a regression of a closed ticket.

# Related Files

- `internal/interop/n8n/parameters.go:1124-1162`: `legacySwitchRules` skips every row without `row["value1"]` (l.1132) and reads `dataType` from the row (l.1135). Both live at node level in n8n. It also ignores `output`.
- `minimal_repros.json` (switch_v1: 3 n8n edges → 2)
- `import-report.csv` rows 1934 and 1534 (`lost_edges {"main": 3}` / `{"main": 2}`)
- `ui_1934_canvas.png` (CheckCommand has one outgoing edge), `ui_1934_import_report.png`

# Attachments

## Fix (2026-10-08)

`switchToKilas` now hands any `rules.rules` node to `legacySwitchToKilas` (`internal/interop/n8n/parameters.go`), which replaces `legacySwitchRules`:

- `value1` and `dataType` come from the node (`dataType` defaults to `number`, n8n's default). `operation`/`value2` come from each row, and an omitted value or operation takes the type's default. Each row then goes through the existing `legacyCondition`.
- v1 routes a row to its `output` (default 0, one of four outputs). Rows that share an output are OR-ed into one rule. An output that no row names gets a never-matching rule labelled "Unused", which keeps later outputs on their wires. v2 routes by row position and carries `outputKey`.
- Fallback is read from the top-level `fallbackOutput`, and -1 means none. In v1 an index past the rules' last output becomes `fallbackOutput: "extra"`, padded with Unused rules if needed. So 1934's `fallbackOutput: 3` keeps its "Send error message" branch. An index naming a rule's own output keeps the existing `switchNumericFallback` issue. An index the node doesn't have is reported.
- When v1 rows are out of output order, a lossy issue says so, because first-match precedence can change.
- `legacyOperations` gains `notStartsWith`, `notEndsWith` and `notRegex`. `legacyDefaultOperation` returns `after` for `dateTime` (n8n's If v1 and Switch v1 default), and the If v1 import shares this.

Tests are in `internal/interop/n8n/switch_import_test.go`:

- The 1934 CheckCommand node's parameters as n8n stores them. Fetched from the template API into a scratchpad and not committed; the targets are No-Op nodes.
- The 1534 Check Status node.
- A v1 Switch with no `dataType`, a gap and a shared output.
- The v1 reorder warning.
- v2 positional routing with `outputKey`, fallback -1 and fallback 0.

Each test checks the rules and the 4/3/4/2/3 wired ports, then runs the imported node through the real Switch executor and confirms n8n's routing. For example, 1934 routes `hello`→0, `/start`→1, `/image a cat`→2 and `/help`→fallback 3. `TestLegacySwitchRulesAreTranslated` was removed: it encoded a per-row `value1`/`dataType` shape that n8n never writes, which is how the regression passed.

Verified: `go test ./internal/interop/...` ok, `go vet ./internal/interop/n8n/` clean, `go test ./internal/guardrails/` ok.
