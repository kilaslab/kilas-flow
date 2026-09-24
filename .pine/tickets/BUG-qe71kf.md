---
id: BUG-qe71kf
title: 'Code node: files stored for a run that then fails stay unreferenced until the execution''s storage goes'
status: todo
priority: low
labels:
    - code-node
    - binary
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-24T15:12:28Z"
---

# Description

Noted in Task 12 (BUG-djp647). A Code node's result may give several files inline as base64; the server stores them one by one after the run succeeded (`Job.storeInline`, `internal/jsrun/inline.go`). If storing one fails part-way (a storage error, the run cancelled), the files already stored stay in the execution's binary storage with nothing referencing them, and the run fails. A failed run that called `prepareBinaryData` leaves its files the same way.

They are removed with the execution's storage, so nothing leaks past it; the cost is dead bytes for the execution's lifetime.

# Acceptance Criteria

- [ ] Decide whether files stored for a run that then failed are removed at once (the binary store would need a delete by ID scoped to the execution), or documented as kept until the execution's storage goes.
