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
- No user-visible English string literal remains in web/src/lib/components and web/src/routes.
- `lang` on the document follows the embed's locale, not a constant.
- A second locale renders end to end in the editor and in the embedded editor.
