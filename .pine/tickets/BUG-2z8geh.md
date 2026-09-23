---
id: BUG-2z8geh
title: 422 validation errors echo the whole request body, including credential secrets, into the response/envelope
status: doing
priority: high
labels:
    - cli
    - security
    - api
parent: EPIC-8rbys7
created: "2026-09-23T03:06:00Z"
updated: "2026-09-23T04:47:38Z"
---

# Description

A failed `credential create` prints the submitted token or password back into the CLI envelope. That output lands in terminals, CI logs and agent transcripts.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: cli-skills; finding ids: CLI-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n8n's public API returns `{"message":"request/body must have required property 'data'"}` without echoing values.

# Steps to Reproduce

1. `kilasflow api create-credential --body '{"name":"x","type":"httpHeaderAuth","data":{"name":"X-Api-Key","value":"s3cr3t-cli-skills"}}'`. 2. `… --body '{"name":"x","type":"httpBearerAuth","secret":{"token":"TOPSECRET-123"}}'`.

# Expected

The problem document never echoes body values for secret-bearing operations, or the CLI redacts `fields`, `token`, `password` and `value` before printing.

# Actual

Exit 2, and `error.detail.problem.errors[].value` holds the entire body, `"value":"s3cr3t-cli-skills"` / `"TOPSECRET-123"` included, twice per response. It lands in the agent's transcript and in MCP tool results, while the credentials skill's non-negotiable 1 says a secret "never appears in … chat". A field-level error (`allowedDomains` wrong type) echoes only that field. The leak is when the location is `body`.

# Acceptance Criteria
- [x] Problem documents never echo body values for secret-bearing operations (credentials, auth, API keys)
- [x] The CLI also redacts `fields`, `token`, `password` and `value` before printing, as defence in depth
- [x] A test posts an invalid credential and asserts the secret does not appear in the response

# Implementation Plan

Strip `value` from huma validation errors on credential routes, or globally when location == `body`. Extend the CLI's redactor to problem `errors[].value`.

# Notes

Related (from the audit): none

# Related Files

cs/leak1.json, cs/b-cred-out.json (first attempt, in the transcript).

# Attachments

## Progress

API side. `internal/api/problems.go` wraps `huma.NewErrorWithContext` once per
process (`installProblemRedaction`, a `sync.Once`, called first thing in
`NewServer`), which is the constructor huma uses for a validation or parse
failure. Each detail's value is dropped when its location is `body` (the raw
bytes of a body that does not parse), when it is an object or a list (huma's
missing- and unexpected-property errors carry the whole surrounding object),
or always on an operation registered with `Metadata["sensitiveBody"] = true`
(`handlers.SensitiveBodyKey`): create-credential, update-credential,
test-credential-payload, login, create-tenant-user, set-tenant-user-password,
create-api-key and create-tenant-api-key. Messages and locations are kept.
Problems a handler builds itself (compile issues, idempotency, the datastore
conflict) never pass through that constructor and keep their values.

Tests in `internal/api/problems_test.go`:
`TestAnInvalidCredentialIsNeverEchoedIntoTheProblem` posts the audit's two
bodies and a malformed-JSON body and asserts neither secret is anywhere in
the response; `TestASecretBearingOperationEchoesNoValueAtAll` sends a scalar
secret to each marked operation; `TestAProblemKeepsAFieldsValueButNeverTheWholeBody`
pins that an unmarked operation still names the refused field's own value.
All three failed with the leak before the fix and pass after; the full
`internal/api/...` suites pass, and `make generate-api-reference-check`
reports no drift (Metadata is not part of the document).

CLI side (defence in depth against a server from before the fix, or a proxy).
`redactProblem` in `internal/cli/client.go` now always decodes the problem
(with `UseNumber`, so surviving values are carried exactly). An `errors[]`
value at the location exactly `body` is dropped whatever its type: that is
where huma put both the whole object and the raw body string. Any other
`errors[]` value is kept but key-redacted at any depth with the credential
keys plus `fields` and `value`, and the rest of the document with the
credential keys plus `fields`. The client's own token is still redacted
wherever it appears. Neither server-built problem an agent is told to read
uses location `body`: compile issues are `body` + a non-empty JSON pointer
(every `ValidationError` carries a `Path`), and idempotency uses
`header.Idempotency-Key`. So their `{code,nodeId,connectionId}` and `{code}`
values come through whole.

Tests: `TestAPINeverCarriesAnEchoedSecretInTheProblem`
(`internal/cli/verbs_api_test.go`) drives `api create-credential` and reads
the printed envelope. A whole credential and a raw body at `body` lose their
value, a field-level object loses `value`/`Token`/`fields`/`PASSWORD`/`api_key`
but keeps its other keys, and a compile issue and an idempotency code stay
intact. `TestMCPToolResultNeverCarriesAnEchoedSecret`
(`internal/cli/mcp_test.go`) asserts the same through an MCP tool result.
The leak cases failed before the change and pass after.
`TestProblemDocumentsAreRedactedBeforeTheyAreCarried` still passes. The CLI
reference's envelope section no longer says the problem is carried verbatim.
