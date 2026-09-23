---
id: FEAT-m1fdn4
title: Host SDK documentation on the site (guide + TypeDoc reference); accurate install and operation counts
status: todo
priority: medium
labels:
    - docs
    - sdk
parent: EPIC-62zt4j
created: "2026-09-23T02:04:34Z"
updated: "2026-09-23T02:04:34Z"
---

# Description

`@kilasflow/sdk` is documented only in its README. That README says `npm install` (which 404s), says "all 80" operations (there are 81), and has a snippet that doesn't parse.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: docs-saas; finding ids: DOC-27). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-62zt4j/`.

**n8n:** n/a

# Steps to Reproduce

1. `sdk/README.md:297-301`: `npm install @kilasflow/sdk`. `npm view @kilasflow/sdk version` → E404. Lines 369-372: the host-page example "runs it against a pulled container image and the published package, with no checkout". No git tags exist, so no image has been published either.
2. `sdk/examples/host-page/README.md:3-6` opens with "No checkout of this repository is needed". The footnote at lines 60-67 contradicts it.
3. `sdk/README.md:46`: "one thin, typed method per API operation — all 80". The spec has 81; `start-credential-oauth` has no method.

# Expected

A pre-release install path first (tarball from a checkout), with the published path marked "after v0.1.0". The operation count should be correct or dropped.

# Actual

A cold reader runs `npm install` and fails.

# Acceptance Criteria
- [ ] An SDK guide page (client, server, browser entry points) and a TypeDoc reference on the site
- [ ] Snippets are type-checked in CI
- [ ] Install instructions are accurate for the release state (from source until published)
- [ ] The datastore embed half matches the generated permit table

# Implementation Plan

Reword until the first release.

# Notes

Related tickets: BUG-b4cb1c, FEAT-3taswf

Related (from the audit): BUG-b4cb1c, FEAT-3taswf

# Related Files

the steps above.

# Attachments
