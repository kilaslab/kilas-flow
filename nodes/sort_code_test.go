package nodes_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// scores are four items with a tie on score (b and d), the first with a
// file, each descending from a different upstream item.
func scores() workflow.NodeInput {
	names := []string{"a", "b", "c", "d"}
	values := []float64{30, 10, 20, 10}
	items := make([]workflow.Item, len(names))
	for index := range names {
		items[index] = workflow.Item{
			JSON:   map[string]any{"name": names[index], "score": values[index]},
			Paired: &workflow.PairedItem{SourceNodeID: "upstream", ItemIndex: index},
		}
	}
	items[0].Binary = map[string]workflow.BinaryRef{"data": {ID: "bin_a", FileName: "a.txt", MediaType: "text/plain", Size: 1}}
	return workflow.NodeInput{"main": items}
}

func sortByCode(t *testing.T, code string, options ...nodes.ExecutorOption) (workflow.NodeOutput, error) {
	t.Helper()
	return jsExecutor(t, nodes.SortExecutorID, options...).Execute(context.Background(),
		jsNode(nodes.SortNodeType, map[string]any{"type": "code", "code": code}), scores(), engine.Request{})
}

func namesOf(items []workflow.Item) []string {
	names := make([]string, len(items))
	for index, item := range items {
		names[index], _ = item.JSON["name"].(string)
	}
	return names
}

// The comparator runs on the JavaScript runtime and decides the order; the
// items themselves are the node's own, moved, so their files and lineage go
// with them. A tie keeps the order the items arrived in.
func TestTheSortNodeSortsWithAJavaScriptComparator(t *testing.T) {
	output, err := sortByCode(t, "return a.json.score - b.json.score;")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := namesOf(output[0]), []string{"b", "d", "c", "a"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
	last := output[0][3]
	if last.Binary["data"].ID != "bin_a" || last.Paired == nil || last.Paired.ItemIndex != 0 || last.Paired.SourceNodeID != "upstream" {
		t.Fatalf("moved item = %#v, want its file and lineage with it", last)
	}
	if output[0][1].Paired.ItemIndex != 3 {
		t.Fatalf("lineage = %#v, want each item's own", output[0][1].Paired)
	}

	byName, err := sortByCode(t, "return a.json.name < b.json.name ? 1 : a.json.name > b.json.name ? -1 : 0;")
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if got, want := namesOf(byName[0]), []string{"d", "c", "b", "a"}; !slices.Equal(got, want) {
		t.Fatalf("order = %v, want names descending %v", got, want)
	}
}

func TestASortComparatorFailureNamesTheNodeAndTheLine(t *testing.T) {
	_, err := sortByCode(t, "const limit = 5;\nthrow new RangeError('too far');")
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || script.Line != 2 || !strings.HasPrefix(err.Error(), `node "Code": RangeError: too far [line 2]`) {
		t.Fatalf("Execute() error = %v, want the throw located on line 2", err)
	}
	_, err = sortByCode(t, "return a.json.score > b.json.score;")
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "the comparator returned a boolean") || !strings.HasSuffix(err.Error(), "[line 1]") {
		t.Fatalf("Execute() error = %v, want a non-number named", err)
	}
}

// A deployment that turned JavaScript off refuses a comparator exactly as it
// refuses a Code node; a Sort by fields is not JavaScript and still runs.
func TestADisabledRuntimeRefusesASortComparatorAsItRefusesCode(t *testing.T) {
	off := nodes.WithoutJavaScript(nodes.JavaScriptDisabled)
	_, sortErr := sortByCode(t, "return a.json.score - b.json.score;", off)
	_, codeErr := jsExecutor(t, nodes.JSCodeExecutorID, off).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"jsCode": "return items"}), threeItems(), engine.Request{})
	if sortErr == nil || codeErr == nil || sortErr.Error() != codeErr.Error() {
		t.Fatalf("sort says %v, code says %v; want the same refusal", sortErr, codeErr)
	}
	output, err := jsExecutor(t, nodes.SortExecutorID, off).Execute(context.Background(),
		jsNode(nodes.SortNodeType, map[string]any{"type": "simple", "sortFieldsUI": "score"}), scores(), engine.Request{})
	if err != nil || len(output[0]) != 4 {
		t.Fatalf("Execute() = %#v, %v; want a field sort unaffected", output, err)
	}
}

func sortDefinition(t *testing.T) node.Definition {
	t.Helper()
	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, ok := registry.Get(nodes.SortNodeType, workflow.V(1))
	if !ok {
		t.Fatal("the Sort node is not registered")
	}
	return definition
}

// Validation reads the comparator as the importer and a run do: a construct
// the runtime refuses is refused in the one sentence, and one that never
// returns a value is caught before it runs, as n8n catches it.
func TestTheSortNodeValidatesItsComparator(t *testing.T) {
	definition := sortDefinition(t)
	validate := func(code string) error {
		return definition.Validate(workflow.Node{Parameters: map[string]any{"type": "code", "code": code}})
	}
	if err := validate("return a.json.score - b.json.score;"); err != nil {
		t.Fatalf("Validate() = %v, want a comparator accepted", err)
	}
	const refused = "return /\\p{L}/u.test(a.json.name) ? -1 : 1;"
	_, analysed := jsrun.Analyze(refused, jsrun.ModeComparator)
	if err := validate(refused); err == nil || analysed == nil || err.Error() != analysed.Error() || !strings.Contains(err.Error(), "which this server does not run") {
		t.Fatalf("Validate() = %v, want the refusal sentence %v", err, analysed)
	}
	for code, want := range map[string]string{
		"   ":                                "the comparator is empty",
		"a.json.score - b.json.score":        "never returns",
		"const f = () => { return 1; }; f()": "never returns",
		"return a.json.score - ;":            "SyntaxError",
	} {
		if err := validate(code); err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%q: Validate() = %v, want %q", code, err, want)
		}
	}
}

// The editor draws the node from its definition: Code is one of the sort
// types, and the comparator's field shows only when it is chosen.
func TestTheSortNodeOffersTheCodeType(t *testing.T) {
	definition := sortDefinition(t)
	if !definition.WholeBatch {
		t.Error("the Sort node is not whole-batch; split into one-item calls, a comparator would sort nothing")
	}
	var offered []string
	var code *node.PropertyDefinition
	for index, property := range definition.Parameters {
		switch property.Key {
		case "type":
			for _, option := range property.Options {
				offered = append(offered, option.Value)
			}
		case "code":
			code = &definition.Parameters[index]
		}
	}
	if !slices.Equal(offered, []string{"simple", "random", "code"}) {
		t.Fatalf("sort types = %v, want n8n's three", offered)
	}
	if code == nil || len(code.VisibleWhen) != 1 || code.VisibleWhen[0].Key != "type" || code.VisibleWhen[0].Equals != "code" {
		t.Fatalf("code property = %#v, want it shown only for the code type", code)
	}
	if code.TypeOptions == nil || code.TypeOptions.Rows < 2 {
		t.Fatalf("code property = %#v, want a multi-line editor", code)
	}
}

// The comparator mode belongs to the Sort node. A Code node naming it, which
// validation already refuses, does not run as one either.
func TestACodeNodeCannotRunAsAComparator(t *testing.T) {
	_, err := jsExecutor(t, nodes.JSCodeExecutorID).Execute(context.Background(),
		jsNode(nodes.JSCodeNodeType, map[string]any{"mode": string(jsrun.ModeComparator), "jsCode": "return 0"}), threeItems(), engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "is neither") {
		t.Fatalf("Execute() error = %v, want the mode refused", err)
	}
}
