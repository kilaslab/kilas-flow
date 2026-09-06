package nodes_test

import (
	"context"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/datastore"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

func datastoreEngine(t *testing.T) *datastore.Engine {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "node-datastore.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	store, err := datastore.NewEngine(db, "")
	if err != nil {
		t.Fatalf("datastore.NewEngine() error = %v", err)
	}
	return store
}

func datastoreTable(t *testing.T, store *datastore.Engine, name string) string {
	t.Helper()
	definition, err := store.Create(context.Background(), "tenant-a", name,
		[]datastore.ColumnInput{{Name: "title", Type: "string"}, {Name: "score", Type: "number"}})
	if err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	return definition.ID
}

func datastoreNode(t *testing.T, parameters map[string]any, settings map[string]any) workflow.IRNode {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodes.DatastoreNodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodes.DatastoreNodeType)
	}
	return workflow.IRNode{
		ID: "ds-1", Name: "Data table", Type: nodes.DatastoreNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Settings: settings, Definition: definition,
	}
}

func datastoreRequest() engine.Request {
	return engine.Request{Execution: engine.ExecutionContext{TenantID: "tenant-a"}}
}

func datastoreLocator(mode, value string) map[string]any {
	return property.WriteLocator(property.Locator{Mode: mode, Value: value})
}

func datastoreConditions(rows ...map[string]any) map[string]any {
	list := make([]any, 0, len(rows))
	for _, row := range rows {
		list = append(list, row)
	}
	return map[string]any{"conditions": list}
}

func datastoreRowParams(id string, operation string, extra map[string]any) map[string]any {
	parameters := map[string]any{
		"resource": "row", "operation": operation, "dataTableId": datastoreLocator("id", id),
	}
	for key, value := range extra {
		parameters[key] = value
	}
	return parameters
}

func datastoreManualColumns(values map[string]any) map[string]any {
	return map[string]any{"mappingMode": property.MappingManual, "value": values}
}

func runDatastore(t *testing.T, store *datastore.Engine, ir workflow.IRNode, input workflow.NodeInput) workflow.NodeOutput {
	t.Helper()
	executor := nodes.NewDatastoreExecutor(store)
	output, err := executor.Execute(context.Background(), ir, input, datastoreRequest())
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func TestDatastoreNodeResolvesWithItsExecutorBound(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Resolve(nodes.DatastoreNodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodes.DatastoreNodeType)
	}
	if definition.Version.Compare(workflow.V(1)) != 0 {
		t.Fatalf("version = %v, want 1", definition.Version)
	}
	if definition.ExecutorID != nodes.DatastoreExecutorID {
		t.Fatalf("executor = %q, want %q", definition.ExecutorID, nodes.DatastoreExecutorID)
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, nil, nil, nil,
		nodes.WithDatastoreEngine(datastoreEngine(t))); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	if _, found := executors.Lookup(nodes.DatastoreExecutorID); !found {
		t.Fatalf("executor %q is not bound", nodes.DatastoreExecutorID)
	}
}

