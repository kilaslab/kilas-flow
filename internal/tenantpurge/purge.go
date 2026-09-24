package tenantpurge

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// ErrTenantRequired reports an id that names no tenant: empty, or nothing but
// whitespace. It is refused before any collaborator is called, because a purge
// that matches nothing by accident is one typo away from a purge that matches
// everything by accident.
var ErrTenantRequired = errors.New("tenantpurge: a tenant id is required")

// ErrProtectedTenant reports a tenant this installation refuses to delete. The
// operator's own tenant is the one that matters: deleting it deletes the
// credential the caller is using, and there is nobody left to recreate it.
var ErrProtectedTenant = errors.New("tenantpurge: this tenant is protected and cannot be deleted")

// RunPurger deletes one tenant's execution trace. *repository.GORMExecutionStore
// implements it.
type RunPurger interface {
	PurgeTenant(ctx context.Context, tenant repository.TenantScope) (repository.TenantPurgeResult, error)
}

// RowPurger deletes one tenant's rows outside the trace and the datastore
// catalogue, one step at a time. *repository.GORMTenantPurger implements it.
type RowPurger interface {
	LockOut(ctx context.Context, tenant repository.TenantScope) (repository.TenantLockOut, error)
	ActiveTriggerWorkflows(ctx context.Context, tenant repository.TenantScope) ([]string, error)
	PurgeTriggers(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error)
	PurgeDefinitions(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error)
	PurgeVectors(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error)
	PurgeIdentity(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error)
}

// DatastorePurger drops one tenant's datastore catalogue rows and their
// physical tables. *datastore.Engine implements it.
type DatastorePurger interface {
	PurgeTenant(ctx context.Context, tenantID string) (datastore.PurgeResult, error)
}

// BinaryPurger removes one tenant's payload directory. binary.Store implements
// it; a deployment with no binary storage configures none, and the step is
// then skipped rather than failing.
type BinaryPurger interface {
	DeleteTenant(tenantID string) (binary.TenantResult, error)
}

// SessionForgetter drops one tenant's in-process conversation sessions.
// *ai.BufferMemory implements it. It is a different interface from the
// handler-side SessionForgetter, which forgets one workflow.
type SessionForgetter interface {
	ForgetTenant(tenantID string)
}

// TriggerStopper unregisters the remote registrations a tenant's active
// workflows own. *webhook.Coordinator implements it with the same call the
// deactivate workflow path makes.
//
// It returns nothing on purpose: unregistering from somebody else's service is
// best effort and never blocks a deletion. A stale registration delivers to a
// route that no longer resolves, which is a 404 rather than a leak.
type TriggerStopper interface {
	Deactivated(ctx context.Context, tenantID, workflowID string, declared map[string]string)
}

// Deps is what a purge needs. Logger defaults to slog.Default(); Binaries,
// Sessions and Triggers may be nil, and a nil TriggerLifecycles is passed
// through as the empty set.
type Deps struct {
	Logger     *slog.Logger
	Runs       RunPurger
	Rows       RowPurger
	Datastores DatastorePurger
	// Binaries is nil when this server has no payload storage configured.
	Binaries BinaryPurger
	// Sessions is nil in a process that holds no sessions (a worker).
	Sessions SessionForgetter
	// Triggers is nil in a process that serves no inbound triggers.
	Triggers TriggerStopper
	// TriggerLifecycles returns the node-type to lifecycle map the coordinator
	// was built with, so stopping a trigger resolves the same hook the
	// activation did. Nil means no node declares one.
	TriggerLifecycles func() map[string]string
	// Protected names tenants this installation refuses to delete.
	Protected []string
}

// Result is the evidence a deletion request leaves behind: what was removed,
// per table, plus the two counts that are not table rows — the physical
// datastore tables dropped and the payloads deleted from disk.
//
// Removed always names every covered table, including those this call found
// empty, so a caller can tell "nothing was there" from "not covered".
type Result struct {
	TenantID        string
	Removed         map[string]int64
	DatastoreTables int
	Binaries        binary.TenantResult
}

// StepError reports the step a purge failed in, or stopped in because the
// context was cancelled.
type StepError struct {
	Step string
	Err  error
}

func (e *StepError) Error() string { return fmt.Sprintf("tenant purge step %q: %v", e.Step, e.Err) }

// Unwrap is what makes errors.Is(err, ctx.Err()) and errors.As onto the
// underlying failure work.
func (e *StepError) Unwrap() error { return e.Err }

// StepInfo is one step as it is reported: the name the docs use and the tables
// whose rows it removes.
type StepInfo struct {
	Name   string
	Tables []string
}

// Service deletes tenants in the documented order.
type Service struct {
	logger     *slog.Logger
	runs       RunPurger
	rows       RowPurger
	datastores DatastorePurger
	binaries   BinaryPurger
	sessions   SessionForgetter
	triggers   TriggerStopper
	lifecycles func() map[string]string
	protected  map[string]struct{}
}

