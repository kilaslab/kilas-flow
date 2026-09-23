---
id: FEAT-4jhtny
title: "OIDC sign-in for the operator dashboard"
status: todo
priority: low
labels:
    - saas
    - auth
    - oidc
parent: EPIC-7c3ry9
created: "2026-09-23T02:07:00Z"
updated: "2026-09-23T02:07:00Z"
---

# Description

SaaS operator staff need access to the dashboard; end users do not.

# Acceptance Criteria
- [ ] An OIDC issuer can be configured, its claims map to the operator tenant, and the local bootstrap user keeps working.

# Implementation Plan

# Notes

Source: a 2026-09-23 review of a host SaaS application (a multi-tenant customer-messaging and CRM product) that plans to replace its in-house workflow engine and data tables by embedding KilasFlow. Written generically on purpose: any SaaS embedding KilasFlow hits the same gap.

# Related Files

# Attachments
