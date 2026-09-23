---
id: BUG-vsmnby
title: 'Pack-trigger lifecycle hooks: values substituted without JSON escaping (injection), scalar-only params, no response capture'
status: doing
priority: high
labels:
    - saas
    - packs
    - webhooks
    - security
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T04:13:29Z"
---

# Description

- Lifecycle templates receive only scalar parameters (`internal/webhook/request_lifecycle.go:119-130`), so multi-select events, collections and conditions cannot be registered with the remote service.
- Values are substituted without JSON escaping (`request_lifecycle.go:211-222`).
- Response data such as a subscription id or a generated secret cannot be captured for later `remove` or verification.

# Acceptance Criteria
- [x] Structured parameters are available JSON-encoded.
- [x] Substitution in a JSON context escapes values.
- [x] `set` may declare `capture: { key: jsonPath }`. The captured values persist on the binding and are usable by `check`, `remove` and the HMAC secret lookup.
- [x] Tests cover injection attempts.

# Implementation Plan

## Progress — part 1 of 2: escaping and structured parameters (2026-09-23)

Criteria 1, 2 and 4 pass. Capture (criterion 3) is part 2, and the ticket closes after it.

- **Context-aware substitution** (`internal/webhook/request_lifecycle.go`). One pass, `expand`, shared by every context:
  - **URL** (`substituteURL`): a *data* field — the `Parameter` and `ParameterJSON` families, listed in `dataFamilies` so that captured values can join them — is written with `safehttp.PathSegment` in the path and `url.QueryEscape` after a `?`. A value of `.` or `..` is refused, because escaping leaves a dot segment unchanged and a proxy resolves it. `PublicURL`, `Route` and the credential fields stay raw: they are the addresses the request is built on.
  - **JSON body** (`substituteBody`, for a template starting with `{` or `[`): a placeholder inside a string has its value JSON-escaped. A placeholder outside a string is written as it is, and must be exactly one JSON value. A placeholder right after a backslash is refused. The rendered body must pass `json.Valid`, or nothing is sent.
  - **Headers** (`substituteHeader`): a value containing CR or LF is refused before the request is built.
- **Structured parameters**: `lifecycleFields` keeps the scalar `Parameter.<key>` fields and adds `ParameterJSON.<key>` for every parameter, lists and objects included.
- **Live WAHA injection fixed**: `WebhookListLifecycle.open` now renders the session path through `substituteURL`. A session of `../../admin?key=` used to send the GET and the PUT, with the tenant's API key, to `/api/sessions/../../admin?key=`.
- **Shared helper**: `safehttp.PathSegment`, which `routing.substitutePath` now uses too. Its behaviour there is unchanged.
- **Review fix, the authority**: a data field that would stand before the URL's path begins (scheme, userinfo, host or port) is now refused, and the error names the field but not the value. `PathEscape` leaves `@` and `:` alone, so `{{ .baseUrl }}{{ .Parameter.x }}` or `https://host:{{ .Parameter.port }}/…` with `@evil.example` sent the credentialed request to `evil.example`. `WebhookListLifecycle.open` now adds a missing leading slash to the session template before rendering, so a relative session path still renders as a path.

Tests:
- `internal/webhook/request_lifecycle_test.go`:
  - `TestRequestLifecycleKeepsAJSONBodyParameterInsideItsString`;
  - `…KeepsAURLParameterInsideItsSegment`, which covers the path, the query, and the value right after the authority;
  - `…RendersStructuredParametersAsJSON`;
  - `…RefusesARequestItCannotRenderSafely`, which covers a CRLF header, `@evil.example` where the host ends, `@evil.example` in the port, a dot segment, a bare non-JSON value, an escaped placeholder and an invalid body;
  - `…ReadsASessionPathWrittenWithoutALeadingSlash`.
- `packs/waha/waha_test.go`: `TestASessionNameCannotMoveTheRegistrationToAnotherEndpoint`.
- `internal/safehttp/safehttp_test.go`: `TestPathSegmentKeepsAValueInsideOneSegment`.
- Every package that depends on `internal/routing`, `internal/webhook` or `internal/safehttp` passes: 31 packages, `go test`.

## Progress — part 2 of 2: capture from the `set` answer (2026-09-23)

