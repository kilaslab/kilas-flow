package handlers

import (
	"context"
	"errors"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file is the dry run an agent needs before it saves: `POST
// /workflows/validate` compiles the document the caller supplies and answers
// what the compiler said, saving nothing.
//
// It exists because the loop's other two verbs both *write* — a save appends a
// revision and a run spends an execution — so before this operation the only
// way to ask "would this graph run" was to change the workflow and find out.
//
// The diagnostics are the import path's (`n8n.ImportIssue`), the same type
// `workflow import` reports and a revision's stored report holds, and the
// severity is the same vocabulary. A second diagnostic type for the same
// question would be a second thing for the editor, the CLI and the import
// report to render, and they would drift.

// validationDocumentID stands in for the identity a create would assign.
//
// A draft posted to /workflows carries no id — POST assigns it — so the
// compiler, which requires one, is given the same placeholder the save path
// uses (workflow.ValidateDraftWithServerID). Nothing is stored under it.
const validationDocumentID = "workflow-server-assigned"

// ValidateWorkflowResource is the whole answer to a dry run: whether the
// document would compile, and every reason it would not.
type ValidateWorkflowResource struct {
	Valid bool `json:"valid" doc:"True when the document compiles and would run"`
	// Diagnostics is never nil, so a client renders one shape on both answers.
	Diagnostics []n8n.ImportIssue `json:"diagnostics" doc:"What the compiler refused, stated as the import report states it: severity blocking, the node where one is named, and the reason"`
}

type validateWorkflowInput struct {
	Body workflowDocumentInput
}

type validateWorkflowOutput struct {
	Body ValidateWorkflowResource
}

// ValidateDocument compiles a supplied draft and reports the diagnostics
// without saving anything.
//
// The catalogue is narrowed to the caller's tenant, which is the half of this
// answer a local check cannot give: a document that references a node this
// tenant may not use has to say so here, because activation would refuse it
// later with the same message and no explanation of why.
func (handler *Workflows) ValidateDocument(ctx context.Context, input *validateWorkflowInput) (*validateWorkflowOutput, error) {
	if handler.workflows == nil {
		return nil, huma.Error503ServiceUnavailable("workflow service unavailable")
	}
	if handler.catalog == nil {
		return nil, huma.Error503ServiceUnavailable("node catalogue unavailable")
	}
	tenant := handler.tenant(ctx)
	document := input.Body.document(validationDocumentID)

	// Compile runs the whole check activation runs — the same compiler, the
	// same tenant-narrowed catalogue. A document that does not even decode as a
	// draft is reported as a diagnostic rather than as a failed request: the
	// caller asked what is wrong with it, and "this field is missing" is that
	// answer.
	_, err := workflow.Compile(document, workflow.CatalogFor(handler.catalog, tenant.ID))
	if err == nil {
		return &validateWorkflowOutput{Body: ValidateWorkflowResource{Valid: true, Diagnostics: []n8n.ImportIssue{}}}, nil
	}

	var validation *workflow.ValidationErrors
	diagnostics := []n8n.ImportIssue{}
	switch {
	case errors.As(err, &validation):
		for _, issue := range validation.Issues {
			diagnostics = append(diagnostics, n8n.ImportIssue{
				Severity: n8n.SeverityBlocking,
				NodeID:   issue.NodeID,
				Field:    issue.Path,
				Reason:   issue.Message,
			})
		}
	default:
		// A structurally unreadable draft has no issue list to walk: it never
		// reached the graph, so there is no element to name.
		diagnostics = append(diagnostics, n8n.ImportIssue{
			Severity: n8n.SeverityBlocking,
			Reason:   err.Error(),
		})
	}
	return &validateWorkflowOutput{Body: ValidateWorkflowResource{Valid: false, Diagnostics: diagnostics}}, nil
}
