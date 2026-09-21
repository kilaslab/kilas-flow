package tenantpurge_test

import (
	"context"
	"errors"
	"slices"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/tenantpurge"
)

// documentedSteps is the order every tenant deletion runs in, written out here
// as the contract it is: the docs page names these steps, and an operator
// reading a half-finished purge needs them to mean what they say. The names are
// also what Service.Steps() returns.
var documentedSteps = []string{
	"lock-out", "stop-triggers", "triggers", "binaries", "runs",
	"definitions", "sessions", "datastores", "vectors", "identity",
}

// documentedCalls is the collaborator sequence the documented order implies for
// a tenant whose active triggers are these workflows. The sub-calls are named
// because they are where the order can actually be got wrong: stopping the
// triggers is one step but two calls, and both have to land before any row is
// deleted.
func documentedCalls(triggerWorkflows ...string) []string {
	calls := []string{"lock-out", "stop-triggers:list"}
	for _, workflowID := range triggerWorkflows {
		calls = append(calls, "stop-triggers:stop:"+workflowID)
	}
	return append(calls, "triggers", "binaries", "runs", "definitions", "sessions", "datastores", "vectors", "identity")
}

// recorder is the call log every fake and decorator appends to.
type recorder struct {
	calls []string
}

func (r *recorder) add(name string) { r.calls = append(r.calls, name) }

func (r *recorder) snapshot() []string { return slices.Clone(r.calls) }

// Stubs answer a purge without touching anything. The counts are distinctive so
// the test can prove they reach the result rather than being dropped.
type stubRuns struct{}

func (stubRuns) PurgeTenant(context.Context, repository.TenantScope) (repository.TenantPurgeResult, error) {
	return repository.TenantPurgeResult{Executions: 2, NodeRuns: 3, Waits: 1}, nil
}

type stubRows struct{}

func (stubRows) LockOut(context.Context, repository.TenantScope) (repository.TenantLockOut, error) {
	return repository.TenantLockOut{APIKeys: 1, Users: 1}, nil
}

func (stubRows) ActiveTriggerWorkflows(context.Context, repository.TenantScope) ([]string, error) {
	return []string{"wf-1", "wf-2"}, nil
}

func (stubRows) PurgeTriggers(context.Context, repository.TenantScope) (map[string]int64, error) {
	return map[string]int64{
		"schedules": 1, "webhook_deliveries": 3, "webhook_routes": 1, "webhook_bindings": 1,
	}, nil
}

func (stubRows) PurgeDefinitions(context.Context, repository.TenantScope) (map[string]int64, error) {
	return map[string]int64{
		"secret_bindings": 1, "credentials": 1, "workflow_versions": 2,
		"workflow_publish_events": 1, "workflows": 2,
	}, nil
}

func (stubRows) PurgeVectors(context.Context, repository.TenantScope) (map[string]int64, error) {
	return map[string]int64{"vector_collections": 1}, nil
}

func (stubRows) PurgeIdentity(context.Context, repository.TenantScope) (map[string]int64, error) {
	return map[string]int64{"api_keys": 1, "users": 1, "tenants": 1}, nil
}

type stubDatastores struct{}

func (stubDatastores) PurgeTenant(context.Context, string) (datastore.PurgeResult, error) {
	return datastore.PurgeResult{Datastores: 1, Columns: 2, Tables: []string{"ds_one", "ds_two"}}, nil
}

type stubBinaries struct{}

func (stubBinaries) DeleteTenant(string) (binary.TenantResult, error) {
	return binary.TenantResult{Executions: 2, Files: 3, Bytes: 4096}, nil
}

type stubSessions struct{}

func (stubSessions) ForgetTenant(string) {}

type stubStopper struct{}

func (stubStopper) Deactivated(context.Context, string, string, map[string]string) {}

// The decorators record a call and then hand it to whatever is behind them, so
// the same sequence assertion works over fakes and over the real stores.
type recordedRuns struct {
	inner tenantpurge.RunPurger
	calls *recorder
}

