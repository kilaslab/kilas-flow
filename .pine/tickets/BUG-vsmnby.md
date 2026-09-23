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
- [ ] `set` may declare `capture: { key: jsonPath }`. The captured values persist on the binding and are usable by `check`, `remove` and the HMAC secret lookup.
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

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
