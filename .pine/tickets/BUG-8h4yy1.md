---
id: BUG-8h4yy1
title: 'Embedded editor 401s: first queries fire before embed token attached'
status: doing
priority: critical
labels:
    - embed
    - editor
    - full-review
    - wf-c415e773
parent: EPIC-cfe7ny
created: "2026-09-19T12:06:09Z"
updated: "2026-09-19T13:44:26Z"
---

Source: KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 + Verify 14/14 + Critique 1/1). Evidence: live repros against stub/n8n/private instances in `scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md) plus journal `wf_c415e773-4e1/journal.jsonl`. Excluded from this epic: 10 verifier-refuted/tracked items (documented bounds, already-open FEAT-1axhdn/FEAT-8mymac halves).

Consolidates 1 finding(s) from dims: find:ui-ops-surfaces.

---
### Embedded editor always fails with 401: its first queries go out before the embed token is attached [find:ui-ops-surfaces] (critical/bug) · area: embed (/embed/[id]) · confidence: high

With auth on, a valid embed session is accepted (the host's branding shows), but the editor body reads "This workflow could not be loaded — 401". The three queries that fire when the editor mounts (workflow, node-types, credentials) are sent without the X-KilasFlow-Embed header, and nothing retries them.

Evidence: Private auth-on instance on :18099 (config at work/ui-ops-surfaces/kf2/config.yaml, embed.allowed_origins set to http://127.0.0.1:8099). POST /api/v1/embed-sessions (scopes workflow:read/run/write) returned a token. With curl, GET /api/v1/workflows/{id}, /node-types and /credentials each returned 200 with header X-KilasFlow-Embed: <token> and 401 without it. A host page on the :8099 stub iframes /embed/{id} and answers kilasflow:embed-ready with kilasflow:embed-session {token, workflowId, scopes, branding}. The branding bar "Acme Ops" renders, then the 401 error appears. The server log shows GET /api/v1/workflows/{id}, /api/v1/credentials and /api/v1/node-types all status=401 in the same millisecond (15:33:29.267). Screenshots: work/ui-ops-surfaces/19-embed-editor.png (run 1) and 21-embed-rerun.png (run 2). Likely cause: routes/embed/[id]/+page.svelte calls setEmbedToken() in a parent $e

n8n behavior: n/a (KilasFlow-specific embed); regression against done ticket FEAT-900msn.

Impact: The white-label embedded editor, a headline product feature, does not work in any deployment with auth on.

Suggested fix: Set the token synchronously when onMessage accepts the session (lib/embed/session.svelte.ts), or in $effect.pre before the child renders, or pass the token into the child's queries as a header. Retry once the token is present. Add an auth-on e2e test for /embed.

Files: web/src/routes/embed/[id]/+page.svelte, web/src/lib/embed/embed-editor.svelte, web/src/lib/embed/session.svelte.ts, web/src/lib/api/http.ts

Existing tickets: FEAT-900msn

## Acceptance criteria

- [ ] Embedded editor always fails with 401: its first queries go out before the embed token is attached
- [ ] Adversarial re-verify against live stub/n8n like the Verify phase (no code-only close)