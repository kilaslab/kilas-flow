---
id: BUG-c7s5ss
title: 'Self-test minor findings: encryption.key boot warning, node ids in run errors, sticky notes as run nodes, legacy function nodes'
status: todo
priority: low
created: "2026-09-25T09:56:46Z"
updated: "2026-09-25T09:56:46Z"
---

# Description

Minor findings of the 2026-09-25 Code-node self-test:

1. At boot the server warns that the config key `encryption.key` "matches nothing and was ignored" when `KILASFLOW_ENCRYPTION_KEY` is set, yet the key is in fact used.
2. The execution-level error names the internal node id (`execute node "n2"`) rather than the node's name.
3. Sticky notes appear in a run's node list as succeeded nodes with 0 items.
4. The legacy n8n `function` and `functionItem` nodes import as "no equivalent" placeholders and are not mentioned in the docs (1 of 151 sampled Code templates has one).

# Acceptance Criteria
- [ ] The boot warning is accurate.
- [ ] Execution-level errors name the node by its name.
- [ ] Sticky notes are not listed as run nodes.
- [ ] The Code-node guide says what happens to `function`/`functionItem` (or they are mapped onto the Code node).
