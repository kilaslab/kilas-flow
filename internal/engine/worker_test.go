package engine_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

func TestServiceStartLaunchesConfiguredLocalWorkers(t *testing.T) {
	store := &idleExecutionStore{claimed: make(chan string, 2)}
	service, err := engine.NewService(engine.ServiceDeps{
		Executions:     store,
		Catalog:        idleCatalog{},
		Runner:         engine.NewRunner(engine.NewRegistry()),
		WorkerID:       "worker",
		DefaultTimeout: time.Second,
	})
	if err != nil {
		t.Fatalf("NewService() error = %v", err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	if err := service.Start(ctx, 2); err != nil {
		t.Fatalf("Start() error = %v", err)
	}
	workers := map[string]bool{}
	for len(workers) < 2 {
		select {
		case workerID := <-store.claimed:
			workers[workerID] = true
		case <-time.After(500 * time.Millisecond):
			t.Fatalf("workers claimed = %#v, want two local workers", workers)
		}
	}
}

type idleCatalog struct{}

func (idleCatalog) Lookup(string, workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	return workflow.NodeDefinition{}, false
}

type idleExecutionStore struct {
	claimed chan string
}

func (store *idleExecutionStore) ClaimNext(_ context.Context, workerID string, _ time.Time) (execution.Record, workflow.Document, bool, error) {
	store.claimed <- workerID
	return execution.Record{}, workflow.Document{}, false, nil
}

func (*idleExecutionStore) Get(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return execution.Record{}, nil
}

// This test only exercises worker startup, so the trigger queue is a stub.
func (*idleExecutionStore) QueueTriggered(context.Context, repository.TenantScope, string, string, execution.Trigger, string, json.RawMessage) (execution.Record, error) {
	return execution.Record{}, nil
}

func (*idleExecutionStore) UpdateRuntime(context.Context, repository.TenantScope, execution.Record) (execution.Record, error) {
	return execution.Record{}, nil
}

func (*idleExecutionStore) CreateNodeRun(context.Context, repository.TenantScope, execution.NodeRun) (execution.NodeRun, error) {
	return execution.NodeRun{}, nil
}

// This test only exercises worker startup, so the trace batch is a stub.
func (*idleExecutionStore) CreateNodeRuns(_ context.Context, _ repository.TenantScope, runs []execution.NodeRun) ([]execution.NodeRun, error) {
	return runs, nil
}

// Nothing is ever claimed for long enough to renew, and this test never
// cancels, so both are stubs.
func (*idleExecutionStore) ExtendLease(context.Context, repository.TenantScope, string, string, time.Time) (bool, error) {
	return true, nil
}

func (*idleExecutionStore) ExecutionState(context.Context, repository.TenantScope, string) (execution.Status, bool, error) {
	return execution.StatusQueued, false, nil
}

// This test only exercises worker startup, so sub-workflow calls are a stub.
func (*idleExecutionStore) StartChild(context.Context, repository.TenantScope, repository.ChildExecution) (execution.Record, workflow.Document, error) {
	return execution.Record{}, workflow.Document{}, nil
}

func (*idleExecutionStore) Cancel(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return execution.Record{}, nil
}

// This test never suspends, so the durable wait surface is a stub.
func (*idleExecutionStore) SuspendExecution(context.Context, repository.TenantScope, repository.SuspendWaitParams) (repository.Wait, execution.Record, error) {
	return repository.Wait{}, execution.Record{}, nil
}

func (*idleExecutionStore) FindWaitByToken(context.Context, string) (repository.Wait, error) {
	return repository.Wait{}, nil
}

func (*idleExecutionStore) FindActiveWait(context.Context, repository.TenantScope, string) (repository.Wait, error) {
	return repository.Wait{}, nil
}

func (*idleExecutionStore) ResumeWait(context.Context, string, string, json.RawMessage, time.Time) (repository.Wait, execution.Record, error) {
	return repository.Wait{}, execution.Record{}, nil
}

func (*idleExecutionStore) SettleExpiredWait(context.Context, uint, repository.ExpiredResolution, time.Time) (repository.Wait, execution.Record, error) {
	return repository.Wait{}, execution.Record{}, nil
}

func (*idleExecutionStore) LoadResumeState(context.Context, repository.TenantScope, string) (repository.Wait, error) {
	return repository.Wait{}, nil
}

func (*idleExecutionStore) ListExpiredWaits(context.Context, time.Time, int) ([]repository.Wait, error) {
	return nil, nil
}
