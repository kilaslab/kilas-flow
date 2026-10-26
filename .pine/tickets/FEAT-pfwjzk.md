---
id: FEAT-pfwjzk
title: llms.txt and agent-readable docs; drift gates for counts quoted in prose; versioning policy
status: done
priority: low
labels:
    - docs
    - llms
    - drift-gates
parent: EPIC-62zt4j
created: "2026-09-23T02:04:35Z"
updated: "2026-09-23T02:04:35Z"
---

# Description

Coding agents are a first-class audience: the binary embeds a skills bundle and an MCP server. The docs site should be agent-readable too, and hand-written counts in prose keep drifting (49 vs 61 node types, 35 vs 81 operations).

# Acceptance Criteria
- [ ] `/llms.txt` and `/llms-full.txt` are served (for example with `starlight-llms-txt`), with the integrator tracks first and a link to the skills bundle
- [ ] CI fails when a count quoted in prose disagrees with the product (node types, operations, credential types)
- [ ] A versioning policy is merged (for example `starlight-versions` from the first release tag)

# Implementation Plan

# Notes

# Related Files

# Attachments

# Progress

Implemented 2026-10-25: llms.txt generated at the site root (starlight-llms-txt), prose counts drift-gated via scripts/docs-facts (extended with operation-count coverage + new tests); gate rides the existing generate-docs-facts-check in CI's drift job (docs-build itself stays Go-free by design). Gates: facts-check 5 pages up to date, planted-drift test fails correctly, go test scripts/docs-facts ok.
