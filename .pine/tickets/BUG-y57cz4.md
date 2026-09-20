---
id: BUG-y57cz4
title: 'Boot/config/observability: binary default, list env keys, silent config, 500 cause, SSE shutdown'
status: testing
priority: high
labels:
    - ops
    - dx
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:10Z"
updated: "2026-09-20T00:41:49Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 6 finding(s) from dims: find:build-health-dx.

---
### Binary storage is off by default (binary.root empty) with no boot warning; HTTP autodetect then silently turns downloaded files into garbled JSON text [find:build-health-dx] (high/ux) · area: config defaults / binary data · confidence: high

A default install and the Docker image run with binary storage disabled. The boot log warns about the encryption key, embed key and auth, but not about this. The HTTP Request node's default autodetect then decodes binary responses as text and reports success. Forcing File mode fails with an error that does not name the setting to change.

Evidence: internal/config/config.go:649 default Binary.Root = "". Dockerfile ENV sets only HOST/PORT/DSN/LOG_FORMAT, and .env.example has no binary root entry. First-run log (work/build-health-dx/firstrun/server.log) has no warning about it. Live test on my own :8114 with defaults: manual trigger -> HTTP Request GET https://www.google.com/favicon.ico (autodetect) -> execution `succeeded`, and output json.body is the raw ICO bytes as a NUL-filled string with no binary and no warning. Error when storage is required: "binary storage is not configured on this server" (nodes/http.go:309, internal/binary/binary.go:201, internal/routing/executor.go:423, nodes/telegram_download.go:76). It does not mention binary.root or KILASFLOW_BINARY_ROOT. The shared review instance also runs without binary storage.

n8n behavior: Binary data works by default (filesystem/memory mode). Autodetect on a non-text response yields a binary property, not text.

Impact: Every file-handling workflow (HTTP downloads, Telegram/WAHA media, attachments) either fails or silently corrupts data on a stock install. n8n handles binary data out of the box.

Suggested fix: Default binary.root to <data dir>/binary (in Docker, /app/data/binary). WARN at boot when it is disabled. Name the key and env var in the runtime error. When storage is off, autodetect should fail or warn instead of text-decoding non-text content types.

Files: internal/config/config.go, Dockerfile, .env.example, nodes/http.go

---
### Live-events stream never closes for an already-finished execution, and execution.failed is sent without an event name (stack trace printed per stream) [find:build-health-dx] (medium/bug) · area: API / SSE events · confidence: high

typedEvent lost its ExecutionFailed case in merge db65aa1. Huma therefore prints an 'unknown event type' stack trace to stderr and sends the terminal frame as an unnamed `message`, which the web editor and SDK never listen for. The server still closes the stream, so the browser's EventSource reconnects with Last-Event-ID. For a terminal execution the handler then waits forever with heartbeats, so every failed-run view holds a connection open indefinitely.

Evidence: internal/api/handlers/executions.go:62-85 has no ExecutionFailed case. It existed in 72aa907 and disappeared in merge db65aa1 (2026-09-06). huma sse.go:264-267 prints the error plus debug.PrintStack() and writes no `event:` line. Live test on my :8114: failing workflow run, then `curl -N .../executions/<id>/events` shows ids 1-3 with names and id 4 as `data: {"type":"execution.failed"...}` with no event name. `curl -N -H 'Last-Event-ID: 4' .../events` shows only `: connected` and hangs (curl exit 28 after 5 s). web/src/lib/workflow-editor/event-stream.svelte.ts:61-75 and sdk/src/browser.ts:204-209 only listen for named events. The shared server log has 21 such stack traces.

Impact: The UI and SDK never learn that a run failed through the live feed. Browser tabs keep reconnecting to streams that never end, each holding a server connection, which also stalls graceful shutdown (separate finding). The naming half is also reported by ui-ops-surfaces F4.

Suggested fix: Restore the ExecutionFailed case and add a test that every events.Type maps to a registered SSE type. On subscribe, read the durable execution: if it is already terminal and nothing is left to replay, emit the terminal event (or close) instead of waiting.

Files: internal/api/handlers/executions.go, web/src/lib/workflow-editor/event-stream.svelte.ts, sdk/src/browser.ts

Existing tickets: FEAT-9ns8cr

---
### An open live-events stream blocks graceful shutdown for the full 15 s shutdown timeout, and the process exits with an error [find:build-health-dx] (medium/bug) · area: server shutdown · confidence: high

http.Server.Shutdown does not cancel active request contexts, and the SSE handler only returns on ctx.Done or a terminal event. So any open editor or executions tab keeps the connection active until server.shutdown_timeout expires. The process then exits non-zero with a plain-text error.

Evidence: internal/api/server.go:219-230. Live test on my :8114: one `curl -N -H 'Last-Event-ID: 4' /api/v1/executions/<failed>/events` held open, then SIGTERM. Process exit took 16 s. Client got a truncated transfer (curl exit 18). stderr: `kilasflow: graceful shutdown: context deadline exceeded`. The slog output only says "shutting down", with no completion or timeout line.

