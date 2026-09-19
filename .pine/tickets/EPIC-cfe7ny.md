---
id: EPIC-cfe7ny
title: KilasFlow full-review remediation — Find/Verify/Critique backlog
status: todo
priority: critical
labels:
    - review
    - n8n-parity
    - remediation
created: "2026-09-19T11:56:35Z"
updated: "2026-09-19T12:06:28Z"
---
# Description

Consolidated remediation backlog from the KilasFlow full-review workflow `wf_c415e773-4e1` (Find 14/14 dimensions + Verify 14/14 adversarial re-verification + Critique 1/1). 648/668 verdicts confirmed (~97%); ~all findings reproduced live against stub / live n8n 2.33.7 / private instances.

# Goals

- Fix every confirmed defect grouped below; each child ticket carries full evidence, n8n behavior, impact, suggested fix, files, and acceptance checkboxes.
- Coverage: 324 of 334 findings. Excluded 10 by verifier verdict: 8 refuted (documented intentional bounds: 30s response wait, 30s outbound cap, 500-no-Respond, 1 MiB body, insecure-defaults framing, Origin-header caveat, manual-active sub-workflow callability, DOM-test premise) + 2 already-tracked-open (FEAT-1axhdn datastore increment/precondition halves, FEAT-8mymac credentialed n8n benchmark half).
- Deduped: single-root-cause clusters merged (exact-version lookup x5, http-body-expression x2, set-import x2, newline x3-5, cred-delete x3, refetch x3, auth-ui x3-7, pgvector x2).

# Children (50)

Critical-first: T01 exact-version lookup, T02 http-body-expression, T03 paired-item lineage, T04 trace dupkey, T05 worker lease, T06 loop restart, T07 newline strip, T08 visibleWhen, T09 exec refetch, T11 mask secret, T14 $fromAI reuse, T18 embed 401, T20 embed escape, T21 set import. Then high/medium thematic tickets T10, T12, T13, T15-T17, T19, T22-T50.

# Grounding

- n8n Set docs (via Context7 `/n8n-io/n8n-docs`): Manual Mapping + Keep Only Set Fields discards unused input.
- n8n item-linking docs: `$(name).item` = linked item via per-item thread; fails only when broken/ambiguous.
- n8n Respond-to-Webhook docs: First Incoming Item default among respond options.

# Evidence index

- Raw findings: `/tmp/all_findings.json` (334), index `/tmp/finding_index.tsv`.
- Per-dimension repros: `/private/tmp/claude-501/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/scratchpad/work/<dim>/` (FINDINGS.md, PROGRESS.md).
- Workflow journal: `~/.claude/projects/-Users-izzadev-projects-k-flow/21032eb5-98ed-492e-a372-bbee00f55df0/subagents/workflows/wf_c415e773-4e1/journal.jsonl`.
- Not done (token ran out in Claude Code): 4 gap probes (pg-multiprocess, data-nodes-parity, messaging-packs-e2e, load-concurrency) + Consolidate phase. Critic gap briefs saved in journal result `ada9e8e96a7c33367`.

