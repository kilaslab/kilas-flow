package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"gorm.io/gorm"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// WebhookBinding is one routable inbound endpoint.
//
// Bindings exist only for active workflows, so routing never has to ask
// whether a workflow is active — an unroutable workflow simply has no row.
type WebhookBinding struct {
	TenantID          string
	WorkflowID        string
	WorkflowVersionID string
	NodeID            string
	Method            string
	// Route is the opaque segment the public URL carries.
	Route string
	// Path is the author's label — what the n8n template called this endpoint.
	Path string
	// Parameters is the trigger node's configuration, carried so the HTTP
	// boundary can authenticate and choose a response mode without recompiling
	// the document on every request.
	Parameters map[string]any
}

// WebhookTrigger describes one webhook node found in a document. The workflow
// package stays free of node-type knowledge, so the caller supplies these.
type WebhookTrigger struct {
	NodeID     string
	Method     string
	Path       string
	Parameters map[string]any
}

// WebhookExtractor pulls the routable triggers out of a document. Injecting it
// keeps the repository free of node-type knowledge while still letting binding
// sync happen inside the activation transaction.
type WebhookExtractor func(workflow.Document) []WebhookTrigger

// WebhookRepository resolves inbound requests to their binding.
type WebhookRepository interface {
	Resolve(ctx context.Context, method, route string) (WebhookBinding, error)
	// ClaimDelivery dedupes a retried delivery. It returns the execution that
	// owns the identifier and whether this caller won the claim.
	ClaimDelivery(ctx context.Context, route, deliveryID, executionID string, window time.Duration) (string, bool, error)
	// RecordDeliveryExecution attaches the queued execution to that claim.
	RecordDeliveryExecution(ctx context.Context, route, deliveryID, executionID string) error
}

var _ WebhookRepository = (*GORMWorkflowStore)(nil)

// Resolve finds the active binding for one inbound request.
//
// It is deliberately not tenant-scoped: an inbound webhook has no session, and
// the route is the only thing identifying it. The binding it returns carries
// the tenant, which every downstream operation is then scoped by. Because the
// route is opaque and globally unique, exactly one row can match — a
// tenant-scoped index without a tenant in the URL would have produced two rows
// matching one request, which is a cross-tenant routing bug rather than a
// refused activation.
//
// A row whose route is empty was activated before routes were minted; its path
// is its route, so those URLs keep working without reactivation.
func (store *GORMWorkflowStore) Resolve(ctx context.Context, method, route string) (WebhookBinding, error) {
	var model webhookBindingModel
	if err := store.db.WithContext(ctx).
		Where("method = ? AND (route = ? OR (route = '' AND path = ?))", method, route, route).
		First(&model).Error; err != nil {
		return WebhookBinding{}, mapNotFound(err, "webhook binding")
	}
	parameters := map[string]any{}
	if len(model.Parameters) > 0 {
		if err := json.Unmarshal(model.Parameters, &parameters); err != nil {
			return WebhookBinding{}, fmt.Errorf("decode webhook parameters: %w", err)
		}
	}
	binding := WebhookBinding{
		TenantID: model.TenantID, WorkflowID: model.WorkflowID, WorkflowVersionID: model.WorkflowVersionID,
		NodeID: model.NodeID, Method: model.Method, Route: model.Route, Path: model.Path, Parameters: parameters,
	}
	if binding.Route == "" {
		binding.Route = model.Path
	}
	return binding, nil
}

