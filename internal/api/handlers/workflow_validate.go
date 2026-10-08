package handlers

import (
	"context"
	"errors"
	"fmt"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/runcode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
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
	// Warnings never make a document invalid. They are what this dry run could
	// not prove, kept apart from Diagnostics because the import vocabulary has
	// no severity for "unchecked", and borrowing one would misreport it.
	Warnings []ValidationWarning `json:"warnings,omitempty" doc:"What this server could not check, such as a Go Code node it has no toolchain to compile. A warning never makes the document invalid"`
}

// ValidationWarning is one thing the dry run left unproven.
type ValidationWarning struct {
	NodeID string `json:"nodeId,omitempty"`
	Field  string `json:"field,omitempty"`
	Reason string `json:"reason"`
}

// CodeChecker builds a Go Code node's source the way a run would, so the dry
// run can report a compile error before anything runs.
//
// It answers nil when the source compiled, a *runcode.CompileError when the
// user's code is wrong, and any other error when the source could not be
// checked at all, such as on a deployment with no Go toolchain.
// nodes.CodeExecutor is the production implementation: it builds through the
// same artifact cache a run reads, so a dry run's build is the run's cache
// hit.
//
// Only this operation builds. A save must never wait on a compiler (see
// validateCodeConfiguration in nodes), and validate is the one request whose
// whole purpose is to ask.
type CodeChecker interface {
	CheckCode(ctx context.Context, source string) error
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

	var validation *workflow.ValidationErrors
	diagnostics := []n8n.ImportIssue{}
	switch {
	case err == nil:
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

	// A draft that never reached the graph has no node worth building.
	var warnings []ValidationWarning
	if err == nil || validation != nil {
		var compiled []n8n.ImportIssue
		compiled, warnings = handler.checkCode(ctx, document, validation)
		diagnostics = append(diagnostics, compiled...)
	}
	return &validateWorkflowOutput{Body: ValidateWorkflowResource{
		Valid: len(diagnostics) == 0, Diagnostics: diagnostics, Warnings: warnings,
	}}, nil
}

// checkCode builds every Go Code node that would run and reports what the
// toolchain said, at the user's own line (runcode.Wrap puts the body there).
//
// A node the compiler already refused is skipped, because its source is not
// worth building and the user would read one problem twice; so is a disabled
// node, because it never runs. A build that could not happen is a warning: an
// install with no toolchain may still run the node from its artifact cache,
// and refusing every Code node there would refuse documents that work.
func (handler *Workflows) checkCode(ctx context.Context, document workflow.Document, refused *workflow.ValidationErrors) ([]n8n.ImportIssue, []ValidationWarning) {
	if handler.code == nil {
		return nil, nil
	}
	alreadyRefused := map[string]bool{}
	if refused != nil {
		for _, issue := range refused.Issues {
			alreadyRefused[issue.NodeID] = true
		}
	}
	var diagnostics []n8n.ImportIssue
	var warnings []ValidationWarning
	for index, candidate := range document.Nodes {
		if candidate.Type != nodes.CodeNodeType || candidate.Disabled || alreadyRefused[candidate.ID] {
			continue
		}
		source, _ := candidate.Parameters["code"].(string)
		field := fmt.Sprintf("/nodes/%d/parameters/code", index)
		err := handler.code.CheckCode(ctx, source)
		var compileError *runcode.CompileError
		switch {
		case err == nil:
		case errors.As(err, &compileError):
			diagnostics = append(diagnostics, n8n.ImportIssue{
				Severity: n8n.SeverityBlocking,
				NodeID:   candidate.ID,
				Field:    field,
				Reason:   fmt.Sprintf("node %q %v", candidate.ID, compileError),
			})
		default:
			warnings = append(warnings, ValidationWarning{
				NodeID: candidate.ID,
				Field:  field,
				Reason: fmt.Sprintf("node %q was not compiled, so a compile error would show only when it runs: %v", candidate.ID, err),
			})
		}
	}
	return diagnostics, warnings
}
