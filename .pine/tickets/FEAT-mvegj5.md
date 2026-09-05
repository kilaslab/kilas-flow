---
id: FEAT-mvegj5
title: Add OpenAI and OpenRouter chat model nodes
status: done
priority: high
labels:
    - ai
    - parity
deps:
    - FEAT-whn5vb
    - FEAT-2f68r8
parent: EPIC-m42s3g
phase: p5
created: "2026-09-05T05:01:31Z"
updated: "2026-09-05T16:44:32Z"
---

## Scope

There is one model node. `chatModelNode()` in `nodes/ai.go` registers `kilasflow.chatModel`, displayed as "OpenAI Chat Model", whose provider is a free-text `baseUrl` parameter defaulting to `https://api.openai.com/v1`. n8n has a distinct node type per provider, so an import cannot map 1:1 onto a node whose identity is a URL string: two different n8n types would collapse into one KilasFlow type and an export could not tell them apart again. Split it into OpenAI and OpenRouter as separate node types over the existing `ai.OpenAICompatible` adapter — OpenRouter is a base-URL change (`https://openrouter.ai/api/v1`) and its own credential type, nothing more.

Three defects are live in the current node and must be fixed here rather than carried into two nodes.

**Token usage is always zero on a streamed run.** The `stream` parameter defaults to `true`, and `chatRequest` in `internal/ai/openai.go` has no `stream_options` field, so `include_usage` is never sent. `Stream` reads `chunk.Usage`, which the provider only emits when asked for it, so `assembled.Usage` stays zero — and `AgentExecutor.Execute` writes those zeros straight into the output item's `usage` object. Every cost report built on that number is wrong by default.

**`temperature: 0` cannot be expressed.** `executeChatModel` writes the descriptor key only `if temperature := numberValue(ir.Parameters["temperature"]); temperature != 0`, and `AgentExecutor.Execute` reads it back under the same guard. Zero — the one value a user reaches for when they want deterministic extraction — is indistinguishable from unset.

**The model call is capped at 30 seconds with no override.** `NewAgentExecutor` builds its client with `safehttp.NewClient(policy)`, and `internal/safehttp/safehttp.go` sets `Client.Timeout` from `policy.Timeout`, whose default is `30 * time.Second`. The HTTP Request node can lower that per node through `timeoutSeconds` (`nodes/http.go`, clamped downward only); a chat model has no node-level control at all, so a long completion dies as a transport error rather than a model error.

Per-provider credentials need p2-6: `internal/credentials/credentials.go` holds a closed package-level `definitions` map with three types, and `validateChatModelConfiguration` today requires a generic `httpBearerAuth`.

## Acceptance criteria

- [~] Two node types register — OpenAI and OpenRouter — each with its own credential type and its own default base URL. **The import/export mapping is not done**: `internal/interop/` was off-limits this session and has no AI type table at all. The node types are named so the mapping is the identity on the last segment. See Work evidence.
- [x] Both nodes expose the shared option set under identical names: `temperature`, `maxTokens`, `topP`, frequency penalty, presence penalty, `timeout` and `maxRetries`.
- [x] `temperature: 0` reaches the provider; a parameter left blank sends nothing at all rather than a default.
- [x] A streamed run reports non-zero token usage matching the provider's own figures, because `stream_options.include_usage` is sent whenever streaming.
- [x] A model call may exceed the 30-second outbound default up to a configured ceiling, and a request beyond that ceiling is refused with a named error rather than silently clamped.
- [x] Model names populate from p2-4's load-options endpoint, and the field stays free-text when the provider cannot be reached.
- [x] The SSRF policy and the per-credential domain scope still apply to every model call, proven by a test that points a model node at a private address and is refused.

## Implementation Plan

Start in `internal/ai/openai.go`, because both new nodes sit on it. Add `stream_options` to `chatRequest` and send `{"include_usage": true}` only when `stream` is set — sending it on a non-streaming request is a provider error on some OpenAI-compatible endpoints. Then `nodes/ai.go` for the two definitions and their validators, then `nodes/executors.go` for the executor ids.

The temperature fix is about presence, not value. `ai.ModelRequest.Temperature` is already a `*float64`, so the type can carry "unset" — the bug is entirely in the descriptor round trip, where `numberValue` collapses absent and zero into the same `0`. Have `executeChatModel` consult the raw parameter map for presence and write the key whenever the parameter exists, and have the agent read it back the same way. Add a test with `temperature: 0` explicitly set, since that is the case every existing test misses.

The timeout is the fiddly part. `http.Client.Timeout` is a ceiling a `context` deadline can only lower, never raise, so a per-node timeout above 30 seconds cannot be reached through the shared `safehttp` client as it stands. Two options: build a second client for model endpoints with its own policy, or keep one client with `Policy.Timeout` unset for the model path and enforce the bound with a per-call `context.WithTimeout` in the executor. Recommend the second — one client means one dialer, and the dialer is where the SSRF re-check on every redirect hop lives; duplicating the client duplicates the security policy and gives a future change two places to be wrong.

