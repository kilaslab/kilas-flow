---
id: BUG-548bk9
title: 'Code-node JavaScript: an unhandled promise rejection from a callback is lost instead of failing the run as in Node'
status: doing
priority: low
parent: EPIC-tjnr1z
created: "2026-09-23T07:09:43Z"
updated: "2026-09-23T07:09:43Z"
---

# Description

In Node an exception thrown from a callback (crypto.randomBytes(n, cb),
pbkdf2, scrypt, randomInt) or a promise rejected with no handler is uncaught
and ends the process, which fails an n8n Code node. goja tracks neither, so
in Code-node JavaScript such an error is lost and the node succeeds with
whatever the code returned. A throw from a timer callback already fails the
run. Found by the crypto lane of FEAT-zjrw76.

# Steps to Reproduce

A Code (JavaScript) node with:

    require('crypto').randomBytes(4, () => { throw new Error('lost') })
    await new Promise((resolve) => setTimeout(resolve, 10))
    return items

# Expected

What n8n does with the same node: check its JS task runner first (it may
fail the node with "lost", or only log it).

# Actual

The node succeeds, and "lost" appears nowhere.

# Acceptance Criteria
- [ ] n8n's behaviour for an unhandled rejection and a throwing async callback
      in a Code node is recorded.
- [ ] Code-node JavaScript does the same, using goja's promise rejection
      tracker, without counting the runtime's own promises or the body's
      result, which the runner consumes.

# Related Files

# Attachments

# Notes

## What n8n does (observed 2026-09-24, black-box)

Observed on the official image `n8nio/n8n:2.33.7` (Node v24.18.0, internal
JS task runner, `NODE_FUNCTION_ALLOW_BUILTIN=*` so `crypto` could be
required). Each case was a Manual Trigger followed by one Code node in "Run
once for all items" mode, run through the REST API, with the execution and
the container log read back.

- **What happens.** Node's default applies: an uncaught exception or a
  rejection with no handler ends the task-runner process. The node fails, but
  not with the script's error. It fails with the broker's generic
  "the runner went away" error: the message is "Node execution failed", and
  the description suggests batching and more runner memory. The script's own
  error (for example `Error: lost` with its stack) appears only in the server's
  log. The runner is restarted for the next execution.
- **Cases that fail the node:**
  - a `crypto.randomBytes` callback that throws while the code still awaits a
    timer;
  - `Promise.reject(new Error(…))` with no handler, and then a wait on a timer;
  - the same with a string reason;
  - a rejected promise that gets its handler only after a timer, which is too
    late;
  - a `setTimeout` callback that throws while the code awaits another timer;
  - an async function called and not awaited that throws;
  - `Promise.resolve().then(() => { throw … })`.
- **Cases that do not fail the node:**
  - A rejection that gets its handler after an `await null`, in the same
    microtask turn, is not unhandled.
  - The node succeeds when the code returns in the same turn as the rejection,
    as in `Promise.reject(…); return items`, or `…; await null; return items`.
    It also succeeds when the throwing callback or timer fires after the code
    has returned. The runner still dies afterwards, but its result has already
    been delivered. This held on every repeat.
- **Timing.** The fatal check happens at the first point where the microtask
  queue has drained while the node is still running. A later, ordinary throw
  from the body does not win: with
  `Promise.reject(other); await timer; throw mine`, the run dies on `other`
  before the timer fires.
- **Per-item mode with "continue (regular output)".** This behaves the same
  way: the whole node fails. A process crash cannot fail one item.
- **`queueMicrotask`** is not defined in n8n's Code node sandbox (a
  ReferenceError). KilasFlow defines it, and a throwing microtask is treated
  like any other rejected job.

## Plan and decisions

- Install goja's promise rejection tracker in `engine_rejections.go`. A
  rejected promise with no handler joins a set, in order, and leaves the set
  if a handler is attached.
- The runner checks the set wherever n8n's runner would die: in `await`,
  after each VM entry has drained goja's job queue, and only while the body's
  promise is still pending. So a rejection handled within the same drain is
  not unhandled, and a body that settles in the same drain as a stray
  rejection keeps its result, as in n8n.
- Excluded from the set:
  - the body's own result promise, which the runner consumes, as it does in
    per-item mode after a failed item;
  - host-call promises made by `bindAsync` (the runtime's own).

  Consequence: a host call the code starts and never awaits does not fail the
  run if it fails. Node would count it. This follows the task's explicit
  instruction. Promises the code derives from a host call (`.then`) are the
  code's own and do count.
- **The error.** It fails the run with the script's own error, not n8n's
  generic "Node execution failed". The generic text is an artifact of the
  runner process dying, and the real error is only in n8n's server log, where
  a KilasFlow user cannot see it. It is a `ScriptError` with `Uncaught` set.
  Its text is `Uncaught Error: lost [line 1]`, and it keeps the name, message,
  line and stack for the editor.
- **Scope.** It is not item-scoped, so it ends the run even with
  continue-on-fail in per-item mode, as n8n's process crash does. No item
  index is stamped on it, because the rejection may come from an earlier item.
