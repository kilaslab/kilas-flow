---
id: FEAT-pxcbqj
title: 'JS Code runtime P4: kilasflow.jsCode node, executor, importer mapping, catalogue availability'
status: doing
priority: high
labels:
    - code-node
    - javascript
deps:
    - FEAT-yxhgeh
    - FEAT-zjrw76
parent: EPIC-tjnr1z
phase: p4
created: "2026-09-23T01:33:22Z"
updated: "2026-09-23T05:48:30Z"
---

# Description

Make imported JavaScript Code nodes runnable, while Python stays refused.

Child of EPIC-tjnr1z. The full design, including the code shapes, file layout and rationale, is in the epic's *Plan → Phase 4* section. Read it before starting.

# Acceptance Criteria
- [ ] `n8n-nodes-base.code` with JavaScript (or no language) imports as `kilasflow.jsCode` with no blocking issue, and exports back byte-identical
- [ ] Existing `foreignCode` nodes with `language: javaScript` run without re-import
- [ ] Python is still refused with the same sentence. foreignCode is not stamped `unavailable`, because its JavaScript form now runs (EPIC amendment 13)
- [ ] The analyser refuses `\p{}`, the `v`/`d` flags, async generators, `import`/`export`, unlisted `require` and `this.getCredentials`, at import and at validate, through the `jsrun.Refusal` template
- [ ] `kilasflow.jsCode` is stamped unavailable only when `code.javascript_enabled=false`, and it can be added from the palette

# Implementation Plan

See EPIC-tjnr1z, *Plan → Phase 4*.

# Notes

- **2026-09-23, b527da2.** The node, executor, importer route, WholeBatch and
  server wiring. FEAT-yxhgeh's review fixes rode along: `$runIndex` counts
  executed runs, not skipped deliveries (checkpoints carry the count); each
  retried attempt keeps its own console lines; the constant-fold guard models
  goja's cost instead of counting depth.
- **Worker processes (FEAT-g6k3y9, 7ea2814).** The owner chose process
  isolation over a documented residual risk. The executor runs on a
  `jsrun.Engine`: the server passes a `jsworker.Pool`; tests, the corpus and
  any caller that configures nothing get an in-process `jsrun.Runner`.
- **Binary size (linux, `-trimpath -ldflags='-s -w'`, against main 018af94,
  the same embedded SPA in both):** before the crypto and Intl lanes, amd64
  +6.71 MB and arm64 +6.36 MB; with everything, amd64 **+7.47 MB** and arm64
  **+7.08 MB**, past the epic's +7 MB budget by 0.47 and 0.08 MB. goja is
  1.76 MB of it, and golang.org/x/text/collate 1.24 MB, which goja itself
  imports for localeCompare; the lanes added about 0.76 MB (the Intl tables
  and zone data, crypto, the tighter guards). Compressing the ~312 KB of
  embedded JavaScript would recover about 0.2 MB, not enough on amd64, so
  the overrun is reported to the owner rather than traded for a feature.

# Related Files

# Attachments
