---
id: BUG-asdh5q
title: a lifecycle request's transport error logs the full URL
status: todo
priority: low
labels:
    - security
    - webhooks
parent: EPIC-7c3ry9
created: "2026-09-23T07:35:21Z"
updated: "2026-09-23T07:35:37Z"
---

# Description

Found by the 2026-09-23 stabilisation sprint review.

`call` (internal/webhook/request_lifecycle.go ~473-499) performs a trigger's lifecycle HTTP request (registering/unregistering a webhook with a customer's service). A transport failure from `http.Client.Do` inside it is Go's `*url.Error`, which formats as `<verb> "<full URL>": <cause>` — the full request URL, not just its scheme and host.

`Deactivated` (internal/webhook/lifecycle.go ~227-229) logs that error at Warn with `"error", err`, which serializes the `*url.Error`'s full string, URL included. The same transport error also becomes the detail of the 502 `Activated` returns on registration failure (internal/webhook/lifecycle.go ~202, `"... did not finish registering with its service (%s): %w"`, surfaced as `huma.Error502BadGateway`).

A lifecycle target URL can carry a captured value or a credential placed directly in the URL (the doc comment on `call` notes Telegram's setWebhook wants its token in the URL). Logging or returning the full URL on a transport error would expose that.

# Steps to Reproduce

1. Configure a trigger lifecycle whose target URL embeds a secret (e.g. a token in the path or query, as Telegram's setWebhook does).
2. Force a transport failure (unreachable host, TLS failure, timeout) during activation or deactivation.
3. Inspect the Warn log line, or the 502 response body from activation.

# Expected

A lifecycle transport error's log line and its 502 activation detail carry only the target's scheme and host, never the full URL (path, query, or anything captured/embedded in it).

# Actual

The full `*url.Error` string, including the complete URL, is logged at Warn (`Deactivated`) and returned as the 502 activation detail (`Activated`).

# Acceptance Criteria
- [ ] Lifecycle transport errors are stripped to scheme and host before being logged
- [ ] The same stripped form is used for the 502 activation detail
- [ ] A test asserts a URL carrying a token/secret does not appear in the logged error or the 502 detail after a forced transport failure

# Related Files

internal/webhook/request_lifecycle.go `call`, ~473-499 (the outbound request; the source of the `*url.Error`)
internal/webhook/lifecycle.go `Deactivated`, ~214-231 (logs the transport error at Warn, ~227-229)
internal/webhook/lifecycle.go `Activated`, ~169-211 (the same transport error becomes the 502 activation detail, ~202)
