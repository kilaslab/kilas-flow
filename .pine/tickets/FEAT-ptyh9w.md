---
id: FEAT-ptyh9w
title: Extract a shared list page shell and a table primitive
status: testing
priority: medium
labels:
    - datastore
    - storage
    - api
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T15:10:00Z"
---

## Scope

`function message(error: unknown): string` is declared seven times under `web/src`, not four as the roadmap states — at `web/src/routes/(dashboard)/app/workflows/+page.svelte:28`, `executions/+page.svelte:37`, `schedules/+page.svelte:37`, `credentials/+page.svelte:47`, `executions/[id]/+page.svelte:79`, `app/workflows/[id]/+page.svelte:60` and `web/src/lib/embed/embed-editor.svelte:50`. Five return `` `${error.status} — ${error.message}` `` for an `ApiError`; the last two return `error.message` alone. The copies have already diverged, so an API failure spells itself two different ways depending on which page the user is looking at. `ApiError` is exported from `web/src/lib/api/http.ts:17`, which is where the helper belonged from the start.

The three-state block around it has drifted the same way. Four list routes each hand-roll a loading skeleton, an error card and an empty state. Schedules and credentials render `Array(2)` skeleton rows in a `grid gap-3` wrapper and an empty state on `rounded-2xl … bg-card px-6 py-12`; executions renders `Array(3)` and `rounded-lg … px-6 py-10`; workflows renders `Array(4)` inside a bordered container, hides its loading text behind `sr-only`, and sizes its error heading `text-sm` where the other three leave it unsized. Nothing about these differences is a decision — they are the residue of four copy-paste events.

The app also has exactly one `<table>`, at `web/src/routes/(dashboard)/executions/+page.svelte:151`, wrapped in a hand-written `overflow-x-auto` container with its own `<caption class="sr-only">`, header row and `Load more` button. `web/src/lib/components/ui` holds fifteen shadcn-svelte primitives and neither a table nor a skeleton among them, so a Datastore grid built today would either copy that markup a second time or invent a second table idiom.

Testing shapes what can be extracted. `web/package.json` declares `"test": "vitest run"` and carries no `@testing-library/svelte`, no `vitest-browser-svelte` and no `jsdom` or `happy-dom`; `web/vite.config.ts` declares no `test` block; all twelve `.test.ts` files sit under `web/src/lib` and exercise plain TypeScript modules, none under `web/src/routes`. There is no Svelte component test infrastructure at all, and `make lint` runs `pnpm check` and never `pnpm test`.

This matters because the Datastore surface is the fifth list page and the second table, arriving with paging, filtering and destructive actions the existing four never had. Extracting the shell before it lands is the difference between one primitive with tests and five hand-written near-copies a design change has to visit individually.

## Acceptance criteria

- [x] `message` is declared once, exported from `web/src/lib/api/http.ts` beside `ApiError`, and no `.svelte` file under `web/src` declares its own copy, proven by a test grepping the source tree.
- [x] All four existing list routes render their loading, error and empty states through the shared shell, and `pnpm check` reports no new diagnostics, run by hand and recorded here.
- [x] The two divergent `message` copies adopt the five-copy behaviour so an `ApiError` renders its status on every surface, captured as before-and-after evidence on this ticket.
- [x] The executions table renders through the new table primitive with its caption, header semantics and horizontal scroll container unchanged, captured as evidence at a narrow viewport.
- [x] The shell's state discriminator and the paging state live in `.ts` modules with `.test.ts` companions, matching `key-value.ts`, and `pnpm test` passes.
- [x] The executions list still discards a stale response when a filter changes mid-flight, proven by a test over the extracted request-guard module rather than by inspection.
- [ ] `pnpm test` is reachable from a Makefile target so the frontend suite is run by hand alongside `make lint` rather than by memory.
- [x] No route file under `web/src/routes` gains a second skeleton, error card or empty-state block, so the fifth copy is never written.

## Outcome

`message` now lives in `web/src/lib/api/http.ts` beside `ApiError`, and `web/src/lib/components/dashboard/list-states.svelte` renders the loading, failed and empty states for all four list routes. Three tested modules sit under `web/src/lib/dashboard/`: `list-state.ts` decides which of the four states shows, `request-guard.ts` holds the out-of-order-response guard, and `cursor-page.ts` holds the executions list's paging. The executions table renders through `web/src/lib/components/ui/table`, taken from the shadcn-svelte registry under the `nova` style the other fifteen primitives use. 180 tests to 212; `pnpm check` reports 0 errors and 0 warnings across 1340 files.

