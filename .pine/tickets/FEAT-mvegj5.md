---
id: FEAT-mvegj5
title: Add OpenAI and OpenRouter chat model nodes
status: todo
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
updated: "2026-09-05T05:01:31Z"
---

## Scope

There is one model node. `chatModelNode()` in `nodes/ai.go` registers `kilasflow.chatModel`, displayed as "OpenAI Chat Model", whose provider is a free-text `baseUrl` parameter defaulting to `https://api.openai.com/v1`. n8n has a distinct node type per provider, so an import cannot map 1:1 onto a node whose identity is a URL string: two different n8n types would collapse into one KilasFlow type and an export could not tell them apart again. Split it into OpenAI and OpenRouter as separate node types over the existing `ai.OpenAICompatible` adapter — OpenRouter is a base-URL change (`https://openrouter.ai/api/v1`) and its own credential type, nothing more.

Three defects are live in the current node and must be fixed here rather than carried into two nodes.

**Token usage is always zero on a streamed run.** The `stream` parameter defaults to `true`, and `chatRequest` in `internal/ai/openai.go` has no `stream_options` field, so `include_usage` is never sent. `Stream` reads `chunk.Usage`, which the provider only emits when asked for it, so `assembled.Usage` stays zero — and `AgentExecutor.Execute` writes those zeros straight into the output item's `usage` object. Every cost report built on that number is wrong by default.

**`temperature: 0` cannot be expressed.** `executeChatModel` writes the descriptor key only `if temperature := numberValue(ir.Parameters["temperature"]); temperature != 0`, and `AgentExecutor.Execute` reads it back under the same guard. Zero — the one value a user reaches for when they want deterministic extraction — is indistinguishable from unset.

**The model call is capped at 30 seconds with no override.** `NewAgentExecutor` builds its client with `safehttp.NewClient(policy)`, and `internal/safehttp/safehttp.go` sets `Client.Timeout` from `policy.Timeout`, whose default is `30 * time.Second`. The HTTP Request node can lower that per node through `timeoutSeconds` (`nodes/http.go`, clamped downward only); a chat model has no node-level control at all, so a long completion dies as a transport error rather than a model error.

Per-provider credentials need p2-6: `internal/credentials/credentials.go` holds a closed package-level `definitions` map with three types, and `validateChatModelConfiguration` today requires a generic `httpBearerAuth`.

## Acceptance criteria

- [ ] Two node types register — OpenAI and OpenRouter — each with its own credential type, its own default base URL, and a 1:1 mapping from the matching `@n8n/n8n-nodes-langchain` type on import and export.
- [ ] Both nodes expose the shared option set under identical names: `temperature`, `maxTokens`, `topP`, frequency penalty, presence penalty, `timeout` and `maxRetries`.
- [ ] `temperature: 0` reaches the provider; a parameter left blank sends nothing at all rather than a default.
- [ ] A streamed run reports non-zero token usage matching the provider's own figures, because `stream_options.include_usage` is sent whenever streaming.
- [ ] A model call may exceed the 30-second outbound default up to a configured ceiling, and a request beyond that ceiling is refused with a named error rather than silently clamped.
- [ ] Model names populate from p2-4's load-options endpoint, and the field stays free-text when the provider cannot be reached.
- [ ] The SSRF policy and the per-credential domain scope still apply to every model call, proven by a test that points a model node at a private address and is refused.

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
