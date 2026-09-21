---
topic: testing
updated: 2026-09-21T01:03:46Z
---

# testing

- 2026-09-21: Test helpers declared at package scope in a test package collide across parallel tickets (decodeProblem in internal/api, repeated twice this wave): rename before landing, and expect the collision only at integration time.
- 2026-09-21: Tests that assert wall-clock rates or margins flake on a loaded machine (login spray, wait deadlines, migration adoption): drive the clock or the scheduler through a nil-default seam and assert the decision, never the elapsed time.
