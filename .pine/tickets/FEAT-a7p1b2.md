---
id: FEAT-a7p1b2
title: Keep loaded rows when a paging request fails
status: todo
priority: medium
created: "2026-09-05T15:11:11Z"
updated: "2026-09-05T15:11:11Z"
phase: p9
parent: EPIC-m42s3g
labels:
    - web
    - ux
---
## Scope

The executions list pages with a cursor. When "Load more" fails, `+page.svelte` sets the same `error` that governs the whole surface, and the render replaces the entire table with the error card — so every row already on screen disappears and only a full reload brings them back. The user has lost work they could see a moment ago, in exchange for a message about a request that was optional.

FEAT-ptyh9w extracted the state decision into `web/src/lib/dashboard/list-state.ts` and **preserved this behaviour deliberately**, with a test pinning `failed` beating a non-zero row count, because fixing it is not a refactor: it means deciding what a paging failure looks like beside rows that loaded fine, which is a state the page does not have.

## Acceptance criteria

- [ ] A failed "Load more" leaves the loaded rows on screen and reports the failure beside them rather than in place of them.
- [ ] The first load failing still replaces the surface, because there is nothing to keep — proven by a test distinguishing the two cases.
- [ ] Retrying after a failed page continues from the cursor it failed on rather than restarting the list.
- [ ] `list-state.ts` gains the distinction and the test that currently pins the defect is replaced by one that pins the fix, so the change is visible in the diff rather than silent.
- [ ] The same treatment reaches any other cursor-paged list, or the ticket records that executions is the only one.

## Implementation Plan

The state is the whole of it. `listState` today answers one question — loading, failed, empty or ready — and a paging failure needs two: what the surface is, and whether the last page attempt failed. Add the second rather than widening the first, because a five-valued enum would make every call site re-derive which of its values still mean "there are rows".

Reject clearing `error` on the next successful page without telling anybody: a user who saw a failure and then sees it vanish has no way to know whether the rows they are looking at are complete.

## References

- Roadmap plan, p9 section: `.pine/roadmap.md`.
- `web/src/lib/dashboard/list-state.ts` — the discriminator, and the test that currently pins this defect.
- `web/src/routes/app/executions/+page.svelte` — the only cursor-paged list.
- `.pine/tickets/FEAT-ptyh9w.md` — the ticket that found this and deliberately did not fix it.