The outbound policy for model endpoints and for self-hosted Ollama is a decision this ticket must settle rather than leave. The SSRF guard refuses private and loopback addresses, and Ollama lives at `http://localhost:11434` — precisely such an address. Recommend keeping the refusal by default and documenting the deployment-level allowance through `Policy`, rather than carving an exception into node code: a policy exception in configuration can be audited, one compiled into a node cannot.

## References

- Roadmap plan, p5 section, entry V2-p5-4: `.pine/roadmap.md`.
- `.pine/roadmap.md` — p2 entries V2-p2-4 (load options) and V2-p2-6 (credential registry).
- `internal/ai/openai.go` (`chatRequest`, `Stream`, `post`), `nodes/ai.go` (`chatModelNode`, `executeChatModel`, `AgentExecutor.Execute`), `internal/safehttp/safehttp.go` (`Policy.Timeout`, `NewClient`), `nodes/http.go` (`timeoutSeconds`), `internal/credentials/credentials.go`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 05 — the OpenRouter Chat Model NDV: credential, notice, model dropdown, options collection. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.

## Work evidence

### Stale premises found while verifying the ticket

- **The credential paragraph was out of date.** "`internal/credentials/credentials.go` holds a closed package-level `definitions` map with three types" describes code that no longer exists. FEAT-2f68r8 (V2-p2-6) landed and is `done`: the registry is now an open `credentials.Registry` in `internal/credentials/registry.go`, built once at package init from `RegisterAll` in `internal/credentials/builtin.go`, which already held **eight** types. Adding `openAiApi` and `openRouterApi` was therefore two entries in `builtin.go` and no registry work at all.
- **`nodes/http.go` has no `timeoutSeconds`.** The per-node HTTP timeout was renamed to `requestTimeoutSeconds`; `timeoutSeconds` survives only as `legacyTimeoutKey` in `nodes/core.go:274`, read as a fallback by `timeoutParameter`. The mechanism the ticket points at is real, under a different name.
- **Acceptance criterion 1 cannot be met in full from this ticket's scope.** It asks for "a 1:1 mapping from the matching `@n8n/n8n-nodes-langchain` type on import and export". `internal/interop/n8n/n8n.go` has **no AI type mapping at all** — its table covers `n8n-nodes-base.*` only, so every `lmChatOpenAi` still imports as `kilasflow.unsupported` (only the `ai_*` *connections* are mapped, by FEAT-afs850). `internal/interop/` was explicitly off-limits for this session. What this ticket delivered is the half it owns: two node types that a 1:1 mapping can name, deliberately named `kilasflow.lmChatOpenAi` and `kilasflow.lmChatOpenRouter` so the mapping is the identity on the last segment. **A follow-up in `internal/interop/n8n` is still required to close AC1.**
- Everything else in the ticket verified as written: all three defects were live and reproduced, `chatModelNode()`, `executeChatModel` and `AgentExecutor.Execute` were where the ticket said, and `safehttp.NewClient` did set `Client.Timeout` from `Policy.Timeout`.

### What was built

