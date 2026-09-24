---
id: BUG-548bk9
title: 'Code-node JavaScript: an unhandled promise rejection from a callback is lost instead of failing the run as in Node'
status: done
priority: low
parent: EPIC-tjnr1z
created: "2026-09-23T07:09:43Z"
updated: "2026-09-24T13:05:38Z"
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
- [x] n8n's behaviour for an unhandled rejection and a throwing async callback
      in a Code node is recorded.
- [x] Code-node JavaScript does the same, using goja's promise rejection
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
- Excluded from the set: only promises the runtime makes and consumes
  itself. That is the body's own result promise, which the runner reads (as
  it does in per-item mode after a failed item), and any internal promise
  marked with `ownPromise`, an opt-in nothing uses yet. A host call's promise
  is handed to the code, so it counts, as in Node (the controller's ruling in
  review, fix round 1).
- A job that throws is uncaught too, for example a timer's callback. It runs
  outside anything the code could catch it in (fix round 1).
- **The error.** It fails the run with the script's own error, not n8n's
  generic "Node execution failed". The generic text is an artifact of the
  runner process dying, and the real error is only in n8n's server log, where
  a KilasFlow user cannot see it. It is a `ScriptError` with `Uncaught` set.
  Its text is `Uncaught Error: lost [line 1]`, and it keeps the name, message,
  line and stack for the editor.
- **Scope.** It is not item-scoped, so it ends the run even with
  continue-on-fail in per-item mode, as n8n's process crash does. No item
  index is stamped on it, because the rejection may come from an earlier item.

## Progress (2026-09-24)

- `internal/jsrun/engine_rejections.go` holds the tracker and the check. The
  other changes are small:
  - `engine.go`: one field, one call in `newVM`, and in `await` the check,
    `consumed`, and `failedJob` for a job's error;
  - `run.go`: `itemScoped` and `forItem` leave an uncaught error to the run;
  - `ScriptError.Uncaught`, which renders as the `Uncaught ` prefix.
- `crypto.js` no longer lists a throwing callback as a difference from Node.
- The tests are in `internal/jsrun/rejections_test.go`. They cover every
  failing and succeeding case observed above, the body's own failure and
  per-item continue-on-fail, host-call promises, and the flag crossing the
  wire.
- **For the host-call reply work in the job loop (Task 1).** Any new place
  that runs a job must go back through `await`'s loop. There the check runs
  after it, and its error goes through `failedJob`. A promise handed to the
  code (`httpRequest`'s) must not be passed to `ownPromise`. Only internal
  plumbing the code never receives may be passed to it.
- **No changelog entry.** The JavaScript Code node itself has no entry under
  `[Unreleased]` yet, and this fix belongs in that entry when the epic adds it.

## Fix round 1 (2026-09-24)

- **A throwing timer callback was not treated as uncaught.** Its error came
  back through `await`'s job path as a plain `ScriptError`. That made it
  item-scoped, and it blamed the item running at the time. `failedJob` now
  marks what the code threw in a job as uncaught.
- **Host-call promises now count** (the controller's ruling). `bindAsync` no
  longer calls `ownPromise`.
- **New tests:**
  - a throwing timer, in all-items mode and armed by an earlier item in
    per-item mode;
  - an unawaited failing host call;
  - a stray rejection from item 0, raised as it returns, which surfaces on
    item 1's first await.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-24.

- Base: `fc3e48cb` (last commit at or before ticket created 2026-09-23)
- Commits (4):
  - `e90a37bf` — merge: BUG-548bk9 an uncaught callback error or unhandled rejection fails a Code node's run, as in n8n
  - `be2f8389` — BUG-548bk9: a throwing timer callback and an unhandled host-call promise are uncaught too
  - `b4282743` — BUG-548bk9: the ticket records n8n's observed behaviour and moves to testing
  - `6a53198b` — BUG-548bk9: an uncaught callback error or unhandled rejection fails a Code node's run while the code is still running, as n8n's task runner does
- Files changed (the ticket's own commits, d2484b8..e90a37bf):

```
 .pine/tickets/BUG-548bk9.md         | 118 +++++++++++++++++++++++++++++++++--
 internal/jsrun/engine.go            |  12 +++-
 internal/jsrun/engine_rejections.go |  94 ++++++++++++++++++++++++++++
 internal/jsrun/errors.go            |   8 +++
 internal/jsrun/js/modules/crypto.js |   3 -
 internal/jsrun/rejections_test.go   | 190 ++++++++++++++++++++++++++++++++++++++++++++++++++++++++
 internal/jsrun/run.go               |  15 +++--
 7 files changed, 428 insertions(+), 12 deletions(-)
```