Impact: Every restart or deploy with a browser open stalls about 15 s and reports failure. Docker's default 10 s stop timeout will SIGKILL first. (In-flight execution draining is a separate engine-runtime finding, F14.)

Suggested fix: Set Server.BaseContext to a context that is cancelled when shutdown begins, or use RegisterOnShutdown to close broker subscriptions, so SSE handlers exit immediately. Log shutdown completion and timeout through slog.

Files: internal/api/server.go, internal/api/handlers/executions.go

---
### List-valued config keys cannot be set through the environment, although generated docs list an Env var for each [find:build-health-dx] (medium/bug) · area: config / env loading · confidence: high

koanf's default decoder turns a string env value into a one-element slice, and no string-to-slice hook is installed. So outbound.allowed_private_endpoints, outbound.allowed_hosts and embed.allowed_origins accept at most one entry from the environment. Container deployments without a mounted config file therefore cannot allow two private endpoints, two outbound hosts, or two embed origins.

Evidence: internal/config/config.go:685-690 uses env.Provider + UnmarshalWithConf with the default DecoderConfig (koanf v2.3.6 koanf.go:268: only StringToTimeDuration and textUnmarshaler hooks, WeaklyTypedInput). Env names documented at config.go:357 (KILASFLOW_OUTBOUND_ALLOWED_HOSTS), config.go:411 (KILASFLOW_EMBED_ALLOWED_ORIGINS) and config.example.yaml:201 (KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS). Live test: `KILASFLOW_OUTBOUND_ALLOWED_PRIVATE_ENDPOINTS='127.0.0.1:8091,127.0.0.1:8092'` (and the space-separated and JSON-array forms) makes boot refuse with `outbound.allowed_private_endpoints[0]: "127.0.0.1:8091,127.0.0.1:8092" must name a host and a port`. For allowed_hosts and allowed_origins the combined string is accepted silently as one pattern that never matches. The only env test (config_test.go:495) uses a single entry. .env.example admits the origin allowlist is "config-file territ

Impact: Docker and compose operators cannot configure multi-entry allowlists (for example an Ollama endpoint plus a stub, or several host apps embedding the editor).

Suggested fix: Use a custom DecoderConfig adding mapstructure.StringToSliceHookFunc(",") with trimming, or split known list keys in the env callback. Add env tests with several entries for all three keys, and document the separator.

Files: internal/config/config.go, internal/config/config_test.go, config.example.yaml, .env.example

---
### Internal 500 errors discard the underlying error: nothing in the logs or the response explains them [find:build-health-dx] (medium/dx) · area: API error handling / logging · confidence: high

Handlers turn repository and runtime errors into generic 500 problems ("execution lookup failed", "workflow operation failed") without logging err. The request logger records only method, path and status. Operators have no way to diagnose 500s.

Evidence: internal/api/handlers/executions.go:373-374 `if err != nil { return nil, huma.Error500InternalServerError("execution lookup failed") }`. Same pattern at executions.go:295,407, workflows.go:673, interop.go:185,189 and auth.go:232-346: 15 call sites, with zero slog calls anywhere in internal/api/handlers. internal/api/middleware/logger.go logs only method/path/status/bytes/duration. The shared review server's log has 49,014 `level=ERROR msg="http request" method=GET path=/api/v1/executions/<id> status=500 bytes=144` lines (5 execution ids) and 56 POST /webhook 500s, with no line saying why.

Impact: Production incidents cannot be debugged from logs. The same queries later return 200, so the transient cause is lost.

Suggested fix: Route 500s through one helper that logs err at ERROR with the request_id (and optionally puts request_id in the problem body).

Files: internal/api/handlers/executions.go, internal/api/handlers/workflows.go, internal/api/handlers/interop.go, internal/api/handlers/auth.go

---
### Config mistakes are silent: a missing explicit -config file, unknown YAML keys and misspelled KILASFLOW_* vars are all ignored, and the boot log never names the config file [find:build-health-dx] (medium/dx) · area: config loading · confidence: high

Load treats a missing config file as normal even when the operator passed -config explicitly. Unmarshal ignores unused keys. Env vars that match no field are dropped. The boot log never says which config file was used. Unknown log.format values also fall back to text silently.

Evidence: internal/config/config.go:676-683 (isNotExist ignored for any path); no ErrorUnused. Live on my :8116: `kilasflow -config /etc/kilasflow/does-not-exist.yaml` boots on defaults with no warning (envlist/nocfg.log). `-config typo.yaml` containing outbound.allowed_private_endpoint and binary.rootdir, plus env KILASFLOW_OUTBOUND_ALLOW_PRIVATE_NETWORK=true (missing S), boots with none of them applied and no warning (envlist/typo.log). The "starting kilasflow" line has no config field. README.md:147 itself says a mis-split key would be "discarded without a word".

Impact: Operators believe a security or egress setting is applied when it is not, and debugging takes a long time.

