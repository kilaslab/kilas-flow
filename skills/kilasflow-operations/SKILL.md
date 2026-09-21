---
name: kilasflow-operations
description: Use when operating an installation rather than a workflow — roles, readiness, migrations, backups, restores, upgrade order, the operator credential, or offboarding a tenant. Triggers on "deploy", "upgrade", "migrate", "migration", "backup", "restore", "ready", "health", "tenant", "operator key", "offboard".
kilasflow_skills_version: 1
kilasflow_commands:
  - kilasflow context
  - kilasflow health
  - kilasflow ready
  - kilasflow version
  - kilasflow auth whoami
  - kilasflow tenant list
  - kilasflow tenant get
  - kilasflow tenant users
  - kilasflow api
kilasflow_operations:
  - get-health
  - get-ready
  - get-me
  - list-tenants
  - get-tenant
  - list-tenant-users
  - delete-tenant
  - create-api-key
  - list-api-keys
  - revoke-api-key
kilasflow_nodes: []
kilasflow_expression_roots: []
kilasflow_not_shipped:
  - 'No skills verbs: kilasflow skills list, show, install, check and export do not ship in this bundle yet'
  - 'No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet'
  - 'No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch'
---

## Non-negotiables

1. Read the installation before you change it. `kilasflow context` is one read-only briefing — server, identity, workflows, datastores, node types — and it is the only cheap way to learn what you are pointed at. `kilasflow health` (`get-health`) is liveness and touches no dependency; it answers `200` through a database outage. Readiness is a different question and has its own verb.
2. Never route traffic on liveness. `kilasflow ready` (`get-ready`) answers `503`, which the CLI carries back as exit code 6, when the database is unreachable or when a datastore is behind the schema version this build serves. That exit code means wait, then retry; it does not mean the process is broken.
3. Nothing migrates on demand. Schema migrations are applied at boot, and a failed migration is a container that does not start rather than a server running against the wrong schema. There is no migrate verb to reach for, so an upgrade is: back up, start the new build, watch the first boot, then check readiness.
4. Never offboard a tenant to test something. The `delete-tenant` operation is irreversible, runs on the operator credential alone, and there is no export-before-delete and no soft delete. Reach it as `kilasflow api delete-tenant --path id=<tenantId>` and only on an explicit instruction; a customer's own valid key gets `403` and cannot even learn whether the tenant exists.

## Strong defaults

- Begin with `kilasflow version`, then `kilasflow context`. `kilasflow version` prints the binary's version and, when a server is configured, the version the server reports; an unreachable server is information here, not a failure.
- The identity is the authority. `kilasflow auth whoami` (`get-me`) prints the tenant, the scopes, the workflow binding and the expiry of the configured credential, and never the credential itself. A key with scopes is an agent token; an empty scope set is the legacy tenant-wide key.
- The operator surface is three reads. `kilasflow tenant list` (`list-tenants`), `kilasflow tenant get <tenantId>` (`get-tenant`) and `kilasflow tenant users <tenantId>` (`list-tenant-users`) all need a key scoped to the operator tenant; anything else is refused with `403` and exit code 3, and the refusal is the server's, not a filtered listing.
- One binary, three shapes. `--role both` (the default) runs API, workers and scheduler in one process; `--role api` serves the API and the scheduler with no workers; `--role worker` runs workers alone. SQLite refuses a split role at startup, because one file and one writer is a corruption story rather than a scaling story; a split deployment means PostgreSQL.
- Keys are minted and revoked through the escape hatch today: `kilasflow api create-api-key --body @key.json` (`create-api-key`), `kilasflow api list-api-keys` (`list-api-keys`) and `kilasflow api revoke-api-key --path id=<keyId>` (`revoke-api-key`). The body carries scopes and an optional workflow binding; the value comes back once and is never readable again. A scoped key may not mint another key — that answers `403` with `scope_denied` — so minting stays a tenant-wide or operator act.
- Back up before every upgrade, per driver: SQLite is the data directory plus its `-wal`/`-shm` siblings, copied after a checkpoint or with the process stopped and restored with the container user's ownership; PostgreSQL is a dump taken with the database's own tools against the pinned pgvector major, restored into a server that can provide the same extensions.
- An upgrade is ordered, not concurrent: back up, confirm a bind mount is writable by the container user, start the new build, watch the first boot apply the SQL migrations and then the per-datastore pass, and check readiness before sending it traffic. See references/UPGRADE_ORDER.md.
- Switching drivers is not an upgrade path. There is no migration between SQLite and PostgreSQL: the new side comes up empty, so anything worth keeping is exported first.

