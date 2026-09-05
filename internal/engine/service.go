package engine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ExecutionStore is the persistence seam used by the durable runtime service.
// Implementations own transactions and ORM details; engine depends only on
// these repository-shaped operations.
type ExecutionStore interface {
	ClaimNext(context.Context, string, time.Time) (execution.Record, workflow.Document, bool, error)
	Get(context.Context, repository.TenantScope, string) (execution.Record, error)
	UpdateRuntime(context.Context, repository.TenantScope, execution.Record) (execution.Record, error)
	CreateNodeRun(context.Context, repository.TenantScope, execution.NodeRun) (execution.NodeRun, error)
	Cancel(context.Context, repository.TenantScope, string) (execution.Record, error)
}

// CredentialStore resolves a stored credential for the tenant that owns the
// running execution.
type CredentialStore interface {
	Resolve(context.Context, repository.TenantScope, string) (credentials.Record, map[string]string, error)
}

// ServiceDeps configures one local durable execution worker.
type ServiceDeps struct {
	Executions  ExecutionStore
	Catalog     workflow.Catalog
	Runner      *Runner
	Credentials CredentialStore
	// Environment is the allowlisted `$env` map. The service never reads the
	// process environment itself, so what a workflow can see is decided once,
	// at composition.
	Environment    map[string]string
	WorkerID       string
	DefaultTimeout time.Duration
}

// Service claims queued execution records and persists their deterministic
// runtime results. It is deliberately transport-independent.
type Service struct {
	executions     ExecutionStore
	catalog        workflow.Catalog
	runner         *Runner
	credentials    CredentialStore
	environment    map[string]string
	workerID       string
	defaultTimeout time.Duration
	activeMu       sync.Mutex
	active         map[string]context.CancelFunc
	startOnce      sync.Once
	wake           chan struct{}
}

func NewService(deps ServiceDeps) (*Service, error) {
	if deps.Executions == nil || deps.Catalog == nil || deps.Runner == nil || deps.WorkerID == "" {
		return nil, fmt.Errorf("engine executions, catalog, runner, and worker ID are required")
	}
	if deps.DefaultTimeout <= 0 {
		return nil, fmt.Errorf("engine default timeout must be positive")
	}
	environment := make(map[string]string, len(deps.Environment))
	for key, value := range deps.Environment {
		environment[key] = value
	}
	return &Service{
		executions:     deps.Executions,
		catalog:        deps.Catalog,
		runner:         deps.Runner,
		credentials:    deps.Credentials,
		environment:    environment,
		workerID:       deps.WorkerID,
		defaultTimeout: deps.DefaultTimeout,
		active:         make(map[string]context.CancelFunc),
		wake:           make(chan struct{}, 1),
	}, nil
}

// RunOnce claims and completes at most one queued execution. It is exported so
// the application worker loop and deterministic integration tests share the
// exact same processing path.
func (service *Service) RunOnce(ctx context.Context) (bool, error) {
	return service.runOnce(ctx, service.workerID)
}

// Get reads the durable execution record and its deterministic node-run log.
func (service *Service) Get(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error) {
	return service.executions.Get(ctx, tenant, executionID)
}

func (service *Service) runOnce(ctx context.Context, workerID string) (bool, error) {
	leaseUntil := time.Now().UTC().Add(service.defaultTimeout)
	record, document, claimed, err := service.executions.ClaimNext(ctx, workerID, leaseUntil)
	if err != nil || !claimed {
		return claimed, err
	}
	tenant := repository.TenantScope{ID: record.TenantID}
	if record.Status == execution.StatusCancelling {
		now := time.Now().UTC()
		record.Status = execution.StatusCancelled
		record.Output = json.RawMessage("null")
		record.Error = structuredError("execution.cancelled", errors.New("execution cancellation was recovered after its worker lease expired"))
		record.FinishedAt = &now
		if _, err := service.executions.UpdateRuntime(ctx, tenant, record); err != nil {
			return true, err
		}
		return true, nil
	}
	runCtx, cancel := context.WithTimeout(ctx, service.defaultTimeout)
	service.activeMu.Lock()
	service.active[record.ID] = cancel
	service.activeMu.Unlock()
	result, runErr := service.run(runCtx, record, document)
	service.activeMu.Lock()
	delete(service.active, record.ID)
	service.activeMu.Unlock()
	cancel()
	for sequence, run := range result.NodeRuns {
		input, err := json.Marshal(run.Input)
		if err != nil {
			return true, fmt.Errorf("marshal node %q input: %w", run.NodeID, err)
		}
		output, err := json.Marshal(run.Output)
		if err != nil {
			return true, fmt.Errorf("marshal node %q output: %w", run.NodeID, err)
		}
		status := execution.StatusSucceeded
		var errorPayload json.RawMessage
		if run.Error != nil {
			status = execution.StatusFailed
			if errors.Is(run.Error, context.Canceled) {
				status = execution.StatusCancelled
				if run.ErrorCode == "" || run.ErrorCode == "node.failed" {
					run.ErrorCode = "execution.cancelled"
				}
			}
			errorPayload = structuredError(run.ErrorCode, run.Error)
		}
		now := time.Now().UTC()
		if _, err := service.executions.CreateNodeRun(ctx, tenant, execution.NodeRun{
			TenantID: record.TenantID, ExecutionID: record.ID, NodeID: run.NodeID, Attempt: 1, Sequence: sequence + 1,
			Status: status, Input: input, Output: output, Error: errorPayload, StartedAt: now, FinishedAt: &now, LeaseOwner: record.LeaseOwner,
		}); err != nil {
			return true, fmt.Errorf("persist node %q run: %w", run.NodeID, err)
		}
	}
	finishedAt := time.Now().UTC()
	if runErr != nil {
		code := "execution.failed"
		status := execution.StatusFailed
		if errors.Is(runErr, context.Canceled) {
			code = "execution.cancelled"
			status = execution.StatusCancelled
		}
		for _, run := range result.NodeRuns {
			if run.ErrorCode == "execution.timeout" {
				code = "execution.timeout"
				break
			}
		}
		record.Status = status
		record.Error = structuredError(code, runErr)
		record.FinishedAt = &finishedAt
		_, updateErr := service.executions.UpdateRuntime(ctx, tenant, record)
		if updateErr != nil {
			return true, updateErr
		}
		return true, nil
	}
	output, err := json.Marshal(result.Output)
	if err != nil {
		return true, fmt.Errorf("marshal execution output: %w", err)
	}
	record.Status = execution.StatusSucceeded
	record.Output = output
	record.Error = json.RawMessage("null")
	record.FinishedAt = &finishedAt
	if _, err := service.executions.UpdateRuntime(ctx, tenant, record); err != nil {
		return true, err
	}
	return true, nil
}

