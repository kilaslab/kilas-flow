---
id: FEAT-zmfsjd
title: Write the n8n migration guide
status: done
priority: medium
labels:
    - docs
    - interop
deps:
    - FEAT-nxxbs5
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:59:46Z"
updated: "2026-09-06T00:53:00Z"
---

## Scope

Migrating from n8n is the reason this whole programme exists, and there is no document describing how to do it or what to expect.

The machinery is real and its honesty is unusual. `internal/interop/n8n` holds 17 mappings, `SupportedMappings()` returns them as an explicitly hand-written list because "interoperability claims are only meaningful if the exact list is written down and testable", and `internal/interop/n8n/corpus/BASELINE.md` scores 39 real fixtures into tiers. An unmappable node becomes `kilasflow.unsupported` — a lossless capsule that keeps the original type, typeVersion and whole original node, renders on the canvas, and whose validator always fails so the draft saves and opens but can never activate. Import produces structured `ImportIssue` diagnostics at three severities: blocking, lossy, dropped.

A migrating user gets none of that explained, and every part of it is something they will hit within minutes:

- **What actually maps.** 17 entries covering the core nodes, Telegram, and both WAHA packages including the legacy unscoped names.
- **What "unsupported" means.** That the workflow imports, opens and cannot activate is a deliberate design, not a failure — and the four arities exist because a node's port count is only knowable from the edges the source drew.
- **The diagnostics vocabulary.** Blocking, lossy and dropped are different and the difference decides what a user must do next.
- **Webhook re-pointing.** Import mints a fresh opaque route per tenant and workflow and returns the new URLs, because n8n templates ship a hardcoded path and importing the same template for a second client would otherwise collide at activation. A migrating user must update the sending system, and will not know to unless told.
- **Credential re-binding.** An n8n credential reference is `{id, name}` and is instance-local, so it is meaningless here; credentials are recreated, not carried.
- **The expression dialects.** n8n marks an expression with a leading `=` on a plain string; KilasFlow uses `{"mode":"expression","value":…}`.
- **What is dropped at the document level.** `settings`, `pinData`, `meta` and `staticData`.

The corpus baseline is the strongest asset here and the easiest to misuse. It reports `imported / activatable / runnable / blocked` counts against real fixtures. Published as a marketing number it is a liability that will age badly; published as an honesty statement — this is what we measure, this is the current score, here is what fails and why — it is the single most credible thing this project can say to somebody deciding whether to migrate.

## Acceptance criteria

- [x] A user with an n8n export can determine, before importing, whether their workflow will work, from the documented mapping list and the diagnostic vocabulary.
- [~] The supported mappings are documented — there are **37**, not 17 — but transcribed rather than generated, because generation needs a build step outside this ticket's scope. The page defers to the live `supportedMappings` field instead. See Work evidence.
- [x] The unsupported capsule is explained as intended behaviour, including why an imported workflow containing one saves and opens but does not activate.
- [x] The three diagnostic severities are documented with a stated action for each.
- [x] Webhook re-pointing is documented with the minted URLs shown in context, and the reason a hardcoded template path cannot be reused is explained.
- [x] Credential re-binding is documented, including that `{id, name}` from the source instance is deliberately not trusted.
- [x] The two expression dialects are documented with a worked example of the same expression in both.
- [x] The corpus baseline is published as a measured statement of current fidelity, with the date, the fixture count, the tier definitions, and a plain description of what the failing tier fails on.

## Implementation Plan

Generate the mapping table from `SupportedMappings()`. It already returns sorted `"<n8nType> ↔ <kilasType>"` strings for exactly this purpose, and a hand-copied table in the documentation would be stale the first time p4 or p5 adds a mapping.

Structure the guide around the user's sequence rather than the code's: export from n8n, import, read the diagnostics, recreate credentials, re-point webhooks, activate, verify. Each step gets what can go wrong and how to tell.

Publish the corpus baseline carefully, and treat its framing as part of the ticket rather than presentation. The tiers must be defined before the numbers are shown, the date and fixture count must be adjacent to the score, and the failing cases need a sentence saying what class of thing they fail on. A number without those three things invites exactly the comparison this project should not be inviting.

There is a real constraint on when the numbers can be published. The corpus is deliberately gitignored and digest-pinned — the WAHA templates repository carries no licence file and the GitHub API reports `license: null`, which is stricter than the SUL rather than looser, so those fixtures are never committed. The guide may publish **aggregate scores** and describe failure classes; it must not publish fixture contents. Say so in the ticket so nobody solves the reproducibility question by committing the corpus.

Write against what ships on the day of writing. Almost every p1 ticket changes import fidelity — branch pruning, pairedItem lineage, multiple trigger roots, error-handling settings, per-trigger payload shaping, per-tenant webhook paths, the lossless capsule, the expression engine, float typeVersion, import diagnostics, AI edges. A guide describing p1's finished state as present tense would be wrong for however long p1 takes. State the current position, date it, and link the tickets that move it.

