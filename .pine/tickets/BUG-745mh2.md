---
id: BUG-745mh2
title: The node panel probes credentials it never shows, twice for duplicates, before the list has loaded
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-16T15:30:38Z"
updated: "2026-10-16T15:30:38Z"
---

# Description

Follow-up from the 2026-10-16 review of FEAT-8752vx's deleted-credential flag. The node panel's credential probe (GET /credentials/{id} for ids missing from the picker's list) is wider than the UI that uses it: it probes every id in `node.credentials`, including types the panel does not render (`visibleWhen` false), on the Settings tab, and when the panel cannot change credentials; duplicate ids can be probed twice; and a probe that finds a live credential runs the server's expensive usage scan.

# Acceptance Criteria
- [ ] Probe ids come only from the credential types the panel renders, only while the credential section is shown, deduplicated, and not before the credential list has loaded.
- [ ] Vitest proves a listed id, an id of a hidden type and a duplicate id are not probed.