func TestDatastoreRowInsertGetUpdateUpsertDelete(t *testing.T) {
	t.Parallel()
	store := datastoreEngine(t)
	id := datastoreTable(t, store, "Metrics")

	inserted := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationInsert, map[string]any{
		"columns": datastoreManualColumns(map[string]any{"title": "hello", "score": 3.0}),
	}), nil), workflow.NodeInput{})
	if len(inserted[0]) != 1 || inserted[0][0].JSON["title"] != "hello" {
		t.Fatalf("insert output = %+v, want the hello row", inserted)
	}

	got := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationGet, map[string]any{
		"match": "any",
		"filters": datastoreConditions(map[string]any{
			"keyName": "title", "condition": "eq", "keyValue": "hello",
		}),
		"returnAll": true,
	}), nil), workflow.NodeInput{})
	if len(got[0]) != 1 || got[0][0].JSON["score"] != 3.0 {
		t.Fatalf("get output = %+v, want the one hello row", got)
	}

	updated := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationUpdate, map[string]any{
		"match": "all",
		"filters": datastoreConditions(map[string]any{
			"keyName": "title", "condition": "eq", "keyValue": "hello",
		}),
		"columns": datastoreManualColumns(map[string]any{"score": 9.0}),
	}), nil), workflow.NodeInput{})
	if len(updated[0]) != 1 || updated[0][0].JSON["score"] != 9.0 {
		t.Fatalf("update output = %+v, want score 9", updated)
	}

	upserted := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationUpsert, map[string]any{
		"match": "any",
		"filters": datastoreConditions(map[string]any{
			"keyName": "title", "condition": "eq", "keyValue": "fresh",
		}),
		"columns": datastoreManualColumns(map[string]any{"title": "fresh", "score": 1.0}),
	}), nil), workflow.NodeInput{})
	if len(upserted[0]) != 1 || upserted[0][0].JSON["title"] != "fresh" {
		t.Fatalf("upsert output = %+v, want the inserted fresh row", upserted)
	}

	deleted := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationDelete, map[string]any{
		"match": "any",
		"filters": datastoreConditions(map[string]any{
			"keyName": "title", "condition": "eq", "keyValue": "fresh",
		}),
	}), nil), workflow.NodeInput{})
	if len(deleted[0]) != 1 {
		t.Fatalf("delete output = %+v, want the removed row", deleted)
	}
	remaining, err := store.List(context.Background(), "tenant-a", id, datastore.RowQuery{ReturnAll: true})
	if err != nil {
		t.Fatalf("List() error = %v", err)
	}
	if len(remaining.Rows) != 1 || remaining.Rows[0]["title"] != "hello" {
		t.Fatalf("remaining rows = %+v, want only hello", remaining.Rows)
	}
}

func TestDatastoreRowBranchesRouteToTheirPort(t *testing.T) {
	t.Parallel()
	store := datastoreEngine(t)
	id := datastoreTable(t, store, "Branches")
	if _, err := store.Insert(context.Background(), "tenant-a", id, map[string]any{"title": "present"}); err != nil {
		t.Fatalf("Insert() error = %v", err)
	}
	branch := func(operation, title string) workflow.NodeOutput {
		return runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, operation, map[string]any{
			"match": "any",
			"filters": datastoreConditions(map[string]any{
				"keyName": "title", "condition": "eq", "keyValue": title,
			}),
		}), nil), workflow.NodeInput{})
	}
	exists := branch(nodes.DatastoreOperationIfExists, "present")
	if len(exists) != 2 || len(exists[0]) != 1 || len(exists[1]) != 0 {
		t.Fatalf("if-exists on a present row = %v items per port, want 1 and 0", portSizes(exists))
	}
	missing := branch(nodes.DatastoreOperationIfExists, "absent")
	if len(missing) != 2 || len(missing[0]) != 0 || len(missing[1]) != 1 {
		t.Fatalf("if-exists on an absent row = %v items per port, want 0 and 1", portSizes(missing))
	}
	notExists := branch(nodes.DatastoreOperationIfNotExists, "absent")
	if len(notExists) != 2 || len(notExists[0]) != 0 || len(notExists[1]) != 1 {
		t.Fatalf("if-not-exists on an absent row = %v items per port, want 0 and 1", portSizes(notExists))
	}
}

func portSizes(output workflow.NodeOutput) []int {
	sizes := make([]int, 0, len(output))
	for _, port := range output {
		sizes = append(sizes, len(port))
	}
	return sizes
}

