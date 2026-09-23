---
id: FEAT-4bjfny
title: 'Node picker discoverability: n8n aliases/display names, synonyms, HTTP fallback row, one Triggers group'
status: todo
priority: medium
labels:
    - editor
    - node-picker
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
---

# Description

"email", "javascript", "python", "edit fields", "delay", "csv" and "dedupe" find nothing. FEAT-qcm5ec added the alias plumbing, but the catalogue has 0 aliases.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-12, UXE-20). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## UXE-12: Picker search has no synonyms or fallback: "email", "javascript", "python", "edit fields", "delay", "csv" and "dedupe" find nothing

*gap · medium · node picker / node-catalog*

**n8n:** The node creator searches codex aliases (Code ← "javascript", "python", "script"; Wait ← "delay", "sleep"; Edit Fields (Set); Remove Duplicates ← "dedupe"). With no match, it suggests the HTTP Request node and "request a node".

**Steps to reproduce:**

1. Open the picker from a "+" handle. 2. Search each term (search.js / search2.js).

**Actual:**

No results for `email`, `javascript`, `python`, `edit fields`, `edit`, `delay`, `csv`, `dedupe`, `spreadsheet`, `function`, `sheet` or `slack`. Substring matches on descriptions are noisy: `date` → Date & Time, MySQL, PostgreSQL, Data table. `if` → IF, Date & Time. `api` puts Embeddings above HTTP Request. The empty state says only "No registered node matches …" and offers no HTTP Request fallback. The live catalogue has **0 aliases**: only 9 AI nodes carry a `codex`, and none has `aliases`.

**Expected:**

n8n-compatible aliases, including n8n's display names ("Edit Fields"), word-boundary matching, and a "Use HTTP Request" fallback row.

**Suggested fix:**

Populate `codex.aliases` in the node definitions (copy n8n's codex aliases), match on word starts rather than any substring of a description, and add the HTTP Request fallback to the empty state.

**Evidence:**

SD/07-picker-no-match.png. `curl /api/v1/node-types` → "aliases total: 0". catalog.ts:80-92 already ranks aliases (rank 2), but the server sends none.

**Related:**

FEAT-qcm5ec (done; added the codex/alias plumbing, but no data)


## UXE-20: Picker taxonomy and copy: "Trigger" and "Triggers" headings, Error Trigger highlighted first, and developer-facing descriptions

*ux · low · node picker / onboarding*

**n8n:** A new workflow's trigger list opens with "Trigger manually", then app events, schedule, webhook, form and chat. Categories are user-facing (AI, Action in an app, Data transformation, Flow, Core, Human in the loop).

**Steps to reproduce:**

1. Create a workflow and click "Add first workflow step".

**Actual:**

Two headings, "TRIGGER" (holding only Error Trigger, which is highlighted by default) and "TRIGGERS". Gmail is filed under "Communication" but Telegram and WAHA under "Messaging". "Datastore" and "Files" are one-node categories. Descriptions leak internals: "Polls users.messages with a leased cursor so two replicas cannot emit the same email twice", and the footer says "one row per node type, always the latest version".

**Expected:**

One Triggers group led by Manual Trigger, consistent categories, and user-facing copy.

**Suggested fix:**

Recategorise errorTrigger as "Triggers", order Manual first, merge Communication/Messaging, and rewrite the descriptions and footer.

**Evidence:**

SD/04-picker-empty-triggers.png, node-types.json categories (`Trigger` for kilasflow.errorTrigger vs `Triggers` for the rest).

**Related:**

none


# Acceptance Criteria
- [ ] Every node carries aliases, including n8n's display names ("Edit Fields" → Set, "Delay" → Wait, "Dedupe" → Remove Duplicates)
- [ ] Word-boundary matching, plus a "Use HTTP Request" fallback row when nothing matches
- [ ] A single Triggers group led by Manual Trigger, and user-facing descriptions instead of developer copy

# Implementation Plan

See each finding's suggested fix above.

# Notes

Related tickets: FEAT-qcm5ec

# Related Files

# Attachments