func (r recordedRuns) PurgeTenant(ctx context.Context, tenant repository.TenantScope) (repository.TenantPurgeResult, error) {
	r.calls.add("runs")
	return r.inner.PurgeTenant(ctx, tenant)
}

type recordedRows struct {
	inner tenantpurge.RowPurger
	calls *recorder
}

func (r recordedRows) LockOut(ctx context.Context, tenant repository.TenantScope) (repository.TenantLockOut, error) {
	r.calls.add("lock-out")
	return r.inner.LockOut(ctx, tenant)
}

func (r recordedRows) ActiveTriggerWorkflows(ctx context.Context, tenant repository.TenantScope) ([]string, error) {
	r.calls.add("stop-triggers:list")
	return r.inner.ActiveTriggerWorkflows(ctx, tenant)
}

func (r recordedRows) PurgeTriggers(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	r.calls.add("triggers")
	return r.inner.PurgeTriggers(ctx, tenant)
}

func (r recordedRows) PurgeDefinitions(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	r.calls.add("definitions")
	return r.inner.PurgeDefinitions(ctx, tenant)
}

func (r recordedRows) PurgeVectors(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	r.calls.add("vectors")
	return r.inner.PurgeVectors(ctx, tenant)
}

func (r recordedRows) PurgeIdentity(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	r.calls.add("identity")
	return r.inner.PurgeIdentity(ctx, tenant)
}

type recordedDatastores struct {
	inner tenantpurge.DatastorePurger
	calls *recorder
}

func (r recordedDatastores) PurgeTenant(ctx context.Context, tenantID string) (datastore.PurgeResult, error) {
	r.calls.add("datastores")
	return r.inner.PurgeTenant(ctx, tenantID)
}

type recordedBinaries struct {
	inner tenantpurge.BinaryPurger
	calls *recorder
}

func (r recordedBinaries) DeleteTenant(tenantID string) (binary.TenantResult, error) {
	r.calls.add("binaries")
	return r.inner.DeleteTenant(tenantID)
}

type recordedSessions struct {
	inner tenantpurge.SessionForgetter
	calls *recorder
}

func (r recordedSessions) ForgetTenant(tenantID string) {
	r.calls.add("sessions")
	r.inner.ForgetTenant(tenantID)
}

type recordedStopper struct {
	inner tenantpurge.TriggerStopper
	calls *recorder
}

func (r recordedStopper) Deactivated(ctx context.Context, tenantID, workflowID string, declared map[string]string) {
	r.calls.add("stop-triggers:stop:" + workflowID)
	r.inner.Deactivated(ctx, tenantID, workflowID, declared)
}

// collaborators is one wiring of the orchestrator, so a test can swap a single
// collaborator without rebuilding the rest.
type collaborators struct {
	runs       tenantpurge.RunPurger
	rows       tenantpurge.RowPurger
	datastores tenantpurge.DatastorePurger
	binaries   tenantpurge.BinaryPurger
	sessions   tenantpurge.SessionForgetter
	stopper    tenantpurge.TriggerStopper
	protected  []string
}

func (c collaborators) service(t *testing.T) *tenantpurge.Service {
	t.Helper()
	service, err := tenantpurge.New(tenantpurge.Deps{
		Logger:     discardLogger(),
		Runs:       c.runs,
		Rows:       c.rows,
		Datastores: c.datastores,
		Binaries:   c.binaries,
		Sessions:   c.sessions,
		Triggers:   c.stopper,
		Protected:  c.protected,
	})
	if err != nil {
		t.Fatalf("New() error = %v", err)
	}
	return service
}

// fakes wires the stubs behind the recorders.
func fakes(calls *recorder, rows tenantpurge.RowPurger) collaborators {
	return collaborators{
		runs:       recordedRuns{stubRuns{}, calls},
		rows:       recordedRows{rows, calls},
		datastores: recordedDatastores{stubDatastores{}, calls},
		binaries:   recordedBinaries{stubBinaries{}, calls},
		sessions:   recordedSessions{stubSessions{}, calls},
		stopper:    recordedStopper{stubStopper{}, calls},
	}
}