// Start launches a bounded process-local worker pool. Every worker claims
// records from the durable repository, so queued work remains recoverable
// after a process restart instead of being tied to an HTTP request goroutine.
func (service *Service) Start(ctx context.Context, maxConcurrent int) error {
	if maxConcurrent < 1 {
		return fmt.Errorf("engine max concurrent executions must be positive")
	}
	service.startOnce.Do(func() {
		for index := 1; index <= maxConcurrent; index++ {
			workerID := fmt.Sprintf("%s-%d", service.workerID, index)
			go service.worker(ctx, workerID)
		}
	})
	return nil
}

func (service *Service) worker(ctx context.Context, workerID string) {
	for {
		worked, _ := service.runOnce(ctx, workerID)
		if ctx.Err() != nil {
			return
		}
		if worked {
			continue
		}
		select {
		case <-ctx.Done():
			return
		case <-service.wake:
		case <-time.After(100 * time.Millisecond):
		}
	}
}

// Wake asks idle workers to poll immediately after an API queues work.
func (service *Service) Wake() {
	select {
	case service.wake <- struct{}{}:
	default:
	}
}

func (service *Service) run(ctx context.Context, record execution.Record, document workflow.Document) (Result, error) {
	ir, err := workflow.Compile(document, service.catalog)
	if err != nil {
		return Result{}, err
	}
	item, err := inputItem(record.Input)
	if err != nil {
		return Result{}, err
	}
	return service.runner.Run(ctx, ir, Request{
		Input:       item,
		Execution:   ExecutionContext{ID: record.ID, Mode: string(record.Trigger)},
		Env:         service.environment,
		Credentials: &tenantCredentials{store: service.credentials, tenant: repository.TenantScope{ID: record.TenantID}},
	})
}

// tenantCredentials binds credential resolution to the tenant that owns the
// running execution, so a workflow can never name a credential from another
// tenant even if it guesses the ID.
type tenantCredentials struct {
	store  CredentialStore
	tenant repository.TenantScope
}

func (resolver *tenantCredentials) ResolveCredential(ctx context.Context, credentialID string) (Credential, error) {
	if resolver == nil || resolver.store == nil {
		return Credential{}, fmt.Errorf("credential storage is not configured")
	}
	record, fields, err := resolver.store.Resolve(ctx, resolver.tenant, credentialID)
	if err != nil {
		return Credential{}, fmt.Errorf("resolve credential: %w", err)
	}
	return Credential{
		ID: record.ID, Name: record.Name, Type: record.Type,
		Fields: fields, AllowedDomains: record.AllowedDomains,
	}, nil
}

// Cancel requests durable cancellation and interrupts the matching in-process
// worker immediately when it is currently running on this instance.
func (service *Service) Cancel(ctx context.Context, tenant repository.TenantScope, executionID string) (execution.Record, error) {
	record, err := service.executions.Cancel(ctx, tenant, executionID)
	if err != nil {
		return execution.Record{}, err
	}
	if record.Status == execution.StatusCancelling {
		service.activeMu.Lock()
		cancel := service.active[executionID]
		service.activeMu.Unlock()
		if cancel != nil {
			cancel()
		}
	}
	return record, nil
}

func inputItem(payload json.RawMessage) (workflow.Item, error) {
	if len(payload) == 0 || string(payload) == "null" {
		return workflow.Item{JSON: map[string]any{}}, nil
	}
	var input map[string]any
	if err := json.Unmarshal(payload, &input); err != nil {
		return workflow.Item{}, fmt.Errorf("manual input must be a JSON object: %w", err)
	}
	if input == nil {
		input = map[string]any{}
	}
	return workflow.Item{JSON: input}, nil
}

func structuredError(code string, err error) json.RawMessage {
	payload, marshalErr := json.Marshal(map[string]string{"code": code, "message": err.Error()})
	if marshalErr != nil {
		return json.RawMessage(`{"code":"execution.failed","message":"execution failed"}`)
	}
	return payload
}
