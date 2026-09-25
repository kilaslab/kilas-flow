---
id: BUG-49vf3j
title: Credential update wipes the fields and domain scope the caller does not send, and create stores the bullet mask as a secret
status: todo
priority: low
labels:
    - credentials
    - api
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

- **Update drops omitted fields** (internal/repository/credentials.go:115-123). This silently clears optional secrets: jwtAuth's secret and privateKey, Google's clientSecret and refresh_token.
- **Update widens an omitted scope.** A missing `allowedDomains` becomes "unrestricted" (:140-147).
- **The docs disagree.** The OpenAPI summary says "any field sent", which implies unsent fields are kept.
- **Create stores the mask.** It stores the bullet placeholder literally as the secret.
- **The mask shows for unset fields.** `Redacted` shows bullets for optional secrets that were never set.

# Acceptance Criteria
- [ ] An omitted secret field and an omitted `allowedDomains` (nil) are kept; an explicit empty list clears the scope.
- [ ] Create refuses the placeholder.
- [ ] `Redacted` shows the mask only for fields that are set.
- [ ] Tests cover each case, and the OpenAPI descriptions say what happens.
