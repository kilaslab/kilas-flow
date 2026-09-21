---
id: FEAT-yxwyav
title: MCP adapter over the settled CLI verbs
status: done
priority: low
labels:
    - agent
    - api
    - sdk
deps:
    - FEAT-ew46cb
parent: EPIC-r0yg5q
phase: p4
created: "2026-09-20T07:47:53Z"
updated: "2026-09-21T03:42:45Z"
---

## Scope

Design §6 (`docs/superpowers/specs/2026-09-20-agent-surface-design.md`): tools map to CLI verbs and their descriptions are generated from `skills/index.json`; read-only tools are unguarded, guarded verbs require `confirm: true`; stdio first, streamable HTTP second.

## Decision: hand-rolled server, not `modelcontextprotocol/go-sdk` (design §9 question 2)

**Decision: hand-roll it, in `internal/mcp`.** The licence boundary does not decide this one —
both options are allowed — so the decision rests on how much protocol the adapter needs and on
what the SDK would put in the image.

*What the adapter actually needs.* Three methods over a newline-framed pipe: `initialize`,
`tools/list`, `tools/call`, plus `ping` and the `initialized` notification. Nothing else: no
resources, prompts, sampling, elicitation, progress, cancellation, sessions, OAuth, or schema
*inference* — the schemas are generated from the verb's own `flag.FlagSet`, which is a runtime
structure no typed-tool API can see. That is `internal/mcp/server.go`, 412 lines including its
comments, with no dependency outside the standard library.

*The licence check (`.pine/memory/licensing.md`).* Fetched the module and read it, rather than
trusting a licence badge:

```text
$ curl -s https://proxy.golang.org/github.com/modelcontextprotocol/go-sdk/@latest
{"Version":"v1.8.0","Time":"2026-09-04T08:08:52Z", …}
$ head -1 LICENSE
The MCP project is undergoing a licensing transition from the MIT License to the Apache License,
Version 2.0 ("Apache-2.0"). All new code and specification contributions to the project are
licensed under Apache-2.0. …
```

Apache-2.0 (with pre-relicensing contributions still MIT) is permissive, and the SDK is not an
n8n package, so nothing in the three prohibitions applies. The boundary is therefore *not* the
reason to decline it; the dependency tree is.

*What the SDK drags in.* `go.mod` requires eight modules directly — `golang-jwt/jwt/v5`,
`google/go-cmp`, `google/jsonschema-go`, `segmentio/encoding`, `yosida95/uritemplate/v3`,
`golang.org/x/oauth2`, `golang.org/x/time`, `golang.org/x/tools` — plus three indirect. Measured
rather than assumed, by building a minimal stdio server on it (`mcp.NewServer` +
`mcp.AddTool` + `StdioTransport`, nothing else):

```text
$ go list -deps . | grep -v '^internal/' | grep '\.'
github.com/google/jsonschema-go/jsonschema
golang.org/x/oauth2{,/internal}
github.com/segmentio/asm/{base64,cpu/*,keyset,internal/unsafebytes,ascii}
github.com/segmentio/encoding/{ascii,json,iso8601}
github.com/modelcontextprotocol/go-sdk/{jsonrpc,auth,oauthex,mcp,internal/*}
github.com/yosida95/uritemplate/v3
golang.org/x/sync/errgroup   golang.org/x/time/rate   golang.org/x/sys/cpu
$ ls -l probe   # 11,387,330 bytes for hello-world
```

The module is 57,217 lines of Go, and the packages that link into a *stdio-only, tools-only*
server are the JSON-RPC layer, the full `mcp` package, a JSON-Schema library, the OAuth/auth
stack, `segmentio/encoding`, `uritemplate` and `x/time/rate`. KilasFlow's binary is already
31 MB with 4 MB of frontend in it; the adapter's job does not justify any of that, and
`go.mod`/`go.sum` are untouched by this ticket.

*Which is more likely to still be correct in six months.* The protocol is moving away from the
shape this ticket implements, and that cuts *against* taking a dependency now. `2026-07-28`
labels the `initialize` handshake **legacy**: version, identity and capabilities became
per-request `_meta`, `initialize` is gone, `server/discover` is required, and `tools/list`
results gained `resultType`/`ttlMs`/`cacheScope`. A server that speaks the legacy handshake is a
supported configuration, and a modern client's `server/discover` probe gets a clean
method-not-found — the fall-back signal the specification defines for exactly this. Our 412
lines can follow that change in an afternoon; an SDK upgrade is a version bump of a 57k-line
dependency with its own era handling, its own dependency tree and its own release cadence, and
the SDK's typed-tool API would not even carry the flag-derived schemas we need.

*Precedent.* The repository already hand-rolls the client half
(`nodes/ai.go:3010-3260`): protocol version `2025-06-18`, `initialize` with
`protocolVersion`/`capabilities`/`clientInfo`, `notifications/initialized`, then `tools/list`
and `tools/call` with `{name, arguments}` and `{content:[{type,text}], isError}`. The wire
surface is understood here, and the adapter matches it.

*Alternatives recorded:* a second `kilasflow-mcp` executable (rejected — the shipped image
carries one command, design §4.1), and generating tools from a hand-written table (rejected —
that is the drift design §6 exists to prevent).

## Acceptance criteria

