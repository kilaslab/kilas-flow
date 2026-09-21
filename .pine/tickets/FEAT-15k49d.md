---
id: FEAT-15k49d
title: 'i18n infrastructure: message catalog, plurals, and a locale for the embed'
status: todo
priority: medium
labels:
    - i18n
    - frontend
created: "2026-09-20T00:40:36Z"
updated: "2026-09-20T02:04:30Z"
---

Source: FEAT-edxxj7 finding "No i18n infrastructure: all UI copy, plurals and sentences are inline English and lang is
fixed to 'en'".

Deferred out of FEAT-edxxj7 for size, not for doubt: adopting a message catalog touches every .svelte file, the lib
helpers that build sentences (version-history.ts, activation.ts, execution.ts statusLabel), the hand-rolled plurals
(execution-canvas.svelte, version-panel.svelte) and the embed branding type. That is a rewrite of the frontend's copy
layer, and FEAT-edxxj7 was scoped to release metadata, stale docs and dead code.

Why it matters: the product is sold white-label, the embedded editor is the surface a customer's users see, and
app.html hardcodes lang="en". The Indonesian market the product name suggests cannot localize either the editor or the
embed chrome, and retrofitting gets more expensive as copy grows.

Work:
1. Adopt a message catalog (paraglide-js is the natural fit for SvelteKit; check its licence against
   .pine/memory/licensing.md before adding it as a dependency).
2. Move UI copy out of components; replace hand-rolled plurals with the catalog's plural rules.
3. Add `locale` to the embed branding/session type, set document.lang from it, and pass it through the embed SDK's
   mount options so a host can localize the editor it embeds.
4. Ship `en` as the first catalog and prove one non-English catalog renders in the editor and in an embed (e2e).

Acceptance:
- [x] No user-visible English string literal remains in web/src/lib/components and web/src/routes.
- [x] `lang` on the document follows the embed's locale, not a constant.
- [x] A second locale renders end to end in the editor and in the embedded editor.

## Implementation notes

### Stage 1 of 4 — paraglide wiring, the locale layer, the first catalogs, the dashboard shell

