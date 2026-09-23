---
id: FEAT-6qzza1
title: Embed quickstart and reference-host walkthrough, tested in CI against a cross-origin host
status: todo
priority: high
labels:
    - docs
    - embedding
    - e2e
deps:
    - BUG-b3p8va
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

The integrator's first 15 minutes: keys, provisioning, minting a session server-side, mounting the iframe, and receiving editor and execution events. Today this path is blocked by BUG-b3p8va, and two of its snippets don't parse.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-12). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n/a

# Steps to Reproduce

1. `guides/embedding.md:58-75`: `const imported = await tenant.client.importWorkflow({ … workflow: { … connections: {} }` is followed directly by `const workflowId = …`. The closing `});` is missing.
2. `sdk/README.md:248-267`: `const editor = mountWorkflowEditor({ … onEvent(event) { … } }` is followed by `editor.unmount();`. The closing `});` is missing.

# Expected

Samples that compile, checked in CI (for example by extracting fenced `js`/`ts` blocks and running `tsc --noEmit` against the SDK types).

# Actual

A reader copying either block gets a SyntaxError.

# Acceptance Criteria
- [ ] Following the page verbatim on a clean machine saves and runs a workflow in the embedded editor, and the host logs `execution-finished`
- [ ] A CI Playwright job runs the quickstart's host page on a different origin than the server, and fails if save or run fails
- [ ] Every snippet on the page is type-checked or executed in CI
- [ ] Both install paths are labelled: pre-release (build from source) and post-release (published image and npm)

# Implementation Plan

Fix both blocks and add a snippet type-check.

# Notes

Related tickets: FEAT-frvez8

Related (from the audit): FEAT-frvez8 (done)

# Related Files

the line references above.

# Attachments
