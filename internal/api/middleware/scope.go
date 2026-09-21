// The authority gate: what a narrowed credential may do, asked of the request
// rather than of the handler.
//
// Two credentials are confined this way and they answer the same questions
// about the same path shapes, so the table below is one table. An embed session
// is confined to a single subject — one workflow or one datastore — and is
// minted by a host page. A scoped API key (§3 of the agent-surface design) is
// the tenant's own key with a scope list, optionally bound to one workflow. The
// difference between them is where the subject comes from and the handful of
// arms where the answer genuinely differs, each marked with the kind it asks.
//
// Design §3.2 is the contract: the refusals below apply to scoped keys and
// leave a legacy tenant-wide key untouched, because that is what keeps an
// operator's existing automation working. Design §3.3 is why this is a
// middleware arm rather than a check in each handler: the rule is about what
// the request targets, and a route added later is refused by the default arm
// rather than by an author remembering to add a check.
package middleware

import (
	"net/http"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

// subjectKind separates the two credentials the table below confines.
type subjectKind int

const (
	kindEmbed subjectKind = iota
	kindKey
)

// subject is what one request is confined to.
type subject interface {
	// Allows reports whether the subject carries a scope, with the embed
	// vocabulary's implication rules.
	Allows(scope embed.Scope) bool
	// Kind names the credential, for the arms where the two differ rather than
	// merely being asked of a different holder.
	Kind() subjectKind
	// BoundWorkflow is the workflow the subject may address, empty when it may
	// address any workflow in its tenant.
	BoundWorkflow() string
	// Denial renders a refusal in the subject's own words, so a caller reads
	// which credential refused it.
	Denial(reason string) string
}

// sessionSubject is an embed session as the table sees it.
type sessionSubject struct{ session embed.Session }

func (subject sessionSubject) Allows(scope embed.Scope) bool { return subject.session.Allows(scope) }
func (subject sessionSubject) Kind() subjectKind             { return kindEmbed }
func (subject sessionSubject) BoundWorkflow() string         { return subject.session.WorkflowID }

func (subject sessionSubject) Denial(reason string) string {
	return "This embed session " + reason + "."
}

// keySubject is a scoped API key as the table sees it.
//
// Only a scoped key reaches this type: the tenant-wide key that carries no
// scopes is the legacy credential and is not confined by this arm at all.
type keySubject struct{ principal auth.Principal }

func (subject keySubject) Allows(scope embed.Scope) bool {
	// The same implication rules the embed vocabulary uses: a key that can
	// write must be able to read, or it could save a document it cannot load.
	for _, held := range subject.principal.Scopes {
		if held == scope {
			return true
		}
		if scope == embed.ScopeRead && (held == embed.ScopeWrite || held == embed.ScopeRun) {
			return true
		}
		if scope == embed.ScopeDatastoreRead && held == embed.ScopeDatastoreWrite {
			return true
		}
	}
	return false
}

func (subject keySubject) Kind() subjectKind     { return kindKey }
func (subject keySubject) BoundWorkflow() string { return subject.principal.WorkflowID }

func (subject keySubject) Denial(reason string) string {
	return "This agent token " + reason + "."
}

// ScopeAuth confines a scoped API key to its scope list and its binding.
//
// Mounted in the same chain position as EmbedAuth, and inert for every other
// caller: a session, a tenant-wide key and an unauthenticated request all pass
// through untouched, so nothing that worked before scopes existed changes.
func ScopeAuth() func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			principal, found := auth.PrincipalFrom(r.Context())
			if !found || principal.Kind != auth.KindAPIKey || !principal.Scoped() {
				next.ServeHTTP(w, r)
				return
			}
			if status, detail := permits(keySubject{principal: principal}, r); status != 0 {
				deny(w, status, detail)
				return
			}
			next.ServeHTTP(w, r)
		})
	}
}

