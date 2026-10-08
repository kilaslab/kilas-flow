---
topic: testing
updated: 2026-10-08T16:37:33Z
---

# testing

- 2026-09-21: Test helpers declared at package scope in a test package collide across parallel tickets (decodeProblem in internal/api, repeated twice this wave): rename before landing, and expect the collision only at integration time.
- 2026-09-21: Tests that assert wall-clock rates or margins flake on a loaded machine (login spray, wait deadlines, migration adoption): drive the clock or the scheduler through a nil-default seam and assert the decision, never the elapsed time.
- 2026-10-08: internal/jsrun TestLuxonMatchesTheRecordedNodeGoldens fails on this machine's time zone database (its Sydney DST case expects +10:00 at 2026-04-05 02:30) on unchanged main too; it is a host artefact, so verify jsrun changes with -skip TestLuxonMatchesTheRecordedNodeGoldens and do not chase it as a regression. (cites: internal/jsrun/luxon_test.go)
