// Package metrics is the server's own observability seam: the series the
// engine, the webhook boundary and the operator's scraper agree on, behind an
// interface those packages can report through without any exposition-format
// knowledge of their own.
//
// There is no Prometheus client library in this package, deliberately. The
// text exposition format is small and stable, the series set is fixed, and the
// one behaviour no client library gives away — bounding the tenant label to
// the top-N tenants, folding the rest — needs the series in hand anyway. A
// future adoption of a client library is a change here and here only: every
// producer talks to the Observer interface, never to a registry.
package metrics

import "time"

// Observer is the whole reporting surface. The engine and the webhook
// boundary take it as this interface, so a test can stub it and a deployment
// can run without one: every method is safe to leave uncalled, and a nil
// Observer is the documented off state — callers guard with a nil check
// rather than the registry tolerating a typed nil.
type Observer interface {
	// ExecutionClaimed reports a worker taking an execution off the queue.
	// waited is how long the execution sat queued before the claim.
	ExecutionClaimed(tenant, trigger string, waited time.Duration)
	// ExecutionStarted reports a claimed execution beginning to run.
	ExecutionStarted(tenant, trigger string)
	// ExecutionSettled reports a terminal transition: succeeded, failed or
	// cancelled.
	ExecutionSettled(tenant, trigger, status string)
	// NodeRun reports one node's finished run inside an execution.
	NodeRun(tenant, nodeType string, took time.Duration)
	// WebhookRequest reports one inbound webhook request's outcome, one of
	// WebhookAccepted, WebhookFiltered or WebhookRefused.
	WebhookRequest(tenant, outcome string)
}

// The webhook outcomes, the closed set the webhook series carries. A request
// that reached the surface and was answered with anything but a success is
// refused whatever the specific status was: the operator's question is "how
// much is being turned away", not "which status code".
const (
	// WebhookAccepted: the delivery was consumed — it queued a run, was
	// answered as a duplicate of one that had, or served a hosted page.
	WebhookAccepted = "accepted"
	// WebhookFiltered: the delivery was received correctly and deliberately
	// not acted on — the trigger's own filter or the ignore-bots option. A
	// 200, on purpose: the sender should stop retrying.
	WebhookFiltered = "filtered"
	// WebhookRefused: the request was turned away — no binding, failed
	// authentication, a body over the limit, a queue failure.
	WebhookRefused = "refused"
)

// The tenant label modes. Off is the default because a tenant-per-series
// label is a cardinality decision only the operator can make: a deployment
// with three tenants pays nothing for it, and one with fifty thousand pays
// for it on every scrape.
const (
	// TenantLabelOff records no tenant label at all.
	TenantLabelOff = "off"
	// TenantLabelOn records a tenant label on every series that has one,
	// unbounded.
	TenantLabelOn = "on"
	// TenantLabelTop records the tenant label only for the top-N tenants by
	// observation count, folding the rest into TenantOther.
	TenantLabelTop = "top"
)

// TenantOther is the label value the top-N mode folds every other tenant
// into. A name rather than an empty string, so a folded series is visibly a
// fold rather than a series that never knew its tenant.
const TenantOther = "other"

// KnownTenantLabels lists the modes the configuration accepts, in the order
// the documentation presents them.
func KnownTenantLabels() []string {
	return []string{TenantLabelOff, TenantLabelOn, TenantLabelTop}
}
