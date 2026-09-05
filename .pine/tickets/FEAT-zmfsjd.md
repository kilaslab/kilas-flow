---
id: FEAT-zmfsjd
title: Write the n8n migration guide
status: todo
priority: medium
labels:
    - docs
    - interop
deps:
    - FEAT-nxxbs5
parent: EPIC-m42s3g
phase: p10
created: "2026-09-05T11:59:46Z"
updated: "2026-09-05T11:59:46Z"
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

- [ ] A user with an n8n export can determine, before importing, whether their workflow will work, from the documented mapping list and the diagnostic vocabulary.
- [ ] The 17 supported mappings are documented and generated from `SupportedMappings()` rather than transcribed, so the list cannot drift from the code.
- [ ] The unsupported capsule is explained as intended behaviour, including why an imported workflow containing one saves and opens but does not activate.
- [ ] The three diagnostic severities are documented with a stated action for each.
- [ ] Webhook re-pointing is documented with the minted URLs shown in context, and the reason a hardcoded template path cannot be reused is explained.
- [ ] Credential re-binding is documented, including that `{id, name}` from the source instance is deliberately not trusted.
- [ ] The two expression dialects are documented with a worked example of the same expression in both.
- [ ] The corpus baseline is published as a measured statement of current fidelity, with the date, the fixture count, the tier definitions, and a plain description of what the failing tier fails on.

## Implementation Plan

Generate the mapping table from `SupportedMappings()`. It already returns sorted `"<n8nType> ↔ <kilasType>"` strings for exactly this purpose, and a hand-copied table in the documentation would be stale the first time p4 or p5 adds a mapping.

Structure the guide around the user's sequence rather than the code's: export from n8n, import, read the diagnostics, recreate credentials, re-point webhooks, activate, verify. Each step gets what can go wrong and how to tell.

Publish the corpus baseline carefully, and treat its framing as part of the ticket rather than presentation. The tiers must be defined before the numbers are shown, the date and fixture count must be adjacent to the score, and the failing cases need a sentence saying what class of thing they fail on. A number without those three things invites exactly the comparison this project should not be inviting.

There is a real constraint on when the numbers can be published. The corpus is deliberately gitignored and digest-pinned — the WAHA templates repository carries no licence file and the GitHub API reports `license: null`, which is stricter than the SUL rather than looser, so those fixtures are never committed. The guide may publish **aggregate scores** and describe failure classes; it must not publish fixture contents. Say so in the ticket so nobody solves the reproducibility question by committing the corpus.

Write against what ships on the day of writing. Almost every p1 ticket changes import fidelity — branch pruning, pairedItem lineage, multiple trigger roots, error-handling settings, per-trigger payload shaping, per-tenant webhook paths, the lossless capsule, the expression engine, float typeVersion, import diagnostics, AI edges. A guide describing p1's finished state as present tense would be wrong for however long p1 takes. State the current position, date it, and link the tickets that move it.

One thing to decide and state: whether the guide recommends importing into a fresh tenant per source instance. It should. `FEAT-...` in p1 makes webhook paths per-tenant precisely so the same template can serve two clients, and that capability is invisible unless the guide tells a migrating agency it exists.

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
