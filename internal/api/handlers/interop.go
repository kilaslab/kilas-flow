package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/interop/n8n"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ImportedWorkflowResource is the saved draft plus everything the adapter
// refused to map.
//
// The report is part of the success response, not an error: an import that
// carried most of a workflow and named the rest is far more useful than one
// that refused the whole file over a single unsupported node.
type ImportedWorkflowResource struct {
	Workflow    WorkflowResource  `json:"workflow"`
	Unsupported []n8n.ImportIssue `json:"unsupported"`
	// Webhooks are the public addresses this workflow's triggers will answer
	// on once it is activated. Without them the caller has an imported
	// workflow and no way to learn where to send anything, which is the same
	// failure as not importing it.
	Webhooks []WebhookRouteResource `json:"webhooks"`
}

// WebhookRouteResource is one trigger's public address.
type WebhookRouteResource struct {
	NodeID string `json:"nodeId"`
	Method string `json:"method"`
	// Path is what the source workflow called this endpoint, kept as a label
	// so the author can recognise it.
	Path string `json:"path"`
	// URL is the address to configure in the sending system. It carries an
	// opaque route rather than the path, so two tenants importing the same
	// template do not collide and neither address is guessable.
	URL string `json:"url"`
}

// ExportedWorkflowResource is n8n-compatible JSON plus what it could not carry.
type ExportedWorkflowResource struct {
	Format   string            `json:"format"`
	Workflow json.RawMessage   `json:"workflow"`
	Lossy    []n8n.ExportIssue `json:"lossy"`
	// SupportedMappings is the advertised node subset, so a caller can see
	// exactly what interoperability is claimed rather than inferring it.
	SupportedMappings []string `json:"supportedMappings"`
}

// importDiagnostics is the report stored with the revision an import created.
//
// It carries an envelope, not a bare issue list, because a reader has to be
// able to tell "this revision was not imported" from "this import had nothing
// to report": the first revision of a hand-built workflow has no report at all,
// and rendering that as a clean import would invent a fact.
type importDiagnostics struct {
	Source     string            `json:"source"`
	ImportedAt time.Time         `json:"importedAt"`
	Issues     []n8n.ImportIssue `json:"issues"`
}

// WorkflowDiagnosticsResource is one revision's stored import report.
type WorkflowDiagnosticsResource struct {
	WorkflowID string `json:"workflowId"`
	VersionID  string `json:"versionId"`
	Revision   int    `json:"revision"`
	// Source is empty when nothing recorded an import for this revision, which
	// is the normal case for a workflow built in the editor.
	Source string `json:"source,omitempty"`
	// ImportedAt is when the import ran.
	ImportedAt *time.Time `json:"importedAt,omitempty"`
	// Issues is what the import could not carry faithfully, each naming the
	// node or field it is about. It is empty for a clean import and for a
	// revision that was never imported; Source is what tells them apart.
	Issues []n8n.ImportIssue `json:"issues"`
}

// Interop is the n8n import and export boundary.
type Interop struct {
	workflows repository.WorkflowRepository
	tenants   TenantResolver
	// catalog is read during import and export so a connection endpoint
	// resolves to a port
	// the target node actually declares. n8n names an endpoint by kind and
	// index; only the registry knows what that means here.
	catalog workflow.Catalog
}

// NewInterop constructs the interop handler.
func NewInterop(workflows repository.WorkflowRepository, catalog workflow.Catalog, tenants TenantResolver) *Interop {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Interop{workflows: workflows, catalog: catalog, tenants: tenants}
}

type importWorkflowInput struct {
	Body struct {
		Format string `json:"format,omitempty" doc:"Only \"n8n\" is supported"`
		// Workflow is the n8n export, passed through untouched. It is never
		// executed — only translated into canonical nodes.
		Workflow json.RawMessage `json:"workflow" doc:"An n8n workflow JSON document"`
		Name     string          `json:"name,omitempty" doc:"Overrides the imported workflow's name"`
	}
}

type importWorkflowOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     ImportedWorkflowResource
}