- [x] The hand-rolled versus `modelcontextprotocol/go-sdk` decision is recorded, including the licence-boundary check (`.pine/memory/licensing.md`).
      (Above: the SDK's licence text read from the module itself, its `go.mod`, the packages that actually link, and the protocol-evolution argument. `go.mod`/`go.sum` unchanged — no new dependency at all.)
- [x] Every tool is a mapping onto an existing CLI verb / API operation, never a second implementation; a test walks the tool list against the command tree.
      (`TestMCPToolsAreTheCommandTree` + `TestMCPDeclaredArgumentsAreTheVerbsOwn`: 61 tools for 63 verbs minus the two server modes, every tool named after a real verb, every verb published exactly once, every property mapped back to argv, every argv resolved by `splitVerb`, and every declared positional proven to be the one the verb's own code demands. The one implementation is `run(registry(), env)`.)
- [x] Guarded tools refuse without `confirm: true`.
      (`TestMCPGuardedToolsRequireConfirmation`: without `confirm` the CLI's own guard answers `confirmation_required` and the stub records **zero** requests; with `confirm: true` the request is byte-identical to `kilasflow workflow activate wf_1 --yes`. Driven live with the official MCP Inspector as well.)

## What shipped

**One verb of the existing binary**: `kilasflow mcp serve` (stdio), registered in
`internal/cli/command.go` like every other verb, so the distroless image carries one command.

| File | What it is |
| --- | --- |
| `internal/mcp/server.go` (412 lines) | The protocol: newline-framed JSON-RPC 2.0, `initialize` with version negotiation, `notifications/initialized`, `ping`, `tools/list`, `tools/call`, the JSON-RPC error codes, panic recovery, a bounded line reader. Standard library only. |
| `internal/mcp/server_test.go` | The transport's own contracts: a malformed message is refused *and the session survives*, notifications are never answered, an unknown method/tool is `-32601`/`-32602`, a panicking tool becomes `-32603` rather than a dead session, a frame is always one line, and the line bound drains rather than ends the session. |
| `internal/cli/mcp.go` (812 lines) | The adapter: tool generation from the command tree, the flags read back out of each verb's `FlagSet`, the skill index read from the embedded bundle, argv building, and the in-process dispatch through `run()`. |
| `internal/cli/mcp_test.go` | The tests below. |

- **Tools are generated, never listed.** One tool per verb, named after the path
  (`workflow_get`, `datastore_columns_rename`). Properties come from `Verb.Flags`' own
  `FlagSet` — a flag added to a verb appears in the schema with no second list to edit — plus
  the verb's declared positionals (`Verb.Args`, a new field on the command tree's own
  definition) plus `confirm` on guarded verbs. A flag the adapter cannot describe fails
  generation with the verb and the flag named, rather than publishing a tool that quietly
  cannot do what the CLI can.
- **`Verb.Args`** is the one addition to the command tree: the positional arguments each verb
  takes, in order, named the way the verb's own usage error names them. `TestMCPDeclaredArgumentsAreTheVerbsOwn`
  drives every verb that declares one with none of them and asserts the usage refusal names the
  first, so the declaration cannot drift from the code that enforces it.
- **Descriptions** are the verb's own `Summary`, plus its refusal sentence when guarded, plus
  the names of the skills in `skills/index.json` whose declarations teach that verb — read from
  the embedded bundle, so a renamed skill shows up in the tool list without an edit here.
- **`confirm: true` is `--yes`.** The adapter passes `--yes` when (and only when) the caller
  says `confirm: true`; without it the CLI's own `requireConfirmation` refuses, which keeps one
  implementation of that refusal. The guard runs before the configuration chain, so a refusal
  sends nothing at all.
- **A result is the CLI's envelope.** The tool result's one text block is `kilasflow <verb>
  --json`'s output verbatim, with `isError` set when the exit code was not 0; a streamed verb's
  payload passes through the same way. A protocol error is reserved for what the protocol owns:
  an unknown tool, an argument the schema does not have, a missing required argument.
- **The server's configuration is the call's configuration.** `--url`/`--token`/`--config` and
  `KILASFLOW_URL`/`KILASFLOW_TOKEN` are resolved once at launch and every tool call inherits
  them (a closure, not argv, so no token lands in a client's transcript); `--timeout` bounds one
  tool call. None of them is a tool property.
- **stdin is the transport.** A verb asking for `--file -` or `--body -` gets a refusal naming
  the way out rather than swallowing the next request.
- **Docs**: `docs/src/content/docs/reference/cli.md` — a command-tree row, a
  `## kilasflow mcp serve` section, a line in the local-verbs list and a bullet in the
  "exceptions to always one envelope" list (stdout is the protocol's).
- **The bundle's router skill** was regenerated (`make generate-skills-command-reference`): it
  gained `kilasflow mcp serve`, and the four verbs FEAT-ew46cb landed that its reference had
  missed.

### Proof

```text
$ gofmt -l internal/cli internal/mcp                                    # silent
$ go build ./... && go vet ./...                                        # clean
$ go test -count=1 ./...                                                # 53 packages ok, no FAIL
$ make skills-check                                                     # ok; install round-trip {"ok":true,"files":29,"drift":[]}
$ make generate-skills-command-reference-check                          # skills/using-kilasflow-skills/SKILL.md: 63 verbs, up to date
$ make generate-skills-index-check                                      # skills/index.json: 13 skills, up to date

$ go test -count=1 -v -run 'TestMCP' ./internal/cli/
--- PASS: TestMCPToolsAreTheCommandTree (0.00s)          # 61 subtests, one per tool
--- PASS: TestMCPGuardedToolsRequireConfirmation (0.01s)  # without/with confirm
--- PASS: TestMCPServeAnswersARealClient (0.00s)
--- PASS: TestMCPServeRefusesWhatTheCLIWouldRefuse (0.00s)
--- PASS: TestMCPDeclaredArgumentsAreTheVerbsOwn (0.02s)
ok  	github.com/kilaslab/kilas-flow/internal/cli	0.294s
```

The stdio transcript below is the **built binary** (`bin/kilasflow`, one command in the image)
driven over real pipes by a throwaway JSON-RPC harness (frames in on stdin, frames out on
stdout, nothing else on either):

```text
> {"jsonrpc":"2.0","id":1,"method":"initialize","params":{"protocolVersion":"2025-06-18","capabilities":{},"clientInfo":{"name":"throwaway-harness","version":"1"}}}
< {"jsonrpc":"2.0","id":1,"result":{"capabilities":{"tools":{}},"instructions":"Every tool is one verb of the kilasflow command line: …","protocolVersion":"2025-06-18","serverInfo":{"name":"kilasflow","title":"KilasFlow","version":"4e93851-dirty"}}}
> {"jsonrpc":"2.0","method":"notifications/initialized"}
> {"jsonrpc":"2.0","id":2,"method":"tools/list"}
< {"jsonrpc":"2.0","id":2,"result":{"tools":[{"name":"version","description":"print this binary's version and, when a server is reachable, its version. KilasFlow skill: kilasflow-operations.","inputSchema":{"additionalProperties":false,"properties":{},"type":"object"}}, …]}}
-- 61 tools; first three: ['version', 'health', 'ready']
-- workflow_get schema: {"description":"read one workflow by id. KilasFlow skills: …","inputSchema":{"additionalProperties":false,"properties":{"workflow_id":{"description":"the verb's workflow id, as `kilasflow workflow get` takes it","type":"string"}},"required":["workflow_id"],"type":"object"},"name":"workflow_get"}
-- workflow_activate properties: ["confirm", "workflow_id"]
> {"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"skills_list","arguments":{}}}
< {"jsonrpc":"2.0","id":3,"result":{"content":[{"type":"text","text":"{\"ok\":true,\"data\":{\"skillsVersion\":1,\"count\":13,…}}]}}
> {"jsonrpc":"2.0","id":4,"method":"tools/call","params":{"name":"workflow_activate","arguments":{"workflow_id":"wf_1"}}}
< {"jsonrpc":"2.0","id":4,"result":{"content":[{"type":"text","text":"{\"ok\":false,\"error\":{\"code\":\"confirmation_required\",\"message\":\"workflow activate publishes a public endpoint; pass --yes to confirm\"},\"meta\":{\"operation\":\"activate-workflow\",\"durationMs\":0}}\n"}],"isError":true}}
-- exit 0; stderr=''
```

A real third-party client, not a harness of mine, on the same binary:

```text
$ CLAUDE_CONFIG_DIR=/tmp/claude-probe claude mcp add kilasflow -- /tmp/wt-FEAT-yxwyav/bin/kilasflow mcp serve
Added stdio MCP server kilasflow …
$ CLAUDE_CONFIG_DIR=/tmp/claude-probe claude mcp list
Checking MCP server health…
kilasflow: /tmp/wt-FEAT-yxwyav/bin/kilasflow mcp serve - ✔ Connected

$ npx -y @modelcontextprotocol/inspector --cli …/kilasflow mcp serve --method tools/list
{ "tools": [ … 61 tools, each with name/description/inputSchema … ] }

$ npx -y @modelcontextprotocol/inspector --cli …/kilasflow mcp serve --method tools/call --tool-name help
{"content":[{"type":"text","text":"{\"ok\":true,\"data\":[{\"path\":\"serve\",…}, …]}"}]}

$ npx -y @modelcontextprotocol/inspector --cli …/kilasflow mcp serve --method tools/call \
    --tool-name workflow_activate --tool-arg workflow_id=wf_1
{"content":[{"type":"text","text":"{\"ok\":false,\"error\":{\"code\":\"confirmation_required\",…}}"}],"isError":true}
{"error":{"code":"tool_is_error","message":"Tool 'workflow_activate' returned isError:true."}}

$ … --tool-name workflow_activate --tool-arg workflow_id=wf_1 --tool-arg confirm=true
{"content":[{"type":"text","text":"{\"ok\":false,\"error\":{\"code\":\"network_error\",\"message\":\"Get \\\"http://127.0.0.1:8080/api/v1/auth/me\\\": … connection refused\"}}"}],"isError":true}
```

The last two are the pair that matters: without `confirm` the call never left the process
(`confirmation_required`); with it the call got past the guard and reached the authority probe
at the endpoint the server resolved — which is what `--yes` means to the CLI.

(`serverInfo.version` is the binary's own `git describe` of the worktree it was built from, so
it moves with the build and is not a claim about the protocol. Everything else in the frames
above is verbatim.)

## Deliberately not shipped

- **Streamable HTTP**, which design §6 puts after stdio. stdio is what a harness launches, and
  the HTTP transport needs an endpoint, session management, `Origin` validation and the
  `MCP-Protocol-Version` header — a second ticket's worth of surface with its own security
  review.
- **The modern protocol revisions** (`2026-07-28` and later: per-request `_meta`, no
  `initialize`, `server/discover`). A client on one gets a clean `method not found`, which is the
  fall-back signal the specification defines; supporting both eras is a change to
  `internal/mcp` alone, and the revision list is a single slice.
- **`resources`, `prompts`, sampling and elicitation.** Not declared in `capabilities` and
  answered `method not found`: the CLI has no operation behind any of them, and a capability the
  product cannot serve is the drift §6 exists to prevent.
- **A tool per *operation*.** The tools are the verbs, which is what §6 says and what keeps the
  adapter a mapping; the escape hatch (`kilasflow api`) is published like every other verb, so
  every operation stays reachable.
- **`--url`/`--token` as tool properties**, deliberately: a credential belongs in the server's
  environment, not in a model's arguments.

## Notes

- **Rebased onto `main` at 4e93851.** This work started at `eef4758`, where
  `internal/guardrails`' compile-scope test already failed (`QueueManualVersion` unpinned);
  FEAT-ew46cb's landing commit fixed it, so the worktree was moved onto that tip before the
  final gate run.
- **The bundle's reference was stale before this ticket**: `make
  generate-skills-command-reference-check` failed on `main` (83 lines: `workflow validate`,
  `workflow duplicate`, `exec retry`, `debug eval` were missing). Regenerating it fixed that and
  added `kilasflow mcp serve` (84 lines, now up to date).
- **No `kilasflow_not_shipped` row is contradicted.** The bundle never claimed the adapter was
  unavailable, and no row is made true by it; `grep -ri mcp skills/` matches only the generated
  reference line. No skill teaches MCP: a harness discovers tools through the protocol, not
  through the CLI's router skill.
- **A real bug the protocol test caught**: the over-long-line drain loop stopped on
  `bufio.ErrBufferFull` and left the line's tail in the buffer, so the *next* message read would
  have been that tail. `TestReadLineRefusesAMessagePastTheLimit` is why it was found before the
  commit rather than in a client's session.
- `./nodes`' `TestATimeoutSavedUnderTheOldKeyStillApplies` is timing-sensitive and failed once
  while several test binaries ran concurrently; it passes in isolation and in the clean
  full-suite runs. Untouched by this ticket.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `b3c9f3f9` (last commit at or before ticket created 2026-09-20)
- Commits (2):
  - `95040fc8` — FEAT-yxwyav: an MCP server over the CLI's own verbs
  - `aa307d46` — FEAT-mha6a0: cut the agent-surface implementation tickets from the design
- Files changed (base → working tree):

```
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/workflows/capstone.yml                     |  127 +
 .github/workflows/ci.yml                           |   55 +-
 .github/workflows/release.yml                      |  120 +-
 .pine/memory/docs.md                               |    8 +
 .pine/memory/tenancy.md                            |    8 +
 .pine/memory/testing.md                            |    9 +
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fvdz46.md                        |  368 +-
 .pine/tickets/BUG-p3t7yq.md                        |  212 +
 .pine/tickets/BUG-rpkjpy.md                        |  864 ++-
 .pine/tickets/BUG-t9j2ek.md                        |  410 +-
 .pine/tickets/BUG-vzzkg3.md                        |  558 +-
 .pine/tickets/BUG-w8h3km.md                        |  123 +
 .pine/tickets/BUG-xmr673.md                        |  707 ++-
 .pine/tickets/EPIC-bkj6yf.md                       |  680 ++-
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-15k49d.md                       | 1343 ++++-
 .pine/tickets/FEAT-1axhdn.md                       | 2017 ++++++-
 .pine/tickets/FEAT-3taswf.md                       | 1205 +++-
 .pine/tickets/FEAT-48hreg.md                       | 1441 ++++-
 .pine/tickets/FEAT-4jns31.md                       |  805 +++
 .pine/tickets/FEAT-5fhj6p.md                       |  292 +-
 .pine/tickets/FEAT-7cg0cd.md                       | 1339 ++++-
 .pine/tickets/FEAT-8mymac.md                       | 1115 +++-
 .pine/tickets/FEAT-bb4s6e.md                       |  837 +++
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++
 .pine/tickets/FEAT-c72set.md                       |  811 +++
 .pine/tickets/FEAT-emf6k5.md                       |  536 +-
 .pine/tickets/FEAT-ew46cb.md                       |  939 +++
 .pine/tickets/FEAT-fpqvwx.md                       |  893 ++-
 .pine/tickets/FEAT-hj8pyx.md                       |  714 ++-
 .pine/tickets/FEAT-m4d2y1.md                       |  763 +++
 .pine/tickets/FEAT-mha6a0.md                       |  123 +-
 .pine/tickets/FEAT-qdedm0.md                       |  993 +++-
 .pine/tickets/FEAT-x5qqpm.md                       |  850 +++
 .pine/tickets/FEAT-yxwyav.md                       |  262 +
 CHANGELOG.md                                       |  184 +-
 CONTRIBUTING.md                                    |    6 +
 Makefile                                           |  149 +-
 README.md                                          |   11 +-
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 +
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++
 cmd/kilasflow/idempotency_test.go                  |  225 +
 cmd/kilasflow/main.go                              |  325 +-
 cmd/kilasflow/main_test.go                         |  115 +
 cmd/kilasflow/sidecar.go                           |  400 ++
 cmd/kilasflow/sidecar_test.go                      |  362 ++
 cmd/kilasflow/webhook_wiring_test.go               |  138 +
 config.example.yaml                                |  154 +-
 .../content/docs/concepts/datastore-concurrency.md |  179 +
 docs/src/content/docs/concepts/execution-model.md  |    1 +
 docs/src/content/docs/concepts/node-registry.md    |   42 +-
 .../src/content/docs/concepts/safety-boundaries.md |   41 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |   55 +
 docs/src/content/docs/guides/community-nodes.md    |  147 +-
 docs/src/content/docs/guides/embedding.md          |   41 +-
 docs/src/content/docs/guides/idempotency.md        |  197 +
 docs/src/content/docs/guides/node-authoring.md     |    6 +
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 +
 .../content/docs/operate/acceptance-capstone.md    |  184 +
 docs/src/content/docs/operate/benchmark.md         |  229 +-
 .../docs/operate/configuration-reference.md        |  313 +-
 docs/src/content/docs/operate/deployment.md        |   31 +-
 .../src/content/docs/operate/javascript-sidecar.md |  193 +
 docs/src/content/docs/operate/security.md          |   23 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 +
 docs/src/content/docs/operate/upgrades.md          |   61 +-
 docs/src/content/docs/reference/api-contract.md    |   39 +-
 docs/src/content/docs/reference/api.md             |   10 +-
 docs/src/content/docs/reference/api/datastores.md  |   33 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/executions.md  |   48 +-
 docs/src/content/docs/reference/api/interop.md     |    6 +
 docs/src/content/docs/reference/api/nodes.md       |   16 +-
 docs/src/content/docs/reference/api/system.md      |    3 +-
 docs/src/content/docs/reference/api/tenants.md     |   21 +
 docs/src/content/docs/reference/api/workflows.md   |   53 +-
 docs/src/content/docs/reference/cli.md             |  900 +++
 docs/src/content/docs/reference/node-packs.md      |   80 +-
 docs/src/content/docs/start/install.md             |   12 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   11 +-
 e2e/.gitignore                                     |    1 +
 e2e/benchmark/README.md                            |  217 +-
 e2e/benchmark/SUMMARY.md                           |  109 +-
 e2e/benchmark/bench-2026-09-20T11-37-55.json       | 5978 ++++++++++++++++++++
 e2e/benchmark/bench-2026-09-20T12-01-21.json       | 5976 +++++++++++++++++++
 e2e/benchmark/lib.mjs                              |  405 +-
 e2e/benchmark/method.mjs                           |  393 ++
 e2e/benchmark/method.test.mjs                      |  366 ++
 e2e/benchmark/n8n-container.mjs                    |  458 ++
 e2e/benchmark/n8n-container.test.mjs               |  131 +
 e2e/benchmark/n8n.mjs                              |  416 +-
 e2e/benchmark/run.mjs                              |  877 ++-
 e2e/benchmark/scan-secrets.mjs                     |  196 +
 e2e/benchmark/scan-secrets.test.mjs                |   70 +
 e2e/benchmark/summarise.mjs                        |  393 +-
 e2e/benchmark/summarise.test.mjs                   |  303 +
 e2e/benchmark/workflows.mjs                        |  670 ++-
 e2e/benchmark/workflows.test.mjs                   |  223 +
 e2e/capstone/epic-capstone.spec.ts                 |  364 ++
 e2e/capstone/global-teardown.ts                    |   26 +
 e2e/fixtures/epic-config.ts                        |  172 +
 e2e/fixtures/epic-corpus.ts                        |  307 +
 e2e/fixtures/epic-external.ts                      |  293 +-
 e2e/fixtures/epic-hermetic.ts                      |  201 +
 e2e/fixtures/epic-image.ts                         |  456 ++
 e2e/fixtures/epic-proofs.ts                        |  854 +++
 e2e/fixtures/error-form-nodes.ts                   |  218 +
 e2e/fixtures/n8n-live.ts                           |    5 +
 e2e/helpers/stub.ts                                |   16 +-
 e2e/playwright.capstone.config.ts                  |   45 +
 e2e/scripts/capstone-lib.mjs                       |  519 ++
 e2e/scripts/capstone-report.mjs                    |  238 +
 e2e/tests/dashboard-lists.spec.ts                  |  187 +
 e2e/tests/datastore.spec.ts                        |    2 +-
 e2e/tests/epic-acceptance.spec.ts                  |  694 +--
 e2e/tests/epic-machinery.spec.ts                   |  581 ++
 e2e/tests/i18n.spec.ts                             |   62 +
 e2e/tests/library-import.spec.ts                   |    6 +-
 e2e/tests/n8n-compare.spec.ts                      |   53 +-
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |   15 +-
 go.mod                                             |    1 +
 go.sum                                             |    2 +
 internal/api/cors_test.go                          |   19 +-
 internal/api/datastores_test.go                    |  418 ++
 internal/api/debug_ops_test.go                     |  476 ++
 internal/api/embed_datastore_test.go               |   28 +
 internal/api/embed_defaults_test.go                |  150 +
 internal/api/handlers/admin.go                     |  154 +
 internal/api/handlers/admin_admin_test.go          |  268 +-
 internal/api/handlers/api_key_scopes_test.go       |  138 +
 internal/api/handlers/auth.go                      |   83 +-
 internal/api/handlers/auth_test.go                 |   63 +-
 internal/api/handlers/datastores.go                |  291 +-
 internal/api/handlers/execution_retry.go           |  206 +
 internal/api/handlers/executions.go                |   36 +-
 internal/api/handlers/idempotency.go               |  115 +
 internal/api/handlers/interop.go                   |   13 +-
 internal/api/handlers/nodes.go                     |   76 +-
 internal/api/handlers/problem.go                   |   17 +
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/workflow_duplicate.go        |   84 +
 internal/api/handlers/workflow_validate.go         |   98 +
 internal/api/handlers/workflows.go                 |  217 +-
 internal/api/idempotency_test.go                   | 1004 ++++
 internal/api/middleware/auth.go                    |    6 +
 internal/api/middleware/cors.go                    |   11 +-
 internal/api/middleware/cors_test.go               |    2 +-
 internal/api/middleware/embed.go                   |  140 +-
 internal/api/middleware/embed_sentences_test.go    |   35 +
 internal/api/middleware/embed_test.go              |   10 +-
 internal/api/middleware/loginlimit.go              |   13 +
 internal/api/middleware/loginlimit_test.go         |    3 +-
 internal/api/middleware/scope.go                   |  382 ++
 internal/api/middleware/scope_test.go              |  303 +
 internal/api/node_types_sidecar_test.go            |   82 +
 internal/api/node_visibility_test.go               |  544 ++
 internal/api/ready_fleet_test.go                   |  358 ++
 internal/api/routes.go                             |   19 +-
 internal/api/scoped_token_test.go                  |  154 +
 internal/api/server.go                             |   19 +-
 internal/api/tenant_delete_test.go                 |  386 ++
 internal/api/workflow_audit_test.go                |  209 +
 internal/auth/auth.go                              |   20 +
 internal/binary/binary.go                          |  117 +
 internal/binary/binary_test.go                     |  217 +
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  446 ++
 internal/cli/cli_test.go                           |  248 +
 internal/cli/client.go                             |  424 ++
 internal/cli/client_test.go                        |  320 ++
 internal/cli/command.go                            |  167 +
 internal/cli/command_test.go                       |  339 ++
 internal/cli/config.go                             |  258 +
 internal/cli/config_test.go                        |  749 +++
 internal/cli/context.go                            |  318 ++
 internal/cli/context_test.go                       |  236 +
 internal/cli/doc.go                                |   44 +
 internal/cli/exit.go                               |  125 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   88 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  543 ++
 internal/cli/mcp.go                                |  812 +++
 internal/cli/mcp_test.go                           |  752 +++
 internal/cli/openapi.go                            |  202 +
 internal/cli/openapi_contract_test.go              |  411 ++
 internal/cli/output.go                             |  116 +
 internal/cli/output_test.go                        |  246 +
 internal/cli/skills_used_test.go                   |   87 +
 internal/cli/sse.go                                |  151 +
 internal/cli/sse_test.go                           |  149 +
 internal/cli/verbs_api.go                          |  316 ++
 internal/cli/verbs_api_test.go                     |  820 +++
 internal/cli/verbs_auth.go                         |  314 +
 internal/cli/verbs_credential.go                   |  260 +
 internal/cli/verbs_credential_test.go              |  119 +
 internal/cli/verbs_datastore.go                    |  492 ++
 internal/cli/verbs_datastore_test.go               |  215 +
 internal/cli/verbs_debug.go                        |  157 +
 internal/cli/verbs_debug_test.go                   |  207 +
 internal/cli/verbs_exec.go                         |  455 ++
 internal/cli/verbs_exec_test.go                    |  501 ++
 internal/cli/verbs_node.go                         |  294 +
 internal/cli/verbs_node_test.go                    |  196 +
 internal/cli/verbs_pack.go                         |  147 +
 internal/cli/verbs_pack_test.go                    |  195 +
 internal/cli/verbs_run.go                          |  262 +
 internal/cli/verbs_run_test.go                     |  391 ++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_skills.go                       | 1069 ++++
 internal/cli/verbs_skills_test.go                  |  614 ++
 internal/cli/verbs_system.go                       |  282 +
 internal/cli/verbs_system_test.go                  |  182 +
 internal/cli/verbs_tenant.go                       |  172 +
 internal/cli/verbs_tenant_test.go                  |  116 +
 internal/cli/verbs_workflow.go                     |  939 +++
 internal/cli/verbs_workflow_test.go                |  590 ++
 internal/config/config.go                          |  387 +-
 internal/config/config_test.go                     |  365 ++
 internal/config/embed_branding_test.go             |  192 +
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 +
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/registry.go                   |    5 +
 internal/database/migrate.go                       |   40 +-
 internal/database/migrate_test.go                  |   83 +-
 internal/database/tenant_columns_test.go           |  309 +
 internal/database/webhook_route_backfill_test.go   |  491 ++
 internal/database/workflow_actor_migration_test.go |  181 +
 internal/datastore/catalogue.go                    |   16 +-
 internal/datastore/column_tenant_test.go           |  152 +
 internal/datastore/concurrency.go                  |  224 +-
 internal/datastore/concurrency_test.go             |  579 +-
 internal/datastore/doc.go                          |   13 +-
 internal/datastore/engine.go                       |   27 +-
 internal/datastore/engine_test.go                  |   66 +-
 internal/datastore/fleet.go                        |  364 +-
 internal/datastore/fleet_engine_test.go            |  711 +++
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  245 +-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |  124 +-
 internal/datastore/upsert_id.go                    |  247 +
 internal/datastore/upsert_id_test.go               |  498 ++
 internal/embed/embed.go                            |  129 +-
 internal/embed/embed_branding_test.go              |  164 +
 internal/embed/embed_lifetime_test.go              |  146 +
 internal/engine/authenticate.go                    |   72 +-
 internal/engine/authenticate_test.go               |  130 +
 internal/engine/datastore_concurrency_test.go      |  393 ++
 internal/engine/eval.go                            |  299 +
 internal/engine/eval_test.go                       |  172 +
 internal/engine/export_test.go                     |   35 +
 internal/engine/service.go                         |   30 +-
 internal/engine/tenant_visibility_test.go          |  379 ++
 internal/engine/wait_service.go                    |   47 +-
 internal/engine/wait_service_test.go               |  219 +-
 internal/guardrails/compile_scope_test.go          |  445 ++
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 +
 internal/idempotency/idempotency.go                |  432 ++
 internal/idempotency/idempotency_test.go           | 1120 ++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 +
 internal/interop/n8n/n8n_test.go                   |   55 +
 internal/interop/n8n/parameters.go                 |   11 +
 internal/mcp/server.go                             |  412 ++
 internal/mcp/server_test.go                        |  221 +
 internal/node/registry.go                          |   31 +
 internal/node/registry_bench_test.go               |  112 +
 internal/node/visibility.go                        |  347 ++
 internal/node/visibility_test.go                   |  796 +++
 internal/nodepack/loaddir.go                       |   86 +
 internal/nodepack/loaddir_module_test.go           |  178 +
 internal/nodepack/module.go                        |  350 ++
 internal/nodepack/module_e2e_test.go               |   98 +
 internal/nodepack/module_test.go                   |  173 +
 internal/nodepack/nodepack.go                      |  116 +-
 internal/nodepack/trigger.go                       |    8 +
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   67 +-
 internal/nodepack/visibility_test.go               |  262 +
 internal/repository/api_key_scopes_test.go         |   78 +
 internal/repository/auth.go                        |   91 +-
 internal/repository/executions.go                  |   59 +-
 internal/repository/idempotency.go                 |  360 ++
 internal/repository/idempotency_test.go            |  615 ++
 internal/repository/models.go                      |   66 +-
 internal/repository/postgres_execution_test.go     |   16 +
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   48 +
 internal/repository/tenant_rows.go                 |  283 +
 internal/repository/tenant_rows_test.go            |  420 ++
 internal/repository/webhooks.go                    |  124 +-
 internal/repository/webhooks_delivery_test.go      |  147 +
 internal/repository/webhooks_test.go               |  184 +-
 internal/repository/workflow_history.go            |  119 +-
 internal/repository/workflow_history_test.go       |  102 +-
 internal/repository/workflows.go                   |   22 +-
 internal/runcode/diagnostic.go                     |   72 +
 internal/runcode/diagnostic_internal_test.go       |   91 +
 internal/runcode/diagnostic_test.go                |   78 +
 internal/runcode/doc.go                            |   73 +-
 internal/runcode/errors.go                         |   25 +
 internal/runcode/inspect.go                        |   90 +
 internal/runcode/inspect_test.go                   |  120 +
 internal/runcode/persist.go                        |  465 ++
 internal/runcode/persist_test.go                   |  696 +++
 internal/runcode/runcode.go                        |  227 +-
 internal/runcode/runcode_test.go                   |   23 +-
 internal/runcode/sandbox.go                        |  334 ++
 internal/runcode/sandbox_test.go                   |  279 +
 internal/sidecarnode/catalogue.go                  |  115 +
 internal/sidecarnode/convert.go                    |  854 +++
 internal/sidecarnode/convert_test.go               |  554 ++
 internal/sidecarnode/load.go                       |  217 +
 internal/sidecarnode/load_test.go                  |  360 ++
 internal/skills/bundle.go                          |   23 +
 internal/skills/bundle_test.go                     |  273 +
 internal/skills/check.go                           |  584 ++
 internal/skills/check_test.go                      |  130 +
 internal/skills/embed_test.go                      |  169 +
 internal/skills/index.go                           |   98 +
 internal/skills/skill.go                           |  416 ++
 internal/skills/skills_test.go                     |  794 +++
 .../clean/skills/kilasflow-fixture/SKILL.md        |   43 +
 .../skills/kilasflow-fixture/references/NOTES.md   |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   42 +
 .../skills/kilasflow-fixture/SKILL.md              |   36 +
 .../skills/kilasflow-fixture/SKILL.md              |   40 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../missing-key/skills/kilasflow-fixture/SKILL.md  |   32 +
 .../skills/kilasflow-fixture/SKILL.md              |   41 +
 .../skills/kilasflow-fixture/references/PRESENT.md |    3 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   38 +
 .../skills/kilasflow-credentials/SKILL.md          |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 .../skills/kilasflow-fixture/SKILL.md              |   39 +
 .../skills/kilasflow-fixture/references/EXTRA.md   |    3 +
 .../unknown-key/skills/kilasflow-fixture/SKILL.md  |   34 +
 .../skills/kilasflow-fixture/SKILL.md              |   43 +
 .../skills/kilasflow-fixture/SKILL.md              |   33 +
 internal/tenantpurge/completeness_test.go          |  368 ++
 internal/tenantpurge/doc.go                        |  120 +
 internal/tenantpurge/docs_test.go                  |  115 +
 internal/tenantpurge/harness_test.go               |  614 ++
 internal/tenantpurge/purge.go                      |  412 ++
 internal/tenantpurge/purge_test.go                 |  507 ++
 internal/wasmpack/audit.go                         |  116 +
 internal/wasmpack/audit_test.go                    |  234 +
 internal/wasmpack/caps.go                          |  130 +
 internal/wasmpack/executor.go                      |  218 +
 internal/wasmpack/executor_test.go                 |  108 +
 internal/wasmpack/host.go                          |  480 ++
 internal/wasmpack/host_internal_test.go            |  161 +
 internal/wasmpack/hostcalls_binary.go              |  114 +
 internal/wasmpack/hostcalls_credentials.go         |   89 +
 internal/wasmpack/hostcalls_http.go                |  248 +
 internal/wasmpack/hostcalls_test.go                |  343 ++
 internal/wasmpack/legacy_test.go                   |   53 +
 internal/wasmpack/limits.go                        |   99 +
 internal/wasmpack/probe_test.go                    |  521 ++
 internal/wasmpack/registry.go                      |   89 +
 internal/wasmpack/registry_test.go                 |  116 +
 internal/wasmpack/spec.go                          |   90 +
 internal/wasmpack/support_test.go                  |  395 ++
 internal/wasmpack/testdata/probe/main.go           |  131 +
 internal/wasmtest/wasmtest.go                      |  359 ++
 internal/wasmtest/wasmtest_test.go                 |  191 +
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++
 internal/webhook/route_label_test.go               |  172 +
 internal/webhook/shape.go                          |   10 +
 internal/webhook/webhook.go                        |   17 +-
 internal/webhook/webhook_test.go                   |    2 +
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |   24 +-
 internal/workflow/compiler_visibility_test.go      |  280 +
 internal/workflow/lifecycle.go                     |   21 +-
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 migrations/postgres/000018_api_key_scopes.down.sql |   10 +
 migrations/postgres/000018_api_key_scopes.up.sql   |   18 +
 migrations/postgres/000019_workflow_actor.down.sql |   21 +
 migrations/postgres/000019_workflow_actor.up.sql   |   55 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 migrations/sqlite/000018_api_key_scopes.down.sql   |   10 +
 migrations/sqlite/000018_api_key_scopes.up.sql     |   18 +
 migrations/sqlite/000019_workflow_actor.down.sql   |   21 +
 migrations/sqlite/000019_workflow_actor.up.sql     |   56 +
 nodes/code.go                                      |   26 +-
 nodes/code_test.go                                 |  131 +
 nodes/datastore.go                                 |   83 +-
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  177 +
 nodes/executors.go                                 |   28 +-
 nodes/pgvector_test.go                             |   42 +-
 nodes/sidecar.go                                   |  383 ++
 nodes/sidecar_egress.go                            |  207 +
 nodes/sidecar_egress_internal_test.go              |  257 +
 nodes/sidecar_egress_test.go                       |  276 +
 nodes/sidecar_engine_test.go                       |  393 ++
 nodes/sidecar_test.go                              |  666 +++
 nodes/sql_options_live_test.go                     |   17 +-
 pkg/sdk/abi.go                                     |  298 +
 pkg/sdk/abi_test.go                                |  360 ++
 pkg/sdk/call.go                                    |  149 +
 pkg/sdk/capabilities.go                            |  169 +
 pkg/sdk/doc.go                                     |   83 +-
 pkg/sdk/example/fetch/main.go                      |   70 +
 pkg/sdk/guest_wasip1.go                            |   69 +
 pkg/sdk/host_other.go                              |  174 +
 pkg/sdk/host_wasip1.go                             |   41 +
 pkg/sdk/internal/abigen/abigen.go                  |   85 +
 pkg/sdk/internal/abigen/cmd/main.go                |   33 +
 pkg/sdk/sdk.go                                     |   55 +-
 pkg/sdk/sdk_test.go                                |   91 +
 scripts/check-coordinates.sh                       |   21 +
 scripts/generate-api-reference.mjs                 |   23 +-
 scripts/skills-command-reference/main.go           |  375 ++
 scripts/skills-command-reference/main_test.go      |  124 +
 scripts/skills-index/main.go                       |  144 +
 scripts/smoke-cli.sh                               |  228 +
 scripts/smoke-postgres.sh                          |   22 +
 sdk/CHANGELOG.md                                   |   33 +-
 sdk/LICENSE                                        |  202 +
 sdk/README.md                                      |  101 +-
 sdk/RELEASING.md                                   |  188 +
 sdk/examples/host-page/README.md                   |   64 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/examples/reference-host/server.mjs             |    8 +-
 sdk/examples/reference-host/tenant.html            |    3 +
 sdk/package.json                                   |   11 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 ++
 sdk/scripts/check-package.mjs                      |  315 ++
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +
 sdk/scripts/release.mjs                            |  149 +
 sdk/src/browser.ts                                 |   13 +-
 sdk/src/generated/models.ts                        |  593 +-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  161 +-
 sdk/test/browser.test.ts                           |   24 +
 sdk/test/operation-coverage.test.mjs               |   23 +
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 +
 sdk/test/release.test.mjs                          |  390 ++
 sdk/test/server.test.ts                            |  131 +-
 sidecar/discover.go                                |   71 +
 sidecar/doc.go                                     |   68 +-
 sidecar/fixture/crash.js                           |   19 +
 sidecar/fixture/dial.js                            |   32 +
 sidecar/fixture/env.js                             |   48 +
 sidecar/fixture/forged.js                          |   52 +
 sidecar/fixture/hang.js                            |   27 +
 sidecar/fixture/heap.js                            |   27 +
 sidecar/fixture/perm.js                            |   57 +
 sidecar/fixture/rss.js                             |   39 +
 sidecar/fixture_test.go                            |    9 +-
 sidecar/hostcall_test.go                           |  346 ++
 sidecar/hostile_test.go                            |  199 +
 sidecar/isolation_test.go                          |  845 +++
 sidecar/logwriter.go                               |   77 +
 sidecar/logwriter_test.go                          |  111 +
 sidecar/memory.go                                  |   51 +
 sidecar/memory_darwin.go                           |   30 +
 sidecar/memory_linux.go                            |   30 +
 sidecar/memory_other.go                            |   12 +
 sidecar/procattr_other.go                          |   30 +
 sidecar/procattr_unix.go                           |   50 +
 sidecar/process.go                                 |  347 ++
 sidecar/process_test.go                            |  238 +
 sidecar/protocol.go                                |  159 +-
 sidecar/runner.go                                  |  182 +
 sidecar/runner/runner.cjs                          |  966 ++++
 sidecar/runner_test.go                             | 1094 ++++
 sidecar/sidecar.go                                 |  654 ++-
 sidecar/sidecar_test.go                            |    2 +-
 sidecar/sidecartest/sidecartest.go                 |   96 +
 .../kf-fixture-badload/dist/nodes/Bad/Bad.node.js  |   19 +
 .../packages/kf-fixture-badload/package.json       |   12 +
 .../dist/credentials/Escape.credentials.js         |    1 +
 .../dist/nodes/Ok/Ok.node.js                       |   16 +
 .../packages/kf-fixture-badmanifest/package.json   |   16 +
 .../dist/nodes/Hostile/Hostile.node.js             |  139 +
 .../dist/nodes/Noisy/Noisy.node.js                 |   36 +
 .../packages/kf-fixture-hostile/package.json       |   13 +
 .../kf-fixture-netload/dist/nodes/Net/Net.node.js  |   19 +
 .../packages/kf-fixture-netload/package.json       |   12 +
 .../dist/credentials/FixtureApi.credentials.js     |   36 +
 .../dist/nodes/Greet/Greet.node.js                 |  154 +
 .../dist/nodes/Relay/Relay.node.js                 |   60 +
 .../packages/kf-fixture-nodes/package.json         |   17 +
 .../dist/nodes/Distinct/Distinct.node.js           |   56 +
 .../dist/nodes/Trigger/Trigger.node.js             |   30 +
 .../packages/kf-fixture-unsupported/package.json   |   13 +
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |   35 +
 .../dist/nodes/Trigger/Trigger.node.js             |   29 +
 .../dist/nodes/v2/FooV2.node.js                    |   33 +
 .../packages/kf-fixture-versions/package.json      |   14 +
 skills/embed.go                                    |   50 +
 skills/index.json                                  |  515 ++
 skills/kilasflow-credentials/SKILL.md              |  114 +
 .../references/CREDENTIAL_TYPES.md                 |  119 +
 skills/kilasflow-datastore/SKILL.md                |  127 +
 skills/kilasflow-datastore/references/FILTERS.md   |  140 +
 skills/kilasflow-debugging/SKILL.md                |  120 +
 .../references/TRACE_READING.md                    |  135 +
 skills/kilasflow-embedding/SKILL.md                |  122 +
 .../references/EMBED_HANDSHAKE.md                  |  134 +
 .../references/SESSION_AUTHORITY.md                |   67 +
 skills/kilasflow-error-handling/SKILL.md           |   97 +
 skills/kilasflow-expressions/SKILL.md              |  112 +
 .../references/EXPRESSION_ROOTS.md                 |  161 +
 skills/kilasflow-import-export/SKILL.md            |   96 +
 .../references/MAPPING_LIMITS.md                   |  143 +
 skills/kilasflow-node-configuration/SKILL.md       |  105 +
 .../references/LOAD_OPTIONS.md                     |  141 +
 .../references/PROPERTY_KINDS.md                   |  165 +
 skills/kilasflow-node-packs/SKILL.md               |   99 +
 .../references/MODULE_AND_SIDECAR.md               |   95 +
 .../kilasflow-node-packs/references/PACK_FORMAT.md |  114 +
 skills/kilasflow-operations/SKILL.md               |  109 +
 .../references/UPGRADE_ORDER.md                    |  210 +
 skills/kilasflow-triggers/SKILL.md                 |  132 +
 .../references/WEBHOOK_DELIVERY.md                 |  148 +
 skills/kilasflow-workflow-lifecycle/SKILL.md       |  115 +
 .../references/NAMING_AND_DESCRIPTIONS.md          |   81 +
 .../references/VALIDATION_CHECKLIST.md             |   95 +
 skills/using-kilasflow-skills/SKILL.md             |  228 +
 web/messages/en/auth.json                          |   36 +
 web/messages/en/canvas.json                        |   52 +
 web/messages/en/common.json                        |   27 +
 web/messages/en/credentials.json                   |   40 +
 web/messages/en/datastores.json                    |  176 +
 web/messages/en/editor.json                        |  134 +
 web/messages/en/embed.json                         |   16 +
 web/messages/en/executions.json                    |   94 +
 web/messages/en/home.json                          |   20 +
 web/messages/en/nav.json                           |   10 +
 web/messages/en/properties.json                    |  118 +
 web/messages/en/schedules.json                     |   33 +
 web/messages/en/settings.json                      |   46 +
 web/messages/en/versions.json                      |   89 +
 web/messages/en/workflows.json                     |  223 +
 web/messages/id/auth.json                          |   36 +
 web/messages/id/canvas.json                        |   52 +
 web/messages/id/common.json                        |   27 +
 web/messages/id/credentials.json                   |   40 +
 web/messages/id/datastores.json                    |  209 +
 web/messages/id/editor.json                        |  164 +
 web/messages/id/embed.json                         |   16 +
 web/messages/id/executions.json                    |   94 +
 web/messages/id/home.json                          |   20 +
 web/messages/id/nav.json                           |   10 +
 web/messages/id/properties.json                    |  123 +
 web/messages/id/schedules.json                     |   33 +
 web/messages/id/settings.json                      |   46 +
 web/messages/id/versions.json                      |   89 +
 web/messages/id/workflows.json                     |  222 +
 web/package.json                                   |    7 +-
 web/pnpm-lock.yaml                                 |  204 +
 web/project.inlang/settings.json                   |   25 +
 web/src/lib/api/generated/admin/admin.ts           |   94 +
 .../api/generated/datastore-rows/datastore-rows.ts |  110 +-
 web/src/lib/api/generated/executions/executions.ts |  222 +
 web/src/lib/api/generated/models/aPIKeyResource.ts |    8 +
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../api/generated/models/createAPIKeyInputBody.ts  |   10 +
 .../api/generated/models/createdAPIKeyResource.ts  |    7 +
 .../api/generated/models/deleteRowsInputBody.ts    |    2 +
 .../generated/models/duplicateWorkflowInputBody.ts |   17 +
 .../generated/models/evalExpressionInputBody.ts    |   20 +
 .../api/generated/models/evalExpressionResource.ts |   17 +
 .../generated/models/evalExpressionResourceType.ts |   22 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   16 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/principalResource.ts  |    9 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 .../api/generated/models/updateRowsInputBody.ts    |    2 +
 .../generated/models/validateWorkflowResource.ts   |   20 +
 .../models/workflowPublishEventResource.ts         |    7 +
 .../workflowPublishEventResourceActorKind.ts       |   18 +
 .../models/workflowVersionSummaryResource.ts       |   12 +
 .../workflowVersionSummaryResourceActorKind.ts     |   18 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  201 +
 web/src/lib/api/http.ts                            |    4 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   56 +-
 .../lib/components/dashboard/list-states.svelte    |   11 +-
 .../components/dashboard/locale-switcher.svelte    |   31 +
 .../lib/components/ui/dialog/dialog-content.svelte |    3 +-
 .../lib/components/ui/dialog/dialog-footer.svelte  |    3 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |    3 +-
 .../workflow-editor/activation-notices.svelte      |   15 +-
 .../components/workflow-editor/canvas-edge.svelte  |    5 +-
 .../components/workflow-editor/canvas-node.svelte  |   27 +-
 .../workflow-editor/editor-controls.svelte         |    6 +-
 .../workflow-editor/execution-canvas-node.svelte   |    9 +-
 .../workflow-editor/execution-canvas.svelte        |   30 +-
 .../components/workflow-editor/node-picker.svelte  |   15 +-
 .../workflow-editor/properties-panel.svelte        |   47 +-
 .../workflow-editor/property-field.svelte          |  228 +-
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  107 +-
 .../workflow-editor/workflow-editor.svelte         |  157 +-
 web/src/lib/dashboard/cursor-page.test.ts          |  285 +-
 web/src/lib/dashboard/cursor-page.ts               |   96 +-
 web/src/lib/dashboard/execution-list.test.ts       |  148 +-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/nav-sections.test.ts         |   55 +-
 web/src/lib/dashboard/nav-sections.ts              |   59 +
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   24 +-
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   39 +-
 web/src/lib/datastore/transfer.ts                  |   15 +-
 web/src/lib/embed/embed-editor.svelte              |   37 +-
 web/src/lib/embed/session.svelte.ts                |   50 +-
 web/src/lib/embed/session.test.ts                  |   20 +-
 web/src/lib/i18n/catalog.test.ts                   |   75 +
 web/src/lib/i18n/copy.test.ts                      |  399 ++
 web/src/lib/i18n/locale.svelte.ts                  |  107 +
 web/src/lib/i18n/locale.test.ts                    |   90 +
 web/src/lib/workflow-editor/activation.ts          |    3 +-
 web/src/lib/workflow-editor/document.ts            |    3 +-
 web/src/lib/workflow-editor/execution.test.ts      |   48 +-
 web/src/lib/workflow-editor/execution.ts           |   55 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   13 +-
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 10556 -> 10731 bytes
 web/src/lib/workflow-editor/import-diagnostics.ts  |    3 +-
 web/src/lib/workflow-editor/ports.ts               |    9 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   53 +-
 .../lib/workflow-editor/version-history.test.ts    |   65 +-
 web/src/lib/workflow-editor/version-history.ts     |   82 +-
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  162 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |   53 +-
 .../app/workflows/[id]/export-dialog.svelte        |   31 +-
 .../app/workflows/diagnostics-section.svelte       |   50 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   39 +-
 .../app/workflows/import-report-drawer.svelte      |    9 +-
 .../(dashboard)/app/workflows/import-report.svelte |   30 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   71 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |   56 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  169 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  212 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  108 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |   67 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  119 +-
 web/src/routes/+layout.svelte                      |   11 +
 web/src/routes/+page.svelte                        |   42 +-
 web/src/routes/approve/[token]/+page.svelte        |   51 +-
 web/src/routes/embed/[id]/+page.svelte             |   13 +-
 web/src/routes/login/+page.svelte                  |   21 +-
 web/vite.config.ts                                 |   21 +-
 696 files changed, 129307 insertions(+), 5299 deletions(-)
```
