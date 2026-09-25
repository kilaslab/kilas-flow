package handlers

import (
	"context"
	"errors"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// This file is where a confined caller's document confinement is derived and
// enforced. Two callers are confined: an embed session, and a scoped API key
// (an agent token), which the routing middleware narrows by the same table.
//
// The routing middleware already confines a session to one workflow, one
// datastore, or one row set. That is a bound on *what it may call*, and it is
// not enough: a session holding workflow:write may save any graph into its own
// workflow, and the graph then acts with the tenant's authority. A Data table
// node can name a sibling table, list or drop every table in the tenant, and a
// node's credential reference can attach a secret the workflow never used — all
// through an endpoint the session is legitimately allowed to call.
//
// So the document itself is checked, at every point where a confined caller can
// give one to the server: a create, a save, a publish, a restore, a run and a
// retry. The check is the same one in every place.
//
// What it asks differs by caller, and deliberately:
//
//   - An embed session is held to the confinement minted into its token from
//     the revision the workflow's owner published — never against the document
//     being checked, which cannot be its own authority — and to the credential
//     rule below.
//   - A scoped key is held to the credential rule alone. It has no minted
//     confinement: it is the tenant's own automation, narrowed in which
//     endpoints it may call, and choosing which of the tenant's credentials a
//     workflow uses is inside that authority (§3 of the agent-surface design).
//     What is not inside it is reading a secret, which no endpoint of the
//     tenant's own does either.
//   - Both may not use a document that attaches an unscoped credential, one that
//     would follow a request to any host. Either caller may edit where a request
//     goes — an HTTP node's URL, a model node's base URL — so a credential with
//     no scope is, in their hands, a way to read its secret off the wire. With a
//     scope, the credential is refused at every host outside it whatever URL
//     they type, so no finer rule about node types and target hosts is needed:
//     that one would have to predict a URL an expression decides at run time.

// confinedCaller reports whether the request comes from a caller whose document
// is checked, and the words a refusal names it by.
func confinedCaller(ctx context.Context) (embed.Session, bool, string, bool) {
	if session, embedded := middleware.EmbedSessionFrom(ctx); embedded {
		return session, true, "this embed session", true
	}
	if principal, found := auth.PrincipalFrom(ctx); found && principal.Kind == auth.KindAPIKey && principal.Scoped() {
		return embed.Session{}, false, "this agent token", true
	}
	return embed.Session{}, false, "", false
}

// confinedDocumentProblem refuses a document that reaches outside what the
// confined caller making the request may reach.
//
// It answers 403 rather than 422: the document may be perfectly valid, and the
// caller is an integrator who needs to know the session's authority is narrower
// than their editor assumed. The detail names each offending node, because a
// refusal nobody can act on just moves the debugging into the host's logs.
//
// A request from an unconfined caller — the internal dashboard, the tenant's
// own key — is unaffected: its caller is the tenant, and the tenant may
// reference anything it owns.
func confinedDocumentProblem(ctx context.Context, store repository.CredentialRepository, tenant repository.TenantScope, document workflow.Document) error {
	session, embedded, caller, confined := confinedCaller(ctx)
	if !confined {
		return nil
	}
	var issues []string
	// A session is not told anything about a credential outside its grant —
	// not even whether it is scoped — so those ids answer only to the grant.
	visible := func(string) bool { return true }
	if embedded {
		issues = nodes.EmbedScopeIssues(document, session.Confinement)
		visible = session.Confinement.AllowsCredential
	}
	unscoped, err := nodes.UnscopedCredentialIssues(document, func(credentialID string) (credentials.Record, bool, error) {
		if !visible(credentialID) {
			return credentials.Record{}, false, nil
		}
		if store == nil {
			return credentials.Record{}, false, errNoCredentialStore
		}
		record, err := store.Get(ctx, tenant, credentialID)
		if errors.Is(err, repository.ErrNotFound) {
			// Nothing stored under that id is nothing to send: the run fails
			// on the missing credential by name.
			return credentials.Record{}, false, nil
		}
		return record, err == nil, err
	})
	if errors.Is(err, errNoCredentialStore) {
		// Without the store the scope cannot be read, and an unchecked
		// credential is exactly the hole this closes.
		return huma.Error503ServiceUnavailable("credential storage unavailable: " + caller + " cannot attach a credential here")
	}
	if err != nil {
		return serverProblem(ctx, "credential scope check failed", err)
	}
	issues = append(issues, unscoped...)
	if len(issues) == 0 {
		return nil
	}
	return huma.Error403Forbidden(caller + " cannot use that workflow document: " + strings.Join(issues, "; "))
}

// errNoCredentialStore marks a document check run by a handler with no
// credential store wired.
var errNoCredentialStore = errors.New("credential storage is not configured")

// confinedDocumentProblem is the handler's form of the check, bound to its
// credential store and the caller's tenant.
func (handler *Workflows) confinedDocumentProblem(ctx context.Context, document workflow.Document) error {
	return confinedDocumentProblem(ctx, handler.credentials, handler.tenant(ctx), document)
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

// confinedVersionProblem checks a stored revision a confined caller is about to
// make the workflow's own or run: a publish, a restore that appends it as a new
// draft, or a run pinned to it.
//
// Those endpoints are inside a session's write authority — publishing is how a
// draft becomes the version production traffic runs — so a revision saved
// before the confinement existed, or saved by an owner against broader rules,
// must be re-checked at the moment it becomes live rather than trusted because
// it is already stored.
func (handler *Workflows) confinedVersionProblem(ctx context.Context, workflowID, versionID string) error {
	if _, _, _, confined := confinedCaller(ctx); !confined {
		return nil
	}
	version, err := handler.workflows.GetVersionByID(ctx, handler.tenant(ctx), workflowID, versionID)
	if err != nil {
		return handler.problem(ctx, err)
	}
	return handler.confinedDocumentProblem(ctx, version.Document)
}

// confinedStoredProblem checks the revision a run would actually compile.
//
// A manual run queues the workflow's latest revision, so that is the document
// checked — not the active one. The distinction matters: an old, unconfined
// revision can be the latest one, and a run is exactly the moment it would
// otherwise execute with the tenant's authority. Checking here rather than in
// the engine is deliberate: an embed session and a scoped key are HTTP-layer
// identities, and this is the route by which one starts a run.
func (handler *Workflows) confinedStoredProblem(ctx context.Context, workflowID string) error {
	if _, _, _, confined := confinedCaller(ctx); !confined {
		return nil
	}
	stored, err := handler.workflows.Get(ctx, handler.tenant(ctx), workflowID)
	if err != nil {
		return handler.problem(ctx, err)
	}
	return handler.confinedDocumentProblem(ctx, stored.LatestVersion.Document)
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
