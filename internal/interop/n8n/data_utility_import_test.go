package n8n_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// executeImported runs an imported node's executor on the given inputs, with
// the ports its definition declares, and returns each output's items.
func executeImported(t *testing.T, imported workflow.Node, executorID string, input workflow.NodeInput) workflow.NodeOutput {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(executorID)
	if !found {
		t.Fatalf("executor %q is not registered", executorID)
	}
	definition, found := registry(t).Lookup(imported.Type, imported.TypeVersion)
	if !found {
		t.Fatalf("%s is not registered", imported.Type)
	}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: imported.ID, Name: imported.Name, Type: imported.Type, TypeVersion: imported.TypeVersion,
		Parameters: imported.Parameters, Definition: definition,
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func TestTheDataUtilityTypesMatchTheNodePack(t *testing.T) {
	t.Parallel()

	if n8n.RenameKeysNodeType != nodes.RenameKeysNodeType || n8n.CompareDatasetsNodeType != nodes.CompareDatasetsNodeType {
		t.Fatalf("the adapter's mirrored types (%s, %s) differ from the node pack's (%s, %s)",
			n8n.RenameKeysNodeType, n8n.CompareDatasetsNodeType, nodes.RenameKeysNodeType, nodes.CompareDatasetsNodeType)
	}
}

// --- Rename Keys ---------------------------------------------------------------

func TestRenameKeysImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.renameKeys", 1,
		`{"keys":{"key":[{"currentKey":"first_name","newKey":"firstName"},{"currentKey":"address.zip","newKey":"zip"}]}}`))
	imported := nodeByName(result.Document, "Legacy")
	if imported.Type != n8n.RenameKeysNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.RenameKeysNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	// The imported parameters are what the executor reads: a rename that
	// imported but did not run would pass every check but this one.
	output := executeImported(t, imported, nodes.RenameKeysExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{
		"first_name": "Ada", "address": map[string]any{"zip": "N1"}, "kept": true,
	}}}})
	want := map[string]any{"firstName": "Ada", "address": map[string]any{}, "zip": "N1", "kept": true}
	if len(output) != 1 || len(output[0]) != 1 || !reflect.DeepEqual(output[0][0].JSON, want) {
		t.Fatalf("output = %#v, want %#v", output, want)
	}

	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	var returned n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Legacy" {
			returned = candidate
		}
	}
	if returned.Type != "n8n-nodes-base.renameKeys" || returned.TypeVersion != 1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.renameKeys v1", returned.Type, returned.TypeVersion)
	}
	wantKeys := map[string]any{"key": []any{
		map[string]any{"currentKey": "first_name", "newKey": "firstName"},
		map[string]any{"currentKey": "address.zip", "newKey": "zip"},
	}}
	if !reflect.DeepEqual(returned.Parameters["keys"], wantKeys) {
		t.Fatalf("exported keys = %#v, want %#v", returned.Parameters["keys"], wantKeys)
	}
}

func TestRenameKeysDropsTheEditorsEmptyRow(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.renameKeys", 1,
		`{"keys":{"key":[{"currentKey":"","newKey":""},{"currentKey":"a","newKey":"b"}]}}`))
	imported := nodeByName(result.Document, "Legacy")
	rows, _ := imported.Parameters["keys"].(map[string]any)["key"].([]any)
	if len(rows) != 1 {
		t.Fatalf("keys = %#v, want only the row that renames something", imported.Parameters["keys"])
	}
}

func TestRenameKeysNamesTheRegexReplacementItCannotCarry(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.renameKeys", 1,
		`{"keys":{"key":[{"currentKey":"a","newKey":"b"}]},"additionalOptions":{"regexReplacement":{"replacements":[{"searchRegex":"^x_","replaceRegex":""}]}}}`))
	imported := nodeByName(result.Document, "Legacy")
	if imported.Type != n8n.RenameKeysNodeType {
		t.Fatalf("imported as %q, want the exact renames kept on the native node", imported.Type)
	}
	blocking := blockingFor(result, "Legacy")
	if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, "regular expression") {
		t.Fatalf("blocking issues = %#v, want the regular-expression replacements named", blocking)
	}
}

