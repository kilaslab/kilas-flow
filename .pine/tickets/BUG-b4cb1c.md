---
id: BUG-b4cb1c
title: 'Board/doc drift: FEAT-3taswf ''done'' but @kilasflow/sdk not on npm; stale node counts; CLI help nouns'
status: done
priority: medium
labels:
    - board-hygiene
    - docs
    - cli
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-10-09T12:19:53Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-10-09.

- Base: `018af94f` (last commit at or before ticket created 2026-09-23)
- Commits (19):
  - `20ba8aab` — merge: the CLI reference's examples run against a booted server in CI, and its exit codes and the catalogue size are generated from the binary (BUG-b4cb1c)
  - `0c64b707` — BUG-b4cb1c: a test reads run() and shippedcatalogue.Register and fails, naming the call, when run() hands the node registry to a registration Register does not make
  - `4ff2a83d` — BUG-b4cb1c: the examples runner kills a stuck command's whole process group, gives up on a held pipe after five seconds, stops cleanly on SIGINT/SIGTERM, and the smoke and drift jobs have timeouts
  - `b8f161f7` — BUG-b4cb1c: the credential-type table check reads only the built-in catalogue section and whole rows, and the generated block carries only the count
  - `01000e3b` — chore(pine): BUG-b4cb1c criterion 4 is met, with its evidence, deviations and the run() follow-up
  - `160baaf4` — BUG-b4cb1c: the credentials page's type count is generated and a test requires a table row for every credential type, which found two types the page never listed
  - `5ab2d4f3` — BUG-b4cb1c: make smoke-cli-examples runs every cli.md example against a booted server and CI now runs it, with smoke-cli, in the smoke job
  - `b0de39bd` — BUG-b4cb1c: a test parses every cli.md example against its verb's own flag set, so a flag the verb lacks or an argument left out fails go test
  - `a07e2ff4` — BUG-b4cb1c: cli.md's exit-code tables are generated from the binary's exit-code table, and a test fails when the CLI raises an error code the page never names
  - `6bce9edb` — BUG-b4cb1c: make generate-docs-facts writes the catalogue size into both pages and make generate-docs-facts-check fails CI when one is stale
  - `928297ee` — BUG-b4cb1c: the catalogue a default install serves is assembled in one place, internal/shippedcatalogue, and the docs' stated size is a generated block compared with it
  - `ce0645fb` — chore(pine): the 2026-10-09 parallel wave moves its tickets to doing
  - `02299729` — BUG-b4cb1c: the datastore and operations skills no longer deny the credential, datastore and tenant-delete verbs the binary ships
  - `cdbd4c00` — BUG-b4cb1c: a test fails when a cli.md example runs a kilasflow verb the binary does not have
  - `f8285403` — BUG-b4cb1c: the mcp serve example reads its token from KILASFLOW_TOKEN instead of putting it on the command line
  - `4d5d9fa6` — BUG-b4cb1c: a verb run with no server URL exits 2 naming how to set one, as cli.md says, instead of 1 with a network error
  - `c19f9e1d` — merge: credential tests say untestable instead of failed, OpenAI takes a base URL and Telegram a probe; docs counts and CLI nouns re-measured (BUG-epy2se, BUG-b4cb1c partial)
  - `c5fce873` — BUG-b4cb1c: the docs state the 62-type, 69-pair catalogue a test now checks, credential --file help names a credential document, and FEAT-3taswf is open again until a tag publishes the SDK
  - `2f8e9b16` — chore(pine): the n8n-parity and UX audit, and the plans it produced
- Files changed (base → working tree):