Suggested fix: Fail when an explicitly given -config path is missing (keep silence only for the implicit config.yaml). Collect unknown keys (koanf.Keys() vs struct tags, plus KILASFLOW_* env names) and WARN with the nearest valid key. Log the resolved config path at boot. Validate log.format.

Files: internal/config/config.go, cmd/kilasflow/main.go

## Acceptance criteria

- [ ] Binary storage is off by default (binary.root empty) with no boot warning; HTTP autodetect then silently turns
- [ ] Live-events stream never closes for an already-finished execution, and execution.failed is sent without an eve
- [ ] An open live-events stream blocks graceful shutdown for the full 15 s shutdown timeout, and the process exits 
- [ ] List-valued config keys cannot be set through the environment, although generated docs list an Env var for eac
- [ ] Internal 500 errors discard the underlying error: nothing in the logs or the response explains them
- [ ] Config mistakes are silent: a missing explicit -config file, unknown YAML keys and misspelled KILASFLOW_* vars
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)
## Progress 2026-09-19 (SecurityDx)
- Status: doing. Research + partial implementation done this session.

## Progress 2026-09-19 (SecurityFront2) — landed, testing
Status: testing (code landed; one scoped test pending, see Unverified).
- Binary storage is ON by default: `binary.root` defaults to `./data/binary`, beside
  the default SQLite DSN, so a stock install and the container image (WORKDIR /app,
  DSN /app/data/kilasflow.db) share one volume. Boot logs a WARN naming
  `binary.root`/`KILASFLOW_BINARY_ROOT` when an operator turns it off. The runtime
  message is now `binary.ErrNotConfigured`, which names the key and the env var
  instead of "binary storage is not configured on this server" (internal/binary
  updated; the two node call sites are WebhookParity's files and are delegated).
- List-valued config keys take several entries from the environment:
  `env.ProviderWithValue` + comma splitting for every field the struct declares as a
  slice (derived by reflection, so a new list key cannot be forgotten). A value that
  merely contains a comma (DSN, password, path) is never split.
- Config mistakes are loud: `config.LoadExplicit` refuses a missing `-config` path
  while the implicit `config.yaml` stays optional; unknown YAML keys and misspelled
  `KILASFLOW_*` vars are WARNed with the nearest valid key (Levenshtein);
  `log.format` must be text or json; `execution.default_timezone` is validated
  against the real zone database; the boot line names the config path and whether it
  was named explicitly. `newLogger` installs `slog.SetDefault`, so those warnings and
  the handler error logs use the configured formatter.
- SSE stream no longer hangs: the durable record is read by an operation middleware
  before the stream opens (huma commits 200 for SSE before the handler, so the
  handler cannot choose a status) — unknown ids and another workflow's execution
  answer a real 404; a finished run with no retained events emits a reconstructed
  terminal frame from the durable record and closes; a broker-dropped stream does the
  same; concurrent streams are capped at 32 per tenant. `events.ExecutionFailed` was
  missing from typedEvent, so every failure frame went out unnamed as `message` with a
  huma stack trace per stream — restored, with `executionEventSchemas()` extracted so
  a test can prove every events.Type has a registered name.
- Graceful shutdown: `http.Server.BaseContext` is cancelled when shutdown begins, so
  open event streams see Done immediately instead of holding Shutdown for the full 15s
  timeout; shutdown completion and failure are logged through slog.
- 500s now log their cause: `serverProblem(ctx, detail, err)` in
  internal/api/handlers/problem.go logs err at ERROR with the request id and returns the
  same generic problem. Applied in executions.go and workflows.go (Workflows.problem now
  takes ctx). auth.go (AuthHardening) and interop.go (ImporterTail) are delegated.
- Also landed for other slices, same file: `config.Auth.OperatorKeyEnv` +
  default KILASFLOW_AUTH_OPERATOR_KEY and the bootstrapIdentity operator-tenant/key
  registration (TenancyAdmin); `execution.wait_sweep_interval`, `execution.max_timeout`,
  `execution.default_timezone` + main.go wiring (EngineWaits); `Environment:
  workflowEnvironment()` restored in ServiceDeps (ExpressionParity/BUG-4053h6 — `$env`
  was always empty); `api.ResumePrefix` const (DXOps2).
Evidence (scoped, passed):
- `go test ./internal/config/ -count=1` ok (incl. 11 new regression tests in
  internal/config/boot_strictness_test.go)
- `go build ./internal/config/ ./internal/embed/ ./nodes/` ok
- `go vet ./cmd/kilasflow/` was blocked by a sibling's in-flight
  internal/repository/workflows.go (workflows.List undefined) — not this ticket.
Unverified: no scoped test run yet for the SSE/shutdown/config-in-main halves
(`go test ./internal/api/ -run 'Events|Shutdown'` could not run while sibling packages
were mid-edit). Those need a re-run before close.
Remaining: credentials.go pagination (BUG-fv5fer) still to apply — pattern is in the
ApiLists message (ListPage + base64 `<RFC3339Nano>\x00<id>` cursor, X-Next-Cursor
header, ErrInvalidCursor -> 400). Not done here for time; recorded so it is not lost.
