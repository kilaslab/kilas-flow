---
id: BUG-b4cb1c
title: 'Board/doc drift: FEAT-3taswf ''done'' but @kilasflow/sdk not on npm; stale node counts; CLI help nouns'
status: doing
priority: medium
labels:
    - board-hygiene
    - docs
    - cli
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

Small drifts between what the board or docs claim and what exists. Following the project's own rule ("A ticket marked done is not evidence the deliverable exists"), they are recorded here.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: lead; finding ids: LEAD-11, LEAD-12, LEAD-10). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## LEAD-11: FEAT-3taswf "Publish @kilasflow/sdk to npm" is done, but the package isn't on npm

*docs · medium · release / board hygiene*

**Steps to reproduce:**

`npm view @kilasflow/sdk version` returns E404

**Actual:**

The ticket is status done while its first and fifth acceptance criteria are unticked ("npm install resolves", "publishing is driven by a git tag"). EPIC-bkj6yf and FEAT-5fhj6p depend on it.

**Expected:**

Status blocked or doing until a tag publishes, per the project's own rule ("A ticket marked done is not evidence the deliverable exists").

**Suggested fix:**

Reopen as blocked on the first release tag.


## LEAD-12: Docs stated 49 node types / 56 pairs; the live catalogue has 61 / 68

*docs · low · docs*

**Actual:**

docs/src/content/docs/concepts/node-registry.md and start/what-kilasflow-is.md both said 49/56 and claimed to be "measured".

**Suggested fix:**

Fixed in this change (re-measured 2026-09-23). A drift gate like skills G1-G5 would stop it recurring.


## LEAD-10: `credential create --help` describes --file as a "workflow document"

*docs · low · cli*

**Actual:**

`-file string  workflow document to send: a path, or - for stdin` on `kilasflow credential create --help`

**Expected:**

"credential document to send"

**Suggested fix:**

Make the shared flag help text take the noun per verb.


# Also found by the audit

## CLI-18: CLI reference drift: catalogue size, a broken example, and exit-code claims

*docs · low · docs-cli*

**Steps to reproduce:**

Read docs/src/content/docs/reference/cli.md against the binary.

**Actual:**

Line 21 says "a 90-entry catalogue" (the live one has 68). Line 424, `kilasflow node describe httpRequest`, exits 4 (CLI-6). Lines 803-805 say `--url ''` gives exit 2; the binary gives exit 1 `network_error` (CLI-14). Line 650 passes `--token "$KILASFLOW_TOKEN"` on the command line (CLI-11). Line 116's "deliberately absent" list is right, but the skills contradict it (CLI-3).

**Expected:**

Examples run as written. Counts are generated.

**Suggested fix:**

Run the cli.md fenced examples in the docs gate against a test server.

**Evidence:**

the cli.md lines quoted, cs/cmdlog.txt.

**Related:**

BUG-b4cb1c (stale node counts in other pages)