Engine and pins (the owner decision, unchanged): `@inlang/paraglide-js` 2.23.2 and
`@inlang/plugin-message-format` 4.4.4, devDependencies of `web/` only, no carets. Both are MIT:
paraglide-js declares it in its npm metadata; plugin-message-format declares nothing in
package.json, so its licence file was read in place
(`web/node_modules/@inlang/plugin-message-format/LICENSE` — "MIT License, Copyright (c) 2024 Opral US
Inc."), the same fact the planner recorded from the upstream monorepo. Neither is an n8n package and
neither carries n8n bytes; `go test -run TestNoN8NDependency ./internal/guardrails/...` passes with
both declared.

What changed:
- `web/project.inlang/settings.json` (new): `baseLocale: en`, `locales: [en, id]`, the message-format
  plugin loaded by relative path (`./node_modules/@inlang/plugin-message-format/dist/index.js` —
  relative to the directory that holds `project.inlang/`, i.e. `web/`; never a CDN URL), and
  `pathPattern` as an ARRAY: `./messages/{locale}/common.json`, then `./messages/{locale}/nav.json`.
  Later stages append one entry per area; order is the merge order.
- `web/messages/{en,id}/{common,nav}.json` (new): 21 keys per locale, named `<area>_<snake_case>`,
  globally unique because the plugin merges every file and valid JS identifiers so `m.common_save()`
  stays dot-accessed. `en` values are byte-identical to the literals they replace, so the swap is
  visible in the diff. `common_item_count` is the first plural (declarations/selectors/match, `one`
  and `other` in both locales — Indonesian has a single category, so both entries carry the same
  form). `common_save` has no component consumer yet: it is the key the locale test drives to prove
  the in-repo state reaches every message function, and the editor's Save button moves onto it in a
  later stage. Language names are endonyms: `nav_language_english` is "English" and
  `nav_language_indonesian` is "Bahasa Indonesia" in BOTH catalogs.
- `web/vite.config.ts`: `paraglideVitePlugin({ project: './project.inlang', outdir:
  './src/lib/paraglide', strategy: ['baseLocale'], emitTsDeclarations: true })` after `sveltekit()`.
  No url/cookie/localStorage/preferredLanguage strategy exists anywhere, so paraglide's own detection
  cannot read navigator or write storage.
- `web/package.json`: `i18n:compile` (the same options as the plugin) is prepended to BOTH `check`
  and `test`, because `src/lib/paraglide` is untracked (it writes its own `.gitignore` containing
  `*`) and its generated README embeds an absolute path. The options now exist in two places, so a
  test pins the generated `strategy` export to exactly `['baseLocale']`.
- `web/src/lib/i18n/locale.svelte.ts` (new): the only module that imports the generated runtime.
  `current` is a `$state<Locale>` and `overwriteGetLocale(() => current)` makes every message function
  follow it. Also `pickLocale` (pure; first candidate the catalogs carry, else the base locale),
  `currentLocale`, `setLocale` (refuses an unshipped locale and returns whether it applied),
  `applyDocumentLanguage` (no-op without `document`), `readStoredLocale`/`rememberLocale` (guarded by
  `typeof window`, try/catch around storage), `localeOptions()` (an explicit message map, endonyms).
  Storage key `kilasflow.locale`, sibling of the existing `kilasflow.sidebar.collapsed`.
- `web/src/routes/+layout.svelte`: one `$effect` writes `currentLocale()` through
  `applyDocumentLanguage`, so `<html lang>` follows the locale. `src/app.html` keeps `lang="en"` and
  deliberately does NOT use `%lang%`: the Go SPA handler serves the fallback index.html verbatim, so
  the placeholder would ship as a literal.
- `web/src/lib/dashboard/nav-sections.ts` (new): the single section registration — `dashboardSections`
  (href + key), `sectionLabel` (catalog lookup), `sectionForPath` (longest declared href that is the
  path or its parent, default `workflows`). `dashboard-nav.svelte` and `(dashboard)/+layout.svelte`
  both read it, so a section can no longer be named in the rail and titled as something else.
- `web/src/lib/components/dashboard/locale-switcher.svelte` (new): a native `<select>` (the pattern
  the editor's property pickers already use), `aria-label` from the catalog, options from
  `localeOptions()`, `setLocale` then `rememberLocale` on change. Placed in the sidebar footer and in
  the mobile `Sheet.Content`, because the dashboard header is `lg:hidden` on the editor route — a
  control there would be unreachable exactly where the copy matters most.
- Shell copy migrated into the catalogs: `(dashboard)/+layout.svelte` (section titles, "Sign out",
  the "Signed in" fallback, expand/collapse sidebar, open workspace navigation, workspace
  navigation), `dashboard-nav.svelte` (its aria-label and the six labels), `list-states.svelte`
  (loading, failed, incomplete, retry). The `KilasFlow` wordmark stays a literal.
- `web/src/lib/i18n/{locale,catalog,copy}.test.ts` (new), and
  `web/src/lib/dashboard/nav-sections.test.ts` rewritten as a behaviour test.

Deleted on purpose: the old `web/src/lib/dashboard/nav-sections.test.ts` parsed the SOURCE of
`dashboard-nav.svelte` and `(dashboard)/+layout.svelte` with a regex and could only fail for a reason
that no longer exists — it asserted that two hand-maintained lists agreed, which is the bug class the
extraction removes. Its replacement asserts observable mapping: every declared href maps to its own
section, a nested path maps to its parent, an unknown path maps to the default, and no two sections
share an href or a label.

The copy guard is the ratchet. It walks `web/src/lib/components/**/*.svelte` and
`web/src/routes/**/*.svelte`, blanks comments, `<script>`, `<style>` and `{…}` expressions, and
reports residual text nodes, copy-bearing attribute values (`title`, `aria-label`,
`aria-description`, `placeholder`, `alt`, `label`), sentence-shaped literals passed to
`new Error`/`toast`/`confirm`/`alert`, and any base-locale catalog value still present verbatim as a
text node or quoted literal. `PENDING` is the 31 files the later stages still own, generated by
running the guard with an empty list and pasting what it reported, sorted; the guard asserts every
listed path still exists so the list cannot rot. Stage 4 empties it and asserts it is empty — that
same test is the mechanical proof of acceptance criterion 1.

Commands and their real outcomes (from `web/` unless stated):
- `pnpm add -D @inlang/paraglide-js@2.23.2 @inlang/plugin-message-format@4.4.4` — ok, regenerated
  `web/pnpm-lock.yaml` (committed).
- `pnpm install --frozen-lockfile` — ok: the committed lockfile is self-sufficient, as CI runs it.
- `pnpm check` — 1941 files, 0 errors, 0 warnings.
- `pnpm test` — 47 files, 523 tests, all pass.
- `pnpm exec vitest run src/lib/i18n src/lib/dashboard/nav-sections.test.ts` — 16 tests, all pass.
- Falsification of the new tests, both reverted immediately: putting `Try again` back into the
  migrated `list-states.svelte` fails the guard with
  `list-states.svelte:106 — text "Try again"` and `… — catalog echo of common_try_again`; deleting
  `nav_settings` from `messages/id/nav.json` fails parity with
  `id/nav.json is missing keys: expected [ 'nav_settings' ] to deeply equal []`.
- `go build ./... && go vet ./... && go test -run TestNoN8NDependency ./internal/guardrails/...` — ok.
- `make build-all` — SPA embedded and `bin/kilasflow` built.
- Real surface: `./bin/kilasflow` against a scratch SQLite file, auth off, 127.0.0.1:8080, opening
  `/app/workflows` — initial `document.documentElement.lang === "en"`, header "Workflows", nav
  Workflows/Executions/Schedules/Credentials/Datastores/Settings; choosing Bahasa Indonesia in the
  sidebar switcher flips `lang` to `id`, the header to "Alur kerja" and all six nav labels to
  Indonesian, and stores `kilasflow.locale=id`; a reload keeps Indonesian; choosing English restores
  the English labels and `lang="en"`. At 390x844 (sidebar hidden, `lg:flex`) the header trigger reads
  "Buka navigasi ruang kerja", the sheet carries the same switcher (`aria-label` "Bahasa") and
  switching from inside the sheet flips the whole UI back to English.

Acceptance criteria: none ticked at this stage, and that is the honest position. Criterion 1 needs
stages 2-4 (31 files are still pending in the guard). Criterion 2 is half met — `<html lang>` now
follows a runtime locale instead of being a constant — but "the embed's locale" arrives with the
session field in a later stage. Criterion 3 needs the editor and the embed, not just the dashboard
shell; the screenshot from this stage still shows English page copy.


### Stage 2 of 4 — every dashboard route's copy moved into the catalogs

Scope: the seven areas the plan names (workflows, executions, schedules, credentials, datastores,
settings, auth) plus one it does not — `home`, for `routes/+page.svelte`, the landing/backend-status
page. The plan's item 4 listed that file without naming an area, and no other area fits: `auth` is the
sign-in and approval surface, and calling the health page part of it would be a lie a later reader
pays for. The landing page's copy (tagline, status pill, `Backend status`, `Refresh`, `checking…`,
`The API did not respond…` with its two commands as parameters, the three doc links, `Last checked
{time}.`, the scaffolding note, `Backend unreachable`) lives in `messages/{en,id}/home.json`.

What changed (27 files modified, 16 catalog files added; 519 keys per locale, 496 of them new here):
- `web/project.inlang/settings.json`: eight `pathPattern` entries appended after `nav`, in the order
  workflows, executions, schedules, credentials, datastores, settings, auth, home.
- New catalogs `web/messages/{en,id}/{workflows,executions,schedules,credentials,datastores,settings,
  auth,home}.json` — tab indent, arrays inline where they fit, `en` values byte-identical to the
  literals they replace, `id` values real Indonesian on one shared vocabulary (Alur kerja, Eksekusi,
  Jadwal, Kredensial, Penyimpanan data, Pengaturan, Batal, Simpan, Hapus, Cari, Impor, Ekspor, baris,
  kolom). Every key is `<area>_<snake_case>`; a script asserts no key is defined in two areas.
- All seven `app/workflows` route files, both execution routes, schedules, credentials, both datastore
  routes, settings, the landing page, login and the approval page: every text node, every
  `title`/`aria-label`/`aria-description`/`placeholder`/`alt`/`label`, every `svelte:head` title (one
  message each, so `· KilasFlow` is never a bare text node), and every `new Error('Unexpected …')`
  sink the guard reports.
- Hand-rolled plurals became catalog plurals: the workflows bulk-delete dialog (`count` + a preview of
  the names), the import report's blocking sentence, `import-diagnostics.ts`'s `N import issues`, the
  four datastore column/row counts, the datastores list page's column count, and `transfer.ts`'s
  `N rows imported, M blank lines skipped` — that one is a single message with TWO plural inputs and
  four comma-separated match arms per locale, and the `skipped > 0` guard is preserved by a second
  message so a clean import still reads `1 row imported` with no trailing clause.
- Lib copy reachable from these surfaces, beyond the plan's item-5 list, because it renders on them:
  `datastore/columns.ts`'s three `coerceValue` sentences (the add-row dialog shows them),
  `dashboard/workflow-list.ts`'s duplicate name (`Copy of {source}`), and `transfer.ts`'s severity
  vocabulary (`Blocking`/`Lossy`/`Dropped`, the same words the workflows area carries, reused as
  `datastores_severity_*`). `api/http.ts`'s fallback sentence and `dashboard/cursor-page.ts`'s stalled
  cursor error are `common_*`. Server-originated `ApiError` text stays English and out of scope.

The guard (the ratchet, extended):
- `PENDING` went from 31 files to 14 — exactly the files that still carry copy: the three
  `components/ui/{dialog,sheet}` primitives the editor's dialogs use, the ten `workflow-editor`
  components, and `routes/embed/[id]/+page.svelte`. That is not an estimate: a script re-runs the
  guard's own `violationsIn` over every guarded file with `PENDING` ignored and diffs the two sets —
  14 dirty, 14 listed, none over-listed, none leaking.
- `ALLOWED_TEXT` gained `GET`, `/api/v1/health` and `/api/v1/ready`, each with a reason: the landing
  page renders the endpoint it probes inside `<code>`, and an API path is not copy.
- Fixed a defect in the stage-1 `withoutComments`: a line comment was blanked up to the NEXT `//`
  rather than to the end of its line, so every second comment stayed in the text and the real code
  between two comments was blanked out. Both symptoms were live: it reported two phantom
  `catalog echo … as a quoted literal` violations on `executions/+page.svelte:43,103` (prose inside
  comments) and it could not see an echo in the code it had blanked. Fixing it made the guard stricter
  and the run below is the one that passes with the fix in place.

Verification, with the real outcomes (`cd web` unless stated):
- `pnpm check` — `COMPLETED 2442 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `pnpm test` — `Test Files 47 passed (47)`, `Tests 523 passed (523)`, same counts as stage 1: no test
  was added, deleted, skipped or re-pinned, and every English assertion the migration could have broken
  (`http.test.ts`, `columns.test.ts`, `transfer.test.ts`, `import-diagnostics.test.ts`,
  `workflow-list.test.ts`, `list-state.test.ts`) passes unedited.
- `pnpm exec vitest run src/lib/i18n` — 12 tests pass: the guard (3), key parity (2), the locale layer.
- Falsification of the guard, reverted immediately: a text node reading `Try again` plus
  `const falsify = 'Load more'` in `schedules/+page.svelte` fails with
  `… :162 — text "Try again"`, `… :162 — catalog echo of common_try_again`,
  `… :151 — catalog echo of datastores_load_more as a quoted literal`; removing them turns it green.
- Byte-identity of `en`: for all 402 guard-report literals in migrated files, 362 appear verbatim as an
  `en` catalog value (the rest are the sentences and plurals that became parameterised messages), and
  the untouched English-asserting tests above pin the remaining ones.
- `=== 1 ?` now survives only in the stage-3 editor files: none in any datastore page, the workflows
  bulk delete, `transfer.ts`, `columns.ts` or `import-diagnostics.ts`.
- Go unchanged, gates still run: `go build ./... && go vet ./...` clean,
  `go test ./internal/guardrails/...` ok, `make build-all` builds `bin/kilasflow`.
- Real surface (`make build-all && ./bin/kilasflow` against a scratch SQLite file with a seeded
  workflow, execution, credential, schedule and two datastores, locale switched to Indonesian through
  the sidebar switcher): `/app/workflows`, `/executions`, `/executions/<id>`, `/schedules`,
  `/credentials`, `/datastores`, `/datastores/<id>`, `/settings` and `/` all render `lang="id"`, an
  Indonesian title (`Alur kerja · KilasFlow`, `Eksekusi · KilasFlow`, `Penyimpanan data · KilasFlow`…)
  and no English chrome from a migrated file; a scan of `document.body.innerText` against every `en`
  value that differs from its `id` counterpart finds nothing, and no page renders a stray `{` or `}`.
  The plurals were read off the real dialogs: the bulk delete confirms
  `Hapus 1 alur kerja (Invoice sync)? Tindakan ini tidak dapat dibatalkan.` for one selected workflow and
  `Hapus 2 alur kerja (Nightly export, Invoice sync)? …` for two; the schedule row confirms
  `Hapus jadwal 0 9 * * 1-5 untuk Invoice sync? Jadwal ini akan berhenti berjalan.`; the add-row dialog
  renders `Tetapkan nilai untuk setiap kolom. Kosong tetap Null …` with no placeholder leak. The one
  English word left inside a migrated page's dialog, `Close`, comes from
  `components/ui/dialog/dialog-content.svelte`, which is in `PENDING` and belongs to stage 3.

Incident, recorded because it touched a committed file: while normalising the catalog JSON with an
inline shell one-liner I truncated `web/messages/en/datastores.json` (the redirect opened the file
before the renderer read it). The file was untracked, so git could not restore it; it was rebuilt from
the paraglide-compiled output of the lost version, which still held every message, with the complex
plural structures taken from the intact `id` catalog. The rebuild is provably equivalent: recompiling
the recovered catalog produces the same arms, string for string, as the saved pre-incident compile
(`101` keys compared, `0` mismatches), and the three keys added after that compile are the severity
vocabulary above.

Acceptance criteria: still none ticked, and that is the honest position. Criterion 1 needs the editor
components and the shared dialog/sheet primitives (stage 3) and the embed page (stage 4) — 14 files
remain in `PENDING`. Criterion 2 awaits the embed session's locale (stage 4); what stage 2 proves is
that every dashboard route follows the in-repo locale, `<html lang>` included. Criterion 3 is closer:
the dashboard renders end to end in Indonesian, but the editor canvas and the embed still do not.

### Stage 3 of 4 — the editor components and the sentence helpers, with real plural rules

Scope: the editor surface. Five new areas, `editor`, `canvas`, `properties`,
`versions` (plus 25 appended `executions_*` keys): 78 + 50 + 107 + 69 keys per
locale, every `en` value byte-identical to the literal it replaced, every `id`
value real Indonesian on the stage-2 vocabulary. The copy guard's `PENDING` set
went from 14 files to 1 — only `src/routes/embed/[id]/+page.svelte` remains, and
it belongs to stage 4.

What changed:
- `workflow-editor.svelte` + `editor-controls/canvas-bridge/canvas-node/canvas-edge/
  node-picker/node-icon` + `activation-notices.svelte`: every text node, every
  `title`/`aria-label`/`placeholder`, and every sentence built inside `{…}`
  expressions or `<script>` (the guard cannot see those) now comes from the
  catalog. The help overlay's labels became `() => string` in `SHORTCUT_REFERENCE`
  so a locale change re-labels the overlay without a remount; the key labels
  (`⌘Z`, `Tab / N`, `+ / -`) stay literal — a key is the same in every locale —
  and the guard was never asked to allow them because it cannot see an
  expression-valued attribute.
- `properties-panel.svelte` + `property-field.svelte` (82 guard violations):
  including the sentences assembled around expressions (`Result {x}:`, the
  `is set but does not apply to this operation.` tail, the mapper sentences) and
  three error strings only the script sees. The bare `true`/`false` text nodes
  that label a JSON boolean became ALLOWED_TEXT entries with a reason rather than
  catalog values, because a catalog value of `true` would echo against every
  `'true'` comparison in the tree — a failure in files this stage does not own.
- `version-panel.svelte` + `version-history.ts`: the panel copy, the role badges
  (`Draf`/`Diterbitkan`), `Revisi {n}`, all four refusals, both confirmations
  (parameterised with the revision title), the four publish-event sentences and
  `A pruned revision`. `relativeTime` keeps `Intl.RelativeTimeFormat` and its
  `{ now, locale }` options exactly as they were — the panel now passes
  `{ locale: currentLocale() }` so Intl formats in the app's locale rather than
  the browser's, and `just now` / `—` come from the catalog.
- `execution.ts` + `execution-canvas*.svelte`: `statusLabel` reads the eight
  status messages and still capitalises a server token no catalog carries
  (`manual`, `subworkflow` — the trigger vocabulary is the server's);
  `formatDuration`, `formatTimestamp` and `formatBytes` render through the
  catalog; `Read-only replay` is a message; the edge count is the catalog's one
  item plural, read once and used for both the label and the accessible name.
- `activation.ts` (`A trigger could not register … — {error}`), `shortcuts.ts`,
  `ports.ts` (`Row found`/`No row`), `expression-grammar.ts` (five sentences),
  `document.ts`, `history-diff.ts`.
- The shared `dialog`/`sheet` primitives' `Close` became `common_close`
  (`Tutup`), which is also what closes the version panel.
- Beyond the plan's list, found on the real surface: SvelteFlow's own zoom
  controls ship English `title`/`aria-label` text (tooltips a sighted user
  reads). Both canvas mounts now pass `ariaLabelConfig` built from the catalog
  (`Panel kontrol`, `Perbesar`, `Perkecil`, `Sesuaikan tampilan`).
- Guard change to report: the quoted-literal half of the echo rule now skips a
  declared `ECHO_EXEMPT` value. Two messages legitimately render a bare word that
  is also an unrelated technical literal — `{name} input {port}` renders `input`
  and `ui/input/input.svelte` holds `"data-slot": dataSlot = "input"`;
  `{label} type {index}` renders `type` and the same file holds
  `Omit<HTMLInputAttributes, "type">`. Rewording the messages would have changed
  what a screen reader announces, so the exemption is declared per value, with
  its reason, and applies only to the quoted-literal scan: a text node still has
  to come from the catalog.

Verification, with the real outcomes (`cd web` unless stated):
- `pnpm check` — `COMPLETED 2771 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `pnpm test` — `Test Files 47 passed (47)`, `Tests 529 passed (529)`: 523 plus
  the six new Indonesian assertions. No existing assertion was edited; the
  English-asserting suites that could have caught a copy change
  (`version-history.test.ts` 28 cases, `activation.test.ts`, `shortcuts.test.ts`,
  `execution.test.ts` 16 cases, `ports.test.ts`, `expression-grammar.test.ts`,
  `document.test.ts`, `history-diff.test.ts`) all pass unedited.
- Indonesian proof, as the plan asks: `setLocale('id')` then
  `restoreConfirmation(...)`, `publishEventSentence(...)`, `versionRoles(...)`,
  `relativeTime(...)`, `connectionChangeSentence(...)` and the plural arms return
  the `id` text in `version-history.test.ts` (28 -> 31 cases), and
  `statusLabel('succeeded') === 'Berhasil'`, `formatDuration(1500) === '1.5
  detik'`, `formatBytes(undefined) === 'ukuran tidak diketahui'` in
  `execution.test.ts` (16 -> 19 cases).
- Falsification of the guard, reverted immediately: putting the literal
  `Read-only replay` back into `execution-canvas.svelte` fails the guard with
  `execution-canvas.svelte:96 — text "Read-only replay"` and
  `… — catalog echo of executions_read_only_replay`; reverting turns it green
  again (3 tests pass).
- `=== 1 ?` search: no matches left in `web/src/lib/components`,
  `web/src/routes`, `web/src/lib/workflow-editor` or `web/src/lib/datastore`.
  All eight in-scope sites are catalog plurals with `one` and `other` arms in
  both locales.
- The probe with `PENDING` emptied reports exactly two violations repo-wide, both
  in `routes/embed/[id]/+page.svelte` (stage 4). Everything this stage owns is
  clean.
- Go unchanged and still gated: `go build ./...`, `go vet ./...`,
  `go test ./internal/guardrails/...` all ok; `make build-all` builds
  `bin/kilasflow`.
- Real surface (`make build-all && ./bin/kilasflow`, scratch SQLite database,
  auth off, a seeded workflow whose middle Set node fans one item out to three,
  run to completion, locale switched to Indonesian through the sidebar switcher):
  - `/executions/<id>`: `lang="id"`, status badge `Berhasil`, duration `23 ms`,
    edge counts `1 item` and `3 item` — the English render of the same run reads
    `1 item` / `3 items`, so the plural is proven per locale — and the canvas
    legend `Pemutaran ulang hanya baca`.
  - `/app/workflows/<id>`: `Tambah langkah`, `Jalankan`, `Nonaktif`,
    `Semua perubahan tersimpan`, toolbar `Urungkan (⌘Z)` / `Ulangi` / `Rapikan` /
    `Simpan` / `Riwayat` / `Pintasan papan tik` / `Aktifkan`, canvas controls
    `Panel kontrol` / `Perbesar` / `Perkecil` / `Sesuaikan tampilan`, and
    `Tambah langkah setelah Downstream`; switching back to English restores
    `Control Panel` / `Zoom In` / `Zoom Out` / `Fit View` / `Tidy up` live.
  - Version panel: `Riwayat versi`, `Belum ada yang diterbitkan.`, tabs `Versi` /
    `Lini masa penerbitan`, `Draf saat ini`, `Revisi 3` and the `Draf` badge, with
    `Tutup` from the shared primitive; the confirmation copy is exercised by
    `Restore` -> `Pulihkan revisi ini?` rendered from `versions_confirm_restore_title`
    plus `versions_restore_confirmation`.

Acceptance criteria: criterion 1 is NOT ticked yet and that is the honest
position — 1 of 14 `PENDING` files remains (`routes/embed/[id]/+page.svelte`), so
"no user-visible English literal remains in web/src/lib/components and
web/src/routes" is one file away, and stage 4 owns it. Criterion 3 is met for the
dashboard and the editor (proved above, screen by screen); the embedded editor is
stage 4. Criterion 2 still waits on the embed session's locale (stage 4).

### Stage 4 of 4 — the embed's locale end to end, the guard closed, and the e2e proof

Scope: the embed surface (session module, embed shell, embedded editor), the
SDK's mount-time locale, and the two e2e tests that prove the second locale on
the assembled product. 14 files: 12 modified, 1 new spec, 2 new catalogs.

What changed:
- `web/messages/{en,id}/embed.json` (new, 14 keys per locale) and the `embed`
  area appended LAST to `project.inlang/settings.json`'s `pathPattern`, so the
  merge order of every earlier area is untouched.
- `web/src/lib/embed/session.svelte.ts`: `EmbedSession.locale: Locale`, resolved
  in `acceptEmbedSession` with the generated `isLocale`
  (`isLocale(typeof data.locale === 'string' ? data.locale : '') ? (data.locale
  as Locale) : baseLocale`) — one locale list for the whole app, no second
  regex — so an absent, wrong-typed or unsupported tag falls back to the base
  locale instead of failing the handshake. `embedSession`'s `onMessage` calls
  `setLocale(accepted.session.locale)` immediately before
  `session = accepted.session`, for the same ordering reason the token
  attachment above it already documents. All six user-visible handshake strings
  now come from the `embed` area.
- The "not our message" branch is no longer a string comparison:
  `acceptEmbedSession` returns `{ error, notSessionMessage?: true }`
  (`EmbedSessionRefusal`), because the refusal text is localized now and
  comparing it would have been a locale-dependent bug the moment a host sent a
  non-base locale.
- `web/src/routes/embed/[id]/+page.svelte`: the four shell strings, and the
  `<svelte:head>` title is parameterised (`embed_page_title_branded({ brand })`)
  so the brand name is the only interpolation.
- `web/src/lib/embed/embed-editor.svelte`: the read-only badge, both banners,
  both loading states, the run sentences and the six `Unexpected …`
  diagnostics. Nine of those strings already existed byte-identical in the
  `workflows`/`editor` areas (`workflows_editor_stale`,
  `workflows_newer_revision`, `workflows_reload_theirs`, `workflows_keep_mine`,
  `workflows_refresh`, `workflows_run_queued`, `workflows_run_succeeded`,
  `workflows_run_status`, `workflows_execution_status`,
  `editor_state_read_only`, `workflows_unexpected_*`) and are reused rather than
  duplicated, so one sentence has one home.
- `sdk/src/browser.ts`: `MountOptions.locale?: string`, documented with its
  precedence and its validation rule, posted in the handshake payload beside
  token/workflowId/scopes/branding. `sdk/CHANGELOG.md` gains an `## Unreleased`
  **Additive** entry; `sdk/README.md`'s mount example gains `locale: 'id'` plus
  one sentence on the handshake; `sdk/examples/reference-host/` now carries a
  real locale per tenant (`server.mjs` gains `locale` on the registry and in the
  `/info` response) and `tenant.html` passes `locale: info.locale`.
- `e2e/helpers/stub.ts`: the host page reads `?locale=` and posts it in the
  session message — the two lines the plan reserved, nothing else.
- `e2e/tests/i18n.spec.ts` (new, exactly two tests).
- `docs/src/content/docs/guides/embedding.md`: one appended paragraph
  ("**Language.**") after the handshake paragraph in step 4.
  `community-nodes.md` and `concepts/tenancy-and-embedding.md` were not touched.
- The copy guard: `PENDING` is now an empty `Record<string, true>` (it was a
  `Set` while it had entries — the repository's own rule wants a static
  string-keyed table) and a new test asserts it stays empty. Both older tests
  survive: the rot check still runs over whatever `PENDING` holds, and the
  violation filter still consults it.

Verification, with the real outcomes (`cd web` unless stated):
- `pnpm check` — `COMPLETED 2785 FILES 0 ERRORS 0 WARNINGS 0 FILES_WITH_PROBLEMS`.
- `pnpm test` — `Test Files 47 passed (47)`, `Tests 532 passed (532)`: stage 3's
  529 plus the three new ones (two in `src/lib/embed/session.test.ts`, one in the
  guard). No existing test was edited, weakened or deleted; the session test's
  `session()` factory gained the now-required `locale` field and nothing else in
  that file changed.
- `pnpm install --frozen-lockfile` — ok; no dependency moved this stage.
- Falsification of the guard, reverted immediately: putting
  `Waiting for the host application…` back into
  `routes/embed/[id]/+page.svelte` fails it with
  `+page.svelte:37 — text "Waiting for the host application…"` and
  `… :37 — catalog echo of embed_waiting`; reverting turns it green (4 tests
  pass).
- Falsification of the locale contract, reverted immediately: replacing the
  resolution with `const locale = baseLocale;` fails `keeps the locale the host
  declared` with `expected 'en' to be 'id'`, while the fallback test still
  passes — the pair of behaviours the field is supposed to have.
- Byte-identity of `en`: for all 13 non-parameterised `embed` keys a script
  compared the catalog value with the literal as it stood at HEAD in the file it
  came from — 13/13 present at HEAD and byte-identical, 0 mismatches.
  `embed_page_title_branded` (`{brand} workflow`) is the parameterised form of
  the template literal `${branding.name} workflow`.
- Residual-copy sweep of the embed surface: no quoted English sentence and no
  capitalised text node is left in `web/src/lib/embed/` or
  `web/src/routes/embed/` (the only match is a test fixture's brand name).
- `sdk`: `pnpm check` clean, `pnpm test` `Test Files 6 passed (6)`,
  `Tests 82 passed (82)`, `pnpm build` ok, `make sdk-version-check` ok. The new
  `posts the mount-time locale with the session` test is the contract test for
  "pass it through the embed SDK's mount options": it fails if the field is
  dropped from the payload.
- `go build ./... && go vet ./...` clean, `go test ./internal/guardrails/...` ok,
  `go test -race ./internal/guardrails/...` ok: no Go changed, but the licence
  boundary still passes with the two frontend devDependencies declared.
- `make generate-api-check generate-types-check generate-api-reference-check` —
  all three no-ops (`api-reference: 14 pages fresh (KilasFlow 0.1.0-dev, 74
  operations)`), and `git status` shows no generated file modified: no API
  operation, header or schema changed.
- `make build-all` — SPA embedded and `bin/kilasflow` built.
- `cd e2e && pnpm exec playwright test tests/i18n.spec.ts --retries=0` —
  `2 passed (1.2m)`. Both proofs are behavioural. The dashboard test switches
  the sidebar switcher to Bahasa Indonesia, asserts `html[lang=id]` and the
  Indonesian nav label, then navigates to the editor route and asserts
  `html[lang=id]` AND the editor toolbar's `Simpan` control — which is what
  proves the preference survives a reload and that the editor, not just the
  shell, is localized. The embed test mints a session, loads the stub host with
  `?locale=id`, waits for `kilasflow:embed-ready`, then asserts the frame's
  `html[lang=id]` and the Indonesian `Simpan` label inside
  `frameLocator('#e2e-frame')`. It runs in a fresh Playwright context with empty
  localStorage, so the session is the only source that could have produced it.
- `cd e2e && pnpm exec playwright test tests/i18n.spec.ts tests/smoke.spec.ts
  --retries=0` — `7 passed (1.8m)`, so the handshake the SDK performs and the
  frame accepts still works for a host that sends no locale at all.
- `node e2e/scripts/skip-budget.mjs` — `0/24 skipped, every one of them
  explained`.

Deliberate scope notes:
- No Go change and no API change: the session's `locale` is a validated field of
  the postMessage payload, minted by the host at mount time, rather than a
  server-side `embedding.Branding` field — which would have meant a huma spec,
  orval and a docs regeneration for a value the frame can validate itself with
  the one locale list it already ships. The server-side alternative stays the
  owner decision recorded in the plan.
- `sdk/src/version.ts` says additive surface takes a minor bump, and this stage
  did not bump: the brief reserves the version number to the owner's release
  call, so the change is recorded under `## Unreleased` in the changelog
  instead. `make sdk-version-check` (the version-agreement gate) passes because
  `SDK_VERSION` and `sdk/package.json` still agree.

Acceptance criteria: all three are now met and ticked. (1) `copy.test.ts` walks
every `.svelte` file under `src/lib/components` and `src/routes` with an empty
`PENDING` and reports nothing, and the falsification above shows the walker
still bites. (2) `<html lang>` follows the runtime locale everywhere (the root
layout effect) and the embed frame's locale comes from its session — the unit
test proves the session keeps a declared locale and falls back for one it does
not carry, and the embed e2e proves it in a real frame. (3) both e2e tests
render Indonesian end to end, in the dashboard editor and in the embedded
editor.
