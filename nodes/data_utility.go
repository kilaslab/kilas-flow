package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The data-utility family's server-owned bindings.
const (
	RenameKeysExecutorID      = "core.renameKeys"
	CompareDatasetsExecutorID = "core.compareDatasets"
)

// The data-utility family's node types.
const (
	RenameKeysNodeType      = "kilasflow.renameKeys"
	CompareDatasetsNodeType = "kilasflow.compareDatasets"
)

// Compare Datasets' port names, in the order n8n numbers its outputs: the
// importer resolves an n8n output index through the catalogue's own order, so
// this list is what keeps a wire drawn to "Different" on "Different".
const (
	CompareInputA   = "input1"
	CompareInputB   = "input2"
	CompareOnlyInA  = "onlyInA"
	CompareSame     = "same"
	CompareDiffers  = "different"
	CompareOnlyInB  = "onlyInB"
	compareMaxPairs = 32
)

// Compare Datasets' "when there are differences" choices, as n8n names them.
const (
	CompareResolveBoth    = "includeBoth"
	CompareResolveInputA  = "preferInput1"
	CompareResolveInputB  = "preferInput2"
	compareResolveMixName = "mix"
)

// --- Rename Keys ---------------------------------------------------------------

// renameKeysNode renames fields of each item: a Set node's one trick, kept as
// a node of its own because an imported workflow names it and because "rename
// these three fields" should not need an assignment table.
//
// Its parameters carry n8n's own shape — a fixed collection `keys` whose `key`
// rows hold `currentKey` and `newKey` — so an import is a copy and an export
// writes the same rows back.
func renameKeysNode() node.Definition {
	return node.Definition{
		Type:        RenameKeysNodeType,
		Version:     workflow.V(1),
		DisplayName: "Rename Keys",
		Description: "Renames fields on each item. Dot notation reaches into nested objects.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:key-round"},
		IconColor:   "#0ea5e9",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "keys", Label: "Keys", Kind: node.PropertyFixedCollection, Required: true,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add rename"},
				Description: "The renames, applied in order. A field an item does not have is left alone; a new name that already exists is overwritten.",
				Groups: []node.PropertyGroup{{
					Key: "key", Label: "Rename",
					Fields: []node.PropertyDefinition{
						{
							Key: "currentKey", Label: "Current Key Name", Kind: node.PropertyString, Required: true,
							Description: "The field to rename. Dot notation (`a.b`) reaches a nested field.",
						},
						{
							Key: "newKey", Label: "New Key Name", Kind: node.PropertyString, Required: true,
							Description: "What to call it. Dot notation moves the value into a nested object.",
						},
					},
				}},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     RenameKeysExecutorID,
		Validate:       validateRenameKeysConfiguration,
	}
}

// renameRow is one current-to-new pair.
type renameRow struct{ current, renamed string }

// readRenameRows reads the `keys` collection. An expression-valued collection
// has no rows to read until run time, so it reads as empty here and the
// executor refuses it there.
func readRenameRows(value any) ([]renameRow, error) {
	if value == nil || expression.IsExpression(value) {
		return nil, nil
	}
	collection, ok := value.(map[string]any)
	if !ok {
		return nil, fmt.Errorf("keys must be a fixed collection of rename rows")
	}
	raw, present := collection["key"]
	if !present {
		return nil, nil
	}
	list, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("keys.key must be a list of rename rows")
	}
	rows := make([]renameRow, 0, len(list))
	for index, entry := range list {
		fields, ok := entry.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("keys.key must be a list of rename rows")
		}
		rows = append(rows, renameRow{
			current: strings.TrimSpace(textOf(fields["currentKey"])),
			renamed: strings.TrimSpace(textOf(fields["newKey"])),
		})
		if rows[index].current == "" || rows[index].renamed == "" {
			return nil, fmt.Errorf("rename %d needs both a current and a new key name", index+1)
		}
	}
	return rows, nil
}

func validateRenameKeysConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["keys"]) {
		return nil
	}
	rows, err := readRenameRows(n.Parameters["keys"])
	if err != nil {
		return err
	}
	if len(rows) == 0 {
		return fmt.Errorf("a rename needs at least one key to rename")
	}
	return nil
}

