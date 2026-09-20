---
id: EPIC-cfe7ny
title: KilasFlow full-review remediation — Find/Verify/Critique backlog
status: done
priority: critical
labels:
    - review
    - n8n-parity
    - remediation
created: "2026-09-19T11:56:35Z"
updated: "2026-09-20T02:04:30Z"
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


## Remediation complete (2026-09-20)

All 50 findings tickets landed, verified by scoped tests, and closed with evidence. Follow-up tickets filed during the work live outside this epic: BUG-j7rtv3 (editor selection/projection loop — fixed), BUG-a1648n (editor coerced imported gt/gte to equals — fixed), BUG-66es9z (datastore ifExists second port unreachable — fixed), FEAT-cwmw90 (webhook URL API), FEAT-qdedm0 (adopt list paging), BUG-341sxn (Go module rename), FEAT-15k49d (i18n).

Verification at the final tree: `go build ./...` clean, `go vet ./...` clean, `go test ./... -count=1` green (import corpus scoreboard regenerated: activatable 14→15, runnable 4→5 after onError support), `web: vitest 496 passed`, `svelte-check 0 errors (1517 files)`, `make generate-api-check` green, `pine doctor` clean. Live smoke at HEAD: a manual→Set→Loop→body→done graph ran to `succeeded` with `$('Seed').item` resolving per item inside the loop and `$('Body').item` after the done port.

Bound recorded by every ticket: the live adversarial re-verify against a stub/private n8n instance (acceptance criterion 2) was not run — no stub/n8n instance was reachable in this environment; each ticket names what replaced it (in-process repro, n8n reference source, worktree A/B).

## Review pass (2026-09-20)

A five-slice adversarial review (security/tenancy, engine+repository, expression engine, importer/webhook/AI, web/SDK/docs) over b4f2147..HEAD found 30 defects the first pass shipped; all were fixed with a failing-first regression test each and the tickets were reopened and closed again:

- security: embed confinement missed the Workflow Tool node (critical), load-options credential bound read the wrong revision, typed-nil embed issuer 500, interop 500s leaking driver text, credentials/schedules/datastores discarding the 500 cause, config warning before the logger, CORS scope, SSE terminal frame.
- engine: per-item suspend dropped the items behind it, expression sub-workflow targets blocked activation, parser parsed the max-iterations fallback, imported sampling options ignored, `fullResponse` envelope regression, v4 redirect default, run-ceiling message.
- expression: `$input`/namespaces marshalling to `{}`, `}}`-scan truncation, abstract equality, Object key order, undefined properties, sort comparator, `['length']`, Math.round, toFixed, exponent form, non-finite JSON.
- importer/webhook: Respond answer lost across processes (critical), Calculator Tool unreachable, followRedirects/timeout units, moment token cascade, Merge options bag, Switch fallback, multi-method binding, form page before the allow-list, multi-method export version.
- web: `make coordinates-check` red on its own comment (P0), listings hiding rows past the first cursor page, editor stealing Enter, suggestion list keyboard path, quickstart path.

Two of the review's own regression tests were themselves defective (a pointer-type classifier that read `*pgconn.PgError` as a caller mistake, and a `.Rows()` on an `Exec`); both were corrected before the suites went green.

Final gates at the closing tree: `go build ./...` + `go vet ./...` clean, `go test ./... -count=1` green, web `vitest` 514 passed, `svelte-check` 0 errors (1517 files), `make generate-api-check`, `make coordinates-check`, `make build-clean-check` green, `pine doctor` clean, and a live smoke (loop lineage + a POST webhook answering n8n's `{"message":"Workflow was started"}` with the trigger item carrying body/headers/executionMode and read-surface redaction).
