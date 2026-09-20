package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// diagnosticsReport is what the n8n import route stores with the revision it
// created: the adapter's own report, envelope included. The repository treats
// it as opaque JSON, so this test asserts it comes back byte-for-byte rather
// than through a shape the storage layer would have to know.
const diagnosticsReport = `{"source":"n8n","importedAt":"2026-09-20T09:00:00Z","issues":[` +
	`{"severity":"blocking","nodeName":"Slack","nodeId":"n1","type":"n8n-nodes-base.slack","typeVersion":2.1,"reason":"no KilasFlow node answers for this type"},` +
	`{"severity":"lossy","nodeName":"HTTP Request","nodeId":"n2","field":"retryOnFail","reason":"retry settings carried as node settings"},` +
	`{"severity":"dropped","field":"pinData","reason":"pinned data is not carried"}]}`

func newDiagnosticsFixture(t *testing.T) (*repository.GORMWorkflowStore, repository.TenantScope) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return repository.NewWorkflowStore(db.DB), repository.TenantScope{ID: "tenant-a"}
}

func diagnosticsDocument(id, name string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            id,
		Name:          name,
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
	}
}

// The report is the only record of what an import could not carry, and the
// dialog that used to show it is gone by the time anybody reads the workflow.
// If it does not come back out of storage nothing else in the chain matters, so
// this is the round trip the whole ticket rests on.
func TestAnImportedDraftStoresItsReportWithTheRevisionAndReadsItBack(t *testing.T) {
	store, tenant := newDiagnosticsFixture(t)
	ctx := context.Background()

	stored, err := store.SaveDraftWithDiagnostics(ctx, tenant,
		diagnosticsDocument("wf_imported", "Imported 1954"), json.RawMessage(diagnosticsReport))
	if err != nil {
		t.Fatalf("SaveDraftWithDiagnostics() error = %v", err)
	}

	read, err := store.WorkflowDiagnostics(ctx, tenant, stored.ID, "")
	if err != nil {
		t.Fatalf("WorkflowDiagnostics() error = %v", err)
	}
	if read.VersionID != stored.LatestVersion.ID {
		t.Errorf("diagnostics version = %q, want the saved revision %q", read.VersionID, stored.LatestVersion.ID)
	}
	if read.Revision != stored.LatestVersion.Revision {
		t.Errorf("diagnostics revision = %d, want %d", read.Revision, stored.LatestVersion.Revision)
	}

	var got, want any
	if err := json.Unmarshal(read.Report, &got); err != nil {
		t.Fatalf("stored report is not JSON: %v (report = %s)", err, read.Report)
	}
	if err := json.Unmarshal([]byte(diagnosticsReport), &want); err != nil {
		t.Fatalf("fixture report is not JSON: %v", err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("stored report = %s, want %s", read.Report, diagnosticsReport)
	}
}

// A report describes one translation of one file. Saving the draft again is a
// new revision of a document a person has since edited, so the report must be
// readable where it belongs (its revision) and absent where it does not: a
// stale report presented as the current revision's diagnostics would name nodes
// that may no longer exist.
func TestAnImportReportStaysOnItsOwnRevisionAfterLaterSaves(t *testing.T) {
	store, tenant := newDiagnosticsFixture(t)
	ctx := context.Background()

	imported, err := store.SaveDraftWithDiagnostics(ctx, tenant,
		diagnosticsDocument("wf_edited", "Imported then edited"), json.RawMessage(diagnosticsReport))
	if err != nil {
		t.Fatalf("SaveDraftWithDiagnostics() error = %v", err)
	}
	importedVersionID := imported.LatestVersion.ID

	document := diagnosticsDocument("wf_edited", "Imported then edited")
	document.Nodes = []workflow.Node{{ID: "n1", Name: "Manual", Type: "kilasflow.manual"}}
	edited, err := store.SaveDraft(ctx, tenant, document)
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if edited.LatestVersion.ID == importedVersionID {
		t.Fatal("the ordinary save reused the imported revision")
	}

	latest, err := store.WorkflowDiagnostics(ctx, tenant, edited.ID, "")
	if err != nil {
		t.Fatalf("WorkflowDiagnostics(latest) error = %v", err)
	}
	if latest.VersionID != edited.LatestVersion.ID {
		t.Errorf("latest diagnostics version = %q, want %q", latest.VersionID, edited.LatestVersion.ID)
	}
	if len(latest.Report) != 0 {
		t.Errorf("latest revision carries diagnostics %s, want none: it was not imported", latest.Report)
	}

	original, err := store.WorkflowDiagnostics(ctx, tenant, edited.ID, importedVersionID)
	if err != nil {
		t.Fatalf("WorkflowDiagnostics(imported revision) error = %v", err)
	}
	if string(original.Report) != diagnosticsReport {
		t.Errorf("imported revision diagnostics = %s, want %s", original.Report, diagnosticsReport)
	}
}

// A report is tenant data like the document it describes. A version id from
// another tenant must resolve to nothing rather than to somebody else's nodes.
func TestImportDiagnosticsAreScopedToTheTenantThatImportedThem(t *testing.T) {
	store, tenant := newDiagnosticsFixture(t)
	ctx := context.Background()

	stored, err := store.SaveDraftWithDiagnostics(ctx, tenant,
		diagnosticsDocument("wf_scoped", "Tenant A import"), json.RawMessage(diagnosticsReport))
	if err != nil {
		t.Fatalf("SaveDraftWithDiagnostics() error = %v", err)
	}

	other := repository.TenantScope{ID: "tenant-b"}
	if _, err := store.WorkflowDiagnostics(ctx, other, stored.ID, stored.LatestVersion.ID); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("WorkflowDiagnostics(other tenant) error = %v, want ErrNotFound", err)
	}
	if _, err := store.WorkflowDiagnostics(ctx, other, stored.ID, ""); !errors.Is(err, repository.ErrNotFound) {
		t.Errorf("WorkflowDiagnostics(other tenant, latest) error = %v, want ErrNotFound", err)
	}
}

