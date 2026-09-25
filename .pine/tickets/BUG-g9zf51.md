---
id: BUG-g9zf51
title: A secret placed in a URL leaks through transport-error text into execution records, error items, logs and API answers
status: todo
priority: high
labels:
    - security
    - credentials
    - redaction
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

Go's `*url.Error` prints the whole request URL, including the path and the query. Several outbound call sites wrap that error raw, so a credential placed in the URL is written wherever the error goes.

Two credential types put their secret in the URL:
- `httpQueryAuth` puts it in the query.
- `telegramApi` puts it in the path, as `/bot<token>/`.

The raw wraps are:
- the HTTP node, nodes/http.go:329-331;
- the routing interpreter, internal/routing/executor.go:251-253 and :529-531;
- loadoptions, internal/loadoptions/loadoptions.go:265-270;
- wasm packs, internal/wasmpack/hostcalls_http.go:204-237 (`sanitizedFailure` strips userinfo only);
- Telegram polling, nodes/telegram_lifecycle.go:171-173 and :290, which logs `"error", err` at Warn on every transport failure;
- request lifecycles, which BUG-asdh5q covers.

The error is then stored as-is:
- as `{"message": err.Error()}` in internal/engine/service.go:1110-1116, which key-based redaction does not touch;
- as a data item on error outputs, internal/engine/runner.go:1605-1620, so a "notify on failure" branch sends the secret on to Slack or email.

# Steps to Reproduce (live, 2026-09-25)

1. Attach an `httpQueryAuth` credential to an HTTP Request node pointing at a closed port. The execution error reads `Get "http://127.0.0.1:18999/closed?api_key=<secret>": …`.
2. Run a Telegram sendMessage whose `baseUrl` is a closed port. The execution error reads `Post "http://127.0.0.1:18999/bot<token>/sendMessage": …`.

# Acceptance Criteria
- [ ] One helper, for example in safehttp, rewrites any `*url.Error` to scheme://host with the path and query withheld. Every outbound call site above uses it.
- [ ] Node errors and error items are scrubbed of the resolved credential's secret values before they are persisted or emitted. `ScrubResolved` exists and nothing calls it.
- [ ] Tests reproduce both live cases and assert that no secret reaches the execution record, the error item or the log.
- [ ] BUG-asdh5q, the lifecycle path, is closed by the same change.
