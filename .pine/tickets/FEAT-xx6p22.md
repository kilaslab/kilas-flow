---
id: FEAT-xx6p22
title: Move the V2 roadmap into the repository
status: done
priority: medium
labels:
    - datastore
    - storage
    - api
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T15:57:52Z"
---

## Scope

`.pine/roadmap.md` exists on disk, and when this ticket was filed `git status` reported it as `?? .pine/roadmap.md` — untracked, and byte-identical to the plan file it had been copied from under `~/.claude/plans/`. Until it was committed, the roadmap of record for the whole V2 programme lived at a home-directory path: outside the repository, outside version control, and inside a directory any plan-mode session is free to rewrite. It was in fact overwritten during the planning session that produced it and reconstructed by hand — the failure this ticket exists to prevent a second time. The file has since been committed by an earlier ticket and edited in place, so the two copies are no longer the same document and the in-repo one is the roadmap of record; see `## Work evidence`.

Sixty-three committed ticket bodies cite that path, one bullet each, always the first bullet under `## References` — sixty-two `FEAT-*` bodies plus `.pine/tickets/EPIC-m42s3g.md`. (The roadmap says "all 62 first-reference bullets"; it counts the features and omits the epic, which carries the same defect.) Sixteen already use the current convention — fourteen in the exact form `- Roadmap plan, pN section, entry V2-pN-M:` followed by the path, as at `.pine/tickets/FEAT-r6xhnp.md:50`, and two opening `- Roadmap plan: ` with the section and entry trailing instead. The other forty-seven use an older wording opening `- Plan:` or `- Plan ` and trailing a section name or a list of related entries, as at `.pine/tickets/FEAT-k3grr5.md:50`. The path is spelled two ways — thirty-eight absolute, twenty-five tilde-relative — and no file outside `.pine/` mentions it.

`pine update` changes fields and leaves the body untouched by design, so no Pine command repoints these; each is a markdown edit to a committed file.

One further corpus-wide untruth belonged here, being the same class of defect: a ticket body asserting something the repository does not contain. Eight bodies claimed a check "runs in CI" — `FEAT-gvn62x.md`, `FEAT-qcm5ec.md`, `FEAT-je4f4t.md`, `FEAT-jwhdsy.md`, `FEAT-ddzk2k.md`, `FEAT-csqgg5.md`, `FEAT-48hreg.md` and `EPIC-c7gbdp.md` — at a time when there was no `.github` directory and no CI configuration of any kind here. `FEAT-7tgasa` has since landed `.github/workflows/ci.yml`, which reverses the answer for most of them, so this half of the ticket became a verification rather than a rewording; the per-body result is under `## Work evidence`.

This matters because the V2 epic is now ninety tickets deep and every one of them delegates its rationale to a document a fresh clone cannot open. A roadmap outside version control cannot be reviewed in a diff, cannot be bisected, and cannot be trusted to still say what the ticket citing it was written against.

## Acceptance criteria

- [x] `.pine/roadmap.md` is tracked, so `git ls-files .pine/roadmap.md` returns it. **Restated:** it was already committed by an earlier ticket before this one ran, so the roadmap does not ship in the same commit as the tickets that explain it. The clause this criterion exists to secure — the roadmap is in version control, diffable and bisectable — holds.
- [x] A `grep -rn` for the old home-directory plan filename over the working tree returns no hit, with the empty output captured as evidence on this ticket.
- [x] The sixty-two `FEAT-*` bodies open their References with the single form `- Roadmap plan, pN section, entry V2-pN-M: ` and the repo-relative path — **fifty-seven of them.** The five p4 family tickets take the entry-less variant instead, because the roadmap numbers p4 from `V2-p4-6` and never assigns an id to the five family tickets that precede it; minting `V2-p4-1…5` would be the same class of untruth this ticket exists to remove. Both counts confirmed by grep.
- [x] `.pine/tickets/EPIC-m42s3g.md` opens its References with the entry-less variant of that form, since the epic names no `V2-pN-M` entry of its own. It takes the section-less form `- Roadmap plan: ` as well, because the epic spans p0–p11 and names no single section either.
- [x] Every trailing clause carried by the forty-seven older `- Plan:` bullets survives as a later reference bullet, so no cross-reference to another entry is lost.
- [x] **Obsolete as written, replaced by a verification.** `FEAT-7tgasa` landed `.github/workflows/ci.yml` before this ticket ran and did the sweep itself: six claims were verified against the workflow and left standing, two were corrected. All eight were re-verified here against the workflow and the Make targets it calls, and none needed changing.
- [x] `pine doctor` passes and `git status` shows the change confined to `.pine/`, both captured as evidence.
- [x] Every p9 ticket written after this one cites the repo-relative path from the outset, so the corpus never holds two roadmap paths at once.

## Implementation Plan

