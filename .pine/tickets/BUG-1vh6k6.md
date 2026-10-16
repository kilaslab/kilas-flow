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
- [ ] The stale validate caveat is replaced by what `pack validate` checks today (verified by running it on the guide's examples).
- [ ] `lifecycle.set` is described as the method the pack declares, in both pages.
- [ ] `make docs-build` passes.