// The order is the contract: intake is closed before anything is deleted, the
// trace before the definitions that name it, and the tenant row last. An
// order that only holds by accident is an order that breaks when somebody adds
// a step.
func TestPurgeRunsTheStepsInTheDocumentedOrder(t *testing.T) {
	calls := &recorder{}
	service := fakes(calls, stubRows{}).service(t)

	result, err := service.Purge(context.Background(), "acme")
	if err != nil {
		t.Fatalf("Purge() error = %v", err)
	}

	want := documentedCalls("wf-1", "wf-2")
	if got := calls.snapshot(); !slices.Equal(got, want) {
		t.Errorf("Purge() called the collaborators as %v, want the documented order %v", got, want)
	}

	steps := service.Steps()
	names := make([]string, 0, len(steps))
	for _, step := range steps {
		names = append(names, step.Name)
	}
	if !slices.Equal(names, documentedSteps) {
		t.Errorf("Steps() = %v, want %v", names, documentedSteps)
	}

	// Waits precede executions inside the runs step: execution_waits.execution_id
	// is ON DELETE RESTRICT, so an executions-first step fails the whole
	// transaction for any tenant with a suspended run. The store that executes
	// this is pinned by repository's TestPurgeTenantRemovesWaitsBeforeExecutions;
	// what is asserted here is that the step declares that order at all.
	runsIndex := slices.Index(names, "runs")
	if runsIndex < 0 {
		t.Fatalf("Steps() has no runs step: %v", names)
	}
	runs := steps[runsIndex].Tables
	waitsIndex, executionsIndex := slices.Index(runs, "execution_waits"), slices.Index(runs, "executions")
	if waitsIndex < 0 || executionsIndex < 0 {
		t.Fatalf("the runs step declares %v, want both the waits and the executions it deletes", runs)
	}
	if waitsIndex > executionsIndex {
		t.Errorf("the runs step declares %v, so executions would be deleted before the waits that reference them", runs)
	}

	if result.TenantID != "acme" {
		t.Errorf("Purge() reported tenant %q, want acme", result.TenantID)
	}
	if len(result.Removed) != len(service.Tables()) {
		t.Errorf("Purge() reported %d tables, want every covered table (%d): a caller cannot tell 'nothing was there' from 'not covered'", len(result.Removed), len(service.Tables()))
	}
	for table, count := range map[string]int64{
		"tenants": 1, "webhook_deliveries": 3, "workflows": 2, "vector_collections": 1,
		"execution_waits": 1, "datastore_columns": 2,
	} {
		if result.Removed[table] != count {
			t.Errorf("Removed[%q] = %d, want %d", table, result.Removed[table], count)
		}
	}
	if result.DatastoreTables != 2 {
		t.Errorf("DatastoreTables = %d, want the 2 tables the engine reported", result.DatastoreTables)
	}
	if result.Binaries != (binary.TenantResult{Executions: 2, Files: 3, Bytes: 4096}) {
		t.Errorf("Binaries = %+v, want the payload store's own counts", result.Binaries)
	}
}

// An empty tenant id matches nothing, and a purge that matches nothing by
// accident is one typo from a purge that matches everything by accident. The
// operator's own tenant is refused for the same reason: deleting it deletes the
// caller's key, and there is nobody left to recreate it.
func TestPurgeRefusesEmptyWhitespaceAndProtectedTenants(t *testing.T) {
	calls := &recorder{}
	wiring := fakes(calls, stubRows{})
	wiring.protected = []string{repository.OperatorTenantID}
	service := wiring.service(t)

	for _, tenantID := range []string{"", " ", "   ", "\t\n"} {
		if _, err := service.Purge(context.Background(), tenantID); !errors.Is(err, tenantpurge.ErrTenantRequired) {
			t.Errorf("Purge(%q) error = %v, want ErrTenantRequired", tenantID, err)
		}
	}
	for _, tenantID := range []string{repository.OperatorTenantID, " " + repository.OperatorTenantID + " "} {
		if _, err := service.Purge(context.Background(), tenantID); !errors.Is(err, tenantpurge.ErrProtectedTenant) {
			t.Errorf("Purge(%q) error = %v, want ErrProtectedTenant", tenantID, err)
		}
	}
	if len(calls.calls) != 0 {
		t.Errorf("a refused purge called %v, want no collaborator touched", calls.calls)
	}
}