// --- Compare Datasets ----------------------------------------------------------

// compareFixture wires two Set nodes into a Compare Datasets node and one No
// Operation node onto each of its four outputs, output N to target N.
func compareFixture(parameters string) string {
	return `{
	  "name": "Compare",
	  "nodes": [
	    {"id":"m","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"a","name":"Source A","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,-80],"parameters":{}},
	    {"id":"b","name":"Source B","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,80],"parameters":{}},
	    {"id":"c","name":"Compare","type":"n8n-nodes-base.compareDatasets","typeVersion":2.3,"position":[440,0],"parameters":` + parameters + `},
	    {"id":"o0","name":"Only A","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[660,-180],"parameters":{}},
	    {"id":"o1","name":"Same","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[660,-60],"parameters":{}},
	    {"id":"o2","name":"Different","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[660,60],"parameters":{}},
	    {"id":"o3","name":"Only B","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[660,180],"parameters":{}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Source A","type":"main","index":0},{"node":"Source B","type":"main","index":0}]]},
	    "Source A": {"main": [[{"node":"Compare","type":"main","index":0}]]},
	    "Source B": {"main": [[{"node":"Compare","type":"main","index":1}]]},
	    "Compare": {"main": [
	      [{"node":"Only A","type":"main","index":0}],
	      [{"node":"Same","type":"main","index":0}],
	      [{"node":"Different","type":"main","index":0}],
	      [{"node":"Only B","type":"main","index":0}]
	    ]}
	  }
	}`
}

const compareParameters = `{"mergeByFields":{"values":[{"field1":"id","field2":"userId"}]},"resolve":"includeBoth","options":{"skipFields":"updatedAt","disableDotNotation":false}}`

func TestCompareDatasetsWiresTwoInputsAndFourOutputsToTheMatchingPorts(t *testing.T) {
	t.Parallel()

	result := importFixture(t, compareFixture(compareParameters))
	imported := nodeByName(result.Document, "Compare")
	if imported.Type != n8n.CompareDatasetsNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.CompareDatasetsNodeType)
	}
	if blocking := blockingFor(result, "Compare"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	edges := edgesOf(result.Document)
	for _, want := range []string{
		"Source A.main->Compare.input1",
		"Source B.main->Compare.input2",
		"Compare.onlyInA->Only A.main",
		"Compare.same->Same.main",
		"Compare.different->Different.main",
		"Compare.onlyInB->Only B.main",
	} {
		if !strings.Contains(edges, want) {
			t.Errorf("edges = %s, want %q", edges, want)
		}
	}

	// A workflow that wires every port must compile as imported.
	document := result.Document
	document.ID = "wf_compare"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v", err)
	}
}

