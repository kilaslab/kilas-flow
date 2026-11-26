---
id: BUG-9hf565
title: 'CI: the Luxon ambiguous-November-hour golden depends on the real clock and has failed since 2026-11-01'
status: todo
priority: high
labels:
    - ci
    - jsrun
created: "2026-11-26T08:05:19Z"
updated: "2026-11-26T08:05:19Z"
---

# Description

`TestLuxonMatchesTheRecordedNodeGoldens` (internal/jsrun) has failed on every CI run since the real clock passed 2026-11-01, on main and on every PR, which blocks the required "Go tests" check. Its probe "New York: the ambiguous November hour takes the first offset" resolves `2026-11-01T01:30` in America/New_York. Luxon seeds its local-to-UTC offset guess with `zone.offset(Settings.now())`. While New York was on EDT the guess was -4 and the ambiguous hour took the first offset (-04:00), which is what the golden recorded. Once "now" is EST the guess is -5 and the same code gives -05:00. Node 24 does the same thing with the vendored bundle: `now=real` gives the failing value, and pinning `Settings.now` to 2026-10-01 gives the golden. So the golden depended on when it was recorded and the runtime is correct.

# Acceptance Criteria
- [x] The probe pins `Settings.now` to 2026-10-01T12:00:00Z, in both the recorder (scripts/js-parity/record.mjs) and the golden (internal/jsrun/testdata/parity/luxon.json), following the pattern the relative-time probes already use. The expected value does not change.
- [x] `go test ./internal/jsrun/` passes, and a Node 24 run of the probe with the pin reproduces the golden.

# Notes

Found while merging FEAT-n12211, FEAT-s3sfx5 and FEAT-4fp51b (PR #57), whose CI was red only on this test. It is a different failure from the local-only Sydney host artefact in .pine/memory/testing.md.