**There were eight copies, not seven.** `web/src/routes/+page.svelte` carried the same three lines under the name `errorMessage`, which is why a grep for `function message` missed it. Its fallback — `'Backend unreachable'` rather than the shared sentence — is a real decision on a page whose entire subject is whether the backend answers, so it keeps that and delegates only the `ApiError` branch. The lesson went into the test: alongside the check that no `.svelte` declares its own `message`, there is a second one asserting that no `.svelte` file contains `instanceof ApiError` at all. A check against the function name would have walked straight past the copy that actually needed finding.

Two of the ticket's line numbers had drifted — `app/workflows/[id]` declared it at 73 rather than 60, and `executions/[id]` at 86 rather than 79 — but every file it named was right, and the five-versus-two split of the two spellings was exactly as described.

**The two divergent copies, before and after.** `app/workflows/[id]/+page.svelte` and `lib/embed/embed-editor.svelte` both read `if (error instanceof ApiError) return error.message;`, and now call the shared helper. A 404 whose problem detail is "Workflow not found" rendered as `Workflow not found` in the editor and as `404 — Workflow not found` on the list linking to it; both now say `404 — Workflow not found`. That covers six surfaces: the editor's load card, save error and run error, and the same three in the embedded editor. Verified live — `/app/workflows` against an absent backend renders `502 — Bad Gateway` through the new shell.

**The ticket has the drift backwards.** It calls the workflows list "the copy that has already drifted". `git show b80890e -- web/src/routes/(dashboard)/app/workflows/+page.svelte` shows the opposite: that commit deliberately rewrote its three state blocks to the density scale — `Array(3)` of `h-20` floating cards became `Array(4)` of `h-11` rows inside a bordered container, the loading sentence became `sr-only`, the error heading gained `text-sm`, and the empty state went from `rounded-2xl … bg-card px-6 py-12` to `rounded-lg … px-6 py-10`. The same commit reached the empty state and the table header on executions but neither its skeleton nor its error card, and left the schedules and credentials state blocks alone entirely. Workflows is the converged copy; the other three are the ones that were never revisited.

So **the workflows variant is what the shell renders**, wholesale, rather than a majority vote taken class by class. It is the only one that was reviewed at the current density scale, and it is the only skeleton that resembles the list it stands in for — schedules and credentials render `h-11` rows in a bordered container but previewed them as floating `h-16` cards, so their layout jumped when data arrived. What visibly changed on the other three: the skeleton, the empty-state geometry, and the error card's heading size and button size. The one deliberate accessibility consequence is that "Loading schedules…", "Loading credentials…" and "Loading executions…" are now announced rather than displayed; they stay inside the same `aria-live="polite"` region, so nothing is lost to a screen reader.

**Named `list-states.svelte`, not `list-page.svelte`.** It renders three states and a slot, not a page — every one of the four keeps its own heading, description and header action, and those actions differ enough (a dialog trigger, a refresh button, a plain button) that absorbing them would have bought a prop per variation. A component called `list-page` invites the fifth list to put its header inside it and then discover it does not fit.

**The props are plain booleans**, as the ticket's trap paragraph asks. Executions needed one adjustment to fit: it used to hold `error` as a pre-formatted string, which would have reached `message()` as a non-Error and rendered "The request could not be completed." It now holds the rejection itself in `failure` and lets the shell word it, so no page is a second opinion on how a failure reads.

**The table is the registry's, unedited.** `scope="col"` is passed at the call site rather than defaulted into `table-head.svelte`: an edit to a vendored primitive is silently lost the next time `shadcn-svelte add table` runs, and the registry is the whole reason for taking the component instead of writing one. Verified at a 420px viewport: the caption text and `sr-only` are unchanged, all six `th` carry `scope="col"`, the `table-container` computes `overflow-x: auto` and genuinely scrolls while the document does not, the table holds its 704px (44rem) minimum, and header cells measure 28px tall with 12px side padding — the same box `py-1.5` produced. One pixel-level difference is inherited from the primitive and kept: cells are `align-middle` where they were previously baseline-aligned, which is visible only in the status column, where the badge is taller than the text beside it.

**Paging and the guard were exercised end to end, not only in unit tests.** Clicking "Load more" took the table from 2 rows to 3 in order and removed the button when the final page reported no cursor. For the guard: an unfiltered first response delayed 2.5 seconds behind the filtered request that superseded it never reached the table, before or after it landed.

Two things this did not do.

**The Makefile target is not done.** `pnpm test` is still reachable only by hand. This ran in a worktree confined to `web/`, with the Makefile explicitly out of bounds because another change was in flight against the same tree. The change is one target and a line in `lint`:

