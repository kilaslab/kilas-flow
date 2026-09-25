package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file holds the two operations that act on one execution's trace: the
// retry that starts it again, and the read-only evaluator that answers what a
// field held while it ran.

// retryExecutionOutput is the new run. It is 201 because a durable execution
// exists as a result of the call, and it carries the whole resource so a caller
// does not have to read the new id back before it can watch the run.
type retryExecutionOutput struct {
	Status int `status:"201"`
	Body   ExecutionResource
}

// Retry starts a new execution from a finished one's workflow, revision and
// input, under the trigger the original ran under.
//
// Retrying re-queues the revision the original ran rather than the workflow's
// newest one. Running the latest revision is a different operation — `run` —
// and answering a retry with it would report a run of a graph the caller never
// asked about, which is the one thing a retry must not do.
//
// A run that has not finished is refused with 409 rather than queued beside
// itself: two executions of one input racing each other is a duplicate side
// effect, and the caller's next move — wait, or cancel — is a decision only it
// can make.
func (handler *Executions) Retry(ctx context.Context, input *executionPathInput) (*retryExecutionOutput, error) {
	if handler.controller == nil || handler.history == nil || handler.catalog == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	tenant := handler.tenants.Resolve(ctx)
	record, err := handler.controller.Get(ctx, tenant, input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, serverProblem(ctx, "execution lookup failed", err)
	}
	if err := handler.ownsExecution(ctx, record); err != nil {
		return nil, err
	}
	if !finishedExecution(record.Status) {
		return nil, &huma.ErrorModel{
			Status: http.StatusConflict,
			Title:  "Conflict",
			Detail: "this execution is still " + string(record.Status) + ": wait for it to finish, or cancel it, before retrying",
		}
	}

	if err := handler.confinedRetryProblem(ctx, record); err != nil {
		return nil, err
	}

	// The same queue path a manual run takes, with the revision named instead
	// of resolved: the graph is compiled under this tenant's catalogue and the
	// trigger node is checked against it before anything is queued, so a retry
	// is held to exactly the rules its first run was. It keeps its original's
	// trigger: a retried webhook run is a production run, and keeps the
	// workflow's static data as one.
	created, err := handler.history.QueueRetry(ctx, tenant, record, workflow.CatalogFor(handler.catalog, tenant.ID))
	if err != nil {
		return nil, handler.retryProblem(ctx, err)
	}
	handler.controller.Wake()
	return &retryExecutionOutput{Status: http.StatusCreated, Body: executionResource(created)}, nil
}

// finishedExecution reports whether a status is one no worker will act on again.
//
// The four that are not: queued and running are work in flight, cancelling is
// work being stopped, and waiting is parked on an approval or a timer the run
// will resume from. A retry of any of them would run the same input beside a
// copy of itself.
func finishedExecution(status execution.Status) bool {
	switch status {
	case execution.StatusSucceeded, execution.StatusFailed, execution.StatusCancelled:
		return true
	default:
		return false
	}
}

// retryProblem maps a queue failure onto the problem the caller sees.
//
// The work a retry would do was already validated once, so a refusal here is
// about the workflow as it exists now: its revision compiled against this
// tenant's catalogue, and the trigger node it started from.
func (handler *Executions) retryProblem(ctx context.Context, err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("the workflow revision this execution ran is not available")
	}
	var validation *workflow.ValidationErrors
	if errors.As(err, &validation) {
		return compileProblem(validation)
	}
	return serverProblem(ctx, "workflow execution retry failed", err)
}

// evalExpressionInput is one expression and the node to read the trace from.
type evalExpressionInput struct {
	ID   string `path:"id" minLength:"1" doc:"Execution identifier"`
	Body struct {
		Expression string `json:"expression" minLength:"1" maxLength:"4096" doc:"The expression to evaluate. A template carrying {{ }} delimiters is evaluated exactly as a document parameter is; without them the whole text is taken as the expression body."`
		NodeID     string `json:"nodeId,omitempty" doc:"The node whose stored output and input the expression reads as $json and $input. Omit it to read the run through $node alone."`
	}
}

// EvalExpressionResource is one expression's answer: the value, and the JSON
// shape it had.
type EvalExpressionResource struct {
	Value json.RawMessage `json:"value" doc:"The evaluated result"`
	Type  string          `json:"type" enum:"string,number,boolean,array,object,null" doc:"The JSON shape the value has"`
}

type evalExpressionOutput struct {
	Body EvalExpressionResource
}

