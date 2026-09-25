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
- [ ] `branding.theme: "light" | "dark" | "system"` is validated at mint and applied as `data-theme` on the embed shell, including the canvas library's colour mode.
- [ ] Optional `branding.fontFamily` from an allowlist, and `branding.radius` within bounds.
- [ ] Contrast checks pass in both themes.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

## Audit 2026-09-25 (code vs ticket, main @ 17b6d38)

Stale refs, gap real. Branding table is now guides/embedding.md:288-300 (not 279-291); light palette at app.css:140. `Branding` (internal/embed/embed.go:81-91) has no theme/font/radius; nothing sets `data-theme`; `<SvelteFlow>` sets no `colorMode` (workflow-editor.svelte:1194, execution-canvas.svelte:94).
- `sanitizeBranding` in session.svelte.ts (~260-275) must learn the new fields. Tokens: `--radius` app.css:124, `--font-sans` :27. Shares the light-palette work with FEAT-a3dwj2.

# Related Files

# Attachments
