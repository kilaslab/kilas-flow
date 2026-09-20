---
topic: embedding
updated: 2026-09-20T05:27:31Z
---

# embedding

- 2026-09-20: KilasFlow has no Go library embedding path: the engine, API and repository types live under internal/ and the only composition root is package main (cmd/kilasflow), so a third-party module cannot import them (a cross-module build fails with "use of internal package not allowed"). A host integrates over HTTP /api/v1 plus the iframe embed session; nodes arrive as declarative packs, not as Go code. (cites: nodes/core.go)
