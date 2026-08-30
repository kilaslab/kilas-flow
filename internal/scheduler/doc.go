// Package scheduler runs cron-triggered workflows.
//
// V1 assumes a single process, which is sufficient for the SQLite default
// deployment. Distributed scheduling is deferred.
//
// Milestone 2+.
package scheduler
