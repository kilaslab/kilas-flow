package nodes_test

import (
	"context"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// TestMergePreservesEachSidesProvenance is the case the runner cannot infer.
//
// Merge concatenates two unrelated streams, so an output item's position says
// nothing about where it came from. Flattening or renumbering would make a
// later reach-back confidently wrong rather than honestly unable.
func TestMergePreservesEachSidesProvenance(t *testing.T) {
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := registry.Lookup("core.merge")
	if !found {
		t.Fatal("the merge executor is not registered")
	}

	input := workflow.NodeInput{
		"input1": {
			{JSON: map[string]any{"side": "left", "n": 1},
				Paired: &workflow.PairedItem{SourceNodeID: "left-source", ItemIndex: 0}},
			{JSON: map[string]any{"side": "left", "n": 2},
				Paired: &workflow.PairedItem{SourceNodeID: "left-source", ItemIndex: 1}},
		},
		"input2": {
			{JSON: map[string]any{"side": "right", "n": 1},
				Paired: &workflow.PairedItem{SourceNodeID: "right-source", ItemIndex: 0}},
		},
	}

	// A compiled node always carries its definition, and Merge's inputs are
	// now computed from `numberInputs` rather than being a fixed pair — so the
	// ports are what the executor reads its streams from.
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "merge", Name: "Merge", Parameters: map[string]any{"mode": "append"},
		Definition: workflow.NodeDefinition{Inputs: []workflow.Port{
			{Name: "input1", Kind: workflow.ConnectionMain},
			{Name: "input2", Kind: workflow.ConnectionMain},
		}},
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 3 {
		t.Fatalf("merge produced %#v, want three items on one port", output)
	}

	for index, want := range []struct {
		source string
		item   int
	}{
		{"left-source", 0},
		{"left-source", 1},
		{"right-source", 0},
	} {
		paired := output[0][index].Paired
		if paired == nil {
			t.Errorf("item %d lost its provenance in the merge", index)
			continue
		}
		if paired.SourceNodeID != want.source || paired.ItemIndex != want.item {
			t.Errorf("item %d descends from %s[%d], want %s[%d]",
				index, paired.SourceNodeID, paired.ItemIndex, want.source, want.item)
		}
	}
}

// TestIFRoutesItemsWithoutRenumberingTheirProvenance covers the filtering node.
// The runner only infers by position when the counts match, and IF's never do —
// so it keeps each item's own origin, which is what makes a reach-back from
// either branch land on the right item.
func TestIFRoutesItemsWithoutRenumberingTheirProvenance(t *testing.T) {
	registry := engine.NewRegistry()
	if err := nodes.RegisterExecutors(registry, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := registry.Lookup("core.if")

	input := workflow.NodeInput{"main": {
		{JSON: map[string]any{"tier": "vip"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 0}},
		{JSON: map[string]any{"tier": "std"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 1}},
		{JSON: map[string]any{"tier": "vip"}, Paired: &workflow.PairedItem{SourceNodeID: "src", ItemIndex: 2}},
	}}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "if", Name: "IF",
		Parameters: map[string]any{"conditions": []any{map[string]any{
			"field": "tier", "operator": "equals", "value": "vip",
		}}},
	}, input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	// The true branch holds items 0 and 2 — at positions 0 and 1 — and each
	// must still say which input item it was.
	if len(output[0]) != 2 {
		t.Fatalf("true branch = %#v, want two items", output[0])
	}
	for position, wantIndex := range []int{0, 2} {
		if got := output[0][position].Paired.ItemIndex; got != wantIndex {
			t.Errorf("true branch position %d descends from item %d, want %d — renumbering by position is exactly the wrong answer here",
				position, got, wantIndex)
		}
	}
	if len(output[1]) != 1 || output[1][0].Paired.ItemIndex != 1 {
		t.Errorf("false branch = %#v, want the item that was at index 1", output[1])
	}
}

// An assignment whose value is an expression writes the resolved value, not the
// marker.
//
// This was the defect: Set read `node.Parameters` directly and took the request
// as `_`, so a workflow assigning `{{ $json.name }}` produced an item whose
// field was the literal object `{"mode":"expression","value":"…"}`. No error,
// no diagnostic — the workflow ran and the data was wrong. The n8n importer
// translates every `=`-prefixed string into exactly that marker, so every
// imported workflow using an expression in a Set was affected.
func TestSetResolvesAnExpressionAssignmentPerItem(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.set")

	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{
			"greeting": map[string]any{"mode": "expression", "value": "Hello {{ $json.name }}"},
			"fixed":    "unchanged",
			"index":    map[string]any{"mode": "expression", "value": "{{ $itemIndex }}"},
		}},
	}, workflow.NodeInput{"main": {
		{JSON: map[string]any{"name": "Ada"}},
		{JSON: map[string]any{"name": "Grace"}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if len(output[0]) != 2 {
		t.Fatalf("output = %#v, want one item per input item", output[0])
	}
	// Per item, not once for the node: resolving outside the loop would write
	// the first item's value onto both.
	if output[0][0].JSON["greeting"] != "Hello Ada" || output[0][1].JSON["greeting"] != "Hello Grace" {
		t.Fatalf("greetings = %#v and %#v, want each item's own value",
			output[0][0].JSON["greeting"], output[0][1].JSON["greeting"])
	}
	if output[0][0].JSON["index"] != float64(0) || output[0][1].JSON["index"] != float64(1) {
		t.Fatalf("indexes = %#v and %#v, want each item's own position",
			output[0][0].JSON["index"], output[0][1].JSON["index"])
	}
	if output[0][0].JSON["fixed"] != "unchanged" {
		t.Fatalf("fixed = %#v, want a literal left alone", output[0][0].JSON["fixed"])
	}
}

// An IF condition whose value is an expression compares the resolved value.
// Against an unresolved marker every item takes the same branch, which is the
// quiet half of the same defect.
func TestIFResolvesAnExpressionConditionPerItem(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.if")

	// The value is read from the item itself, so the two rows must part.
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"conditions": []any{map[string]any{
			"field": "tier", "operator": "equals",
			"value": map[string]any{"mode": "expression", "value": "{{ $json.wanted }}"},
		}}},
	}, workflow.NodeInput{"main": {
		{JSON: map[string]any{"tier": "vip", "wanted": "vip"}},
		{JSON: map[string]any{"tier": "vip", "wanted": "standard"}},
	}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 1 || output[0][0].JSON["wanted"] != "vip" {
		t.Fatalf("true branch = %#v, want the matching item only", output[0])
	}
	if len(output[1]) != 1 || output[1][0].JSON["wanted"] != "standard" {
		t.Fatalf("false branch = %#v, want the non-matching item only", output[1])
	}
}

// A malformed condition is one error, not one per row.
func TestIFReportsAMalformedConditionOnceBeforeAnyItem(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.if")

	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "if", Name: "IF", Type: "kilasflow.if", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"conditions": []any{}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil {
		t.Fatal("an empty condition list was accepted")
	}
}

// A Set node saved before assignments had order or types still loads, still
// validates and still produces the same items.
//
// The old shape is a plain `{name: value}` map. Refusing it would break every
// workflow saved before this change, and silently rewriting it would rewrite
// documents nobody asked to touch.
func TestSetStillRunsTheOldFlatAssignmentShape(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.set")

	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{
			"status": "ready",
			"count":  float64(2),
			"note":   map[string]any{"mode": "expression", "value": "for {{ $json.name }}"},
		}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{"name": "Ada"}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0].JSON
	// No declared type: the value goes through as it is, which is what the old
	// executor did.
	if item["status"] != "ready" || item["count"] != float64(2) {
		t.Fatalf("item = %#v, want the values unchanged", item)
	}
	if item["note"] != "for Ada" {
		t.Fatalf("note = %#v, want the expression resolved", item["note"])
	}
}

