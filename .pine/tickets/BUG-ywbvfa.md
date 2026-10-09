---
id: BUG-ywbvfa
title: 'Focus, hover and pressed states are inconsistent: three focus styles, and the primary button''s ring is invisible'
status: doing
priority: high
labels:
    - frontend
    - accessibility
    - focus
    - visual-revamp
deps:
    - FEAT-p75n7j
parent: EPIC-3en6xr
created: "2026-09-23T03:05:16Z"
updated: "2026-10-09T11:03:29Z"
---

# Description

Keyboard focus is drawn three ways. Nav and links get a 2 px solid outline at 2–4 px offset. shadcn buttons and inputs get a 3 px box-shadow ring at 50% of the ring colour, and on the teal primary button that ring is teal on teal, which is effectively invisible (74-focus-button-dark.png). Hand-rolled inputs and selects (search and filters) fall back to the browser's `outline: auto 1px`. The pressed state exists only on buttons (translate 1 px), and canvas node selection takes each node's own colour. v2 defines one focus ring (2 px solid --kf-focus, 2 px offset, inset −1 px in dense lists and grids), one hover (--kf-hover), one pressed (--kf-pressed plus 1 px translate on buttons) and one selection (--kf-selection).

# Acceptance Criteria
- [x] Tabbing through /app/workflows, the editor toolbar, the inspector and a dialog shows the same focus outline on every stop: a Playwright test records the outline style, width and colour on 30 consecutive Tab stops and finds exactly one combination (baseline 3).
  - Evidence: `e2e/tests/focus-tab-order.spec.ts`, both themes pass: one combination, `solid|2px|oklch(0.82 0.13 95)|no-ring-shadow` dark and `solid|2px|oklch(0.56 0.11 75)|no-ring-shadow` light (12 list + 5 import dialog + 6 toolbar + 7 inspector stops). On main the walk could not even leave the editor toolbar: Tab was the add-step shortcut on every non-text control and opened the node picker (fixed, see Progress).
- [x] The focus ring passes ≥3:1 against the surface it is drawn on in both themes: turmeric oklch(.82 .13 95) on dark, ochre oklch(.56 .11 75) on light. Around filled controls, including the turmeric primary button, the 2 px offset gap shows the surface between ring and fill. This is verified by a zoomed screenshot of the focused primary button in both themes.
  - Evidence: per-stop contrast in focus-tab-order.spec.ts (dark 8.33–10.94:1, light 4.25–4.74:1); `e2e/visual/focus-ring.spec.ts` (4x device-scale shot per theme, outline solid 2px, offset ≥2px, ≥3:1 against the first opaque surface behind it); token pairs "focus on canvas/bg/panel/raised/overlay" in tokens.contrast.test.ts.
- [x] Every interactive element has distinct hover, pressed, focus-visible and disabled states; a state-matrix story for Button, IconButton, Input, Select, Switch, Tab, MenuItem, ListRow and canvas Node is captured in the VR-03 suite.
  - Evidence: the `/design/states` harness (`web/src/lib/design-system/state-matrix.svelte`, dev and `KILASFLOW_DESIGN_HARNESS=1` builds only) and `e2e/visual/state-matrix.spec.ts`: 9 sections × 2 themes, states forced through the DevTools protocol. `pnpm test:visual` 21 passed; flake check `--repeat-each=3` 63 passed, 0 diffs. darwin baselines local and gitignored; linux baselines come from CI (see Progress).
- [x] No focus indicator is removed without a replacement: 0 `outline-none` without a matching focus-visible style (lint).
  - Evidence: `web/src/lib/design-system/focus-lint.test.ts` (in `pnpm test`, so `make lint` and CI; alone via `pnpm lint:focus`): 0 violations, with fixtures proving each rule fails. It reported 11 on main.

# Implementation Plan

Set the global :focus-visible rule from the token block and remove the ring-3/ring-ring/50 variants from the shadcn primitives. Hand-rolled inputs adopt the Input primitive (VR-11).

# Notes

Source: the 2026-09-23 frontend visual audit. The owner chose direction A, "Kunyit", after rejecting "Nila" for resembling qonflo.ai. The full audit and draft tokens are attached to the parent epic.

# Related Files

# Attachments

## Progress (2026-10-09)

Track I, worktree branch `worktree-agent-aa56c84abae43f91e`, on top of FEAT-p75n7j's tokens.

