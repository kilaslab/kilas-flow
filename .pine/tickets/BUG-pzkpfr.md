---
id: BUG-pzkpfr
title: 'Typing in the inspector re-projects the whole canvas: keystroke latency 29→98 ms from 50→300 nodes'
status: todo
priority: medium
labels:
    - editor
    - performance
    - regression
parent: EPIC-8rbys7
created: "2026-09-23T01:48:15Z"
updated: "2026-09-23T01:48:15Z"
---

# Description

Load, pan and zoom stay at 60 fps at 300 nodes. Typing is the one thing that degrades with size. BUG-qmgz2f halved the cost, but it is still O(n).

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-15). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** Typing latency in the NDV does not depend on workflow size.

# Steps to Reproduce

1. Open "[ux-editor] Perf 300 nodes". 2. Open "set 1" and type into its field 200 ms apart (SD/typeperf.js), and in a burst (SD/editperf.js).

# Expected

Under 50 ms per keystroke at any size.

# Actual

Median keystroke-to-frame latency is 29 ms at 50 nodes, 55 ms at 150 and 98 ms at 300. At 300 nodes, 11 of 12 keystrokes are 50-66 ms long tasks. A 20-key burst reaches a median of 123 ms and p95 of 251 ms, and Cmd+Z → frame takes 97 ms. Load, pan and zoom stay at 60 fps (see perf.md), so this is the only size-dependent cost.

# Acceptance Criteria
- [ ] A parameter keystroke updates only the edited node's projection
- [ ] Median keystroke latency is under 50 ms at 300 nodes, measured with the audit's perf script and recorded

# Implementation Plan

Patch only the changed node in `nodes` (keyed update), or debounce the projection of parameter edits (the canvas only shows the subtitle).

# Notes

Related tickets: BUG-qmgz2f

Related (from the audit): BUG-qmgz2f (done; measured 211 ms at 400 nodes and claimed a fix; halved but still O(n))

# Related Files

SD/perf.md section 4, SD/perf-typing.json. workflow-editor.svelte:381-386: the `$effect` rebuilds `documentFromCanvas(displayed, definitions, issues)` and maps every node on each draft change.

# Attachments