// syncWebhookBindings replaces a workflow's bindings inside the caller's
// transaction.
//
// Doing this in the same transaction as activation is what keeps routing
// consistent: there is no window in which a workflow is active but unroutable,
// or routable but inactive.
func syncWebhookBindings(tx *gorm.DB, tenantID, workflowID, versionID string, triggers []WebhookTrigger) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).Delete(&webhookBindingModel{}).Error; err != nil {
		return fmt.Errorf("clear webhook bindings: %w", err)
	}
	now := time.Now().UTC()
	for _, trigger := range triggers {
		if trigger.Path == "" || trigger.Method == "" {
			continue
		}
		parameters, err := json.Marshal(trigger.Parameters)
		if err != nil {
			return fmt.Errorf("encode webhook parameters: %w", err)
		}
		route, err := mintWebhookRoute(tx, tenantID, workflowID, trigger.NodeID)
		if err != nil {
			return err
		}
		binding := webhookBindingModel{
			TenantID: tenantID, WorkflowID: workflowID, WorkflowVersionID: versionID, NodeID: trigger.NodeID,
			Method: trigger.Method, Route: route, Path: trigger.Path, Parameters: parameters, CreatedAt: now,
		}
		if err := tx.Create(&binding).Error; err != nil {
			// Two indexes can fail here and they mean different things. The
			// per-tenant one on (tenant, path) is the rule that still holds:
			// one tenant cannot claim the same endpoint twice. The global one
			// on the minted route can only fire on a minting bug, and saying
			// "already claimed" for that would send someone hunting for a
			// workflow that does not exist.
			if isRouteCollision(err) {
				return fmt.Errorf("webhook route collision for node %q; this is a minting fault, not a claimed path", trigger.NodeID)
			}
			return fmt.Errorf("webhook path %q is already claimed by another active workflow in this tenant", trigger.Path)
		}
	}
	return nil
}

// removeWebhookBindings drops every binding a workflow owns.
func removeWebhookBindings(tx *gorm.DB, tenantID, workflowID string) error {
	if err := tx.Where("tenant_id = ? AND workflow_id = ?", tenantID, workflowID).Delete(&webhookBindingModel{}).Error; err != nil {
		return fmt.Errorf("remove webhook bindings: %w", err)
	}
	return nil
}

// webhookRouteBytes is how much entropy a minted route carries. Sixteen bytes
// is far beyond guessable, and an unguessable route is a meaningful defence for
// an endpoint that is very often unauthenticated.
const webhookRouteBytes = 16

// mintWebhookRoute returns the opaque route for one trigger node, creating it
// on first activation and reusing it forever after.
//
// Reuse is the whole point. A route minted per activation would change the
// public URL every time a workflow was deactivated and reactivated, which
// breaks every sender already configured against it.
func mintWebhookRoute(tx *gorm.DB, tenantID, workflowID, nodeID string) (string, error) {
	var existing webhookRouteModel
	err := tx.Where("tenant_id = ? AND workflow_id = ? AND node_id = ?", tenantID, workflowID, nodeID).
		First(&existing).Error
	if err == nil {
		return existing.Route, nil
	}
	if !errors.Is(err, gorm.ErrRecordNotFound) {
		return "", fmt.Errorf("read webhook route: %w", err)
	}

	raw := make([]byte, webhookRouteBytes)
	if _, err := rand.Read(raw); err != nil {
		return "", fmt.Errorf("mint webhook route: %w", err)
	}
	minted := webhookRouteModel{
		TenantID: tenantID, WorkflowID: workflowID, NodeID: nodeID,
		Route: hex.EncodeToString(raw), CreatedAt: time.Now().UTC(),
	}
	if err := tx.Create(&minted).Error; err != nil {
		return "", fmt.Errorf("record webhook route: %w", err)
	}
	return minted.Route, nil
}

// isRouteCollision distinguishes the global route index from the per-tenant
// path index, which fail for entirely different reasons.
func isRouteCollision(err error) bool {
	return err != nil && strings.Contains(strings.ToLower(err.Error()), "uidx_webhook_bindings_route")
}

// WebhookRoutes lists the public routes a workflow's triggers answer on, so an
// import can tell the user what to paste into the sending system.
//
// Without this the user has an activated workflow and no way to learn its
// address, which is the same failure as not importing it at all.
func (store *GORMWorkflowStore) WebhookRoutes(ctx context.Context, tenant TenantScope, workflowID string) ([]WebhookBinding, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []webhookBindingModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND workflow_id = ?", tenant.ID, workflowID).
		Order("node_id ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list webhook routes: %w", err)
	}
	bindings := make([]WebhookBinding, 0, len(models))
	for _, model := range models {
		route := model.Route
		if route == "" {
			route = model.Path
		}
		bindings = append(bindings, WebhookBinding{
			TenantID: model.TenantID, WorkflowID: model.WorkflowID, WorkflowVersionID: model.WorkflowVersionID,
			NodeID: model.NodeID, Method: model.Method, Route: route, Path: model.Path,
		})
	}
	return bindings, nil
}

