package loadoptions_test

import (
	"context"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

func TestDatastoreListLoaderIsTenantScopedAndHiddenFromEmbed(t *testing.T) {
	t.Parallel()

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	seen := ""
	if err := resolver.RegisterInternal(loadoptions.DatastoreListLoader, loadoptions.Datastores(
		func(_ context.Context, tenantID string) ([]loadoptions.DatastoreOption, error) {
			seen = tenantID
			return []loadoptions.DatastoreOption{{ID: "datastore_1", Name: "Metrics"}}, nil
		})); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}
	loader := property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.DatastoreListLoader}
	result, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1"}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if seen != "t1" {
		t.Fatalf("tenant = %q, want t1", seen)
	}
	if len(result.Options) != 1 || result.Options[0].Value != "datastore_1" || result.Options[0].Label != "Metrics" {
		t.Fatalf("options = %+v, want the Metrics table", result.Options)
	}
	embedded, err := resolver.Load(context.Background(), loader,
		loadoptions.Scope{TenantID: "t1", WorkflowID: "wf_1"}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(embedded.Options) != 0 || embedded.Reason == "" {
		t.Fatalf("embed load = %+v, want an empty list with a reason", embedded)
	}
}

// Two tables whose names differ only in case read as one label in the From-list
// picker, and choosing between them was a guess. Such a pair can still exist —
// SQLite's index folds ASCII only — so each label that shares its name is told
// apart by the tail of its id, the part a UUIDv7 does not share with a table
// made the same minute. A name nothing else holds keeps its plain label.
func TestDatastoreListLabelsTellTablesThatShareANameApart(t *testing.T) {
	t.Parallel()

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterInternal(loadoptions.DatastoreListLoader, loadoptions.Datastores(
		func(context.Context, string) ([]loadoptions.DatastoreOption, error) {
			return []loadoptions.DatastoreOption{
				{ID: "datastore_01a0cc80-b966-713c-a365-273d1111aaaa", Name: "Ärger"},
				{ID: "datastore_01a0cc80-b966-713c-a365-273d2222bbbb", Name: "ärger"},
				{ID: "datastore_01a0cc80-b966-713c-a365-273d3333cccc", Name: "Metrics"},
			}, nil
		})); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}
	loader := property.OptionsLoader{Source: property.LoaderInternal, Name: loadoptions.DatastoreListLoader}
	result, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1"}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	want := []string{"Ärger · 1111aaaa", "ärger · 2222bbbb", "Metrics"}
	if len(result.Options) != len(want) {
		t.Fatalf("options = %+v, want %d", result.Options, len(want))
	}
	for index, label := range want {
		if result.Options[index].Label != label {
			t.Errorf("option %d label = %q, want %q", index, result.Options[index].Label, label)
		}
	}
}

func TestDatastoreSchemaLoaderNeedsATable(t *testing.T) {
	t.Parallel()

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterSchema(loadoptions.DatastoreMappingColumnsLoader, loadoptions.DatastoreColumns(
		func(_ context.Context, tenantID, ref string) ([]loadoptions.DatastoreColumn, error) {
			if tenantID != "t1" || ref != "datastore_1" {
				t.Fatalf("describe(%q, %q), want (t1, datastore_1)", tenantID, ref)
			}
			return []loadoptions.DatastoreColumn{{Name: "title", Type: "string"}}, nil
		})); err != nil {
		t.Fatalf("RegisterSchema() error = %v", err)
	}
	loader := property.OptionsLoader{
		Source: property.LoaderInternal, Name: loadoptions.DatastoreMappingColumnsLoader,
	}
	schema, err := resolver.LoadSchema(context.Background(), loader,
		loadoptions.Scope{TenantID: "t1", Dependencies: map[string]string{"dataTableId": "datastore_1"}}, "", nil)
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	if len(schema.Fields) != 1 || schema.Fields[0].ID != "title" || schema.Fields[0].Type != "string" {
		t.Fatalf("schema = %+v, want the title column", schema)
	}
	if _, err := resolver.LoadSchema(context.Background(), loader,
		loadoptions.Scope{TenantID: "t1"}, "", nil); err == nil {
		t.Error("LoadSchema() without a table = nil, want a refusal")
	}
}
