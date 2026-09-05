package handlers

import (
	"context"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// ExecutionController is the narrow runtime service surface exposed to HTTP.
// The concrete worker remains independent from routing and its storage layer.
type ExecutionController interface {
	ExecutionWaker
	Get(context.Context, repository.TenantScope, string) (execution.Record, error)
	Cancel(context.Context, repository.TenantScope, string) (execution.Record, error)
}

// Executions provides user-requested lifecycle controls for durable runs.
type Executions struct {
	controller ExecutionController
	tenants    TenantResolver
}

type executionPathInput struct {
	ID string `path:"id" minLength:"1" doc:"Execution identifier"`
}

// NewExecutions constructs the execution control handler.
func NewExecutions(controller ExecutionController, tenants TenantResolver) *Executions {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Executions{controller: controller, tenants: tenants}
}

// Register wires execution controls that need the live runtime service.
func (handler *Executions) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "get-execution", Method: http.MethodGet, Path: "/executions/{id}",
		Summary: "Get a workflow execution", Description: "Returns durable execution state and its ordered node-run trace.", Tags: []string{"Executions"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "cancel-execution", Method: http.MethodPost, Path: "/executions/{id}/cancel", DefaultStatus: http.StatusAccepted,
		Summary: "Cancel a workflow execution", Description: "Requests cancellation of queued or running work.", Tags: []string{"Executions"},
	}, handler.Cancel)
}

// Get returns a tenant-scoped execution record, including its persisted trace.
func (handler *Executions) Get(ctx context.Context, input *executionPathInput) (*executionOutput, error) {
	if handler.controller == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	record, err := handler.controller.Get(ctx, handler.tenants.Resolve(ctx), input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("execution lookup failed")
	}
	return &executionOutput{Body: executionResource(record)}, nil
}

// Cancel persists a cancellation request and interrupts a local worker when
// that worker owns the execution.
func (handler *Executions) Cancel(ctx context.Context, input *executionPathInput) (*executionRequestOutput, error) {
	if handler.controller == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	record, err := handler.controller.Cancel(ctx, handler.tenants.Resolve(ctx), input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, huma.Error500InternalServerError("execution cancellation failed")
	}
	return &executionRequestOutput{Status: http.StatusAccepted, Body: executionRequestResource(record)}, nil
}