func TestCompareDatasetsImportsItsFieldsAndOptionsAndRuns(t *testing.T) {
	t.Parallel()

	result := importFixture(t, compareFixture(compareParameters))
	imported := nodeByName(result.Document, "Compare")

	wantFields := map[string]any{"values": []any{map[string]any{"field1": "id", "field2": "userId"}}}
	if !reflect.DeepEqual(imported.Parameters["mergeByFields"], wantFields) {
		t.Fatalf("mergeByFields = %#v, want %#v", imported.Parameters["mergeByFields"], wantFields)
	}
	if imported.Parameters["resolve"] != "includeBoth" {
		t.Fatalf("resolve = %#v, want includeBoth", imported.Parameters["resolve"])
	}
	// The field names differ per side, and the skipped field is left out.
	output := executeImported(t, imported, nodes.CompareDatasetsExecutorID, workflow.NodeInput{
		nodes.CompareInputA: {
			{JSON: map[string]any{"id": float64(1), "name": "Ada", "updatedAt": "mon"}},
			{JSON: map[string]any{"id": float64(2), "name": "Grace", "updatedAt": "mon"}},
			{JSON: map[string]any{"id": float64(3), "name": "Linus", "updatedAt": "mon"}},
		},
		nodes.CompareInputB: {
			{JSON: map[string]any{"userId": float64(1), "name": "Ada", "updatedAt": "tue"}},
			{JSON: map[string]any{"userId": float64(2), "name": "Gracie", "updatedAt": "tue"}},
			{JSON: map[string]any{"userId": float64(9), "name": "Dennis", "updatedAt": "tue"}},
		},
	})
	if len(output) != 4 {
		t.Fatalf("outputs = %d, want 4", len(output))
	}
	counts := []int{len(output[0]), len(output[1]), len(output[2]), len(output[3])}
	if !reflect.DeepEqual(counts, []int{1, 1, 1, 1}) {
		t.Fatalf("In A only / Same / Different / In B only = %v, want one each", counts)
	}
	if output[0][0].JSON["name"] != "Linus" || output[1][0].JSON["name"] != "Ada" || output[3][0].JSON["name"] != "Dennis" {
		t.Fatalf("outputs = %#v", output)
	}
	changes, _ := output[2][0].JSON["different"].(map[string]any)
	if _, skipped := changes["updatedAt"]; skipped || changes["name"] == nil {
		t.Fatalf("different = %#v, want the name difference and no updatedAt", changes)
	}
}

func TestCompareDatasetsExportsBackWithEveryWireOnItsSlot(t *testing.T) {
	t.Parallel()

	result := importFixture(t, compareFixture(compareParameters))
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}

	var returned n8n.Node
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Compare" {
			returned = candidate
		}
	}
	if returned.Type != "n8n-nodes-base.compareDatasets" {
		t.Fatalf("exported as %q, want n8n-nodes-base.compareDatasets", returned.Type)
	}
	wantFields := map[string]any{"values": []any{map[string]any{"field1": "id", "field2": "userId"}}}
	if !reflect.DeepEqual(returned.Parameters["mergeByFields"], wantFields) {
		t.Fatalf("exported mergeByFields = %#v, want %#v", returned.Parameters["mergeByFields"], wantFields)
	}
	if options, _ := returned.Parameters["options"].(map[string]any); options["skipFields"] != "updatedAt" {
		t.Fatalf("exported options = %#v, want skipFields carried", returned.Parameters["options"])
	}

	slots := exported.Document.Connections["Compare"]["main"]
	if len(slots) != 4 {
		t.Fatalf("exported output slots = %d, want 4", len(slots))
	}
	for index, want := range []string{"Only A", "Same", "Different", "Only B"} {
		if len(slots[index]) != 1 || slots[index][0].Node != want {
			t.Errorf("output slot %d = %#v, want a single wire to %q", index, slots[index], want)
		}
	}
	// Source B feeds the second input, not the first.
	if targets := exported.Document.Connections["Source B"]["main"]; len(targets) != 1 || targets[0][0].Index != 1 {
		t.Fatalf("Source B wires = %#v, want input index 1", targets)
	}
}

func TestCompareDatasetsRefusesTheOptionsItDoesNotImplement(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		parameters string
		wantNeedle string
	}{
		{
			name:       "a mix of versions",
			parameters: `{"mergeByFields":{"values":[{"field1":"id","field2":"id"}]},"resolve":"mix"}`,
			wantNeedle: "mix of versions",
		},
		{
			name:       "fuzzy comparison",
			parameters: `{"mergeByFields":{"values":[{"field1":"id","field2":"id"}]},"options":{"fuzzyCompare":true}}`,
			wantNeedle: "fuzzily",
		},
		{
			name:       "every match",
			parameters: `{"mergeByFields":{"values":[{"field1":"id","field2":"id"}]},"options":{"multipleMatches":"all"}}`,
			wantNeedle: "one to one",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, compareFixture(test.parameters))
			placeholder := nodeByName(result.Document, "Compare")
			if placeholder.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", placeholder.Type)
			}
			blocking := blockingFor(result, "Compare")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming %q", blocking, test.wantNeedle)
			}
			// Refused, not lost: the whole node goes back out as n8n wrote it,
			// on its four output slots.
			exported, err := n8n.Export(result.Document, registry(t))
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			if slots := exported.Document.Connections["Compare"]["main"]; len(slots) != 4 {
				t.Fatalf("exported output slots = %d, want the placeholder to keep all 4", len(slots))
			}
		})
	}
}

