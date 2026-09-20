package handlers

import (
	"context"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// This file is where an embed session's confinement is derived and enforced.
//
// The routing middleware already confines a session to one workflow, one
// datastore, or one row set. That is a bound on *what it may call*, and it is
// not enough: a session holding workflow:write may save any graph into its own
// workflow, and the graph then acts with the tenant's authority. A Data table
// node can name a sibling table, list or drop every table in the tenant, and a
// node's credential reference can attach a secret the workflow never used — all
// through an endpoint the session is legitimately allowed to call.
//
// So the document itself is checked, at every point where an embed session can
// give one to the server: a save, a publish, a restore, and a run. The check is
// the same one in all four places, against the confinement minted into the
// session token from the revision the workflow's owner published — never
// against the document being checked, which cannot be its own authority.

// embedDocumentProblem refuses a document that reaches outside the confinement
// of the embed session making the request.
//
// It answers 403 rather than 422: the document may be perfectly valid, and the
// caller is an integrator who needs to know the session's authority is narrower
// than their editor assumed. The detail names each offending node, because a
// refusal nobody can act on just moves the debugging into the host's logs.
//
// A request with no embed session — the internal dashboard, an operator's API
// key — is unaffected: its caller is the tenant, and the tenant may reference
// anything it owns.
func embedDocumentProblem(ctx context.Context, document workflow.Document) error {
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		return nil
	}
	issues := nodes.EmbedScopeIssues(document, session.Confinement)
	if len(issues) == 0 {
		return nil
	}
	return huma.Error403Forbidden(
		"this embed session cannot use that workflow document: " + strings.Join(issues, "; "))
}

// embedConfinementOf derives the confinement a session for this workflow
// carries, from the revision its owner published.
//
// The active revision is the right source and the latest draft is the fallback.
// Activation and publishing are refused to embed sessions by the routing
// middleware, so the active revision is the last document a trusted caller
// authored — and the point of minting the confinement here rather than deriving
// it per request is that a revision poisoned before this check existed can no
// longer authorise itself.
//
// A workflow that has never been activated confines its session to whatever its
// latest draft references. That draft predates the session, so it was authored
// by the owner.
func embedConfinementOf(stored workflow.StoredWorkflow) embed.Confinement {
	revision := stored.LatestVersion
	if stored.ActiveVersion != nil {
		revision = *stored.ActiveVersion
	}
	return nodes.DocumentReferences(revision.Document)
}

// embedVersionProblem checks a stored revision an embed session is about to
// make the workflow's own: a publish, or a restore that appends it as a new
// draft.
//
// Both endpoints are inside a session's write authority — publishing is how a
// draft becomes the version production traffic runs — so a revision saved
// before the confinement existed, or saved by an owner against broader rules,
// must be re-checked at the moment it becomes live rather than trusted because
// it is already stored.
func (handler *Workflows) embedVersionProblem(ctx context.Context, workflowID, versionID string) error {
	if _, embedded := middleware.EmbedSessionFrom(ctx); !embedded {
		return nil
	}
	version, err := handler.workflows.GetVersionByID(ctx, handler.tenant(ctx), workflowID, versionID)
	if err != nil {
		return handler.problem(ctx, err)
	}
	return embedDocumentProblem(ctx, version.Document)
}

// embedStoredProblem checks the revision a run would actually compile.
//
// A manual run queues the workflow's latest revision, so that is the document
// checked — not the active one. The distinction matters: an old, unconfined
// revision can be the latest one, and a run is exactly the moment it would
// otherwise execute with the tenant's authority. Checking here rather than in
// the engine is deliberate: the embed session is an HTTP-layer identity, and
// this is the only route by which one can start a run.
func (handler *Workflows) embedStoredProblem(ctx context.Context, workflowID string) error {
	if _, embedded := middleware.EmbedSessionFrom(ctx); !embedded {
		return nil
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), workflowID)
	if err != nil {
		return handler.problem(ctx, err)
	}
	return embedDocumentProblem(ctx, stored.LatestVersion.Document)
}

// embedAllowsCredential reports whether an embed session may see one credential.
//
// An internal caller sees every credential in its tenant. An embed session sees
// only the ids its confinement names, so the picker offers exactly what the
// session's document may attach instead of every name the tenant stores.
func embedAllowsCredential(ctx context.Context, credentialID string) bool {
	session, embedded := middleware.EmbedSessionFrom(ctx)
	if !embedded {
		return true
	}
	return session.Confinement.AllowsCredential(credentialID)
}