// permits decides whether one request is inside a subject's authority.
//
// The rule is deliberately about *what the request targets*, not about which
// handler will run, and a refusal carries the status the caller should see: 403
// where the subject is told it may not, 404 where the subject must not learn
// whether the resource exists. A status of zero means the request is inside the
// subject's authority.
func permits(subject subject, r *http.Request) (int, string) {
	path := strings.TrimPrefix(r.URL.Path, "/api/v1")
	bound := subject.BoundWorkflow()

	switch {
	case path == "/auth/me" && r.Method == http.MethodGet:
		// A scoped key has to be able to describe itself: `kilasflow auth
		// whoami` is how the CLI learns its own scopes before a guarded verb,
		// and the answer is the caller's own identity rather than tenant data.
		// An embed session has no such need.
		if subject.Kind() != kindKey {
			return http.StatusForbidden, subject.Denial("cannot use this endpoint")
		}
		return 0, ""

	case path == "/node-types" || strings.HasPrefix(path, "/node-types/"):
		// The editor cannot render without the node catalogue, and the
		// catalogue handler narrows itself to the session's tenant, so it
		// carries only what that tenant may see.
		//
		// The load-options endpoint under this prefix does, so it is not
		// covered by that reasoning: it can reach a customer's service, and an
		// internal loader reads this process's own state. The handler bounds it
		// by the session's WorkflowID rather than only its tenant, because
		// nothing here checks a workflow — see LoadOptions.
		return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")

	case path == "/credential-types" || strings.HasPrefix(path, "/credential-types/"):
		// A node references a credential by id, so building that reference
		// needs the type catalogue — and the payload validator that goes with
		// it spends a live call against the customer's service, so it is part
		// of credential mutation rather than of reading.
		if subject.Kind() != kindKey || r.Method != http.MethodGet {
			return http.StatusForbidden, subject.Denial("cannot manage credentials")
		}
		return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")

	case path == "/credentials" || strings.HasPrefix(path, "/credentials/"):
		if r.Method != http.MethodGet {
			return http.StatusForbidden, subject.Denial("cannot manage credentials")
		}
		if path == "/credentials" {
			// Credential *names* are needed to render a node's credential
			// picker. Values are never returned by this endpoint.
			return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")
		}

		// Reading one credential is the other half of the same need, and it
		// returns the public half only. Everything else under this prefix —
		// create, update, delete, and the test that spends a stored secret —
		// is credential mutation and stays with a human (§3.2). Only a scoped
		// key reaches this arm: an embed session is refused the whole prefix by
		// the kind check, the way the default arm refused it before scopes
		// existed.
		if subject.Kind() != kindKey || strings.HasSuffix(path, "/test") {
			return http.StatusForbidden, subject.Denial("cannot manage credentials")
		}
		return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")

	case path == "/workflows/import":
		// Importing creates a *new* workflow, which is outside any session's
		// single-workflow authority and outside a scoped key's list (§3.2).
		return http.StatusForbidden, subject.Denial("cannot import workflows")

	case path == "/workflows":
		// Listing the tenant's workflows is only meaningful for a credential
		// that is not confined to one. An embed session always is, and so is a
		// workflow-bound key: its subject is one workflow, and the listing
		// would show it every sibling name.
		if subject.Kind() != kindKey || bound != "" {
			return http.StatusForbidden, subject.Denial("cannot list workflows")
		}
		if r.Method == http.MethodGet {
			return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")
		}
		return refused(subject, subject.Allows(embed.ScopeWrite), "is read-only")

	case strings.HasPrefix(path, "/workflows/"):
		rest := strings.TrimPrefix(path, "/workflows/")
		workflowID, action, _ := strings.Cut(rest, "/")
		if bound != "" && workflowID != bound {
			if subject.Kind() == kindKey {
				// A bound token is told the workflow does not exist rather
				// than that it may not read it: a 403 would confirm the id is
				// real, which is exactly what a token confined to one workflow
				// must not be able to ask (§3.2, criterion 4).
				return http.StatusNotFound, "Workflow not found."
			}
			return http.StatusForbidden, subject.Denial("is scoped to a different workflow")
		}
		switch {
		case action == "run":
			return refused(subject, subject.Allows(embed.ScopeRun), "cannot run workflows")
		case action == "activate" || action == "deactivate":
			// Activation publishes a webhook endpoint for the whole
			// deployment; that is an owner action, not an embed one.
			return http.StatusForbidden, subject.Denial("cannot change activation")
		case strings.HasSuffix(action, "publish") && subject.Kind() == kindKey:
			// Publishing a revision is activation by another name: it pins the
			// revision production traffic runs. An embed session keeps its
			// existing behaviour — this arm is about scoped keys.
			return http.StatusForbidden, subject.Denial("cannot change activation")
		case r.Method == http.MethodDelete:
			return http.StatusForbidden, subject.Denial("cannot delete a workflow")
		case r.Method == http.MethodGet:
			return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")
		default:
			return refused(subject, subject.Allows(embed.ScopeWrite), "is read-only")
		}

	case path == "/executions":
		// A listing must be narrowed to the subject's own workflow. Without
		// this, an embedded editor could page through every execution in the
		// tenant, including workflows it was never granted.
		if !subject.Allows(embed.ScopeRead) {
			return http.StatusForbidden, subject.Denial("cannot read executions")
		}
		if bound != "" && r.URL.Query().Get("workflowId") != bound {
			return http.StatusForbidden, subject.Denial("must list executions of its own workflow")
		}
		return 0, ""

	case strings.HasPrefix(path, "/executions/"):
		// Which workflow a single execution belongs to is only knowable by
		// loading it, so the ownership check lives in the handler. This gate
		// covers the scope; ownsExecution covers the identity.
		return refused(subject, subject.Allows(embed.ScopeRead), "cannot read executions")

	case path == "/datastores":
		// Listing every table in the tenant leaks sibling names, and creating
		// one is schema work — the datastore equivalent of the workflow import
		// refused above. A key that is not bound to a workflow may read the
		// list; nothing may create one here.
		if subject.Kind() != kindKey || bound != "" {
			return http.StatusForbidden, subject.Denial("cannot manage datastores")
		}
		if r.Method == http.MethodGet {
			return refused(subject, subject.Allows(embed.ScopeDatastoreRead), "cannot read")
		}
		return http.StatusForbidden, subject.Denial("cannot manage datastores")

	case strings.HasPrefix(path, "/datastores/"):
		rest := strings.TrimPrefix(path, "/datastores/")
		if rest == "" {
			return http.StatusForbidden, subject.Denial("cannot use this endpoint")
		}
		// The session's datastore identity is checked in the handler, not
		// here: a request naming another datastore must read as unknown
		// (404) rather than as forbidden (403), so this gate covers only
		// the scope and ownsDatastore covers the identity.
		datastoreID, remainder, _ := strings.Cut(rest, "/")
		if datastoreID == "" {
			return http.StatusForbidden, subject.Denial("cannot use this endpoint")
		}
		action, _, _ := strings.Cut(remainder, "/")
		switch {
		case action == "":
			// Reading one table's definition is data-plane; renaming or
			// dropping it is schema work, refused like import and
			// activation above.
			if r.Method == http.MethodGet {
				return refused(subject, subject.Allows(embed.ScopeDatastoreRead), "cannot read")
			}
			return http.StatusForbidden, subject.Denial("cannot manage datastores")
		case action == "clear" || action == "columns":
			// Clearing every row and editing columns reshape the table
			// itself, not its contents.
			return http.StatusForbidden, subject.Denial("cannot manage datastores")
		case action == "rows":
			// Row reads, the single-row read, the CSV export, the filtered
			// update and delete, the upsert, and the CSV import all address
			// contents under one datastore id, so the method alone decides
			// the family: GET reads, the mutating verbs write.
			if r.Method == http.MethodGet {
				return refused(subject, subject.Allows(embed.ScopeDatastoreRead), "cannot read")
			}
			switch r.Method {
			case http.MethodPost, http.MethodPut, http.MethodDelete:
				return refused(subject, subject.Allows(embed.ScopeDatastoreWrite), "is read-only")
			default:
				return http.StatusForbidden, subject.Denial("cannot use this endpoint")
			}
		default:
			return http.StatusForbidden, subject.Denial("cannot use this endpoint")
		}

	case path == "/schedules" || strings.HasPrefix(path, "/schedules/"):
		// A schedule names the workflow it starts in its body, which a path
		// gate cannot read, so a confined credential is refused the whole
		// surface: an embed session because scheduling is backend work, and a
		// workflow-bound key because it could name a sibling workflow.
		if subject.Kind() != kindKey || bound != "" {
			return http.StatusForbidden, subject.Denial("cannot manage schedules")
		}
		if r.Method == http.MethodGet {
			return refused(subject, subject.Allows(embed.ScopeRead), "cannot read")
		}
		return refused(subject, subject.Allows(embed.ScopeWrite), "is read-only")

	case path == "/resume" || strings.HasPrefix(path, "/resume/"):
		// Approval resume is never available to a confined credential:
		// resuming someone else's approval is the confused-deputy shape the
		// restriction exists to stop. The resume handler and the service
		// repeat this denial in depth, so a denied call never consumes its
		// token either way.
		return http.StatusForbidden, subject.Denial("cannot answer an approval")

	default:
		// Listing every workflow, minting another session or another key,
		// managing tenants, streaming an execution's events, installing a
		// pack: none of that belongs to a confined credential, and a route
		// added later lands here rather than in an allow list somebody has to
		// remember to update.
		return http.StatusForbidden, subject.Denial("cannot use this endpoint")
	}
}

// refused turns a scope answer into a refusal, so an allowed arm reads as one
// line rather than a status and an empty detail.
func refused(subject subject, allowed bool, reason string) (int, string) {
	if allowed {
		return 0, ""
	}
	return http.StatusForbidden, subject.Denial(reason)
}
