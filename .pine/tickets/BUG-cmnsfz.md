---
id: BUG-cmnsfz
title: Inbound webhook header auth persists the shared secret when the header has a custom name
status: todo
priority: low
labels:
    - security
    - credentials
    - webhooks
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

With a custom header name such as `X-Hook-Pass` (internal/webhook/webhook.go:647-653), the shared secret is kept in the trigger payload and shown in the execution view. Key-based redaction (internal/execution/redact.go:16-54) does not recognise the name.

# Acceptance Criteria
- [ ] The configured auth header is redacted by name before the payload is stored.
- [ ] A test covers it.
