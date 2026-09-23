---
id: BUG-ngt25j
title: 'Code node and sticky content use a single-line input: Enter does nothing, pasted multi-line code collapses'
status: todo
priority: high
labels:
    - editor
    - code-node
    - ndv
parent: EPIC-8rbys7
created: "2026-09-23T01:48:14Z"
updated: "2026-09-23T01:48:14Z"
---

# Description

The Go Code node cannot be written in the editor at all: pasted multi-line code becomes one line that fails to compile. This also blocks the JS runtime epic's usability.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: ux-editor; finding ids: UXE-2). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** The Code node opens a full multi-line code editor with line numbers, syntax highlighting and completions.

# Steps to Reproduce

1. Add a Code node and open it. 2. Paste `out := []Item{}\nfor _, it := range items {\n\tout = append(out, it)\n}\nreturn out, nil` into "Go code", or press Enter to start a new line. 3. Save and Execute.

# Expected

A multi-line code editor (at least a monospace textarea) for code parameters and sticky content.

# Actual

The field is `<input type=text>` (28 px high). The paste becomes `out := []Item{} for _, it := range items { \tout = append(out, it) } return out, nil`, and Enter inserts nothing. The run fails: `node "Code": code did not compile: ... ./main.go:32:17: syntax error: unexpected keyword for at end of statement`. The Sticky Note "Content" field has the same problem, so notes cannot be multi-line markdown from the editor.

# Acceptance Criteria
- [ ] Code parameters use a multi-line code editor with monospace font, indentation and line numbers (at minimum a textarea)
- [ ] Sticky note content is multi-line
- [ ] Pasting multi-line code preserves the newlines, and a pasted Go body compiles

# Implementation Plan

Declare `typeOptions.rows` (or an `editor: code` hint) on nodes/code.go `code` and annotation.go `content`, and render a CodeMirror editor for code kinds. As a stopgap, render any string property whose key is code/content as a textarea.

# Notes

Related (from the audit): none

# Related Files

SD/27-code-ndv.png, SD/28-after-run.png, SD/codepaste.js output (`INPUT | "out := []Item{} for …"`), SD/42-sticky-click.png. Code: property-field.svelte:1020-1026 picks `<textarea>` only when `needsMultiline(value, typeOptions.rows)`. nodes/code.go:41 (`code`) and nodes/annotation.go:40 (`content`) declare no `rows`/editor typeOption, and their defaults are one line.

# Attachments
