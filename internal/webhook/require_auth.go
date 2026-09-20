package webhook

import (
	"net/http"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// RequireAuthentication is the deployment switch that refuses a delivery to any
// trigger that does not authenticate its callers.
//
// It is a builder rather than a NewHandler argument so composition reads like
// the rest of the wiring, and because the zero value has to mean "off": every
// database migrated from before the setting existed must keep behaving exactly
// as it did.
func (handler *Handler) RequireAuthentication(required bool) *Handler {
	handler.requireAuth = required
	return handler
}

// LogPosture states, once at boot, whether inbound webhooks require
// authentication.
//
// Both directions are stated rather than only the interesting one: an operator
// reading a log needs to know which posture they are running, and silence would
// mean collating the config file and the environment to find out. It is Info
// rather than Warn because open is the deliberate default for an imported
// workflow, not a surprise.
func (handler *Handler) LogPosture() {
	if handler.requireAuth {
		handler.logger.Info("inbound webhooks require authentication", "require_auth", true)
		return
	}
	handler.logger.Info("inbound webhooks accept unauthenticated deliveries unless the trigger sets its own authentication",
		"require_auth", false, "enable_with", "KILASFLOW_WEBHOOK_REQUIRE_AUTH=true")
}

// authenticates reports whether a binding's trigger checks who is calling.
//
// The mode is extracted exactly as authenticate() extracts it — the same type
// assertion, no trimming — so the two stay in step: a non-string value reads as
// no mode here and gets the same answer there, and a mode that is neither empty
// nor "none" is left to authenticate() to accept or reject as it always has.
//
// An IP allow-list is deliberately not authentication: it restricts by network
// address rather than by credential, and it is weak wherever the peer address is
// a proxy's.
func (handler *Handler) authenticates(r *http.Request, binding repository.WebhookBinding) bool {
	authentication, _ := binding.Parameters["authentication"].(string)
	if authentication != "" && authentication != "none" {
		return true
	}
	kind := handler.triggers.Lookup(binding.NodeType)
	if kind.Verify == nil {
		return false
	}
	if kind.Verifies == nil {
		return true
	}
	return kind.Verifies(Delivery{Request: r, Binding: binding})
}

// refuseUnauthenticated answers the one caller who can act on the reason.
//
// The workflow is named by id rather than by name because a binding carries no
// name, and the route is already the capability that reached this point, so the
// id adds nothing a caller holding the route did not have.
func (handler *Handler) refuseUnauthenticated(w http.ResponseWriter, binding repository.WebhookBinding) {
	handler.logger.Warn("a delivery was refused because webhook.require_auth is set and this trigger does not authenticate its callers",
		"route", binding.Route, "tenant", binding.TenantID, "workflow", binding.WorkflowID, "node", binding.NodeID)
	problem(w, http.StatusForbidden, "This deployment requires webhook authentication (webhook.require_auth) and workflow "+
		binding.WorkflowID+" does not authenticate this trigger. Set the trigger node's Authentication to Basic auth, "+
		"Header auth or JWT auth and attach a credential, then activate the workflow again.")
}
