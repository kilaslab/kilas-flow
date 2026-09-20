---
id: FEAT-qdedm0
title: Dashboard lists fetch every row; adopt limit/cursor from the paged list endpoints
status: doing
priority: medium
created: "2026-09-20T00:51:08Z"
updated: "2026-09-20T07:42:30Z"
---

# Description

# Acceptance Criteria
- [ ] Define acceptance criteria

# Implementation Plan

# Notes

# Related Files

# Attachments

Links: FEAT-0895qc (list UX), BUG-fv5fer (endpoints). Created from DXOps2's note: the four list endpoints advertise limit/cursor and no dashboard page uses them; passing undefined preserves behaviour but the half-wired surface should be adopted or documented before FEAT-0895qc closes.

## Note (2026-09-20)

The premise of this ticket — no dashboard page uses the `limit`/`cursor` the
list endpoints advertise — is closed by BUG-th16c1 (`4a49d70`): the six surfaces
that read those listings now drain every page through
`web/src/lib/dashboard/cursor-page.ts` (`drainPages`, `headerCursor`,
`DRAIN_PAGE_LIMIT = 500`, unit-tested) and the five listings plus the executions
workflow map and both credential pickers show the whole tenant again.

What is deliberately **not** done, and is the remaining product decision here: a
"Load more" list that fetches pages lazily instead of draining up-front. The
drain keeps the current behaviour — search, sort and the "N in this workspace"
count work over the complete list — at the cost of one request per 500 rows.
Left open for that call rather than closed.
