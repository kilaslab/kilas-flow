package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// TenantResolver isolates the temporary standalone tenant from future auth or
// embed-session resolution.
type TenantResolver interface {
	Resolve(context.Context) repository.TenantScope
}

type defaultTenantResolver struct{}

func (defaultTenantResolver) Resolve(context.Context) repository.TenantScope {
	return repository.TenantScope{ID: repository.DefaultTenantID}
}

// Workflows provides the REST lifecycle surface over canonical workflow JSON.
type Workflows struct {
	workflows  repository.WorkflowRepository
	executions repository.ExecutionRepository
	catalog    workflow.Catalog
	tenants    TenantResolver
}

// WorkflowVersionResource is one persisted canonical document snapshot. It
// deliberately omits tenant ownership from the public response.
type WorkflowVersionResource struct {
	ID            string            `json:"id"`
	WorkflowID    string            `json:"workflowId"`
	Revision      int               `json:"revision"`
	SchemaVersion int               `json:"schemaVersion"`
	Document      workflow.Document `json:"document"`
	CreatedAt     time.Time         `json:"createdAt"`
}

// WorkflowResource is the authoritative workflow representation returned by
// create, get, update, activation, and deactivation operations.
type WorkflowResource struct {
	ID            string                   `json:"id"`
	Name          string                   `json:"name"`
	Active        bool                     `json:"active"`
	LatestVersion WorkflowVersionResource  `json:"latestVersion"`
	ActiveVersion *WorkflowVersionResource `json:"activeVersion,omitempty"`
	CreatedAt     time.Time                `json:"createdAt"`
	UpdatedAt     time.Time                `json:"updatedAt"`
}

// WorkflowSummary is the compact list representation used by the dashboard.
type WorkflowSummary struct {
	ID             string    `json:"id"`
	Name           string    `json:"name"`
	Active         bool      `json:"active"`
	LatestRevision int       `json:"latestRevision"`
	UpdatedAt      time.Time `json:"updatedAt"`
}

// ExecutionRequestResource reports the persisted request created by a manual
// run. The engine processes it in the subsequent runtime milestone.
type ExecutionRequestResource struct {
	ID                string            `json:"id"`
	WorkflowID        string            `json:"workflowId"`
	WorkflowVersionID string            `json:"workflowVersionId"`
	Status            execution.Status  `json:"status"`
	Trigger           execution.Trigger `json:"trigger"`
	Input             json.RawMessage   `json:"input,omitempty"`
	CreatedAt         time.Time         `json:"createdAt"`
}

// WorkflowValidationIssue is attached to an RFC 9457 error detail's value so
// API clients can map compiler failures back to graph elements without parsing
// a human-readable message.
type WorkflowValidationIssue struct {
	Code         workflow.ErrorCode `json:"code"`
	NodeID       string             `json:"nodeId,omitempty"`
	ConnectionID string             `json:"connectionId,omitempty"`
}

type createWorkflowInput struct {
	Body workflow.Document
}

type workflowPathInput struct {
	ID string `path:"id" minLength:"1" doc:"Workflow identifier"`
}

type updateWorkflowInput struct {
	ID   string `path:"id" minLength:"1" doc:"Workflow identifier"`
	Body workflow.Document
}

type runWorkflowInput struct {
	ID   string `path:"id" minLength:"1" doc:"Workflow identifier"`
	Body *struct {
		Input json.RawMessage `json:"input,omitempty" doc:"Optional manual-run input JSON"`
	}
}

type workflowOutput struct {
	Body WorkflowResource
}

type createdWorkflowOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     WorkflowResource
}

type workflowListOutput struct {
	Body []WorkflowSummary
}

type deletedWorkflowOutput struct {
	Status int `status:"204"`
}

type executionRequestOutput struct {
	Status int `status:"202"`
	Body   ExecutionRequestResource
}

// NewWorkflows constructs the lifecycle handler. Missing dependencies are
// answered as service-unavailable rather than causing startup-time panics in
// narrow API tests.
func NewWorkflows(workflows repository.WorkflowRepository, executions repository.ExecutionRepository, catalog workflow.Catalog, tenants TenantResolver) *Workflows {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Workflows{
		workflows: workflows, executions: executions, catalog: catalog, tenants: tenants,
	}
}

