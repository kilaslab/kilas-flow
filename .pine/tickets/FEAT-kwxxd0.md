---
id: FEAT-kwxxd0
title: Run a local Ollama model as the AI runtime for tests
status: done
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
updated: "2026-09-05T19:28:47Z"
---

## Scope

Every AI path in this product is currently untestable without somebody's API key and a network connection. `.pine/roadmap.md` lists an OpenRouter key under "Open items for the owner" for p5 testing, which is an honest admission that the AI tickets have no offline verification story. A test suite that needs a funded third-party account is a test suite that runs on one machine.

A local model closes that, and most of the work is already done. `internal/ai.NewOpenAICompatible(client, baseURL, apiKey)` takes an arbitrary base URL and defaults to `https://api.openai.com/v1` only when the value is empty. `chatModelNode()` in `nodes/ai.go` exposes `baseUrl` as a first-class parameter described as "Any OpenAI-compatible endpoint", with a default of the OpenAI URL, and its `model` picker loads options from `{baseUrl}/models` with `ItemsPath: "data"` and `ValueField: "id"`. Ollama serves an OpenAI-compatible API at `/v1` including `/v1/models` in exactly that shape, and its credential requirement here is `{Type: "httpBearerAuth"}` with no `Required: true`, so an unauthenticated local server needs no credential at all.

So no adapter, no new node and no new client are needed. What is needed is a small amount of configuration and one genuine obstacle.

**The SSRF guard blocks it, and that is correct behaviour.** `internal/config/config.go` defaults `OutboundHTTP.AllowPrivateNetworks` to `false`, and `internal/safehttp` rejects a resolved loopback address at dial time. A chat model node pointed at `http://localhost:11434/v1` is refused before a request is made. This is the guard working as designed — a tenant-supplied base URL reaching loopback is precisely the attack it exists to stop — so the fix is an explicit allowance for a known local endpoint, never turning the guard off.

That distinction is the load-bearing part of this ticket. `allow_private_networks: true` in a test configuration would make the AI suites pass while running the product with a security posture that does not match production, and would mean the suite could never catch a regression in the guard. An `allowed_hosts` entry for the model endpoint keeps the guard on and admits one address.

**The model is pinned to `gemma4:12b-mlx`**, by the owner's instruction, pulled with `ollama pull gemma4:12b-mlx`. Two consequences follow from that exact tag and both need recording rather than discovering.

The `-mlx` suffix names an Apple MLX build, so the tag is Apple-Silicon-only. A `linux/amd64` CI runner cannot pull it. That is not an obstacle to this ticket — it reinforces two decisions already made, that these suites run on demand rather than on every pull request and that a machine without the model skips cleanly — but it does mean the suite must not assume the model is reachable anywhere the rest of CI runs. Name a portable fallback tag for any non-macOS runner, or state plainly that the AI suites are macOS-only and let the skip path carry the rest.

And a 12B model at typical quantisation wants meaningfully more memory than the 9B class originally sketched here — enough that a laptop running it alongside a KilasFlow instance, a browser and a database is the real constraint on how many of these tests run in parallel. Bound the AI suites' concurrency explicitly rather than inheriting the harness default.

One thing to verify before building anything on top: that this model reliably emits OpenAI-format tool calls. The entire agent suite in V2-p11-6 depends on it, and a model that chats well but does not call tools would make that ticket unbuildable as written. Check it first, with a single hand-run request, and record the answer here.

## Acceptance criteria

- [x] A chat model node configured against a local Ollama endpoint completes a request end to end, with no third-party API key present in the environment.
- [x] The model picker populates from the local server's `/v1/models`, proving the dynamic-options loader works against it rather than only against OpenAI.
- [~] ~~The endpoint is reached through an explicit `outbound.allowed_hosts` entry~~; `allow_private_networks` remains `false` in every configuration the suite uses. **Amended** — `allowed_hosts` cannot do this and never could; see the amendment below. The endpoint is reached through a new `outbound.allowed_private_endpoints` entry, and `allow_private_networks` is `false` everywhere in this suite.
- [x] A test proves the guard still refuses a *different* loopback address, so the allowance is one host and not a hole.
- [x] The model is pinned to `gemma4:12b-mlx` in the suite's configuration, its tool-calling behaviour is verified and recorded before the suite is built on it, and a run against any other model is a visible configuration change rather than a silent behavioural difference.
- [x] Tests assert on structure — that a tool was called, that a reply was produced, that an execution completed — never on the exact wording of generated text.
- [x] A machine without Ollama, or without the pinned model, gets a skipped suite naming the exact `ollama pull` command, not a failure — including a runner whose architecture cannot serve an `-mlx` build at all.
- [x] The setup is documented well enough that a second machine reproduces it, including the model pull and roughly what it costs in disk and memory. Written as the package comment on `nodes/ai_ollama_test.go`, beside the code it describes, rather than in `docs/` — which was out of scope this session and now carries a stale claim; see Follow-ups.

## Implementation Plan