func executeRenameKeys(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if expression.IsExpression(ir.Parameters["keys"]) {
		return nil, fmt.Errorf("node %q: the rename list must be written out, not computed by an expression", ir.Name)
	}
	rows, err := readRenameRows(ir.Parameters["keys"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if len(rows) == 0 {
		return nil, fmt.Errorf("node %q: a rename needs at least one key to rename", ir.Name)
	}

	renamed := make([]workflow.Item, 0, len(input["main"]))
	for _, item := range input["main"] {
		// cloneItem keeps the binary attachments and the provenance: a renamed
		// item is the same item, which is what lets a later reach-back find
		// the one it came from.
		built := cloneItem(item)
		if built.JSON == nil {
			built.JSON = map[string]any{}
		}
		for _, row := range rows {
			value, found := lookupPath(built.JSON, row.current)
			if !found || row.current == row.renamed {
				continue
			}
			unsetPath(built.JSON, row.current)
			setPath(built.JSON, row.renamed, value)
		}
		renamed = append(renamed, built)
	}
	return workflow.NodeOutput{renamed}, nil
}

// lookupPath reads a dotted path and says whether it was there, which a nil
// result cannot: a field holding null exists and is renamed like any other.
func lookupPath(object map[string]any, path string) (any, bool) {
	current := any(object)
	for _, segment := range strings.Split(path, ".") {
		next, ok := current.(map[string]any)
		if !ok {
			return nil, false
		}
		value, found := next[segment]
		if !found {
			return nil, false
		}
		current = value
	}
	return current, true
}

// setPath writes a value at a dotted path, creating the objects on the way. An
// intermediate that is not an object is replaced by one: the caller asked for
// a value at that path, and a path through a string has no other reading.
func setPath(object map[string]any, path string, value any) {
	segments := strings.Split(path, ".")
	current := object
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

// unsetPath removes the value at a dotted path and leaves the objects that held
// it, empty or not, where they are.
func unsetPath(object map[string]any, path string) {
	segments := strings.Split(path, ".")
	current := object
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			return
		}
		current = next
	}
	delete(current, segments[len(segments)-1])
}

// --- Compare Datasets ----------------------------------------------------------

// compareDatasetsNode splits two streams by what they have in common: items
// only in A, items the two agree on, items they disagree on, and items only in
// B. It is Merge's keep-matches and keep-non-matches modes with the remainder
// sorted rather than thrown away.
func compareDatasetsNode() node.Definition {
	return node.Definition{
		Type:        CompareDatasetsNodeType,
		Version:     workflow.V(1),
		DisplayName: "Compare Datasets",
		Description: "Compares two item streams by matching fields and sorts every item into: in A only, same, different, or in B only.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:git-compare"},
		IconColor:   "#f59e0b",
		Inputs: []workflow.Port{
			{Name: CompareInputA, DisplayName: "Input A", Kind: workflow.ConnectionMain},
			{Name: CompareInputB, DisplayName: "Input B", Kind: workflow.ConnectionMain},
		},
		Outputs: []workflow.Port{
			{Name: CompareOnlyInA, DisplayName: "In A only", Kind: workflow.ConnectionMain},
			{Name: CompareSame, DisplayName: "Same", Kind: workflow.ConnectionMain},
			{Name: CompareDiffers, DisplayName: "Different", Kind: workflow.ConnectionMain},
			{Name: CompareOnlyInB, DisplayName: "In B only", Kind: workflow.ConnectionMain},
		},
		Parameters: []node.PropertyDefinition{
			{
				Key: "mergeByFields", Label: "Fields to Match", Kind: node.PropertyFixedCollection, Required: true,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add fields to match"},
				Description: "Pairs of fields that must be equal for an A item and a B item to be the same record. Dot notation reaches nested fields.",
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Fields to Match",
					Fields: []node.PropertyDefinition{
						{Key: "field1", Label: "Input A Field", Kind: node.PropertyString, Required: true},
						{Key: "field2", Label: "Input B Field", Kind: node.PropertyString, Required: true},
					},
				}},
			},
			{
				Key: "resolve", Label: "When There Are Differences", Kind: node.PropertyOptions, Default: CompareResolveBoth,
				Options: []node.PropertyOption{
					{Label: "Include Both Versions", Value: CompareResolveBoth},
					{Label: "Use Input A Version", Value: CompareResolveInputA},
					{Label: "Use Input B Version", Value: CompareResolveInputB},
				},
				Description: "What the Different output carries for a matched pair that disagrees: a report of both versions, or the whole item from one input.",
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "skipFields", Label: "Fields to Skip Comparing", Kind: node.PropertyString,
						Description: "Comma-separated field names left out of the comparison, so a changing timestamp does not make every pair different.",
					},
					{
						Key: "disableDotNotation", Label: "Disable Dot Notation", Kind: node.PropertyBoolean, Default: false,
						Description: "On, a field named `a.b` is a field whose name contains a dot rather than a path into `a`.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CompareDatasetsExecutorID,
		Validate:       validateCompareDatasetsConfiguration,
		// Each side is compared with the whole of the other: handed one item
		// at a time, nothing would ever have a partner.
		WholeBatch: true,
	}
}