type exportWorkflowInput struct {
	ID     string `path:"id" minLength:"1" doc:"Workflow identifier"`
	Format string `query:"format" doc:"Only \"n8n\" is supported"`
}

type exportWorkflowOutput struct {
	Body ExportedWorkflowResource
}

type workflowDiagnosticsInput struct {
	ID string `path:"id" minLength:"1" doc:"Workflow identifier"`
	// VersionID names the revision to read. Empty means the newest revision,
	// which is what an editor showing the current draft asks for.
	VersionID string `query:"versionId" doc:"Revision to read; defaults to the newest"`
}

type workflowDiagnosticsOutput struct {
	Body WorkflowDiagnosticsResource
}

// Register wires import, export and the stored import report.
func (handler *Interop) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "import-workflow", Method: http.MethodPost, Path: "/workflows/import",
		DefaultStatus: http.StatusCreated,
		Summary:       "Import an n8n workflow",
		Description: "Translates n8n workflow JSON into a KilasFlow draft. Unsupported nodes are imported as " +
			"visible placeholders that block activation rather than being dropped or silently remapped.",
		Tags: []string{"Interop"},
	}, handler.Import)
	huma.Register(api, huma.Operation{
		OperationID: "export-workflow", Method: http.MethodGet, Path: "/workflows/{id}/export",
		Summary:     "Export a workflow as n8n JSON",
		Description: "Converts the latest revision into n8n-compatible JSON and reports what could not be carried.",
		Tags:        []string{"Interop"},
	}, handler.Export)
	huma.Register(api, huma.Operation{
		OperationID: "workflow-diagnostics", Method: http.MethodGet, Path: "/workflows/{id}/diagnostics",
		Summary:     "Read a revision's import report",
		Description: "Returns the report stored with a revision when an import created it: what the n8n " +
			"translation could not carry faithfully, per node and per field. A revision that was not " +
			"imported answers with no source and no issues.",
		Tags: []string{"Interop"},
	}, handler.Diagnostics)
}

