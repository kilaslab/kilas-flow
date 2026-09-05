package repository

import (
	"context"
	"encoding/json"
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
	Method            string
	Path              string
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
	Resolve(ctx context.Context, method, path string) (WebhookBinding, error)
}

var _ WebhookRepository = (*GORMWorkflowStore)(nil)

// Resolve finds the active binding for one inbound request.
//
// It is deliberately not tenant-scoped: an inbound webhook has no session, and
// the path is the only thing identifying it. The binding it returns carries
// the tenant, which every downstream operation is then scoped by.
func (store *GORMWorkflowStore) Resolve(ctx context.Context, method, path string) (WebhookBinding, error) {
	var model webhookBindingModel
	if err := store.db.WithContext(ctx).
		Where("method = ? AND path = ?", method, path).
		First(&model).Error; err != nil {
		return WebhookBinding{}, mapNotFound(err, "webhook binding")
	}
	parameters := map[string]any{}
	if len(model.Parameters) > 0 {
		if err := json.Unmarshal(model.Parameters, &parameters); err != nil {
			return WebhookBinding{}, fmt.Errorf("decode webhook parameters: %w", err)
		}
	}
	return WebhookBinding{
		TenantID: model.TenantID, WorkflowID: model.WorkflowID, WorkflowVersionID: model.WorkflowVersionID,
		NodeID: model.NodeID, Method: model.Method, Path: model.Path, Parameters: parameters,
	}, nil
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
		binding := webhookBindingModel{
			TenantID: tenantID, WorkflowID: workflowID, WorkflowVersionID: versionID, NodeID: trigger.NodeID,
			Method: trigger.Method, Path: trigger.Path, Parameters: parameters, CreatedAt: now,
		}
		if err := tx.Create(&binding).Error; err != nil {
			// The unique index on (method, path) turns a collision into a clear
			// activation failure rather than an ambiguous route at request time.
			return fmt.Errorf("webhook path %q is already claimed by another active workflow", trigger.Path)
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
