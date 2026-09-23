---
id: FEAT-g6k3y9
title: Code-node JavaScript runs in worker processes, so a runaway built-in costs a worker, not the server
status: doing
priority: high
parent: EPIC-tjnr1z
created: "2026-09-23T06:11:28Z"
updated: "2026-09-23T06:11:31Z"
---

# Description

goja cannot interrupt a single built-in call, and the heap watchdog can only
interrupt. The in-process guard against a built-in that allocates or loops as
far as a number tells it (`boundAllocations`) is therefore a denylist, and
every review of it found another way around it: a workflow author could hold
a core or take the whole server's memory in one call. The owner chose process
isolation, as n8n's task runners do, on 2026-09-23.

The server runs Code-node JavaScript in a pool of worker processes: the same
binary, re-executed. A runaway built-in now costs one worker, which the parent
kills and replaces. The in-process guards stay, as defence in depth that turns
the common cases into a readable RangeError instead of a killed worker.

# Acceptance Criteria
- [ ] `jsrun.Runner.Run` is `Prepare` (in the parent: encode the input, check
      its cap, build the snapshot; compiles and runs nothing) plus `Execute`
      (on a fresh VM). A `Job`, a `Result` and every jsrun error cross a pipe
      and keep their meaning: `errors.Is` on each sentinel, and the
      ScriptError, SyntaxError and UnsupportedError fields.
- [ ] `internal/jsworker`: a pool of at most `javascript_max_concurrent`
      workers, started on demand and reused; a two-way framed protocol so
      `$('Node')` and item pairing are answered by the parent while the code
      runs.
- [ ] A worker that overruns a wall-clock backstop past the time limit is
      killed and the run fails with the time-limit sentence; one that dies
      mid-run fails the run with a named error (memory when the runtime said
      so), and the next run gets a fresh worker.
- [ ] Cancelling the execution kills the worker running it.
- [ ] Linux: each worker has an address-space rlimit, `oom_score_adj` 1000 and
      a parent-death signal. Everywhere: a minimal environment (none of the
      server's secrets), and it exits when its stdin closes.
- [ ] `cmd/kilasflow` runs as a worker when started as one, before reading
      config, and the server uses the pool. Tests and the corpus keep the
      in-process runner.
- [ ] Guardrail: `internal/jsworker` does not import goja; jsrun still
      imports no os, os/exec or syscall.
- [ ] Docs: the execution model and configuration reference describe the
      workers.

# Implementation Plan

# Notes

# Related Files

# Attachments
