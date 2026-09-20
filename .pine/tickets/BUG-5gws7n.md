---
id: BUG-5gws7n
title: Wait timers race the callback against the handle assignment in waitTimers.arm
status: done
priority: high
labels:
    - engine
    - concurrency
created: "2026-09-20T05:08:00Z"
updated: "2026-09-20T05:02:49Z"
---

# Description

`go test ./internal/engine -race` reports a data race that depends on scheduling,
so it appears and disappears between runs — it survived the test cache for a
while and then failed on a rebuilt binary:

```
WARNING: DATA RACE
Read at 0x00c0000023d0 by goroutine 1741:
  internal/engine.(*waitTimers).arm.func1()
      internal/engine/wait_service.go:201
Previous write at 0x00c0000023d0 by goroutine 1668:
  internal/engine.(*waitTimers).arm()
      internal/engine/wait_service.go:200
  internal/engine.(*Service).armWaitTimer()
  internal/engine.(*Service).suspend()
  internal/engine.(*Service).runOnce()
```

Both failing cases were reached through expired waits —
`TestExpiredWaitsResolveOnTheirOwnDeadline` and
`TestResumeOfAPerItemSuspendProcessesEveryItem` — and each of them passes alone,
which is the shape of a race between the timer callback and the goroutine that
armed it rather than a broken test.

Cause, in `waitTimers.arm`:

```go
var timer *time.Timer
timer = time.AfterFunc(after, func() {
	timers.forget(timer)
	fire()
})
```

`time.AfterFunc` starts the timer before it returns the handle, and `armWaitTimer`
clamps a deadline that has already passed to a zero delay. The callback can
therefore run while `arm` is still assigning `timer`, so the closure reads the
variable the other goroutine is writing — the race above, and, without the
detector, a nil handle reaching `forget` on the unlucky interleaving.

# Acceptance Criteria
- [ ] `go test ./internal/engine -race -count=3` is clean, twice in a row.
- [ ] The fix orders the assignment before the callback's read rather than
      widening the window (no sleep, no retry, no dropped `forget`).
- [ ] `timers.forget` still runs for every fired timer, so `armed` cannot grow
      with dead handles; `detach` still stops everything the map holds.

# Implementation Plan

Hand the handle over through a buffered channel: `arm` sends after the
assignment, the callback receives before it forgets. The receive cannot happen
before the send, which is the happens-before edge the direct capture lacked, and
the mutex keeps the map insert ahead of `forget` even when the timer fires
instantly.

# Notes

`internal/engine/wait_service.go:200` was the only `time.AfterFunc` call site in
the repository, so there is no sibling with the same defect to fix.

The race is pre-existing: it reproduces on `458b038` (before this session's
commits) in a fresh worktree with `-count=1` forced.

# Related Files

- `internal/engine/wait_service.go`
- `internal/engine/wait_service_test.go`
- `internal/engine/service.go`

# Attachments

## Work Evidence

Closed by `pine close --evidence` on 2026-09-20.

- Base: `8bbff74c` (last commit at or before ticket created 2026-09-20)
- Commits (1):
  - `9e3134f1` — BUG-5gws7n: the wait timer hands its handle to the callback instead of capturing it
- Files changed (base → working tree):

```
 .pine/tickets/BUG-rpkjpy.md | 185 +++++++++++++++++++++-----------------------
 1 file changed, 89 insertions(+), 96 deletions(-)
```
