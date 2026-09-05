---
id: FEAT-agj52c
title: Build the Datastore editor surface
status: todo
priority: medium
labels:
    - datastore
    - storage
    - api
deps:
    - FEAT-xeq6st
    - FEAT-ptyh9w
parent: EPIC-m42s3g
phase: p9
created: "2026-09-05T08:28:44Z"
updated: "2026-09-05T08:28:44Z"
---

## Scope

A new dashboard section has to be declared twice, in two files that know nothing about each other. The `items` array at `web/src/lib/components/dashboard/dashboard-nav.svelte:11-17` holds five literal entries — `{ href: '/app/workflows', label: 'Workflows', icon: GitBranch }` and its siblings for executions, schedules, credentials and settings — and drives the sidebar and the mobile sheet alike. The `sectionTitle` derivation at `web/src/routes/(dashboard)/+layout.svelte:12-18` is a separate `$derived.by` chain of four `page.url.pathname.startsWith` tests that falls through to `return 'Workflows'`. A route registered in one place and not the other still renders: a `/datastores` page added only to the nav shows the header title "Workflows", because the fall-through is a real label rather than an empty string, and nothing anywhere fails.

The surfaces this ticket needs do not exist either. `web/src` contains no `confirm(`, no alert-dialog primitive and no `contenteditable` anywhere — the delete buttons on schedules and credentials fire their request on the first click. There is one `<table>` in the whole app, at `executions/+page.svelte:151`, and one paging control, an inline `Load more` button on the same page. `web/src/lib/components/ui` carries fifteen shadcn-svelte primitives and no table among them, which is what V2-p9-8 exists to fix before this ticket starts.

There is no Svelte component test infrastructure at all. `web/package.json` declares `"test": "vitest run"` and carries no `@testing-library/svelte`, no `vitest-browser-svelte` and no `jsdom` or `happy-dom`; `web/vite.config.ts` has no `test` block; all twelve `.test.ts` files sit under `web/src/lib` and exercise plain TypeScript modules, none under `web/src/routes`. Whatever this surface is going to prove, it proves through extracted modules and recorded manual evidence, not through rendering a component in a test.

That constraint is the argument for the scope decision this ticket has to settle. Inline cell editing means optimistic state, per-cell validation, keyboard traversal and conflict handling on a grid whose correctness nothing can assert automatically, in an app that has never shipped an editable cell. A read-only grid with a modal row editor reuses the `Dialog` pattern the workflows, schedules and credentials pages all already carry, and it is the difference between a slice that can be reviewed and one that can only be clicked at.

## Acceptance criteria

- [ ] `/datastores` appears in the sidebar and its header reads "Datastores" rather than falling through to "Workflows", captured as evidence at both desktop and mobile widths.
- [ ] A test asserts every entry in the nav `items` array has a matching `sectionTitle` branch, so a future section cannot be registered in only one of the two places.
- [ ] The row grid pages forward through a datastore larger than one page using the `nextCursor` the API returns, with no row duplicated and none skipped.
- [ ] A filter change that resolves after a later one cannot repaint the grid, proven by a test over the extracted request-guard module rather than by inspection.
- [ ] Deleting a column or a row asks for confirmation before any request is sent, and cancelling leaves the datastore byte-identical, captured as evidence on this ticket.
- [ ] Filter values are sent in the type the stored column declares, and a numeric filter against a numeric column returns the same rows on SQLite and PostgreSQL.
- [ ] The generated client is regenerated from the live specification and `pnpm generate:api:check` exits zero against the committed result.
- [ ] `pnpm check` and `pnpm test` both pass, run by hand alongside `make lint` and recorded here rather than assumed.

## Implementation Plan

Register the route in both places first, before any grid exists. It is four lines of change, it is the step that silently half-lands, and doing it first means every later screenshot is taken through the real shell. Add the test pairing the two declarations in the same commit, because a rule enforced only by memory is the rule that produced the fall-through.

Build the list page on the shell V2-p9-8 extracts, not beside it. If the shell cannot express the datastore list without a new prop for every state, that is a defect to fix there, not a reason to hand-write a fifth copy — the point of sequencing these two tickets was that this page never writes one.

**Read-only grid or inline cells.** Recommend read-only with a modal row editor: it reuses the `Dialog` composition already proven on three pages, puts the whole row's validation in one submit rather than per-cell commits, and confines every mutable widget to a modal — which matters when nothing can assert grid behaviour automatically. Reject inline cell editing for slice one, which needs optimistic updates, per-cell coercion, keyboard traversal and a conflict story against the row-level concurrency V2-p9-6 settles, none of it covered by a test that could catch a regression.

Reject a virtualised grid too: the page is cursor-paged against the row cap V2-p9-5 enforces server-side, so the DOM never holds more than one page. Put the column model, the filter serialisation and the request guard in `.ts` modules under `web/src/lib` with `.test.ts` companions, following `key-value.ts` — that is where the criteria above are provable, and it keeps the `.svelte` files thin enough that `pnpm check` plus recorded evidence is honest coverage rather than a gap dressed as one.

The trap is filter type coercion. A stored column is `TEXT`, `DOUBLE PRECISION`, `BOOLEAN` or `TIMESTAMPTZ(3)` on PostgreSQL and `TEXT`, `REAL`, `BOOLEAN` as 0/1 or `DATETIME(3)` on SQLite, but every filter input in a browser produces a string. Send `"3"` against a numeric column and PostgreSQL rejects or coerces it while SQLite's dynamic typing quietly compares a string to a number and matches nothing; `"true"` against a boolean stored as 1 behaves the same way. The failure surfaces as an empty grid — the correct-looking empty state the shell already renders — with no error and on one driver only, and the user concludes the data is gone. Coerce in the extracted serialiser against the stored column type, refuse an uncoercible value at the filter control, and test both drivers.

Recommend read-only plus modal for slice one, revisited only if usage shows the modal round trip dominates the cost of routine edits. What reopens it is a component test harness landing for some other reason: with rendering assertable, inline editing stops being a slice that can only be verified by hand.

## References

- Roadmap plan, p9 section, entry V2-p9-9: `.pine/roadmap.md`.
- `web/src/lib/components/dashboard/dashboard-nav.svelte` — the hard-coded `items` array at lines 11 to 17, the first of the two registration points.
- `web/src/routes/(dashboard)/+layout.svelte` — the `sectionTitle` chain at lines 12 to 18, the second registration point, whose fall-through returns `'Workflows'`.
- `web/src/routes/(dashboard)/executions/+page.svelte` — the app's only `<table>` at line 151, its `Load more` control and the `requestToken` stale-response guard at line 28.
- `web/src/routes/(dashboard)/schedules/+page.svelte` — the `Dialog` editor at lines 158 to 191 and the unconfirmed `remove` at lines 80 to 87 a confirmation step has to displace.
- `web/src/lib/workflow-editor/key-value.ts` and `key-value.test.ts` — the pattern for logic testable without a component harness.
- `web/package.json` and `web/vite.config.ts` — `"test": "vitest run"`, no component-testing dependency and no vitest `test` block.
- `web/scripts/check-api-client.mjs` — the staleness check `pnpm generate:api:check` runs against the committed generated client.
- Local n8n UI reference: `design-refs/n8n-v2/INDEX.md` entries 18-20, 23-24 — the list surface, the detail grid with its row-selection column, Add Row and Add Column, paging controls, and the AG Grid inline-rename header. Captured from a live local n8n 2.x instance; gitignored, never vendored.
