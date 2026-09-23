---
id: BUG-2xrz6c
title: a Wait with onError continueErrorOutput can never resume
status: todo
priority: medium
labels:
    - engine
    - wait
parent: EPIC-8rbys7
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

The compiler adds an error output to any node whose `onError` setting is `continueErrorOutput` (`withErrorPort`, internal/workflow/compiler.go ~885-899), including a Wait node. `adaptStoredResumeOutput` pads the single stream the approval surface and the timer sweeper store to whatever port count the node declares (internal/engine/wait_service.go ~405-431), so a Wait with an error output pads its stored output to 2 streams. But `Runner.Resume`'s own port-count check expects `expectedPorts(node)` to already match what was stored, and a Wait's resume path independently expects 1 stream — the two disagree once an error output is added.

# Steps to Reproduce

1. Add a Wait node with `onError: continueErrorOutput`.
2. Suspend it (an approval wait or a timed wait) and resume it.

# Expected

The Wait resumes normally, routing to its regular or error output as its outcome decides.

# Actual

The execution fails with `resume output for node … has 2 streams, want 1`.

# Acceptance Criteria
- [ ] A Wait with `onError: continueErrorOutput` resumes successfully on both its regular and error paths
- [ ] A regression test covers a Wait with an error output through suspend and resume

# Related Files

internal/workflow/compiler.go `withErrorPort`, ~885-899 (adds the error output whenever `onError` is `continueErrorOutput`)
internal/engine/wait_service.go `adaptStoredResumeOutput`, ~405-431 (pads the stored single stream to the node's declared port count)
internal/engine/runner.go — the resume output stream-count check (`resume output for node %q has %d streams, want %d`, ~line 897)
