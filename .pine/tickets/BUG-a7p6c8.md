---
id: BUG-a7p6c8
title: Embed credential listing leaks other credentials' names and ids through the paging cursor
status: todo
priority: low
labels:
    - security
    - credentials
    - embedding
parent: EPIC-brpz48
created: "2026-09-25T12:48:09Z"
updated: "2026-09-25T12:48:09Z"
---

# Description

`List` filters rows after the store has built the page, but still returns `page.NextCursor` (internal/api/handlers/credentials.go:231-249). That cursor is `base64(name\x00id)` of the last unfiltered row (internal/repository/credentials.go:240-259). With `limit=1`, an embed guest can page through and decode every credential name and id in the tenant.

# Acceptance Criteria
- [ ] Filter before paging, or build the cursor only from rows the caller may see.
- [ ] A test pages with `limit=1` as an embed session and sees no foreign name or id.