// New wires a tenant purge. The three collaborators that delete rows are
// required: a purge that silently skipped one would report success over rows it
// never removed.
func New(deps Deps) (*Service, error) {
	if deps.Runs == nil {
		return nil, errors.New("tenantpurge: a run purger is required")
	}
	if deps.Rows == nil {
		return nil, errors.New("tenantpurge: a row purger is required")
	}
	if deps.Datastores == nil {
		return nil, errors.New("tenantpurge: a datastore purger is required")
	}
	logger := deps.Logger
	if logger == nil {
		logger = slog.Default()
	}
	protected := make(map[string]struct{}, len(deps.Protected))
	for _, id := range deps.Protected {
		if trimmed := strings.TrimSpace(id); trimmed != "" {
			protected[trimmed] = struct{}{}
		}
	}
	return &Service{
		logger:     logger,
		runs:       deps.Runs,
		rows:       deps.Rows,
		datastores: deps.Datastores,
		binaries:   deps.Binaries,
		sessions:   deps.Sessions,
		triggers:   deps.Triggers,
		lifecycles: deps.TriggerLifecycles,
		protected:  protected,
	}, nil
}

// step is one named stage of the purge and everything it deletes.
type step struct {
	name   string
	tables []string
	run    func(ctx context.Context, tenant repository.TenantScope, result *Result) (map[string]int64, error)
}

func (s *Service) steps() []step {
	return []step{
		{name: "lock-out", tables: []string{"api_keys", "users"}, run: s.lockOut},
		{name: "stop-triggers", run: s.stopTriggers},
		{
			name:   "triggers",
			tables: []string{"poll_cursors", "schedules", "webhook_deliveries", "webhook_routes", "webhook_bindings"},
			run: func(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
				return s.rows.PurgeTriggers(ctx, tenant)
			},
		},
		{name: "binaries", run: s.purgeBinaries},
		{
			name:   "runs",
			tables: []string{"execution_node_runs", "execution_waits", "executions", "idempotency_keys"},
			run:    s.purgeRuns,
		},
		{
			name: "definitions",
			tables: []string{
				"secret_bindings", "credentials", "workflow_versions", "workflow_publish_events", "workflow_static_data", "workflows",
			},
			run: func(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
				return s.rows.PurgeDefinitions(ctx, tenant)
			},
		},
		{name: "sessions", run: s.forgetSessions},
		{
			name:   "datastores",
			tables: []string{"datastore_columns", "datastores"},
			run:    s.purgeDatastores,
		},
		{
			name: "vectors",
			tables: []string{
				"vector_documents_384", "vector_documents_768", "vector_documents_1024",
				"vector_documents_1536", "vector_collections",
			},
			run: func(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
				return s.rows.PurgeVectors(ctx, tenant)
			},
		},
		{
			name:   "identity",
			tables: []string{"api_keys", "users", "tenants"},
			run: func(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
				return s.rows.PurgeIdentity(ctx, tenant)
			},
		},
	}
}

// Steps reports the purge's stages, in the order they run. The names are a
// contract: the deployment docs name them, and an operator reading a
// half-finished purge has to be able to match a log line to a step.
func (s *Service) Steps() []StepInfo {
	steps := s.steps()
	infos := make([]StepInfo, 0, len(steps))
	for _, current := range steps {
		infos = append(infos, StepInfo{Name: current.name, Tables: append([]string(nil), current.tables...)})
	}
	return infos
}

// Tables reports every table the purge removes rows from, in first-seen step
// order. The schema-driven completeness test uses it as the set of tables a
// purge covers.
func (s *Service) Tables() []string {
	seen := map[string]struct{}{}
	tables := []string{}
	for _, current := range s.steps() {
		for _, table := range current.tables {
			if _, ok := seen[table]; ok {
				continue
			}
			seen[table] = struct{}{}
			tables = append(tables, table)
		}
	}
	return tables
}

// Exempt names the tenant-scoped tables a purge deliberately does not touch,
// with the reason. It is empty today — every tenant_id table in the schema is
// deleted by a step — and it exists so a later table that genuinely holds no
// tenant data is a decision somebody wrote down rather than a gap in a list.
//
// The reason is asserted to be non-empty and to name a real table by this
// package's completeness test, because an empty reason is how an exemption
// becomes a silent leak.
func (s *Service) Exempt() map[string]string {
	return map[string]string{}
}

