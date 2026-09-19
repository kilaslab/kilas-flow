---
id: BUG-dndnhn
title: Loop Over Items loses state when body node returns new items; loop restarts unbounded
status: todo
priority: critical
labels:
    - engine
    - loop
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T12:06:09Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:engine-runtime.

---
### Loop Over Items (SplitInBatches) loses its state when a body node returns new items, so the loop restarts on the body's output and runs away without bound [find:engine-runtime] (critical/bug) · area: nodes/loop.go loop state · confidence: high

kilasflow.loop stores its cursor, pending items and collected items as a `$loop` key on the first dispatched item (loop.go:149-153). When the body returns items without that key, readLoopState reports 'first entry' and takes the body output as fresh work. The iteration counter restarts at 1 each time, so the maxIterations guard never trips. Body nodes that drop the key include HTTP Request, Code, Aggregate, AI and DB nodes, and an IF or Filter that drops item 0 of a batch.

Evidence: Script <W>/t_loop_http.py on private :8101: n8n import Trigger -> SplitOut -> SplitInBatches v3 -> HTTP GET stub /sleep?s=0.1&i={{$json.arr}} -> Loop, with done -> NoOp and input [1,2,3]. The stub log shows `i=1` once, then `i=` (empty) 193 times between 15:35:43 and 15:36:03. The run only stopped at the 15 s timeout and was then reclaimed and re-run (see the lease finding). The previous attempt against a 60 s server logged 859,404 calls to /item?i= in 159 s (<W>/prior/stub-runaway.log).

n8n behavior: SplitInBatches v3 keeps its cursor in node context. Whatever the body returns is collected for `done`, and each input item is processed exactly once.

Impact: The canonical n8n pattern Loop -> HTTP/API call -> Loop turns N intended calls into unbounded calls against third-party APIs (rate-limit bans, billing). 13/100 top templates use Loop Over Items (19 nodes).

Suggested fix: Move loop state out of items into runner-owned per-execution node state keyed by the loop node id, and persist it in the Wait checkpoint. Count iterations there and derive 'first entry' from that state, never from item content.

Files: nodes/loop.go, internal/engine/runner.go

Existing tickets: FEAT-sar60r

## Acceptance criteria

- [ ] Loop Over Items (SplitInBatches) loses its state when a body node returns new items, so the loop restarts on t
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)