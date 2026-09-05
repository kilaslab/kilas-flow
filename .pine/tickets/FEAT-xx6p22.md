---
id: FEAT-xx6p22
title: Move the V2 roadmap into the repository
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

`.pine/roadmap.md` exists on disk and is byte-identical to `/Users/izzadev/.claude/plans/distributed-worker-nats-crispy-finch.md`, but `git status` reports it as `?? .pine/roadmap.md` — untracked. Until it is committed, the roadmap of record for the whole V2 programme still lives at a home-directory path: outside the repository, outside version control, and inside a directory any plan-mode session is free to rewrite. It was in fact overwritten during the planning session that produced it and reconstructed by hand — the failure this ticket exists to prevent a second time.

Sixty-three committed ticket bodies cite that path, one bullet each, always the first bullet under `## References` — sixty-two `FEAT-*` bodies plus `.pine/tickets/EPIC-m42s3g.md:70`. (The roadmap says "all 62 first-reference bullets"; it counts the features and omits the epic, which carries the same defect.) Sixteen already use the current convention, `- Roadmap plan, pN section, entry V2-pN-M:` followed by the path, as at `.pine/tickets/FEAT-r6xhnp.md:50`; the other forty-seven use an older wording opening `- Plan:` and trailing a section name or a list of related entries, as at `.pine/tickets/FEAT-k3grr5.md:50`. The path is spelled two ways — thirty-eight absolute, twenty-five tilde-relative — and no file outside `.pine/` mentions it.

`pine update` changes fields and leaves the body untouched by design, so no Pine command repoints these; each is a markdown edit to a committed file.

One further corpus-wide untruth belongs here, being the same class of defect: a ticket body asserting something the repository does not contain. Eight bodies claim a check "runs in CI" — `FEAT-gvn62x.md:36`, `FEAT-qcm5ec.md:26`, `FEAT-je4f4t.md:47`, `FEAT-jwhdsy.md:38`, `FEAT-ddzk2k.md:44`, `FEAT-csqgg5.md:41`, `FEAT-48hreg.md:41` and `EPIC-c7gbdp.md:71` — while there is no `.github` directory and no CI configuration of any kind here.

This matters because the V2 epic is now ninety tickets deep and every one of them delegates its rationale to a document a fresh clone cannot open. A roadmap outside version control cannot be reviewed in a diff, cannot be bisected, and cannot be trusted to still say what the ticket citing it was written against.

## Acceptance criteria

- [ ] `.pine/roadmap.md` is tracked, so `git ls-files .pine/roadmap.md` returns it and the roadmap ships in the same commit as the tickets that explain it.
- [ ] A `grep -rn distributed-worker-nats-crispy-finch .` over the working tree returns no hit, with the empty output captured as evidence on this ticket.
- [ ] The sixty-two `FEAT-*` bodies open their References with the single form `- Roadmap plan, pN section, entry V2-pN-M: ` and the repo-relative path, confirmed by a grep counting sixty-two matches.
- [ ] `.pine/tickets/EPIC-m42s3g.md` opens its References with the entry-less variant of that form, since the epic names no `V2-pN-M` entry of its own.
- [ ] Every trailing clause carried by the forty-seven older `- Plan:` bullets survives as a later reference bullet, so no cross-reference to another entry is lost.
- [ ] No ticket body asserts that a check runs in CI; the eight bodies that do today state instead that the check is run by hand and its output recorded on the ticket.
- [ ] `pine doctor` passes and `git status` shows the change confined to `.pine/`, both captured as evidence.
- [ ] Every p9 ticket written after this one cites the repo-relative path from the outset, so the corpus never holds two roadmap paths at once.

## Implementation Plan

Commit the file before touching a single citation. The ordering is not cosmetic: repointing first leaves sixty-three bullets aimed at a path that does not yet exist in the tree. `.pine/.gitignore` ignores `attachments/` and nothing else, so `git add .pine/roadmap.md` is the whole of it. Confirm byte identity against the source copy with `diff` and record that output before the home-directory file is abandoned, because that is the last moment the two can be compared.

Script the rewrite rather than editing sixty-three files by hand, and keep the script as evidence. Reject a single blanket `sed` over the corpus: the two path spellings are different strings, so a substitution written against the absolute form succeeds on thirty-eight files and misses the twenty-five tilde-spelled ones, and the grep for the new path then returns thirty-eight hits, which reads as progress. Match instead on the stable substring `distributed-worker-nats-crispy-finch.md`, and drive the wording normalisation from a table of ticket id to phase and entry rather than parsing the entry out of the prose.

**Which path form.** The candidates are the repo-relative `.pine/roadmap.md` and a workspace-absolute path. Recommend repo-relative: every second-and-later reference bullet in the corpus is already repo-relative — `internal/database/database.go`, `web/src/lib/workflow-editor/node-visual.ts` — so an absolute first bullet would be the only path in any ticket that breaks when the repository is cloned elsewhere.

The trap is that nothing validates a reference bullet. `pine doctor` checks workspace structure, not link targets, and a bullet naming a path that does not exist renders exactly like one that does. A half-finished rewrite therefore raises no error anywhere; it surfaces months later, when an agent opens a p4 ticket, cannot find the roadmap, and reconstructs the rationale from the ticket body alone. The only defence is the grep asserted above, run by hand and pasted into the ticket.

Take the CI-claim correction here and leave the other correction this roadmap section parks alongside it. The CI claims are the same defect in the same files and cost eight sentences while the corpus is already open. The PostgreSQL integration-test cleanup at `internal/database/database_test.go:145-150`, which drops only `models[0..3]` and leaks `credentials`, `webhook_bindings` and `schedules`, is a Go change to a test this ticket has no other reason to open; it belongs to V2-p9-1.

**Where the roadmap lives.** Recommend `.pine/roadmap.md`, beside the tickets that cite it and inside the directory `pine context` already treats as the project briefing surface. What would reopen it: a decision to publish the roadmap to customers or render it from a docs site, at which point `docs/` wins and the move costs one commit plus sixty-three bullets again — which is the reason to keep the rewrite script rather than throw it away.

## References

- Roadmap plan, p9 section, entry V2-p9-0: `.pine/roadmap.md`.
- `.pine/roadmap.md` — the copied roadmap, untracked at the time of writing and byte-identical to the plan file it replaces.
- `.pine/tickets/EPIC-m42s3g.md` — line 70, the epic's own `- Plan:` bullet, the sixty-third citation the roadmap's count of sixty-two omits.
- `.pine/tickets/FEAT-r6xhnp.md` — line 50, one of the sixteen bullets already in the target wording.
- `.pine/tickets/FEAT-k3grr5.md` — line 50, one of the forty-seven older `- Plan:` bullets carrying a trailing section clause that must survive.
- `.pine/.gitignore` — ignores `attachments/` only, so the roadmap is trackable where it already sits.
- `.pine/tickets/FEAT-gvn62x.md`, `FEAT-qcm5ec.md`, `FEAT-je4f4t.md`, `FEAT-jwhdsy.md`, `FEAT-ddzk2k.md`, `FEAT-csqgg5.md`, `FEAT-48hreg.md`, `EPIC-c7gbdp.md` — the eight bodies asserting a check runs in CI.
- `internal/database/database_test.go` — lines 145-150, the cleanup this ticket deliberately leaves to V2-p9-1.
