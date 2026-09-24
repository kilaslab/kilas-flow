---
id: BUG-c19kyx
title: 'Code node: a literal run argument to $items() or .all() is not flagged at import or save'
status: todo
priority: low
labels:
    - code-node
    - javascript
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-24T15:12:28Z"
---

# Description

Noted in Task 12 (BUG-kvpx6x). `$items('X', output, run)` and `$('X').all(branch, run)` read one output of a node's latest run; a run earlier than the latest is refused, but only when the code runs, because which run is the latest is known only then (`$items('X', 0, 0)` is fine on a node's first run and refused on its third). So neither the importer nor save-time validation says anything about a run argument, and a workflow whose code reads an earlier run activates and fails, or, inside a try/catch, carries on without that run's items. Corpus 2063 is that case: it reads `$items(name, 0, counter)` in a loop with the run taken from a counter variable, inside a try/catch.

The only run arguments that are always safe are a literal `-1` (n8n's "latest") and `$runIndex` read in lockstep; a literal number, a variable or any other expression may name an earlier run.

# Acceptance Criteria

- [ ] Decide whether the analyser should warn (not refuse: the read can be right) at import and save when a run argument to `$items(…)` or `.all(…)` is anything other than a literal `-1` or `$runIndex`, and if so, add it as a non-blocking diagnostic naming the line.
