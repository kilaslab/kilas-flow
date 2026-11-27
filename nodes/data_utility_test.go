package nodes_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// renameRows is the `keys` collection as n8n writes it.
func renameRows(pairs ...string) map[string]any {
	rows := make([]any, 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		rows = append(rows, map[string]any{"currentKey": pairs[index], "newKey": pairs[index+1]})
	}
	return map[string]any{"key": rows}
}

func runRenameKeys(t *testing.T, keys map[string]any, items ...workflow.Item) []workflow.Item {
	t.Helper()
	output, err := flowExecutor(t, nodes.RenameKeysExecutorID).Execute(context.Background(),
		flowNode(t, nodes.RenameKeysNodeType, map[string]any{"keys": keys}),
		workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 {
		t.Fatalf("Rename Keys produced %d outputs, want one", len(output))
	}
	return output[0]
}

func TestRenameKeysRenamesTopLevelFields(t *testing.T) {
	t.Parallel()

	items := runRenameKeys(t, renameRows("first_name", "firstName", "email", "mail"),
		workflow.Item{JSON: map[string]any{"first_name": "Ada", "email": "ada@example.com", "age": float64(36)}})

	want := map[string]any{"firstName": "Ada", "mail": "ada@example.com", "age": float64(36)}
	if len(items) != 1 || !reflect.DeepEqual(items[0].JSON, want) {
		t.Fatalf("items = %#v, want %#v", items, want)
	}
}

func TestRenameKeysReachesNestedFieldsThroughDotNotation(t *testing.T) {
	t.Parallel()

	items := runRenameKeys(t, renameRows("user.name", "profile.fullName", "user.id", "id"),
		workflow.Item{JSON: map[string]any{"user": map[string]any{"name": "Ada", "id": float64(7)}}})

	want := map[string]any{
		"user":    map[string]any{},
		"profile": map[string]any{"fullName": "Ada"},
		"id":      float64(7),
	}
	if !reflect.DeepEqual(items[0].JSON, want) {
		t.Fatalf("item = %#v, want %#v", items[0].JSON, want)
	}
}

func TestRenameKeysLeavesAMissingFieldAndRenamesNull(t *testing.T) {
	t.Parallel()

	items := runRenameKeys(t, renameRows("absent", "other", "nothing", "none"),
		workflow.Item{JSON: map[string]any{"kept": true, "nothing": nil}})

	if _, created := items[0].JSON["other"]; created {
		t.Fatalf("item = %#v, want a missing field to stay missing", items[0].JSON)
	}
	// A field holding null exists: it is renamed like any other rather than
	// being mistaken for an absent one.
	if value, present := items[0].JSON["none"]; !present || value != nil {
		t.Fatalf("item = %#v, want the null field renamed to none", items[0].JSON)
	}
	if _, left := items[0].JSON["nothing"]; left {
		t.Fatalf("item = %#v, want the old name gone", items[0].JSON)
	}
}

func TestRenameKeysOverwritesAnExistingNewName(t *testing.T) {
	t.Parallel()

	items := runRenameKeys(t, renameRows("a", "b"), workflow.Item{JSON: map[string]any{"a": "from-a", "b": "from-b"}})

	if !reflect.DeepEqual(items[0].JSON, map[string]any{"b": "from-a"}) {
		t.Fatalf("item = %#v, want the renamed value to win", items[0].JSON)
	}
}

func TestRenameKeysKeepsBinaryAndProvenanceAndDoesNotMutateItsInput(t *testing.T) {
	t.Parallel()

	source := workflow.Item{
		JSON:   map[string]any{"a": "x"},
		Binary: map[string]workflow.BinaryRef{"data": {FileName: "photo.png"}},
		Paired: &workflow.PairedItem{SourceNodeID: "upstream", ItemIndex: 3},
	}
	items := runRenameKeys(t, renameRows("a", "b"), source)

	if items[0].Binary["data"].FileName != "photo.png" {
		t.Fatalf("binary = %#v, want the attachment carried", items[0].Binary)
	}
	if items[0].Paired == nil || items[0].Paired.SourceNodeID != "upstream" || items[0].Paired.ItemIndex != 3 {
		t.Fatalf("paired = %#v, want the provenance carried", items[0].Paired)
	}
	if _, untouched := source.JSON["a"]; !untouched {
		t.Fatalf("the input item was changed: %#v", source.JSON)
	}
}

func TestRenameKeysValidation(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodes.RenameKeysNodeType, workflow.V(1))
	if !found {
		t.Fatal("Rename Keys is not registered")
	}

	tests := []struct {
		name       string
		parameters map[string]any
		wantErr    string
	}{
		{name: "rows are enough", parameters: map[string]any{"keys": renameRows("a", "b")}},
		{name: "no rows", parameters: map[string]any{"keys": map[string]any{"key": []any{}}}, wantErr: "at least one key"},
		{name: "no keys parameter", parameters: map[string]any{}, wantErr: "at least one key"},
		{name: "empty new name", parameters: map[string]any{"keys": renameRows("a", "")}, wantErr: "both a current and a new key name"},
		{name: "not a collection", parameters: map[string]any{"keys": "a=b"}, wantErr: "fixed collection"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := definition.Validate(workflow.Node{Parameters: test.parameters})
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v, want none", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}

// --- Compare Datasets ------------------------------------------------------------

func matchRows(pairs ...string) map[string]any {
	rows := make([]any, 0, len(pairs)/2)
	for index := 0; index+1 < len(pairs); index += 2 {
		rows = append(rows, map[string]any{"field1": pairs[index], "field2": pairs[index+1]})
	}
	return map[string]any{"values": rows}
}

func record(fields map[string]any) workflow.Item { return workflow.Item{JSON: fields} }

// compare runs Compare Datasets and returns its four outputs as named, in the
// order the definition declares them.
func compare(t *testing.T, parameters map[string]any, a, b []workflow.Item) (onlyA, same, different, onlyB []workflow.Item) {
	t.Helper()
	input := workflow.NodeInput{}
	if a != nil {
		input[nodes.CompareInputA] = a
	}
	if b != nil {
		input[nodes.CompareInputB] = b
	}
	output, err := flowExecutor(t, nodes.CompareDatasetsExecutorID).Execute(context.Background(),
		flowNode(t, nodes.CompareDatasetsNodeType, parameters), input, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 4 {
		t.Fatalf("Compare Datasets produced %d outputs, want four", len(output))
	}
	return output[0], output[1], output[2], output[3]
}

func TestCompareDatasetsSortsEveryItemIntoItsOutput(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{
		record(map[string]any{"id": float64(1), "name": "Ada", "city": "London"}),
		record(map[string]any{"id": float64(2), "name": "Grace", "city": "NYC"}),
		record(map[string]any{"id": float64(3), "name": "Linus", "city": "Helsinki"}),
	}
	b := []workflow.Item{
		record(map[string]any{"id": float64(2), "name": "Grace", "city": "NYC"}),
		record(map[string]any{"id": float64(3), "name": "Linus", "city": "Portland"}),
		record(map[string]any{"id": float64(4), "name": "Dennis", "city": "NJ"}),
	}
	onlyA, same, different, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, b)

	if len(onlyA) != 1 || onlyA[0].JSON["name"] != "Ada" {
		t.Fatalf("In A only = %#v, want Ada", onlyA)
	}
	if len(same) != 1 || same[0].JSON["name"] != "Grace" {
		t.Fatalf("Same = %#v, want Grace", same)
	}
	if len(onlyB) != 1 || onlyB[0].JSON["name"] != "Dennis" {
		t.Fatalf("In B only = %#v, want Dennis", onlyB)
	}
	if len(different) != 1 {
		t.Fatalf("Different = %#v, want Linus alone", different)
	}
	want := map[string]any{
		"keys": map[string]any{"id": float64(3)},
		"same": map[string]any{"name": "Linus"},
		"different": map[string]any{
			"city": map[string]any{"inputA": "Helsinki", "inputB": "Portland"},
		},
	}
	if !reflect.DeepEqual(different[0].JSON, want) {
		t.Fatalf("Different item = %#v, want %#v", different[0].JSON, want)
	}
}

func TestCompareDatasetsMatchesFieldsWithDifferentNamesOnEachSide(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"email": "ada@example.com", "plan": "pro"})}
	b := []workflow.Item{record(map[string]any{"contact": "ada@example.com", "plan": "pro"})}
	onlyA, same, different, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("email", "contact")}, a, b)

	if len(onlyA) != 0 || len(different) != 0 || len(onlyB) != 0 || len(same) != 1 {
		t.Fatalf("outputs = %d/%d/%d/%d, want the pair on Same alone", len(onlyA), len(same), len(different), len(onlyB))
	}
}

