package repository

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
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
	NodeType          string
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
	NodeType   string
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
	// ResolveRoute returns every binding answering on one route, across
	// methods, which is what a request that names a route but not the method
	// it will use — a CORS preflight — needs to be answered.
	ResolveRoute(ctx context.Context, route string) ([]WebhookBinding, error)
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
func (store *GORMWorkflowStore) Resolve(ctx context.Context, method, route string) (WebhookBinding, error) {
	var model webhookBindingModel
	if err := store.db.WithContext(ctx).
		Where("method = ? AND (route = ? OR (route = '' AND path = ?))", method, route, route).
		First(&model).Error; err != nil {
		return WebhookBinding{}, mapNotFound(err, "webhook binding")
	}
	return bindingFromModel(model)
}

// ResolveRoute returns every binding for one route, across methods.
//
// A route is the routing identity, so a caller that names one — a CORS
// preflight does, before it knows which method the request will use — needs
// the bindings on it rather than the single row Resolve picks by method. A row
// activated before routes were minted answers on its path, so one route can
// carry more than one method's binding; a caller that already knows the method
// calls Resolve, which is the narrower lookup.
//
// The order is by method so a caller can build a stable answer — an Allow
// header — from the slice without sorting it again.
func (store *GORMWorkflowStore) ResolveRoute(ctx context.Context, route string) ([]WebhookBinding, error) {
	// An empty route would match every row activated before routes were
	// minted, whose route column is empty — a lookup for nothing answering
	// with everything.
	if route == "" {
		return nil, mapNotFound(gorm.ErrRecordNotFound, "webhook binding")
	}
	var models []webhookBindingModel
	if err := store.db.WithContext(ctx).
		Where("route = ? OR (route = '' AND path = ?)", route, route).
		Order("method ASC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("resolve webhook route: %w", err)
	}
	if len(models) == 0 {
		return nil, mapNotFound(gorm.ErrRecordNotFound, "webhook binding")
	}
	bindings := make([]WebhookBinding, 0, len(models))
	for _, model := range models {
		binding, err := bindingFromModel(model)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
	}
	return bindings, nil
}

// bindingFromModel materialises one stored row.
//
// Every reader goes through here so they cannot disagree about what a row
// means. The parameters in particular: a listing that dropped them would
// silently disable every trigger that auto-registers itself on activation,
// WAHA included, because the lifecycle decides from the listed routes.
func bindingFromModel(model webhookBindingModel) (WebhookBinding, error) {
	parameters := map[string]any{}
	if len(model.Parameters) > 0 {
		if err := json.Unmarshal(model.Parameters, &parameters); err != nil {
			return WebhookBinding{}, fmt.Errorf("decode webhook parameters: %w", err)
		}
	}
	// A row whose route is empty was activated before routes were minted; its
	// path is its route, so those URLs keep working without reactivation.
	route := model.Route
	if route == "" {
		route = model.Path
	}
	return WebhookBinding{
		TenantID: model.TenantID, WorkflowID: model.WorkflowID, WorkflowVersionID: model.WorkflowVersionID,
		NodeID: model.NodeID, NodeType: model.NodeType, Method: model.Method,
		Route: route, Path: model.Path, Parameters: parameters,
	}, nil
}

// ErrWebhookPathClaimed reports that an activation was refused because an
// endpoint it needs is already answering.
//
// It is a sentinel so the HTTP boundary can tell "the graph is wrong" from
// "the endpoint is taken" and answer 409 with a message instead of a generic
// 500. The caller's document is valid; somebody else simply holds the route.
var ErrWebhookPathClaimed = errors.New("webhook endpoint already claimed")

// WebhookConflictError names the endpoint that could not be claimed and the
// workflow already answering on it.
//
// It wraps ErrWebhookPathClaimed, so errors.Is finds the sentinel while
// errors.As recovers the identifiers a message and a link need.
type WebhookConflictError struct {
	// Path is the label the refused document gave the endpoint.
	Path string
	// Method and Route are the binding that was refused. The route is the
	// identity that actually collided; the path is what the author called it.
	Method string
	Route  string
	// WorkflowID is the workflow already bound to that method and route. It is
	// empty when the row disappeared before it could be named, which is honest
	// — inventing a workflow would be worse than naming none.
	WorkflowID string
}