One thing to decide and state: whether the guide recommends importing into a fresh tenant per source instance. It should. `FEAT-...` in p1 makes webhook paths per-tenant precisely so the same template can serve two clients, and that capability is invisible unless the guide tells a migrating agency it exists.

## Work evidence

Written: `docs/src/content/docs/guides/n8n-migration.md`, replacing the
"not yet written" stub the documentation-site ticket left there. Nothing else
in `docs/` was touched; `docs/astro.config.mjs` is unchanged, and the Guides
sidebar entry autogenerates from the directory.

Verified with `make docs-build` — 17 pages built, link validator reports
"All internal links are valid."

### Ticket premises that were stale, and what is true

- **"17 mappings" is wrong. There are 37.** `TestSupportedMappingsAreAdvertisedExplicitly`
  in `internal/interop/n8n/n8n_test.go` asserts the exact list and it holds 37
  pairs, covering 35 distinct KilasFlow node types (`pack.waha` and
  `pack.wahaTrigger` each have a scoped and an unscoped n8n type string). The
  stub page this replaced said 32, which was also stale. The guide says 37 and
  tells the reader to take the live list from their own instance's
  `supportedMappings` instead of from the page.
- **The four arities are 1, 2, 4 and 8**, and the placeholder's *version number
  is* its port count — `nodes.UnsupportedArities`. Documented as such.
- **The corpus baseline numbers are real but dated.** `BASELINE.md` was last
  regenerated in `d099d1f` (2026-09-05 20:54). Six commits touching
  `internal/interop/n8n` have landed since — the LangChain cluster mappings and
  the PostgreSQL/MySQL operation sets among them — so 13/39 activatable is a
  floor for that date, not a current measurement. The guide dates the number,
  says so plainly, and gives the two commands to re-run it.

### Acceptance criteria

Met, with one deliberate deviation:

- The mapping list is **transcribed and pinned by a test, not generated at
  build time.** Generation would need a Go step in the docs build, which means
  touching `docs/astro.config.mjs`, the `Makefile` or a new script outside
  `docs/src/content/docs/guides/` — all out of this ticket's scope while three
  other sessions are writing in `docs/`. The drift risk is mitigated instead:
  the page states that `supportedMappings` on any export response is the live
  list, gives the `curl | jq` to read it, and says that the instance wins over
  the page. A follow-up can wire real generation.
- Unsupported capsule, three severities with an action each, webhook
  re-pointing with minted URLs shown in an example response, credential
  re-binding including why `{id, name}` is not trusted, both expression
  dialects with the same expression written twice, and the corpus baseline with
  tiers defined before the numbers, the date and fixture count adjacent to the
  score, and a per-class description of the 26 failures: all present.
- The recommendation to import each source instance into its own tenant is
  made, with the per-tenant webhook-label uniqueness and globally unique opaque
  route as the reason.

### Licence boundary

No fixture contents are published — aggregate tier counts and failure classes
only. The one place the guide comes close is the `n8n-nodes-testing.testData`
and `n8n-nodes-base.convertToFile` type names and the WAHA/PostgreSQL credential
types, all of which are already in the committed `BASELINE.md`. Node UUIDs that
appear in `BASELINE.md` were deliberately *not* reused in the worked example;
synthetic ids stand in. The guide states why the corpus cannot be committed so
that nobody later "fixes" reproducibility by committing it.

### Things found that are true and worth someone's attention

- **Export loses a mapped node's error-handling settings, silently.**
  `continueOnFail`, `retryOnFail`, `maxTries` and `waitBetweenTries` are carried
  in on import and honoured by the runner, but `n8n.Export` builds a mapped node
  from type, version, position and parameters only, and raises no `lossy` issue
  for the omission. An unsupported placeholder, by contrast, returns all of them
  from its capsule — so the round trip is lossless for the node that cannot run
  and lossy for the one that can. Documented in the guide as fact; worth a
  ticket.
- **Export never writes `settings`.** `Export` initialises
  `Settings: map[string]any{}` and nothing fills it, so a workflow's timezone —
  which import deliberately does carry, because a dropped zone runs a schedule
  at the wrong hour every day — is not written back, and no diagnostic says so.

## References

- Roadmap plan, p10 section, entry V2-p10-19: `.pine/roadmap.md`.
- `internal/interop/n8n/n8n.go` — `SupportedMappings()`, the `mappings` table, `Import`, `documentIssues`, `ImportIssue` and the severity values.
- `nodes/unsupported.go` — the capsule, `UnsupportedArities`, and the always-failing validator.
- `internal/interop/n8n/parameters.go` — the per-node converters, and the credential-reference handling.
- `internal/interop/n8n/corpus/BASELINE.md` — the tier definitions and the current scores.
- `scripts/corpus-sync.sh` — the digest pinning and the licensing rationale that bounds what may be published.
- `.pine/memory/licensing.md` — the rule that the WAHA templates are never committed.
- `internal/api/handlers/interop.go` — the import response carrying `unsupported` and the minted `webhooks`.
- `internal/repository/webhooks.go` — `mintWebhookRoute` and why a template's hardcoded path cannot be reused.
- `internal/expression/doc.go` — the KilasFlow expression dialect.
