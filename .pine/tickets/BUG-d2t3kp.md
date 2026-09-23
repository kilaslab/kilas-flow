---
id: BUG-d2t3kp
title: Deleting an active workflow leaves its trigger registered with the remote service
status: done
priority: high
labels:
    - webhooks
    - lifecycle
parent: EPIC-7c3ry9
created: "2026-09-23T05:21:31Z"
updated: "2026-09-23T05:23:27Z"
---

# Description

`Delete` (`internal/api/handlers/workflows.go:701-717`) only calls `handler.workflows.Delete`, which drops the workflow's webhook bindings locally but never runs the trigger's lifecycle `remove` hook. `Deactivate` (`internal/api/handlers/workflows.go:787-799`) already runs `handler.triggers.Deactivated` before it drops the bindings, so a plain deactivate correctly unregisters a trigger with its remote service (Telegram's `setWebhook`, a pack's subscription API, etc.). Deleting an *active* workflow skips that hook entirely, so the remote side keeps delivering to a route that no longer exists.

Found while exploring BUG-vsmnby, which added the lifecycle capture/remove machinery this bug leaves unreachable from Delete.

# Steps to Reproduce

1. Activate a workflow whose trigger declares a lifecycle (e.g. a pack trigger with `remove`), so the remote service is told to deliver to this instance.
2. Delete the workflow without first deactivating it.

# Expected

Deleting an active workflow runs the same lifecycle deactivation `Deactivate` runs, so the remote registration is torn down before the workflow disappears.

# Actual

`Delete` never calls `handler.triggers.Deactivated`. The remote service is left registered against a route that `Delete` has already removed, so it keeps trying to deliver to a route that answers 404 forever instead of being told to stop.

# Acceptance Criteria
- [x] Deleting an active workflow calls its trigger's lifecycle `remove` hook exactly once, before the workflow is deleted, mirroring `Deactivate`'s order and failure handling (a hook failure is logged and never blocks the delete).
- [x] Deleting an inactive workflow calls no lifecycle hook.
- [x] Tests cover both cases.

# Related Files

internal/api/handlers/workflows.go (`Delete`, `Deactivate`), internal/webhook/lifecycle.go (`Coordinator.Deactivated`), internal/api/handlers/workflows_delete_test.go

# Attachments

## Progress

`Delete` now reads the workflow with `handler.workflows.Get` and, when it is active, calls `handler.triggers.Deactivated` before `handler.workflows.Delete` runs — the same order `Deactivate` uses and for the same reason: the hook needs the route before it is dropped. `Deactivated` has no error return (see `internal/webhook/lifecycle.go`'s `Coordinator.Deactivated`), so, exactly like `Deactivate`, a hook failure is only logged by the coordinator and never fails the delete request.

Tests (`internal/api/handlers/workflows_delete_test.go`):
- `TestDeletingAnActiveWorkflowRunsItsTriggersRemoveHook` — active workflow, `Deactivated` called exactly once with the right tenant/workflow.
- `TestDeletingAnInactiveWorkflowRunsNoLifecycleHook` — inactive workflow, `Deactivated` never called.

`go test ./internal/api/... ./internal/webhook/...` passes. `go vet ./internal/api/... ./internal/webhook/...` and `go build ./...` are clean.

Not re-tested here: that a successful `remove` clears the route's captured lifecycle state. That is `Coordinator.Deactivated`'s own behaviour, already covered by `internal/webhook/lifecycle_test.go`'s `TestCapturedValuesOutliveActivationUntilTheirRegistrationIsRemoved`, and reusing it at the handler level would mean duplicating that test's HTTP-stub and catalog wiring for no new coverage — this fix only makes `Delete` reach the same `Coordinator.Deactivated` call `Deactivate` already exercised.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-23.

- Base: `906ac77e` (last commit at or before ticket created 2026-09-23)
- Commits (1):
  - `c3cf620e` — BUG-d2t3kp: deleting an active workflow unregisters its trigger before it disappears
- Files changed (base → working tree):

```
 .pine/tickets/BUG-d2t3kp.md                    | 54 +++++++++++++++++
 internal/api/handlers/workflows.go             | 15 +++++
 internal/api/handlers/workflows_delete_test.go | 81 ++++++++++++++++++++++++--
 3 files changed, 145 insertions(+), 5 deletions(-)
```
