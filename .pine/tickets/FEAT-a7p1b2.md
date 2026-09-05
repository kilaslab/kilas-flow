---
id: FEAT-a7p1b2
title: Keep loaded rows when a paging request fails
status: done
priority: medium
created: "2026-09-05T15:11:11Z"
updated: "2026-09-05T16:02:00Z"
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

- [x] A failed "Load more" leaves the loaded rows on screen and reports the failure beside them rather than in place of them.
- [x] The first load failing still replaces the surface, because there is nothing to keep — proven by a test distinguishing the two cases.
- [x] Retrying after a failed page continues from the cursor it failed on rather than restarting the list.
- [x] `list-state.ts` gains the distinction and the test that currently pins the defect is replaced by one that pins the fix, so the change is visible in the diff rather than silent.
- [x] The same treatment reaches any other cursor-paged list, or the ticket records that executions is the only one.

## Implementation Plan

The state is the whole of it. `listState` today answers one question — loading, failed, empty or ready — and a paging failure needs two: what the surface is, and whether the last page attempt failed. Add the second rather than widening the first, because a five-valued enum would make every call site re-derive which of its values still mean "there are rows".

Reject clearing `error` on the next successful page without telling anybody: a user who saw a failure and then sees it vanish has no way to know whether the rows they are looking at are complete.

## References

- Roadmap plan, p9 section: `.pine/roadmap.md`.
- `web/src/lib/dashboard/list-state.ts` — the discriminator, and the test that currently pins this defect.
- `web/src/routes/app/executions/+page.svelte` — the only cursor-paged list.
- `.pine/tickets/FEAT-ptyh9w.md` — the ticket that found this and deliberately did not fix it.

## Work evidence

### Where the ticket's premises were stale

The executions page is at `web/src/routes/(dashboard)/executions/+page.svelte`,
not `web/src/routes/app/executions/+page.svelte` — the dashboard routes moved
into a `(dashboard)` group. Everything else the ticket claims held: `listState`
did return `failed` ahead of a non-zero count, `list-state.test.ts` did carry a
test pinning that, and the executions page did set one `failure` for both kinds
of request.

One stale claim lives in the code rather than the ticket. The comment above the
pinning test read "Executions keeps rows on screen when a 'Load more' fails" —
which was never true; the error card replaced them. That comment is gone with
the test it explained.

### The fix is two questions, not a fifth state

`listState` keeps its four values and now lets a non-zero row count beat
`failed`; `failedBesideRows` is the second question, answering whether to
report the failure next to the rows. The plan's reason for splitting them
holds: no call site has to work out which of the enum's values still mean
"there are rows to render".

The row count is the whole discriminator, and it works because both callers
already keep the invariant it rests on. `load()` assigns `emptyPage()` in its
catch, so a first-load failure always arrives with a count of zero; TanStack
Query keeps the last successful `data` when a refetch errors, so those pages
arrive with the rows they had. A non-zero count beside a failure therefore
always means rows that loaded intact.

### The notice belongs to ListStates, not to the executions page

Putting it in the page would have been the smaller diff and the wrong one. The
three TanStack-backed lists pass `isError` and a count off cached data, so
`failed` with rows on screen is reachable on all four surfaces — a background
refetch failing over a loaded list. Relaxing `listState` alone would have kept
their rows and made their failure silent, which is a worse defect than the one
being fixed. The notice sits in `list-states.svelte` after `children`, so every
list that can reach the state reports it.

That is also the answer to the last acceptance criterion. Executions is the only
cursor-paged list: `cursor-page.ts` is imported by exactly one file, and no
other route mentions a cursor. The other three reach the same state by a
different route and are covered by the same notice.

### Retry continues from the failed cursor

It always did, by construction — `appendPage` is the only writer of `page` in
`loadMore` and only runs on success, so a failed page leaves `nextCursor`
pointing at the page that failed. What was missing was an affordance that used
it: the only retry on screen was the error card's, which calls `load()` and
restarts from the top. `onRetryMore` is that affordance, and the executions page
passes `loadMore`. The `catch` now carries a comment naming what a future edit
would break by resetting `page` there.

The page's own "Load more" button hides while the notice is up, since the
notice's "Try again" is the same request — two buttons a thumb's width apart
doing one thing is worse than one.

### On clearing the error

`loadMore` clears `failure` when the attempt starts rather than when it
succeeds, so the notice describes only the attempt in flight. This is not the
silent clearing the plan rejects: the cursor never advanced past the page that
failed, so a successful retry fetches exactly the page that was missing and the
list it leaves behind is complete. Nothing is skipped, so there is nothing left
to warn about.

### Runs

Both from `web/`, after `pnpm install --frozen-lockfile` in the worktree.

```
npx vitest run                                            22 files, 233 tests, all green
npx svelte-kit sync && npx svelte-check --tsconfig ./tsconfig.json --threshold error
                                                          1342 files, 0 errors, 0 warnings
```

`svelte-check` was checked against a planted `const planted: number = "not a
number";` first — a bare `svelte-check --threshold error` with no `--tsconfig`
silently checks nothing, and the repository's own `check` script is
`svelte-kit sync && svelte-check --tsconfig ./tsconfig.json`.

The three new tests were reverted against the pre-fix logic (`if (failed)
return 'failed'` restored, `failedBesideRows` stubbed to `false`) and fail:

```
× keeps the rows already loaded when the next page fails
    AssertionError: expected 'failed' to be 'ready'
× replaces the surface for a first-load failure but not for one over loaded rows
    AssertionError: expected 'failed' to be 'ready'
× reports a failure that landed on top of loaded rows
    AssertionError: expected false to be true
Tests  3 failed | 8 passed (11)
```
