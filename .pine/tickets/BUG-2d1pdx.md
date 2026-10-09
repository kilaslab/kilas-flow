---
id: BUG-2d1pdx
title: 'make test is red: an OpenAI base-URL test shares one recording credential stub across parallel subtests'
status: todo
priority: medium
parent: EPIC-8rbys7
created: "2026-10-09T11:52:28Z"
updated: "2026-10-09T11:52:28Z"
---

# Description

`TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL` (nodes/openai_credential_url_test.go, added by BUG-epy2se) shares one `*stubCredentials` across four `t.Parallel()` subtests. `stubCredentials` appends to its `calls` slice on every resolve, so the subtests write the same slice concurrently. Under `-race` the race detector fails the test, and because the race is reported while other parallel tests are running, it also marks about a dozen unrelated `nodes` tests as failed. `make test` runs with `-race`, so it is red on main.

Found by the Track G coder in the 2026-10-09 parallel wave.

# Steps to Reproduce

1. `go test ./nodes -race -run TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL -count=3`

# Expected

The test passes under `-race`.

# Actual

`WARNING: DATA RACE` (three reports in three runs) and `--- FAIL: TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL`.

# Acceptance Criteria
- [x] Each subtest gets its own resolver, and the test passes under `-race` (`go test ./nodes -race -run TestAnOpenAIChatModelFallsBackToItsCredentialsBaseURL -count=3`: ok; the same command on the old file reports 3 data races).

# Notes

Test-only change; production code is untouched.
