// Package tenantpurge deletes one tenant and everything it owns.
//
// It is the orchestrator a deletion request runs through. Two halves of the
// work already existed — the repository's trace purge and the datastore
// engine's catalogue purge — and neither was reachable from anywhere; this
// package assembles them, adds the payload directory, the trigger rows, the
// definitions, the identity and the in-process sessions, and fixes the order
// they have to run in.
//
// # The documented order
//
// Steps, in the order Purge runs them and Service.Steps reports them:
//
//	lock-out       revoke the tenant's API keys, disable its accounts
//	stop-triggers  unregister remote triggers and stop pollers
//	triggers       schedules, webhook deliveries, routes and bindings
//	binaries       the tenant's payload directory
//	runs           node runs, waits, executions
//	definitions    secret bindings, credentials, versions, publish events, workflows
//	sessions       the in-process conversation memory
//	datastores     the catalogue rows and the physical tables
//	vectors        vector collections and their documents
//	identity       api keys, users, then the tenant row itself
//
// The first three steps close every intake path before a row is deleted: the
// tenant's own keys and sessions (lock-out), remote services and pollers
// (stop-triggers), and inbound webhooks and cron schedules (triggers). That is
// not tidiness, it is the difference between a purge that converges and one
// that does not. A webhook arriving between the runs step and the trigger step
// queues an execution against a workflow version that is about to be deleted,
// and the definitions step then fails on executions.workflow_version_id's
// RESTRICT foreign key — for a busy tenant, on most first attempts.
//
// stop-triggers must precede both the bindings and the credentials:
// webhook.Coordinator.Deactivated reads the tenant's bindings to decide which
// remote registrations to undo, and the lifecycle it calls resolves the
// tenant's credentials (a Telegram poller needs its bot token to call
// deleteWebhook). Deleting either first would leave remote services delivering
// into a route that no longer resolves.
//
// Waits precede executions inside the runs step because
// execution_waits.execution_id is ON DELETE RESTRICT: a tenant whose approval
// has been suspended for days is the ordinary case for a deletion request,
// not an edge one. Node runs come first for the same reason.
//
// The tenant row is last, and it is the record that the purge finished: while
// it exists the deletion is incomplete, and every step before it is scoped by
// the tenant id it names.
//
// # Transactions, per dialect
//
// One transaction per step, never one transaction for the whole purge.
//
// A single transaction would be wrong for three reasons. Each step is a
// different kind of evidence and a different kind of risk: a definition that
// refuses to go is a foreign key somebody else owns, while a trigger row that
// refuses to go is intake that has to be stopped first. A failed step has to
// report the rows the earlier steps already removed, which a rolled-back
// mega-transaction would erase. And a large tenant's deletes and DROP TABLEs
// should not hold one write lock for the whole of it.
//
// The dialects differ in what a step's transaction costs:
//
//   - SQLite serialises writers and the pool this build uses has a single
//     connection, so a statement issued on the outer handle while a
//     transaction is open deadlocks against itself. Every step therefore runs
//     its statements on the transaction's own handle and nothing else. DDL is
//     transactional here, so a failed DROP TABLE takes the catalogue delete
//     with it.
//   - PostgreSQL is MVCC: a step's transaction takes row locks only on the rows
//     it deletes and an ACCESS EXCLUSIVE lock on each table it drops, so the
//     rest of the installation keeps working while a tenant is deleted.
//     PostgreSQL also aborts the whole transaction on the first failed
//     statement, which is exactly why one transaction per step is the honest
//     shape: a failure after several deletes rolls that step back whole, and
//     the counts the caller already has are the steps that committed.
//
// Between steps the purge checks the context. A request that is cancelled — or,
// more often, a client whose SDK gave up after thirty seconds while the
// deletion of a large tenant is still running — stops the purge at the next
// step boundary and reports which step it stopped in. The caller is expected to
// detach the purge from the request's own deadline before calling Purge: a
// cancelled SQL statement never converges, and a half-deleted tenant that has to
// be retried is worse than one request that outlives its client.
//
// # Retry
//
// Every step is idempotent and every step is scoped by the tenant id, so a
// retry re-runs the whole order and converges:
//
//   - Deletes match on the tenant, and a table with nothing left deletes zero
//     rows. LockOut stamps only rows that are not already stamped, so a retry
//     reports zero rather than counting what the first attempt did.
//   - DROP TABLE carries IF EXISTS, so a purge interrupted after its DDL does
//     not wedge every later attempt on the table it removed itself. The
//     payload directory is located exactly and a missing one is a zero result.
//   - The tenant row exists only until the last step, so a purge that failed
//     earlier is retried exactly as it was issued: no different argument, no
//     resumed state.
//
// Result.Removed names every covered table, including the ones this call found
// empty. A caller has to be able to tell "nothing was there" from "this table
// was not covered at all", and a missing key cannot tell it.
//
// # Why the production purge never introspects the schema
//
// The completeness question — "is every tenant-scoped table covered?" — is
// answered by a test that introspects the live schema, not by the purge. The
// PostgreSQL tier exists to be pointed at a database the operator already has,
// which may be shared with a host application whose own tables have a tenant_id
// column of their own; a purge that discovered its tables at run time would
// either delete a host application's rows or refuse to delete a customer's.
// The set of tables is a property of this schema, and this schema ships as
// migrations, so the check belongs where the migrations are readable.
//
// Adding a tenant-scoped table later is therefore two things: one line in the
// step that owns it, and nothing else. The schema-driven completeness test in
// this package fails until that line exists or the test is told, with a reason,
// that the table holds no tenant data.
package tenantpurge
