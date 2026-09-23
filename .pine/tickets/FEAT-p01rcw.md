---
id: FEAT-p01rcw
title: 'First-run experience: stale ''Scaffolding only'' root page, /app 404, no samples/templates/help'
status: todo
priority: high
labels:
    - onboarding
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

A new user opening the instance URL lands on a page that says the canvas "arrives in Milestone 1", with no link to the editor. `/app` is a bare 404. Nothing in the app explains the product or offers a first workflow.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-4, OPS-5). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-4: First run lands on a stale "Scaffolding only" status page that does not link to the editor, and /app is a bare 404

*ux · high · onboarding*

**n8n:** Opening the instance URL goes straight to the Overview (or the owner-setup screen), with "Start from scratch" and "Try an AI agent" cards.

**Steps to reproduce:**

1. Boot the server. The log prints `http server listening addr=127.0.0.1:18080 docs=/docs openapi=/api/openapi.json` and no editor URL. 2. Open http://127.0.0.1:18080/, as README.md:95 tells you to ("run make build-all && ./bin/kilasflow and open http://localhost:8080"). 3. Try http://127.0.0.1:18080/app.

**Actual:**

"/" shows "KilasFlow · Embeddable workflow automation engine", a "Backend status" card of two GET endpoints, and the footer "Scaffolding only — the workflow canvas arrives in Milestone 1." It has no link to /app/workflows. Its only links (API reference, OpenAPI JSON/YAML) are nearly invisible at 1.48:1 contrast (OPS-19). /app renders an unstyled "404 Not Found" with no navigation, and the console logs `Not found: /app`.

**Expected:**

"/" redirects to /app/workflows (or /login when auth is on), and /app does the same. The boot log prints the editor URL. The scaffold copy goes away.

**Suggested fix:**

Replace routes/+page.svelte with a redirect (keep the status card under Settings → About). Add routes/(dashboard)/app/+page.ts that redirects to /app/workflows, and log `editor=/app/workflows` at boot.

**Evidence:**

agents/ux-ops/01-root.png, agents/ux-ops/52-app-404.png; web/src/routes/+page.svelte:154-156; README.md:95; kf-server.log line 8.

**Related:**

none


## OPS-5: No guided first workflow: no templates, sample workflow, help link or "what's new" anywhere in the app

*gap · medium · onboarding*

**n8n:** The empty Overview offers "Start from scratch" and "Test a simple AI Agent example", a Templates gallery (api.n8n.io), and a help menu (Documentation, Forum, Course, "What's new", About with version).

**Steps to reproduce:**

1. Mock an empty workflow list (route **/api/v1/workflows?** → []) or use a fresh DB. 2. Look for templates, examples, docs or help links.

**Actual:**

The empty state is "Build your first flow — A workflow starts as a private draft…" with New workflow and Import n8n buttons. No templates or sample flow exist, and the product is never explained. The dashboard has no link to /docs (the only one is on the scaffold page) and no link to the user docs in docs/src/content/docs (start/first-workflow.md exists but isn't reachable). Settings → About shows only a git SHA ("018af94"), with no changelog or what's new. "New workflow" asks for a name before anything else. Keyboard-shortcut help exists only inside the editor.

**Expected:**

A first-run path: 2-3 bundled sample workflows (for example Webhook → Set → Respond, or an Ollama agent), a template gallery or Import-from-URL, and a Help menu with docs, API reference, shortcuts, version and changelog.

**Suggested fix:**

Ship a few embedded example workflows with "Use this" on the empty state. Add a sidebar Help menu (Docs, API reference, Shortcuts, What's new from CHANGELOG.md, version).

**Evidence:**

agents/ux-ops/04-workflows-empty-state.png, agents/ux-ops/05-new-workflow-dialog.png, agents/ux-ops/40-settings.png; `grep -rn 'href="/docs"' web/src` matches only routes/+page.svelte.

**Related:**

none


# Acceptance Criteria
- [ ] `/` and `/app` redirect to `/app/workflows` (or `/login` when auth is on); the status card moves under Settings → About
- [ ] The boot log prints the editor URL
- [ ] The empty state offers 2–3 bundled sample workflows (for example Webhook → Set → Respond, and an Ollama agent) with "Use this", plus Import from URL/JSON
- [ ] A Help menu with Docs, API reference, Shortcuts, What's new (from CHANGELOG.md) and the version

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