// An untouched fuzzy flag and the default match mode are not options at all.
func TestCompareDatasetsAcceptsTheDefaultsExplicitly(t *testing.T) {
	t.Parallel()

	result := importFixture(t, compareFixture(
		`{"mergeByFields":{"values":[{"field1":"id","field2":"id"}]},"options":{"fuzzyCompare":false,"multipleMatches":"first"}}`))
	if imported := nodeByName(result.Document, "Compare"); imported.Type != n8n.CompareDatasetsNodeType {
		t.Fatalf("imported as %q, want the native node", imported.Type)
	}
}

// --- The deliberately-unavailable reason table -----------------------------------

func TestServerLocalNodesSayTheyAreIntentionallyUnavailableAndPointElsewhere(t *testing.T) {
	t.Parallel()

	tests := []struct {
		nodeType    string
		wantNeedles []string
	}{
		{nodeType: "n8n-nodes-base.executeCommand", wantNeedles: []string{"shell commands", "HTTP Request node (kilasflow.httpRequest)"}},
		{nodeType: "n8n-nodes-base.readWriteFile", wantNeedles: []string{"host's disk", "Google Drive node (kilasflow.googleDrive)", "Data table (kilasflow.datastore)"}},
		{nodeType: "n8n-nodes-base.writeBinaryFile", wantNeedles: []string{"host's disk", "Google Drive node (kilasflow.googleDrive)"}},
		{nodeType: "n8n-nodes-base.localFileTrigger", wantNeedles: []string{"watches a folder", "Google Drive Trigger node (kilasflow.googleDriveTrigger)", "Webhook node (kilasflow.webhook)"}},
		{nodeType: "n8n-nodes-base.ssh", wantNeedles: []string{"SSH session", "HTTP Request node (kilasflow.httpRequest)"}},
		{nodeType: "n8n-nodes-base.ftp", wantNeedles: []string{"FTP and SFTP", "Google Drive node (kilasflow.googleDrive)"}},
	}
	for _, test := range tests {
		t.Run(test.nodeType, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, legacyNodeFixture(test.nodeType, 1, `{}`))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 {
				t.Fatalf("blocking issues = %#v, want exactly one", blocking)
			}
			reason := blocking[0].Reason
			for _, needle := range append([]string{"deliberately", "multi-tenant", test.nodeType}, test.wantNeedles...) {
				if !strings.Contains(reason, needle) {
					t.Errorf("reason = %q, want it to contain %q", reason, needle)
				}
			}
			// A decision, not a gap: the "no equivalent" sentence is for gaps.
			if strings.Contains(reason, "has no equivalent") {
				t.Errorf("reason = %q, want a deliberate refusal rather than the gap sentence", reason)
			}
			// The node is still preserved whole.
			exported, err := n8n.Export(result.Document, registry(t))
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			for _, candidate := range exported.Document.Nodes {
				if candidate.Name == "Legacy" && candidate.Type != test.nodeType {
					t.Fatalf("exported as %q, want %q", candidate.Type, test.nodeType)
				}
			}
		})
	}
}

// A node that is merely not built yet is a gap, and the plain sentence says so:
// the reason table must never excuse one.
func TestAnOrdinaryGapIsNotCalledDeliberate(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.microsoftOutlook", 2, `{}`))
	blocking := blockingFor(result, "Legacy")
	if len(blocking) != 1 {
		t.Fatalf("blocking issues = %#v, want exactly one", blocking)
	}
	if strings.Contains(blocking[0].Reason, "deliberately") || !strings.Contains(blocking[0].Reason, "has no equivalent") {
		t.Fatalf("reason = %q, want the plain gap sentence", blocking[0].Reason)
	}
}
