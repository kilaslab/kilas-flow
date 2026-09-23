---
id: BUG-fthahg
title: 'Tolerated failures and $(''X'') reads diverge from n8n: one error item per input, error as an object, all output ports joined'
status: todo
priority: medium
created: "2026-09-23T07:47:27Z"
updated: "2026-09-23T07:47:27Z"
---

# Description

Found by the whole-branch review of EPIC-tjnr1z (2026-09-23); visible through
Code-node JavaScript, but engine-wide. n8n's JS task runner
(packages/@n8n/task-runner/src/js-task-runner/js-task-runner.ts) was checked
for the first two:

1. A node-level tolerated failure (toleratedOutput in internal/engine/runner.go)
   emits one error item per input item. n8n's Code node in "Run once for all
   items" mode emits a single `{ json: { error: message } }` under
   continueOnFail, so a downstream node fires once, not once per input item.
2. An error item carries `error` as an object `{ message, node }`
   (errorItem). n8n's Code node writes `error` as the message string, in both
   modes, so `{{ $json.error }}` renders differently and
   `{{ $json.error.message }}` is undefined in n8n.
3. `$('X').all()`, `.first()` and `.last()` read every output port of X joined
   (nodeItemFor flattens them): both IF branches, and error-output items. n8n
   reads the main output's first port, so `$('IF').last()` does not return a
   false-branch item.

The stabilise sprint changes errorItem (it no longer copies Paired) and the
pairing code around nodeItemFor, so settle this after that merges.

# Steps to Reproduce

1. Manual → Code (all items, continueRegularOutput) that throws, fed 3 items →
   Set `{{ $json.error }}`: KilasFlow gives 3 items with an object; n8n gives 1
   item with the message.
2. IF with items on both branches → Code reading `$('IF').all().length`.

# Expected

What n8n does in each case.

# Actual

As described above.

# Acceptance Criteria
- [ ] A tolerated node-level failure of a Code node in all-items mode emits the
      one error item n8n does (decide per node type whether this is the
      engine's rule or the Code node's).
- [ ] Error items carry `error` in n8n's shape, or the difference is decided
      and documented.
- [ ] `$('X')` reads the ports n8n reads, in expressions and in Code alike.

# Related Files

# Attachments
