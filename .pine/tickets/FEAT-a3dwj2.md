---
id: FEAT-a3dwj2
title: 'Dashboard IA: Overview with stats, Help menu, theme toggle (light palette unreachable), packs/embed settings'
status: todo
priority: medium
labels:
    - dashboard
    - ux
    - ia
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Compared with n8n's sidebar, KilasFlow has no Overview, templates, variables, help or theme choice, although a light palette ships in app.css.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-22). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The sidebar has Overview (workflows, credentials and executions tabs plus production-execution stats), Templates, Projects, Variables, Insights and Help (docs, forum, what's new, about), with Settings covering Users, API, Community nodes, Log streaming and so on.

# Steps to Reproduce

1. Look at the sidebar on any dashboard page. 2. Search the web source for a theme switch.

# Expected

At minimum a Help menu, an Overview with counts and recent failures, and a Variables page (see OPS-23), with a theme option if light mode is meant to ship.

# Actual

The nav is Workflows · Executions · Schedules · Credentials · Datastores · Settings, plus a language select. It has no Overview or stats, templates, projects or folders, variables, help or docs link, user menu, packs (node pack) management, or embed and tenant settings (those are API-only). Settings holds Account (empty when auth is off), API keys and "About" (status and SHA). A light palette exists in app.css:140 ([data-theme='light']), but nothing sets it: no toggle and no prefers-color-scheme hook. What does exist works: aria-current on the active link, title tooltips when collapsed, collapse state persisted in localStorage (`kilasflow.sidebar.collapsed`), and a hamburger at 768 px.

# Acceptance Criteria
- [ ] An Overview page with counts and recent failures
- [ ] A theme option (system / dark / light) that sets `data-theme`
- [ ] A settings surface for node packs and embed/tenant configuration (currently API-only), or a documented decision not to have one

# Implementation Plan

Plan the IA additions as tickets, and add a theme item to the language/footer area that sets data-theme on <html>.

# Notes

Related (from the audit): none

# Related Files

agents/ux-ops/03-workflows-empty.png, agents/ux-ops/47-sidebar-collapsed.png, agents/ux-ops/45-768-workflows.png; `grep -rn "data-theme" web/src --include=*.svelte` returns nothing.

# Attachments
