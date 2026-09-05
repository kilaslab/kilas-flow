# Session checkpoint

Written 2026-09-06 02:45 local, at the stop time the operator scheduled.

## Where things stand

- `main` is at `c0d329d` — fix(config): apply the operator's egress policy to edit-time option loading
- Working tree clean; no agent worktrees left (all merged and removed).
- Full Go suite green against live PostgreSQL 16, MySQL 8 and MariaDB 11; `go vet` and `gofmt` clean; web suite and `pnpm check` green.
- Epic EPIC-m42s3g: 

## Shipped this session

- c0d329d fix(config): apply the operator's egress policy to edit-time option loading
- faa88dd merge: make the PostgreSQL tier real: pooling, indexes and retention (FEAT-5fv8gf)
- 470cfe2 merge: run a local Ollama model as the AI runtime for tests (FEAT-kwxxd0)
- da4f349 fix(interop): stop the export losing a node's retry policy and the timezone
- 0febf82 fix(repository): let a PostgreSQL execution finish
- ba5ff72 fix(sql): refuse SQL a database node was never asked to send
- e8f1d67 merge: write the architecture and concepts documentation (FEAT-5mvech)
- 821f6d1 merge: ship a five-minute Compose quickstart (FEAT-m94hhx)
- 946c8bf merge: expose version history, diff and restore in the editor (FEAT-1500sp)
- 5793c01 merge: write the n8n migration guide (FEAT-zmfsjd)
- 9c67fde merge: give every API request an owner and a real tenant (FEAT-ddzk2k)
- dda9ba3 merge: stand up the documentation site on Astro Starlight (FEAT-nxxbs5)
- 41adee0 merge: store workflow version history and pin the published version (FEAT-ajw7wt)
- 9cb4b3d merge: adopt the cluster-node model for AI sub-nodes (FEAT-ybm2pd)
- 03ef24f fix(release): stop a git tag from running commands in the release path
- 0dc69b2 merge: publish multi-architecture container images (FEAT-53fht8)
- 6863653 merge: stop the code node's time limit paying for wasm translation (BUG-9s3htg)
- 94a487d merge: add OpenAI and OpenRouter chat model nodes (FEAT-mvegj5)
- c87b5c8 merge: move the schema from struct reflection into migration files (FEAT-gvn62x)
- 5c0a7a9 merge: show what activation could not do for the user (FEAT-sdjdh2)
- a8d292f fix(interop): stop the database nodes losing what an n8n workflow said
- 6c277dd merge: move the V2 roadmap into the repository (FEAT-xx6p22)
- 975d8e7 merge: keep the loaded rows when a later request fails (FEAT-a7p1b2)
- 95eaade merge: run the checks this repository already defines (FEAT-7tgasa)
- e10bd4e feat(nodes): give the database nodes n8n's options collection and query batching
- 3a211ca merge: give the dashboard lists one shell and one table (FEAT-ptyh9w)
- 479a9f1 feat(nodes): bring the MySQL node to n8n's operation set
- bc54b2a merge: correct the documentation of record (FEAT-sfy1tq)
- e8c9bc8 fix(interop): import IS NULL as the null test it means
- bf2d82e feat(nodes): bring the PostgreSQL node to n8n's operation set
- 31839b5 feat(loadoptions): introspect database schemas for table and column pickers
- eb3f2e0 feat(registry): add the resource mapper kind for column mapping
- 771bce4 feat(registry): add the resource locator kind and an internal list seam
- d099d1f feat(nodes): decide and deliver the Code node compatibility story
- 5d79a85 feat(engine): reach parity on the workflow-composition node family
- 8a8279c feat(nodes): reach parity on the time and scheduling node family
- 360a8db feat(api): test a database credential before a workflow runs
- ff19e57 feat(nodes): close the five real gaps in the SQL node
- 1a43b04 feat(nodes): reach parity on the data-shaping node family
- c94583a feat(nodes): reach parity on the flow-control node family
- ddfc94c fix(workflow): enforce required credentials at compile time
- c38dcdc feat(nodes): add the assignment collection kind and move Set onto it
- 053214b fix(nodes): resolve expressions in the Set and IF nodes
- 109ff8f feat(packs): add the Telegram action node at n8n parity
- 21056a1 feat(nodes): add the Telegram trigger with self-registering webhooks
- a7c2fb9 feat(interop): map WAHA workflows through the n8n importer
- ab8c261 feat(packs): add the WAHA trigger with per-event outputs
- b79bab3 feat(packs): ship the WAHA node pack for both published versions
- f3cb58e feat(nodepack): generate node packs from an OpenAPI document
- cdf3a55 feat(routing): interpret declarative node routing in Go
- cc61e99 feat(engine): store binary payloads behind the item contract
- 87bfa19 feat(editor): serve node icons and delete the hardcoded editor maps
- 172c7b6 feat(registry): tag entries by source and prove executor bindings
- 002ce3b feat(node): serve dynamic property options from the server
- c2161a0 feat(node): bring conditional property visibility to n8n parity
- 17bae26 feat(credentials): open the credential type registry to node declarations
- 1b474ec feat(registry): extend the node property model to the kinds real nodes need
- f977a94 feat(workflow): describe ports fully and widen the connection kinds
- e7c22c2 feat(webhook): drive bindings from the catalogue and add trigger lifecycle hooks
- 2c2c607 feat(registry): add presentation metadata to the node definition
- b217b82 feat(expression): extend the grammar to n8n's evaluation semantics
- 90995aa feat(engine): support bounded loops for batch iteration
- 765a764 feat(engine): track paired-item lineage and run index
- 10c41c2 feat(webhook): shape trigger payloads by node type and keep the raw body
- 4816f2e feat(webhook): mint opaque routes per binding and dedupe retried deliveries
- aa396aa feat(engine): honour continueOnFail, retryOnFail and maxTries
- 3bd1309 feat(interop): report every element the n8n importer drops
- 29074c6 fix(engine): never run the branch that was not taken
- 2fef7c7 fix(execution): stop redaction standing between the wire and the runtime
- d4dc81a feat(engine): allow several trigger roots and run only the one that fired
- afd8c92 feat(interop): import and export n8n's AI connections
- b34aa12 feat(registry): represent node type versions as decimals
- bba40aa feat(interop): add the Sticky Note node and make the capsule lossless