What changed:
- One ring: `@layer base { :focus-visible { outline: var(--kf-focus-width) solid var(--kf-focus); outline-offset: var(--kf-focus-offset) } }`. Author styles beat the browser's `outline: auto`, so hand-rolled search fields, selects and checkboxes get it without edits (their conversion to primitives stays FEAT-f3hx3a). Four utilities cover the exceptions: `focus-inset` (dense rows), `focus-region` (tabindex=-1 targets: canvas region, inspector asides, dialogs, menu/popover/select/sheet content, tabs content, scroll areas, dashboard `<main>`), `focus-within-ring` + `focus-delegate` (node-picker search, chat composer, code editor).
- Primitives (`web/src/lib/components/ui`): every `ring-3`/`ring-[3px]`/`ring-ring/50`/`outline-1` focus variant, the aria-invalid ring and `outline-none`/`outline-hidden` are gone (the invalid border stays). Button: `transition-[color,background-color,border-color,opacity,translate]` (the ring never animates), default `hover:bg-(--kf-accent-hover) active:bg-(--kf-accent-press)`, outline/secondary/ghost `hover:bg-accent active:bg-(--kf-pressed)`, destructive an active tint, link `text-(--kf-accent-text)`, styled `aria-disabled`. Input, textarea, select trigger, checkbox, switch: hover border `--kf-border-hover`; switch thumb scales on press. Tabs, menu and select items: `active:bg-(--kf-pressed)`; menu/select items `focus-inset`.
- New primitive `ui/list-row` (40px, `--kf-hover`/`--kf-pressed`, `focus-inset`, `aria-selected`/`data-selected` → `--kf-selection-row` with a 2px `--kf-selection` bar, `aria-disabled` → `--kf-fg-disabled`). Adopting it on the lists is FEAT-f3hx3a, after Track E merges the workflows page.
- Canvas nodes: the tile draws the focus ring (`.svelte-flow__node:focus-visible .kf-node-tile`), `--kf-hover` on hover and `--kf-pressed` on press (unlayered rules after the Svelte Flow import), and selection is `--kf-selection` (FEAT-p75n7j).
- `focus-lint.ts`: rule A (a class set with `outline-none`/`outline-hidden` needs a replacement; `focus-delegate` needs a `focus-within-ring` ancestor in the same file; no inline `outline: none`), rule B (CSS), rule C (CSS-in-JS, with a reviewed list: CodeMirror's `.cm-focused` outline is replaced by the code editor container's `focus-within-ring`, and the lint checks that container still carries it). Every message says which utility to use.
- Harness and suite: `/design/states` route, `e2e/playwright.visual.config.ts` (`dark-1440`, `light-1440`), `e2e/visual/*`, `make test-visual`, the CI step and artefact upload, CONTRIBUTING.

Deviations from the plan:
- `web/src/lib/workflow-editor/shortcuts.ts`: Tab on a focused control (button, link, summary, or a control role) is now keyboard navigation; on the canvas, a node (role="group") and the page it still opens the node picker. Without this the toolbar and inspector could not be tabbed through at all, so criterion 1 was unreachable. Unit-tested in shortcuts.test.ts.
- `focus-inset` insets by the ring width (−2px) rather than −1px: inside an overflow-hidden list card the −1px ring lost its outer pixel and showed as 1px.
- Canvas node hover and pressed are the shared `--kf-hover`/`--kf-pressed` surfaces on the tile, not a `--kf-border-strong` border: the idle tile border already sits at L .42 (= border-strong in dark), so a border hover was invisible.
- New derived token `--kf-border-hover` (a quarter from `--kf-border-strong` toward `--kf-fg`): `--input` already maps to `--kf-border-strong`, so a hover to border-strong would not change anything.
- The outline Button variant is `bg-transparent` (its `dark:bg-input/30 dark:hover:bg-input/50` overrides went, so it shares the one hover).
- The lint reported 11 violations on main, not 24: the plan's count included the 13 primitives whose `outline-none` sits next to a `focus-visible:ring-*` indicator, which rule A (as specified) accepts. They were rewritten anyway.
- The Tab test walks 6 toolbar stops and 7 inspector stops (30 in all): the toolbar has six enabled controls after "Add step" on a saved workflow, and a seventh Tab enters the canvas, where Svelte Flow edges show focus by stroke, not outline (canvas chrome, FEAT-xj5tv6; noted there).
- CI: Playwright 1.63 fails a run that writes a missing baseline even with `updateSnapshots: 'missing'`, so the CI visual step is `continue-on-error` while `e2e/visual/__screenshots__/linux/` does not exist; the config writes the baselines in that state and the step uploads them.

Left:
- Commit the linux baselines from the first CI run's `visual-regression` artefact into `e2e/visual/__screenshots__/linux/`; from then on the step blocks.
- Hand-rolled inputs and lists adopting Input/ListRow: FEAT-f3hx3a. Edge focus style and the dark-in-light idle node border: FEAT-xj5tv6. 1280 projects and product surfaces in the visual suite: FEAT-egm8bf.

