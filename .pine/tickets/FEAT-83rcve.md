---
id: FEAT-83rcve
title: Expressions take $('X').all/.first/.last(branch, run) as n8n and the Code node do
status: todo
priority: low
created: "2026-09-25T10:25:28Z"
updated: "2026-09-25T10:25:28Z"
---

# Description

Found by the review of BUG-fthahg (2026-09-25). In an expression,
`$('X').all()`, `.first()` and `.last()` take no arguments: the methods are
registered with a fixed arity of zero (internal/expression/methods.go), so
`{{ $('IF').all(1) }}` or `{{ $('IF').first(0, -1) }}` is refused. n8n takes
`(branchIndex, runIndex)` on all three, in expressions and in Code alike, and
KilasFlow's Code node already does (internal/jsrun/js/runtime.js `read`).

The refusal is loud, not a misread, but an imported workflow that names a
branch in an expression fails where n8n's runs.

# Expected

`$('X').all(branch, run)`, `.first(branch, run)` and `.last(branch, run)` in an
expression read that output of X's latest run, with the same checks and the
same words as Code and `$items(name, output, run)`: an output X does not have
or an earlier run is refused by name, -1 is the latest run. With no branch the
connected output is read (BUG-fthahg). The same methods on a plain list keep
taking no arguments.

# Acceptance Criteria
- [ ] `.all/.first/.last(branch, run)` on a `$('X')` receiver read that output
      and run, in expressions, as Code reads them.
- [ ] Wrong output and earlier run are refused in the words Code and `$items`
      use.
- [ ] Docs (concepts/expressions, reference/expression-grammar) say so.