// Purge deletes one tenant and everything it owns, in the documented order.
//
// A failure returns the partial Result — what the committed steps removed — and
// a *StepError naming the step it failed in. Steps after the failure are not
// attempted, and the whole call is safe to repeat: see the package comment for
// why a retry converges.
func (s *Service) Purge(ctx context.Context, tenantID string) (Result, error) {
	id := strings.TrimSpace(tenantID)
	if id == "" {
		return Result{}, ErrTenantRequired
	}
	if _, protected := s.protected[id]; protected {
		return Result{}, fmt.Errorf("%w: %s", ErrProtectedTenant, id)
	}

	tenant := repository.TenantScope{ID: id}
	result := Result{TenantID: id, Removed: make(map[string]int64)}
	for _, table := range s.Tables() {
		result.Removed[table] = 0
	}

	for _, current := range s.steps() {
		if err := ctx.Err(); err != nil {
			return result, &StepError{Step: current.name, Err: err}
		}
		counts, err := current.run(ctx, tenant, &result)
		if err != nil {
			s.logger.Error("tenant purge step failed", "tenant", id, "step", current.name, "error", err)
			return result, &StepError{Step: current.name, Err: err}
		}
		for table, count := range counts {
			result.Removed[table] += count
		}
		s.logger.Info("tenant purge step removed rows", "tenant", id, "step", current.name, "removed", counts)
	}
	return result, nil
}

// lockOut revokes the tenant's keys and disables its accounts. The counts are
// not removals — nothing is deleted here — so they go to the log and not into
// Result.Removed, which answers "what was removed".
func (s *Service) lockOut(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
	locked, err := s.rows.LockOut(ctx, tenant)
	if err != nil {
		return nil, err
	}
	s.logger.Info("tenant locked out", "tenant", tenant.ID, "api_keys", locked.APIKeys, "users", locked.Users)
	return nil, nil
}

// stopTriggers asks the coordinator to unregister every remote trigger the
// tenant's active workflows own, before their bindings and the credentials the
// hooks authenticate with are deleted.
//
// Failing to list the workflows fails the step: a purge that could not tell
// which remote services to stop would leave deliveries arriving at a tenant
// that no longer exists. Failing to stop one does not, and cannot — Deactivated
// reports nothing.
func (s *Service) stopTriggers(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
	if s.triggers == nil {
		return nil, nil
	}
	workflows, err := s.rows.ActiveTriggerWorkflows(ctx, tenant)
	if err != nil {
		return nil, err
	}
	var declared map[string]string
	if s.lifecycles != nil {
		declared = s.lifecycles()
	}
	for _, workflowID := range workflows {
		s.triggers.Deactivated(ctx, tenant.ID, workflowID, declared)
	}
	return nil, nil
}

// purgeBinaries removes the tenant's payload directory. A filesystem has no
// transaction to join, which is why this stands as a step of its own: the
// directory is removed before the rows naming its payloads, so a failure here
// leaves the trace intact and the retry finds the same directory.
func (s *Service) purgeBinaries(_ context.Context, tenant repository.TenantScope, result *Result) (map[string]int64, error) {
	if s.binaries == nil {
		return nil, nil
	}
	removed, err := s.binaries.DeleteTenant(tenant.ID)
	if err != nil {
		return nil, err
	}
	result.Binaries = removed
	return nil, nil
}

// purgeRuns deletes the tenant's execution trace: node runs, then waits, then
// executions, in the one transaction the run purger owns. The request
// idempotency keys travel with it: a key's recorded outcome names the execution
// a run queued or the datastore row a write returned, so it is the tenant's
// data and outlives neither.
func (s *Service) purgeRuns(ctx context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
	purged, err := s.runs.PurgeTenant(ctx, tenant)
	if err != nil {
		return nil, err
	}
	return map[string]int64{
		"execution_node_runs": int64(purged.NodeRuns),
		"execution_waits":     int64(purged.Waits),
		"executions":          int64(purged.Executions),
		"idempotency_keys":    int64(purged.IdempotencyKeys),
	}, nil
}

// forgetSessions drops the tenant's in-process conversations. Best effort by
// construction: there is nothing to fail, and a session that outlived its
// tenant would be caught by the revalidation TTL the lock-out step relies on.
func (s *Service) forgetSessions(_ context.Context, tenant repository.TenantScope, _ *Result) (map[string]int64, error) {
	if s.sessions != nil {
		s.sessions.ForgetTenant(tenant.ID)
	}
	return nil, nil
}

// purgeDatastores drops the tenant's catalogue rows and the physical tables
// they name, and reports how many tables were dropped separately: a physical
// table is not a row and cannot be counted as one.
func (s *Service) purgeDatastores(ctx context.Context, tenant repository.TenantScope, result *Result) (map[string]int64, error) {
	purged, err := s.datastores.PurgeTenant(ctx, tenant.ID)
	if err != nil {
		return nil, err
	}
	result.DatastoreTables = len(purged.Tables)
	return map[string]int64{
		"datastore_columns": int64(purged.Columns),
		"datastores":        int64(purged.Datastores),
	}, nil
}
