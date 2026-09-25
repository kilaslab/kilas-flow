---
id: BUG-0bzsa1
title: Header-placed secrets follow a cross-host redirect, and lifecycle calls skip the host, redirect-scope and type checks
status: todo
priority: medium
labels:
    - security
    - credentials
    - redirects
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

**Cross-host redirects.** `CheckRedirect` stops a hop only when `scope.AllowsHost` is false (internal/safehttp/safehttp.go:391-395), and an empty domain list allows every host. Go drops only `Authorization` and `Cookie` when the host changes, so `X-Api-Key` (wahaApi, httpHeaderAuth) and custom headers reach any redirect target, including after an https→http downgrade.

Live evidence from 2026-09-25: an httpHeaderAuth request to a redirecting stub sent `X-Api-Key` on the first hop. The cross-host hop was refused only because the target was loopback.

**Lifecycle calls.** `call` in internal/webhook/request_lifecycle.go:473-497 applies the credential with no `AllowsHost` check and no `WithCredentialScope`. `lifecycleFields` (:442-461) and `telegramCredentialFor` (nodes/telegram_lifecycle.go:125-143) never check the credential's type.

# Acceptance Criteria
- [ ] With a credential scope present and an empty domain list, a redirect may only follow to the same host as the first request.
- [ ] A scheme downgrade is refused while a credential is attached.
- [ ] Lifecycle calls apply credentials through the same path the engine uses: host check, redirect scope, type check.
- [ ] Tests cover a cross-host redirect for header, custom and WAHA credentials, and a lifecycle call to a disallowed host.
