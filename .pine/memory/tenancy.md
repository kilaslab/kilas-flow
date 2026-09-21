---
topic: tenancy
updated: 2026-09-21T01:03:34Z
---

# tenancy

- 2026-09-21: Parallel tickets that add a tenant_id table must cover it in internal/tenantpurge in the same wave: TestEveryTenantTableIsPurgedOrExplicitlyExempt is schema-driven and fails the merged tree, not either branch.
