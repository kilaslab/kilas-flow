---
id: FEAT-kcdrcy
title: 'NDV credentials: create inline, one Authentication selector for HTTP Request, truncate long names'
status: todo
priority: medium
labels:
    - credentials
    - ndv
    - ux
parent: EPIC-8rbys7
created: "2026-09-23T01:34:44Z"
updated: "2026-09-23T01:34:44Z"
---

# Description

Creating a credential from a node leaves the editor. HTTP Request shows five credential pickers at once, and a long credential name pushes the panel off-screen.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-ops; finding ids: OPS-11, OPS-12, OPS-27). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

# Findings

## OPS-11: A credential can't be created from inside a node: "add one under Credentials" navigates away, and duplicate credential names are indistinguishable

*gap · medium · credentials / ndv*

**n8n:** The NDV credential dropdown has "+ Create new credential", which opens the credential modal on top of the canvas with the type preselected and selects the new credential when you save.

**Steps to reproduce:**

1. Open a workflow with an HTTP Request node and select it. 2. In the Credential block, click "add one under Credentials" under "No HTTP Query Auth credential yet".

**Actual:**

The app navigates to /credentials. The type isn't preselected, nothing returns you to the workflow, and the new credential isn't attached to the node. Separately, two credentials both named "[ux-ops] Header auth" (duplicates are accepted) appear as two identical options in the node's picker.

**Expected:**

An inline create modal from the NDV with the type preselected that auto-selects the result, and either unique names per type or a disambiguating suffix (type or host scope) in the picker.

**Suggested fix:**

Pull the credential form out into a reusable component, open it from the properties panel with `typeID` fixed, and call onCredentialChange with the created id.

**Evidence:**

agents/ux-ops/16-ndv-credentials-section.png, agents/ux-ops/15b-click-node.png; web/src/lib/components/workflow-editor/properties-panel.svelte:232 (`onclick={() => void goto('/credentials')}`).

**Related:**

none


## OPS-12: The HTTP Request node shows five credential pickers at once, so it's unclear which credential the node needs or uses

*ux · medium · credentials / ndv*

**n8n:** HTTP Request asks Authentication = None / Predefined Credential Type / Generic Credential Type, then Generic Auth Type (Basic, Header, …), then exactly one credential select.

**Steps to reproduce:**

1. Select any HTTP Request node on the canvas. 2. Look at the "Credential" block.

**Actual:**

Five selects appear together (HTTP Basic Auth, HTTP Header Auth, HTTP Bearer Auth, HTTP Query Auth, HTTP Custom Auth), each with its own "No … credential yet — add one under Credentials" line. Nothing says they are alternatives or what happens when several are set. The node definition has no `authentication` parameter and no `visibleWhen` on its credential requirements, so the panel's own visibility logic (web/src/lib/workflow-editor/credentials.ts:18-45) can't narrow them.

**Expected:**

A single "Authentication" choice that reveals exactly one credential select (n8n parity; imported workflows already carry authentication/genericAuthType).

**Suggested fix:**

Add `authentication` and `genericAuthType` options to kilasflow.httpRequest and httpTool, and put visibleWhen on each credential requirement.

**Evidence:**

agents/ux-ops/16-ndv-credentials-section.png, agents/ux-ops/18-ndv-dangling-credential.png; node-types.json kilasflow.httpRequest `credentials` (5 entries, no visibleWhen).

**Related:**

none


## OPS-27: A long credential name widens every select in the node's credential block past the panel edge

*bug · low · ndv / credentials*

**n8n:** Credential names truncate inside a fixed-width dropdown.

**Steps to reproduce:**

1. Create a Bearer credential whose name is 300 characters (the API accepts it; there is no length limit). 2. Select any HTTP Request node.

**Actual:**

The selects take the widest option's width. #credential-httpHeaderAuth measured 1140→3158 px inside a 319 px panel, so every credential select and the "add one under Credentials" text are clipped at the right edge. A realistic 50–60 character name already exceeds the panel.

**Expected:**

Selects stay within the panel, and names truncate with an ellipsis.

**Suggested fix:**

Give the credential block `min-w-0`, set `w-full max-w-full` on the select, and cap credential names at about 120 characters on the server.

**Evidence:**

agents/ux-ops/18-ndv-dangling-credential.png; eval measurement in this report; properties-panel.svelte:214 (`min-w-0 flex-1` inside an unconstrained grid).

**Related:**

none


# Acceptance Criteria
- [ ] "+ Create new credential" in the NDV opens the credential form over the canvas with the type preselected, and selects the new credential on save
- [ ] HTTP Request and HTTP Tool have `authentication` + `genericAuthType` options (n8n parity), and exactly one credential select is visible
- [ ] Duplicate credential names are disambiguated in pickers (type or host suffix)
- [ ] Credential selects stay inside the panel with ellipsis truncation, and names are capped at about 120 characters server-side

# Implementation Plan

See each finding's suggested fix above.

# Notes

# Related Files

# Attachments
