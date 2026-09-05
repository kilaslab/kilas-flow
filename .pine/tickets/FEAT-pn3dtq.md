---
id: FEAT-pn3dtq
title: Add secure credentials, safe expressions, and HTTP Request node
status: done
priority: critical
labels:
    - credentials
    - expressions
    - http
    - security
deps:
    - FEAT-3mady6
    - FEAT-f681vt
parent: EPIC-c7gbdp
phase: p2
created: "2026-08-29T15:41:06Z"
updated: "2026-09-05T01:42:00Z"
---

## Scope

Complete the first end-to-end automation slice safely: encrypted credential management, a non-JavaScript expression subset, and an HTTP Request node. Reuse the core registry/runtime/editor instead of creating special paths.

## Acceptance criteria

- Credential CRUD stores encrypted payloads using a configured master key; plaintext credential values never enter workflow JSON, API list responses, logs, or execution records.
- Credential ownership/type and allowed HTTP-domain scope are validated before node execution. The HTTP node has request/payload/time limits and an SSRF policy that rejects disallowed private/internal targets according to documented policy.
- Fixed and expression parameter values are represented explicitly; the evaluator supports the approved V1 context (`$json`, `$input`, `$node`, `$env`, `$execution`) and rejects arbitrary JavaScript/code execution.
- HTTP Request is a registered node with metadata-driven parameter UI, credential selection, expression preview/error feedback, and deterministic request/response item mapping.
- A running end-to-end test proves Manual Trigger → Set → HTTP Request, including expression resolution, success/error persistence, redaction, credential non-disclosure, and SSRF-denial coverage.

## References

- PRD: §§24 Core nodes, 33–34, 36 Credentials, 49–50, 57; Milestone 2 and §65 first slice.
- Design reference: `11-ndv-http-request-params.png`, `12-canvas-wired-manual-set-http.png`, `15-param-fixed-expression-toggle.png`, `16-expression-editor.png`, `18-credentials-list.png`, `22-credential-modal-basic-auth.png`, `23-credential-saved.png`.

## Relevant documentation

- Consult current Go crypto and HTTP guidance only through their official docs if a non-standard API/library is chosen.
- Use `find-docs` before library-specific SSRF, encryption, request-client, or Svelte Flow API decisions; record links/versions in work evidence.

## Relevant skills

- `pine`, `find-docs`, `test-driven-development`, `systematic-debugging`, `verification-before-completion`.
- `web-design-guidelines`, `playwright-cli` for the credential/editor browser flows.

## Implementation Plan

### Explicit parameter values

- A parameter is fixed unless it is the object `{"mode":"expression","value":"…"}`. Making the marker part of the value keeps "is this an expression?" answerable from the document alone, with no per-node convention and no `=`-prefix ambiguity against strings that genuinely start with `=`.
- `expression.Resolve` walks a parameter map and evaluates every marker it finds, so nested key/value and list parameters get expressions for free.

### Evaluator

- `internal/expression` parses `{{ … }}` segments over a restricted grammar: a root (`$json`, `$input`, `$node`, `$env`, `$execution`, `$itemIndex`) followed by `.field`, `["field"]`, and `[0]` accessors. Anything else — calls, operators, bare identifiers — is a parse error, so a tenant-authored parameter can never become code.
- A template that is exactly one expression yields that value with its type intact; a template with surrounding text yields a string.

### Credentials

- `internal/credentials`: AES-256-GCM `Cipher` keyed from the environment variable named by `Security.EncryptionKeyEnv`, a type catalogue (`httpBasicAuth`, `httpHeaderAuth`, `httpBearerAuth`) whose field metadata drives the UI, and `Apply` to attach auth to an outbound request.
- Stored records keep the payload encrypted; `AllowedDomains` scopes where a credential may be sent. API responses carry field metadata and non-secret values only — secret fields are never returned after creation.

### HTTP Request node and SSRF policy

- `internal/safehttp` builds a client whose dialer re-checks the resolved IP of every connection, including each redirect hop, against loopback, private, link-local, unique-local, and multicast ranges. Checking at dial time rather than parsing the URL is what closes DNS rebinding.
- `nodes/http.go` registers `kilasflow.httpRequest` with metadata-driven parameters, bounded timeout, response size, and redirect count, and maps responses to items deterministically.

### Engine wiring

- `engine.Request` gains the execution identity, node-output lookup, environment allowlist, and credential resolver an executor needs to evaluate expressions and authenticate, so nodes never reach into storage themselves.

## Work Evidence

- Expressions: `internal/expression` parses `{{ … }}` over a restricted data-access grammar. `TestEvaluateRejectsAnythingThatIsNotDataAccess` proves that `process.exit(1)`, `require('fs')`, `fetch(...)`, operators, statement separators, `this`, and `globalThis` are all parse errors — the evaluator has no code path that could execute them.
- Explicit modes: only `{"mode":"expression","value":"…"}` is evaluated. A fixed string containing `{{ }}` stays literal data, proven both in Go (`TestResolveEvaluatesOnlyExplicitlyMarkedParameters`) and in the editor (`parameter.test.ts`), so client and server apply the same rule.
- Credentials: AES-256-GCM with a fresh nonce per seal; a tampered ciphertext, a wrong key, and a truncated payload all fail to open. The stored SQLite row was read back directly during the live run and held ciphertext (`CB51FAC5…`) plus `{"name":"X-Api-Key"}` — only the non-secret half is plaintext, which is what lets a listing avoid the master key entirely.
- Non-disclosure: create, list, and get return `••••••••` for secret fields. Re-sending that placeholder on update keeps the stored secret rather than blanking it.
- SSRF: `internal/safehttp` re-checks the resolved IP at dial time and on every redirect hop, which is what closes DNS rebinding rather than only parsing the URL. Loopback, unspecified, RFC 1918, link-local (including 169.254.169.254), CGNAT, reserved, multicast, unique-local, NAT64, and IPv4-mapped IPv6 are all blocked, and non-HTTP schemes never leave the check.
- Live end-to-end against a running API and a local echo target: Manual Trigger → Set → HTTP Request succeeded with `$json.customer` resolving the path to `/users/ada` and `$env.REGION` plus `$json.status` resolving the query to `region=eu-west-1&status=ready`. The echo response proved the credential header was applied, and the persisted execution recorded it as `"apiKey": "[redacted]"` — the secret never reached the execution record.
- SSRF denial and credential scope were both proven live: the same workflow under the default policy failed with `request target is not allowed: loopback address 127.0.0.1`, and the same credential aimed at `localhost` failed with `credential "Echo API" is not allowed for host "localhost"`.
- The `outbound` config section is deliberately one word: `envKeyToPath` treats the first underscore as the section separator, so an `outbound_http` section could never be reached by an environment override.
- `go test ./...`, `go vet ./...`, `pnpm test` (37 passing), `pnpm check` (0 errors), `pnpm generate:api:check`, `pnpm build`, `make smoke-sqlite`.