// The orchestrator cannot do its job without the three collaborators that
// delete rows, and a tenant purge that silently skipped one would report
// success over rows it never removed.
func TestNewRefusesAMissingCollaborator(t *testing.T) {
	complete := fakes(&recorder{}, stubRows{})
	for name, mutate := range map[string]func(*collaborators){
		"runs":       func(c *collaborators) { c.runs = nil },
		"rows":       func(c *collaborators) { c.rows = nil },
		"datastores": func(c *collaborators) { c.datastores = nil },
	} {
		t.Run(name, func(t *testing.T) {
			wiring := complete
			mutate(&wiring)
			if _, err := tenantpurge.New(tenantpurge.Deps{
				Logger:     discardLogger(),
				Runs:       wiring.runs,
				Rows:       wiring.rows,
				Datastores: wiring.datastores,
				Binaries:   wiring.binaries,
				Sessions:   wiring.sessions,
				Triggers:   wiring.stopper,
			}); err == nil {
				t.Errorf("New() with no %s = nil error, want a refusal", name)
			}
		})
	}
}

// flakyDefinitions fails the definitions step once. It wraps the real tenant
// purger so the failure lands on a real transaction, over real rows.
type flakyDefinitions struct {
	inner tenantpurge.RowPurger
	fail  bool
}

func (f *flakyDefinitions) LockOut(ctx context.Context, tenant repository.TenantScope) (repository.TenantLockOut, error) {
	return f.inner.LockOut(ctx, tenant)
}

func (f *flakyDefinitions) ActiveTriggerWorkflows(ctx context.Context, tenant repository.TenantScope) ([]string, error) {
	return f.inner.ActiveTriggerWorkflows(ctx, tenant)
}

func (f *flakyDefinitions) PurgeTriggers(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	return f.inner.PurgeTriggers(ctx, tenant)
}

func (f *flakyDefinitions) PurgeDefinitions(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	if f.fail {
		f.fail = false
		return nil, errors.New("tenantpurge test: definitions refused to go")
	}
	return f.inner.PurgeDefinitions(ctx, tenant)
}

func (f *flakyDefinitions) PurgeVectors(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	return f.inner.PurgeVectors(ctx, tenant)
}

func (f *flakyDefinitions) PurgeIdentity(ctx context.Context, tenant repository.TenantScope) (map[string]int64, error) {
	return f.inner.PurgeIdentity(ctx, tenant)
}

