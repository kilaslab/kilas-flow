package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
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
	// triggers runs webhook registration around activation. Optional: without
	// it a workflow still activates and routes, it simply never tells a remote
	// service where to deliver.
	triggers TriggerCoordinator
	tenants  TenantResolver
	waker    ExecutionWaker
}

// ExecutionWaker lets the lifecycle API notify idle runtime workers after it
// commits a new durable execution request.
type ExecutionWaker interface {
	Wake()
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

// ExecutionRequestResource reports the durable request created by a manual
// run or updated by a cancellation request.
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

// workflowDocumentInput is the editable, canonical document body. Its
// identity is deliberately absent: POST assigns it and PUT takes it from the
// path, so a request can never choose a workflow ID or contradict its URL.
type workflowDocumentInput struct {
	SchemaVersion int                   `json:"schemaVersion"`
	Name          string                `json:"name"`
	Nodes         []workflow.Node       `json:"nodes"`
	Connections   []workflow.Connection `json:"connections"`
	Settings      map[string]any        `json:"settings"`
}

func (input workflowDocumentInput) document(id string) workflow.Document {
	return workflow.Document{
		ID:            id,
		SchemaVersion: input.SchemaVersion,
		Name:          input.Name,
		Nodes:         input.Nodes,
		Connections:   input.Connections,
		Settings:      input.Settings,
	}
}

type createWorkflowInput struct {
	Body workflowDocumentInput
}

type workflowPathInput struct {
	ID string `path:"id" minLength:"1" doc:"Workflow identifier"`
}

type workflowVersionPathInput struct {
	ID        string `path:"id" minLength:"1" doc:"Workflow identifier"`
	VersionID string `path:"versionId" minLength:"1" doc:"Workflow revision identifier"`
}

type workflowVersionOutput struct {
	Body WorkflowVersionResource
}

type updateWorkflowInput struct {
	ID   string `path:"id" minLength:"1" doc:"Workflow identifier"`
	Body workflowDocumentInput
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

// ExecutionNodeRunResource is the externally inspectable trace of one node
// attempt. Payloads were already redacted at the repository boundary.
type ExecutionNodeRunResource struct {
	NodeID     string           `json:"nodeId"`
	Attempt    int              `json:"attempt"`
	RunIndex   int              `json:"runIndex" doc:"The Nth time this node ran in the execution, counting from zero. Distinct from attempt, which counts retries of one run."`
	Sequence   int              `json:"sequence"`
	Status     execution.Status `json:"status"`
	Input      json.RawMessage  `json:"input,omitempty"`
	Output     json.RawMessage  `json:"output,omitempty"`
	Error      json.RawMessage  `json:"error,omitempty"`
	StartedAt  time.Time        `json:"startedAt"`
	FinishedAt *time.Time       `json:"finishedAt,omitempty"`
}

// ExecutionResource surfaces the durable execution state, result, structured
// error, and ordered node-run history through the REST API.
type ExecutionResource struct {
	ID                      string                     `json:"id"`
	WorkflowID              string                     `json:"workflowId"`
	WorkflowVersionID       string                     `json:"workflowVersionId"`
	Status                  execution.Status           `json:"status"`
	Trigger                 execution.Trigger          `json:"trigger"`
	TriggerNodeID           string                     `json:"triggerNodeId,omitempty" doc:"The trigger node this run started from, when one workflow declares several"`
	ParentExecutionID       string                     `json:"parentExecutionId,omitempty" doc:"The execution that called this one, for a sub-workflow run"`
	Input                   json.RawMessage            `json:"input,omitempty"`
	Output                  json.RawMessage            `json:"output,omitempty"`
	Error                   json.RawMessage            `json:"error,omitempty"`
	StartedAt               time.Time                  `json:"startedAt"`
	FinishedAt              *time.Time                 `json:"finishedAt,omitempty"`
	CancellationRequestedAt *time.Time                 `json:"cancellationRequestedAt,omitempty"`
	NodeRuns                []ExecutionNodeRunResource `json:"nodeRuns"`
}

type executionOutput struct {
	Body ExecutionResource
}

// NewWorkflows constructs the lifecycle handler. Missing dependencies are
// answered as service-unavailable rather than causing startup-time panics in
// narrow API tests.
func NewWorkflows(workflows repository.WorkflowRepository, executions repository.ExecutionRepository, catalog workflow.Catalog, tenants TenantResolver, waker ExecutionWaker) *Workflows {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Workflows{
		workflows: workflows, executions: executions, catalog: catalog, tenants: tenants, waker: waker,
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
		OperationID: "get-workflow-version", Method: http.MethodGet, Path: "/workflows/{id}/versions/{versionId}",
		Summary: "Get one workflow revision", Description: "Returns an immutable revision by ID, so an execution inspector can replay the exact graph that ran.", Tags: []string{"Workflows"},
	}, handler.GetVersion)
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

// Create creates a first immutable draft snapshot with a server-owned ID.
func (handler *Workflows) Create(ctx context.Context, input *createWorkflowInput) (*createdWorkflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	document := input.Body.document("")
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

// GetVersion returns one immutable revision by ID.
func (handler *Workflows) GetVersion(ctx context.Context, input *workflowVersionPathInput) (*workflowVersionOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	version, err := handler.workflows.GetVersionByID(ctx, handler.tenant(ctx), input.ID, input.VersionID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowVersionOutput{Body: workflowVersionResource(version)}, nil
}

// Update appends a new immutable draft revision for an existing workflow.
func (handler *Workflows) Update(ctx context.Context, input *updateWorkflowInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	if _, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID); err != nil {
		return nil, handler.problem(err)
	}
	document := input.Body.document(input.ID)
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
func (handler *Workflows) Activate(ctx context.Context, input *workflowPathInput) (*activationOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	stored, err := handler.workflows.Activate(ctx, tenant, input.ID, handler.catalog)
	if err != nil {
		return nil, handler.problem(err)
	}

	// Lifecycle hooks run after the commit, never inside it. A remote call can
	// block for seconds against somebody else's API while holding a row lock,
	// and it cannot be rolled back — un-calling setWebhook is another network
	// call. The brief window where the workflow is active but the service has
	// not been told is harmless: an unregistered webhook delivers nothing.
	var notices []webhook.Notice
	if handler.triggers != nil {
		activationNotices, err := handler.triggers.Activated(ctx, tenant.ID, stored.ID, handler.lifecycleIDs())
		if err != nil {
			// Half-registered is worse than inactive, because the user believes
			// the workflow is listening.
			if _, deactivateErr := handler.workflows.Deactivate(ctx, tenant, stored.ID); deactivateErr != nil {
				return nil, huma.Error500InternalServerError(
					"a trigger could not register and the workflow could not be rolled back", err)
			}
			return nil, huma.Error502BadGateway(err.Error())
		}
		notices = activationNotices
	}
	// The workflow is active either way. A notice is a thing the user now has
	// to do — paste a URL into someone else's console — and an activation that
	// only said "active" would leave a trigger that receives nothing looking
	// exactly like one that is listening.
	return &activationOutput{Body: activationResource(stored, notices)}, nil
}

// ActivationResource is a workflow plus anything activation could not do for
// the user.
type ActivationResource struct {
	WorkflowResource
	// Notices are empty for a workflow whose triggers need nothing.
	Notices []ActivationNotice `json:"notices"`
}

// ActivationNotice names one trigger and what it needs.
type ActivationNotice struct {
	NodeID   string `json:"nodeId"`
	NodeType string `json:"nodeType"`
	Message  string `json:"message"`
}

type activationOutput struct {
	Body ActivationResource
}

func activationResource(stored workflow.StoredWorkflow, notices []webhook.Notice) ActivationResource {
	resource := ActivationResource{WorkflowResource: workflowResource(stored), Notices: []ActivationNotice{}}
	for _, notice := range notices {
		resource.Notices = append(resource.Notices, ActivationNotice{
			NodeID: notice.NodeID, NodeType: notice.NodeType, Message: notice.Message,
		})
	}
	return resource
}

// Deactivate is idempotent and retains the previously pinned active revision.
func (handler *Workflows) Deactivate(ctx context.Context, input *workflowPathInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	// Unregistering runs *before* the bindings are dropped, because the hook
	// needs the route to tell the service which registration to remove.
	if handler.triggers != nil {
		handler.triggers.Deactivated(ctx, tenant.ID, input.ID, handler.lifecycleIDs())
	}
	stored, err := handler.workflows.Deactivate(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// lifecycleIDs maps a trigger node type to the lifecycle hook it declares, read
// from the catalogue so no node type name appears here.
func (handler *Workflows) lifecycleIDs() map[string]string {
	declared := map[string]string{}
	lister, ok := handler.catalog.(interface{ List() []node.Definition })
	if !ok {
		return declared
	}
	for _, definition := range lister.List() {
		if definition.LifecycleID != "" {
			declared[definition.Type] = definition.LifecycleID
		}
	}
	return declared
}

// Run validates the latest draft and records a queued manual request. The
// engine consumes queued records in the dependent execution-runtime ticket.
func (handler *Workflows) Run(ctx context.Context, input *runWorkflowInput) (*executionRequestOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	var payload json.RawMessage
	if input.Body != nil {
		payload = input.Body.Input
	}
	created, err := handler.executions.QueueManualLatest(ctx, handler.tenant(ctx), input.ID, handler.catalog, payload)
	if err != nil {
		return nil, handler.problem(err)
	}
	if handler.waker != nil {
		handler.waker.Wake()
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
		Status: record.Status, Trigger: record.Trigger, Input: execution.Redact(record.Input), CreatedAt: record.StartedAt,
	}
}

// executionSummaryResource is the compact history row. Duration is computed
// here rather than stored so it stays consistent with the timestamps a client
// can already see.
func executionSummaryResource(record execution.Record) ExecutionSummary {
	summary := ExecutionSummary{
		ID: record.ID, WorkflowID: record.WorkflowID, WorkflowVersionID: record.WorkflowVersionID,
		Status: record.Status, Trigger: record.Trigger, TriggerNodeID: record.TriggerNodeID,
		ParentExecutionID: record.ParentExecutionID,
		StartedAt:         record.StartedAt, FinishedAt: record.FinishedAt,
	}
	if record.FinishedAt != nil {
		duration := record.FinishedAt.Sub(record.StartedAt).Milliseconds()
		summary.DurationMs = &duration
	}
	return summary
}

func executionResource(record execution.Record) ExecutionResource {
	resource := ExecutionResource{
		ID: record.ID, WorkflowID: record.WorkflowID, WorkflowVersionID: record.WorkflowVersionID,
		Status: record.Status, Trigger: record.Trigger, TriggerNodeID: record.TriggerNodeID,
		ParentExecutionID: record.ParentExecutionID,
		// Redacting again on the way out keeps the guarantee even for records
		// written before this boundary existed, or by a future in-memory path
		// that never touched durable storage.
		Input: execution.Redact(record.Input), Output: execution.Redact(record.Output),
		Error: execution.Redact(record.Error), StartedAt: record.StartedAt, FinishedAt: record.FinishedAt,
		CancellationRequestedAt: record.CancellationRequestedAt,
		NodeRuns:                make([]ExecutionNodeRunResource, 0, len(record.NodeRuns)),
	}
	for _, nodeRun := range record.NodeRuns {
		resource.NodeRuns = append(resource.NodeRuns, ExecutionNodeRunResource{
			NodeID: nodeRun.NodeID, Attempt: nodeRun.Attempt, RunIndex: nodeRun.RunIndex, Sequence: nodeRun.Sequence,
			Status: nodeRun.Status, Input: execution.Redact(nodeRun.Input),
			Output: execution.Redact(nodeRun.Output), Error: execution.Redact(nodeRun.Error),
			StartedAt: nodeRun.StartedAt, FinishedAt: nodeRun.FinishedAt,
		})
	}
	return resource
}

// TriggerCoordinator registers and unregisters a workflow's triggers with the
// remote services they depend on.
//
// Declared here rather than taking *webhook.Coordinator so the handler package
// does not depend on the webhook package, and so a test can supply one.
type TriggerCoordinator interface {
	Activated(ctx context.Context, tenantID, workflowID string, declared map[string]string) ([]webhook.Notice, error)
	Deactivated(ctx context.Context, tenantID, workflowID string, declared map[string]string)
}

// WithTriggers attaches the lifecycle coordinator.
func (handler *Workflows) WithTriggers(coordinator TriggerCoordinator) *Workflows {
	handler.triggers = coordinator
	return handler
}