Treat this as configuration and documentation, not as a code change, and resist the temptation to add an "Ollama node". The existing `baseUrl` parameter is the entire integration surface and a dedicated node would be a second code path for the same protocol.

The one code question worth settling is whether the SSRF allowance should be expressible cleanly. `outbound.allowed_hosts` exists and is the right mechanism; check whether it is consulted before or after the private-address check in `internal/safehttp`, because if the private-address guard runs unconditionally first then an allowlist entry for a loopback host cannot take effect and this ticket needs a small change to make the allowlist authoritative for exactly the hosts it names. Determine which it is before planning the rest — it changes this from a configuration ticket into a small code ticket.

For determinism, set temperature to zero and pin the model tag. That gets consistency of behaviour, not of text, which is why the assertions must be structural. A test asserting that a model replied with a particular sentence will fail on a model update and teach the team to distrust the suite.

Two operational notes worth writing down for whoever runs this. The first request after a model load is much slower than subsequent ones, so a suite with a per-test timeout tuned to warm performance will fail on the first test only — warm `gemma4:12b-mlx` in the fixture before the first assertion. And a 12B model needs several gigabytes of resident memory; a runner that cannot hold it is the reason these suites stay on demand rather than on every pull request, and the reason their parallelism is bounded separately from the rest of the harness.

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
- Owner instruction, 2026-09-05: the end-to-end Playwright suites use `gemma4:12b-mlx`, pulled locally with `ollama pull gemma4:12b-mlx`.

## Amendment (2026-09-06) — this was a code ticket, and three premises in the body are false

**1. `outbound.allowed_hosts` cannot admit a loopback address, and the body assumes throughout that it can.** Verified against `internal/safehttp/safehttp.go`: `Policy.CheckURL` reads `AllowedHosts` at pre-flight and never looks at an IP; `Policy.CheckAddress` runs inside `NewClient`'s `DialContext` on every connection and returns early only on `Policy.AllowPrivateNetworks`, never reading `AllowedHosts`. The two guards are independent and neither consults the other, so the implementation plan's open question — "before or after" — has no answer in those terms. It is *neither*, which is exactly the condition the plan said would make this a code ticket.

**2. The `httpBearerAuth` requirement is not optional.** The body says the credential requirement carries no `Required: true`, "so an unauthenticated local server needs no credential at all". The declaration is indeed unflagged, but `executeChatModel` refuses a node whose `Credentials[BearerCredentialType]` is empty, and `validateChatModelConfiguration` refuses the same at save time with "an httpBearerAuth credential holding the API key is required". A local Ollama therefore needs a credential — one holding an **empty token**, which `internal/ai` turns into no `Authorization` header at all. That is what the suite uses, and the acceptance criterion about no third-party key still holds.

**3. "Treat this as configuration and documentation, not as a code change."** Not available, for reason 1. What was avoided is the thing that instruction was protecting against: no Ollama node was added, no exception was compiled into any node, and `internal/ai` and the node definitions are untouched.

## Work evidence

### The lever, and why it is shaped this way

`safehttp.Policy` gained `AllowedPrivateEndpoints []string` — exact `host:port` entries that may resolve to a private address while the guard stays on for everything else. Wired as `outbound.allowed_private_endpoints`.

Every constraint on it is a refusal:

- **The port is part of the grant.** `127.0.0.1:11434` admits the model server and not the SSH daemon or the database on the same box. An entry with no port is refused rather than read as "any port".
- **No wildcards**, even though `AllowedHosts` honours a `*.` prefix. `*.internal:11434` would be a licence to sweep a network, which is the thing the field exists to avoid. An operator reaching for the familiar syntax is told no rather than quietly given something narrower or wider than they asked for.
- **A malformed entry matches nothing**, so a typo costs the allowance rather than widening it — and `config.Validate` refuses it at startup through `safehttp.CheckPrivateEndpoint`, so it is never silently ignored. The grammar lives once, in `safehttp`, because the way two copies drift is an operator being told their exemption is well formed by a checker that is not the one deciding whether to honour it.
- **It grants nothing at pre-flight.** `CheckURL` is untouched, so a non-empty `allowed_hosts` still has to name the host too, and a redirect away from the endpoint is dialled — and refused — on its own address.
- **The host is compared as the URL wrote it**, never as it resolves, so an entry for `localhost:11434` does not admit `127.0.0.1:11434`.

`Policy.CheckAddress(ip)` is unchanged and still gives the stricter answer; the exemption lives in a new `Policy.CheckEndpointAddress(host, port, ip)` that the dialer calls, because only the dialer knows both halves. What the grant does **not** protect against is written on the field itself: it trusts DNS for an entry naming a hostname (prefer an IP literal), and it says nothing about what the service listening there can be made to do.

### Verified by hand before anything was built on it

