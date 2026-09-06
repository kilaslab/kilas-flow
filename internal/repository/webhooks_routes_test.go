package repository

import (
	"context"
	"testing"

	"github.com/glebarez/sqlite"
	"gorm.io/gorm"
)

// WebhookRoutes must carry the trigger parameters Resolve decodes: the
// activation lifecycle reads listed routes to decide auto-registration, and
// a listing without parameters silently disables every auto-registering
// trigger (WAHA included).
func TestWebhookRoutesCarriesTriggerParameters(t *testing.T) {
	t.Parallel()

	db, err := gorm.Open(sqlite.Open(":memory:"), &gorm.Config{})
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	if err := db.AutoMigrate(&webhookBindingModel{}); err != nil {
		t.Fatalf("AutoMigrate() error = %v", err)
	}
	row := webhookBindingModel{
		TenantID: "tenant-1", WorkflowID: "wf-1", WorkflowVersionID: "v1",
		NodeID: "n1", NodeType: "pack.wahaTrigger", Method: "POST",
		Route: "abc123", Path: "waha",
		Parameters: []byte(`{"autoRegister":true,"events":["message"]}`),
	}
	if err := db.Create(&row).Error; err != nil {
		t.Fatalf("Create() error = %v", err)
	}

	store := &GORMWorkflowStore{db: db}
	bindings, err := store.WebhookRoutes(context.Background(), TenantScope{ID: "tenant-1"}, "wf-1")
	if err != nil {
		t.Fatalf("WebhookRoutes() error = %v", err)
	}
	if len(bindings) != 1 {
		t.Fatalf("bindings = %d, want 1", len(bindings))
	}
	if bindings[0].Parameters["autoRegister"] != true {
		t.Errorf("parameters = %#v, want autoRegister carried", bindings[0].Parameters)
	}
}
