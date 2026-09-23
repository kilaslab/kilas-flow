package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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
	// sessions drops a workflow's retained agent conversations on delete.
	// Optional: without it deletion still removes the workflow, and the
	// conversations age out under retention instead.
	sessions SessionForgetter
	// idempotency makes a retried run replay instead of queueing a second
	// execution. Optional: a nil service refuses a request that carries an
	// Idempotency-Key rather than running it unprotected.
	idempotency *idempotency.Service
}

// SessionForgetter drops a workflow's retained agent conversations. It is a
// one-method interface so the handler never depends on the memory store
// itself, only on the deletion it needs.
type SessionForgetter interface {
	ForgetWorkflow(tenantID, workflowID string)
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
	// TriggerNodeID is the trigger this run starts from. It echoes the run
	// request's choice, so a client can tell which branch it queued without
	// reading the execution back; empty means every trigger runs.
	TriggerNodeID string          `json:"triggerNodeId,omitempty"`
	Input         json.RawMessage `json:"input,omitempty"`
	CreatedAt     time.Time       `json:"createdAt"`
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
	BaseVersionID string                `json:"baseVersionId,omitempty" doc:"Latest version ID the editor saved from; a save from a stale revision is refused with 409"`
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
	// SkillsUsed is what the caller reported using to make this write. It is
	// recorded on the revision it creates.
	SkillsUsed string `header:"X-KilasFlow-Skills-Used" doc:"Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates"`
	Body       workflowDocumentInput
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

// WorkflowVersionSummaryResource is one row of a workflow's history listing. It
// deliberately omits the document: a listing exists to choose a revision, not
// to ship every graph the workflow has ever had.
type WorkflowVersionSummaryResource struct {
	ID            string    `json:"id"`
	WorkflowID    string    `json:"workflowId"`
	Revision      int       `json:"revision"`
	SchemaVersion int       `json:"schemaVersion"`
	Label         string    `json:"label,omitempty" doc:"What a person called this revision, when one was named"`
	CreatedBy     string    `json:"createdBy,omitempty" doc:"Who saved this revision, where the author is known"`
	ActorKind     string    `json:"actorKind,omitempty" enum:"user,key" doc:"What kind of caller saved this revision: user for a signed-in person, key for an API key. Absent when the write predates attribution or arrived unauthenticated."`
	ActorLabel    string    `json:"actorLabel,omitempty" doc:"The actor's email address or API key name, where one is known"`
	ActorKeyID    string    `json:"actorKeyId,omitempty" doc:"Identifier of the API key that saved this revision, so a reused key name still names the key that acted. Absent for a session."`
	ActorMeta     []string  `json:"actorMeta,omitempty" doc:"What the caller reported using to make the write, from the X-KilasFlow-Skills-Used header"`
	CreatedAt     time.Time `json:"createdAt"`
	Draft         bool      `json:"draft" doc:"True for the newest revision, the one an editor is working on"`
	Published     bool      `json:"published" doc:"True for the revision production traffic runs. Stated by the server so a client never infers publication by comparing identifiers."`
}

// WorkflowVersionListResource is one page of workflow history, newest first.
type WorkflowVersionListResource struct {
	Items      []WorkflowVersionSummaryResource `json:"items"`
	NextCursor string                           `json:"nextCursor,omitempty" doc:"Pass back as ?cursor= to read the next page"`
}

// WorkflowPublishEventResource is one entry of the publish audit trail.
type WorkflowPublishEventResource struct {
	WorkflowID string    `json:"workflowId"`
	VersionID  string    `json:"versionId" doc:"For a restore, the revision that was restored from"`
	Action     string    `json:"action" enum:"published,unpublished,restored"`
	Actor      string    `json:"actor,omitempty" doc:"Who acted, where the actor is known"`
	ActorKind  string    `json:"actorKind,omitempty" enum:"user,key" doc:"What kind of caller acted: user for a signed-in person, key for an API key. Absent when the publish predates attribution or arrived unauthenticated."`
	ActorLabel string    `json:"actorLabel,omitempty" doc:"The actor's email address or API key name, where one is known"`
	ActorKeyID string    `json:"actorKeyId,omitempty" doc:"Identifier of the API key that acted. Absent for a session."`
	Reason     string    `json:"reason,omitempty"`
	CreatedAt  time.Time `json:"createdAt"`
}

type listWorkflowVersionsInput struct {
	ID     string `path:"id" minLength:"1" doc:"Workflow identifier"`
	Limit  int    `query:"limit" minimum:"1" maximum:"100" doc:"Maximum revisions to return (default 25)"`
	Cursor string `query:"cursor" doc:"Opaque cursor from a previous listing's nextCursor"`
}

type workflowVersionListOutput struct {
	Body WorkflowVersionListResource
}

type workflowPublishEventListOutput struct {
	Body []WorkflowPublishEventResource
}

// publishVersionInput carries the audit reason. The body is optional because a
// publish is meaningful without an explanation, and requiring one would only
// teach callers to send a placeholder.
type publishVersionInput struct {
	ID        string `path:"id" minLength:"1" doc:"Workflow identifier"`
	VersionID string `path:"versionId" minLength:"1" doc:"Workflow revision identifier"`
	// SkillsUsed is what the caller reported using to make this write. A
	// publish writes no revision, so it is recorded on the revision a restore
	// creates and ignored by the publish itself.
	SkillsUsed string `header:"X-KilasFlow-Skills-Used" doc:"Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates"`
	Body       *struct {
		Reason string `json:"reason,omitempty" maxLength:"255" doc:"Why this revision was published or restored, recorded in the audit trail"`
	}
}

func (input publishVersionInput) reason() string {
	if input.Body == nil {
		return ""
	}
	return input.Body.Reason
}

type updateWorkflowInput struct {
	ID      string `path:"id" minLength:"1" doc:"Workflow identifier"`
	IfMatch string `header:"If-Match" doc:"Latest version ID the editor saved from; a save from a stale revision is refused with 409"`
	// SkillsUsed is what the caller reported using to make this write. It is
	// recorded on the revision it creates.
	SkillsUsed string `header:"X-KilasFlow-Skills-Used" doc:"Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates"`
	Body       workflowDocumentInput
}

type runWorkflowInput struct {
	ID string `path:"id" minLength:"1" doc:"Workflow identifier"`
	// IdempotencyKey is the caller's retry key. Absent and empty are the same
	// thing to the framework, and both mean no idempotency.
	IdempotencyKey string `header:"Idempotency-Key" minLength:"1" maxLength:"255" pattern:"^[!-~]+$" doc:"1-255 printable ASCII characters. A retry carrying the same key and the same request is answered with the first request's outcome and repeats no side effect, marked with Idempotent-Replayed: true. The same key with a different request or resource is refused with 409. Keys are per tenant and are remembered for idempotency.retention. An empty header means no idempotency."`
	Body           *struct {
		Input json.RawMessage `json:"input,omitempty" doc:"Optional manual-run input JSON"`
		// TriggerNodeID picks which trigger the run starts from. A workflow
		// that declares several — a webhook beside a nightly schedule is the
		// standard shape — fires only the one named here, with that trigger's
		// item shape. Empty runs every trigger, which is what a single-trigger
		// workflow has always done.
		TriggerNodeID string `json:"triggerNodeId,omitempty" doc:"Trigger node this manual run starts from. Omit to run every trigger."`
		// WorkflowVersionID pins the revision to run instead of the newest one,
		// so an agent can reproduce a run against the exact graph it was
		// reading. A version that is not this workflow's reads as 404.
		WorkflowVersionID string `json:"workflowVersionId,omitempty" doc:"Revision to run. Omit to run the workflow's latest revision."`
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

// listWorkflowsInput is one page request from the dashboard. The bounds match
// the repository's own clamp, so an out-of-range value is refused at the edge
// with a schema error rather than silently clamped behind the caller's back.
type listWorkflowsInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"500" doc:"Maximum workflows to return (default 100)"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's X-Next-Cursor header"`
}

type workflowListOutput struct {
	// NextCursor is empty on the last page. It is a header rather than a body
	// field because the body is the bare array the dashboard has always read.
	NextCursor string `header:"X-Next-Cursor" doc:"Cursor for the next page; empty when there is none"`
	Body       []WorkflowSummary
}

type deletedWorkflowOutput struct {
	Status int `status:"204"`
}

type executionRequestOutput struct {
	Status int `status:"202"`
	Body   ExecutionRequestResource
}

// runWorkflowOutput is the manual run's response. It is a type of its own
// rather than executionRequestOutput because cancellation shares that type and
// has no idempotency key, so it must not advertise the replay header.
type runWorkflowOutput struct {
	Status int `status:"202"`
	// Replayed is empty on a first response, and huma omits an empty header, so
	// the marker is present exactly when the response came from a record.
	Replayed string `header:"Idempotent-Replayed" doc:"true when this response replays an earlier request with the same Idempotency-Key"`
	Body     ExecutionRequestResource
}

// ExecutionNodeRunResource is the externally inspectable trace of one node
// attempt. Payloads were already redacted at the repository boundary.
type ExecutionNodeRunResource struct {
	NodeID   string           `json:"nodeId"`
	Attempt  int              `json:"attempt"`
	RunIndex int              `json:"runIndex" doc:"The Nth time this node ran in the execution, counting from zero. Distinct from attempt, which counts retries of one run."`
	Sequence int              `json:"sequence"`
	Status   execution.Status `json:"status"`
	Input    json.RawMessage  `json:"input,omitempty"`
	Output   json.RawMessage  `json:"output,omitempty"`
	Error    json.RawMessage  `json:"error,omitempty"`
	// Response is the HTTP answer a Respond to Webhook node produced for a
	// waiting caller, persisted with the run so an inspector — or a boundary in
	// another process — can read what the caller received.
	Response json.RawMessage `json:"response,omitempty"`
	// Console is what the node's code printed, persisted with the run so an
	// execution read later, or from another process, still shows it.
	Console    json.RawMessage `json:"console,omitempty" doc:"What a Code node's code printed: {lines: [{level, text, at}], truncated}. level is log, info, warn, error or debug; truncated is true when output past the node's console limit was dropped. Absent when the node printed nothing."`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
}

// ExecutionResource surfaces the durable execution state, result, structured
// error, and ordered node-run history through the REST API.
type ExecutionResource struct {
	ID                      string            `json:"id"`
	WorkflowID              string            `json:"workflowId"`
	WorkflowVersionID       string            `json:"workflowVersionId"`
	Status                  execution.Status  `json:"status"`
	Trigger                 execution.Trigger `json:"trigger"`
	TriggerNodeID           string            `json:"triggerNodeId,omitempty" doc:"The trigger node this run started from, when one workflow declares several"`
	ParentExecutionID       string            `json:"parentExecutionId,omitempty" doc:"The execution that called this one, for a sub-workflow run"`
	Input                   json.RawMessage   `json:"input,omitempty"`
	Output                  json.RawMessage   `json:"output,omitempty"`
	Error                   json.RawMessage   `json:"error,omitempty"`
	StartedAt               time.Time         `json:"startedAt"`
	FinishedAt              *time.Time        `json:"finishedAt,omitempty"`
	CancellationRequestedAt *time.Time        `json:"cancellationRequestedAt,omitempty"`
	// ResumeURL is the machine resume link for a waiting execution, and
	// ApprovalURL its human page. Both are present only while the execution
	// waits; any other status omits them.
	ResumeURL   string                     `json:"resumeUrl,omitempty"`
	ApprovalURL string                     `json:"approvalUrl,omitempty"`
	NodeRuns    []ExecutionNodeRunResource `json:"nodeRuns"`
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
		Summary: "List workflows", Description: "Returns one page of workflow summaries, newest first. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.", Tags: []string{"Workflows"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "get-workflow", Method: http.MethodGet, Path: "/workflows/{id}",
		Summary: "Get a workflow", Description: "Returns the current canonical document and lifecycle metadata.", Tags: []string{"Workflows"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "list-workflow-webhooks", Method: http.MethodGet, Path: "/workflows/{id}/webhooks",
		Summary:     "List a workflow's webhook URLs",
		Description: "Returns every webhook trigger's public address. The opaque route is minted on first read and reused forever, so the URL is known before activation and unchanged by it.",
		Tags:        []string{"Workflows"},
	}, handler.ListWebhooks)
	huma.Register(api, huma.Operation{
		OperationID: "get-workflow-version", Method: http.MethodGet, Path: "/workflows/{id}/versions/{versionId}",
		Summary: "Get one workflow revision", Description: "Returns an immutable revision by ID, so an execution inspector can replay the exact graph that ran.", Tags: []string{"Workflows"},
	}, handler.GetVersion)
	huma.Register(api, huma.Operation{
		OperationID: "list-workflow-versions", Method: http.MethodGet, Path: "/workflows/{id}/versions",
		Summary: "List workflow revisions", Description: "Returns one page of a workflow's version history, newest first. Summaries carry no document.", Tags: []string{"Workflows"},
	}, handler.ListVersions)
	huma.Register(api, huma.Operation{
		OperationID: "publish-workflow-version", Method: http.MethodPost, Path: "/workflows/{id}/versions/{versionId}/publish",
		Summary: "Publish one workflow revision", Description: "Compiles the named revision and pins it as the version production traffic runs, which is how a bad save is rolled back.", Tags: []string{"Workflow lifecycle"},
	}, handler.PublishVersion)
	huma.Register(api, huma.Operation{
		OperationID: "restore-workflow-version", Method: http.MethodPost, Path: "/workflows/{id}/versions/{versionId}/restore",
		Summary: "Restore one workflow revision", Description: "Appends a new revision carrying an older snapshot's document. History is append-only: the restored-from revision is left unchanged.", Tags: []string{"Workflow lifecycle"},
	}, handler.RestoreVersion)
	huma.Register(api, huma.Operation{
		OperationID: "list-workflow-publish-events", Method: http.MethodGet, Path: "/workflows/{id}/publish-events",
		Summary: "List a workflow's publish history", Description: "Returns every publish, unpublish and restore recorded for a workflow, newest first.", Tags: []string{"Workflow lifecycle"},
	}, handler.ListPublishEvents)
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
		Summary: "Queue a manual workflow run", Description: "Validates and queues a saved revision without requiring activation. Body.workflowVersionId pins the revision to run; omit it to run the latest. Body.triggerNodeId selects the trigger to start from; omit it to run every trigger, and a node that cannot start a run is refused with 422. Send Idempotency-Key to make a retry safe: " + idempotencyKeyDoc, Tags: []string{"Workflow lifecycle"},
	}, handler.Run)
	huma.Register(api, huma.Operation{
		OperationID: "validate-workflow-document", Method: http.MethodPost, Path: "/workflows/validate",
		Summary: "Validate a workflow document",
		Description: "Compiles the supplied document with the same compiler activation uses and answers the diagnostics, " +
			"saving nothing. The catalogue is narrowed to the caller's tenant, so a document that references a node this " +
			"workspace may not use says so here rather than at activation.",
		Tags: []string{"Workflows"},
	}, handler.ValidateDocument)
	huma.Register(api, huma.Operation{
		OperationID: "duplicate-workflow", Method: http.MethodPost, Path: "/workflows/{id}/duplicate", DefaultStatus: http.StatusCreated,
		Summary: "Duplicate a workflow",
		Description: "Copies a workflow's latest revision into a new workflow of the same tenant, named after the source " +
			"with (copy) after it unless the request names one.",
		Tags: []string{"Workflows"},
	}, handler.Duplicate)
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
	stored, err := handler.workflows.SaveDraft(audited(ctx, input.SkillsUsed), handler.tenant(ctx), document)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &createdWorkflowOutput{
		Status: http.StatusCreated, Location: "/api/v1/workflows/" + stored.ID, Body: workflowResource(stored),
	}, nil
}