// DefaultDeliveryWindow is how long a delivery identifier is remembered.
//
// It has to outlast the sender's whole retry sequence or the last retry misses
// and runs the workflow a second time. WAHA retries fifteen times at two-second
// intervals — thirty seconds — and five minutes leaves room for a sender that
// backs off rather than retrying at a fixed rate.
const DefaultDeliveryWindow = 5 * time.Minute

// ClaimDelivery records one logical delivery and reports whether this request
// is the first to arrive with that identifier.
//
// The record is written *before* the execution is queued, and by an insert that
// fails on conflict rather than a read followed by a write: two retries landing
// concurrently would both pass a read-then-write and both queue a run.
//
// A claim that loses returns the execution the winner queued, so the duplicate
// can be answered with the same outcome instead of running the workflow again.
func (store *GORMWorkflowStore) ClaimDelivery(ctx context.Context, route, deliveryID, executionID string, window time.Duration) (string, bool, error) {
	if route == "" || deliveryID == "" {
		// Nothing to dedupe on. A delivery with no identifier is never
		// collapsed with another, because the only alternative — hashing the
		// body — would treat two genuinely identical messages as one.
		return "", true, nil
	}
	if window <= 0 {
		window = DefaultDeliveryWindow
	}
	now := time.Now().UTC()

	// An expired claim is not a duplicate. Clearing it first keeps the unique
	// index from rejecting a legitimate later delivery with the same
	// identifier.
	if err := store.db.WithContext(ctx).
		Where("route = ? AND delivery_id = ? AND expires_at < ?", route, deliveryID, now).
		Delete(&webhookDeliveryModel{}).Error; err != nil {
		return "", false, fmt.Errorf("clear expired delivery: %w", err)
	}

	claim := webhookDeliveryModel{
		Route: route, DeliveryID: deliveryID, ExecutionID: executionID,
		ExpiresAt: now.Add(window), CreatedAt: now,
	}
	if err := store.db.WithContext(ctx).Create(&claim).Error; err == nil {
		return executionID, true, nil
	}

	var existing webhookDeliveryModel
	if err := store.db.WithContext(ctx).
		Where("route = ? AND delivery_id = ?", route, deliveryID).
		First(&existing).Error; err != nil {
		return "", false, fmt.Errorf("read existing delivery: %w", err)
	}
	return existing.ExecutionID, false, nil
}

// RecordDeliveryExecution attaches the queued execution to a claim taken before
// that execution existed.
func (store *GORMWorkflowStore) RecordDeliveryExecution(ctx context.Context, route, deliveryID, executionID string) error {
	if route == "" || deliveryID == "" || executionID == "" {
		return nil
	}
	return store.db.WithContext(ctx).Model(&webhookDeliveryModel{}).
		Where("route = ? AND delivery_id = ?", route, deliveryID).
		Update("execution_id", executionID).Error
}

// EnsureWebhookRoutes mints the public routes for a workflow's webhook triggers
// without activating it, and returns them.
//
// An import saves a draft, so no binding exists yet and there would be nothing
// to tell the user. Minting here is safe because a route is keyed by
// (tenant, workflow, node) and reused forever: activation finds the same one
// rather than replacing it, so the URL reported at import is the URL the
// endpoint eventually answers on.
func (store *GORMWorkflowStore) EnsureWebhookRoutes(ctx context.Context, tenant TenantScope, workflowID string, document workflow.Document) ([]WebhookBinding, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	if store.webhooks == nil {
		return nil, nil
	}
	triggers := store.webhooks(document)
	bindings := make([]WebhookBinding, 0, len(triggers))
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		for _, trigger := range triggers {
			if trigger.Path == "" || trigger.Method == "" {
				continue
			}
			route, err := mintWebhookRoute(tx, tenant.ID, workflowID, trigger.NodeID)
			if err != nil {
				return err
			}
			bindings = append(bindings, WebhookBinding{
				TenantID: tenant.ID, WorkflowID: workflowID, NodeID: trigger.NodeID,
				Method: trigger.Method, Route: route, Path: trigger.Path,
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	return bindings, nil
}

// WebhookRouteMinter is the optional half of the workflow repository that can
// mint public routes ahead of activation. It is separate from
// WorkflowRepository so a store assembled without a webhook extractor is still
// a complete workflow repository.
type WebhookRouteMinter interface {
	EnsureWebhookRoutes(ctx context.Context, tenant TenantScope, workflowID string, document workflow.Document) ([]WebhookBinding, error)
}
