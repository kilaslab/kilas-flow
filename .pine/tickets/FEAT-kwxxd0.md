---
id: FEAT-kwxxd0
title: Run a local Ollama model as the AI runtime for tests
status: todo
priority: high
labels:
    - e2e
    - testing
    - ai
deps:
    - FEAT-mvegj5
parent: EPIC-m42s3g
phase: p11
created: "2026-09-05T12:02:39Z"
updated: "2026-09-05T12:02:39Z"
---

## Scope

Every AI path in this product is currently untestable without somebody's API key and a network connection. `.pine/roadmap.md` lists an OpenRouter key under "Open items for the owner" for p5 testing, which is an honest admission that the AI tickets have no offline verification story. A test suite that needs a funded third-party account is a test suite that runs on one machine.

A local model closes that, and most of the work is already done. `internal/ai.NewOpenAICompatible(client, baseURL, apiKey)` takes an arbitrary base URL and defaults to `https://api.openai.com/v1` only when the value is empty. `chatModelNode()` in `nodes/ai.go` exposes `baseUrl` as a first-class parameter described as "Any OpenAI-compatible endpoint", with a default of the OpenAI URL, and its `model` picker loads options from `{baseUrl}/models` with `ItemsPath: "data"` and `ValueField: "id"`. Ollama serves an OpenAI-compatible API at `/v1` including `/v1/models` in exactly that shape, and its credential requirement here is `{Type: "httpBearerAuth"}` with no `Required: true`, so an unauthenticated local server needs no credential at all.

So no adapter, no new node and no new client are needed. What is needed is a small amount of configuration and one genuine obstacle.

**The SSRF guard blocks it, and that is correct behaviour.** `internal/config/config.go` defaults `OutboundHTTP.AllowPrivateNetworks` to `false`, and `internal/safehttp` rejects a resolved loopback address at dial time. A chat model node pointed at `http://localhost:11434/v1` is refused before a request is made. This is the guard working as designed — a tenant-supplied base URL reaching loopback is precisely the attack it exists to stop — so the fix is an explicit allowance for a known local endpoint, never turning the guard off.

That distinction is the load-bearing part of this ticket. `allow_private_networks: true` in a test configuration would make the AI suites pass while running the product with a security posture that does not match production, and would mean the suite could never catch a regression in the guard. An `allowed_hosts` entry for the model endpoint keeps the guard on and admits one address.

A 9B-class model is the right size: large enough to follow a tool-calling instruction reliably enough for a test to assert on structure, small enough to run on a developer laptop.

## Acceptance criteria

- [ ] A chat model node configured against a local Ollama endpoint completes a request end to end, with no third-party API key present in the environment.
- [ ] The model picker populates from the local server's `/v1/models`, proving the dynamic-options loader works against it rather than only against OpenAI.
- [ ] The endpoint is reached through an explicit `outbound.allowed_hosts` entry; `allow_private_networks` remains `false` in every configuration the suite uses.
- [ ] A test proves the guard still refuses a *different* loopback address, so the allowance is one host and not a hole.
- [ ] The model and its version are pinned, and a run against a different model is a visible configuration change rather than a silent behavioural difference.
- [ ] Tests assert on structure — that a tool was called, that a reply was produced, that an execution completed — never on the exact wording of generated text.
- [ ] A developer without Ollama installed gets a skipped suite with a message naming what to install, not a failure.
- [ ] The setup is documented well enough that a second machine reproduces it, including the model pull and roughly what it costs in disk and memory.

## Implementation Plan

Treat this as configuration and documentation, not as a code change, and resist the temptation to add an "Ollama node". The existing `baseUrl` parameter is the entire integration surface and a dedicated node would be a second code path for the same protocol.

The one code question worth settling is whether the SSRF allowance should be expressible cleanly. `outbound.allowed_hosts` exists and is the right mechanism; check whether it is consulted before or after the private-address check in `internal/safehttp`, because if the private-address guard runs unconditionally first then an allowlist entry for a loopback host cannot take effect and this ticket needs a small change to make the allowlist authoritative for exactly the hosts it names. Determine which it is before planning the rest — it changes this from a configuration ticket into a small code ticket.

For determinism, set temperature to zero and pin the model tag. That gets consistency of behaviour, not of text, which is why the assertions must be structural. A test asserting that a model replied with a particular sentence will fail on a model update and teach the team to distrust the suite.

Two operational notes worth writing down for whoever runs this. The first request after a model load is much slower than subsequent ones, so a suite with a per-test timeout tuned to warm performance will fail on the first test only — warm the model in the fixture. And a 9B model at typical quantisation needs several gigabytes of memory; a CI runner that cannot hold it is a reason to keep these suites on demand rather than on every pull request, which is a decision to record here rather than discover when the pipeline runs out of memory.

State plainly what this does and does not prove. It proves the wiring: that the node, the credential, the loader, the agent loop, the tool invocation and the execution record all work against a real OpenAI-compatible server. It does not prove behaviour against the models a customer will actually use, and no local suite can. Say so, so nobody reads a green suite as a quality claim about agent output.

## References

- Roadmap plan, p11 section, entry V2-p11-2: `.pine/roadmap.md`.
- `internal/ai/openai.go` — `NewOpenAICompatible`, the base-URL handling and the `/chat/completions` call.
- `nodes/ai.go` — `chatModelNode()`, the `baseUrl` parameter, the optional `httpBearerAuth` requirement, and the `/models` options loader with `ItemsPath: "data"` and `ValueField: "id"`.
- `internal/config/config.go` — `OutboundHTTP.AllowPrivateNetworks` defaulting to `false`, and `AllowedHosts`.
- `internal/safehttp/safehttp.go` — the loopback and private-address checks, and the order in which the allowlist is consulted.
- `internal/property/loader.go` — `LoaderHTTP` and `DependsOn`, which make the model picker re-query when the base URL changes.
- `.pine/roadmap.md` — "Open items for the owner", the OpenRouter key this ticket makes optional for testing.
- `.pine/tickets/FEAT-mvegj5.md` — V2-p5-1, the chat model nodes this exercises.