func TestDatastoreTableCreateListRenameClearDelete(t *testing.T) {
	t.Parallel()
	store := datastoreEngine(t)
	executor := nodes.NewDatastoreExecutor(store)
	run := func(parameters map[string]any) workflow.NodeOutput {
		t.Helper()
		output, err := executor.Execute(context.Background(),
			datastoreNode(t, parameters, nil), workflow.NodeInput{}, datastoreRequest())
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		return output
	}
	table := func(operation string, extra map[string]any) map[string]any {
		parameters := map[string]any{"resource": "table", "operation": operation}
		for key, value := range extra {
			parameters[key] = value
		}
		return parameters
	}

	created := run(table(nodes.DatastoreOperationCreateTable, map[string]any{"name": "Lifecycle"}))
	id, _ := created[0][0].JSON["id"].(string)
	if id == "" {
		t.Fatalf("create output = %+v, want an id", created)
	}
	listed := run(table(nodes.DatastoreOperationListTables, nil))
	if len(listed[0]) != 1 || listed[0][0].JSON["name"] != "Lifecycle" {
		t.Fatalf("list output = %+v, want the Lifecycle table", listed)
	}
	renamed := run(table(nodes.DatastoreOperationRenameTable, map[string]any{
		"dataTableId": datastoreLocator("id", id), "name": "Renamed",
	}))
	if renamed[0][0].JSON["name"] != "Renamed" {
		t.Fatalf("rename output = %+v, want Renamed", renamed)
	}
	// A table create is name-only, so the column arrives through its own
	// call before the By-Name insert below proves the name locator.
	if err := store.AddColumn(context.Background(), "tenant-a", id, datastore.ColumnInput{Name: "title", Type: "string"}); err != nil {
		t.Fatalf("AddColumn() error = %v", err)
	}
	byName := runDatastore(t, store, datastoreNode(t, datastoreRowParams("", nodes.DatastoreOperationInsert, map[string]any{
		"dataTableId": datastoreLocator("name", "Renamed"),
		"columns":     datastoreManualColumns(map[string]any{"title": "v"}),
	}), nil), workflow.NodeInput{})
	if len(byName[0]) != 1 || byName[0][0].JSON["title"] != "v" {
		t.Fatalf("by-name insert = %+v, want the v row", byName)
	}
	cleared := run(table(nodes.DatastoreOperationClearTable, map[string]any{
		"dataTableId": datastoreLocator("id", id),
	}))
	if cleared[0][0].JSON["deleted"] != 1.0 {
		t.Fatalf("clear output = %+v, want deleted 1", cleared)
	}
	dropped := run(table(nodes.DatastoreOperationDeleteTable, map[string]any{
		"dataTableId": datastoreLocator("id", id),
	}))
	if dropped[0][0].JSON["id"] != id {
		t.Fatalf("delete output = %+v, want the id", dropped)
	}
	if _, err := store.GetDatastore(context.Background(), "tenant-a", id); err == nil {
		t.Fatal("GetDatastore after drop = nil, want unknown")
	}
}

func TestDatastoreAutoMappingTakesTheItemsColumns(t *testing.T) {
	t.Parallel()
	store := datastoreEngine(t)
	id := datastoreTable(t, store, "Auto")
	output := runDatastore(t, store, datastoreNode(t, datastoreRowParams(id, nodes.DatastoreOperationInsert, map[string]any{
		"columns": map[string]any{"mappingMode": property.MappingAuto},
	}), nil), workflow.NodeInput{"main": []workflow.Item{
		{JSON: map[string]any{"title": "auto", "score": 2.0, "unmapped": "dropped"}},
	}})
	if len(output[0]) != 1 {
		t.Fatalf("output = %+v, want one row", output)
	}
	if output[0][0].JSON["title"] != "auto" {
		t.Fatalf("row = %+v, want the item's title", output[0][0].JSON)
	}
	if _, present := output[0][0].JSON["unmapped"]; present {
		t.Fatalf("row = %+v, want the unmapped field dropped", output[0][0].JSON)
	}
}

func TestDatastoreColumnExpressionsAreRefusedAtSaveAndAtRun(t *testing.T) {
	t.Parallel()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	marker := map[string]any{"mode": "expression", "value": "{{ $json.column }}"}
	document := func(parameters map[string]any) workflow.Document {
		return workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_ds", Name: "Datastore",
			Nodes: []workflow.Node{
				{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)},
				{ID: "ds", Name: "Data table", Type: nodes.DatastoreNodeType, TypeVersion: workflow.V(1),
					Parameters: parameters},
			},
			Connections: []workflow.Connection{{
				ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "manual", Port: "main"},
				Target: workflow.Endpoint{NodeID: "ds", Port: "main"},
			}},
			Settings: map[string]any{},
		}
	}
	parameters := map[string]any{
		"resource": "row", "operation": nodes.DatastoreOperationGet, "match": "any",
		"dataTableId": datastoreLocator("id", "datastore_x"),
		"filters":     datastoreConditions(map[string]any{"keyName": marker, "condition": "eq", "keyValue": "hello"}),
	}
	_, err := workflow.Compile(document(parameters), registry)
	if err == nil || !strings.Contains(err.Error(), "filters.conditions[0].keyName") {
		t.Fatalf("Compile() = %v, want a refusal naming filters.conditions[0].keyName", err)
	}

	// The run-time backstop, against the unresolved parameters: a body that
	// supplies the column name resolves after compilation, so only the
	// pre-resolve check can see it.
	store := datastoreEngine(t)
	id := datastoreTable(t, store, "Guarded")
	executor := nodes.NewDatastoreExecutor(store)
	ir := datastoreNode(t, parameters, nil)
	ir.Parameters["dataTableId"] = datastoreLocator("id", id)
	_, err = executor.Execute(context.Background(), ir, workflow.NodeInput{"main": []workflow.Item{
		{JSON: map[string]any{"column": "title"}},
	}}, datastoreRequest())
	if err == nil || !strings.Contains(err.Error(), "filters.conditions[0].keyName") {
		t.Fatalf("Execute() = %v, want a refusal naming filters.conditions[0].keyName", err)
	}
	remaining, listErr := store.List(context.Background(), "tenant-a", id, datastore.RowQuery{ReturnAll: true})
	if listErr != nil {
		t.Fatalf("List() error = %v", listErr)
	}
	if len(remaining.Rows) != 0 {
		t.Fatalf("rows = %+v, want the refused run to commit nothing", remaining.Rows)
	}
}