// Import translates n8n JSON and saves the result as an ordinary draft.
func (handler *Interop) Import(ctx context.Context, input *importWorkflowInput) (*importWorkflowOutput, error) {
	if handler.workflows == nil {
		return nil, huma.Error503ServiceUnavailable("workflow storage unavailable")
	}
	if format := input.Body.Format; format != "" && format != "n8n" {
		return nil, huma.Error422UnprocessableEntity("only the n8n import format is supported")
	}

	result, err := n8n.Import(input.Body.Workflow, handler.catalog)
	if err != nil {
		// A malformed file is the caller's problem and its message names the
		// exact reason, so it is passed through rather than flattened.
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if name := input.Body.Name; name != "" {
		result.Document.Name = name
	}

	unsupported := result.Unsupported
	if unsupported == nil {
		unsupported = []n8n.ImportIssue{}
	}

	// The report is stored with the revision, not only returned. The response
	// is read once and then discarded with the dialog, and the nodes an import
	// left blocked, lossy or dropped have to still be nameable when somebody
	// opens the workflow tomorrow (BUG-f9frth).
	report, err := json.Marshal(importDiagnostics{
		Source: "n8n", ImportedAt: time.Now().UTC(), Issues: unsupported,
	})
	if err != nil {
		return nil, huma.Error500InternalServerError("could not encode the import report")
	}

	// Saved through the ordinary draft path — the same validation, the same
	// revision history, no import-specific write route — carrying the report.
	store, ok := handler.workflows.(repository.WorkflowDiagnosticsStore)
	if !ok {
		// Answering 201 with a report that nothing kept would reproduce exactly
		// the bug this route exists to fix, so a store that cannot hold one is
		// named rather than quietly degraded.
		return nil, huma.Error503ServiceUnavailable("workflow storage cannot record import diagnostics")
	}
	stored, err := store.SaveDraftWithDiagnostics(ctx, handler.tenants.Resolve(ctx), result.Document, report)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	webhooks := []WebhookRouteResource{}
	if router, ok := handler.workflows.(repository.WebhookRouteMinter); ok {
		routes, err := router.EnsureWebhookRoutes(ctx, handler.tenants.Resolve(ctx), stored.ID, stored.LatestVersion.Document)
		if err != nil {
			return nil, huma.Error500InternalServerError("could not determine the imported webhook addresses", err)
		}
		for _, route := range routes {
			webhooks = append(webhooks, WebhookRouteResource{
				NodeID: route.NodeID, Method: route.Method, Path: route.Path,
				URL: "/webhook/" + route.Route,
			})
		}
	}

	return &importWorkflowOutput{
		Status: http.StatusCreated, Location: "/api/v1/workflows/" + stored.ID,
		Body: ImportedWorkflowResource{
			Workflow: workflowResource(stored), Unsupported: unsupported, Webhooks: webhooks,
		},
	}, nil
}

// Export converts the latest revision into n8n JSON.
func (handler *Interop) Export(ctx context.Context, input *exportWorkflowInput) (*exportWorkflowOutput, error) {
	if handler.workflows == nil {
		return nil, huma.Error503ServiceUnavailable("workflow storage unavailable")
	}
	if input.Format != "" && input.Format != "n8n" {
		return nil, huma.Error422UnprocessableEntity("only the n8n export format is supported")
	}

	stored, err := handler.workflows.Get(ctx, handler.tenants.Resolve(ctx), input.ID)
	if err != nil {
		return nil, huma.Error404NotFound("workflow not found")
	}

	result, err := n8n.Export(stored.LatestVersion.Document, handler.catalog)
	if err != nil {
		return nil, huma.Error500InternalServerError("workflow could not be exported")
	}
	encoded, err := json.Marshal(result.Document)
	if err != nil {
		return nil, huma.Error500InternalServerError("workflow could not be encoded")
	}

	lossy := result.Lossy
	if lossy == nil {
		lossy = []n8n.Lossy{}
	}
	return &exportWorkflowOutput{Body: ExportedWorkflowResource{
		Format: "n8n", Workflow: encoded, Lossy: lossy,
		SupportedMappings: n8n.SupportedMappings(),
	}}, nil
}

// Diagnostics returns the import report stored with one revision.
//
// It is a read of the revision, not of the workflow: the newest revision of a
// workflow somebody has since edited carries no report, and the revision an
// import created keeps the one it was written with. The editor asks for the
// revision it is displaying and states nothing about the ones it is not.
func (handler *Interop) Diagnostics(ctx context.Context, input *workflowDiagnosticsInput) (*workflowDiagnosticsOutput, error) {
	if handler.workflows == nil {
		return nil, huma.Error503ServiceUnavailable("workflow storage unavailable")
	}
	store, ok := handler.workflows.(repository.WorkflowDiagnosticsStore)
	if !ok {
		return nil, huma.Error503ServiceUnavailable("workflow storage cannot read import diagnostics")
	}

	stored, err := store.WorkflowDiagnostics(ctx, handler.tenants.Resolve(ctx), input.ID, input.VersionID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("workflow revision not found")
		}
		return nil, huma.Error500InternalServerError("could not read the workflow's import report", err)
	}

	resource := WorkflowDiagnosticsResource{
		WorkflowID: input.ID, VersionID: stored.VersionID, Revision: stored.Revision,
		Issues: []n8n.ImportIssue{},
	}
	if len(stored.Report) > 0 {
		var report importDiagnostics
		if err := json.Unmarshal(stored.Report, &report); err != nil {
			// The column is written by this file and read back whole. A payload
			// that does not parse was written by something else, and inventing a
			// report out of it would be worse than saying so.
			return nil, huma.Error500InternalServerError("the stored import report is unreadable", err)
		}
		resource.Source = report.Source
		if !report.ImportedAt.IsZero() {
			importedAt := report.ImportedAt
			resource.ImportedAt = &importedAt
		}
		if report.Issues != nil {
			resource.Issues = report.Issues
		}
	}
	return &workflowDiagnosticsOutput{Body: resource}, nil
}