func TestCompareDatasetsNeedsEveryMatchedPairToAgree(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"first": "Ada", "last": "Lovelace"})}
	b := []workflow.Item{record(map[string]any{"first": "Ada", "last": "Byron"})}
	onlyA, _, _, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("first", "first", "last", "last")}, a, b)

	// Two pairs, and one of them disagrees: these are different records, not
	// one record with a different field.
	if len(onlyA) != 1 || len(onlyB) != 1 {
		t.Fatalf("In A only %d, In B only %d, want one each", len(onlyA), len(onlyB))
	}
}

func TestCompareDatasetsPairsDuplicateKeysOneToOne(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{
		record(map[string]any{"id": float64(1), "n": "a1"}),
		record(map[string]any{"id": float64(1), "n": "a2"}),
		record(map[string]any{"id": float64(1), "n": "a3"}),
	}
	b := []workflow.Item{
		record(map[string]any{"id": float64(1), "n": "a1"}),
		record(map[string]any{"id": float64(1), "n": "a2"}),
	}
	onlyA, same, different, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, b)

	// Two partners for three items: the first two pair in order, and the third
	// has none rather than claiming the first partner again.
	if len(same) != 2 || len(onlyA) != 1 || onlyA[0].JSON["n"] != "a3" || len(different) != 0 || len(onlyB) != 0 {
		t.Fatalf("outputs = onlyA %#v same %#v different %#v onlyB %#v", onlyA, same, different, onlyB)
	}
}