```
 .github/assets/editor.png                          |  Bin 0 -> 119127 bytes
 .github/workflows/ci.yml                           |   61 +-
 .github/workflows/code-corpus.yml                  |   54 +
 .gitignore                                         |    4 +
 .pine/MEMORY.md                                    |    2 +
 .pine/memory/code-node.md                          |   30 +-
 .pine/memory/docs.md                               |    4 +-
 .pine/memory/licensing.md                          |   12 +-
 .pine/memory/n8n-reference.md                      |    8 +-
 .pine/memory/persistence.md                        |    3 +-
 .pine/memory/testing.md                            |    3 +-
 .pine/memory/web-editor.md                         |    4 +-
 .pine/roadmap.md                                   |    8 +-
 .pine/tickets/BUG-0592hz.md                        |   62 +
 .pine/tickets/BUG-0bzsa1.md                        |  464 +++
 .pine/tickets/BUG-0grt9g.md                        |  427 +++
 .pine/tickets/BUG-0xv7bg.md                        |   68 +
 .pine/tickets/BUG-14gp8r.md                        |  198 +
 .pine/tickets/BUG-15st2k.md                        |  198 +
 .pine/tickets/BUG-2d1pdx.md                        |   49 +
 .pine/tickets/BUG-2eryxn.md                        |   95 +
 .pine/tickets/BUG-2mes2k.md                        |   83 +
 .pine/tickets/BUG-2n4rfz.md                        |   58 +
 .pine/tickets/BUG-2vcwjf.md                        |  126 +
 .pine/tickets/BUG-2xrz6c.md                        |   51 +
 .pine/tickets/BUG-2z8geh.md                        |  136 +
 .pine/tickets/BUG-3k12ky.md                        |  187 +
 .pine/tickets/BUG-3mem9s.md                        |  105 +
 .pine/tickets/BUG-3qxx0j.md                        |   70 +
 .pine/tickets/BUG-46g75c.md                        |  219 ++
 .pine/tickets/BUG-49vf3j.md                        |  428 +++
 .pine/tickets/BUG-4ch186.md                        |   85 +
 .pine/tickets/BUG-548bk9.md                        |  179 +
 .pine/tickets/BUG-56qqgx.md                        |   88 +
 .pine/tickets/BUG-5bgx5c.md                        |   81 +
 .pine/tickets/BUG-5dn8hr.md                        |   39 +
 .pine/tickets/BUG-5fhcx7.md                        |   50 +
 .pine/tickets/BUG-5xkexq.md                        |   63 +
 .pine/tickets/BUG-605n21.md                        |  154 +
 .pine/tickets/BUG-66fhea.md                        |  112 +
 .pine/tickets/BUG-6d6wbg.md                        |  115 +
 .pine/tickets/BUG-6gkd12.md                        | 1183 ++++++
 .pine/tickets/BUG-719gaz.md                        |  117 +
 .pine/tickets/BUG-9296bf.md                        |  532 +++
 .pine/tickets/BUG-9dw5me.md                        |   66 +
 .pine/tickets/BUG-9hx5xm.md                        |   85 +
 .pine/tickets/BUG-9pmv8y.md                        | 1148 ++++++
 .pine/tickets/BUG-a7p6c8.md                        |  487 +++
 .pine/tickets/BUG-a9d2hb.md                        |  212 ++
 .pine/tickets/BUG-argnka.md                        |   34 +
 .pine/tickets/BUG-asdh5q.md                        |  607 +++
 .pine/tickets/BUG-b3p8va.md                        |  120 +
 .pine/tickets/BUG-b4cb1c.md                        |  163 +
 .pine/tickets/BUG-b8bwhw.md                        |   67 +
 .pine/tickets/BUG-bcahaj.md                        |  113 +
 .pine/tickets/BUG-bw2zc1.md                        |   93 +
 .pine/tickets/BUG-c19kyx.md                        |  149 +
 .pine/tickets/BUG-c3fgw5.md                        |   60 +
 .pine/tickets/BUG-c73h98.md                        |   51 +
 .pine/tickets/BUG-c7s5ss.md                        |   33 +
 .pine/tickets/BUG-cmnsfz.md                        |  457 +++
 .pine/tickets/BUG-cnhq1f.md                        |   54 +
 .pine/tickets/BUG-cqbq25.md                        |   41 +
 .pine/tickets/BUG-d2t3kp.md                        |   73 +
 .pine/tickets/BUG-djp647.md                        |  130 +
 .pine/tickets/BUG-dstsg9.md                        |   69 +
 .pine/tickets/BUG-e7dwpk.md                        |  116 +
 .pine/tickets/BUG-e8ytyq.md                        |   58 +
 .pine/tickets/BUG-ecbq28.md                        |  132 +
 .pine/tickets/BUG-epy2se.md                        |  131 +
 .pine/tickets/BUG-fthahg.md                        |  244 ++
 .pine/tickets/BUG-g7ffj1.md                        |   92 +
 .pine/tickets/BUG-g9zf51.md                        |  480 +++
 .pine/tickets/BUG-gk7mf5.md                        |   69 +
 .pine/tickets/BUG-h6tj4e.md                        |   93 +
 .pine/tickets/BUG-hejyb9.md                        |  109 +
 .pine/tickets/BUG-hmp85t.md                        |  116 +
 .pine/tickets/BUG-hnvn3r.md                        |   55 +
 .pine/tickets/BUG-j7qrp2.md                        |   71 +
 .pine/tickets/BUG-jwhj6y.md                        |  122 +
 .pine/tickets/BUG-jx2g0k.md                        |  527 +++
 .pine/tickets/BUG-k99658.md                        |  213 ++
 .pine/tickets/BUG-kkkrmx.md                        |   35 +
 .pine/tickets/BUG-kvpx6x.md                        |  144 +
 .pine/tickets/BUG-mzk0xn.md                        |   63 +
 .pine/tickets/BUG-n4e97f.md                        |   51 +
 .pine/tickets/BUG-n6p7qy.md                        |  145 +
 .pine/tickets/BUG-n9a6bz.md                        |   67 +
 .pine/tickets/BUG-namghh.md                        |  118 +
 .pine/tickets/BUG-nbymq4.md                        |   82 +
 .pine/tickets/BUG-ngt25j.md                        |  739 ++++
 .pine/tickets/BUG-nn74ph.md                        |   60 +
 .pine/tickets/BUG-nzy3pa.md                        |  228 ++
 .pine/tickets/BUG-p334yw.md                        |   65 +
 .pine/tickets/BUG-p3j233.md                        |   71 +
 .pine/tickets/BUG-pc284d.md                        |   60 +
 .pine/tickets/BUG-pder07.md                        |  430 +++
 .pine/tickets/BUG-pdsydm.md                        |  125 +
 .pine/tickets/BUG-phv0r9.md                        |   67 +
 .pine/tickets/BUG-ppvyzr.md                        |   65 +
 .pine/tickets/BUG-pzkpfr.md                        |   76 +
 .pine/tickets/BUG-q6b75c.md                        |   70 +
 .pine/tickets/BUG-qe71kf.md                        |  142 +
 .pine/tickets/BUG-r1m83f.md                        |   92 +
 .pine/tickets/BUG-rbask0.md                        |  158 +
 .pine/tickets/BUG-rh7mpa.md                        |   62 +
 .pine/tickets/BUG-rs0xq1.md                        |   52 +
 .pine/tickets/BUG-rytwy7.md                        |  118 +
 .pine/tickets/BUG-sgrxhh.md                        |   68 +
 .pine/tickets/BUG-t12ffz.md                        |  196 +
 .pine/tickets/BUG-t3p92b.md                        |  107 +
 .pine/tickets/BUG-t9e3k5.md                        |   34 +
 .pine/tickets/BUG-tpyg0q.md                        |   65 +
 .pine/tickets/BUG-txafja.md                        |  762 ++++
 .pine/tickets/BUG-v8ksv8.md                        |   79 +
 .pine/tickets/BUG-vbw239.md                        |   31 +
 .pine/tickets/BUG-vsmnby.md                        |  152 +
 .pine/tickets/BUG-w18vn3.md                        |   62 +
 .pine/tickets/BUG-w9k234.md                        |   61 +
 .pine/tickets/BUG-x28fsx.md                        |  160 +
 .pine/tickets/BUG-x2sxzt.md                        |  525 +++
 .pine/tickets/BUG-x6gyc1.md                        |   77 +
 .pine/tickets/BUG-x94b3d.md                        |   46 +
 .pine/tickets/BUG-xam6t8.md                        |  179 +
 .pine/tickets/BUG-xkz7qx.md                        |  499 +++
 .pine/tickets/BUG-xpr3jj.md                        |  416 +++
 .pine/tickets/BUG-y38bss.md                        |   90 +
 .pine/tickets/BUG-ywbvfa.md                        |   38 +
 .pine/tickets/BUG-z0s4zg.md                        |  100 +
 .pine/tickets/BUG-zf4pnj.md                        |  115 +
 .pine/tickets/EPIC-3en6xr.md                       |   88 +
 .pine/tickets/EPIC-62zt4j.md                       |  110 +
 .pine/tickets/EPIC-7c3ry9.md                       |   66 +
 .pine/tickets/EPIC-8rbys7.md                       |  192 +
 .pine/tickets/EPIC-brpz48.md                       |   77 +
 .pine/tickets/EPIC-hkypt5.md                       |    8 +-
 .pine/tickets/EPIC-m42s3g.md                       |    2 +-
 .pine/tickets/EPIC-tjnr1z.md                       | 1234 +++++++
 .pine/tickets/FEAT-02cj1g.md                       |  102 +
 .pine/tickets/FEAT-02zdcq.md                       |  169 +
 .pine/tickets/FEAT-0hdfzd.md                       |  106 +
 .pine/tickets/FEAT-0xsc1s.md                       |   43 +
 .pine/tickets/FEAT-0ynje5.md                       |   35 +
 .pine/tickets/FEAT-1ge0xc.md                       |   38 +
 .pine/tickets/FEAT-1mxtsn.md                       |  104 +
 .pine/tickets/FEAT-21h6xp.md                       |  254 ++
 .pine/tickets/FEAT-274c4p.md                       |   68 +
 .pine/tickets/FEAT-27g2za.md                       |   40 +
 .pine/tickets/FEAT-2kx0hx.md                       |  260 ++
 .pine/tickets/FEAT-2m24nh.md                       |   53 +
 .pine/tickets/FEAT-2m4yvz.md                       |  101 +
 .pine/tickets/FEAT-2npfgy.md                       |   46 +
 .pine/tickets/FEAT-38je8w.md                       |   35 +
 .pine/tickets/FEAT-39ttf6.md                       |   39 +
 .pine/tickets/FEAT-3kwr8j.md                       |   38 +
 .pine/tickets/FEAT-3t112f.md                       |   53 +
 .pine/tickets/FEAT-3taswf.md                       |   16 +-
 .pine/tickets/FEAT-3ykb4v.md                       |   37 +
 .pine/tickets/FEAT-4bjfny.md                       |  100 +
 .pine/tickets/FEAT-4bvcrb.md                       |   38 +
 .pine/tickets/FEAT-4e376e.md                       |   56 +
 .pine/tickets/FEAT-4fp51b.md                       |   42 +
 .pine/tickets/FEAT-4jhtny.md                       |   35 +
 .pine/tickets/FEAT-4pz9fn.md                       |   37 +
 .pine/tickets/FEAT-53pa9a.md                       |   52 +
 .pine/tickets/FEAT-5fhj6p.md                       |    2 +-
 .pine/tickets/FEAT-5fx926.md                       |   57 +
 .pine/tickets/FEAT-5g42rz.md                       |   38 +
 .pine/tickets/FEAT-5kv1jq.md                       |    8 +-
 .pine/tickets/FEAT-5yd3y0.md                       |   99 +
 .pine/tickets/FEAT-6m295t.md                       |   38 +
 .pine/tickets/FEAT-6qzza1.md                       |   58 +
 .pine/tickets/FEAT-6r663e.md                       |   40 +
 .pine/tickets/FEAT-70j6dn.md                       |   55 +
 .pine/tickets/FEAT-7cg0cd.md                       |    6 +-
 .pine/tickets/FEAT-7q13t6.md                       |  385 ++
 .pine/tickets/FEAT-7t0xks.md                       |   39 +
 .pine/tickets/FEAT-83rcve.md                       |   36 +
 .pine/tickets/FEAT-8752vx.md                       |   54 +
 .pine/tickets/FEAT-8zgwp6.md                       |   37 +
 .pine/tickets/FEAT-9ep5pw.md                       |   38 +
 .pine/tickets/FEAT-9we7kw.md                       | 1003 +++++
 .pine/tickets/FEAT-9yr3tt.md                       |   40 +
 .pine/tickets/FEAT-a3dwj2.md                       |   52 +
 .pine/tickets/FEAT-afkx3k.md                       |  118 +
 .pine/tickets/FEAT-bfrkyk.md                       |   54 +
 .pine/tickets/FEAT-bvyc0b.md                       |   52 +
 .pine/tickets/FEAT-c81kp3.md                       |   59 +
 .pine/tickets/FEAT-cf5n4j.md                       |   21 +
 .pine/tickets/FEAT-cgm1y3.md                       |    2 +-
 .pine/tickets/FEAT-csqgg5.md                       |    6 +-
 .pine/tickets/FEAT-dn6s8s.md                       |   59 +
 .pine/tickets/FEAT-e898nz.md                       |  210 ++
 .pine/tickets/FEAT-edzr73.md                       |  121 +
 .pine/tickets/FEAT-egm8bf.md                       |   37 +
 .pine/tickets/FEAT-eqzpzq.md                       |  136 +
 .pine/tickets/FEAT-ez6xtm.md                       |   55 +
 .pine/tickets/FEAT-f045nj.md                       |  131 +
 .pine/tickets/FEAT-f3hx3a.md                       |   37 +
 .pine/tickets/FEAT-f40kg4.md                       |  142 +
 .pine/tickets/FEAT-fpqg78.md                       |   52 +
 .pine/tickets/FEAT-fqmh01.md                       |   97 +
 .pine/tickets/FEAT-fs3pjr.md                       |  205 ++
 .pine/tickets/FEAT-g33qf6.md                       |   42 +
 .pine/tickets/FEAT-g6k3y9.md                       |  188 +
 .pine/tickets/FEAT-gcq50s.md                       |   17 +-
 .pine/tickets/FEAT-gzd32h.md                       |   36 +
 .pine/tickets/FEAT-hxztwz.md                       |   45 +
 .pine/tickets/FEAT-je4f4t.md                       |    4 +-
 .pine/tickets/FEAT-jrrqs2.md                       |   21 +
 .pine/tickets/FEAT-jwhdsy.md                       |    2 +-
 .pine/tickets/FEAT-kcdrcy.md                       |  130 +
 .pine/tickets/FEAT-kfmq1z.md                       |   53 +
 .pine/tickets/FEAT-kpn0m3.md                       |   37 +
 .pine/tickets/FEAT-ktasef.md                       |  103 +
 .pine/tickets/FEAT-ky75b5.md                       |   52 +
 .pine/tickets/FEAT-m1fdn4.md                       |   56 +
 .pine/tickets/FEAT-m7aw75.md                       |   54 +
 .pine/tickets/FEAT-mammrz.md                       |  107 +
 .pine/tickets/FEAT-mccadj.md                       |   44 +
 .pine/tickets/FEAT-mh4e8g.md                       |   38 +
 .pine/tickets/FEAT-mj2nek.md                       |   98 +
 .pine/tickets/FEAT-mngmn1.md                       |   39 +
 .pine/tickets/FEAT-mq412g.md                       |   58 +
 .pine/tickets/FEAT-mxmjt7.md                       |  129 +
 .pine/tickets/FEAT-n010f0.md                       |   33 +
 .pine/tickets/FEAT-n12211.md                       |   40 +
 .pine/tickets/FEAT-nch9dg.md                       |    6 +-
 .pine/tickets/FEAT-npc3ge.md                       |   37 +
 .pine/tickets/FEAT-nq1vsx.md                       |   53 +
 .pine/tickets/FEAT-p01rcw.md                       |   98 +
 .pine/tickets/FEAT-p75n7j.md                       |   38 +
 .pine/tickets/FEAT-pfwjzk.md                       |   30 +
 .pine/tickets/FEAT-ppnetz.md                       |  141 +
 .pine/tickets/FEAT-pqnxx4.md                       |   37 +
 .pine/tickets/FEAT-prw1hw.md                       |   56 +
 .pine/tickets/FEAT-pt6ge9.md                       |   44 +
 .pine/tickets/FEAT-pxcbqj.md                       |  479 +++
 .pine/tickets/FEAT-q81bq4.md                       |    2 +-
 .pine/tickets/FEAT-qae4sh.md                       |   23 +
 .pine/tickets/FEAT-qdwm1k.md                       |   50 +
 .pine/tickets/FEAT-qf0hsa.md                       |   53 +
 .pine/tickets/FEAT-r267jj.md                       |   41 +
 .pine/tickets/FEAT-r87gtj.md                       |   26 +
 .pine/tickets/FEAT-r8ph93.md                       |   38 +
 .pine/tickets/FEAT-rdfjh1.md                       |   42 +
 .pine/tickets/FEAT-re138f.md                       |   54 +
 .pine/tickets/FEAT-rkj8ry.md                       |  319 ++
 .pine/tickets/FEAT-s3sfx5.md                       |   36 +
 .pine/tickets/FEAT-s99vdp.md                       |  155 +
 .pine/tickets/FEAT-sc3qrq.md                       |   54 +
 .pine/tickets/FEAT-sz4ddp.md                       |   57 +
 .pine/tickets/FEAT-t26rt7.md                       |    2 +-
 .pine/tickets/FEAT-t38djq.md                       |   56 +
 .pine/tickets/FEAT-t58m89.md                       |   37 +
 .pine/tickets/FEAT-t672pv.md                       |   57 +
 .pine/tickets/FEAT-tjcr13.md                       |   52 +
 .pine/tickets/FEAT-v2nenc.md                       |   58 +
 .pine/tickets/FEAT-v8md2e.md                       |   66 +
 .pine/tickets/FEAT-vjjs8t.md                       |  951 +++++
 .pine/tickets/FEAT-vntngh.md                       |   64 +
 .pine/tickets/FEAT-vvwpjw.md                       |    2 +-
 .pine/tickets/FEAT-w7n7x6.md                       |  131 +
 .pine/tickets/FEAT-w9kqeg.md                       |   10 +-
 .pine/tickets/FEAT-wcr6en.md                       |   52 +
 .pine/tickets/FEAT-wdxkvw.md                       |   43 +
 .pine/tickets/FEAT-wzfz3d.md                       |   59 +
 .pine/tickets/FEAT-x9gq0s.md                       |  320 ++
 .pine/tickets/FEAT-xj5tv6.md                       |   38 +
 .pine/tickets/FEAT-xr75b9.md                       |   58 +
 .pine/tickets/FEAT-xzdn35.md                       |   56 +
 .pine/tickets/FEAT-ybm2pd.md                       |    2 +-
 .pine/tickets/FEAT-yrnkz0.md                       |   37 +
 .pine/tickets/FEAT-ys734v.md                       |   36 +
 .pine/tickets/FEAT-yxhgeh.md                       |  491 +++
 .pine/tickets/FEAT-yyjfjq.md                       |    2 +-
 .pine/tickets/FEAT-z90r5a.md                       |   38 +
 .pine/tickets/FEAT-zhdxc4.md                       |   38 +
 .pine/tickets/FEAT-zjrw76.md                       |  454 +++
 .pine/tickets/FEAT-zm3wh2.md                       | 1209 ++++++
 .pine/tickets/FEAT-zn5rqy.md                       |  103 +
 .pine/tickets/FEAT-zwpvbf.md                       |   60 +
 CHANGELOG.md                                       |  478 ++-
 CONTRIBUTING.md                                    |   25 +
 Makefile                                           |   65 +
 README.md                                          |  450 +--
 cmd/kilasflow/boot_logger_test.go                  |   30 +
 cmd/kilasflow/catalogue_docs_test.go               |   84 +
 cmd/kilasflow/javascript_test.go                   |   29 +
 cmd/kilasflow/main.go                              |  181 +-
 cmd/kilasflow/shipped_catalogue_drift_test.go      |  227 ++
 cmd/kilasflow/sqlite_files_test.go                 |   50 +
 config.example.yaml                                |  102 +-
 docs/src/content/docs/concepts/architecture.md     |   86 +
 docs/src/content/docs/concepts/credentials.md      |  305 +-
 docs/src/content/docs/concepts/execution-model.md  |   52 +-
 docs/src/content/docs/concepts/expressions.md      |   20 +-
 .../src/content/docs/concepts/items-and-lineage.md |   22 +-
 docs/src/content/docs/concepts/node-registry.md    |   15 +-
 .../src/content/docs/concepts/safety-boundaries.md |  277 +-
 .../content/docs/concepts/tenancy-and-embedding.md |  100 +-
 docs/src/content/docs/concepts/webhooks.md         |  109 +-
 docs/src/content/docs/guides/code-javascript.md    |  426 +++
 docs/src/content/docs/guides/community-nodes.md    |    7 +-
 docs/src/content/docs/guides/embedding.md          |  106 +-
 docs/src/content/docs/guides/n8n-migration.md      |  240 +-
 .../content/docs/operate/acceptance-capstone.md    |    3 +-
 .../docs/operate/configuration-reference.md        |  184 +-
 docs/src/content/docs/operate/configuration.md     |   16 +
 docs/src/content/docs/operate/deployment.md        |   27 +
 docs/src/content/docs/operate/security.md          |   47 +-
 docs/src/content/docs/operate/tenant-deletion.md   |    4 +-
 docs/src/content/docs/reference/api-contract.md    |   25 +-
 docs/src/content/docs/reference/api.md             |    6 +-
 docs/src/content/docs/reference/api/credentials.md |    6 +-
 docs/src/content/docs/reference/api/events.md      |   14 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |   17 +
 docs/src/content/docs/reference/api/workflows.md   |   11 +-
 docs/src/content/docs/reference/cli.md             |  335 +-
 .../content/docs/reference/expression-grammar.md   |   13 +-
 docs/src/content/docs/reference/node-packs.md      |    8 +
 docs/src/content/docs/start/what-kilasflow-is.md   |   20 +-
 e2e/fixtures/epic-code.ts                          |  271 ++
 e2e/fixtures/epic-proofs.ts                        |   23 +
 e2e/fixtures/n8n-live.ts                           |    1 +
 e2e/helpers/seed.ts                                |   27 +
 e2e/helpers/server.ts                              |   12 +
 e2e/helpers/stub.ts                                |    7 +-
 e2e/tests/code-editor.spec.ts                      |  154 +
 e2e/tests/datastore-pg.spec.ts                     |    4 +-
 e2e/tests/datastore.spec.ts                        |  115 +-
 e2e/tests/editor-chat.spec.ts                      |   68 +-
 e2e/tests/google-oauth.spec.ts                     |   28 +
 e2e/tests/js-code.spec.ts                          |  702 ++++
 e2e/tests/live-backend-api.spec.ts                 |   72 +-
 e2e/tests/n8n-compare.spec.ts                      |   33 +-
 e2e/tests/n8n-paste.spec.ts                        |  116 +
 e2e/tests/node-coverage.spec.ts                    |   89 +-
 e2e/tests/smoke.spec.ts                            |   86 +
 gflow-prd-v1.md                                    |    8 +-
 go.mod                                             |   12 +-
 go.sum                                             |   22 +-
 internal/ai/agent.go                               |   64 +-
 internal/ai/ai.go                                  |   30 +-
 internal/ai/ai_test.go                             |  131 +
 internal/ai/fromai.go                              |  131 +-
 internal/ai/fromai_test.go                         |  121 +-
 internal/ai/openai.go                              |  247 +-
 internal/ai/openai_test.go                         |  156 +
 internal/ai/outputschema.go                        |  587 ++-
 internal/ai/outputschema_test.go                   |  229 ++
 internal/ai/reasoning_test.go                      |  245 ++
 internal/ai/toolusage.go                           |   46 +
 internal/ai/toolusage_test.go                      |   60 +
 internal/api/auth_test.go                          |   28 +
 internal/api/convert_fragment_test.go              |   82 +
 internal/api/credential_confinement_test.go        |  273 ++
 internal/api/credential_testable_test.go           |   62 +
 internal/api/credentials_pagination_test.go        |   58 +
 internal/api/credentials_probe_scope_test.go       |  235 ++
 internal/api/credentials_test.go                   |   81 +-
 internal/api/credentials_update_test.go            |  206 ++
 internal/api/datastores_csv_test.go                |   34 +
 internal/api/datastores_test.go                    |   57 +
 internal/api/debug_ops_test.go                     |   45 +-
 internal/api/docs.go                               |   10 +-
 internal/api/embed_confinement_test.go             |    9 +-
 internal/api/embed_publish_test.go                 |  160 +
 internal/api/embed_test.go                         |  106 +-
 internal/api/events_test.go                        |  104 +-
 internal/api/handlers/admin.go                     |    9 +-
 internal/api/handlers/admin_admin_test.go          |    2 +-
 internal/api/handlers/auth.go                      |    6 +-
 internal/api/handlers/credentials.go               |  348 +-
 internal/api/handlers/credentials_probe_test.go    |   75 +
 internal/api/handlers/datastores.go                |   13 +-
 internal/api/handlers/datastores_csv.go            |   27 +-
 internal/api/handlers/embed.go                     |    2 +-
 internal/api/handlers/embedscope.go                |  156 +-
 internal/api/handlers/execution_console_test.go    |   62 +
 internal/api/handlers/execution_retry.go           |   59 +-
 internal/api/handlers/execution_waiting_test.go    |   82 +
 internal/api/handlers/executions.go                |  139 +-
 internal/api/handlers/executions_events_test.go    |   65 +
 internal/api/handlers/interop.go                   |   59 +
 internal/api/handlers/oauth.go                     |  199 +-
 internal/api/handlers/problem.go                   |   16 +
 internal/api/handlers/resume_test.go               |    2 +-
 internal/api/handlers/system.go                    |   22 +-
 internal/api/handlers/workflow_validate.go         |   95 +-
 internal/api/handlers/workflow_validate_test.go    |  171 +
 internal/api/handlers/workflows.go                 |  107 +-
 internal/api/handlers/workflows_delete_test.go     |   81 +-
 internal/api/middleware/embed.go                   |   54 +-
 internal/api/middleware/embed_publish_test.go      |   87 +
 internal/api/middleware/embed_sentences_test.go    |   24 +-
 internal/api/middleware/embed_test.go              |   88 +
 internal/api/middleware/scope.go                   |   48 +-
 internal/api/middleware/scope_test.go              |    3 +
 internal/api/oauth_test.go                         |  364 +-
 internal/api/problems.go                           |   97 +
 internal/api/problems_test.go                      |  171 +
 internal/api/routes.go                             |   14 +-
 internal/api/schedules_test.go                     |   28 +
 internal/api/server.go                             |    8 +-
 internal/api/server_test.go                        |   40 +
 internal/api/workflow_tags_test.go                 |  342 ++
 internal/api/workflows_test.go                     |    8 +
 internal/cli/cli.go                                |   22 +
 internal/cli/client.go                             |  143 +-
 internal/cli/command_test.go                       |    4 +-
 internal/cli/docs_examples_test.go                 |  122 +
 internal/cli/document_flag_help_test.go            |   31 +
 internal/cli/exit.go                               |  136 +
 internal/cli/exit_reference_test.go                |  286 ++
 internal/cli/exit_test.go                          |   26 +
 internal/cli/guard_test.go                         |  144 +-
 internal/cli/mcp.go                                |   44 +-
 internal/cli/mcp_test.go                           |  317 +-
 internal/cli/openapi.go                            |  134 +-
 internal/cli/openapi_contract_test.go              |   43 +-
 internal/cli/skills_not_shipped_test.go            |  109 +
 internal/cli/sse.go                                |   27 +-
 internal/cli/verbs_api.go                          |   83 +-
 internal/cli/verbs_api_test.go                     |  324 +-
 internal/cli/verbs_credential.go                   |    4 +-
 internal/cli/verbs_credential_test.go              |   71 +
 internal/cli/verbs_datastore.go                    |    5 +-
 internal/cli/verbs_debug.go                        |   67 +-
 internal/cli/verbs_debug_test.go                   |   82 +
 internal/cli/verbs_exec.go                         |   16 +
 internal/cli/verbs_exec_test.go                    |   43 +
 internal/cli/verbs_skills.go                       |   94 +-
 internal/cli/verbs_skills_test.go                  |  119 +
 internal/cli/verbs_workflow.go                     |  110 +-
 internal/cli/verbs_workflow_tag_test.go            |   84 +
 internal/cli/verbs_workflow_test.go                |   59 +
 internal/config/boot_strictness_test.go            |  216 ++
 internal/config/config.go                          |  364 +-
 internal/config/config_test.go                     |  142 +
 internal/config/sqlite_root_test.go                |   51 +
 internal/credentials/builtin.go                    |   62 +-
 internal/credentials/credentials.go                |  143 +-
 internal/credentials/credentials_test.go           |  164 +-
 internal/credentials/external.go                   |   13 +-
 internal/credentials/oauth.go                      |  117 +-
 internal/credentials/oauth_redirect_test.go        |   41 +
 internal/credentials/oauth_test.go                 |   95 +-
 internal/credentials/probe_test.go                 |  181 +
 internal/credentials/registry.go                   |   99 +-
 internal/credentials/scope.go                      |  102 +
 internal/credentials/scope_test.go                 |  215 ++
 internal/credentials/scrub.go                      |  107 +
 internal/credentials/scrub_test.go                 |   61 +
 internal/credentials/vault.go                      |   10 +-
 .../datastore_unique_names_migration_test.go       |  212 ++
 internal/database/migrate_test.go                  |    2 +
 ...webhook_route_lifecycle_state_migration_test.go |   78 +
 internal/database/workflow_actor_migration_test.go |    7 +-
 internal/datastore/catalogue.go                    |   46 +-
 internal/datastore/engine.go                       |   26 +-
 internal/datastore/engine_test.go                  |   13 +-
 internal/datastore/names.go                        |  132 +
 internal/datastore/names_test.go                   |  271 ++
 internal/docexamples/docexamples.go                |  318 ++
 internal/docexamples/docexamples_test.go           |  209 ++
 internal/embed/confinement.go                      |   12 -
 internal/embed/confinement_test.go                 |   14 +-
 internal/embed/embed.go                            |   87 +-
 internal/embed/embed_publish_test.go               |  113 +
 internal/embed/embed_test.go                       |   60 +
 internal/engine/always_output_test.go              |  511 +++
 internal/engine/approval.go                        |    2 +-
 internal/engine/authenticate.go                    |  155 +-
 internal/engine/authenticate_test.go               |   74 +
 internal/engine/batch_failure_test.go              |  144 +
 internal/engine/batch_nodes_tolerance_test.go      |  108 +
 internal/engine/checkpoint.go                      |   94 +-
 internal/engine/checkpoint_test.go                 |  101 +
 internal/engine/console_test.go                    |  397 ++
 internal/engine/error_workflow_test.go             |  312 +-
 internal/engine/eval.go                            |  287 +-
 internal/engine/eval_test.go                       |  269 +-
 internal/engine/export_test.go                     |   13 +-
 internal/engine/fanout_lineage_test.go             |  241 ++
 internal/engine/item_outcomes.go                   |  131 +
 internal/engine/item_outcomes_test.go              |  201 +
 internal/engine/lineage_internal_test.go           |   74 +
 internal/engine/node_branch_test.go                |  157 +
 internal/engine/node_branches.go                   |   87 +
 internal/engine/node_timing_test.go                |  211 ++
 internal/engine/per_item_suspend_test.go           |  438 +++
 internal/engine/required_inputs_test.go            |  198 +
 internal/engine/resume_failure_test.go             |  174 +
 internal/engine/run_presentation_test.go           |   77 +
 internal/engine/runindex_skip_test.go              |  299 ++
 internal/engine/runindex_test.go                   |   70 +
 internal/engine/runner.go                          | 1204 +++++-
 internal/engine/runner_test.go                     |  968 +++++
 internal/engine/secret_scrub.go                    |  159 +
 internal/engine/secret_scrub_test.go               |  134 +
 internal/engine/service.go                         |  216 +-
 internal/engine/static_data.go                     |  136 +
 internal/engine/static_data_service_test.go        |  268 ++
 internal/engine/static_data_test.go                |   69 +
 internal/engine/subworkflow_test.go                |  259 +-
 internal/engine/wait_error_output_test.go          |  181 +
 internal/engine/wait_links_test.go                 |  159 +
 internal/engine/wait_service.go                    |   87 +-
 internal/engine/wait_service_test.go               |  641 +++-
 internal/engine/worker_test.go                     |    8 +
 internal/events/events.go                          |   37 +-
 internal/events/events_test.go                     |   68 +
 internal/execution/records.go                      |    7 +
 internal/expression/doc.go                         |    2 +-
 internal/expression/expression.go                  |   38 +
 internal/expression/expression_test.go             |   66 +
 internal/expression/globals.go                     |   22 +-
 internal/expression/parity_test.go                 |   48 +
 internal/expression/roots.go                       |  178 +-
 internal/guardrails/compile_scope_test.go          |    7 +-
 internal/guardrails/jsruntime_boundary_test.go     |  321 ++
 internal/idempotency/once.go                       |   64 +
 internal/idempotency/once_test.go                  |   56 +
 internal/interop/n8n/chatmodel_import_test.go      |  332 ++
 internal/interop/n8n/code_import_test.go           |  115 +
 internal/interop/n8n/connection_order_test.go      |   52 +
 internal/interop/n8n/corpus/scoreboard_test.go     |   23 +-
 internal/interop/n8n/cycle_import_test.go          |  178 +
 internal/interop/n8n/export_test.go                |    7 +
 internal/interop/n8n/extract_import_test.go        |   83 +
 internal/interop/n8n/fragment_import_test.go       |  134 +
 internal/interop/n8n/importer_tail_test.go         |   64 +-
 internal/interop/n8n/legacy_agent_test.go          |  120 +
 internal/interop/n8n/loose.go                      |  252 ++
 internal/interop/n8n/loose_json_test.go            |  239 ++
 internal/interop/n8n/n8n.go                        |  544 ++-
 internal/interop/n8n/n8n_test.go                   |  590 ++-
 internal/interop/n8n/parameters.go                 |  938 ++++-
 internal/interop/n8n/rag_import_test.go            |  215 +-
 internal/interop/n8n/roundtrip_fidelity_test.go    |  145 +
 internal/interop/n8n/set_null_import_test.go       |  150 +
 internal/interop/n8n/switch_import_test.go         |  371 ++
 internal/interop/n8n/tags.go                       |  182 +
 internal/interop/n8n/tags_test.go                  |  312 ++
 .../interop/n8n/testdata/n8n_cluster_nodes.json    |   24 +
 internal/interop/n8n/testdata/n8n_rag_16706.json   |   12 +-
 internal/interop/n8n/testdata/n8n_rag_3647.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_4799.json    |    2 +-
 internal/interop/n8n/testdata/n8n_rag_5908.json    |    2 +-
 internal/interop/n8n/tool_variant_import_test.go   |  111 +
 internal/interop/n8n/topology_import_test.go       |  147 +
 internal/interop/n8n/vectorstore.go                |  153 +
 internal/interop/n8n/vectorstore_import_test.go    |  281 ++
 internal/interop/n8n/workflow_tool_self_test.go    |   93 +
 internal/jsrun/analyze.go                          |  666 ++++
 internal/jsrun/analyze_errors.go                   |  187 +
 internal/jsrun/analyze_html.go                     |  575 +++
 internal/jsrun/analyze_html_test.go                |   63 +
 internal/jsrun/analyze_test.go                     |  184 +
 internal/jsrun/bounds_test.go                      |  364 ++
 internal/jsrun/buffer_test.go                      |   78 +
 internal/jsrun/clock.go                            |   68 +
 internal/jsrun/codec.go                            |  330 ++
 internal/jsrun/codec_internal_test.go              |  103 +
 internal/jsrun/comparator_test.go                  |  221 ++
 internal/jsrun/console.go                          |   64 +
 internal/jsrun/console_test.go                     |  109 +
 internal/jsrun/corpus/BASELINE.md                  |  412 +++
 internal/jsrun/corpus/MANIFEST.json                |  769 ++++
 internal/jsrun/corpus/baseline.json                | 3464 ++++++++++++++++++
 internal/jsrun/corpus/corpus_test.go               |  388 ++
 internal/jsrun/corpus/doc.go                       |   32 +
 internal/jsrun/corpus/jsdiff_test.go               |  317 ++
 internal/jsrun/corpus/scoreboard_test.go           |  710 ++++
 internal/jsrun/corpus/testdata/control/orders.json |  141 +
 internal/jsrun/crypto.go                           |  859 +++++
 internal/jsrun/crypto_test.go                      |  792 ++++
 internal/jsrun/doc.go                              |   99 +
 internal/jsrun/engine.go                           |  739 ++++
 internal/jsrun/engine_host.go                      |  174 +
 internal/jsrun/engine_nodejs.go                    |   34 +
 internal/jsrun/engine_rejections.go                |   94 +
 internal/jsrun/engine_timers.go                    |   85 +
 internal/jsrun/errors.go                           |  230 ++
 internal/jsrun/export_test.go                      |   80 +
 internal/jsrun/guards_test.go                      |  172 +
 internal/jsrun/helpers.go                          |  298 ++
 internal/jsrun/helpers_test.go                     |  600 +++
 internal/jsrun/htmlcomments_test.go                |  120 +
 internal/jsrun/inline.go                           |  149 +
 internal/jsrun/intl.go                             | 3853 ++++++++++++++++++++
 internal/jsrun/intl_internal_test.go               |  204 ++
 internal/jsrun/intl_test.go                        |  780 ++++
 internal/jsrun/items.go                            |  248 ++
 internal/jsrun/js/modules/buffer.js                |  296 ++
 internal/jsrun/js/modules/crypto.js                |  808 ++++
 internal/jsrun/js/modules/errors.js                |  298 ++
 internal/jsrun/js/modules/helpers.js               |  342 ++
 internal/jsrun/js/modules/intl.js                  |  538 +++
 internal/jsrun/js/modules/luxon.js                 |  110 +
 internal/jsrun/js/modules/timers.js                |   75 +
 internal/jsrun/js/modules/url.js                   |   10 +
 internal/jsrun/js/modules/util.js                  |   62 +
 internal/jsrun/js/modules/web.js                   |  295 ++
 internal/jsrun/js/runtime.js                       | 1726 +++++++++
 internal/jsrun/jsrun.go                            |  247 ++
 internal/jsrun/jsrun_test.go                       |  487 +++
 internal/jsrun/libraries.go                        |   86 +
 internal/jsrun/luxon_test.go                       |  163 +
 internal/jsrun/modules.go                          |   68 +
 internal/jsrun/modules_test.go                     |   50 +
 internal/jsrun/natives.go                          |  113 +
 internal/jsrun/norace_test.go                      |    7 +
 internal/jsrun/programs.go                         |  145 +
 internal/jsrun/race_test.go                        |    8 +
 internal/jsrun/rejections_test.go                  |  190 +
 internal/jsrun/roots.go                            |  152 +
 internal/jsrun/roots_test.go                       |  584 +++
 internal/jsrun/run.go                              |  484 +++
 internal/jsrun/security_test.go                    |  284 ++
 internal/jsrun/surface_internal_test.go            |  596 +++
 internal/jsrun/testdata/parity/collation.json      |   10 +
 internal/jsrun/testdata/parity/currencies.json     |   59 +
 internal/jsrun/testdata/parity/date-options.json   | 3598 ++++++++++++++++++
 internal/jsrun/testdata/parity/dates.json          |   33 +
 internal/jsrun/testdata/parity/errors.json         |  304 ++
 internal/jsrun/testdata/parity/html-comments.json  |   37 +
 internal/jsrun/testdata/parity/luxon.json          |   68 +
 internal/jsrun/testdata/parity/numbers.json        |  317 ++
 internal/jsrun/testdata/parity/utf8.json           | 2598 +++++++++++++
 internal/jsrun/testdata/parity/weeks.json          |   28 +
 internal/jsrun/testdata/parity/zones.json          |  832 +++++
 internal/jsrun/testdata/surface.txt                |  512 +++
 internal/jsrun/watchdog.go                         |  117 +
 internal/jsrun/web_test.go                         |  304 ++
 internal/jsrun/wire.go                             |  213 ++
 internal/jsrun/wire_test.go                        |   57 +
 internal/jsrun/wording.go                          |  150 +
 internal/jsrun/wording_internal_test.go            |   48 +
 internal/jsrun/wording_test.go                     |  327 ++
 internal/jsrun/wrapper.go                          |  111 +
 internal/jsworker/confine.go                       |  192 +
 internal/jsworker/confine_linux.go                 |  234 ++
 internal/jsworker/confine_linux_test.go            |  501 +++
 internal/jsworker/confine_test.go                  |  250 ++
 internal/jsworker/doc.go                           |   78 +
 internal/jsworker/helpers_test.go                  |  275 ++
 internal/jsworker/jsworker_test.go                 |  595 +++
 internal/jsworker/limits_linux.go                  |   93 +
 internal/jsworker/limits_other.go                  |   45 +
 internal/jsworker/load_test.go                     |  144 +
 internal/jsworker/norace.go                        |    6 +
 internal/jsworker/pool.go                          |  952 +++++
 internal/jsworker/probe_other_test.go              |    6 +
 internal/jsworker/protocol.go                      |  158 +
 internal/jsworker/race.go                          |    6 +
 internal/jsworker/security_test.go                 |   52 +
 internal/jsworker/tenants_test.go                  |  194 +
 internal/jsworker/worker.go                        |  326 ++
 internal/loadoptions/datastores.go                 |   31 +-
 internal/loadoptions/datastores_test.go            |   35 +
 internal/loadoptions/loadoptions.go                |   14 +-
 internal/loadoptions/loadoptions_test.go           |   30 +
 internal/loadoptions/redirect_test.go              |   30 +
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   21 +-
 internal/mcp/server.go                             |   32 +-
 internal/node/registry.go                          |   60 +-
 internal/nodepack/trigger.go                       |   77 +-
 internal/nodepack/trigger_capture_test.go          |  221 ++
 internal/property/editor_test.go                   |   51 +
 internal/property/property.go                      |   52 +
 internal/repository/auth.go                        |    9 +-
 internal/repository/credentials.go                 |  137 +-
 internal/repository/credentials_page_test.go       |   69 +
 internal/repository/executions.go                  |  262 +-
 internal/repository/models.go                      |   11 +-
 internal/repository/node_run_console_test.go       |  117 +
 internal/repository/postgres_execution_test.go     |    1 +
 internal/repository/prefix_test.go                 |   40 +
 internal/repository/schedules.go                   |   43 +-
 internal/repository/static_data.go                 |   81 +
 internal/repository/static_data_test.go            |   50 +
 internal/repository/table_names_test.go            |   22 +
 internal/repository/tenant_rows.go                 |    6 +-
 internal/repository/waits.go                       |    4 +
 internal/repository/waits_test.go                  |   68 +
 internal/repository/webhook_state.go               |  108 +
 internal/repository/webhook_state_test.go          |  148 +
 internal/repository/webhooks.go                    |   18 +-
 internal/repository/workflow_history.go            |    4 +
 internal/repository/workflow_tags.go               |   87 +
 internal/repository/workflow_tags_test.go          |  283 ++
 internal/repository/workflows.go                   |   44 +-
 internal/routing/executor.go                       |   13 +-
 internal/runcode/diagnostic_internal_test.go       |   38 +
 internal/runcode/runcode.go                        |   58 +-
 internal/runcode/runcode_test.go                   |   80 +
 internal/runcode/sandbox.go                        |   73 +
 internal/safehttp/path.go                          |   15 +
 internal/safehttp/redact.go                        |   82 +
 internal/safehttp/redact_test.go                   |  108 +
 internal/safehttp/redirect_scope_test.go           |  141 +
 internal/safehttp/safehttp.go                      |   62 +-
 internal/safehttp/safehttp_test.go                 |   20 +
 internal/scheduler/scheduler.go                    |   51 +-
 internal/scheduler/scheduler_test.go               |   83 +-
 internal/shippedcatalogue/shippedcatalogue.go      |  162 +
 internal/shippedcatalogue/shippedcatalogue_test.go |  132 +
 internal/skills/bundle_test.go                     |   35 +
 internal/skills/skill.go                           |   24 +
 internal/sqlbuild/sqlbuild_test.go                 |    5 +-
 internal/sqlnode/export_test.go                    |   32 +-
 internal/sqlnode/guard_test.go                     |    2 +-
 internal/sqlnode/internal_test.go                  |    1 +
 internal/sqlnode/sqlite.go                         |  302 ++
 internal/sqlnode/sqlite_bounded_test.go            |  242 ++
 internal/sqlnode/sqlite_confinement_test.go        |  300 ++
 internal/sqlnode/sqlite_unix_test.go               |  116 +
 internal/sqlnode/sqlnode.go                        |  100 +-
 internal/sqlnode/sqlnode_test.go                   |   37 +-
 internal/sqlnode/tls_test.go                       |  200 +
 internal/tenantpurge/harness_test.go               |   20 +-
 internal/tenantpurge/purge.go                      |    2 +-
 internal/tenantpurge/purge_test.go                 |    2 +-
 internal/wasmpack/hostcalls_http.go                |   24 +-
 internal/wasmpack/probe_test.go                    |    4 +-
 internal/web/embed.go                              |   43 +-
 internal/web/embed_test.go                         |   95 +
 internal/webhook/header_auth_redaction_test.go     |   72 +
 internal/webhook/lifecycle.go                      |   74 +-
 internal/webhook/lifecycle_redaction_test.go       |   74 +
 internal/webhook/lifecycle_scope_test.go           |  129 +
 internal/webhook/lifecycle_test.go                 |  114 +
 internal/webhook/request_lifecycle.go              |  581 ++-
 internal/webhook/request_lifecycle_capture_test.go |  401 ++
 internal/webhook/request_lifecycle_test.go         |  275 ++
 internal/webhook/route_state_test.go               |   75 +
 internal/webhook/sandbox.go                        |   90 +
 internal/webhook/sandbox_test.go                   |  231 ++
 internal/webhook/secret_record_test.go             |   66 +
 internal/webhook/webhook.go                        |   69 +-
 internal/webhook/webhook_test.go                   |   86 +
 internal/workflow/compiler.go                      |  247 +-
 internal/workflow/compiler_test.go                 |   85 +
 internal/workflow/cycle.go                         |  192 +
 internal/workflow/cycle_test.go                    |  161 +
 internal/workflow/document.go                      |   18 +
 internal/workflow/document_test.go                 |   44 +
 internal/workflow/shape.go                         |   43 +
 internal/workflow/tags.go                          |  113 +
 internal/workflow/tags_test.go                     |  188 +
 .../postgres/000021_node_run_console.down.sql      |    8 +
 migrations/postgres/000021_node_run_console.up.sql |   25 +
 .../000022_datastore_unique_names.down.sql         |    8 +
 .../postgres/000022_datastore_unique_names.up.sql  |   64 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../postgres/000024_workflow_static_data.down.sql  |    4 +
 .../postgres/000024_workflow_static_data.up.sql    |   25 +
 migrations/postgres/000025_workflow_tags.down.sql  |    5 +
 migrations/postgres/000025_workflow_tags.up.sql    |   36 +
 migrations/sqlite/000021_node_run_console.down.sql |    8 +
 migrations/sqlite/000021_node_run_console.up.sql   |   21 +
 .../sqlite/000022_datastore_unique_names.down.sql  |    8 +
 .../sqlite/000022_datastore_unique_names.up.sql    |   57 +
 .../000023_webhook_route_lifecycle_state.down.sql  |    7 +
 .../000023_webhook_route_lifecycle_state.up.sql    |   20 +
 .../sqlite/000024_workflow_static_data.down.sql    |    4 +
 .../sqlite/000024_workflow_static_data.up.sql      |   25 +
 migrations/sqlite/000025_workflow_tags.down.sql    |    5 +
 migrations/sqlite/000025_workflow_tags.up.sql      |   35 +
 nodes/ai.go                                        |  707 +++-
 nodes/ai_fromai_data_test.go                       |  392 ++
 nodes/ai_scope_test.go                             |   52 +
 nodes/ai_test.go                                   |  418 ++-
 nodes/ai_tools_test.go                             |  334 ++
 nodes/annotation.go                                |    1 +
 nodes/batch_shape_test.go                          |  157 +
 nodes/chat.go                                      |    2 +-
 nodes/code.go                                      |   46 +-
 nodes/code_test.go                                 |   48 +
 nodes/core.go                                      |   26 +-
 nodes/credential_redaction_test.go                 |   76 +
 nodes/credential_redirect_test.go                  |  145 +
 nodes/credentialscope_test.go                      |  131 +
 nodes/database.go                                  |   14 +-
 nodes/database_test.go                             |   69 +-
 nodes/datastore.go                                 |  626 +++-
 nodes/datastore_byname_test.go                     |   76 +
 nodes/datastore_test.go                            |  127 +-
 nodes/datastore_tool_test.go                       | 1366 ++++++-
 nodes/embedscope.go                                |  141 +-
 nodes/embedscope_test.go                           |   56 +-
 nodes/executors.go                                 |   37 +-
 nodes/executors_test.go                            |    2 +
 nodes/extract.go                                   |  250 +-
 nodes/extract_test.go                              |   90 +
 nodes/flow.go                                      |   21 +-
 nodes/glyphs_test.go                               |  151 +
 nodes/google.go                                    |    2 +-
 nodes/http.go                                      |   96 +-
 nodes/http_error_test.go                           |  235 ++
 nodes/item_inputs_test.go                          |  112 +
 nodes/jscode.go                                    |  319 +-
 nodes/jscode_helpers.go                            |  272 ++
 nodes/jscode_helpers_test.go                       |  270 ++
 nodes/jscode_lineage_test.go                       |  207 ++
 nodes/jscode_roots.go                              |   49 +
 nodes/jscode_roots_test.go                         |  177 +
 nodes/jscode_run_test.go                           |  361 ++
 nodes/jscode_test.go                               |   41 +-
 nodes/model_redirect_internal_test.go              |  189 +
 nodes/mysql_v2.go                                  |    2 +-
 nodes/openai_credential_url_test.go                |   54 +
 nodes/pgvector.go                                  |   48 +-
 nodes/pgvector_customer.go                         |   30 +-
 nodes/pgvector_test.go                             |    4 +-
 nodes/postgres_v2.go                               |    2 +-
 nodes/rag.go                                       |   13 +
 nodes/rag_test.go                                  |    6 +-
 nodes/sidecar_egress.go                            |   28 +-
 nodes/sort_code_test.go                            |  206 ++
 nodes/sqlite_attach_test.go                        |    4 +-
 nodes/subworkflow.go                               |   47 +-
 nodes/telegram_download.go                         |   15 +-
 nodes/telegram_lifecycle.go                        |   92 +-
 nodes/testdata/n8n_chat_model_options.json         |    3 +-
 nodes/tool_fromai.go                               |  151 +
 nodes/transform.go                                 |  127 +-
 nodes/vectorstore_retrieve.go                      |   84 +
 nodes/vectorstore_retrieve_test.go                 |  250 ++
 nodes/vectorstore_tool.go                          |  325 ++
 nodes/vectorstore_tool_export_test.go              |   20 +
 nodes/vectorstore_tool_security_test.go            |  208 ++
 nodes/vectorstore_tool_test.go                     |  565 +++
 nodes/webhook.go                                   |    3 +
 nodes/workflow_tool_locator_test.go                |   50 +
 packs/telegram/default_base_url_test.go            |   32 +
 packs/telegram/redaction_test.go                   |   43 +
 packs/waha/waha_test.go                            |   55 +-
 schemas/workflow-v1.schema.json                    |   24 +-
 scripts/cli-examples/main.go                       |  583 +++
 scripts/cli-examples/main_test.go                  |  187 +
 scripts/cli-examples/process_other.go              |   14 +
 scripts/cli-examples/process_test.go               |  110 +
 scripts/cli-examples/process_unix.go               |   35 +
 scripts/cli-examples/seed.go                       |  275 ++
 scripts/cli-examples/server.go                     |  183 +
 scripts/cli-examples/testdata/credential.json      |    5 +
 scripts/cli-examples/testdata/patch.json           |   19 +
 scripts/cli-examples/testdata/payload.json         |    1 +
 scripts/cli-examples/testdata/wf.json              |    9 +
 scripts/cli-examples/testdata/workflow.json        |   28 +
 scripts/code-corpus-sync.sh                        |  312 ++
 scripts/docs-facts/main.go                         |  177 +
 scripts/docs-facts/main_test.go                    |  278 ++
 scripts/generate-api-reference.mjs                 |   51 +-
 scripts/internal/splice/splice.go                  |  149 +
 scripts/internal/splice/splice_test.go             |  112 +
 scripts/js-diff/harness.mjs                        |  310 ++
 scripts/js-parity/record-engine.mjs                |  341 ++
 scripts/js-parity/record.mjs                       |  756 ++++
 scripts/skills-command-reference/main.go           |  112 +-
 scripts/smoke-cli.sh                               |   10 +-
 sdk/CHANGELOG.md                                   |    7 +
 sdk/examples/reference-host/server.mjs             |   12 +-
 sdk/src/generated/models.ts                        |  500 ++-
 sdk/src/server.ts                                  |   20 +-
 sidecar/doc.go                                     |    2 +-
 sidecar/runner_test.go                             |   15 +-
 .../kf-fixture-versions/dist/nodes/Foo.node.js     |    2 +-
 skills/index.json                                  |   21 +-
 skills/kilasflow-credentials/SKILL.md              |    6 +-
 .../references/CREDENTIAL_TYPES.md                 |   28 +-
 skills/kilasflow-datastore/SKILL.md                |    9 +-
 skills/kilasflow-datastore/references/FILTERS.md   |    6 +-
 skills/kilasflow-debugging/SKILL.md                |    8 +-
 skills/kilasflow-embedding/SKILL.md                |    9 +-
 .../references/EMBED_HANDSHAKE.md                  |    2 +
 .../references/SESSION_AUTHORITY.md                |    6 +-
 skills/kilasflow-error-handling/SKILL.md           |    6 +-
 skills/kilasflow-expressions/SKILL.md              |   12 +-
 .../references/EXPRESSION_ROOTS.md                 |    2 +-
 skills/kilasflow-import-export/SKILL.md            |   15 +-
 .../references/MAPPING_LIMITS.md                   |   57 +-
 skills/kilasflow-operations/SKILL.md               |    4 +-
 skills/kilasflow-triggers/SKILL.md                 |    4 +-
 skills/using-kilasflow-skills/SKILL.md             |    4 +-
 third_party/lodash/LICENSE                         |   47 +
 third_party/lodash/PROVENANCE.md                   |   32 +
 third_party/lodash/embed.go                        |   15 +
 third_party/lodash/lodash.min.js                   |  136 +
 third_party/luxon/LICENSE                          |    7 +
 third_party/luxon/PROVENANCE.md                    |   34 +
 third_party/luxon/embed.go                         |   15 +
 third_party/luxon/luxon.min.js                     |    1 +
 third_party/waha/PROVENANCE.md                     |    5 +-
 web/messages/en/auth.json                          |    2 +
 web/messages/en/credentials.json                   |    3 +
 web/messages/en/datastores.json                    |   48 +-
 web/messages/en/editor.json                        |   49 +-
 web/messages/en/executions.json                    |  193 +-
 web/messages/en/nav.json                           |    2 +-
 web/messages/en/properties.json                    |    9 +-
 web/messages/en/settings.json                      |    9 +-
 web/messages/en/workflows.json                     |   25 +-
 web/messages/id/auth.json                          |    2 +
 web/messages/id/credentials.json                   |    3 +
 web/messages/id/datastores.json                    |   48 +-
 web/messages/id/editor.json                        |   59 +-
 web/messages/id/executions.json                    |  193 +-
 web/messages/id/nav.json                           |    2 +-
 web/messages/id/properties.json                    |    9 +-
 web/messages/id/settings.json                      |    9 +-
 web/messages/id/workflows.json                     |   25 +-
 web/package.json                                   |   10 +
 web/pnpm-lock.yaml                                 |  160 +-
 web/src/app.css                                    |   52 +
 .../lib/api/generated/credentials/credentials.ts   |    6 +-
 web/src/lib/api/generated/executions/executions.ts |    4 +-
 web/src/lib/api/generated/interop/interop.ts       |  103 +-
 .../api/generated/models/aIAgentCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIAgentFailedEvent.ts |   24 +
 .../api/generated/models/aIModelCompletedEvent.ts  |   24 +
 .../lib/api/generated/models/aIModelDeltaEvent.ts  |   24 +
 .../api/generated/models/aIModelStartedEvent.ts    |   24 +
 .../api/generated/models/aIToolCompletedEvent.ts   |   24 +
 .../lib/api/generated/models/aIToolFailedEvent.ts  |   24 +
 .../lib/api/generated/models/aIToolStartedEvent.ts |   24 +
 .../lib/api/generated/models/codeConsoleEvent.ts   |   24 +
 .../generated/models/convertFragmentInputBody.ts   |   16 +
 .../generated/models/convertedFragmentResource.ts  |   21 +
 web/src/lib/api/generated/models/credentialBody.ts |    4 +-
 .../api/generated/models/credentialBodyFields.ts   |    2 +-
 .../lib/api/generated/models/credentialResource.ts |    5 +-
 .../api/generated/models/credentialTypeResource.ts |    9 +
 web/src/lib/api/generated/models/document.ts       |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |    2 +-
 .../generated/models/evalExpressionInputBody.ts    |   12 +-
 .../generated/models/executionNodeRunResource.ts   |    2 +
 .../lib/api/generated/models/executionResource.ts  |    3 +
 .../lib/api/generated/models/healthOutputBody.ts   |    2 +
 web/src/lib/api/generated/models/index.ts          |   16 +
 .../api/generated/models/listWorkflowsParams.ts    |    5 +
 web/src/lib/api/generated/models/otherEvent.ts     |   24 +
 .../models/streamExecutionEvents200Item.ts         |   99 +
 .../lib/api/generated/models/testPayloadBody.ts    |    4 +-
 web/src/lib/api/generated/models/typeOptions.ts    |    4 +
 .../lib/api/generated/models/typeOptionsEditor.ts  |   14 +
 .../generated/models/typeOptionsEditorLanguage.ts  |   17 +
 .../generated/models/validateWorkflowResource.ts   |    6 +
 .../lib/api/generated/models/validationWarning.ts  |   13 +
 .../api/generated/models/webhookResponseEvent.ts   |   24 +
 .../api/generated/models/workflowDocumentInput.ts  |    6 +
 .../lib/api/generated/models/workflowSummary.ts    |    2 +
 web/src/lib/api/http.ts                            |   32 +-
 .../components/dashboard/workflow-tag-chips.svelte |   27 +
 .../lib/components/ui/dialog/dialog-content.svelte |    2 +-
 .../workflow-editor/canvas-bridge.svelte           |    3 +-
 .../workflow-editor/canvas-chat-panel.svelte       |  381 +-
 .../components/workflow-editor/canvas-node.svelte  |  110 +-
 .../workflow-editor/chat-markdown.svelte           |   38 +
 .../components/workflow-editor/code-editor.svelte  |   92 +
 .../workflow-editor}/diagnostics-section.svelte    |    0
 .../workflow-editor/execution-canvas-node.svelte   |   45 +-
 .../workflow-editor/execution-canvas.svelte        |    6 +-
 .../workflow-editor}/import-report.svelte          |    0
 .../components/workflow-editor/node-console.svelte |   27 +
 .../workflow-editor/node-console.test.ts           |   70 +
 .../components/workflow-editor/node-picker.svelte  |   22 +-
 .../workflow-editor/panel-query-harness.svelte     |   19 +
 .../workflow-editor/paste-report-sheet.svelte      |   45 +
 .../workflow-editor/properties-panel.svelte        |  180 +-
 .../workflow-editor/properties-panel.test.ts       |  174 +
 .../workflow-editor/property-field.svelte          |   28 +-
 .../workflow-editor/property-field.test.ts         |   50 +
 .../workflow-editor/version-panel.svelte           |   15 +-
 .../workflow-editor/webhook-address.svelte         |   91 +
 .../workflow-editor/webhook-address.test.ts        |   63 +
 .../workflow-editor/workflow-editor.svelte         |  464 ++-
 web/src/lib/dashboard/auth-mode.test.ts            |   65 +
 web/src/lib/dashboard/auth-mode.ts                 |   79 +
 web/src/lib/dashboard/credential-scope.test.ts     |   34 +
 web/src/lib/dashboard/credential-scope.ts          |   31 +
 web/src/lib/dashboard/credential-test.test.ts      |   45 +
 web/src/lib/dashboard/credential-test.ts           |   27 +
 web/src/lib/dashboard/sidebar.test.ts              |   34 +
 web/src/lib/dashboard/sidebar.ts                   |   35 +
 web/src/lib/dashboard/workflow-list.test.ts        |  138 +-
 web/src/lib/dashboard/workflow-list.ts             |   99 +-
 web/src/lib/datastore/columns.test.ts              |   36 +
 web/src/lib/datastore/columns.ts                   |   30 +
 web/src/lib/datastore/transfer.test.ts             |   33 +
 web/src/lib/datastore/transfer.ts                  |   39 +-
 web/src/lib/embed/embed-editor.svelte              |   94 +-
 web/src/lib/embed/session.svelte.ts                |   68 +-
 web/src/lib/embed/session.test.ts                  |  118 +-
 web/src/lib/workflow-editor/authoring.test.ts      |    6 +-
 web/src/lib/workflow-editor/canvas-actions.ts      |    2 +
 web/src/lib/workflow-editor/canvas-chrome.test.ts  |   96 +
 web/src/lib/workflow-editor/canvas-chrome.ts       |   62 +
 .../lib/workflow-editor/canvas-projection.test.ts  |  147 +
 web/src/lib/workflow-editor/catalog.test.ts        |   12 +
 web/src/lib/workflow-editor/catalog.ts             |   10 +-
 web/src/lib/workflow-editor/chat-markdown.test.ts  |  110 +
 web/src/lib/workflow-editor/chat-markdown.ts       |  211 ++
 web/src/lib/workflow-editor/chat-stream.test.ts    |   70 +
 web/src/lib/workflow-editor/chat-stream.ts         |  112 +
 web/src/lib/workflow-editor/chat.test.ts           |   69 +-
 web/src/lib/workflow-editor/chat.ts                |   86 +-
 web/src/lib/workflow-editor/clipboard.test.ts      |  219 +-
 web/src/lib/workflow-editor/clipboard.ts           |  276 +-
 web/src/lib/workflow-editor/code-editor.test.ts    |   90 +
 web/src/lib/workflow-editor/code-editor.ts         |  264 ++
 web/src/lib/workflow-editor/document.test.ts       |   73 +-
 web/src/lib/workflow-editor/document.ts            |  265 +-
 web/src/lib/workflow-editor/event-stream.svelte.ts |   79 +-
 web/src/lib/workflow-editor/event-stream.test.ts   |   41 +-
 .../lib/workflow-editor/execution-watch.test.ts    |   58 +
 web/src/lib/workflow-editor/execution-watch.ts     |   56 +
 web/src/lib/workflow-editor/execution.test.ts      |  169 +-
 web/src/lib/workflow-editor/execution.ts           |  181 +-
 .../lib/workflow-editor/expression-assist.test.ts  |    7 +
 web/src/lib/workflow-editor/expression-assist.ts   |   12 +-
 web/src/lib/workflow-editor/glyphs.json            |   57 +
 web/src/lib/workflow-editor/layout.ts              |    9 +-
 web/src/lib/workflow-editor/node-visual.test.ts    |  126 +-
 web/src/lib/workflow-editor/node-visual.ts         |  165 +-
 web/src/lib/workflow-editor/picker-rows.test.ts    |  100 +
 web/src/lib/workflow-editor/picker-rows.ts         |   38 +
 web/src/lib/workflow-editor/ports.test.ts          |   37 +
 web/src/lib/workflow-editor/ports.ts               |   38 +-
 web/src/lib/workflow-editor/save-feedback.test.ts  |   52 +
 web/src/lib/workflow-editor/save-feedback.ts       |   41 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |   94 +-
 web/src/lib/workflow-editor/shortcuts.ts           |   87 +-
 web/src/lib/workflow-editor/sticky.test.ts         |   45 +-
 web/src/lib/workflow-editor/sticky.ts              |   87 +-
 web/src/lib/workflow-editor/validation.ts          |    5 +-
 .../lib/workflow-editor/webhook-address.test.ts    |  151 +
 web/src/lib/workflow-editor/webhook-address.ts     |   82 +
 web/src/lib/workflow-editor/workflow-input.ts      |   27 +
 web/src/routes/(dashboard)/+layout.svelte          |   40 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |   81 +-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  131 +-
 .../app/workflows/[id]/export-dialog.svelte        |    2 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |    2 +-
 .../app/workflows/import-report-drawer.svelte      |    2 +-
 .../routes/(dashboard)/credentials/+page.svelte    |   58 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |   88 +-
 .../(dashboard)/executions/[id]/+page.svelte       |  245 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |    2 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |   32 +-
 web/src/routes/+page.svelte                        |    6 +-
 web/src/routes/approve/[token]/+page.svelte        |    8 +
 web/src/routes/login/+page.svelte                  |   70 +-
 1059 files changed, 140963 insertions(+), 4674 deletions(-)
```