// EvalExpression evaluates one expression against an execution's stored trace.
//
// Nothing is written. This is the observe step of the create → validate → run →
// observe → patch loop, and it answers a question re-running cannot: what a
// field held at a node in the run that already happened, without changing the
// workflow or spending another run's side effects.
//
// The execution is loaded under the caller's tenant, so another tenant's id is
// a 404 like every other execution read, and an embed session is held to its
// own workflow by the same ownership rule.
func (handler *Executions) EvalExpression(ctx context.Context, input *evalExpressionInput) (*evalExpressionOutput, error) {
	if handler.controller == nil {
		return nil, huma.Error503ServiceUnavailable("execution runtime unavailable")
	}
	evaluator, ok := handler.controller.(ExecutionEvaluator)
	if !ok {
		return nil, huma.Error503ServiceUnavailable("expression evaluation unavailable")
	}
	tenant := handler.tenants.Resolve(ctx)
	record, err := handler.controller.Get(ctx, tenant, input.ID)
	if errors.Is(err, repository.ErrNotFound) {
		return nil, huma.Error404NotFound("execution not found")
	}
	if err != nil {
		return nil, serverProblem(ctx, "execution lookup failed", err)
	}
	if err := handler.ownsExecution(ctx, record); err != nil {
		return nil, err
	}

	// The revision the run was pinned to, so the names, settings and clock the
	// expression reads are the ones the run read.
	document := handler.ranDocument(ctx, record)

	result, err := evaluator.EvaluateExpression(ctx, record, document, input.Body.Expression, input.Body.NodeID)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity("the expression could not be evaluated: " + err.Error())
	}
	return &evalExpressionOutput{Body: EvalExpressionResource{Value: result.Value, Type: result.Type}}, nil
}

// ExecutionEvaluator is the runtime's read-only expression surface.
//
// It is an optional interface rather than a member of ExecutionController so
// the fakes every existing execution test supplies keep compiling, exactly as
// the waiting-links lookup does: a runtime that cannot evaluate answers 503
// rather than evaluating under some other rule.
type ExecutionEvaluator interface {
	EvaluateExpression(context.Context, execution.Record, workflow.Document, string, string) (engine.ExpressionEvaluation, error)
}

// WorkflowVersionReader reads the revision an execution ran.
type WorkflowVersionReader interface {
	GetVersionByID(context.Context, repository.TenantScope, string, string) (workflow.Version, error)
}

// WithWorkflowVersions attaches the revision reader the evaluator names nodes
// with. Without it an evaluation still runs, addressing nodes by the ids the
// trace itself shows.
func (handler *Executions) WithWorkflowVersions(workflows WorkflowVersionReader) *Executions {
	handler.workflows = workflows
	return handler
}

// WithCredentials gives the handler the credential store a confined caller's
// retry is checked against.
func (handler *Executions) WithCredentials(store repository.CredentialRepository) *Executions {
	handler.credentials = store
	return handler
}

// confinedRetryProblem holds a retry by a confined caller to the same document
// check a run gets.
//
// A retry queues the revision the original ran, which need not be the latest
// one a run would check: an old revision attaching an unscoped credential — or,
// for a session, a credential outside its grant — would otherwise execute
// through this route with the tenant's authority. Without the revision reader
// the document cannot be read, so a confined caller is refused rather than let
// through unchecked.
func (handler *Executions) confinedRetryProblem(ctx context.Context, record execution.Record) error {
	if _, _, caller, confined := confinedCaller(ctx); !confined {
		return nil
	} else if handler.workflows == nil {
		return huma.Error503ServiceUnavailable("workflow revisions unavailable: " + caller + " cannot retry here")
	}
	tenant := handler.tenants.Resolve(ctx)
	version, err := handler.workflows.GetVersionByID(ctx, tenant, record.WorkflowID, record.WorkflowVersionID)
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("the revision this execution ran no longer exists")
	}
	if err != nil {
		return serverProblem(ctx, "execution revision lookup failed", err)
	}
	return confinedDocumentProblem(ctx, handler.credentials, tenant, version.Document)
}

// ranDocument reads the revision an execution was pinned to.
//
// A revision that cannot be read is not an error about the evaluation: the
// trace is the data under inspection, and a workflow whose revision is gone
// leaves its nodes addressable by id rather than making the run unreadable.
func (handler *Executions) ranDocument(ctx context.Context, record execution.Record) workflow.Document {
	if handler.workflows == nil || record.WorkflowVersionID == "" {
		return workflow.Document{}
	}
	version, err := handler.workflows.GetVersionByID(ctx, handler.tenants.Resolve(ctx), record.WorkflowID, record.WorkflowVersionID)
	if err != nil {
		return workflow.Document{}
	}
	return version.Document
}
