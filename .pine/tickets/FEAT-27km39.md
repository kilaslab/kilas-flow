---
id: FEAT-27km39
title: Write the operator guide and generate the configuration reference
status: todo
priority: high
labels:
    - docs
    - platform
deps:
    - FEAT-nxxbs5
    - FEAT-m94hhx
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:54:47Z"
updated: "2026-09-05T11:54:47Z"
---

## Scope

`internal/config/config.go` defines a `Config` struct with **ten** sections: `server`, `database`, `security`, `outbound`, `webhook`, `embed`, `branding`, `execution`, `binary`, `log`. `config.example.yaml` documents **seven**. The three it omits are `outbound`, `webhook` and `embed`.

Two of the three missing sections are the product's security boundary.

`outbound` is `OutboundHTTP` — `allow_private_networks` defaulting to false, `allowed_hosts`, `max_redirects`, `max_response_bytes` and `timeout`. It is the SSRF egress policy applied to every workflow HTTP request through `internal/safehttp`. An operator reading the example file has no way to learn it exists, let alone that it has a default worth reviewing.

`embed` is `signing_key_env`, `allowed_origins` and `session_ttl`. `allowed_origins` is the allowlist that decides which pages may host the editor, and it **fails closed**: empty means embedding is disabled entirely. An operator who has read the example file and is trying to make embedding work has nothing to find.

This is not a one-off omission to patch; it is a class of defect that will recur every time the struct gains a field, because the example file is maintained by hand and by memory. The example has already drifted by three whole sections. The fix is to stop hand-maintaining it.

Several more configuration facts live only in source and only in comments. `envKeyToPath` treats the first underscore in an environment variable name as the section separator, so a section name must be a single word — which is why `OutboundHTTP` is keyed `outbound` and not `outbound_http`, and it is a trap for anyone adding a section. `KILASFLOW_WORKFLOW_ENV_*` is a deliberately separate prefix so a workflow expression can never read the DSN or the master key. `KILASFLOW_ENCRYPTION_KEY` and `KILASFLOW_EMBED_SIGNING_KEY` are both optional at boot: a missing key logs a warning and disables the feature rather than refusing to start, which is right for a first run and is how an operator ends up with credential encryption silently off in production.

There is also nothing that tells an operator how to run this thing responsibly: no deployment topology, no statement of what the PostgreSQL tier unlocks, no backup or restore procedure, no upgrade path, and no security posture in one place. `FEAT-a94c8y` already carries the most important of those as an acceptance criterion — that a table prefix is a naming convention and not an isolation boundary, and that a dedicated schema plus a restricted role is the only configuration that genuinely isolates — but it is one line in one ticket rather than a page an operator will read.

## Acceptance criteria

- [ ] The configuration reference is generated from `internal/config/config.go`, so a new field appears in the documentation without anyone remembering to add it, and a section can never again be absent.
- [ ] The generated reference is checked in the V2-p10-1 pipeline, and a struct change with no regenerated reference fails the run.
- [ ] Every key documents its type, its default, its environment-variable name and whether it is required, including all ten sections.
- [ ] `config.example.yaml` is either generated from the same source or removed in favour of the reference, so the repository never again holds two disagreeing descriptions of the same struct.
- [ ] The single-word section rule imposed by `envKeyToPath` is documented where somebody adding a section will see it.
- [ ] The boot behaviour for a missing encryption key and a missing embed signing key is documented, including exactly what capability is silently lost in each case.
- [ ] Deployment topologies are documented — single container with SQLite, container with external PostgreSQL, and shared-customer-database with the `kflow_` prefix — with what each one gives up.
- [ ] The security posture is one page: the SSRF policy, the embed origin allowlist, the internal-database guard, credential sealing, and the statement that a table prefix is a naming convention and not an isolation boundary.
- [ ] Backup, restore and upgrade are documented for both drivers, including what to do about the data volume before pulling a newer image.

## Implementation Plan

Generate the reference; do not write it. A small Go program that walks the `Config` struct by reflection, reads the `koanf` tags and the doc comments, and emits Markdown into the docs site is perhaps a hundred lines, and it is the only version of this that stays true. Emit it into `docs/` and check it in CI the way the API client is checked — regenerate, compare, fail on difference — reusing the pattern `web/scripts/check-api-client.mjs` already establishes.

That approach needs the doc comments on the config structs to be good, since they become user-facing text. Several already are. Reading them with that in mind, and improving the ones that are terse, is part of this ticket rather than a follow-up.

Then decide what happens to `config.example.yaml`. Two workable answers and one bad one. Generating it from the same walk keeps a copy-and-edit starting point, which operators genuinely want. Deleting it in favour of the reference is simpler and removes the drift surface entirely. The bad answer is keeping it hand-maintained beside a generated reference, which reproduces exactly the divergence this ticket exists to close. Recommend generating it, with a header saying it is generated.

For the operator prose, write from what the smoke scripts already prove rather than from intent — `scripts/smoke-postgres.sh` is a working description of the PostgreSQL topology, and `scripts/smoke-docker.sh` encodes the volume-permission behaviour a bind-mount user will hit.

Be careful to document the PostgreSQL tier as it is on the day of writing, not as p6 will leave it. `database.max_open_conns` defaults to 1 for PostgreSQL too while `execution.max_concurrent` defaults to 10, and V2-p6-3 exists because that makes every current concurrency claim a no-op. Say what is true today and link the ticket that changes it; a documentation ticket that describes an unbuilt future in the present tense is worse than one that describes a limitation.

One judgement to record explicitly: the shared-customer-database topology is the one this product's ICP most wants and the one with the sharpest failure mode. `FEAT-a94c8y` and `FEAT-k65hqv` between them settle that the prefix is not a boundary and that a dedicated schema with a restricted role is. That has to be the recommendation on the page, stated plainly, not a caveat at the bottom.

## References

- Roadmap plan, p10 section, entry V2-p10-12: `.pine/roadmap.md`.
- `internal/config/config.go` — the ten-section `Config` struct, `EnvPrefix`, `envKeyToPath` and the single-word rule at line 81, and the koanf precedence chain.
- `config.example.yaml` — the seven sections it documents, against the struct's ten.
- `cmd/kilasflow/main.go` — `workflowEnvironment`, `databaseGuard`, `outboundPolicy`, and the warn-and-continue behaviour for absent keys.
- `internal/safehttp/safehttp.go` — what the `outbound` section actually governs.
- `internal/embed/embed.go` — `allowed_origins` failing closed, and the session lifetime cap.
- `internal/database/database.go` — the SQLite connection pin, the DSN pragmas, and `Migrate`.
- `scripts/smoke-postgres.sh`, `scripts/smoke-docker.sh` — working descriptions of two topologies.
- `.pine/tickets/FEAT-a94c8y.md` — the prefix-is-not-a-boundary statement this page must carry.
- `.pine/tickets/FEAT-gvn62x.md` — V2-p6-1, versioned migrations, which the upgrade page depends on and must not pre-announce as done.
- `web/scripts/check-api-client.mjs` — the generate-and-compare pattern the reference check should copy.
