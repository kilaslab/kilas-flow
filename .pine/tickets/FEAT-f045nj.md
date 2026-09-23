---
id: FEAT-f045nj
title: 'Tenants, provisioning and identity: operator key, tenant/user/key via SDK and curl, scoped keys, auth modes, SSO stance'
status: todo
priority: medium
labels:
    - docs
    - tenancy
    - auth
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

The recipe for provisioning tenants exists only in an example README. The embedding guide says keys are "minted at the store layer", which is false, and scoped API keys are documented only in the CLI reference.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-6, DOC-19, DOC-26). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

# Findings

## DOC-6: Operator key and tenant provisioning are not documented on the site, and the embedding guide says the first tenant key is "minted at the store layer"

*docs · high · tenancy*

**Steps to reproduce:**

1. `guides/embedding.md:44-48` says "tenant rows are operator-managed … and the first key per tenant is minted at the store layer, because key creation is scoped to the caller's own tenant."
2. Live on :18190 with `KILASFLOW_AUTH_OPERATOR_KEY` set: `POST /api/v1/tenants` → 201, then `POST /api/v1/tenants/docs-saas-acme/api-keys` → 201 with a `kfa1_…` token. A customer key calling `GET /api/v1/tenants` → 403.
3. `grep -rn OPERATOR_KEY docs/src/content/docs` finds only `operate/tenant-deletion.md:93-96`. The required key shape (`kfa1_<12 hex>_<base64url secret>`, refused at boot otherwise) is documented only in `sdk/examples/host-page/README.md:15-20` and `sdk/examples/reference-host/README.md:48-55`. `operate/configuration-reference.md:377-387` (`auth.operator_key_env`) does not mention the shape.

**Actual:**

An integrator reading only the site cannot provision a tenant, and the embedding guide tells them it cannot be done through the API.

**Expected:**

A "Provision tenants" page covering generating the operator key, the boot-time registration log line, tenant then user then key, the fact that operator routes require `auth.enabled`, and rotation.

**Suggested fix:**

Write the page, and correct `embedding.md:44-48` and the operator checklist (`embedding.md:389-406`), which omits the operator key.

**Evidence:**

the steps above. The operator flow was verified live (see `evidence/kf-auth-server.log`, lines "registered the operator API key" and "POST /api/v1/tenants/default/api-keys status=201").

**Related:**

FEAT-frvez8 (done)


## DOC-19: Scoped API keys (scopes, workflowId, expiresAt) are documented only inside the CLI reference

*gap · medium · auth*

**Steps to reproduce:**

1. `CreateAPIKeyInputBody` accepts `scopes` (the five embed scopes), `workflowId` and `expiresAt`. Live on :18190, a tenant key minted a key with `scopes:["datastore:read"]` → 201.
2. `grep -rn -i "scoped key\|agent token\|tenant-wide key" docs/src/content/docs` (excluding generated pages) finds only `reference/cli.md` (the guardrails context).

**Actual:**

Least-privilege backend keys, for example a key that only reads one table, are an invisible feature.

**Expected:**

An "API keys and scopes" page covering tenant-wide versus scoped keys, the workflow binding, expiry, rotation and revocation, 401 versus 403, and a table of which operations each scope allows.

**Suggested fix:**

Write the page as part of the identity docs.

**Evidence:**

the steps above.

**Related:**

none


## DOC-26: No statement on SSO or OIDC

*gap · low · auth*

**n8n:** n8n offers SAML/OIDC SSO on paid tiers.

**Steps to reproduce:**

1. `grep -rli "oidc\|saml\|openid" internal` → nothing. `grep -rn -i "sso\|single sign" docs/src/content/docs` → nothing.

**Actual:**

An integrator evaluating "auth/SSO" gets no answer.

**Expected:**

The identity page says there is no SSO, dashboard login is email and password, and in the embed model the host's own authentication is the authentication.

**Suggested fix:**

One paragraph on the identity page.

**Evidence:**

the steps above.

**Related:**

none


# Acceptance Criteria
- [ ] A tenant, its first user and a scoped API key can be provisioned from the docs alone (the commands are run in CI or verified)
- [ ] A scope → operation table for API keys, with rotation guidance
- [ ] An explicit statement on SSO/OIDC (not supported today), with the recommended pattern (the host authenticates and mints sessions)
- [ ] The "store layer" claim is removed

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-frvez8

# Related Files

# Attachments