## Still open

    ○ EPIC-m42s3g ● high [epic] [p0] KilasFlow V2 — n8n-first workflow compatibility (69/122)
    ├── ○ FEAT-cx3hq1 ● high [p11] Stand up the browser end-to-end harness
    ├── ○ FEAT-27km39 ● high [p10] Write the operator guide and generate the configuration reference
    ├── ○ FEAT-bscygc ● high [p10] Declare the public API contract and its stability rules
    ├── ○ FEAT-4d0bje ● high [p6] Apply a network policy and domain scoping to database targets
    ├── ○ FEAT-096vs9 ● high [p5] Give chat memory real session semantics and retention
    ├── ○ FEAT-96p7m3 ● high [p5] Add the Basic LLM Chain node
    ├── ○ FEAT-cgm1y3 ● high [p5] Bring the AI Agent node to n8n parity
    ├── ◑ FEAT-ptyh9w ● medium [p9] Extract a shared list page shell and a table primitive
    ├── ○ FEAT-0556ck ● medium [p10] Put n8n import and export in the editor
    ├── ○ FEAT-r6xhnp ● medium [p6] Prefix every table and index for shared databases
    ├── ○ FEAT-knpfqf ● low [p8] Integrate external secrets managers
    ├── ○ FEAT-48hreg ● low [p8] Publish a native community module SDK on WebAssembly
    ├── ○ FEAT-rj17xj ● low [p8] Add human approval with durable wait and resume
    └── ○ FEAT-7cg0cd ● low [p8] Run programmatic community nodes in a JavaScript sidecar
    ○ BUG-v6tdjr ● medium [bug] Nine doc comments describe behaviour the code does not have
    
    16 tickets
    Status: ○ todo  ◐ doing  ◑ testing  ● blocked  ✓ done

## What the next session should know

Three things that cost real time tonight and are worth not rediscovering.

**A check that silently stops checking is worse than no check.** Three
separate instances: `npx svelte-check` without `--tsconfig` reports zero errors
over 1300 files while checking nothing; a test helper skipped when a security
guard regressed, because a policy refusal looked like "nothing is listening";
and a `generousLimits()` 120-second helper hid a real timeout defect until CI
existed. Verify a check catches something by planting a failure once.

**Tickets here are frequently stale, and so are code comments.** Almost every
ticket opened tonight cited line numbers that had moved, and several described
code that no longer existed or recommended a library that breaks the build.
BUG-v6tdjr records nine doc comments that describe behaviour the code lacks;
three are checked, and one of those three turned out to be the *code* that was
wrong, not the comment. Read the function before the comment.

**SQLite and PostgreSQL disagree in ways only PostgreSQL reveals.** A `CASE` of
untyped placeholders that SQLite accepts, PostgreSQL rejects at parse time —
which silently stopped every execution finishing and re-ran every workflow once
a minute. There is now an `eachDriver` helper in
`internal/repository/postgres_execution_test.go`; use it for anything touching
SQL. Note the hazard recorded in `.pine/memory/persistence.md`: `internal/database`
drops every table while `internal/repository` expects them to stay, so the
PostgreSQL-gated suite needs `-p 1`.

## Suggested order from here

1. **BUG-v6tdjr** — six candidate doc comments still unchecked. Each may be a
   comment fix or, as one already was, a real defect.
2. **Wave C, serialised** — FEAT-96p7m3, then FEAT-cgm1y3, then FEAT-096vs9.
   All three write `nodes/ai.go` and `internal/ai/`; running them in parallel
   will conflict.
3. **FEAT-4d0bje** — network policy for database targets. Held back all session
   because it shares files with the SQL guard work, which is now committed.
4. **FEAT-r6xhnp** — alone, with nothing else touching migrations or the
   repository. It renames every table.
5. Everything else in `pine ready` is parallelisable on disjoint areas.