Commit the file before touching a single citation. The ordering is not cosmetic: repointing first leaves sixty-three bullets aimed at a path that does not yet exist in the tree. `.pine/.gitignore` ignores `attachments/` and nothing else, so `git add .pine/roadmap.md` is the whole of it. **The byte-identity check this paragraph originally asked for is void.** The moment for it passed: the in-repo copy has been committed and edited since, and now carries a whole `p10` section the home-directory copy never had. The two are no longer the same document, the in-repo copy is the roadmap of record, and nothing may be copied back over it.

Script the rewrite rather than editing sixty-three files by hand, and keep the script as evidence. Reject a single blanket `sed` over the corpus: the two path spellings are different strings, so a substitution written against the absolute form succeeds on thirty-eight files and misses the twenty-five tilde-spelled ones, and the grep for the new path then returns thirty-eight hits, which reads as progress. Match instead on the stable filename substring shared by both spellings of the old plan path, and drive the wording normalisation from a table of ticket id to phase and entry rather than parsing the entry out of the prose.

**Which path form.** The candidates are the repo-relative `.pine/roadmap.md` and a workspace-absolute path. Recommend repo-relative: every second-and-later reference bullet in the corpus is already repo-relative — `internal/database/database.go`, `web/src/lib/workflow-editor/node-visual.ts` — so an absolute first bullet would be the only path in any ticket that breaks when the repository is cloned elsewhere.

The trap is that nothing validates a reference bullet. `pine doctor` checks workspace structure, not link targets, and a bullet naming a path that does not exist renders exactly like one that does. A half-finished rewrite therefore raises no error anywhere; it surfaces months later, when an agent opens a p4 ticket, cannot find the roadmap, and reconstructs the rationale from the ticket body alone. The only defence is the grep asserted above, run by hand and pasted into the ticket.

Take the CI-claim correction here and leave the other correction this roadmap section parks alongside it. The CI claims are the same defect in the same files and cost eight sentences while the corpus is already open. (In the event `FEAT-7tgasa` reached them first: it built the pipeline and swept the eight claims against it, so what remained here was re-verification, not rewriting.) The PostgreSQL integration-test cleanup at `internal/database/database_test.go:145-150`, which drops only `models[0..3]` and leaks `credentials`, `webhook_bindings` and `schedules`, is a Go change to a test this ticket has no other reason to open; it belongs to V2-p9-1.

**Where the roadmap lives.** Recommend `.pine/roadmap.md`, beside the tickets that cite it and inside the directory `pine context` already treats as the project briefing surface. What would reopen it: a decision to publish the roadmap to customers or render it from a docs site, at which point `docs/` wins and the move costs one commit plus sixty-three bullets again — which is the reason to keep the rewrite script rather than throw it away.

## References

- Roadmap plan, p9 section, entry V2-p9-0: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the roadmap of record, tracked, and no longer the same document as the home-directory copy it was taken from; line 1124 is the V2-p9-0 paragraph that commissioned this ticket.
- `.pine/tickets/EPIC-m42s3g.md` — line 76, the epic's own bullet, the sixty-third citation the roadmap's count of sixty-two omits.
- `.pine/tickets/FEAT-r6xhnp.md` — line 50, one of the sixteen bullets already in the target wording.
- `.pine/tickets/FEAT-k3grr5.md` — line 50, one of the forty-seven older `- Plan:` bullets carrying a trailing section clause that must survive.
- `.pine/.gitignore` — ignores `attachments/` only, so the roadmap is trackable where it already sits.
- `.github/workflows/ci.yml` and `Makefile` — the pipeline and the targets the eight CI claims were re-verified against.
- `.pine/tickets/FEAT-7tgasa.md` — lines 154-183, the sweep of the eight CI claims that this ticket confirms rather than repeats.
- `.pine/tickets/FEAT-gvn62x.md`, `FEAT-qcm5ec.md`, `FEAT-je4f4t.md`, `FEAT-jwhdsy.md`, `FEAT-ddzk2k.md`, `FEAT-csqgg5.md`, `FEAT-48hreg.md`, `EPIC-c7gbdp.md` — the eight bodies that asserted a check runs in CI.
- `.pine/tickets/FEAT-5kfctc.md` — the one p4 body already carrying the target wording and the repo-relative path, so it was outside this rewrite and left untouched.
- `internal/database/database_test.go` — lines 145-150, the cleanup this ticket deliberately leaves to V2-p9-1.

## Work evidence

**Roadmap tracking.** `git ls-files .pine/roadmap.md` returns `.pine/roadmap.md`. The file was committed by an earlier ticket, not by this one, so the first acceptance criterion was already satisfied when the repointing started and has been restated above to say so. The in-repo copy has also diverged from the home-directory copy since — it carries a `p10` section the other never had — so the byte-identity `diff` the Implementation Plan called for is void, and the in-repo copy is the roadmap of record.

**The rewrite.** Sixty-three first-reference bullets were repointed by one script driven from an explicit table of ticket id to phase and entry, matched on the filename substring common to both spellings of the old path rather than on either spelling. Sixty-two `FEAT-*` bodies plus `.pine/tickets/EPIC-m42s3g.md`; `.pine/roadmap.md`'s own line 1126 and three lines of this ticket body were edited by hand.

