---
id: BUG-1vh6k6
title: The node-authoring guide says pack validate cannot check triggers, and that lifecycle.set always PUTs
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:30:38Z"
updated: "2026-10-16T15:30:38Z"
---

# Description

Follow-up from the 2026-10-16 review of FEAT-qdwm1k. The node-authoring guide (docs/src/content/docs/guides/node-authoring.md) still says a file-level `kilasflow pack validate` reports the trigger "bound to executor core.packTrigger, which this server has not installed"; validation now stubs that executor and runs the trigger rules, so it checks capture and secretCapture too. The "pieces" paragraph and reference/node-packs.md also say `lifecycle.set` PUTs, while `method` is declared per step.

# Acceptance Criteria
- [x] The stale validate caveat is replaced by what `pack validate` checks today (verified by running it on the guide's examples). Built `./bin/kilasflow` with `make build` and ran `pack validate` on both trigger examples extracted from the guide to a temp dir (`pack.acmeTrigger`, `pack.acmeSubscriptionTrigger`) and on `packs/waha/pack-trigger-202502.json`: all `ok:true`, `issues:[]`, exit 0. Two broken variants exit 1 with `invalid_pack`: `hmac.secretCapture is "token", which the lifecycle's set does not capture`, and `lifecycle check reads Captured.nope, which set does not capture`.
- [x] `lifecycle.set` is described as the method the pack declares, in both pages. guides/node-authoring.md "The pieces" paragraph now says it sends with the method the pack declares (`PUT` in the first example, `POST` in the second); reference/node-packs.md says the request is sent with the method the pack declares, `check` and `remove` declared the same way.
- [x] `make docs-build` passes. 52 pages built, all internal links valid; `make generate-docs-facts-check` up to date.

## Progress (2026-10-16)

- Replaced the "One honest caveat" paragraph in guides/node-authoring.md with what file-level `pack validate` checks today: the trigger rules (events and catch-all, webhook, `hmac.algorithm`, lifecycle `capture` placement, `{{ .Captured.<key> }}` reads, `secretCapture` against `secretParameter` and against the captured keys). It also states what validate cannot check: the bound credential and the remote service's answer.
- Described `lifecycle.set` as the pack-declared method in both pages.