```make
.PHONY: web-test
web-test: ## Run the frontend test suite
	cd $(WEB_DIR) && pnpm test
```

**A defect in the executions list is preserved rather than fixed.** When "Load more" fails, the page replaces the whole table with the error card and the rows already on screen disappear; only a full reload brings them back. `listState` reproduces that faithfully — there is a test pinning `failed` beating a non-zero `count` — because fixing it means deciding what a paging failure should look like next to rows that loaded fine, and that is a new state, not a refactor. It wants its own ticket and a `pagingError` distinct from the one that governs the whole surface.

## Implementation Plan

Move `message` first, because it is the smallest change, the one with a visible user-facing bug attached, and it settles where shared frontend helpers live before anything larger has to choose. It goes into `web/src/lib/api/http.ts` beside `ApiError`, gets a `.test.ts` covering an `ApiError`, a plain `Error` and a non-error value, and the seven declarations become one import. Fixing the two divergent copies belongs to this step, not to a follow-up.

Then the shell. **A component or a rune factory.** Recommend a `list-page.svelte` in `web/src/lib/components/dashboard/` taking the state flags as props and `{#snippet}` blocks for the loaded content, the empty state and the error heading. Reject a `createListState()` rune factory each page composes by hand: it would leave the markup — which is what actually drifted — duplicated in four files while abstracting the part that did not.

For the table, take shadcn-svelte's table primitive from the registry `web/components.json` already configures, under the same `nova` style as the other fifteen. Reject `@tanstack/table-core`: it brings a headless model for sorting, grouping and column sizing nothing in this app asks for, and the one existing table is thirty lines of markup a registry component reproduces exactly.

Do not add a component test harness here. Extract the testable parts — the state discriminator, the cursor handling, the column model — into `.ts` modules with `.test.ts` companions as `key-value.ts` and `ports.ts` already do, and leave markup covered by `pnpm check` plus recorded evidence. Wire `pnpm test` into a Makefile target while touching the build, since it appears in none.

The trap is that the four pages are not the same page. Schedules, credentials and workflows read `isPending` and `isError` off TanStack Query objects; executions drives `loading`, `error`, `items` and `nextCursor` as hand-managed `$state` and guards a stale response with the `requestToken` counter declared at `executions/+page.svelte:28` and compared at lines 52 and 56. A shell whose props assume a query object invites that page to be rewritten to fit, and the guard disappears in the rewrite. Nothing fails: a filter change simply repaints the previous filter's rows, with no error and no symptom until someone reads the numbers closely. Take the flags as plain booleans so both call shapes fit, and extract the guard into its own tested module before touching that page.

Recommend deferring the component harness to the ticket that first needs it. What reopens it is the Datastore grid in V2-p9-9: if its cell rendering cannot be reduced to tested `.ts` modules, a harness has become cheaper than the manual evidence it replaces, and the decision is revisited there rather than argued in advance.

## References

- Roadmap plan, p9 section, entry V2-p9-8: `.pine/roadmap.md`.
- `web/src/routes/(dashboard)/schedules/+page.svelte` — `message` at lines 37 to 40 and the three-state block at lines 109 to 132, the canonical copy.
- `web/src/routes/(dashboard)/credentials/+page.svelte` — the second copy at lines 47 to 50 and 128 to 151, identical but for its noun and icon.
- `web/src/routes/(dashboard)/executions/+page.svelte` — hand-managed loading state and the `requestToken` guard at lines 22 to 60, the three-state block at 125 to 148, and the app's only `<table>` at line 151.
- `web/src/routes/(dashboard)/app/workflows/+page.svelte` — the copy that has already drifted, at lines 28 to 31 and 118 to 145.
- `web/src/lib/api/http.ts` — `ApiError` at line 17, the home the shared `message` belongs in.
- `web/src/lib/workflow-editor/key-value.ts` and `key-value.test.ts` — the existing pattern for frontend logic that can be tested without a component harness.
- `web/package.json` — `"test": "vitest run"` with no component-testing dependency of any kind.
- `web/components.json` — the shadcn-svelte registry and `nova` style a table primitive comes from.
- `Makefile` — `lint` at lines 104 to 108, which runs `pnpm check` and never `pnpm test`.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entry 24 — n8n's grid is **AG Grid** (`ag-header-cell-text`) with an `inline-editable-area` in the header. This repository has no grid library and exactly one `<table>`, so adopting or declining one is this ticket's decision, not a styling detail. Captured from a live local n8n 2.x instance; gitignored, never vendored.