func TestCompareDatasetsReadsDotNotationUnlessDisabled(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"user": map[string]any{"id": float64(1)}, "v": "x"})}
	b := []workflow.Item{record(map[string]any{"user": map[string]any{"id": float64(1)}, "v": "y"})}

	_, _, different, _ := compare(t, map[string]any{"mergeByFields": matchRows("user.id", "user.id")}, a, b)
	if len(different) != 1 {
		t.Fatalf("Different = %#v, want the nested key to match", different)
	}

	// With dot notation off, `user.id` names a field that neither item has.
	onlyA, _, _, onlyB := compare(t, map[string]any{
		"mergeByFields": matchRows("user.id", "user.id"),
		"options":       map[string]any{"disableDotNotation": true},
	}, a, b)
	if len(onlyA) != 1 || len(onlyB) != 1 {
		t.Fatalf("In A only %d, In B only %d, want no match without dot notation", len(onlyA), len(onlyB))
	}
}

func TestCompareDatasetsSkipsTheFieldsItIsToldTo(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"id": float64(1), "name": "Ada", "updatedAt": "monday"})}
	b := []workflow.Item{record(map[string]any{"id": float64(1), "name": "Ada", "updatedAt": "tuesday"})}

	_, _, different, _ := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, b)
	if len(different) != 1 {
		t.Fatalf("Different = %#v, want the timestamp to count", different)
	}
	_, same, different, _ := compare(t, map[string]any{
		"mergeByFields": matchRows("id", "id"),
		"options":       map[string]any{"skipFields": "updatedAt"},
	}, a, b)
	if len(same) != 1 || len(different) != 0 {
		t.Fatalf("Same %d, Different %d, want the pair equal once the timestamp is skipped", len(same), len(different))
	}
}

func TestCompareDatasetsTreatsAFieldOnlyOneSideHasAsADifference(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"id": float64(1), "name": "Ada"})}
	b := []workflow.Item{record(map[string]any{"id": float64(1), "name": "Ada", "extra": true})}

	_, _, different, _ := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, b)
	if len(different) != 1 {
		t.Fatalf("Different = %#v, want the extra field to count", different)
	}
	changes, _ := different[0].JSON["different"].(map[string]any)
	extra, _ := changes["extra"].(map[string]any)
	if extra["inputA"] != nil || extra["inputB"] != true {
		t.Fatalf("different = %#v, want extra null on A and true on B", changes)
	}
}