// comparePair is one pair of fields an A item and a B item must agree on.
type comparePair struct{ a, b string }

func readComparePairs(value any) ([]comparePair, error) {
	rows, err := fixedCollectionRows(value, "mergeByFields", "match")
	if err != nil {
		return nil, err
	}
	pairs := make([]comparePair, 0, len(rows))
	for index, row := range rows {
		pair := comparePair{a: strings.TrimSpace(textOf(row["field1"])), b: strings.TrimSpace(textOf(row["field2"]))}
		if pair.a == "" || pair.b == "" {
			return nil, fmt.Errorf("fields to match %d needs a field name on both inputs", index+1)
		}
		pairs = append(pairs, pair)
	}
	return pairs, nil
}

// compareOptions reads the options collection. A missing or malformed one is
// the defaults.
func compareOptions(parameters map[string]any) (skip []string, dotNotation bool) {
	options, _ := parameters["options"].(map[string]any)
	if text, ok := options["skipFields"].(string); ok {
		skip = splitFieldList(text)
	}
	disabled, _ := options["disableDotNotation"].(bool)
	return skip, !disabled
}

func validateCompareDatasetsConfiguration(n workflow.Node) error {
	if expression.IsExpression(n.Parameters["mergeByFields"]) {
		return nil
	}
	pairs, err := readComparePairs(n.Parameters["mergeByFields"])
	if err != nil {
		return err
	}
	if len(pairs) == 0 {
		return fmt.Errorf("comparing datasets needs at least one pair of fields to match")
	}
	if len(pairs) > compareMaxPairs {
		return fmt.Errorf("comparing datasets matches on at most %d field pairs", compareMaxPairs)
	}
	switch resolve := textOf(n.Parameters["resolve"]); resolve {
	case "", CompareResolveBoth, CompareResolveInputA, CompareResolveInputB:
	case compareResolveMixName:
		return fmt.Errorf("using a mix of versions for the differing fields is not supported: choose Input A's version, Input B's version, or both")
	default:
		return fmt.Errorf("resolve %q is not supported", resolve)
	}
	return nil
}

// compareKey renders the values of the fields an item is matched on, or false
// when one is missing: a missing field is not a value that happens to equal
// another missing field.
func compareKey(item workflow.Item, fields []string, dotNotation bool) (string, bool) {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		value, found := compareField(item.JSON, field, dotNotation)
		if !found || value == nil {
			return "", false
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		parts = append(parts, string(encoded))
	}
	return strings.Join(parts, "\x00"), true
}

func compareField(object map[string]any, field string, dotNotation bool) (any, bool) {
	if !dotNotation {
		value, found := object[field]
		return value, found
	}
	return lookupPath(object, field)
}

func removeCompareField(object map[string]any, field string, dotNotation bool) {
	if !dotNotation {
		delete(object, field)
		return
	}
	unsetPath(object, field)
}

