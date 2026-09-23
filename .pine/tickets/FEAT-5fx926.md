---
id: FEAT-5fx926
title: WASM module pack tutorial and community-nodes guide rewrite
status: todo
priority: low
labels:
    - docs
    - wasm
    - community-nodes
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

The community-nodes guide contradicts the node-pack reference on WASM module packs and uses the legacy `sdk.Main` API.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-13). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n/a

# Steps to Reproduce

1. `guides/community-nodes.md` frontmatter: "Neither host half ships yet". Line 16: "no host loads packs yet". Lines 22-24: "The host half … is not built". Lines 47-53: "A pack therefore cannot call an API, read a credential". Line 125: "an author-side contract today, not an operator path". Lines 131-137: "The WASM pack path is not wired … it has no `.wasm` story".
2. The same page at line 55 says "The **host side ships now**". `reference/node-packs.md:175-231` documents loading, auditing and the `http`/`binary.*` capabilities for module packs.
3. The guide's sample is `func run(items []sdk.Item) …; sdk.Main(run)`, which is the legacy batch contract with no parameters and no capabilities. The capable API is `sdk.MainCall(func(sdk.Call) …)` (`pkg/sdk/call.go:47`). `pkg/sdk/example/fetch/main.go:9-10` says "Its pack.json beside this file is a template", but no pack.json exists there.

# Expected

A single accurate "Build a WASM module pack" tutorial that covers building a module with `MainCall`, writing the manifest, the sha256, installing, capabilities and credentials, and testing with `SetHost`.

# Actual

A node author cannot tell whether WASM packs work. (They do; the reference page is the accurate one.)

# Acceptance Criteria
- [ ] A tutorial takes a WASM pack from MainCall, capabilities, credentials and manifest to install and SetHost testing; the pack makes an HTTP call through a credential
- [ ] No page says WASM packs are unbuilt
- [ ] The `example/fetch/pack.json` manifest exists
- [ ] The JS sidecar page moves under Build nodes

# Implementation Plan

Rewrite the page, and add `pack.json` to `pkg/sdk/example/fetch`.

# Notes

Related tickets: FEAT-48hreg

Related (from the audit): FEAT-48hreg (done)

# Related Files

the file and line references above.

# Attachments
