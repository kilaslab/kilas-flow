package repository_test

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A workflow's static data is its tenant's alone, is replaced whole by a
// save, and goes when the workflow does.
func TestStaticDataIsTenantScopedAndGoesWithItsWorkflow(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-static-a"}
		other := repository.TenantScope{ID: "drv-static-b"}
		workflows := repository.NewWorkflowStore(db.DB)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion, Name: "Keeps state",
			Nodes: []workflow.Node{}, Connections: []workflow.Connection{}, Settings: map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		store := repository.NewStaticDataStore(db.DB)
		if data, err := store.LoadStaticData(ctx, tenant, saved.ID); err != nil || data != nil {
			t.Fatalf("LoadStaticData() before any save = %s, %v; want nothing", data, err)
		}
		for _, document := range []string{`{"global":{"n":1}}`, `{"global":{"n":2}}`} {
			if err := store.SaveStaticData(ctx, tenant, saved.ID, json.RawMessage(document)); err != nil {
				t.Fatalf("SaveStaticData() error = %v", err)
			}
		}
		if data, err := store.LoadStaticData(ctx, tenant, saved.ID); err != nil || string(data) != `{"global":{"n":2}}` {
			t.Fatalf("LoadStaticData() = %s, %v; want the last save", data, err)
		}
		if data, err := store.LoadStaticData(ctx, other, saved.ID); err != nil || data != nil {
			t.Fatalf("another tenant read %s, %v", data, err)
		}
		if err := workflows.Delete(ctx, tenant, saved.ID); err != nil {
			t.Fatalf("Delete() error = %v", err)
		}
		if data, err := store.LoadStaticData(ctx, tenant, saved.ID); err != nil || data != nil {
			t.Fatalf("LoadStaticData() after the workflow was deleted = %s, %v; want nothing", data, err)
		}
	})
}