// The ordered shape is what makes a declared type mean something, and what lets
// two rows write the same field.
func TestSetAppliesOrderedRowsInOrderWithTheirDeclaredTypes(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.set")

	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{"assignments": []any{
			map[string]any{"id": "r1", "name": "winner", "type": "string", "value": "first"},
			// A number arriving as text — which is what an expression over a
			// string field produces — becomes a number because the row says so.
			map[string]any{"id": "r2", "name": "count", "type": "number",
				"value": map[string]any{"mode": "expression", "value": "{{ $json.total }}"}},
			map[string]any{"id": "r3", "name": "active", "type": "boolean", "value": "true"},
			map[string]any{"id": "r4", "name": "tags", "type": "array", "value": `["a","b"]`},
			map[string]any{"id": "r5", "name": "meta", "type": "object", "value": `{"k":"v"}`},
			map[string]any{"id": "r6", "name": "winner", "type": "string", "value": "second"},
		}}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{"total": "42"}}}}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	item := output[0][0].JSON
	// The later row wins, which a map could not express at all.
	if item["winner"] != "second" {
		t.Fatalf("winner = %#v, want the later row's value", item["winner"])
	}
	if item["count"] != float64(42) {
		t.Fatalf("count = %#v, want a number rather than the text it arrived as", item["count"])
	}
	if item["active"] != true {
		t.Fatalf("active = %#v, want a boolean", item["active"])
	}
	tags, _ := item["tags"].([]any)
	if len(tags) != 2 || tags[0] != "a" {
		t.Fatalf("tags = %#v, want a decoded array", item["tags"])
	}
	meta, _ := item["meta"].(map[string]any)
	if meta["k"] != "v" {
		t.Fatalf("meta = %#v, want a decoded object", item["meta"])
	}
}

// A value that cannot be read as its declared type is named, not coerced into
// something plausible.
func TestSetRefusesAValueThatIsNotItsDeclaredType(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, _ := executors.Lookup("core.set")

	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "set", Name: "Set", Type: "kilasflow.set", TypeVersion: workflow.V(1),
		Parameters: map[string]any{"assignments": map[string]any{"assignments": []any{
			map[string]any{"id": "r1", "name": "count", "type": "number", "value": "not a number"},
		}}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil {
		t.Fatal("a value that is not its declared type was accepted")
	}
	if !strings.Contains(err.Error(), "count") || !strings.Contains(err.Error(), "number") {
		t.Errorf("error = %q, want it to name the row and the type", err)
	}
}
