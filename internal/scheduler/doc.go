// Package scheduler runs cron-triggered workflows.
//
// A due schedule is claimed inside the transaction that reads it: claiming
// advances next_run_at, so of any number of processes racing the same due time
// exactly one queues the run. Several processes against one PostgreSQL
// database are therefore safe, and the election needs no advisory lock. Role
// gating keeps a split deployment to a single scheduler anyway — workers never
// run the scheduler — which is what makes that a preference rather than a
// requirement.
//
// The zone a schedule is evaluated in comes from the workflow's own setting,
// falling back to the instance's (see DefaultTimezone), so "every day at 09:00"
// means nine where the operator is rather than nine UTC.
package scheduler
