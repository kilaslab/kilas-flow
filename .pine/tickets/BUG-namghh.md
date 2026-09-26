---
id: BUG-namghh
title: 'Low-contrast text: root links 1.48:1, datastore type labels and ''Failed'' badge under WCAG AA'
status: done
priority: low
labels:
    - accessibility
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-26T16:48:06Z"
---

# Description

Most pages pass the contrast check. A few tokens don't.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-19). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** n/a

# Steps to Reproduce

1. Run agents/ux-ops/contrast.js (WCAG ratio for every visible text node) on /, /app/workflows, /credentials, /datastores/<id>, /settings, /executions and /schedules in dark theme, and again with data-theme=light.

# Expected

≥4.5:1 for body text.

# Actual

The "/" links (API reference, OpenAPI JSON/YAML) score 1.48:1 in dark and 1.22:1 in light, because they use `text-(--color-accent)`, and --accent is the hover-surface token oklch(0.31 0.055 170). Datastore header "· string/number/boolean/datetime" scores 4.43:1 (dark) and 3.27:1 (light) at 12 px. The executions "Failed" badge scores 4.36:1 at 12 px. Every other page passes, and focus rings are visible on every tab stop.

# Acceptance Criteria
- [x] All body text reaches at least 4.5:1 in both dark and light themes (the audit's contrast.js scan is clean)

# Implementation Plan

Use text-primary for links, drop the /70 opacity on the datastore type label, and darken the destructive badge text a step.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/contrast.json, agents/ux-ops/01-root.png; web/src/routes/+page.svelte:154-156; web/src/app.css:42,108.

# Attachments

## Progress

Fixed with the existing tokens; `app.css` is untouched.

- The "/" links use `text-primary` instead of `text-(--color-accent)`.
  `--accent` is the hover-surface token, so it was never a text colour.
- The datastore header's "· type" label drops its `/70` opacity and reads as
  `text-muted-foreground`, the colour of the header text beside it.
- The datastore "Null" cell placeholder had the same fault one line down
  (`text-muted-foreground/60`, 3.57:1 dark and 2.66:1 light) and got the same
  fix. The audit's data had no null cell, so it never scanned one.
- The executions "Failed" badge text is now the destructive token mixed 80/20
  toward the foreground (`statusTone` in `web/src/lib/workflow-editor/execution.ts`).
  Mixing toward the foreground lightens the text in dark and darkens it in
  light, so no `dark:` pair and no new token. `--destructive` itself is left
  alone: the canvas node badge paints white text on a solid `bg-destructive`,
  and lightening the token would cost that badge its contrast.

Measured in Chromium against the real dev server (Vite on a spare port, the
API mocked through Playwright routes). Each element's computed colour and its
effective background, alpha composited layer by layer, went through the WCAG
formula. The "before" rows put the old colour back on the same element with an
inline style, so only the colour differs. A token-math script (oklch to sRGB)
gave the same numbers to the second decimal.

| Text (size) | Theme | Before | After |
| --- | --- | --- | --- |
| "/" links (14px) | dark | 1.48 | 8.76 |
| "/" links (14px) | light | 1.22 | 6.88 |
| Datastore "· type" label (12px) | dark | 4.43 | 7.64 |
| Datastore "· type" label (12px) | light | 3.27 | 6.44 |
| Datastore "Null" cell (14px) | dark | 3.57 | 7.64 |
| Datastore "Null" cell (14px) | light | 2.66 | 6.44 |
| Executions "Failed" badge (12px) | dark | 4.36 | 5.56 |
| Executions "Failed" badge (12px) | light | 4.54 | 5.88 |

The light "Failed" badge already passed on the list (4.54 on the card), but
the execution detail header sets the same badge straight on the page
background, where token math gives 4.40 (computed, that page was not rendered);
the mix takes it clear of the line on both.

After the fix, a scan of every visible text node on `/`, `/executions` and
`/datastores/<id>` in both themes finds nothing under 4.5:1 (the list has one
row per status, so every badge tone was scanned). I did not re-scan
`/app/workflows`, `/credentials`, `/settings` or `/schedules`; the audit found
them clean and no file they render changed.

Still borderline, not changed: the "Cancelled" badge (`text-warning` on its own
tint) is 4.53:1 on the light list, and token math gives 4.37:1 for it on the
light execution detail header, which is not one of the audited pages.

Checks: `pnpm test` (52 files, 630 tests) and `pnpm check` (svelte-check, 0
errors, 0 warnings) pass. No test pinned the old classes.

## Work Evidence

Closed by `pine close --evidence` on 2026-09-26. Rewritten on 2026-09-26 to list only this ticket's own commit (`git log --grep "BUG-namghh"`) and the files exactly that commit changed: `--evidence` diffs from the ticket's creation commit, which is the bulk audit commit, so it listed hundreds of files that are not this fix. The status change is committed separately, as `chore(pine): close BUG-namghh with its landing evidence`, and touches only this file.

- Commits (1):
  - `147595f` — BUG-namghh: the root links, datastore type labels and Failed badge read at 4.5:1 or better in both themes
- Files changed by that commit (`git show --stat 147595f`):

```
 .pine/tickets/BUG-namghh.md                        | 56 +++++++++++++++++++++-
 web/src/lib/workflow-editor/execution.ts           | 12 ++++-
 .../(dashboard)/datastores/[id]/+page.svelte       |  4 +-
 web/src/routes/+page.svelte                        |  6 +--
 4 files changed, 70 insertions(+), 8 deletions(-)
```