func (e *WebhookConflictError) Error() string {
	if e.WorkflowID == "" {
		return fmt.Sprintf("webhook %s %q is already claimed by another workflow", e.Method, e.Path)
	}
	return fmt.Sprintf("webhook %s %q is already claimed by workflow %s", e.Method, e.Path, e.WorkflowID)
}

// Unwrap is what makes errors.Is(err, ErrWebhookPathClaimed) work.
func (e *WebhookConflictError) Unwrap() error { return ErrWebhookPathClaimed }

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
			TenantID: tenantID, WorkflowID: workflowID, WorkflowVersionID: versionID,
			NodeID: trigger.NodeID, NodeType: trigger.NodeType,
			Method: trigger.Method, Route: route, Path: trigger.Path, Parameters: parameters, CreatedAt: now,
		}
		if err := insertBinding(tx, &binding); err != nil {
			// The path is not unique, so the only index that can refuse this
			// row is the global one on the minted route: another workflow is
			// already answering on it. That is a genuine conflict — the
			// endpoint is taken — so it is reported as one, naming the
			// workflow that holds it.
			if isRouteCollision(err) {
				return &WebhookConflictError{
					Path: trigger.Path, Route: route, Method: trigger.Method,
					WorkflowID: bindingOwner(tx, trigger.Method, route),
				}
			}
			return fmt.Errorf("record webhook binding for node %q: %w", trigger.NodeID, err)
		}
	}
	return nil
}

// insertBinding writes one binding behind a savepoint.
//
// The savepoint is what makes the failure above reportable. PostgreSQL aborts
// the enclosing transaction after a failed statement, so a plain insert would
// leave the lookup that names the conflicting workflow unable to run and the
// caller with a conflict it could not describe; rolling back to the savepoint
// returns the transaction to a usable state. Nothing is salvaged by it — the
// caller rolls the whole activation back either way — only reported.
func insertBinding(tx *gorm.DB, binding *webhookBindingModel) error {
	return tx.Transaction(func(inner *gorm.DB) error {
		return inner.Create(binding).Error
	})
}

// bindingOwner names the workflow already bound to one method and route.
//
// It is empty when the row is gone, which is honest: the conflict is real
// whatever named it, and inventing a workflow would be worse than naming none.
func bindingOwner(tx *gorm.DB, method, route string) string {
	var owner webhookBindingModel
	if err := tx.Where("method = ? AND route = ?", method, route).First(&owner).Error; err != nil {
		return ""
	}
	return owner.WorkflowID
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

// isRouteCollision reports whether a refused binding insert was refused by the
// unique index on the minted route.
//
// Both drivers translate a unique violation into gorm.ErrDuplicatedKey, so the
// test is on that sentinel rather than on the message text, which differs by
// dialect and by whether a table prefix rewrote the table name. The route
// index is the only unique index webhook_bindings carries — the path label is
// deliberately not unique — so a duplicate key from this insert is the route
// index and nothing else. A failure inside mintWebhookRoute is not passed
// through here: a route that cannot be minted is a fault, not a conflict.
func isRouteCollision(err error) bool {
	return errors.Is(err, gorm.ErrDuplicatedKey)
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
		binding, err := bindingFromModel(model)
		if err != nil {
			return nil, err
		}
		bindings = append(bindings, binding)
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

// WebhookRouteReader lists a workflow's public routes. It is the slice of the
// store a lifecycle hook needs, so the coordinator does not take the whole
// workflow repository to read three fields.
type WebhookRouteReader interface {
	WebhookRoutes(ctx context.Context, tenant TenantScope, workflowID string) ([]WebhookBinding, error)
}

// WebhookRouteMinter is the optional half of the workflow repository that can
// mint public routes ahead of activation. It is separate from
// WorkflowRepository so a store assembled without a webhook extractor is still
// a complete workflow repository.
type WebhookRouteMinter interface {
	EnsureWebhookRoutes(ctx context.Context, tenant TenantScope, workflowID string, document workflow.Document) ([]WebhookBinding, error)
}