// Register wires the documented workflow CRUD and lifecycle operations.
func (handler *Workflows) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-workflow", Method: http.MethodPost, Path: "/workflows", DefaultStatus: http.StatusCreated,
		Summary: "Create a workflow draft", Description: "Creates revision 1 of a canonical workflow document.", Tags: []string{"Workflows"},
	}, handler.Create)
	huma.Register(api, huma.Operation{
		OperationID: "list-workflows", Method: http.MethodGet, Path: "/workflows",
		Summary: "List workflows", Description: "Lists workflows visible to the current tenant.", Tags: []string{"Workflows"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "get-workflow", Method: http.MethodGet, Path: "/workflows/{id}",
		Summary: "Get a workflow", Description: "Returns the current canonical document and lifecycle metadata.", Tags: []string{"Workflows"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "update-workflow", Method: http.MethodPut, Path: "/workflows/{id}",
		Summary: "Save a workflow draft", Description: "Appends an immutable revision, including incomplete drafts.", Tags: []string{"Workflows"},
	}, handler.Update)
	huma.Register(api, huma.Operation{
		OperationID: "delete-workflow", Method: http.MethodDelete, Path: "/workflows/{id}",
		Summary: "Delete a workflow", Description: "Soft-deletes a workflow while retaining audit evidence.", Tags: []string{"Workflows"},
	}, handler.Delete)
	huma.Register(api, huma.Operation{
		OperationID: "activate-workflow", Method: http.MethodPost, Path: "/workflows/{id}/activate",
		Summary: "Activate latest workflow revision", Description: "Compiles the latest revision before pinning it as active.", Tags: []string{"Workflow lifecycle"},
	}, handler.Activate)
	huma.Register(api, huma.Operation{
		OperationID: "deactivate-workflow", Method: http.MethodPost, Path: "/workflows/{id}/deactivate",
		Summary: "Deactivate workflow", Description: "Idempotently disables trigger execution while retaining published history.", Tags: []string{"Workflow lifecycle"},
	}, handler.Deactivate)
	huma.Register(api, huma.Operation{
		OperationID: "run-workflow", Method: http.MethodPost, Path: "/workflows/{id}/run", DefaultStatus: http.StatusAccepted,
		Summary: "Queue a manual workflow run", Description: "Validates and queues the latest saved revision without requiring activation.", Tags: []string{"Workflow lifecycle"},
	}, handler.Run)
}

// Create creates a first immutable draft snapshot. The server owns the
// workflow identifier even if a client included one in the request payload.
func (handler *Workflows) Create(ctx context.Context, input *createWorkflowInput) (*createdWorkflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	document := input.Body
	document.ID = ""
	if err := workflow.ValidateDraftWithServerID(document); err != nil {
		return nil, draftProblem(err)
	}
	stored, err := handler.workflows.SaveDraft(ctx, handler.tenant(ctx), document)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &createdWorkflowOutput{
		Status: http.StatusCreated, Location: "/api/v1/workflows/" + stored.ID, Body: workflowResource(stored),
	}, nil
}

// List returns tenant-scoped workflow summaries.
func (handler *Workflows) List(ctx context.Context, _ *struct{}) (*workflowListOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.List(ctx, handler.tenant(ctx))
	if err != nil {
		return nil, handler.problem(err)
	}
	items := make([]WorkflowSummary, 0, len(stored))
	for _, item := range stored {
		items = append(items, WorkflowSummary{
			ID: item.ID, Name: item.Name, Active: item.Active, LatestRevision: item.LatestVersion.Revision, UpdatedAt: item.UpdatedAt,
		})
	}
	return &workflowListOutput{Body: items}, nil
}

// Get returns the stored canonical document and lifecycle state.
func (handler *Workflows) Get(ctx context.Context, input *workflowPathInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// Update appends a new immutable draft revision for an existing workflow.
func (handler *Workflows) Update(ctx context.Context, input *updateWorkflowInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	if _, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID); err != nil {
		return nil, handler.problem(err)
	}
	document := input.Body
	if document.ID != "" && document.ID != input.ID {
		return nil, draftProblem(errors.New("workflow document id must match the path"))
	}
	document.ID = input.ID
	if err := workflow.ValidateDraft(document); err != nil {
		return nil, draftProblem(err)
	}
	stored, err := handler.workflows.SaveDraft(ctx, handler.tenant(ctx), document)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// Delete removes a workflow from future tenant-scoped reads.
func (handler *Workflows) Delete(ctx context.Context, input *workflowPathInput) (*deletedWorkflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	if err := handler.workflows.Delete(ctx, handler.tenant(ctx), input.ID); err != nil {
		return nil, handler.problem(err)
	}
	return &deletedWorkflowOutput{Status: http.StatusNoContent}, nil
}

// Activate validates and pins the latest saved revision.
func (handler *Workflows) Activate(ctx context.Context, input *workflowPathInput) (*workflowOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Activate(ctx, handler.tenant(ctx), input.ID, handler.catalog)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// Deactivate is idempotent and retains the previously pinned active revision.
func (handler *Workflows) Deactivate(ctx context.Context, input *workflowPathInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Deactivate(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// Run validates the latest draft and records a queued manual request. The
// engine consumes queued records in the dependent execution-runtime ticket.
func (handler *Workflows) Run(ctx context.Context, input *runWorkflowInput) (*executionRequestOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	stored, err := handler.workflows.Get(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	if _, err := workflow.Compile(stored.LatestVersion.Document, handler.catalog); err != nil {
		return nil, handler.problem(err)
	}
	var payload json.RawMessage
	if input.Body != nil {
		payload = input.Body.Input
	}
	created, err := handler.executions.Create(ctx, tenant, execution.Record{
		WorkflowID: stored.ID, WorkflowVersionID: stored.LatestVersion.ID,
		Status: execution.StatusQueued, Trigger: execution.TriggerManual, Input: payload,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	return &executionRequestOutput{Status: http.StatusAccepted, Body: executionRequestResource(created)}, nil
}

func (handler *Workflows) tenant(ctx context.Context) repository.TenantScope {
	return handler.tenants.Resolve(ctx)
}

func (handler *Workflows) available(needsExecution bool) error {
	if handler.workflows == nil || (needsExecution && (handler.executions == nil || handler.catalog == nil)) {
		return huma.Error503ServiceUnavailable("workflow service unavailable")
	}
	return nil
}

func (handler *Workflows) problem(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("workflow not found")
	}
	var validation *workflow.ValidationErrors
	if errors.As(err, &validation) {
		return compileProblem(validation)
	}
	return huma.Error500InternalServerError("workflow operation failed")
}

func draftProblem(err error) error {
	return &huma.ErrorModel{
		Status: http.StatusUnprocessableEntity,
		Detail: "workflow draft is invalid",
		Errors: []*huma.ErrorDetail{{Message: err.Error(), Location: "body"}},
	}
}

func compileProblem(validation *workflow.ValidationErrors) error {
	problem := &huma.ErrorModel{Status: http.StatusUnprocessableEntity, Detail: "workflow validation failed"}
	for _, issue := range validation.Issues {
		problem.Errors = append(problem.Errors, &huma.ErrorDetail{
			Message:  issue.Message,
			Location: "body" + issue.Path,
			Value: WorkflowValidationIssue{
				Code: issue.Code, NodeID: issue.NodeID, ConnectionID: issue.ConnectionID,
			},
		})
	}
	return problem
}

func workflowResource(stored workflow.StoredWorkflow) WorkflowResource {
	resource := WorkflowResource{
		ID: stored.ID, Name: stored.Name, Active: stored.Active,
		LatestVersion: workflowVersionResource(stored.LatestVersion), CreatedAt: stored.CreatedAt, UpdatedAt: stored.UpdatedAt,
	}
	if stored.ActiveVersion != nil {
		version := workflowVersionResource(*stored.ActiveVersion)
		resource.ActiveVersion = &version
	}
	return resource
}

func workflowVersionResource(version workflow.Version) WorkflowVersionResource {
	return WorkflowVersionResource{
		ID: version.ID, WorkflowID: version.WorkflowID, Revision: version.Revision,
		SchemaVersion: version.SchemaVersion, Document: version.Document, CreatedAt: version.CreatedAt,
	}
}

func executionRequestResource(record execution.Record) ExecutionRequestResource {
	return ExecutionRequestResource{
		ID: record.ID, WorkflowID: record.WorkflowID, WorkflowVersionID: record.WorkflowVersionID,
		Status: record.Status, Trigger: record.Trigger, Input: record.Input, CreatedAt: record.StartedAt,
	}
}