func TestCompareDatasetsResolvesADifferenceToOneInputsItem(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"id": float64(1), "city": "London"})}
	b := []workflow.Item{record(map[string]any{"id": float64(1), "city": "Paris"})}

	for resolve, wantCity := range map[string]string{"preferInput1": "London", "preferInput2": "Paris"} {
		_, _, different, _ := compare(t, map[string]any{"mergeByFields": matchRows("id", "id"), "resolve": resolve}, a, b)
		if len(different) != 1 || different[0].JSON["city"] != wantCity {
			t.Fatalf("%s: Different = %#v, want the item with city %q", resolve, different, wantCity)
		}
	}
}

func TestCompareDatasetsRunsWhenOnlyOneInputHasItems(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"id": float64(1)})}
	onlyA, same, different, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, nil)
	if len(onlyA) != 1 || len(same) != 0 || len(different) != 0 || len(onlyB) != 0 {
		t.Fatalf("outputs = %d/%d/%d/%d, want everything on In A only", len(onlyA), len(same), len(different), len(onlyB))
	}

	onlyA, _, _, onlyB = compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, nil, []workflow.Item{record(map[string]any{"id": float64(9)})})
	if len(onlyA) != 0 || len(onlyB) != 1 {
		t.Fatalf("In A only %d, In B only %d, want the B item alone", len(onlyA), len(onlyB))
	}
}

func TestCompareDatasetsAnItemMissingAMatchFieldHasNoPartner(t *testing.T) {
	t.Parallel()

	a := []workflow.Item{record(map[string]any{"name": "no id"})}
	b := []workflow.Item{record(map[string]any{"name": "no id either"})}
	onlyA, _, _, onlyB := compare(t, map[string]any{"mergeByFields": matchRows("id", "id")}, a, b)
	// A missing field is not a value that happens to equal another missing one.
	if len(onlyA) != 1 || len(onlyB) != 1 {
		t.Fatalf("In A only %d, In B only %d, want the two to stay apart", len(onlyA), len(onlyB))
	}
}

func TestCompareDatasetsDeclaresTwoInputsAndFourOutputsInN8NsOrder(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, found := registry.Lookup(nodes.CompareDatasetsNodeType, workflow.V(1))
	if !found {
		t.Fatal("Compare Datasets is not registered")
	}
	var inputs, outputs []string
	for _, port := range definition.Inputs {
		inputs = append(inputs, port.Name)
	}
	for _, port := range definition.Outputs {
		outputs = append(outputs, port.Name)
	}
	if want := []string{"input1", "input2"}; !reflect.DeepEqual(inputs, want) {
		t.Fatalf("inputs = %v, want %v", inputs, want)
	}
	if want := []string{"onlyInA", "same", "different", "onlyInB"}; !reflect.DeepEqual(outputs, want) {
		t.Fatalf("outputs = %v, want %v", outputs, want)
	}
}

func TestCompareDatasetsValidation(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.CompareDatasetsNodeType, workflow.V(1))

	tests := []struct {
		name       string
		parameters map[string]any
		wantErr    string
	}{
		{name: "one pair is enough", parameters: map[string]any{"mergeByFields": matchRows("id", "id")}},
		{name: "no pair", parameters: map[string]any{}, wantErr: "at least one pair"},
		{name: "half a pair", parameters: map[string]any{"mergeByFields": matchRows("id", "")}, wantErr: "on both inputs"},
		{name: "mixing versions is refused", parameters: map[string]any{"mergeByFields": matchRows("id", "id"), "resolve": "mix"}, wantErr: "mix of versions"},
		{name: "an unknown resolution", parameters: map[string]any{"mergeByFields": matchRows("id", "id"), "resolve": "other"}, wantErr: "not supported"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			err := definition.Validate(workflow.Node{Parameters: test.parameters})
			switch {
			case test.wantErr == "" && err != nil:
				t.Fatalf("Validate() error = %v, want none", err)
			case test.wantErr != "" && (err == nil || !strings.Contains(err.Error(), test.wantErr)):
				t.Fatalf("Validate() error = %v, want it to mention %q", err, test.wantErr)
			}
		})
	}
}
