---
id: FEAT-knpfqf
title: Integrate external secrets managers
status: todo
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:16:26Z"
updated: "2026-09-05T05:16:26Z"
---

## Scope

Every secret in KilasFlow lives in one place and arrives by one route. `internal/config/config.go` exposes a single `Security.EncryptionKeyEnv`, and `cmd/kilasflow/main.go` reads the AES-256-GCM master key with `credentials.KeyFromEnvironment`, which is `os.Getenv` behind a length check. Credential values are sealed with that key into `credentialModel.Payload`, and `GORMCredentialStore.Resolve` is, by its own comment, "the single path by which a plaintext secret leaves storage". PRD §34 asks for more than that in one sentence — "Master key must come from environment or external secret manager" — and only the first half was ever built.

For the product's stated ICP this is a blocker rather than a nicety. A white-label host embedding KilasFlow into their SaaS already runs Vault, AWS Secrets Manager or their cloud's equivalent, has a rotation policy, and has auditors who ask where the keys are. Telling them the master key is an environment variable on the container, and that every downstream API token is a row in the workflow database, ends the conversation. It is also operationally worse than it looks: rotating a secret today means editing a credential through the API in every deployment that uses it, because nothing here can follow a reference.

This ticket adds two related but distinct mechanisms, and they must not be conflated. The first is the *master key source*: the key that opens the credential cipher may come from a secrets manager instead of the environment. The second is *credential values by reference*: a stored credential field may hold a pointer into a manager rather than a sealed secret, resolved on the way out at the moment the runtime needs it. The first is a boot-time concern with one consumer; the second is a per-execution concern with a cache, a failure mode and a tenancy rule.

Tenancy is the rule that shapes the second mechanism. `credentialModel` carries `tenant_id` and every repository call takes a `TenantScope`; an external reference must be resolved inside that scope, against a manager binding that belongs to that tenant. A provider configured once at the process level, with tenant-authored reference strings pointed at it, is a cross-tenant read waiting to happen.

## Acceptance criteria

- [ ] The credential master key can be sourced from an external secrets manager instead of `Security.EncryptionKeyEnv`, and the environment path keeps working unchanged for deployments that do not configure one.
- [ ] A manager that is configured but unreachable at boot refuses startup with a named error. It never degrades to the "no key configured" path, which disables credential storage.
- [ ] A credential field can hold a reference into a manager instead of a sealed value, and it is resolved on the existing `Resolve` path so no new route exists by which plaintext leaves storage.
- [ ] A manager binding belongs to a tenant, and a reference authored under one tenant cannot read a secret bound to another — proven by test.
- [ ] Resolved values are cached in memory with a bounded, configurable TTL, are never written to disk, and are dropped when the credential or the binding changes.
- [ ] A value fetched from a manager never reaches an execution record, an API response, or a log line — including when it is interpolated into an HTTP request body rather than a header.
- [ ] At least one provider ships end to end with a live integration test, and adding a second provider is a new file implementing the interface, not a change to the credential store.

## Implementation Plan

Add a `secrets` config section — one word, because `envKeyToPath` treats the first underscore as the section separator, the same rule that forced `outbound` rather than `outbound_http`. Then define the provider interface in a new `internal/secrets` package: fetch one secret by reference, report health, and nothing else. Keep it deliberately small; a provider that also lists, writes or rotates invites KilasFlow to become a secrets manager, which it should not.

Wire the master key first, because it has exactly one consumer and proves the provider interface end to end. `cmd/kilasflow/main.go` currently treats a missing key as a warning and runs with credential storage disabled — that behaviour is right and must be preserved for a deployment that configures nothing. It must not extend to a *configured* manager that is down: a transient network error at boot silently disabling every credential in the installation is far worse than refusing to start. Make the two cases distinguishable in code, not just in a log message.

Then references. Resolve them inside `GORMCredentialStore.Resolve`, after decryption and before the plaintext half is merged in, so the one documented exit path for secrets stays the one exit path. Add the tenant-scoped manager-binding model to `internal/repository/models.go`, and a cache in `internal/secrets` keyed by tenant and reference with a TTL from config.

The design decision worth settling here rather than discovering later: how a workflow refers to an external secret. n8n exposes a `$secrets.<provider>.<key>` expression root and restricts it to credential fields. KilasFlow could add a matching root to `rootValue` in `internal/expression/expression.go`, next to `$json`, `$env` and `$execution`. Recommend not doing that, at least not first. Put the reference in the credential, where it is resolved on a path that already exists and already governs who may read what; an expression root would let any workflow author interpolate an arbitrary secret into an HTTP body, and the redaction boundary would be the only thing standing between that and an execution record. If a `$secrets` root is added later for import compatibility, scope it to credential fields the way n8n does.

The provider question is a cost decision, so make it with evidence. Vault's KV v2 API is plain HTTP with a token header and needs no vendor SDK, so it can go through `internal/safehttp` and inherit the SSRF policy and egress control that everything else outbound already obeys. AWS Secrets Manager needs SigV4, which means either hand-rolled signing or the AWS SDK and the binary size that comes with it. Recommend shipping Vault first behind the interface, then deciding the cloud providers against a real request rather than in the abstract.

One trap to watch. `execution.Redact` catches values whose *key* looks sensitive or whose *shape* looks like a credential. A token fetched from a manager and placed into a request body under an innocuous field name matches neither test, so it will be persisted verbatim in the execution record. Cover this with a test before the feature is called done; the fix belongs with p1-6's redaction work, but the failure belongs to this ticket.

## References

- Plan: `~/.claude/plans/distributed-worker-nats-crispy-finch.md`, section p8, V2-p8-6.
- PRD: `gflow-prd-v1.md` §34 Credentials ("Master key must come from environment or external secret manager"), §57 Security Requirements, §64 ("secrets manager integrations").
- Code: `internal/config/config.go` (`Security.EncryptionKeyEnv`, the one-word section rule on `OutboundHTTP`), `cmd/kilasflow/main.go` (the `KeyFromEnvironment` switch and its "credential storage is disabled" branch, `workflowEnvironment`), `internal/credentials/credentials.go` (`KeyFromEnvironment`, `Cipher`, the closed `definitions` map), `internal/repository/credentials.go` (`GORMCredentialStore.Resolve`), `internal/repository/models.go` (`credentialModel`), `internal/expression/expression.go` (`rootValue` and the supported roots), `internal/execution/redact.go`, `internal/safehttp/safehttp.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 17 — External Secrets sits beside SSO and LDAP in the settings sidebar. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
