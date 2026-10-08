---
id: BUG-c7s5ss
title: 'Self-test minor findings: encryption.key boot warning, node ids in run errors, sticky notes as run nodes, legacy function nodes'
status: done
priority: low
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Minor findings of the 2026-09-25 Code-node self-test:

1. At boot the server warns that the config key `encryption.key` "matches nothing and was ignored" when `KILASFLOW_ENCRYPTION_KEY` is set, yet the key is in fact used.
2. The execution-level error names the internal node id (`execute node "n2"`) rather than the node's name.
3. Sticky notes appear in a run's node list as succeeded nodes with 0 items.
4. The legacy n8n `function` and `functionItem` nodes import as "no equivalent" placeholders and are not mentioned in the docs (1 of 151 sampled Code templates has one).

# Acceptance Criteria
- [x] The boot warning is accurate.
- [x] Execution-level errors name the node by its name.
- [x] Sticky notes are not listed as run nodes.
- [x] The Code-node guide says what happens to `function`/`functionItem` (or they are mapped onto the Code node).

## Fix (2026-10-08)

1. Boot warning: fixed by BUG-n4e97f and BUG-66fhea. `KILASFLOW_ENCRYPTION_KEY` raises no warning, and the warnings use the configured log format.
2. Node names in run errors: `nodeFailure` (internal/engine/runner.go) names the node by its name, not its id. When the executor's error already opens with `node "<name>": `, the name is not repeated (`execute node "Boom": Error: nope [line 1]`). The chat panel (web/src/lib/workflow-editor/chat.ts) reads the name from either prefix.
3. Sticky notes: a portless node was seeded as a root next to the trigger and, once the fallback skipped it, recorded as skipped at the end of the run. `annotation(node)` now excludes it from the seed, the fallback and `recordSkips`.
4. docs/src/content/docs/guides/code-javascript.md "Differences from n8n" says the legacy Function and Function Item nodes import as "no equivalent" placeholders, and how to move their bodies into a Code node.

Tests: `TestAFailedRunNamesTheNodeByItsName` and `TestAStickyNoteIsNotListedAsARunNode` (internal/engine/run_presentation_test.go, both failed first), plus a chat.test.ts case. `go test ./internal/engine`, the chat vitest and `make corpus-check` pass. Live check against the built binary: a run of Manual → Set → Code (throws), with a sticky note, lists manual/n2/n3 only, and its error reads `execute node "Boom": Error: nope [line 1]`.

