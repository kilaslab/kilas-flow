---
id: EPIC-brpz48
title: 'Credential security hardening: the 2026-09-25 audit and live test of the common credential types'
status: todo
priority: high
labels:
    - security
    - credentials
created: "2026-09-25T12:47:14Z"
updated: "2026-09-25T12:47:14Z"
---

# Description

A code audit and a live test of the credentials module (2026-09-25), covering the common credential types in use: httpBasicAuth, httpHeaderAuth, httpBearerAuth, httpQueryAuth, httpCustomAuth, jwtAuth, postgres, mysql, sqlite, telegramApi, wahaApi, openAiApi, openRouterApi, and the Google Drive and Gmail OAuth2 types.

**What holds**, checked against the code and confirmed live against a running binary:
- Secret fields are sealed with AES-256-GCM, a fresh nonce per seal. The live database file holds no plaintext.
- The API and the UI never return a secret. Every response shows the bullet mask, and the edit form's secret input starts empty.
- Writing the mask back keeps the stored secret.
- Tenant isolation holds at every repository query.
- Expressions and Code nodes cannot read a secret:
  - `$env` is prefix-limited;
  - `$credentials` exists only in routing templates;
  - `this.getCredentials` and `httpRequestWithAuthentication` are refused statically.
- n8n export carries no credential.
- Postgres and MySQL targets go through the egress policy.

**What does not hold** is in the children, in priority order.

# Children (priority order)

H2 → H1 → M1 → M2/M3 → M4 → SQLite (M5 plus the hang) → L1 → L2 → the low ones.

# Progress (2026-09-25)

**Fixed, each reviewed until clean, and merged to main.**
- BUG-g9zf51 and BUG-asdh5q: URL secrets were kept out of error text. `safehttp.RedactError` is used at every outbound call site, and the engine scrubs secrets from node errors and error items.
- BUG-0bzsa1: redirect scope.
  - A credential with no domains follows a redirect only to the same host.
  - No https→http step while a credential is attached.
  - Lifecycle requests and Telegram go through the engine path.
  - The chat model, embeddings, Vault and OAuth token calls carry the scope.
- BUG-cmnsfz: the verified webhook auth header is stored redacted.
- BUG-xkz7qx: re-pointing a credential is closed.
  - Default domains: openAiApi, openRouterApi and Google are fixed; telegramApi and wahaApi follow their own baseUrl.
  - The compiler refuses credential types a node does not declare.
  - Embed sessions and scoped keys may not run unscoped credentials; disabled nodes are exempt.
  - Found by e2e: a follow-up lets dialogs scroll.
- BUG-a7p6c8: the embed listing cursor.
- BUG-0grt9g: Google Connect.
  - A per-flow nonce cookie binds it to the browser.
  - PKCE S256.
  - The state is single-use across replicas.
- BUG-49vf3j: update keeps omitted fields and scope; create refuses the mask; only set secrets are masked.
- BUG-pder07: the unsaved-credential test.
  - It keeps a stored secret on its stored target and effective scope.
  - `credentialId` is validated before the claim.
  - A tenant runs at most 4 tests at once.
- BUG-xpr3jj: SQLite.
  - Paths are confined to `<sql.sqlite_root>/<tenant>/`.
  - Open is bounded by the caller's deadline.
  - The test claim release is idempotent.

**Live re-test on the final binary, against an echo stub, via the API and playwright-cli.**
- Every common type still authenticates correctly.
- No secret appears in an execution error, record or log. The only plaintext is a response body the stub itself reflected.
- The mask round-trip is safe.
- A cross-host redirect no longer carries the header.
- SQLite traversal and absolute paths are refused instantly.
- The stored-secret test exploit gets 422.
- An OpenAI key pointed elsewhere through baseUrl is refused, and the stub receives nothing.
- OAuth start sets an HttpOnly Lax nonce cookie and an S256 challenge, and a forged state is refused.
- The UI shows the default scopes and no secret appears in the DOM.

**Open (low):** FEAT-r87gtj (crypto hygiene), BUG-5dn8hr (database TLS defaults), BUG-argnka (nondeterministic credential resolution), FEAT-qae4sh (audit trail and delete-in-use), BUG-n4e97f (misleading boot warning about the key environment variables).