`gemma4:12b-mlx` emits OpenAI-format tool calls. One hand-run request to `/v1/chat/completions` with a `tools` array came back with `finish_reason: "tool_calls"` and a `tool_calls[0].function.arguments` of `{"city":"Bandung"}`. Ollama's own `/api/tags` lists the model's capabilities as `["completion","vision","audio","tools","thinking"]`. `/v1/models` returns `{"object":"list","data":[{"id":"gemma4:12b-mlx",…}]}` — exactly the `ItemsPath: "data"` / `ValueField: "id"` shape the picker expects. The response also carries a non-standard `reasoning` field beside `content`, which `internal/ai` ignores harmlessly.

**This suite was run against a real local Ollama**, not only against `httptest`: all three gated tests pass in ~15s warm.

### Tests, and proof they fail without the change

The lever was reverted (`CheckEndpointAddress` reduced to `CheckAddress`) and each test re-run, then restored:

| Test | Failure without the change |
| --- | --- |
| `TestOneNamedPrivateEndpointIsReachedWhileEveryOtherOneStaysBlocked` | `Get "http://127.0.0.1:54543": … loopback address 127.0.0.1, want the request to go through` |
| `TestCheckEndpointAddressExemptsOnlyTheEndpointItWasAskedAbout` | `CheckEndpointAddress(named endpoint) = … loopback address 127.0.0.1, want it admitted` |
| `TestAModelCallReachesTheOneLoopbackEndpointTheDeploymentNamed` | `node "AI Agent": model turn 1: call model: Post "http://127.0.0.1:54575/chat/completions": … loopback address 127.0.0.1` |
| all three `TestOllama*` (with the env var set) | `the policy names http://127.0.0.1:11434/v1 and the guard refused it anyway` |

Narrowness is pinned separately, and these pass both before and after because they are about what stays refused: `TestAPrivateEndpointEntryThatIsNotAHostAndPortGrantsNothing` (ten malformed entries, each refused by `CheckPrivateEndpoint` *and* granting nothing to the dialer), `TestAPrivateEndpointAllowanceIsNotAlsoAHostAllowlistEntry`, `TestAnAllowedPrivateEndpointDoesNotCarryARedirectSomewhereElse`, and the second half of `TestAModelCallReachesTheOneLoopbackEndpointTheDeploymentNamed`, which points a model node at a *second* loopback server and requires the refusal. `TestAModelCallToAPrivateAddressIsRefused` (from FEAT-mvegj5) is untouched and still passes.

### One defect found in the suite's own design, while proving it

The first version of the gated Ollama suite **skipped** when the lever was removed, because a policy refusal looked like "nothing answered there". A suite that goes quiet when the guard regresses is worse than no suite. `requireOllama` and `warmOllama` now check `errors.Is(err, safehttp.ErrBlocked)` first and `t.Fatal` on it: an unreachable server is a machine-shaped reason to skip, a refusal by a policy that names the endpoint is a product regression.

### Running it, and what it costs

Opt-in through `KILASFLOW_TEST_OLLAMA_BASE_URL`; `KILASFLOW_TEST_OLLAMA_MODEL` overrides the pinned tag, so a run against another model is a deliberate, visible change. Unset, all three tests skip with the exact `ollama pull gemma4:12b-mlx` command.

```
ollama serve
ollama pull gemma4:12b-mlx
KILASFLOW_TEST_OLLAMA_BASE_URL=http://127.0.0.1:11434/v1 go test ./nodes/ -run Ollama -count=1
```

~7.7 GB on disk, several GB resident while answering. Nothing in the file calls `t.Parallel`: the constraint is the laptop also running an editor, a browser and a database, not the CPU. The model is warmed in the fixture before any assertion, because the first request after a load is far slower than the ones after it and a timeout tuned to warm performance would otherwise fail on the first test only. The `-mlx` tag is an Apple MLX build and Apple-Silicon-only; a `linux/amd64` runner finds no such model and skips with the instruction to set a portable tag.

### What a green run proves, and what it does not

It proves the wiring: the node, the credential, the options loader, the agent loop, the tool invocation and the egress policy all work against a real OpenAI-compatible server. It proves nothing about the quality of a model a customer will actually use, and no local suite can. That paragraph is in the test file itself so it is read by whoever next sees the suite go green.

## Follow-ups

- `docs/src/content/docs/concepts/safety-boundaries.md` was out of scope this session and is now stale in two places: it says a hostname allowlist and a blanket IP-range disable are the only two knobs, and states that "a model server at `127.0.0.1:11434` requires `allow_private_networks`". Both need `outbound.allowed_private_endpoints`.
- `config.example.yaml` documents no `outbound:` section at all — `FEAT-27km39` already covers that; the new key belongs in the same pass.
- `FEAT-cx3hq1` (the Playwright harness) was blocked by the same false premise and is now unblocked: its stub server needs an `allowed_private_endpoints` entry, not `allowed_hosts`.
- The generic `kilasflow.chatModel` node exposes no per-node model timeout — only the two provider nodes have the Options collection with `timeout`. A local model run therefore falls back to `Policy.Timeout` for its whole deadline. Not wrong, but worth a ticket if local models become common.
