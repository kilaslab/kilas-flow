---
id: FEAT-4e376e
title: Multi-page n8n Form node and Wait-for-form with custom fields
status: todo
priority: medium
labels:
    - n8n
    - parity
    - forms
parent: EPIC-8rbys7
created: "2026-09-23T01:17:55Z"
updated: "2026-09-23T01:17:55Z"
---

# Description

`n8n-nodes-base.form` appears in 38 templates and is the #7 unlock. Today a Wait-for-form resumes only on the approval page.

Found by the 2026-09-23 n8n-parity and UX audit (source reports: node-gap; finding ids: NG-17). The evidence paths refer to that session's scratchpad, and the summary is in `.pine/attachments/EPIC-8rbys7/`.

**n8n:** `n8n-nodes-base.form` appears in 38 templates (3.8%). It adds pages after a Form Trigger ("Next Form Page") and a "Form Ending" page; per the n8n docs, it "create[s] user-facing forms with multiple steps". The Wait node's `resume: form` shows a custom form.

# Steps to Reproduce

1. Import Manual → n8n Form, using template 2878 (`formFields`, `options`).
2. Import a Wait with `resume: form`.

# Expected

The form trigger continues into subsequent pages and an ending page within one execution, and a Wait `form` renders the fields it declares.

# Actual

- The Form node becomes a blocking placeholder and is the #7 unlock (+15 templates).
- The Wait imports with the lossy note "n8n's form wait resumes on a form the workflow defines; this server resumes on its approval page, where the run is approved or denied. Any fields the n8n form collected are not part of that page."

# Acceptance Criteria
- [ ] A Form Trigger continues into Next Form Page and Form Ending pages within one execution
- [ ] Wait `resume: form` renders the declared fields and passes the submission on as items
- [ ] Template 2878 imports with no blocking issue

# Implementation Plan

Reuse the durable Wait/resume machinery (FEAT-rj17xj) to serve a `kilasflow.form` page per node, keyed by the execution's resume token, plus a completion/ending page type.

# Notes

Related tickets: FEAT-rj17xj

Related (from the audit): none (FEAT-rj17xj done, approval page only)

# Related Files

`probes/n8n_Form_page.json`, `internal/interop/n8n/parameters.go:3051-3060`

# Attachments