// A purge that fails half way must stop where it failed, name the step that
// failed, and leave everything it has not deleted in place — then converge on
// the retry, because a deletion request is retried by an operator and cannot
// need a different argument the second time.
func TestAFailedStepStopsThePurgeAndARetryConverges(t *testing.T) {
	for _, drv := range purgeDialects(t) {
		t.Run(drv.name, func(t *testing.T) {
			db := drv.open(t, "")
			env := newPurgeEnv(t, db, "")
			tenantID := randomTenantID("pt-fail-")
			calls := &recorder{}

			seed := env.seedTenant(tenantID)
			t.Cleanup(func() {
				if _, err := env.service(nil).Purge(context.Background(), tenantID); err != nil {
					t.Errorf("cleanup purge of %s error = %v", tenantID, err)
				}
				dropLeftoverTables(t, db, seed)
			})

			rows := &flakyDefinitions{inner: repository.NewTenantPurger(db.DB), fail: true}
			service := collaborators{
				runs:       recordedRuns{env.executions, calls},
				rows:       recordedRows{rows, calls},
				datastores: recordedDatastores{env.engine, calls},
				binaries:   recordedBinaries{env.files, calls},
				sessions:   recordedSessions{env.memory, calls},
				stopper:    recordedStopper{stubStopper{}, calls},
			}.service(t)

			first, err := service.Purge(context.Background(), tenantID)
			if err == nil {
				t.Fatal("Purge() = nil error, want the seeded definitions failure")
			}
			var stepErr *tenantpurge.StepError
			if !errors.As(err, &stepErr) || stepErr.Step != "definitions" {
				t.Errorf("Purge() error = %v, want a StepError naming the definitions step", err)
			}
			if first.TenantID != tenantID {
				t.Errorf("the partial result names tenant %q, want %q", first.TenantID, tenantID)
			}

			// Everything up to and including the step that fails, and nothing
			// after it: documentedCalls("…") is [lock-out, stop-triggers:list,
			// stop-triggers:stop:<wf>, triggers, binaries, runs, definitions,
			// sessions, datastores, vectors, identity].
			wantFirst := documentedCalls(seed.workflowID)[:7]
			if got := calls.snapshot(); !slices.Equal(got, wantFirst) {
				t.Errorf("the failed purge called %v, want %v and nothing after the failure", got, wantFirst)
			}
			// The steps after the failure did not run, so what they own is
			// still there: the tenant row itself, and the datastore the
			// datastores step would have dropped.
			if count := rawIDCount(t, db, "tenants", "id", tenantID); count != 1 {
				t.Errorf("tenants holds %d rows for the failed tenant, want the row the identity step has not deleted yet", count)
			}
			if !db.Migrator().HasTable(seed.dsTable) {
				t.Errorf("the physical table %s is gone although the datastores step never ran", seed.dsTable)
			}

			second, err := service.Purge(context.Background(), tenantID)
			if err != nil {
				t.Fatalf("Purge(retry) error = %v", err)
			}
			// The retry runs the whole order again and stops no triggers: the
			// first attempt's triggers step deleted the bindings that list is
			// read from, so the tenant has none left to unregister.
			retry := calls.snapshot()[len(wantFirst):]
			if want := documentedCalls(); !slices.Equal(retry, want) {
				t.Errorf("the retry called %v, want the documented order %v", retry, want)
			}
			if second.Removed["tenants"] != 1 {
				t.Errorf("the retry removed %d tenant rows, want 1", second.Removed["tenants"])
			}
			if count := rawIDCount(t, db, "tenants", "id", tenantID); count != 0 {
				t.Errorf("tenants holds %d rows after the retry, want 0", count)
			}
			if db.Migrator().HasTable(seed.dsTable) {
				t.Errorf("the physical table %s survived the retry", seed.dsTable)
			}
		})
	}
}

// A cancelled request stops the purge at the next step boundary — no later
// step is attempted — and says which step it stopped in. The caller detaches
// the purge from the request deadline before calling it; what is asserted here
// is that the boundary exists at all.
func TestPurgeStopsAtAStepBoundaryWhenTheContextIsCancelled(t *testing.T) {
	calls := &recorder{}
	service := fakes(calls, stubRows{}).service(t)

	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := service.Purge(ctx, "acme")
	var stepErr *tenantpurge.StepError
	if !errors.As(err, &stepErr) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Purge(cancelled) error = %v, want a StepError wrapping context.Canceled", err)
	}
	if len(calls.calls) != 0 {
		t.Errorf("a cancelled purge called %v, want no collaborator touched", calls.calls)
	}
}

// dropLeftoverTables removes a physical datastore table a failed run could have
// left behind, so the shared PostgreSQL server does not accumulate ds_ tables.
func dropLeftoverTables(t *testing.T, db *database.DB, seed *tenantSeed) {
	t.Helper()
	identifier := seed.dsTable
	if db.Dialector.Name() == "sqlite" {
		identifier = "`" + identifier + "`"
	} else {
		identifier = `"` + identifier + `"`
	}
	if err := db.Exec("DROP TABLE IF EXISTS " + identifier).Error; err != nil {
		t.Errorf("drop the leftover table %s: %v", seed.dsTable, err)
	}
}
