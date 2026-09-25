---
id: BUG-c19kyx
title: 'Code node: a literal run argument to $items() or .all() is not flagged at import or save'
status: testing
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-25T03:00:00Z"
---

# Description

Noted in Task 12 (BUG-kvpx6x). `$items('X', output, run)` and `$('X').all(branch, run)` read one output of a node's latest run; a run earlier than the latest is refused, but only when the code runs, because which run is the latest is known only then (`$items('X', 0, 0)` is fine on a node's first run and refused on its third). So neither the importer nor save-time validation says anything about a run argument, and a workflow whose code reads an earlier run activates and fails, or, inside a try/catch, carries on without that run's items. Corpus 2063 is that case: it reads `$items(name, 0, counter)` in a loop with the run taken from a counter variable, inside a try/catch.

The only run arguments that are always safe are a literal `-1` (n8n's "latest") and `$runIndex` read in lockstep; a literal number, a variable or any other expression may name an earlier run.

# Acceptance Criteria

- [x] Decide whether the analyser should warn (not refuse: the read can be right) at import and save when a run argument to `$items(…)` or `.all(…)` is anything other than a literal `-1` or `$runIndex`, and if so, add it as a non-blocking diagnostic naming the line.

# Notes

## Decision (2026-09-25)

Do not add a diagnostic. The runtime refusal of an earlier run is unchanged.

A run argument other than a literal `-1` or `$runIndex` may name an earlier run, and it may not: `$items('X', 0, 0)` is right on a node's first run and refused on its third. Noting it must not refuse the read, the import, or the save.

Import issues are only `blocking`, `lossy`, and `dropped` (`internal/interop/n8n/n8n.go`). There is no warning severity. Amendment 14 of EPIC-tjnr1z already dropped a non-blocking hint for that reason.

- `blocking` stops the workflow activating (`docs/src/content/docs/guides/n8n-migration.md`, the editor's import report). That refuses a read that can be right.
- `lossy` means the element was carried, but differently. The workflow still activates. The run argument is carried as written, so this word would claim a change that did not happen.
- `dropped` means the element was not carried at all. The workflow still activates. The argument is still in the code and still runs, so this word would claim it was discarded.

Save-time validation has the same gap: `ConfigValidator` returns an error, and `POST /workflows/validate` records every compiler refusal as `blocking`. A note there would stop the node saving.

Neither `lossy` nor `dropped` can say "this might fail when the code reaches it" without changing what those words mean. The Code (JavaScript) page already says an earlier run fails only then; it now also says import and save stay quiet about the argument.
