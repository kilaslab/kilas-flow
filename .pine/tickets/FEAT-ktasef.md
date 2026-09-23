---
id: FEAT-ktasef
title: 'Editor canvas: live per-node run status, item counts, error badges, and actionable run errors'
status: todo
priority: high
labels:
    - editor
    - debugging
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:25:27Z"
updated: "2026-09-23T01:25:27Z"
---

# Description

After Execute, the editor shows only a banner. When a run is refused, the chat panel shows a bare "workflow validation failed" with no node or reason. In n8n, the canvas itself is the debugger.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead, ux-debug; finding ids: UXD-1, LEAD-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXD-1: Running from the editor gives no per-node status, item counts or error badge on the canvas, only a banner

*gap · high · editor-canvas*

**n8n:** While the run is in progress each node shows a spinner, then a green check with the item count or a red error triangle. Edges show "N items", and the failing node is outlined in red. Opening any node shows its input and output from that run straight away.

**Steps to reproduce:**

1. Open `[ux-debug] a HTTP 500 chain` in the editor. 2. Click **Execute**. 3. Also try `[ux-debug] e loop with wait` and watch the canvas while it runs.

**Actual:**

The canvas does not change. A strip reads `Run failed: execute node "call-api": node "Call API": request failed with status 500  View execution`, and while a run is going it reads `Run waiting… View execution`. No node is marked, no edge shows a count, and clicking a node opens only the Parameters/Settings panel. The editor polls `GET /executions/{id}` for the overall status and never reads the node runs.

**Expected:**

Live node states and item counts on the editor canvas (the SSE feed `/executions/{id}/events` already carries them), a red badge on the failing node, and the node's run data available from the editor.

**Suggested fix:**

Subscribe the editor to the execution's event stream, as the execution page already does with `applyEvents`, and render status and count overlays with the same canvas-node decorations `execution-canvas-node.svelte` uses.

**Evidence:**

`$SP/agents/ux-debug/a-02-after-execute.png`, `e-01-running-canvas.png`, `a-04-node-click.png`. web/src/routes/(dashboard)/app/workflows/[id]/+page.svelte:430-482 (the run polls status only), web/src/lib/components/workflow-editor/workflow-editor.svelte:1113-1124 (banner and link).

**Related:**

BUG-f9frth (done; its finding "After Execute the editor shows only a banner" still reproduces), FEAT-56nep4 (done)


## LEAD-2: Chat panel shows a bare "workflow validation failed" with no node or reason

*ux · high · debugging / editor-chat*

**n8n:** n8n refuses to run a workflow with issues and lists each node and issue ("Workflow has issues…"), and highlights the nodes on the canvas.

**Steps to reproduce:**

1. Import a workflow where a node not on the executed path, e.g. a Telegram node on an IF branch, lacks its credential. 2. Open Chat on the canvas and send a message.

**Actual:**

The chat bubble says only `workflow validation failed`. The API knows the reason: `workflow validate` returns `node "a8" requires a telegramApi credential` with nodeId and field.

**Expected:**

The chat panel, and the Execute button path, show the blocking diagnostics with node names, and clicking one opens that node.

**Suggested fix:**

Surface the problem+json `errors[]` / diagnostics in the chat panel and the run toast; badge the offending nodes.

**Evidence:**

scratchpad readme/snap3.yml; `kilasflow workflow validate` output


# Acceptance Criteria
- [ ] While a run executes, each node shows running, success with item count, or an error badge, and edges show item counts (from the SSE feed already used by the execution page)
- [ ] Clicking a node after a run shows its input and output from that run
- [ ] A run refused by validation shows each blocking diagnostic, with the node name and the reason, in the banner, the chat panel and the run toast; clicking one opens the node
- [ ] The offending nodes are badged on the canvas
- [ ] n8n-like run animation, the same on the editor canvas and on the execution replay: a running node shows a spinner or pulsing border; edges carrying data animate while their source node runs (`animated` is hard-coded `false` in `execution-canvas.svelte:64` today); success is a green check with the item count; failure is a red outline with an error triangle; a node that finished with issues (continue-on-fail, partial error output, "no output data") gets a yellow warning state
- [ ] Loops: a node inside a loop shows a run counter ("3 runs") that updates live, and edges show cumulative item counts (see BUG-0xv7bg for the run selector)
- [ ] Waiting: a node suspended in a Wait shows a distinct waiting state; a timer wait shows "resumes at…" (see BUG-x6gyc1)
- [ ] The animation respects `prefers-reduced-motion` (a static state indicator instead of motion)

# Implementation Plan

See each finding's suggested fix above.

# Notes

**2026-09-23: progress on criterion 3.** The canvas chat panel now shows every blocking diagnostic with its node name, and clicking one opens the node (FEAT-edzr73). The banner's issue links now work too: Svelte Flow's stale selection report used to undo the programmatic selection, fixed with `pendingSelection`. The run-status animation criteria are still open.

Related tickets: BUG-f9frth, FEAT-56nep4

# Related Files

# Attachments