# Acceptance Criteria
- [x] FEAT-3taswf is reopened as blocked on the first release tag, or its unticked criteria move to a new ticket
- [x] The node counts in the docs are re-measured (done in the audit change on 2026-09-23), and a drift gate compares the prose with GET /node-types
- [x] `credential create --help` describes --file as a credential document; the shared flag help takes a per-verb noun
- [x] The CLI reference examples run as written (checked in CI), and the catalogue size and exit-code claims are generated. Evidence (2026-10-09): `make smoke-cli-examples` (scripts/cli-examples) runs 86 cli.md examples against a booted server and compares each exit code with its `# exit N`; it is a step of the CI smoke job, with `make smoke-cli`. The catalogue size is the sentence `shippedcatalogue.RenderSentence` writes between generated markers (`TestTheDocumentedCatalogueSizeIsTheShippedOne`, `make generate-docs-facts-check` in the drift job, and the runner's check of the served catalogue). The exit-code tables are `cli.RenderExitCodeReference()` (`TestCliMdExitCodeSectionIsGenerated`, `TestEveryErrorCodeTheCLIRaisesIsDocumented`).

## Progress (2026-10-08)

Criteria 1-3 done; criterion 4 (CLI-18) only partly, so the ticket stays `todo`.

- **FEAT-3taswf** reopened: `status: todo` plus a `blocked` label (the board has no blocked column; `pine doctor` flags `status: blocked`), with a note at the top of the ticket that it waits on the first `sdk-v*` tag publishing through `release.yml`'s sdk job, and which criteria (1, 5, 7) to tick then. Nothing was published.
- **Node counts** re-measured: a default install now serves 62 types across 69 pairs (59 types / 64 pairs compiled, 3 / 5 from the Telegram and WAHA packs), one more than the 2026-09-23 audit's 61/68. `concepts/node-registry.md` and `start/what-kilasflow-is.md` corrected. Drift gate: `cmd/kilasflow/catalogue_docs_test.go` registers what `run()` registers before directory packs (`nodes.RegisterAll`, `nodes.RegisterVectorNodes`, `telegram.Register`, `waha.Register`), counts types and pairs the way `GET /node-types` lists them, and fails when either page's sentence disagrees. It caught the 61→62 drift on its first run. Limit: it mirrors `run()`'s registration calls rather than calling the endpoint, so a new embedded pack added to `run()` must be added there too.
- **CLI help nouns:** `registerCreateFlags(noun)` takes the verb's noun; `credential create/update -h` say "credential document to send", `workflow create -h` says "workflow document to send". Test: `internal/cli/document_flag_help_test.go`. `make generate-skills-command-reference-check` up to date.
- **CLI-18, partly:** cli.md no longer states a catalogue size ("90-entry" → "the whole catalogue"), and the `node describe` / `node options` examples use the full type `kilasflow.httpRequest` (describe matches the type exactly, so `httpRequest` exited 4).

Left for criterion 4: run cli.md's fenced examples against a test server in CI; and the skills' contradiction of the "deliberately absent" list (CLI-3).

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments

## Fix (2026-10-08, CLI-14)

The binary now does what cli.md says. Every verb that sends a request with no base URL (`--url ''`) is refused with exit 2 and `no server URL: pass --url, set KILASFLOW_URL, or name one in the configuration file`. Before, it exited 1 with `network_error: unsupported protocol scheme ""`. The check sits in the CLI client's `buildRequest`, so it fires only where a request would have been sent, after each verb's own argument checks. Test: `TestAServerVerbWithNoServerURLIsAUsageError` (internal/cli/exit_test.go), which failed first for `workflow list`, `workflow get`, `exec get` and `credential list`. Still left for criterion 4: the CI job that runs cli.md's examples, CLI-11 and CLI-3.

CLI-11 (2026-10-08): the `mcp serve` example in cli.md no longer passes `--token "$KILASFLOW_TOKEN"` on the command line, where a process listing shows it. It relies on `KILASFLOW_TOKEN` from the environment, which the verb already reads.

Criterion 4, partly (2026-10-08): `TestEveryVerbCliMdRunsExists` (internal/cli/docs_examples_test.go) checks, without a server, that every `kilasflow …` line in cli.md's code blocks names a verb of the live command tree (up to three words, e.g. `datastore columns add`). It passes today, and a planted `kilasflow apix` made it fail. Still open: running the examples themselves against a test server in CI.

CLI-3 (2026-10-08): kilasflow-datastore and kilasflow-operations said the credential, datastore and tenant-delete writes "have no verbs yet", though each ships as a guarded verb. Their `not_shipped` entry, frontmatter and body both, now names only what is absent: schedule create/update/delete, pack install and tenant create, which matches cli.md's list. The index is regenerated, and `skills-check` and the skills tests pass.

## Progress (2026-10-09)

Criterion 4 is met; all three halves are in CI.

**The examples run.** `scripts/cli-examples` (`make smoke-cli-examples`) boots the binary it is given against a fresh SQLite database (auth on, a generated signing key, the operator key, an encryption key for credentials), seeds the ids a block uses by driving the same binary, and runs each fenced shell example with `sh -c`, comparing its exit code with the trailing `# exit N` (0 without one). It runs 86 examples and lists 3 server-path ones (`kilasflow`, `serve`, `-config`) by name without running them. The egress guard stays on: the workflow the examples run calls the server's own health endpoint, admitted by `outbound.allowed_hosts=127.0.0.1` plus `outbound.allowed_private_endpoints=127.0.0.1:<port>`, never `allow_private_networks`. A floor of 60 executed examples stops a page that lost its examples from passing. Without a server, `TestEveryCliMdExampleParsesAgainstItsVerb` (internal/cli) parses every example's words against its verb's own flag set the way `run()` builds it, so a flag the verb lacks, a flag with no value or a missing argument fails `go test`; a planted `--bogus` made it fail. Planted drift (`# exit 3` changed to `# exit 5`; a catalogue number changed in node-registry.md) made the runner fail naming the line. `make smoke-cli` (which existed but ran nowhere) and `make smoke-cli-examples` are now steps of the CI `smoke` job. `internal/docexamples` is the pure extractor (prompts and output lines, quoting, pipes and redirects, continuations, `# exit N`), with its own tests.

**What the runner found in cli.md, fixed:** ids were invented and some the wrong shape (now `$WORKFLOW_ID`, `$VERSION_ID`, `$EXECUTION_ID`, `$CREDENTIAL_ID`, `$DATASTORE_ID`, `$CURSOR`, one legend paragraph at the top); `node options kilasflow.httpRequest --property channel` named a property that does not exist (now `kilasflow.executeWorkflow --property workflowId --mode list`, and the prose says when `--credential` is needed); `--cursor cur_2` was not a cursor (the runner derives a real one by asking the example's own listing for one item); `mcp serve --url http://127.0.0.1:8080` hard-coded a port (now the environment); `debug eval ... --run 2 --item 1` needed a second run (now `--run 0 --item 0`, and `--run 3` shown as exit 2), and `$json.url` / `$json.n` evaluated to null on the nodes named (now `$json.status` on Set, `$("Set").first().json.n` for the value); `pack validate ./packs/telegram` was shown as working but exits 1 because the embedded packs have no `pack.sha256` sidecar (the page now shows that as `# exit 1`, and a runnable directory form `./my-pack`, which the runner lays out from the telegram manifest with a sidecar); `smtp.json` named a credential type that does not exist (now `credential.json`, an `httpHeaderAuth` document); the `--url ''` claim is now three fenced examples. Real non-zero exits are annotated (`workflow get` after delete exit 4, `api not-an-operation` exit 2, guarded verbs without `--yes` exit 3, `node describe httpRequest` exit 4, ...).

**The catalogue size is generated.** `internal/shippedcatalogue` (`Register`, `Count`, `RenderSentence`) assembles what a default install serves, in `run()`'s order; the sentence sits between `<!-- generated:catalogue-size -->` markers on node-registry.md and what-kilasflow-is.md. `cmd/kilasflow/catalogue_docs_test.go` compares each page's block with it (the regex over prose is gone). The runner also counts `node list --json` by type and source and compares it with node-registry.md's block, which is the check that `shippedcatalogue.Register` has not fallen behind `run()`.

**The exit codes are generated.** `cli.ExitCodes()`, `cli.StatusExits()` (built by calling `exitForStatus`) and the code-3 refusals render as `cli.RenderExitCodeReference()` between `<!-- generated:exit-codes -->` markers in cli.md. `TestCliMdExitCodeSectionIsGenerated` holds the page to it; `TestExitCodeTableHasARowForEveryExitConstant` holds the table to exit.go; `TestEveryErrorCodeTheCLIRaisesIsDocumented` reads the CLI's source for every `ErrCode` (string literals, constants, what `exitForStatus` returns) and requires each in backticks in cli.md. It failed first on `mcp_catalogue`, `mcp_transport` and `unexpected_shape`, which are now named.

**Generator.** `scripts/docs-facts` (`make generate-docs-facts` / `generate-docs-facts-check`, the latter a `drift` job step) writes the marked sections; `--check` names each stale page and the first differing line. The splice, stale-summary and repo-root helpers moved to `scripts/internal/splice`, shared with `scripts/skills-command-reference` (its tests and output are unchanged).

**Beyond the plan:** the optional credential-type count is generated too (`<!-- generated:credential-types -->` in concepts/credentials.md). The page said "Thirteen types ship" over thirteen rows while the registry holds fifteen; the two missing rows (`googleDriveOAuth2Api`, `gmailOAuth2`) are added and a test requires a row for every registered type.

**Deviations from the plan:** `exec cancel` on a finished execution exits 0 on this build (the plan expected 5), so it carries no annotation. The cursor example is on `workflow versions` only; the `datastore rows --cursor` example is dropped because it would need seeded rows. `Counts` is `{Compiled, Packs Size}` with `Total()`. `docexamples.Example` also carries `Block` (the index of its shell fence), which the runner uses to seed once per block. The three exit-code tables are one generated block, with the lead-in sentences generated too, instead of three.

**Follow-up (not done on purpose):** `run()` in cmd/kilasflow/main.go still makes its own registration calls, and `shippedcatalogue.Register` copies them. Making `run()` call a shared function (with its real executors and policy) would remove the copy; it was left alone because tracks B, C and F add nodes there in the same wave. Until then two checks catch a miss: `TestShippedCatalogueRegistersWhatRunRegisters` (cmd/kilasflow) reads `run()` and `Register` with `go/parser` and fails, naming the call, when `run()` hands the node registry to a call that `Register` does not make (or the reverse); a call that adds no node to a default install (`nodepack.LoadDir`, `setupSidecar`, `wasmpack.NewExecutor`, `webhook.Extract`, `ApplyVisibility`) is listed in `runCallsThatAddNoShippedNode` with its reason. Planting `telegram.Register(nil, ...)` in `Register`, and an unmirrored `nodepack.Register(nodeRegistry, ...)` in `run()`, each failed it. The runner's served-catalogue check is the second net. Also: the credentials skill's `CREDENTIAL_TYPES.md` still says "Thirteen types ship" (skills are BUG-x28fsx's).

**Review fixes (2026-10-09):** the credential-type table check reads only the "The built-in catalogue" section and matches whole first-cell rows (a removed row now fails; the default-domains table no longer satisfies it), and the generated credentials block carries only the count. The examples runner kills each command's whole process group on timeout and sets `WaitDelay`, for the shell lines and for the seeding binary calls (a `sleep 30 | cat` example now ends near the timeout instead of at 30 s), stops cleanly on SIGINT and SIGTERM (server stopped, `kilasflow-cli-examples-*` removed, checked by sending both mid-run), and the `smoke` and `drift` CI jobs have `timeout-minutes`. cli.md's sample workflow id is UUID-shaped.

