// Package guardrails holds checks that protect project-wide invariants which no
// single package owns and which a code review would not reliably catch.
//
// The invariants are documented in .pine/memory/; the tests here are what makes
// them enforceable. They run inside `go test ./...` so that a change violating
// one fails in the same command every other ticket already runs, rather than
// depending on anyone remembering a rule.
package guardrails
