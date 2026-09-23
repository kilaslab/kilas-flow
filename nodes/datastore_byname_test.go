package nodes_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// sharedNameStore holds two tables whose names differ only in the case of a
// letter SQLite's lower() does not fold, so the unique index lets both exist
// and only the resolver stands between a By-Name locator and the wrong table.
// The first holds a row, the second holds none: the shape of the audit's
// finding, where a run that asked for the empty table read the full one.
func sharedNameStore() *stubDatastoreStore {
	store := newStubDatastoreStore()
	store.addTable("tenant-a", "ds_upper", "Ärger", datastoreToolColumnsFor("title"),
		[]datastore.Row{{"id": int64(1), "title": "not the table that was asked for"}})
	store.addTable("tenant-a", "ds_lower", "ärger", datastoreToolColumnsFor("title"), nil)
	return store
}

// wantAmbiguousName fails unless err is the ambiguity refusal and names both
// tables, so the author can choose one by id.
func wantAmbiguousName(t *testing.T, what string, err error) {
	t.Helper()
	if !errors.Is(err, datastore.ErrAmbiguousName) {
		t.Fatalf("%s = %v, want ErrAmbiguousName", what, err)
	}
	for _, id := range []string{"ds_upper", "ds_lower"} {
		if !strings.Contains(err.Error(), id) {
			t.Errorf("%s = %q, want it to list %s", what, err, id)
		}
	}
}

// A By-Name locator used to act on the first table whose name matched, so a
// Get of the empty "leads" returned the rows of "Leads", and an Update, Delete
// or Clear would have written to it (BUG-e7dwpk). A name two tables share is
// refused, and no table is read.
func TestDatastoreByNameRefusesANameTwoTablesShare(t *testing.T) {
	t.Parallel()
	store := sharedNameStore()
	executor := nodes.NewDatastoreExecutor(store)
	output, err := executor.Execute(context.Background(), datastoreNode(t, map[string]any{
		"resource": "row", "operation": nodes.DatastoreOperationGet,
		"dataTableId": datastoreLocator("name", "ärger"),
	}, nil), workflow.NodeInput{}, datastoreRequest())
	if err == nil {
		t.Fatalf("Execute() = %+v, want the shared name refused", output)
	}
	wantAmbiguousName(t, "Get by a shared name", err)
	if calls := store.listCalls(); len(calls) != 0 {
		t.Errorf("the refused run still read %s", calls[0].id)
	}
}

// The agent tool binds its table through the same resolver, so a tool bound by
// a shared name is refused when it is built rather than handed to the agent
// pointing at whichever table the list returned first.
func TestDatastoreToolByNameRefusesANameTwoTablesShare(t *testing.T) {
	t.Parallel()
	executor := nodes.NewDatastoreToolExecutor(sharedNameStore())
	_, err := executor.Execute(context.Background(), datastoreToolIR(t, "Lookup", map[string]any{
		"toolDescription": "Look up a row.",
		"dataTableId":     datastoreLocator("name", "ÄRGER"),
	}), workflow.NodeInput{}, engine.Request{
		Execution: engine.ExecutionContext{ID: "exec-1", TenantID: "tenant-a", WorkflowID: "wf_tool"},
	})
	wantAmbiguousName(t, "a tool bound by a shared name", err)
}