// The method exists to keep a report. Accepting an empty one would answer 201
// for an import whose diagnostics nobody can read, which is the bug in a
// different place.
func TestSavingWithDiagnosticsRefusesAnEmptyReport(t *testing.T) {
	store, tenant := newDiagnosticsFixture(t)

	if _, err := store.SaveDraftWithDiagnostics(context.Background(), tenant,
		diagnosticsDocument("wf_empty", "No report"), nil); err == nil {
		t.Fatal("SaveDraftWithDiagnostics() with no report succeeded, want a refusal")
	}
}

// A restore appends an old document to a new revision. Copying the old
// revision's report onto it would say this document's nodes were translated by
// an importer, when in fact they were restored by hand.
func TestRestoringAnImportedRevisionDoesNotCarryItsReportForward(t *testing.T) {
	store, tenant := newDiagnosticsFixture(t)
	ctx := context.Background()

	imported, err := store.SaveDraftWithDiagnostics(ctx, tenant,
		diagnosticsDocument("wf_restored", "Imported"), json.RawMessage(diagnosticsReport))
	if err != nil {
		t.Fatalf("SaveDraftWithDiagnostics() error = %v", err)
	}

	restored, err := store.RestoreVersion(ctx, tenant, imported.ID, imported.LatestVersion.ID, "test restore")
	if err != nil {
		t.Fatalf("RestoreVersion() error = %v", err)
	}
	if restored.LatestVersion.ID == imported.LatestVersion.ID {
		t.Fatal("restore did not append a revision")
	}

	read, err := store.WorkflowDiagnostics(ctx, tenant, restored.ID, restored.LatestVersion.ID)
	if err != nil {
		t.Fatalf("WorkflowDiagnostics(restored revision) error = %v", err)
	}
	if len(read.Report) != 0 {
		t.Errorf("restored revision diagnostics = %s, want none", read.Report)
	}
}