## Decision tree

```
what are you doing?
|
+-- answering "is it up?"
|     -> kilasflow health          liveness, no dependency
|        kilasflow ready           readiness: 503 = wait (exit 6)
|        the 200 body carries the datastore schema-version spread
|
+-- upgrading a build
|     -> back up (see references/UPGRADE_ORDER.md)
|        start the new build and watch the first boot:
|          SQL migrations, then the per-datastore pass, then the listener
|        kilasflow ready before traffic
|
+-- a datastore is behind the build
|     -> nothing to run: boot repeats the pass every 30 seconds
|        a datastore AHEAD of the build refuses the boot and names both versions
|
+-- minting or revoking a key
|     -> kilasflow api create-api-key / list-api-keys / revoke-api-key
|        scopes: workflow:read, workflow:write, workflow:run,
|                datastore:read, datastore:write
|
+-- offboarding one customer
|     -> kilasflow api delete-tenant --path id=<tenantId>   ONLY on instruction
|        it is irreversible and converges: send it again and read the counts
|
+-- a node type says "unavailable"
      -> the deployment cannot run it (no Go toolchain for kilasflow.code,
         no Node for the sidecar, no credential for a SQL node)
         the catalogue reports it before the run, not at run time
```

## Not shipped yet

- No skills verbs: kilasflow skills list, show, install, check and export do not ship in this bundle yet — the bundle is written and gated in the repository, and `kilasflow help` lists no `skills` path, so a harness installs the files by hand until that ticket lands.
- No guarded write verbs: credential create, update and delete, datastore create, rename, delete, columns and clear, schedule create, update and delete, pack install, and tenant create and delete have no verbs yet — every one of those operations is served, so reach it with `kilasflow api <operation-id>`, `--path` for a placeholder and `--body @file.json` for a body, and treat the missing `--yes` guard as your own responsibility to ask the user first.
- No tenant api-keys verb: create-api-key, list-api-keys and revoke-api-key are served and reached through the escape hatch — the design lists a `tenant api-keys` verb and it is deliberately absent, because the only key operation under a tenant path mints a credential for somebody else, and listing the caller's own keys is the flat `list-api-keys` operation.

## Anti-patterns

- "Health is green, so send traffic" → liveness touches no dependency, so it stays green through a database outage and through a datastore migration that has not finished → gate on `kilasflow ready` (`get-ready`) and treat exit code 6 as wait-and-retry.
- "The upgrade failed, so I'll run the migration by hand" → there is no migrate verb and no supported partial state: migrations run at boot, SQL first and then the per-datastore pass, and a failure stops the container → read which step failed, fix the data or roll back to the backup, and start the build again.
- "I'll delete the tenant and recreate it to clean it up" → the deletion is irreversible, there is no export-before-delete, and it takes the tenant's datastore tables with it → export what the customer needs first, and repeat the delete only to converge a partial run.
- "The delete answered 500, so it stopped half-way and is broken" → every step only deletes what is still there, the tenant is locked out from the first step, and a repeat finishes the job → send it again and read the counts; `tenantRemoved: false` with zeros is the confirmation.
- "The tenant is gone, so nothing of it survives" → backups, logs, already-issued embed sessions, secrets in an external manager and a worker still mid-run are all outside the deletion → handle those beside it when a regulator asks.
- "Two workers on one SQLite file will scale it" → the split role is refused at startup and the driver holds one writer → run multiple workers against PostgreSQL, or scale the box.
- "I'll restore the dump into a stock PostgreSQL image" → migration `000006_vector_store` creates the `vector` extension and the dump carries that statement → restore into the pinned `pgvector` image, or install the extension first.
- "The webhook address is the path label" → a binding is matched only by its minted route, and a backfilled binding's address changed → read the address with `kilasflow workflow get <workflowId>`'s webhook list, re-activate any trigger that registers its own address, and never hard-code `/webhook/<label>`.
- "The agent's token is fine, I'll widen it" → an agent token is an API key with scopes and the server enforces them per operation, so a scope the workflow needs is the answer, not a tenant-wide key → mint the narrow key, keep the operator key for tenant routes, and read `kilasflow auth whoami` when the refusal does not explain itself.

## Reference files

| File | Read when |
| --- | --- |
| UPGRADE_ORDER.md | you are backing up, upgrading, restoring or switching drivers, or offboarding a tenant and need the order the boot and the deletion really run in |
