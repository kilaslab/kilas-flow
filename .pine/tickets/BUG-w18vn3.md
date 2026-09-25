---
id: BUG-w18vn3
title: n8n import rejects a whole file when a node's typeVersion is a string
status: todo
priority: medium
labels:
    - import
    - n8n
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Found by the 2026-09-25 Code-node self-test. Template 4028 has `"typeVersion": "3.4"` (a string) on one node. n8n loads it; KilasFlow's n8n import rejects the whole file with 422 and a raw Go message ("cannot unmarshal string into Go struct field Document.nodes.5.typeVersion"). With the value turned into a number it imports fine.

# Acceptance Criteria
- [ ] A numeric string typeVersion imports as that number (n8n tolerates it).
- [ ] A typeVersion that is not a number at all gets a plain diagnostic naming the node, not a Go unmarshal error.
- [ ] No raw Go decode errors reach the import response for other loosely typed fields that n8n tolerates in the same way (check the Document struct).
