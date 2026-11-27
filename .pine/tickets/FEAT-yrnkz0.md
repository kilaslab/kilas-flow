---
id: FEAT-yrnkz0
title: Theme control for the embedded editor
status: todo
priority: medium
labels:
    - saas
    - embedding
    - theming
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

A light palette exists but nothing can select it; the app is dark-first (`web/src/app.css:84-95`). Embed branding is only name, logo, accent, `hideRun` and `hideSave` (`docs/src/content/docs/guides/embedding.md:279-291`), so a light-first host cannot match its own look. FEAT-a3dwj2 covers only the dashboard toggle.

# Acceptance Criteria
- [x] `branding.theme: "light" | "dark" | "system"` is validated at mint and applied as `data-theme` on the embed shell, including the canvas library's colour mode.
- [x] Optional `branding.fontFamily` from an allowlist, and `branding.radius` within bounds.
- [x] Contrast checks pass in both themes.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale refs, gap real. Branding table is now guides/embedding.md:288-300 (not 279-291); light palette at app.css:140. `Branding` (internal/embed/embed.go:81-91) has no theme/font/radius; nothing sets `data-theme`; `<SvelteFlow>` sets no `colorMode` (workflow-editor.svelte:1194, execution-canvas.svelte:94).
- `sanitizeBranding` in session.svelte.ts (~260-275) must learn the new fields. Tokens: `--radius` app.css:124, `--font-sans` :27. Shares the light-palette work with FEAT-a3dwj2.

# Related Files

# Attachments

## Progress

2026-11-27, on `feat/epic-sweep-2026-11-27`:

- `embed.Branding` gains `theme` (light, dark, system), `fontFamily` (default, system, serif) and `radius` (0 to 16 px, `*int` so 0 is a real value). These are validated at mint (`Branding.Validate`) and merged field by field in `WithDefaults`. The reflection guard in embed_branding_test.go now covers optional numbers.
- The frame re-sanitises them (`sanitizeBranding`). `brand-root.ts` adds `resolveTheme`, `appearanceProperties` and `themeDocument`. The embed route sets `data-theme` and the properties on both the shell and `<html>`, so portalled dialogs match. `system` keeps following `prefers-color-scheme` through a matchMedia listener.
- Canvas: app.css binds every `--xy-*` variable to a `--kf-*` token on `.svelte-flow`, so `data-theme` recolours the canvas. Svelte Flow's own `colorMode` stays unset on purpose: its class-based defaults are overridden by those bindings.
- Radius: Tailwind's `--radius-*` resolve at `:root`, so the radius only reaches `rounded-lg` and the other utilities because it is also set on `<html>`. The shell copy covers the `--kf-radius-*` users.
- Contrast: tokens.contrast.test.ts already checks 44 pairs per theme in dark and light, plus host accents. The new e2e also checks text on background at 4.5:1 or better in the light shell.
- Tests: Go (embed_theme_test.go), vitest (brand-root, session sanitiser), and e2e `embed-theme.spec.ts` (2 tests: light + serif + radius 0 across shell, canvas, step tile and history sheet; system following emulated media; the omitted theme stays dark). Both pass, and so does embed-branding.spec.ts.
- Not done: no deployment-level `branding.theme` default in config (only name and logo exist there). The fonts are the fixed stacks.
