package handlers

import (
	"context"
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// This file is `POST /workflows/{id}/duplicate`: copy a workflow's latest
// revision into a new workflow of the same tenant.
//
// The copy is an ordinary draft — revision 1 of a new workflow, saved through
// the same path every other create uses — because a duplicate that were
// special-cased in storage would be a second kind of workflow for the editor,
// the activation check and the execution queue to know about.

// duplicateWorkflowInput is the copy request. The name is optional because the
// common case is "give me my own workflow back to edit": a caller that has to
// invent a name first is a caller doing the server's job.
type duplicateWorkflowInput struct {
	ID string `path:"id" minLength:"1" doc:"Workflow identifier"`
	// SkillsUsed is what the caller reported using to make this write. It is
	// recorded on the revision the copy creates.
	SkillsUsed string `header:"X-KilasFlow-Skills-Used" doc:"Comma-separated names of the skills an agent used to make this call, recorded on the revision it creates"`
	Body       *struct {
		Name string `json:"name,omitempty" maxLength:"255" doc:"Name for the copy; defaults to the source name followed by (copy)"`
	}
}

// Duplicate creates a new workflow from the latest revision of an existing one.
//
// The latest revision, not the active one: duplicating is how an agent takes
// the workflow it has been editing and experiments on a second one, and the
// draft is what it has been editing.
//
// The copy always gets a server-minted identity — carrying the source's id
// would append a revision to the original instead of copying it, since the id
// is what the save path keys on. That is the only collision storage can express
// here: the schema puts no uniqueness on a workflow's name (POST /workflows
// accepts a repeated one, and so does import), so a copy named after an
// existing workflow is stored rather than refused, and duplicating twice yields
// two copies. The one name rule the schema does impose is the column's 255
// characters, refused as an invalid draft.
//
// No compile check runs: a draft that does not compile is savable by design,
// and duplicating one is how somebody repairs it without touching the original.
func (handler *Workflows) Duplicate(ctx context.Context, input *duplicateWorkflowInput) (*createdWorkflowOutput, error) {
	if err := handler.available(false); err != nil {
		return nil, err
	}
	tenant := handler.tenant(ctx)
	source, err := handler.workflows.Get(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}

	document := source.LatestVersion.Document
	document.ID = ""
	document.Name = duplicateName(input, source.Name)
	if err := workflow.ValidateDraftWithServerID(document); err != nil {
		return nil, draftProblem(err)
	}

	saved, err := handler.workflows.SaveDraft(audited(ctx, input.SkillsUsed), tenant, document)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &createdWorkflowOutput{
		Status: http.StatusCreated, Location: "/api/v1/workflows/" + saved.ID, Body: workflowResource(saved),
	}, nil
}

// duplicateName is the copy's name: what the caller asked for, or the source's
// name with (copy) after it.
func duplicateName(input *duplicateWorkflowInput, sourceName string) string {
	if input.Body != nil {
		if name := strings.TrimSpace(input.Body.Name); name != "" {
			return name
		}
	}
	return sourceName + " (copy)"
}