- **Two provider node types**, `kilasflow.lmChatOpenAi` and `kilasflow.lmChatOpenRouter`, over the existing `ai.OpenAICompatible` adapter, each with its own required credential type (`openAiApi`, `openRouterApi` — n8n's IDs) and its own default base URL. Both are built from one `chatModelProvider` description, so the two definitions cannot drift apart.
- **`kilasflow.chatModel` is kept and renamed** to "OpenAI-Compatible Chat Model". The type string is untouched, so no saved workflow notices, and it stays the node for an endpoint with no vendor to name — which is what FEAT-kwxxd0 (Ollama) depends on.
- **`model` is a resource locator**, `list` (loaded from `{baseUrl}/models` through p2-4) plus a free-text `id` mode. A plain `options` field has no free-text escape in `property-field.svelte` — a failed load leaves an unusable empty `<select>` — so the locator is what makes AC6's "stays free-text when the provider cannot be reached" true rather than aspirational. It is also n8n's own shape for `lmChatOpenAi` at 1.2+.
- **An `options` collection** with n8n's member names verbatim: `temperature`, `maxTokens`, `topP`, `frequencyPenalty`, `presencePenalty`, `timeout`, `maxRetries`. Transcribed into `nodes/testdata/n8n_chat_model_options.json` with its source file and read date, in the shape `n8n_sql_options.json` established; a test compares the declaration against that record rather than against the reference checkout, which no build input may read.
- **Defect 1 — streamed usage.** `chatRequest` gained `stream_options`, sent as `{"include_usage": true}` only when `stream` is set.
- **Defect 2 — `temperature: 0`.** Fixed as presence rather than value on both sides of the descriptor, and fixed on the existing `chatModel` node too rather than carried into two more. `ModelRequest` gained `TopP`, `FrequencyPenalty` and `PresencePenalty` as `*float64` for the same reason.
- **Defect 3 — the 30-second cap.** Took the ticket's recommended option: the model client is `safehttp.NewClient(policy)` with `Policy.Timeout` zeroed and nothing else changed, so the dialer, its per-hop `CheckAddress`, and `CheckRedirect` all still close over the same `Policy` value — one security policy, one dialer. The bound is a per-run `context.WithTimeout` in the executor. A node that names no timeout gets the deployment's own outbound default, so nothing saved earlier changes behaviour.
- **The ceiling** is `DefaultModelTimeoutCeiling` (10 minutes), overridable per deployment through `nodes.WithModelTimeout`. Above it the node is refused with `ErrModelTimeoutAboveCeiling`, never clamped.
- **`maxRetries` is honoured, not merely declared.** Retrying lives in `OpenAICompatible.post`, the last point at which nothing has been consumed — a stream that has already emitted chunks cannot be replayed. Only 429 and 5xx are retried.
- **Two security gaps closed on the model path.** A model's base URL now gets the same `policy.CheckURL` pre-flight an HTTP node's does, and the credential's own `AllowedDomains` now bind the call — previously a key scoped to `api.openai.com` could be pointed anywhere by editing one parameter. The rule is reused through a new `engine.Credential.AllowsHost`, so `Authenticate` and the model path share one implementation.

### The outbound-policy decision this ticket had to settle

Kept the refusal. `safehttp`'s address check runs in the dialer on every connection and is not consulted alongside `AllowedHosts` — an allowlist entry does **not** exempt a host from the private-address guard, so `Policy.AllowPrivateNetworks` is the only lever, and it is a deployment-level configuration flag that can be audited. No exception is compiled into any node. `TestAModelCallToAPrivateAddressIsRefused` pins this against a policy with the product's real posture. (This also answers the open question in the roadmap's V2-p11-2 entry: the allowlist is consulted at `CheckURL`, the private-address guard at dial time, and they are independent — so Ollama on loopback needs `allow_private_networks`, not an `allowed_hosts` entry.)

### Tests, and proof they would fail without the change

New tests, each reverted in turn to confirm it fails without its fix and then restored:

| Revert | Failing test(s) |
| --- | --- |
| stop sending `stream_options` | `TestAStreamedRequestAsksForTokenUsageAndAPlainOneDoesNot`, `TestAStreamedRunReportsTheProvidersOwnTokenUsage` |
| `attempts := 1` | `TestARateLimitedModelRequestIsSentAgainUpToTheRetryBound` |
| restore the `!= 0` temperature guard | `TestATemperatureOfZeroReachesTheProvider` (`request temperature = <nil>, want an explicit 0 on the wire`) |
| leave `Policy.Timeout` on the model client | `TestAModelCallMayOutlastTheDeploymentsOutboundTimeout` (`Client.Timeout exceeded while awaiting headers`) |
| drop the credential domain-scope check | `TestACredentialScopedToOneHostCannotBeSentToAnother` |
| unregister the two provider nodes | `TestEachProviderChatModelIsItsOwnNodeTypeWithItsOwnCredentialAndAddress`, `TestTheChatModelOptionsMatchWhatTheReferenceWasRecordedAsSaying`, `TestAProviderChatModelRefusesACredentialItCannotUse`, `TestAnOptionTheUserNeverAddedIsNotSentAtAll` |

Also added: `TestASamplingOptionTheCallerLeftUnsetIsNotSentAtAll`, `TestARejectedModelRequestIsNotSentAgain`, `TestAModelTimeoutAboveTheCeilingIsRefusedRatherThanClamped`. Every model call in every test goes to a local `httptest` server; nothing reaches a provider.

### Commands

- `go build ./...` — clean.
- `go vet ./...` — clean.
- `gofmt -l .` — empty outside `web/`.
- `go test ./... -count=1` — every package `ok`, including `internal/guardrails` (licence boundary) and `internal/interop/n8n`.
- `go test ./internal/ai/ ./internal/credentials/ ./internal/engine/ -race -count=1` — all `ok`.
- `go test ./nodes/ -race -count=1` — fails only on `TestTheGoCodeNodeRunsOncePerItemWhenAsked`, the pre-existing BUG-9s3htg wasm compile that outruns its ceiling under the race detector. No other race failure introduced.

### Follow-up left open

- **AC1's import/export half.** `internal/interop/n8n/n8n.go` needs two entries mapping `@n8n/n8n-nodes-langchain.lmChatOpenAi` → `kilasflow.lmChatOpenAi` and `.lmChatOpenRouter` → `kilasflow.lmChatOpenRouter`, plus parameter translation for the `options` collection and n8n's `model` resource-locator shape. Deliberately not done here: `internal/interop/` was off-limits for this session.
- **`responseFormat` (JSON mode) was not adopted.** `ai.ModelRequest` has no way to carry it, and declaring an option that is collected and never sent is worse than not offering it. Recorded under `_unadopted` in the fixture with the reason.