func TestDatastoreNodeHasNoSQLNodeDependency(t *testing.T) {
	t.Parallel()

	// The implementation, not this test: the test itself wires the
	// executor registry, which legitimately names every package.
	matched, err := filepath.Glob("datastore.go")
	if err != nil {
		t.Fatalf("Glob() error = %v", err)
	}
	if len(matched) == 0 {
		t.Fatal("no datastore node implementation found beside this test")
	}
	for _, path := range matched {
		contents, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("ReadFile(%s) error = %v", path, err)
		}
		if strings.Contains(string(contents), "internal/sqlnode") {
			t.Fatalf("%s imports internal/sqlnode: the node reaches storage through the row store, never through user-database SQL", path)
		}
	}
}

func TestDatastoreFailuresNameTheirItemAndContinueOnFailEmitsTheRest(t *testing.T) {
	t.Parallel()
	store := datastoreEngine(t)
	id := datastoreTable(t, store, "Partial")
	// The seventh item carries a score the number column cannot take; the
	// mapping resolves per item, so only that item's write is poisoned.
	items := make([]workflow.Item, 0, 10)
	for index := range 10 {
		score := any(float64(index))
		if index == 6 {
			score = "not-a-number"
		}
		items = append(items, workflow.Item{JSON: map[string]any{"title": "row", "score": score}})
	}
	parameters := datastoreRowParams(id, nodes.DatastoreOperationInsert, map[string]any{
		"columns": map[string]any{
			"mappingMode": property.MappingAuto,
		},
	})
	executor := nodes.NewDatastoreExecutor(store)

	_, err := executor.Execute(context.Background(), datastoreNode(t, parameters, nil),
		workflow.NodeInput{"main": items}, datastoreRequest())
	if err == nil || !strings.Contains(err.Error(), "item 7") {
		t.Fatalf("Execute() = %v, want a failure naming item 7", err)
	}

	output, err := executor.Execute(context.Background(),
		datastoreNode(t, parameters, map[string]any{"continueOnFail": true}),
		workflow.NodeInput{"main": items}, datastoreRequest())
	if err != nil {
		t.Fatalf("Execute() with continueOnFail error = %v", err)
	}
	if len(output[0]) != 10 {
		t.Fatalf("output holds %d items, want 10 — nine rows plus the failure in its place", len(output[0]))
	}
	descriptor, isError := output[0][6].JSON[engine.ErrorItemKey].(map[string]any)
	if !isError {
		t.Fatalf("item 7 = %#v, want the failure in the failing item's place", output[0][6].JSON)
	}
	if descriptor["item"] != 7.0 {
		t.Fatalf("error descriptor = %+v, want it to name item 7", descriptor)
	}
	if output[0][6].Paired == nil || output[0][6].Paired.ItemIndex != 6 {
		t.Fatalf("item 7 lineage = %+v, want it paired to input index 6", output[0][6].Paired)
	}
	for index, item := range output[0] {
		if index == 6 {
			continue
		}
		if _, failed := item.JSON[engine.ErrorItemKey]; failed {
			t.Fatalf("item %d = %#v, want the row that did insert", index+1, item.JSON)
		}
	}
}
