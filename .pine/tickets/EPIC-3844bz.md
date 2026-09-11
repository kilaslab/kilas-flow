---
id: EPIC-3844bz
title: AGL GOWA n8n suite on KilasFlow
status: done
priority: medium
created: "2026-09-11T07:44:52Z"
updated: "2026-09-11T08:29:31Z"
---

# Description

# Goals

## Work Evidence

Closed by `pine close --evidence` on 2026-09-11.

- Base: `2b7e35a2` (last commit at or before ticket created 2026-09-11)
- Files changed (base → working tree):

```
 .agents/skills/pine/SKILL.md     |  2 +-
 .gitignore                       |  1 +
 AGENTS.md                        |  2 +-
 internal/interop/n8n/n8n.go      | 31 +++++++++++++++++++++++++++++++
 internal/interop/n8n/n8n_test.go |  2 ++
 internal/web/dist/index.html     | 38 +++++++++++++++++++++++++++++++++++++-
 6 files changed, 73 insertions(+), 3 deletions(-)
```
