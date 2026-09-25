---
id: BUG-qe71kf
title: 'Code node: files stored for a run that then fails stay unreferenced until the execution''s storage goes'
status: testing
priority: low
labels:
    - code-node
    - binary
parent: EPIC-tjnr1z
created: "2026-09-24T15:12:28Z"
updated: "2026-09-25T03:10:00Z"
---

# Description

Noted in Task 12 (BUG-djp647). A Code node's result may give several files inline as base64; the server stores them one by one after the run succeeded (`Job.storeInline`, `internal/jsrun/inline.go`). If storing one fails part-way (a storage error, the run cancelled), the files already stored stay in the execution's binary storage with nothing referencing them, and the run fails. A failed run that called `prepareBinaryData` leaves its files the same way.

They are removed with the execution's storage, so nothing leaks past it; the cost is dead bytes for the execution's lifetime.

# Acceptance Criteria

- [x] Decide whether files stored for a run that then failed are removed at once (the binary store would need a delete by ID scoped to the execution), or documented as kept until the execution's storage goes.

# Notes

## Plan

Look at `internal/binary.Store` before choosing. If it already deletes one payload by id inside an execution, or that is a few lines on a method it has, remove the files at once and test that a second store failure drops the first id without touching another execution. Otherwise document the keep-until-the-execution-goes behaviour on the Code (JavaScript) page, next to binary data, and record why here.

## Decision (2026-09-25)

Keep the files until the execution's storage goes. Documented on the Code (JavaScript) page, in the binary-data paragraph. No store or runtime change.

`Store` deletes only by execution (`DeleteExecution`, the whole directory) or by tenant (`DeleteTenant`). `Put` and `Get` are the only operations that name one id, and neither removes a payload that was stored successfully. A delete by id would be a new method on `Store`, `FileStore` and `Scoped`, and a delete that itself failed would be a new failure path on a run that has already failed. `prepareBinaryData` stores during the run, over the worker protocol; after that worker is gone, cleaning up would also mean guessing which ids this run created. The files are removed with the execution's storage, so nothing leaks past it.
