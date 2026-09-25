---
id: BUG-0grt9g
title: 'Google OAuth connect: the state is not bound to the browser that started it, and there is no PKCE and no single use'
status: todo
priority: medium
labels:
    - security
    - credentials
    - oauth
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

`SignOAuthState` (internal/credentials/oauth.go:84-99) HMACs only `{tenantId, credentialId, origin, exp}`, and the callback (internal/api/handlers/oauth.go:128-178) needs no session or cookie.

**Exploit:** an attacker in tenant A starts a connect on their own credential and sends the authorize URL to a victim. The victim consents to the platform app, which asks for gmail.modify or drive, and the victim's tokens are stored in the attacker's credential. The state can also be replayed for 10 minutes, and there is no PKCE.

# Acceptance Criteria
- [ ] Start sets a random nonce as a SameSite=Lax HttpOnly cookie, and the nonce is carried inside the signed state. The callback verifies it.
- [ ] A state is single-use.
- [ ] PKCE (S256) is used.
- [ ] Tests cover a callback from another browser, a replay, and a missing verifier.