Forms found before the rewrite, over the sixty-three bullets: thirty-eight `- Plan: `, fourteen already in the target `- Roadmap plan, pN section, entry V2-pN-M: ` form, nine `- Plan ` with no colon, two bare `- Roadmap plan: `. Path spellings: thirty-eight absolute, twenty-five tilde-relative.

```
$ grep -rn <old plan filename> .
(no output; exit 1)

$ git ls-files .pine/roadmap.md
.pine/roadmap.md

$ # over the 62 FEAT bodies this ticket rewrote
  entry form  `- Roadmap plan, pN section, entry V2-pN-M: `.pine/roadmap.md`.`   57
  entry-less  `- Roadmap plan, pN section: `.pine/roadmap.md`.`                   5
$ # corpus-wide, every FEAT body now in the entry form
113
$ grep -rc '^- Plan' .pine/tickets/*.md | grep -v ':0' | wc -l
0
$ pine doctor
✓ config.json and board.json are valid
✓ global memory ~/.pine/MEMORY.md is 666 bytes
✓ no problems found
$ git status --short | grep -cv '^ M \.pine/'
0
```

**Why five p4 bodies take the entry-less variant.** `FEAT-vvwpjw`, `FEAT-jwhdsy`, `FEAT-q81bq4`, `FEAT-az620p` and `FEAT-8qyfh1` are the five p4 family tickets. The roadmap's p4 section opens "Five tickets grouped by family" and names them only by family — flow control, data shaping, time and control, workflow composition, code — then numbers the newer database work from `V2-p4-6`. No `V2-p4-1` … `V2-p4-5` label exists anywhere in `.pine/`, so an entry id was not available to cite and minting one would have manufactured exactly the untraceable pointer this ticket exists to remove. Each of the five carries its family name in the reference bullet immediately below the first, alongside the cross-references its old bullet trailed. `.pine/tickets/FEAT-a7p1b2.md:36` is the standing precedent for the entry-less form.

**Carried clauses.** Twenty-two of the sixty-three old bullets trailed a section name or a list of related entries. Each became a second reference bullet directly under the first, so no cross-reference was dropped: for example `FEAT-096vs9` keeps "p1 entry V2-p1-6 (redaction) and p6 entry V2-p6-6 for the durable tier", and `FEAT-ej0468`'s "the epic's decision table row on AI nodes" became a bullet naming `.pine/tickets/EPIC-m42s3g.md`. The remaining forty-one bullets named only the ticket's own entry, which the canonical form carries in full.

**The eight CI claims, re-verified against `.github/workflows/ci.yml`.** The acceptance criterion asking that no body assert a check runs in CI predates the pipeline. `FEAT-7tgasa` landed it in commit `679afb7` and swept the eight claims itself. All eight were checked again here; none needed changing.

Six are true and stand:

- `FEAT-qcm5ec:26`, `FEAT-je4f4t:47`, `FEAT-jwhdsy:77`, `FEAT-ddzk2k:46` and `EPIC-c7gbdp:71` all say `generate:api:check` or the SDK's type check fails on drift in CI. The `drift` job runs `make generate-api-check` (→ `cd web && pnpm generate:api:check`) and `make generate-types-check` (→ `cd sdk && pnpm generate:types:check`, which is `sdk/scripts/check-types.mjs`) on every pull request.
- `FEAT-gvn62x:36` says a models-versus-migrations test would catch drift in CI. The `test` job runs `make test` → `go test ./... -race`, so a Go test is caught in CI once written. The criterion is still unticked because the test does not exist yet; the claim about where it would run is now accurate.

Two had already been corrected by `FEAT-7tgasa` and needed nothing here:

- `FEAT-csqgg5:41` now says only the committed control fixtures are scored in CI and that the full baseline is a `make corpus-baseline` run by hand. That matches the workflow, which runs `make corpus-check` under a step named "n8n corpus (control fixtures only; baseline not verifiable in CI)".
- `FEAT-48hreg` no longer mentions CI at all; its example-pack claim was reworded to a hand build with the output recorded on the ticket, and no such job exists.

`FEAT-cwz4ac:46` and `FEAT-27km39:47` were left alone, as `FEAT-7tgasa` also concluded: both say a future artefact *should* be checked in CI, which is an instruction for unwritten work rather than an assertion about the present.

**Not touched.** `.pine/tickets/FEAT-5kfctc.md` was being edited by another session and was excluded from the rewrite. It needed no change: its first reference bullet already reads `- Roadmap plan, p4 section, entry V2-p4-12: `.pine/roadmap.md`.`, which is the target form and the repo-relative path.

**The rewrite script** ran from a scratch directory rather than being committed, so that the old filename it matches on cannot itself become a hit for the grep this ticket asserts. It is reproducible from the table in this section: ticket id → phase, entry, carried clause; replace the single line containing the marker, asserting first that it is the only such line in the file and that it is the first bullet under `## References`.