// List returns one page of tenant-scoped workflow summaries.
//
// The body stays a bare array that the dashboard already reads; the cursor for
// the next page rides in a header, so adding pagination did not change the
// response shape a running client depends on.
func (handler *Workflows) List(ctx context.Context, input *listWorkflowsInput) (*workflowListOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	page, err := handler.workflows.ListSummaries(ctx, handler.tenant(ctx), repository.WorkflowFilter{
		Limit: input.Limit, Cursor: input.Cursor,
	})
	// A cursor the client did not receive from this API is a bad request, not a
	// server fault, so it must not be reported as a 500.
	if errors.Is(err, repository.ErrInvalidCursor) {
		return nil, huma.Error400BadRequest("workflow cursor is invalid")
	}
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	items := make([]WorkflowSummary, 0, len(page.Workflows))
	for _, item := range page.Workflows {
		items = append(items, WorkflowSummary{
			ID: item.ID, Name: item.Name, Active: item.Active, LatestRevision: item.LatestRevision, UpdatedAt: item.UpdatedAt,
		})
	}
	return &workflowListOutput{Body: items, NextCursor: page.NextCursor}, nil
}

// Get returns the stored canonical document and lifecycle state.
func (handler *Workflows) Get(ctx context.Context, input *workflowPathInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

type workflowWebhookListOutput struct {
	Body []WebhookRouteResource
}

// ListWebhooks mints — or reads back — each trigger's public route.
//
// Minting here rather than only at activation reuses repository.WebhookRouteMinter
// exactly as POST /workflows/import does (internal/api/handlers/interop.go:214):
// routes are keyed by (tenant, workflow, node) and reused forever, so a GET that
// mints once is idempotent and the URL it reports is the URL that will answer.
func (handler *Workflows) ListWebhooks(ctx context.Context, input *workflowPathInput) (*workflowWebhookListOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	stored, err := handler.workflows.Get(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	minter, ok := handler.workflows.(repository.WebhookRouteMinter)
	if !ok {
		return nil, huma.Error503ServiceUnavailable("workflow storage cannot resolve webhook addresses")
	}
	bindings, err := minter.EnsureWebhookRoutes(ctx, tenant, stored.ID, stored.LatestVersion.Document)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	// An empty array rather than null: a workflow with no trigger has an empty
	// list of addresses, not an absent one.
	resources := make([]WebhookRouteResource, 0, len(bindings))
	for _, binding := range bindings {
		resources = append(resources, WebhookRouteResource{
			NodeID: binding.NodeID, Method: binding.Method, Path: binding.Path,
			URL: "/webhook/" + binding.Route,
		})
	}
	return &workflowWebhookListOutput{Body: resources}, nil
}

// GetVersion returns one immutable revision by ID.
func (handler *Workflows) GetVersion(ctx context.Context, input *workflowVersionPathInput) (*workflowVersionOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	version, err := handler.workflows.GetVersionByID(ctx, handler.tenant(ctx), input.ID, input.VersionID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowVersionOutput{Body: workflowVersionResource(version)}, nil
}

// ListVersions returns one page of a workflow's revision history.
func (handler *Workflows) ListVersions(ctx context.Context, input *listWorkflowVersionsInput) (*workflowVersionListOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	page, err := handler.workflows.ListVersions(ctx, handler.tenant(ctx), input.ID, repository.VersionFilter{
		Limit: input.Limit, Cursor: input.Cursor,
	})
	// A cursor the client did not receive from this API is a bad request, not a
	// server fault, so it must not be reported as a 500.
	if errors.Is(err, repository.ErrInvalidCursor) {
		return nil, huma.Error400BadRequest("workflow version cursor is invalid")
	}
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	resource := WorkflowVersionListResource{
		Items:      make([]WorkflowVersionSummaryResource, 0, len(page.Versions)),
		NextCursor: page.NextCursor,
	}
	for _, summary := range page.Versions {
		resource.Items = append(resource.Items, WorkflowVersionSummaryResource{
			ID: summary.ID, WorkflowID: summary.WorkflowID, Revision: summary.Revision,
			SchemaVersion: summary.SchemaVersion, Label: summary.Label, CreatedBy: summary.CreatedBy,
			ActorKind: summary.ActorKind, ActorLabel: summary.ActorLabel, ActorKeyID: summary.ActorKeyID,
			ActorMeta: summary.ActorMeta,
			CreatedAt: summary.CreatedAt, Draft: summary.Draft, Published: summary.Published,
		})
	}
	return &workflowVersionListOutput{Body: resource}, nil
}

// PublishVersion pins one named revision as the version production traffic
// runs.
func (handler *Workflows) PublishVersion(ctx context.Context, input *publishVersionInput) (*workflowOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	if err := handler.embedVersionProblem(ctx, input.ID, input.VersionID); err != nil {
		return nil, err
	}
	// The catalogue is narrowed to the publishing tenant: a revision that
	// references a node this tenant may not use is refused here, before it
	// becomes the version production traffic runs.
	tenant := handler.tenant(ctx)
	stored, err := handler.workflows.PublishVersion(audited(ctx, input.SkillsUsed), tenant, input.ID, input.VersionID, workflow.CatalogFor(handler.catalog, tenant.ID), input.reason())
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// RestoreVersion appends a new revision carrying an older snapshot's document.
func (handler *Workflows) RestoreVersion(ctx context.Context, input *publishVersionInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	if err := handler.embedVersionProblem(ctx, input.ID, input.VersionID); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.RestoreVersion(audited(ctx, input.SkillsUsed), handler.tenant(ctx), input.ID, input.VersionID, input.reason())
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowOutput{Body: workflowResource(stored)}, nil
}

// ListPublishEvents returns a workflow's publish audit trail.
func (handler *Workflows) ListPublishEvents(ctx context.Context, input *workflowPathInput) (*workflowPublishEventListOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	events, err := handler.workflows.ListPublishEvents(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	items := make([]WorkflowPublishEventResource, 0, len(events))
	for _, event := range events {
		items = append(items, WorkflowPublishEventResource{
			WorkflowID: event.WorkflowID, VersionID: event.VersionID, Action: string(event.Action),
			Actor: event.Actor, ActorKind: event.ActorKind, ActorLabel: event.ActorLabel, ActorKeyID: event.ActorKeyID,
			Reason: event.Reason, CreatedAt: event.CreatedAt,
		})
	}
	return &workflowPublishEventListOutput{Body: items}, nil
}

// Update appends a new immutable draft revision for an existing workflow.
//
// A save names the revision it was taken from, in `If-Match` or
// `baseVersionId`. A save from a stale revision is refused with 409 before
// anything is written, so two tabs can never silently overwrite each other —
// the loser reloads or retries against the newer revision.
func (handler *Workflows) Update(ctx context.Context, input *updateWorkflowInput) (*workflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	if base := input.baseVersion(); base != "" && base != stored.LatestVersion.ID {
		return nil, &huma.ErrorModel{
			Status: http.StatusConflict,
			Title:  "Conflict",
			Detail: "This workflow changed since you loaded it (expected revision " + base + ", latest is " + stored.LatestVersion.ID + "). Reload and save again.",
		}
	}
	document := input.Body.document(input.ID)
	// An embed session is refused before the compiler is consulted: what it may
	// put in the document is a question about its authority, and answering it
	// first keeps a refusal from arriving as a graph error the guest cannot
	// act on.
	if err := embedDocumentProblem(ctx, document); err != nil {
		return nil, err
	}
	if err := workflow.ValidateDraft(document); err != nil {
		return nil, draftProblem(err)
	}
	saved, err := handler.workflows.SaveDraft(audited(ctx, input.SkillsUsed), handler.tenant(ctx), document)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &workflowOutput{Body: workflowResource(saved)}, nil
}

// baseVersion prefers the header form; the body field covers generated clients
// that cannot set headers per call.
func (input *updateWorkflowInput) baseVersion() string {
	if input == nil {
		return ""
	}
	if input.IfMatch != "" {
		return input.IfMatch
	}
	return input.Body.BaseVersionID
}

// Delete removes a workflow from future tenant-scoped reads.
func (handler *Workflows) Delete(ctx context.Context, input *workflowPathInput) (*deletedWorkflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	if err := handler.workflows.Delete(ctx, tenant, input.ID); err != nil {
		return nil, handler.problem(ctx, err)
	}
	// The conversations die with the workflow: a deleted workflow's sessions
	// would otherwise linger until retention aged them out, addressable by
	// nothing. Forgetting is best-effort after the commit — the store is a
	// working buffer, and a failed drop must not fail a completed delete.
	if handler.sessions != nil {
		handler.sessions.ForgetWorkflow(tenant.ID, input.ID)
	}
	return &deletedWorkflowOutput{Status: http.StatusNoContent}, nil
}

// Activate validates and pins the latest saved revision.
func (handler *Workflows) Activate(ctx context.Context, input *workflowPathInput) (*activationOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	auditedCtx := audited(ctx, "")
	stored, err := handler.workflows.Activate(auditedCtx, tenant, input.ID, workflow.CatalogFor(handler.catalog, tenant.ID))
	if err != nil {
		return nil, handler.problem(ctx, err)
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
			if _, deactivateErr := handler.workflows.Deactivate(auditedCtx, tenant, stored.ID); deactivateErr != nil {
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
	stored, err := handler.workflows.Deactivate(audited(ctx, ""), tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
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
//
// An embed session is checked against the revision this call would compile
// before anything is queued: a session can only reach this route carrying its
// own token, and a run is where a document that escaped the save-time check —
// an older revision, saved before the check existed — would otherwise execute
// with the tenant's authority.
//
// The body may name the trigger to start from. A workflow that declares several
// — a webhook beside a nightly schedule — otherwise fires all of them with the
// same item, which sends duplicate writes and hands trigger-shaped expressions
// the wrong payload; and the run is refused by name when the node cannot start
// one.
//
// A caller that sends Idempotency-Key gets the first request's outcome back
// instead of a second queued run. The confinement check runs before the key is
// read, so a replay is held to the same rules as the request it replays.
func (handler *Workflows) Run(ctx context.Context, input *runWorkflowInput) (*runWorkflowOutput, error) {
	if err := handler.available(true); err != nil {
		return nil, err
	}
	var payload json.RawMessage
	triggerNodeID, versionID := "", ""
	if input.Body != nil {
		payload = input.Body.Input
		triggerNodeID = strings.TrimSpace(input.Body.TriggerNodeID)
		versionID = strings.TrimSpace(input.Body.WorkflowVersionID)
	}
	// The confinement check is about the document this call would execute, so
	// it follows the revision the call names: a pinned run is checked against
	// that revision, an ordinary one against the latest.
	if versionID == "" {
		if err := handler.embedStoredProblem(ctx, input.ID); err != nil {
			return nil, err
		}
	} else if err := handler.embedVersionProblem(ctx, input.ID, versionID); err != nil {
		return nil, err
	}
	// queue is the side effect, and it wakes a worker exactly once: a replay
	// returns the recorded outcome without running this.
	queue := func(ctx context.Context) (idempotency.Response, error) {
		tenant := handler.tenant(ctx)
		var created execution.Record
		var err error
		// The catalogue is narrowed at the call site rather than into a local
		// first: the guardrail that holds every queue path to a tenant-scoped
		// catalogue reads the argument, and a variable it cannot follow would
		// be an obligation it cannot check.
		if versionID == "" {
			created, err = handler.executions.QueueManualLatest(ctx, tenant, input.ID, workflow.CatalogFor(handler.catalog, tenant.ID), triggerNodeID, payload)
		} else {
			created, err = handler.executions.QueueManualVersion(ctx, tenant, input.ID, versionID, workflow.CatalogFor(handler.catalog, tenant.ID), triggerNodeID, payload)
		}
		if err != nil {
			return idempotency.Response{}, err
		}
		if handler.waker != nil {
			handler.waker.Wake()
		}
		resource := executionRequestResource(created)
		// The recorded form drops the input echo. It is the durable identity a
		// replay needs, and keeping a copy of the caller's payload in a second
		// table would outlive execution.retention.
		replay := resource
		replay.Input = nil
		return idempotency.Response{Status: http.StatusAccepted, Body: resource, Record: replay}, nil
	}

	if input.IdempotencyKey == "" {
		response, err := queue(ctx)
		if err != nil {
			return nil, handler.problem(ctx, err)
		}
		return &runWorkflowOutput{Status: response.Status, Body: response.Body.(ExecutionRequestResource)}, nil
	}
	if handler.idempotency == nil {
		return nil, idempotencyUnavailable()
	}
	result, err := handler.idempotency.Do(ctx, handler.tenant(ctx).ID, input.IdempotencyKey, idempotency.Request{
		Operation: "run-workflow",
		Target:    input.ID,
		Body: struct {
			Input             json.RawMessage `json:"input"`
			TriggerNodeID     string          `json:"triggerNodeId"`
			WorkflowVersionID string          `json:"workflowVersionId"`
		}{Input: payload, TriggerNodeID: triggerNodeID, WorkflowVersionID: versionID},
	}, queue)
	if err != nil {
		if problem, ok := idempotencyProblem(ctx, err); ok {
			return nil, problem
		}
		return nil, handler.problem(ctx, err)
	}
	var resource ExecutionRequestResource
	if err := result.Outcome.Decode(&resource); err != nil {
		return nil, serverProblem(ctx, "the recorded run outcome could not be read", err)
	}
	return &runWorkflowOutput{Status: result.Outcome.Status, Replayed: replayedHeader(result.Replayed), Body: resource}, nil
}

func (handler *Workflows) tenant(ctx context.Context) repository.TenantScope {
	return handler.tenants.Resolve(ctx)
}

// skillsUsedHeader is where an agent reports which skills informed a write.
//
// The header is the KilasFlow analogue of the n8n pack's skillsUsed argument:
// it is how an installation learns which skills actually get used, so it is
// read at the only boundary that has the request and stored on the revision the
// call creates.
const skillsUsedHeader = "X-KilasFlow-Skills-Used"

// audited returns ctx carrying the identity the write it precedes is attributed
// to: the request's principal, plus whatever the caller reported using.
//
// It is one function rather than a line in every handler so the vocabulary —
// which authentication path becomes which actor kind — has exactly one
// definition, and so an installation with authentication disabled records the
// same nothing everywhere rather than a different guess per verb.
func audited(ctx context.Context, skillsUsed string) context.Context {
	actor := repository.Actor{}
	if principal, found := auth.PrincipalFrom(ctx); found {
		actor.Label = principal.Label
		actor.KeyID = principal.KeyID
		switch principal.Kind {
		case auth.KindAPIKey:
			actor.Kind = repository.ActorKindKey
		case auth.KindSession:
			actor.Kind = repository.ActorKindUser
		}
	}
	for _, name := range strings.Split(skillsUsed, ",") {
		if trimmed := strings.TrimSpace(name); trimmed != "" {
			actor.Meta = append(actor.Meta, trimmed)
		}
	}
	return repository.WithActor(ctx, actor)
}

func (handler *Workflows) available(needsExecution bool) error {
	if handler.workflows == nil || (needsExecution && (handler.executions == nil || handler.catalog == nil)) {
		return huma.Error503ServiceUnavailable("workflow service unavailable")
	}
	return nil
}

// problem maps a repository failure onto the problem a caller sees.
//
// The ctx is passed rather than reconstructed because the cause is logged
// here: a 500 whose reason is discarded leaves an operator with a status code
// and a path, which is how the review found 49,014 unexplained execution
// failures in one shared log.
func (handler *Workflows) problem(ctx context.Context, err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("workflow not found")
	}
	// A webhook path another workflow already claimed is a conflict, not a
	// server fault: the operator can see which endpoint is taken and change
	// one of the two paths. Answering 500 sent them to the logs for a mistake
	// they could have fixed from the response.
	var conflict *repository.WebhookConflictError
	if errors.As(err, &conflict) {
		detail := "another workflow already serves this webhook path: change the path on one of them"
		if conflict.WorkflowID != "" {
			detail = "workflow " + conflict.WorkflowID + " already serves this webhook path: change the path on one of them"
		}
		return &huma.ErrorModel{Status: http.StatusConflict, Title: "Conflict", Detail: detail}
	}
	var validation *workflow.ValidationErrors
	if errors.As(err, &validation) {
		return compileProblem(validation)
	}
	return serverProblem(ctx, "workflow operation failed", err)
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
		Status: record.Status, Trigger: record.Trigger, TriggerNodeID: record.TriggerNodeID,
		Input: execution.Redact(record.Input), CreatedAt: record.StartedAt,
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
			Response: execution.Redact(nodeRun.Response), Console: execution.Redact(nodeRun.Console),
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

// WithSessionMemory attaches the agent conversation store, so deleting a
// workflow also drops its sessions.
func (handler *Workflows) WithSessionMemory(sessions SessionForgetter) *Workflows {
	handler.sessions = sessions
	return handler
}

// WithTriggers attaches the lifecycle coordinator.
func (handler *Workflows) WithTriggers(coordinator TriggerCoordinator) *Workflows {
	handler.triggers = coordinator
	return handler
}

// WithIdempotency attaches the request idempotency service. Without it a
// request that carries an Idempotency-Key is refused rather than run
// unprotected.
func (handler *Workflows) WithIdempotency(service *idempotency.Service) *Workflows {
	handler.idempotency = service
	return handler
}
