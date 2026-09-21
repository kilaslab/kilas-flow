---
id: FEAT-yxwyav
title: MCP adapter over the settled CLI verbs
status: doing
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
updated: "2026-09-21T03:34:01Z"
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