Criterion 3 passes, so all four do.

- **Manifest** (`internal/webhook/request_lifecycle.go`, `internal/nodepack/trigger.go`):
  - `RequestDescriptor.Capture` (`capture: {key: "data.id"}`) keeps values from the JSON answer.
  - `RequestLifecycle.Validate`, run by `Trigger.validate`, refuses `capture` on `check` or `remove`, a key a template cannot name, and a path that is empty or has an empty segment.
  - `SuccessJSONPath` now walks the same dotted-path reader, `jsonPathValue`.
- **Flow**:
  - `LifecycleContext.State` (`LifecycleStateStore`: `Load`/`Save`/`Clear`) is one route's state. `Coordinator.WithState` supplies it, scoped by tenant and route.
  - `Create` saves what `set` answered with, all or nothing. A missing, null, empty, or object/list value fails activation, and so does an answer that is not JSON. Numbers keep their digits (`UseNumber`). A trigger that captures, with no state to keep it in, is refused before anything is sent.
  - `check` and `remove` read the values as `{{ .Captured.<key> }}`. `Captured` is in `dataFamilies`, so a captured value is escaped exactly like a `Parameter`.
  - A descriptor naming a value that was never kept is not sent: `check` then reports "not registered", and `remove` has nothing to remove.
  - A successful `remove` clears the state. A failed one keeps it.
- **Persistence**:
  - Migration `000022_webhook_route_lifecycle_state` adds a nullable `lifecycle_state` column to `webhook_routes`: `blob` in SQLite, `bytea` in Postgres.
  - `GORMWorkflowStore.WithLifecycleState(cipher)` seals the values with the credential cipher. `LifecycleState`, `SaveLifecycleState` and `ClearLifecycleState` scope by tenant and route. Nothing is kept without the key.
  - `cmd/kilasflow/main.go` reuses the credential cipher instance, and gives the coordinator the state only when the key is set.
- **HMAC**:
  - `TriggerHMAC.SecretCapture` is an alternative to `SecretParameter`. Validation refuses both at once, and refuses a `secretCapture` that the lifecycle's `set` does not capture.
  - `Resolve` opens the route's state into `WebhookBinding.Captured`, and `secretOf` reads it from there.
  - State that cannot be opened (no key, or the wrong key) fails the lookup rather than routing the delivery unverified.

Tests:
- `internal/webhook/request_lifecycle_capture_test.go`:
  - `TestRequestLifecycleKeepsWhatSetAnsweredForCheckAndRemove`;
  - `…EscapesACapturedValueLikeAParameter`, which covers `/`, `?` and `@` in the path and the query, a dot segment, and the host position;
  - `…FailsARegistrationWhoseAnswerLacksACapture`;
  - `…SendsNothingThatNeedsAValueItNeverCaptured`;
  - `…RefusesToCaptureWithNowhereToKeepIt`;
  - `…ReadsANestedSuccessPath`.
- `internal/webhook/lifecycle_test.go`: `TestCapturedValuesOutliveActivationUntilTheirRegistrationIsRemoved`, which drives the coordinator through activate, deactivate with no remove, reactivate (the check finds the kept id), deactivate with remove (state cleared), and reactivate (registers anew).
- `internal/repository/webhook_state_test.go`, run on SQLite and on Postgres:
  - `TestCapturedLifecycleStateIsSealedOnTheRouteAndOutlivesItsBindings`;
  - `TestCapturedLifecycleStateIsNeverKeptOrReadWithoutItsKey`.
- `internal/nodepack/trigger_capture_test.go`:
  - `TestAManifestCapturesOnlyFromSetAndOnlyWhatItCanName`;
  - `TestAnHMACSecretCanBeAValueTheRegistrationCaptured`.
- `internal/database/webhook_route_lifecycle_state_migration_test.go`: `TestTheRouteLifecycleStateColumnComesAndGoesWithItsMigrationOn{SQLite,Postgres}`. `workflow_actor_migration_test.go` now rolls back by migration name (`rollBackBelow`).
- `go test ./...` passes. The Postgres-gated suites for `database`, `repository`, `tenantpurge` and `datastore` pass against pgvector/pg17.

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
