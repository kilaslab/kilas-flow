---
id: FEAT-15k49d
title: 'i18n infrastructure: message catalog, plurals, and a locale for the embed'
status: done
priority: medium
labels:
    - i18n
    - frontend
created: "2026-09-20T00:40:36Z"
updated: "2026-09-21T00:43:06Z"
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

## Work Evidence

Closed by `pine close --evidence` on 2026-09-21.

- Base: `b41e9c4e` (last commit at or before ticket created 2026-09-20)
- Commits (3):
  - `e88ec63e` — FEAT-15k49d: a message catalog, real plurals, and a locale for the embed
  - `64639963` — chore(pine): close EPIC-cfe7ny with the final verification record
  - `31c424f8` — chore(pine): file editor-loop, conditions-coercion, paging and follow-up tickets
- Files changed (base → working tree):

```
 .editorconfig                                      |   33 +
 .env.example                                       |    2 +-
 .github/ISSUE_TEMPLATE/bug_report.yml              |   89 +
 .github/ISSUE_TEMPLATE/config.yml                  |   11 +
 .github/ISSUE_TEMPLATE/feature_request.yml         |   57 +
 .github/PULL_REQUEST_TEMPLATE.md                   |   25 +
 .github/actions/js-toolchain/action.yml            |   11 +-
 .github/dependabot.yml                             |   78 +
 .github/workflows/ci.yml                           |   31 +-
 .github/workflows/release.yml                      |  123 +-
 .gitignore                                         |    1 +
 .pine/MEMORY.md                                    |    4 +
 .pine/memory/e2e.md                                |    9 +
 .pine/memory/embedding.md                          |    8 +
 .pine/tickets/BUG-1tj5wy.md                        |  455 +++-
 .pine/tickets/BUG-277a2m.md                        |  472 ++++-
 .pine/tickets/BUG-341sxn.md                        |  623 ++++++
 .pine/tickets/BUG-4053h6.md                        |  573 +++++-
 .pine/tickets/BUG-57n76x.md                        |  455 +++-
 .pine/tickets/BUG-5gws7n.md                        |   97 +
 .pine/tickets/BUG-66es9z.md                        |  248 +++
 .pine/tickets/BUG-6as5y7.md                        |  474 ++++-
 .pine/tickets/BUG-6bqh51.md                        |  455 +++-
 .pine/tickets/BUG-6jvcs5.md                        |  499 ++++-
 .pine/tickets/BUG-8dmp5y.md                        |  563 ++++-
 .pine/tickets/BUG-8h4yy1.md                        |  462 ++++-
 .pine/tickets/BUG-8sb0jw.md                        |  509 ++++-
 .pine/tickets/BUG-8t94wn.md                        |  486 ++++-
 .pine/tickets/BUG-9853ay.md                        |  519 ++++-
 .pine/tickets/BUG-a1648n.md                        |  110 +
 .pine/tickets/BUG-a9mp5a.md                        |  Bin 10476 -> 38076 bytes
 .pine/tickets/BUG-aede06.md                        |  628 +++++-
 .pine/tickets/BUG-c241hm.md                        |  485 ++++-
 .pine/tickets/BUG-cq4yk3.md                        |  511 ++++-
 .pine/tickets/BUG-dndnhn.md                        |  453 +++-
 .pine/tickets/BUG-esb9sh.md                        |  456 +++-
 .pine/tickets/BUG-f9frth.md                        |  604 +++++-
 .pine/tickets/BUG-fng4m2.md                        |   88 +
 .pine/tickets/BUG-fv5fer.md                        |  525 ++++-
 .pine/tickets/BUG-fvdz46.md                        |  400 ++++
 .pine/tickets/BUG-gaavr5.md                        |  508 ++++-
 .pine/tickets/BUG-hfhzq6.md                        |  457 +++-
 .pine/tickets/BUG-hm76dq.md                        |  477 ++++-
 .pine/tickets/BUG-j7rtv3.md                        |  137 ++
 .pine/tickets/BUG-kzkvv6.md                        |  478 ++++-
 .pine/tickets/BUG-mewhrd.md                        |  455 +++-
 .pine/tickets/BUG-mz8xrb.md                        |  472 ++++-
 .pine/tickets/BUG-npfz43.md                        |  455 +++-
 .pine/tickets/BUG-p3t7yq.md                        |  212 ++
 .pine/tickets/BUG-pwckhd.md                        |  453 +++-
 .pine/tickets/BUG-qmgz2f.md                        |  506 ++++-
 .pine/tickets/BUG-qq4xva.md                        |  471 ++++-
 .pine/tickets/BUG-rjd6fm.md                        |  453 +++-
 .pine/tickets/BUG-rpkjpy.md                        |  984 +++++++++
 .pine/tickets/BUG-rrkjrd.md                        |  461 ++++-
 .pine/tickets/BUG-s0wy50.md                        |  455 +++-
 .pine/tickets/BUG-t2wezf.md                        |  468 ++++-
 .pine/tickets/BUG-t9j2ek.md                        |  452 ++++
 .pine/tickets/BUG-tcqkad.md                        |  500 ++++-
 .pine/tickets/BUG-th16c1.md                        |  218 ++
 .pine/tickets/BUG-txc9xg.md                        |  453 +++-
 .pine/tickets/BUG-vzzkg3.md                        |  598 ++++++
 .pine/tickets/BUG-w8h3km.md                        |  123 ++
 .pine/tickets/BUG-wdypd2.md                        |  488 ++++-
 .pine/tickets/BUG-wp2y0y.md                        |  454 +++-
 .pine/tickets/BUG-xf1wqm.md                        |  455 +++-
 .pine/tickets/BUG-xmr673.md                        |  737 +++++++
 .pine/tickets/BUG-y57cz4.md                        |  532 ++++-
 .pine/tickets/BUG-ysvmaa.md                        |  578 +++++-
 .pine/tickets/BUG-ze1nn8.md                        |  454 +++-
 .pine/tickets/BUG-ztzxck.md                        |  473 ++++-
 .pine/tickets/EPIC-87t47t.md                       |   50 +
 .pine/tickets/EPIC-bkj6yf.md                       |   46 +
 .pine/tickets/EPIC-cfe7ny.md                       |   26 +-
 .pine/tickets/EPIC-m0bne8.md                       |   57 +
 .pine/tickets/EPIC-r0yg5q.md                       |   27 +
 .pine/tickets/FEAT-0895qc.md                       |  457 +++-
 .pine/tickets/FEAT-15k49d.md                       |  494 +++++
 .pine/tickets/FEAT-1axhdn.md                       | 2017 +++++++++++++++++-
 .pine/tickets/FEAT-2mth85.md                       |   57 +
 .pine/tickets/FEAT-3taswf.md                       | 1199 ++++++++---
 .pine/tickets/FEAT-41m8dj.md                       |   55 +
 .pine/tickets/FEAT-48hreg.md                       |   20 +-
 .pine/tickets/FEAT-4jns31.md                       |   26 +
 .pine/tickets/FEAT-4ve1bq.md                       |   67 +
 .pine/tickets/FEAT-56nep4.md                       |  456 +++-
 .pine/tickets/FEAT-77rveq.md                       |   73 +
 .pine/tickets/FEAT-7cg0cd.md                       |   20 +-
 .pine/tickets/FEAT-8mymac.md                       |    4 +-
 .pine/tickets/FEAT-a5fhjw.md                       |  401 ++++
 .pine/tickets/FEAT-adyeh0.md                       |   53 +
 .pine/tickets/FEAT-bb4s6e.md                       |   27 +
 .pine/tickets/FEAT-bp59m4.md                       |  420 ++++
 .pine/tickets/FEAT-c72set.md                       |   26 +
 .pine/tickets/FEAT-cwmw90.md                       |  530 +++++
 .pine/tickets/FEAT-ds4e0m.md                       |   54 +
 .pine/tickets/FEAT-edxxj7.md                       |  496 ++++-
 .pine/tickets/FEAT-emf6k5.md                       |  573 ++++++
 .pine/tickets/FEAT-ew46cb.md                       |   26 +
 .pine/tickets/FEAT-fpqvwx.md                       |  936 +++++++++
 .pine/tickets/FEAT-g07pj8.md                       |   67 +
 .pine/tickets/FEAT-hj8pyx.md                       |  750 +++++++
 .pine/tickets/FEAT-j5s2n4.md                       |  503 ++++-
 .pine/tickets/FEAT-jvembs.md                       |  484 ++++-
 .pine/tickets/FEAT-m4d2y1.md                       |   30 +
 .pine/tickets/FEAT-mha6a0.md                       |  259 +++
 .pine/tickets/FEAT-nqpvf6.md                       |  463 ++++-
 .pine/tickets/FEAT-p77zr3.md                       |   67 +
 .pine/tickets/FEAT-qdedm0.md                       | 1025 +++++++++
 .pine/tickets/FEAT-x5km1z.md                       |  454 +++-
 .pine/tickets/FEAT-x5qqpm.md                       |   26 +
 .pine/tickets/FEAT-yxwyav.md                       |   26 +
 CHANGELOG.md                                       |  169 ++
 CODE_OF_CONDUCT.md                                 |  174 ++
 CONTRIBUTING.md                                    |  145 ++
 Dockerfile                                         |   15 +-
 Makefile                                           |   69 +-
 README.md                                          |   78 +-
 SECURITY.md                                        |  101 +
 cmd/kilasflow/embed_issuer.go                      |   18 +
 cmd/kilasflow/embed_issuer_test.go                 |  134 ++
 cmd/kilasflow/fleet.go                             |   92 +
 cmd/kilasflow/fleet_test.go                        |  395 ++++
 cmd/kilasflow/idempotency_test.go                  |  225 ++
 cmd/kilasflow/main.go                              |  371 +++-
 cmd/kilasflow/main_test.go                         |    6 +-
 cmd/kilasflow/retention_test.go                    |    8 +-
 cmd/kilasflow/secrets_boot_test.go                 |    4 +-
 cmd/kilasflow/webhook_wiring_test.go               |  138 ++
 cmd/nodepackgen/authorcmd.go                       |    2 +-
 cmd/nodepackgen/generate.go                        |    8 +-
 cmd/nodepackgen/generate_test.go                   |   12 +-
 compose.postgres.yaml                              |    9 +-
 compose.yaml                                       |   16 +-
 config.example.yaml                                |  110 +-
 devbox.json                                        |    2 -
 docs/astro.config.mjs                              |    8 +-
 docs/src/content/docs/404.md                       |    2 +-
 docs/src/content/docs/concepts/credentials.md      |   21 +-
 .../content/docs/concepts/datastore-concurrency.md |  179 ++
 docs/src/content/docs/concepts/execution-model.md  |  211 +-
 docs/src/content/docs/concepts/expressions.md      |   70 +-
 docs/src/content/docs/concepts/node-registry.md    |   75 +-
 .../src/content/docs/concepts/safety-boundaries.md |  119 +-
 .../content/docs/concepts/tenancy-and-embedding.md |   27 +-
 docs/src/content/docs/concepts/webhooks.md         |  169 +-
 docs/src/content/docs/guides/community-nodes.md    |  133 +-
 docs/src/content/docs/guides/embedding.md          |   57 +-
 docs/src/content/docs/guides/idempotency.md        |  197 ++
 docs/src/content/docs/guides/n8n-migration.md      |  125 +-
 docs/src/content/docs/guides/node-authoring.md     |   16 +-
 .../src/content/docs/guides/tenant-scoped-nodes.md |  183 ++
 docs/src/content/docs/index.mdx                    |    2 +-
 .../docs/operate/configuration-reference.md        |  178 +-
 docs/src/content/docs/operate/deployment.md        |   36 +-
 docs/src/content/docs/operate/security.md          |   69 +-
 docs/src/content/docs/operate/tenant-deletion.md   |  194 ++
 docs/src/content/docs/operate/upgrades.md          |   78 +-
 docs/src/content/docs/reference/api-contract.md    |   45 +-
 docs/src/content/docs/reference/api.md             |    8 +-
 docs/src/content/docs/reference/api/auth.md        |   11 +-
 docs/src/content/docs/reference/api/credentials.md |    9 +-
 docs/src/content/docs/reference/api/datastores.md  |  417 ++++
 docs/src/content/docs/reference/api/embed.md       |    6 +-
 docs/src/content/docs/reference/api/errors.md      |   13 +-
 docs/src/content/docs/reference/api/executions.md  |    4 +-
 docs/src/content/docs/reference/api/interop.md     |    4 +-
 docs/src/content/docs/reference/api/nodes.md       |   18 +-
 docs/src/content/docs/reference/api/schedules.md   |   11 +-
 docs/src/content/docs/reference/api/system.md      |    7 +-
 docs/src/content/docs/reference/api/tenants.md     |  221 ++
 docs/src/content/docs/reference/api/workflows.md   |   58 +-
 docs/src/content/docs/reference/cli.md             |  628 ++++++
 docs/src/content/docs/reference/node-packs.md      |   15 +-
 docs/src/content/docs/start/install.md             |   58 +-
 docs/src/content/docs/start/what-kilasflow-is.md   |   92 +-
 .../specs/2026-09-20-agent-surface-design.md       |  519 +++++
 e2e/fixtures/error-form-nodes.ts                   |  218 ++
 e2e/fixtures/live-backend.ts                       |  263 +++
 e2e/fixtures/n8n-live.ts                           |    5 +
 e2e/fixtures/pack-convert-driver.go                |    2 +-
 e2e/helpers/stub.ts                                |   11 +-
 e2e/tests/dashboard-lists.spec.ts                  |  187 ++
 e2e/tests/datastore.spec.ts                        |    2 +-
 e2e/tests/i18n.spec.ts                             |   62 +
 e2e/tests/library-import.spec.ts                   |    6 +-
 e2e/tests/live-backend-api.spec.ts                 |  386 ++++
 e2e/tests/live-backend-datastore.spec.ts           |  282 +++
 e2e/tests/live-backend-http-auth.spec.ts           |   94 +
 e2e/tests/live-backend-queue.spec.ts               |  213 ++
 e2e/tests/live-backend-webhook.spec.ts             |  401 ++++
 e2e/tests/n8n-compare.spec.ts                      |   53 +-
 e2e/tests/node-coverage.spec.ts                    |   56 +-
 e2e/tests/pack-editor.spec.ts                      |   39 +-
 e2e/tests/waha-migration.spec.ts                   |    5 +-
 go.mod                                             |    4 +-
 go.sum                                             |    4 +
 internal/ai/agent.go                               |  104 +-
 internal/ai/agent_output_test.go                   |    2 +-
 internal/ai/ai.go                                  |   47 +-
 internal/ai/ai_test.go                             |  134 +-
 internal/ai/fromai_test.go                         |    2 +-
 internal/ai/maf/runtime.go                         |    2 +-
 internal/ai/maf/runtime_test.go                    |    2 +-
 internal/ai/memory.go                              |   21 +
 internal/ai/openai.go                              |  146 +-
 internal/ai/openai_test.go                         |  147 +-
 internal/ai/outputschema.go                        |   16 +
 internal/api/auth_test.go                          |   28 +-
 internal/api/cors_test.go                          |  136 ++
 internal/api/credentials_pagination_test.go        |   76 +
 internal/api/credentials_test.go                   |  114 +-
 internal/api/csv_export_test.go                    |   66 +
 internal/api/datastores_csv_test.go                |    2 +-
 internal/api/datastores_test.go                    |  471 ++++-
 internal/api/embed_confinement_test.go             |  352 ++++
 internal/api/embed_datastore_test.go               |   32 +-
 internal/api/embed_defaults_test.go                |  150 ++
 internal/api/embed_test.go                         |   35 +-
 internal/api/events_test.go                        |   97 +-
 internal/api/handlers/admin.go                     |  678 ++++++
 internal/api/handlers/admin_admin_test.go          |  834 ++++++++
 internal/api/handlers/auth.go                      |  237 ++-
 internal/api/handlers/auth_test.go                 |  422 ++++
 internal/api/handlers/credentials.go               |   73 +-
 internal/api/handlers/datastores.go                |  371 +++-
 internal/api/handlers/datastores_csv.go            |   80 +-
 internal/api/handlers/datastores_csv_test.go       |  147 ++
 internal/api/handlers/embed.go                     |   20 +-
 internal/api/handlers/embedscope.go                |  126 ++
 internal/api/handlers/executions.go                |  332 ++-
 internal/api/handlers/idempotency.go               |  115 ++
 internal/api/handlers/interop.go                   |  148 +-
 internal/api/handlers/nodes.go                     |  134 +-
 internal/api/handlers/problem.go                   |  129 ++
 internal/api/handlers/resume.go                    |    8 +-
 internal/api/handlers/resume_test.go               |   12 +-
 internal/api/handlers/schedules.go                 |   56 +-
 internal/api/handlers/system.go                    |  142 +-
 internal/api/handlers/tenants.go                   |    6 +-
 internal/api/handlers/workflows.go                 |  320 ++-
 internal/api/handlers/workflows_conflict_test.go   |    8 +-
 internal/api/handlers/workflows_delete_test.go     |    8 +-
 internal/api/idempotency_test.go                   | 1004 +++++++++
 internal/api/import_diagnostics_test.go            |  142 ++
 internal/api/list_pagination_test.go               |  221 ++
 internal/api/middleware/auth.go                    |   86 +-
 internal/api/middleware/auth_test.go               |  436 ++++
 internal/api/middleware/clientip.go                |   50 +
 internal/api/middleware/cors.go                    |   25 +-
 internal/api/middleware/cors_test.go               |   52 +-
 internal/api/middleware/embed.go                   |    5 +-
 internal/api/middleware/embed_test.go              |    4 +-
 internal/api/middleware/loginlimit.go              |  216 ++
 internal/api/middleware/loginlimit_test.go         |  171 ++
 internal/api/middleware/sessioncache.go            |  156 ++
 internal/api/node_types_test.go                    |  109 +-
 internal/api/node_visibility_test.go               |  544 +++++
 internal/api/openapi_security_test.go              |  196 ++
 internal/api/ready_fleet_test.go                   |  358 ++++
 internal/api/routes.go                             |   63 +-
 internal/api/server.go                             |  176 +-
 internal/api/server_test.go                        |    4 +-
 internal/api/tenant_delete_test.go                 |  386 ++++
 internal/api/workflow_history_test.go              |    2 +-
 internal/api/workflows_test.go                     |  187 +-
 internal/auth/auth_test.go                         |   44 +-
 internal/auth/session.go                           |   94 +-
 internal/auth/session_internal_test.go             |   49 +
 internal/binary/binary.go                          |  134 +-
 internal/binary/binary_test.go                     |  219 +-
 internal/cli/api_prefix.go                         |   43 +
 internal/cli/cli.go                                |  382 ++++
 internal/cli/cli_test.go                           |  248 +++
 internal/cli/client.go                             |  397 ++++
 internal/cli/client_test.go                        |  320 +++
 internal/cli/command.go                            |  139 ++
 internal/cli/command_test.go                       |  302 +++
 internal/cli/config.go                             |  258 +++
 internal/cli/config_test.go                        |  749 +++++++
 internal/cli/context.go                            |  318 +++
 internal/cli/context_test.go                       |  236 +++
 internal/cli/doc.go                                |   35 +
 internal/cli/exit.go                               |  113 +
 internal/cli/exit_test.go                          |  101 +
 internal/cli/flags.go                              |   84 +
 internal/cli/guard.go                              |   43 +
 internal/cli/guard_test.go                         |  212 ++
 internal/cli/openapi.go                            |  202 ++
 internal/cli/openapi_contract_test.go              |  411 ++++
 internal/cli/output.go                             |  116 ++
 internal/cli/output_test.go                        |  246 +++
 internal/cli/sse.go                                |  151 ++
 internal/cli/sse_test.go                           |  149 ++
 internal/cli/verbs_api.go                          |  315 +++
 internal/cli/verbs_api_test.go                     |  820 ++++++++
 internal/cli/verbs_auth.go                         |  289 +++
 internal/cli/verbs_credential.go                   |  146 ++
 internal/cli/verbs_credential_test.go              |  119 ++
 internal/cli/verbs_datastore.go                    |  209 ++
 internal/cli/verbs_datastore_test.go               |  215 ++
 internal/cli/verbs_exec.go                         |  413 ++++
 internal/cli/verbs_exec_test.go                    |  436 ++++
 internal/cli/verbs_node.go                         |  292 +++
 internal/cli/verbs_node_test.go                    |  196 ++
 internal/cli/verbs_pack.go                         |  143 ++
 internal/cli/verbs_pack_test.go                    |  195 ++
 internal/cli/verbs_run.go                          |  257 +++
 internal/cli/verbs_run_test.go                     |  314 +++
 internal/cli/verbs_schedule.go                     |   36 +
 internal/cli/verbs_schedule_test.go                |   46 +
 internal/cli/verbs_system.go                       |  282 +++
 internal/cli/verbs_system_test.go                  |  182 ++
 internal/cli/verbs_tenant.go                       |   86 +
 internal/cli/verbs_tenant_test.go                  |  116 ++
 internal/cli/verbs_workflow.go                     |  592 ++++++
 internal/cli/verbs_workflow_test.go                |  425 ++++
 internal/conditions/conditions.go                  |  455 +++-
 internal/conditions/conditions_test.go             |  197 +-
 internal/conditions/doc.go                         |    8 +
 internal/config/boot_strictness_test.go            |  323 +++
 internal/config/config.go                          |  476 ++++-
 internal/config/config_test.go                     |  104 +-
 internal/config/embed_branding_test.go             |  192 ++
 internal/config/embed_validate.go                  |   34 +
 internal/config/packs_visibility.go                |   83 +
 internal/config/packs_visibility_test.go           |  168 ++
 internal/config/webhook_require_auth_test.go       |   50 +
 internal/credentials/builtin.go                    |   70 +-
 internal/credentials/credentials_test.go           |   62 +-
 internal/credentials/external.go                   |    4 +-
 internal/credentials/external_test.go              |    6 +-
 internal/credentials/redirect_test.go              |  141 ++
 internal/credentials/registry.go                   |   83 +-
 internal/credentials/vault.go                      |    2 +-
 internal/database/database.go                      |    2 +-
 internal/database/database_test.go                 |    2 +-
 internal/database/migrate.go                       |   37 +-
 internal/database/migrate_test.go                  |  103 +-
 internal/database/prefix_test.go                   |    4 +-
 internal/database/tenant_columns_test.go           |  309 +++
 internal/database/webhook_route_backfill_test.go   |  491 +++++
 internal/datastore/catalogue.go                    |  164 +-
 internal/datastore/catalogue_test.go               |  115 ++
 internal/datastore/column_tenant_test.go           |  152 ++
 internal/datastore/concurrency.go                  |  224 +-
 internal/datastore/concurrency_test.go             |  579 +++++-
 internal/datastore/config_bind_test.go             |    2 +-
 internal/datastore/doc.go                          |   13 +-
 internal/datastore/engine.go                       |   31 +-
 internal/datastore/engine_test.go                  |   70 +-
 internal/datastore/fleet.go                        |  361 +++-
 internal/datastore/fleet_engine_test.go            |  711 +++++++
 internal/datastore/idents_test.go                  |    2 +-
 internal/datastore/isolation.go                    |   96 +-
 internal/datastore/isolation_test.go               |  245 ++-
 internal/datastore/migrate_test.go                 |    1 +
 internal/datastore/model.go                        |    9 +-
 internal/datastore/rows.go                         |  124 +-
 internal/datastore/trace_test.go                   |    2 +-
 internal/datastore/upsert_id.go                    |  247 +++
 internal/datastore/upsert_id_test.go               |  498 +++++
 internal/datetime/datetime_test.go                 |    2 +-
 internal/embed/confinement.go                      |  156 ++
 internal/embed/confinement_test.go                 |  142 ++
 internal/embed/embed.go                            |  146 +-
 internal/embed/embed_branding_test.go              |  164 ++
 internal/embed/embed_lifetime_test.go              |  146 ++
 internal/embed/embed_test.go                       |   16 +-
 internal/engine/approval.go                        |   19 +-
 internal/engine/approval_test.go                   |    4 +-
 internal/engine/authenticate.go                    |   23 +-
 internal/engine/authenticate_test.go               |  135 ++
 internal/engine/checkpoint.go                      |   19 +-
 internal/engine/datastore_concurrency_test.go      |  393 ++++
 internal/engine/error_workflow_test.go             |  230 +++
 internal/engine/export_test.go                     |   20 +
 internal/engine/expression_context_test.go         |    6 +-
 internal/engine/lease_test.go                      |   24 +-
 internal/engine/live_progress_test.go              |  187 ++
 internal/engine/loopstate_test.go                  |  183 ++
 internal/engine/multiproc.go                       |    4 +-
 internal/engine/multiprocess_test.go               |   42 +-
 internal/engine/runner.go                          | 1554 +++++++++-----
 internal/engine/runner_test.go                     | 1118 +++++++++-
 internal/engine/service.go                         |  327 ++-
 internal/engine/service_test.go                    |  407 +++-
 internal/engine/subworkflow_test.go                |   24 +-
 internal/engine/tenant_visibility_test.go          |  379 ++++
 internal/engine/trace.go                           |    2 +-
 internal/engine/trace_persist_test.go              |   39 +-
 internal/engine/trace_test.go                      |   28 +-
 internal/engine/wait_service.go                    |  116 +-
 internal/engine/wait_service_test.go               |  551 ++++-
 internal/engine/worker_test.go                     |    8 +-
 internal/events/events.go                          |    2 +-
 internal/events/events_test.go                     |    2 +-
 internal/execution/records.go                      |   11 +
 internal/execution/redact_datastore_test.go        |    2 +-
 internal/execution/redact_test.go                  |    2 +-
 internal/expression/doc.go                         |   13 +
 internal/expression/evaluator.go                   |  227 +-
 internal/expression/expression.go                  |  165 +-
 internal/expression/expression_test.go             |    2 +-
 internal/expression/globals.go                     |   45 +-
 internal/expression/methods.go                     |   40 +-
 internal/expression/parity_test.go                 |  538 ++++-
 internal/expression/roots.go                       |   26 +
 internal/guardrails/compile_scope_test.go          |  440 ++++
 internal/guardrails/licence_boundary_test.go       |    4 +-
 internal/idempotency/hash.go                       |   64 +
 internal/idempotency/hash_test.go                  |  142 ++
 internal/idempotency/idempotency.go                |  432 ++++
 internal/idempotency/idempotency_test.go           | 1120 ++++++++++
 internal/idempotency/sweeper.go                    |   94 +
 internal/idempotency/sweeper_test.go               |  146 ++
 internal/interop/n8n/corpus/BASELINE.md            |   16 +-
 internal/interop/n8n/corpus/baseline.json          |   43 +-
 internal/interop/n8n/corpus/scoreboard_test.go     |   36 +-
 internal/interop/n8n/gowa.go                       |   39 +-
 internal/interop/n8n/gowa_test.go                  |   19 +-
 internal/interop/n8n/importer_tail_test.go         | 1087 ++++++++++
 internal/interop/n8n/n8n.go                        |  495 ++++-
 internal/interop/n8n/n8n_test.go                   |  248 ++-
 internal/interop/n8n/parameters.go                 | 2174 ++++++++++++++++++--
 internal/interop/n8n/sqlfidelity_test.go           |    8 +-
 internal/interop/n8n/trivialcode.go                |  107 -
 internal/interop/n8n/trivialcode_test.go           |  109 -
 internal/interop/n8n/waitsubworkflow_test.go       |  460 +++++
 internal/loadoptions/datastores.go                 |    2 +-
 internal/loadoptions/datastores_test.go            |    6 +-
 internal/loadoptions/loadoptions.go                |   17 +-
 internal/loadoptions/loadoptions_test.go           |   10 +-
 internal/loadoptions/redirect_test.go              |  117 ++
 internal/loadoptions/schema.go                     |    2 +-
 internal/loadoptions/sql.go                        |    4 +-
 internal/loadoptions/sql_test.go                   |   10 +-
 internal/loadoptions/workflows.go                  |    2 +-
 internal/node/registry.go                          |   35 +-
 internal/node/registry_bench_test.go               |  112 +
 internal/node/registry_test.go                     |    6 +-
 internal/node/visibility.go                        |  347 ++++
 internal/node/visibility_test.go                   |  796 +++++++
 internal/nodepack/author.go                        |    6 +-
 internal/nodepack/author_test.go                   |   47 +-
 internal/nodepack/convert.go                       |    8 +-
 internal/nodepack/convert_test.go                  |   10 +-
 internal/nodepack/loaddir.go                       |    8 +-
 internal/nodepack/loaddir_test.go                  |   16 +-
 internal/nodepack/nodepack.go                      |   30 +-
 internal/nodepack/startcase_test.go                |    2 +-
 internal/nodepack/trigger.go                       |   18 +-
 internal/nodepack/trigger_require_auth_test.go     |   43 +
 internal/nodepack/validate.go                      |   25 +-
 internal/nodepack/visibility_test.go               |  262 +++
 internal/property/locator_test.go                  |    4 +-
 internal/property/mapper_test.go                   |    2 +-
 internal/property/visibility_test.go               |    2 +-
 internal/repository/auth.go                        |  392 +++-
 internal/repository/auth_admin_test.go             |  477 +++++
 internal/repository/auth_test.go                   |    8 +-
 internal/repository/claim_lease_test.go            |   12 +-
 internal/repository/claim_wake_test.go             |   14 +-
 internal/repository/credentials.go                 |  107 +-
 internal/repository/credentials_external_test.go   |    8 +-
 internal/repository/execution_retention.go         |    2 +-
 internal/repository/execution_retention_test.go    |   15 +-
 internal/repository/executions.go                  |  131 +-
 internal/repository/idempotency.go                 |  360 ++++
 internal/repository/idempotency_test.go            |  615 ++++++
 internal/repository/import_diagnostics.go          |   83 +
 internal/repository/import_diagnostics_test.go     |  199 ++
 internal/repository/models.go                      |   65 +-
 internal/repository/models_test.go                 |  151 +-
 internal/repository/postgres_execution_test.go     |   30 +-
 internal/repository/prefix_test.go                 |   16 +-
 internal/repository/schedule_list_test.go          |  188 ++
 internal/repository/schedules.go                   |  114 +-
 internal/repository/subworkflow_activation_test.go |  234 +++
 internal/repository/table_names_test.go            |    1 +
 internal/repository/tenant_purge.go                |   48 +-
 internal/repository/tenant_purge_test.go           |   58 +-
 internal/repository/tenant_rows.go                 |  283 +++
 internal/repository/tenant_rows_test.go            |  420 ++++
 internal/repository/waits.go                       |    2 +-
 internal/repository/waits_test.go                  |   12 +-
 internal/repository/webhooks.go                    |  280 ++-
 internal/repository/webhooks_delivery_test.go      |  147 ++
 internal/repository/webhooks_test.go               |  464 +++++
 internal/repository/workflow_history.go            |   16 +-
 internal/repository/workflow_history_test.go       |   10 +-
 internal/repository/workflow_list_test.go          |  196 ++
 internal/repository/workflows.go                   |  257 ++-
 internal/routing/executor.go                       |   12 +-
 internal/routing/request.go                        |    8 +-
 internal/routing/response.go                       |    4 +-
 internal/routing/routing.go                        |    2 +-
 internal/routing/routing_test.go                   |   10 +-
 internal/runcode/runcode_test.go                   |    2 +-
 internal/safehttp/safehttp.go                      |    2 +-
 internal/safehttp/safehttp_test.go                 |    2 +-
 internal/scheduler/doc.go                          |   13 +-
 internal/scheduler/extract.go                      |   56 +-
 internal/scheduler/item.go                         |    2 +-
 internal/scheduler/rule_test.go                    |    2 +-
 internal/scheduler/scheduler.go                    |    2 +-
 internal/scheduler/scheduler_test.go               |   81 +-
 internal/sqlbuild/sqlbuild.go                      |    2 +-
 internal/sqlbuild/sqlbuild_test.go                 |    6 +-
 internal/sqlguard/attack_test.go                   |    2 +-
 internal/sqlguard/sqlguard_test.go                 |    2 +-
 internal/sqlnode/guard_test.go                     |    4 +-
 internal/sqlnode/internal_test.go                  |    4 +-
 internal/sqlnode/policy_test.go                    |    4 +-
 internal/sqlnode/sqlnode.go                        |    6 +-
 internal/sqlnode/sqlnode_test.go                   |    2 +-
 internal/tenantpurge/completeness_test.go          |  368 ++++
 internal/tenantpurge/doc.go                        |  120 ++
 internal/tenantpurge/docs_test.go                  |  115 ++
 internal/tenantpurge/harness_test.go               |  614 ++++++
 internal/tenantpurge/purge.go                      |  412 ++++
 internal/tenantpurge/purge_test.go                 |  507 +++++
 internal/web/embed.go                              |    2 +-
 internal/webhook/export_test.go                    |    4 +-
 internal/webhook/form.go                           |  262 +++
 internal/webhook/form_test.go                      |  169 ++
 internal/webhook/jwt.go                            |  144 ++
 internal/webhook/jwt_test.go                       |  212 ++
 internal/webhook/lifecycle.go                      |    6 +-
 internal/webhook/lifecycle_test.go                 |   10 +-
 internal/webhook/request_lifecycle.go              |  431 +++-
 internal/webhook/request_lifecycle_test.go         |  411 ++++
 internal/webhook/require_auth.go                   |   74 +
 internal/webhook/require_auth_test.go              |  367 ++++
 internal/webhook/route_label_test.go               |  172 ++
 internal/webhook/shape.go                          |  328 ++-
 internal/webhook/shape_test.go                     |  180 +-
 internal/webhook/webhook.go                        |  920 +++++++--
 internal/webhook/webhook_test.go                   |  991 ++++++++-
 internal/workflow/catalog_scope.go                 |   39 +
 internal/workflow/compiler.go                      |  105 +-
 internal/workflow/compiler_test.go                 |    2 +-
 internal/workflow/compiler_visibility_test.go      |  280 +++
 internal/workflow/document.go                      |    4 +
 internal/workflow/document_test.go                 |    8 +-
 internal/workflow/typeversion_test.go              |    2 +-
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../000011_webhook_path_label_index.up.sql         |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   29 +
 .../postgres/000013_node_run_response.down.sql     |    9 +
 .../postgres/000013_node_run_response.up.sql       |   28 +
 .../000014_webhook_route_backfill.down.sql         |   14 +
 .../postgres/000014_webhook_route_backfill.up.sql  |   62 +
 .../postgres/000015_idempotency_keys.down.sql      |    6 +
 migrations/postgres/000015_idempotency_keys.up.sql |   49 +
 .../000016_webhook_deliveries_tenant.down.sql      |   14 +
 .../000016_webhook_deliveries_tenant.up.sql        |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   14 +
 .../000017_datastore_columns_tenant.up.sql         |   39 +
 .../000011_webhook_path_label_index.down.sql       |   12 +
 .../sqlite/000011_webhook_path_label_index.up.sql  |   22 +
 .../000012_workflow_import_diagnostics.down.sql    |    9 +
 .../000012_workflow_import_diagnostics.up.sql      |   24 +
 .../sqlite/000013_node_run_response.down.sql       |    9 +
 migrations/sqlite/000013_node_run_response.up.sql  |   23 +
 .../sqlite/000014_webhook_route_backfill.down.sql  |   14 +
 .../sqlite/000014_webhook_route_backfill.up.sql    |   58 +
 migrations/sqlite/000015_idempotency_keys.down.sql |    6 +
 migrations/sqlite/000015_idempotency_keys.up.sql   |   48 +
 .../000016_webhook_deliveries_tenant.down.sql      |   13 +
 .../sqlite/000016_webhook_deliveries_tenant.up.sql |   52 +
 .../000017_datastore_columns_tenant.down.sql       |   12 +
 .../sqlite/000017_datastore_columns_tenant.up.sql  |   38 +
 nodes/ai.go                                        |  555 ++++-
 nodes/ai_mcp_test.go                               |   10 +-
 nodes/ai_ollama_test.go                            |   16 +-
 nodes/ai_test.go                                   |  890 +++++++-
 nodes/ai_tools_test.go                             |    8 +-
 nodes/annotation.go                                |    6 +-
 nodes/apostrophe_live_test.go                      |    4 +-
 nodes/assignments.go                               |   53 +-
 nodes/bindings_test.go                             |   10 +-
 nodes/code.go                                      |    8 +-
 nodes/code_test.go                                 |   10 +-
 nodes/conditions.go                                |    4 +-
 nodes/core.go                                      |   12 +-
 nodes/database.go                                  |   10 +-
 nodes/database_test.go                             |   12 +-
 nodes/datastore.go                                 |  140 +-
 nodes/datastore_increment.go                       |   85 +
 nodes/datastore_test.go                            |  425 +++-
 nodes/datastore_tool_test.go                       |   12 +-
 nodes/datetime.go                                  |   10 +-
 nodes/datetime_test.go                             |    8 +-
 nodes/embedscope.go                                |  217 ++
 nodes/embedscope_test.go                           |  288 +++
 nodes/error_workflow.go                            |  227 ++
 nodes/error_workflow_test.go                       |  118 ++
 nodes/executors.go                                 |   22 +-
 nodes/executors_test.go                            |   78 +-
 nodes/flow.go                                      |   10 +-
 nodes/flow_test.go                                 |   10 +-
 nodes/http.go                                      |  157 +-
 nodes/http_test.go                                 |  126 +-
 nodes/jscode.go                                    |    6 +-
 nodes/jscode_test.go                               |    6 +-
 nodes/loop.go                                      |    6 +-
 nodes/mysql_v2.go                                  |   10 +-
 nodes/mysql_v2_test.go                             |   10 +-
 nodes/pgvector.go                                  |    8 +-
 nodes/pgvector_test.go                             |   54 +-
 nodes/postgres_v2.go                               |   16 +-
 nodes/postgres_v2_test.go                          |   10 +-
 nodes/presentation_test.go                         |   39 +-
 nodes/routing.go                                   |    6 +-
 nodes/sql_options.go                               |    4 +-
 nodes/sql_options_live_test.go                     |   31 +-
 nodes/sql_options_test.go                          |    6 +-
 nodes/sqlite_attach_test.go                        |    8 +-
 nodes/subworkflow.go                               |  168 +-
 nodes/subworkflow_calls_test.go                    |   90 +
 nodes/telegram.go                                  |    6 +-
 nodes/telegram_download.go                         |    9 +-
 nodes/telegram_lifecycle.go                        |   13 +-
 nodes/telegram_test.go                             |   18 +-
 nodes/transform.go                                 |    6 +-
 nodes/transform_test.go                            |    6 +-
 nodes/unsupported.go                               |   22 +-
 nodes/wait.go                                      |   10 +-
 nodes/webhook.go                                   |  490 ++++-
 packs/telegram/telegram.go                         |    8 +-
 packs/telegram/telegram_test.go                    |   20 +-
 packs/waha/README.md                               |   22 +
 packs/waha/manifest-202409.json                    |   11 +-
 packs/waha/manifest-202502.json                    |   11 +-
 packs/waha/pack-trigger-202409.json                |   11 +-
 packs/waha/pack-trigger-202502.json                |   11 +-
 packs/waha/waha.go                                 |   93 +-
 packs/waha/waha_test.go                            |  411 +++-
 packs/waha/webhook-lifecycle.json                  |   14 +
 pkg/sdk/example/echo/main.go                       |    2 +-
 pkg/sdk/sdk_test.go                                |    2 +-
 pkg/sdk/wasm_exec_test.go                          |    2 +-
 scripts/check-coordinates.sh                       |   87 +
 scripts/config-reference.go                        |    2 +-
 scripts/config-reference_test.go                   |    2 +-
 scripts/generate-api-reference.mjs                 |   47 +-
 scripts/smoke-cli.sh                               |  228 ++
 scripts/smoke-dev.sh                               |   27 +
 scripts/smoke-postgres.sh                          |   22 +
 sdk/CHANGELOG.md                                   |   33 +-
 sdk/LICENSE                                        |  202 ++
 sdk/README.md                                      |   96 +-
 sdk/RELEASING.md                                   |  188 ++
 sdk/examples/host-page/README.md                   |   61 +-
 sdk/examples/host-page/server.mjs                  |   68 +-
 sdk/examples/reference-host/README.md              |  122 +-
 sdk/examples/reference-host/package.json           |    2 +-
 sdk/examples/reference-host/server.mjs             |   37 +-
 sdk/examples/reference-host/tenant.html            |    3 +
 sdk/package.json                                   |   17 +-
 sdk/pnpm-lock.yaml                                 |    3 +
 sdk/scripts/check-example.mjs                      |  320 +++
 sdk/scripts/check-package.mjs                      |  315 +++
 sdk/scripts/lib/pack.mjs                           |   77 +
 sdk/scripts/lib/release.mjs                        |  266 +++
 sdk/scripts/release.mjs                            |  149 ++
 sdk/src/browser.ts                                 |   13 +-
 sdk/src/generated/models.ts                        | 1113 +++++++++-
 sdk/src/http.ts                                    |   35 +-
 sdk/src/server.ts                                  |  296 ++-
 sdk/test/browser.test.ts                           |   24 +
 sdk/test/operation-coverage.test.mjs               |   39 +-
 sdk/test/operations.test.ts                        |   31 +
 sdk/test/release-workflow.test.mjs                 |  223 ++
 sdk/test/release.test.mjs                          |  390 ++++
 sdk/test/server.test.ts                            |  131 +-
 web/messages/en/auth.json                          |   36 +
 web/messages/en/canvas.json                        |   52 +
 web/messages/en/common.json                        |   27 +
 web/messages/en/credentials.json                   |   40 +
 web/messages/en/datastores.json                    |  176 ++
 web/messages/en/editor.json                        |  134 ++
 web/messages/en/embed.json                         |   16 +
 web/messages/en/executions.json                    |   94 +
 web/messages/en/home.json                          |   20 +
 web/messages/en/nav.json                           |   10 +
 web/messages/en/properties.json                    |  118 ++
 web/messages/en/schedules.json                     |   33 +
 web/messages/en/settings.json                      |   46 +
 web/messages/en/versions.json                      |   89 +
 web/messages/en/workflows.json                     |  223 ++
 web/messages/id/auth.json                          |   36 +
 web/messages/id/canvas.json                        |   52 +
 web/messages/id/common.json                        |   27 +
 web/messages/id/credentials.json                   |   40 +
 web/messages/id/datastores.json                    |  209 ++
 web/messages/id/editor.json                        |  164 ++
 web/messages/id/embed.json                         |   16 +
 web/messages/id/executions.json                    |   94 +
 web/messages/id/home.json                          |   20 +
 web/messages/id/nav.json                           |   10 +
 web/messages/id/properties.json                    |  123 ++
 web/messages/id/schedules.json                     |   33 +
 web/messages/id/settings.json                      |   46 +
 web/messages/id/versions.json                      |   89 +
 web/messages/id/workflows.json                     |  222 ++
 web/package.json                                   |    7 +-
 web/pnpm-lock.yaml                                 |  204 ++
 web/project.inlang/settings.json                   |   25 +
 web/src/lib/api/generated/admin/admin.ts           | 1051 ++++++++++
 web/src/lib/api/generated/auth/auth.ts             |   32 +-
 .../lib/api/generated/credentials/credentials.ts   |   30 +-
 .../api/generated/datastore-rows/datastore-rows.ts |  110 +-
 web/src/lib/api/generated/datastores/datastores.ts |   32 +-
 web/src/lib/api/generated/embed/embed.ts           |    2 +-
 web/src/lib/api/generated/executions/executions.ts |    2 +-
 web/src/lib/api/generated/interop/interop.ts       |  115 +-
 web/src/lib/api/generated/models/binaryRemoval.ts  |   16 +
 .../models/createTenantAPIKeyInputBody.ts          |   17 +
 .../api/generated/models/createTenantInputBody.ts  |   24 +
 .../generated/models/createTenantUserInputBody.ts  |   29 +
 .../api/generated/models/deleteRowsInputBody.ts    |    2 +
 .../lib/api/generated/models/embedSessionBody.ts   |   11 +-
 .../api/generated/models/embedSessionResource.ts   |    1 +
 .../generated/models/executionNodeRunResource.ts   |    1 +
 .../generated/models/executionRequestResource.ts   |    1 +
 .../api/generated/models/incrementRowsInputBody.ts |   22 +
 .../generated/models/incrementRowsOutputBody.ts    |   16 +
 .../models/incrementRowsOutputBodyRowsItem.ts      |    9 +
 web/src/lib/api/generated/models/index.ts          |   24 +
 .../lib/api/generated/models/listApiKeysParams.ts  |   20 +
 .../api/generated/models/listCredentialsParams.ts  |   20 +
 .../api/generated/models/listDatastoresParams.ts   |   20 +
 .../api/generated/models/listSchedulesParams.ts    |   20 +
 .../generated/models/listTenantUsersOutputBody.ts  |   15 +
 .../api/generated/models/listTenantsOutputBody.ts  |   15 +
 .../api/generated/models/listWorkflowsParams.ts    |   20 +
 web/src/lib/api/generated/models/node.ts           |    1 +
 .../lib/api/generated/models/notReadyProblem.ts    |   31 +
 .../lib/api/generated/models/readyDatastores.ts    |   19 +
 .../api/generated/models/readyDatastoresSpread.ts  |   12 +
 .../lib/api/generated/models/readyOutputBody.ts    |    3 +
 .../api/generated/models/runWorkflowInputBody.ts   |    2 +
 .../generated/models/setUserPasswordInputBody.ts   |   18 +
 .../api/generated/models/tenantDeletionResource.ts |   24 +
 .../models/tenantDeletionResourceRemoved.ts        |   12 +
 web/src/lib/api/generated/models/tenantResource.ts |   17 +
 .../api/generated/models/updateRowsInputBody.ts    |    2 +
 web/src/lib/api/generated/models/userResource.ts   |   18 +
 .../generated/models/workflowDiagnosticsParams.ts  |   14 +
 .../models/workflowDiagnosticsResource.ts          |   20 +
 web/src/lib/api/generated/nodes/nodes.ts           |    8 +-
 web/src/lib/api/generated/schedules/schedules.ts   |   32 +-
 web/src/lib/api/generated/system/system.ts         |   18 +-
 .../workflow-lifecycle/workflow-lifecycle.ts       |    2 +-
 web/src/lib/api/generated/workflows/workflows.ts   |  129 +-
 web/src/lib/api/http.ts                            |    4 +-
 .../lib/components/dashboard/dashboard-nav.svelte  |   56 +-
 .../lib/components/dashboard/list-states.svelte    |   11 +-
 .../components/dashboard/locale-switcher.svelte    |   31 +
 .../lib/components/ui/dialog/dialog-content.svelte |    3 +-
 .../lib/components/ui/dialog/dialog-footer.svelte  |    3 +-
 .../lib/components/ui/sheet/sheet-content.svelte   |   14 +-
 .../workflow-editor/activation-notices.svelte      |   15 +-
 .../workflow-editor/canvas-bridge.svelte           |   39 +
 .../components/workflow-editor/canvas-edge.svelte  |   82 +
 .../components/workflow-editor/canvas-node.svelte  |  381 ++--
 .../workflow-editor/editor-controls.svelte         |    6 +-
 .../workflow-editor/execution-canvas-node.svelte   |    9 +-
 .../workflow-editor/execution-canvas.svelte        |   30 +-
 .../components/workflow-editor/node-picker.svelte  |  155 +-
 .../workflow-editor/properties-panel.svelte        |  105 +-
 .../workflow-editor/property-field.svelte          |  452 ++--
 .../workflow-editor/property-field.test.ts         |   81 +
 .../workflow-editor/version-panel.svelte           |  163 +-
 .../workflow-editor/workflow-editor.svelte         |  827 +++++++-
 web/src/lib/dashboard/cursor-page.test.ts          |  368 +++-
 web/src/lib/dashboard/cursor-page.ts               |  141 ++
 web/src/lib/dashboard/execution-list.test.ts       |  148 +-
 web/src/lib/dashboard/execution-list.ts            |   98 +-
 web/src/lib/dashboard/nav-sections.test.ts         |   55 +-
 web/src/lib/dashboard/nav-sections.ts              |   59 +
 web/src/lib/dashboard/workflow-list.test.ts        |   20 +-
 web/src/lib/dashboard/workflow-list.ts             |   24 +-
 web/src/lib/datastore/columns.test.ts              |   41 +
 web/src/lib/datastore/columns.ts                   |   39 +-
 web/src/lib/datastore/transfer.ts                  |   15 +-
 web/src/lib/embed/embed-editor.svelte              |  197 +-
 web/src/lib/embed/session.svelte.ts                |  113 +-
 web/src/lib/embed/session.test.ts                  |   86 +-
 web/src/lib/i18n/catalog.test.ts                   |   75 +
 web/src/lib/i18n/copy.test.ts                      |  399 ++++
 web/src/lib/i18n/locale.svelte.ts                  |  107 +
 web/src/lib/i18n/locale.test.ts                    |   90 +
 web/src/lib/type-guards.ts                         |   13 +
 web/src/lib/workflow-editor/activation.ts          |    3 +-
 web/src/lib/workflow-editor/authoring.test.ts      |  118 ++
 web/src/lib/workflow-editor/canvas-actions.ts      |   14 +
 web/src/lib/workflow-editor/catalog.test.ts        |   99 +
 web/src/lib/workflow-editor/catalog.ts             |   99 +
 web/src/lib/workflow-editor/clipboard.test.ts      |  203 ++
 web/src/lib/workflow-editor/clipboard.ts           |  293 +++
 web/src/lib/workflow-editor/conditions.test.ts     |   35 +-
 web/src/lib/workflow-editor/conditions.ts          |   59 +-
 web/src/lib/workflow-editor/credentials.test.ts    |    4 +-
 web/src/lib/workflow-editor/document.test.ts       |  120 ++
 web/src/lib/workflow-editor/document.ts            |  260 ++-
 web/src/lib/workflow-editor/execution.test.ts      |   48 +-
 web/src/lib/workflow-editor/execution.ts           |   55 +-
 .../lib/workflow-editor/expression-assist.test.ts  |   55 +-
 web/src/lib/workflow-editor/expression-assist.ts   |   50 +
 .../lib/workflow-editor/expression-grammar.test.ts |    3 +-
 web/src/lib/workflow-editor/expression-grammar.ts  |   29 +-
 web/src/lib/workflow-editor/history-diff.ts        |  Bin 10556 -> 10731 bytes
 web/src/lib/workflow-editor/history.test.ts        |   68 +
 web/src/lib/workflow-editor/history.ts             |   93 +
 .../lib/workflow-editor/import-diagnostics.test.ts |   41 +
 web/src/lib/workflow-editor/import-diagnostics.ts  |   93 +
 web/src/lib/workflow-editor/layout.test.ts         |  108 +
 web/src/lib/workflow-editor/layout.ts              |  257 ++-
 web/src/lib/workflow-editor/loader-cache.test.ts   |  116 ++
 web/src/lib/workflow-editor/loader-cache.ts        |   97 +
 web/src/lib/workflow-editor/node-visual.ts         |   12 +
 web/src/lib/workflow-editor/ports.test.ts          |  224 +-
 web/src/lib/workflow-editor/ports.ts               |  123 +-
 web/src/lib/workflow-editor/run-trigger.test.ts    |   75 +
 web/src/lib/workflow-editor/run-trigger.ts         |   39 +
 web/src/lib/workflow-editor/shortcuts.test.ts      |  109 +
 web/src/lib/workflow-editor/shortcuts.ts           |  158 ++
 web/src/lib/workflow-editor/sticky.test.ts         |   37 +
 web/src/lib/workflow-editor/sticky.ts              |   75 +
 web/src/lib/workflow-editor/validation.test.ts     |   41 +-
 web/src/lib/workflow-editor/validation.ts          |   38 +-
 .../lib/workflow-editor/version-history.test.ts    |   65 +-
 web/src/lib/workflow-editor/version-history.ts     |   82 +-
 web/src/lib/workflow-editor/workflow-cache.test.ts |   46 +
 web/src/lib/workflow-editor/workflow-cache.ts      |   33 +
 web/src/routes/(dashboard)/+layout.svelte          |   32 +-
 .../routes/(dashboard)/app/workflows/+page.svelte  |  239 ++-
 .../(dashboard)/app/workflows/[id]/+page.svelte    |  409 +++-
 .../app/workflows/[id]/export-dialog.svelte        |   31 +-
 .../app/workflows/diagnostics-section.svelte       |   50 +-
 .../(dashboard)/app/workflows/import-dialog.svelte |   54 +-
 .../app/workflows/import-report-drawer.svelte      |   61 +
 .../(dashboard)/app/workflows/import-report.svelte |  132 +-
 .../routes/(dashboard)/credentials/+page.svelte    |  129 +-
 web/src/routes/(dashboard)/datastores/+page.svelte |  113 +-
 .../(dashboard)/datastores/[id]/+page.svelte       |  169 +-
 web/src/routes/(dashboard)/executions/+page.svelte |  261 ++-
 .../(dashboard)/executions/[id]/+page.svelte       |  108 +-
 web/src/routes/(dashboard)/schedules/+page.svelte  |  156 +-
 web/src/routes/(dashboard)/settings/+page.svelte   |  173 +-
 web/src/routes/+layout.svelte                      |   11 +
 web/src/routes/+page.svelte                        |   42 +-
 web/src/routes/approve/[token]/+page.svelte        |   51 +-
 web/src/routes/embed/[id]/+page.svelte             |   23 +-
 web/src/routes/login/+page.svelte                  |   21 +-
 web/vite.config.ts                                 |   28 +-
 860 files changed, 126906 insertions(+), 6454 deletions(-)
```
