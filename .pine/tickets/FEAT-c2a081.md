---
id: FEAT-c2a081
title: Add the MCP client tool node
status: todo
priority: low
labels:
    - platform
    - longtail
deps:
    - FEAT-je4f4t
parent: EPIC-m42s3g
phase: p8
created: "2026-09-05T05:12:26Z"
updated: "2026-09-05T05:12:26Z"
---

## Scope

An AI Agent in KilasFlow can call exactly one kind of tool. `nodes/ai.go` registers `kilasflow.httpTool` with a single output port `{Name: "tool", Kind: workflow.ConnectionTool}`, and `executeHTTPTool` emits one item carrying a descriptor under the `$ai` key. `AgentExecutor.Execute` collects those with `descriptorsFrom(input["tools"])` and then builds every one of them through `executor.httpToolFrom(...)` unconditionally — the descriptor's own `"kind"` field is never read. Any second tool type added today would be silently reinterpreted as a malformed HTTP request.

This ticket adds `kilasflow.mcpClientTool`, a node that connects to a Model Context Protocol server and exposes its tools to a connected agent. MCP is how a host application publishes its own capabilities to an agent without a bespoke node per capability, which is precisely the shape a white-label embedded platform needs: the host runs the MCP server, the customer's workflow points a node at it, and the agent gains the host's tools without KilasFlow shipping a line of integration code. PRD §64 already named "MCP Client node surfaced directly on canvas" as deferred work.

The implementation vehicle is the official `github.com/modelcontextprotocol/go-sdk`, which keeps this native Go with no sidecar. Its `mcp.StreamableClientTransport` carries an `HTTPClient *http.Client` field, so the client that `internal/safehttp.NewClient(policy)` builds — the one whose dialer re-checks the resolved IP of every connection and every redirect hop — goes in directly. An MCP endpoint is a tenant-authored URL and must be governed by the same SSRF policy as the HTTP node; a transport that built its own client would quietly lose that.

The node is scoped to tools. MCP resources and prompts are out of scope, and the ticket is finished without them.

## Acceptance criteria

- [ ] `kilasflow.mcpClientTool` is registered with a single output port of kind `ai_tool` and connects to the AI Agent's tools port like the HTTP Request Tool does.
- [ ] Every MCP request goes through an `http.Client` built by `internal/safehttp`, so a server URL pointing at loopback, a private range, or the cloud metadata service is refused by the same policy the HTTP node enforces.
- [ ] Server authentication comes from the credential store, and the token never appears in the workflow document, the execution record, or a log line.
- [ ] The agent exposes each selected server tool under its own name with the server's own input schema, and a model tool call produces a real `tools/call` round trip whose result is returned to the model as a tool turn.
- [ ] A tool name colliding with another tool connected to the same agent fails the run with a message naming both source nodes, rather than one shadowing the other.
- [ ] `AgentExecutor` dispatches on the descriptor's declared type; an MCP descriptor is never built as an HTTP tool, and a descriptor of an unknown type fails with a named error.
- [ ] An unreachable server, a server error, or a tool call exceeding its time budget fails that tool call with a diagnostic the agent reports back to the model, without aborting the surrounding execution unless the node is configured to.

## Implementation Plan

Fix the dispatch before adding the node. `AgentExecutor.Execute` in `nodes/ai.go` loops over `toolDescriptors` and calls `httpToolFrom` for each one; add a discriminator to the descriptor emitted by `executeHTTPTool` and switch on it. Doing this first means the new node cannot be half-wired: an MCP descriptor reaching the old loop would be turned into an HTTP request against whatever its parameters happened to contain.

Then add `internal/mcp`, a thin wrapper over the SDK that owns connection, tool listing and tool invocation, and knows nothing about workflow types. Add `github.com/modelcontextprotocol/go-sdk` to `go.mod`, pin the version, and construct `mcp.StreamableClientTransport{Endpoint: …, HTTPClient: safehttp.NewClient(policy)}`. The transport has no `Headers` field — the SDK's own examples inject headers with a custom `http.RoundTripper` — so wrap the safehttp client's transport rather than building a new client, or the SSRF dialer is lost at the moment it matters most.

Then the node in `nodes/ai.go` and its credential type. Give the node a server URL, an authentication credential, a tool selection and a per-call timeout. One MCP server usually publishes many tools while a KilasFlow descriptor maps to one `ai.Tool`, so emit one item per selected tool on the `tool` port; that keeps `descriptorsFrom` unchanged and makes the agent's duplicate-name check apply naturally across MCP and HTTP tools alike. The credential type belongs in the open registry that p2-6 builds; if that has not landed, add `mcpClientApi` to the closed `definitions` map in `internal/credentials/credentials.go` and note the debt.

Two decisions to settle rather than leave silent. First, transport: support streamable HTTP only and refuse a `stdio` server configuration with an explicit diagnostic. A stdio server means spawning a process inside the runtime image, which is distroless with no shell, and it reopens the arbitrary-third-party-process question that belongs to the sidecar ticket, not here. Second, session lifetime: connect once when the node executes and hold the session for the duration of the agent run rather than reconnecting per tool call — MCP `initialize` is a round trip, and an agent that calls three tools should not pay it three times. Close the session when the agent run finishes, including on failure.

The tool selector wants `multiOptions` from p2-2 and a dynamic list from p2-4's load-options endpoint. Until both exist, a plain string allowlist with an "expose every tool" default is acceptable; say so in the parameter description rather than shipping a select that cannot be populated.

## References

- Roadmap plan, p8 section, entry V2-p8-3: `.pine/roadmap.md`.
- PRD: `gflow-prd-v1.md` §64 ("MCP Client node surfaced directly on canvas").
- Model Context Protocol Go SDK (`/modelcontextprotocol/go-sdk`, v1.x): `mcp.StreamableClientTransport{Endpoint, HTTPClient, ReconnectOptions}`, `client.Connect(ctx, transport, nil)`, `(*ClientSession).CallTool`; the SDK's own proxy example documents that the transport has no headers field and that custom headers are injected through the `HTTPClient`'s `RoundTripper`.
- Code: `nodes/ai.go` (`HTTPToolNodeType`, `executeHTTPTool`, the `$ai` `descriptorKey`, `AgentExecutor.Execute`, `httpToolFrom`, `descriptorsFrom`), `internal/ai/ai.go` (`Tool`, `ToolDefinition`), `internal/safehttp/safehttp.go` (`NewClient`, `Policy`), `internal/credentials/credentials.go` (the closed `definitions` map), `internal/workflow/document.go` (`ConnectionTool`).
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 17 — n8n exposes MCP as an instance-level setting, not only as a node. Captured from a local n8n 2.33.7 instance; gitignored, never vendored.