// executeCompareDatasets sorts the two inputs into the four outputs.
//
// Pairing is one to one and in order: an A item takes the first B item that
// shares its key and has not been taken yet, so two A items with the same key
// pair with two B items rather than both claiming the first. What is left of A
// is "in A only"; what is left of B is "in B only". Ordering is A's order for
// the first three outputs and B's for the last, so the result never depends on
// map iteration.
func executeCompareDatasets(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateCompareDatasetsConfiguration(workflow.Node{Parameters: ir.Parameters}); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	pairs, err := readComparePairs(ir.Parameters["mergeByFields"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	skip, dotNotation := compareOptions(ir.Parameters)
	resolve := defaultString(textOf(ir.Parameters["resolve"]), CompareResolveBoth)

	fieldsA := make([]string, len(pairs))
	fieldsB := make([]string, len(pairs))
	for index, pair := range pairs {
		fieldsA[index], fieldsB[index] = pair.a, pair.b
	}

	itemsA, itemsB := input[CompareInputA], input[CompareInputB]
	// B's candidates by key, each key's items in B's own order.
	candidates := map[string][]int{}
	for index, item := range itemsB {
		if key, ok := compareKey(item, fieldsB, dotNotation); ok {
			candidates[key] = append(candidates[key], index)
		}
	}
	taken := make([]bool, len(itemsB))

	onlyA, same, different, onlyB := []workflow.Item{}, []workflow.Item{}, []workflow.Item{}, []workflow.Item{}
	for indexA, itemA := range itemsA {
		key, keyed := compareKey(itemA, fieldsA, dotNotation)
		partner := -1
		if keyed {
			if queue := candidates[key]; len(queue) > 0 {
				partner, candidates[key] = queue[0], queue[1:]
			}
		}
		if partner < 0 {
			onlyA = append(onlyA, cloneItem(itemA))
			continue
		}
		taken[partner] = true
		itemB := itemsB[partner]

		sameFields, differing := compareRecords(itemA, itemB, fieldsA, fieldsB, skip, dotNotation)
		if len(differing) == 0 {
			same = append(same, cloneItem(itemA))
			continue
		}
		different = append(different, differenceItem(ir, indexA, itemA, itemB, pairs, sameFields, differing, resolve, dotNotation))
	}
	for index, item := range itemsB {
		if !taken[index] {
			onlyB = append(onlyB, cloneItem(item))
		}
	}
	// The order is the definition's: In A only, Same, Different, In B only.
	return workflow.NodeOutput{onlyA, same, different, onlyB}, nil
}

// compareRecords compares what two matched items hold besides the fields they
// were matched on, one top-level field at a time. A field only one side has is
// a difference, with null standing for the side that lacks it.
func compareRecords(itemA, itemB workflow.Item, fieldsA, fieldsB, skip []string, dotNotation bool) (same map[string]any, different map[string]map[string]any) {
	restA, restB := cloneMap(itemA.JSON), cloneMap(itemB.JSON)
	for _, field := range fieldsA {
		removeCompareField(restA, field, dotNotation)
	}
	for _, field := range fieldsB {
		removeCompareField(restB, field, dotNotation)
	}
	for _, field := range skip {
		removeCompareField(restA, field, dotNotation)
		removeCompareField(restB, field, dotNotation)
	}

	names := map[string]bool{}
	for name := range restA {
		names[name] = true
	}
	for name := range restB {
		names[name] = true
	}
	ordered := make([]string, 0, len(names))
	for name := range names {
		ordered = append(ordered, name)
	}
	sort.Strings(ordered)

	same, different = map[string]any{}, map[string]map[string]any{}
	for _, name := range ordered {
		valueA, inA := restA[name]
		valueB, inB := restB[name]
		if inA && inB && jsonEqual(valueA, valueB) {
			same[name] = valueA
			continue
		}
		different[name] = map[string]any{"inputA": valueA, "inputB": valueB}
	}
	return same, different
}

// jsonEqual compares two values the way they would be compared once written:
// object keys in any order, and 1 equal to 1.0.
func jsonEqual(a, b any) bool {
	encodedA, errA := json.Marshal(a)
	encodedB, errB := json.Marshal(b)
	return errA == nil && errB == nil && string(encodedA) == string(encodedB)
}

// differenceItem builds one item of the Different output.
func differenceItem(ir workflow.IRNode, indexA int, itemA, itemB workflow.Item, pairs []comparePair, same map[string]any, differing map[string]map[string]any, resolve string, dotNotation bool) workflow.Item {
	var built workflow.Item
	switch resolve {
	case CompareResolveInputA:
		built = cloneItem(itemA)
	case CompareResolveInputB:
		built = cloneItem(itemB)
		built.Paired = nil
	default:
		keys := make(map[string]any, len(pairs))
		for _, pair := range pairs {
			if value, found := compareField(itemA.JSON, pair.a, dotNotation); found {
				keys[pair.a] = cloneValue(value)
			}
		}
		differences := make(map[string]any, len(differing))
		for name, versions := range differing {
			differences[name] = map[string]any{
				"inputA": cloneValue(versions["inputA"]), "inputB": cloneValue(versions["inputB"]),
			}
		}
		built = workflow.Item{JSON: map[string]any{
			"keys": keys, "same": cloneMap(same), "different": differences,
		}}
		if itemA.Binary != nil {
			built.Binary = cloneItem(itemA).Binary
		}
	}
	// A different pair descends from its A item, which the runner cannot infer
	// because the output has fewer items than the input.
	if built.Paired == nil {
		built.Paired = &workflow.PairedItem{SourceNodeID: ir.ID, SourcePort: CompareInputA, ItemIndex: indexA}
		if itemA.Paired != nil && !itemA.Paired.Lost {
			inherited := *itemA.Paired
			built.Paired = &inherited
		}
	}
	return built
}
