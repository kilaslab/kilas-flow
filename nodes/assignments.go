package nodes

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/conditions"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/property"
)

// assignmentRow is one `{name, type, value}` row as a document carries it.
type assignmentRow struct {
	ID    string
	Name  string
	Type  property.AssignmentType
	Value any
}

// readAssignments reads the Set node's parameter in either shape.
//
// Two shapes exist and both have to work. The ordered one is n8n's — a
// `{"assignments": [{id, name, type, value}]}` object — and it is what every
// imported workflow and every newly saved node carries. The flat one is a plain
// `{"field": value}` map, which is what KilasFlow stored before assignments had
// order or types, and a workflow saved then must still run unchanged.
//
// The two are told apart structurally rather than by a version flag: the
// ordered shape has an `assignments` key holding a list of objects that carry a
// `name`. A legacy map with a field genuinely called `assignments` holding a
// list of named objects would be read as the ordered shape — vanishingly
// unlikely, and the alternative is a flag that every hand-written document has
// to remember to set.
func readAssignments(value any) ([]assignmentRow, error) {
	object, ok := value.(map[string]any)
	if !ok || len(object) == 0 {
		return nil, fmt.Errorf("assignments must be a non-empty object")
	}
	if rows, ordered := orderedAssignments(object); ordered {
		return rows, nil
	}

	// The legacy shape. Its order is whatever the map iteration gives, which is
	// exactly the problem the ordered shape exists to fix — so it is sorted
	// here, to make a run of an old document at least deterministic.
	names := make([]string, 0, len(object))
	for name := range object {
		names = append(names, name)
	}
	sortStringsInPlace(names)
	rows := make([]assignmentRow, 0, len(names))
	for _, name := range names {
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("assignment keys must not be empty")
		}
		// No declared type: the value is written as it is, which is what the
		// old executor did.
		rows = append(rows, assignmentRow{Name: name, Value: object[name]})
	}
	return rows, nil
}

// orderedAssignments reads the `{assignments: [...]}` shape, or reports that
// this is not one.
func orderedAssignments(object map[string]any) ([]assignmentRow, bool) {
	list, present := object["assignments"].([]any)
	if !present {
		return nil, false
	}
	rows := make([]assignmentRow, 0, len(list))
	for _, entry := range list {
		declared, ok := entry.(map[string]any)
		if !ok {
			return nil, false
		}
		name, named := declared["name"].(string)
		if !named {
			return nil, false
		}
		typeName, _ := declared["type"].(string)
		identifier, _ := declared["id"].(string)
		rows = append(rows, assignmentRow{
			ID: identifier, Name: name,
			Type:  property.AssignmentType(typeName),
			Value: declared["value"],
		})
	}
	return rows, true
}

// coerce reads a row's value as its declared type.
//
// The type is what makes an assignment collection worth having: without it a
// number typed into a form is a string, and a workflow comparing it downstream
// takes the wrong branch. An undeclared type — the legacy shape — writes the
// value through untouched, which is what that shape has always done.
//
// The table is n8n's own (`validateFieldType`, which a Set node has used since
// 3.2): a value that is undefined or null is written as null whatever the
// declared type, an empty string is zero for a number, and only a value that
// genuinely cannot be converted is an error — and then only when the node was
// not asked to ignore conversion errors.
func (row assignmentRow) coerce() (any, error) {
	if row.Type == "" {
		return row.Value, nil
	}
	if row.Value == nil || expression.IsUndefined(row.Value) {
		// n8n writes null here for every type. A missing field and an empty
		// one have to stay distinguishable downstream, and "" for a missing
		// value was how they stopped being.
		return nil, nil
	}
	switch row.Type {
	case property.AssignmentString:
		if text, ok := row.Value.(string); ok {
			return text, nil
		}
		encoded, err := json.Marshal(row.Value)
		if err != nil {
			return nil, fmt.Errorf("assignment %q cannot be read as text", row.Name)
		}
		return string(encoded), nil
	case property.AssignmentNumber:
		if number, ok := conditions.Number(row.Value); ok {
			return number, nil
		}
		if text, isText := row.Value.(string); isText {
			return nil, fmt.Errorf("assignment %q is declared a number and %q is not one", row.Name, text)
		}
		return nil, fmt.Errorf("assignment %q is declared a number and its value is not one", row.Name)
	case property.AssignmentBoolean:
		if decided, ok := conditions.Boolean(row.Value); ok {
			return decided, nil
		}
		if text, isText := row.Value.(string); isText {
			return nil, fmt.Errorf("assignment %q is declared a boolean and %q is not one", row.Name, text)
		}
		return nil, fmt.Errorf("assignment %q is declared a boolean and its value is not one", row.Name)
	case property.AssignmentArray:
		if list, ok := row.Value.([]any); ok {
			return list, nil
		}
		return decodeAssignmentJSON[[]any](row, "an array")
	case property.AssignmentObject:
		if object, ok := row.Value.(map[string]any); ok {
			return object, nil
		}
		return decodeAssignmentJSON[map[string]any](row, "an object")
	default:
		return nil, fmt.Errorf("assignment %q declares type %q, which is not one of %v",
			row.Name, row.Type, property.AssignmentTypes())
	}
}

// decodeAssignmentJSON reads a structured value that arrived as text.
//
// An expression that produced JSON, or a user who typed it into the editor,
// both land here; anything else is a mistake worth naming rather than a value
// to guess at.
func decodeAssignmentJSON[T any](row assignmentRow, described string) (any, error) {
	text, ok := row.Value.(string)
	if !ok {
		return nil, fmt.Errorf("assignment %q is declared %s and its value is not", row.Name, described)
	}
	var decoded T
	if err := json.Unmarshal([]byte(strings.TrimSpace(text)), &decoded); err != nil {
		return nil, fmt.Errorf("assignment %q is declared %s and %q is not valid JSON", row.Name, described, text)
	}
	return decoded, nil
}

func sortStringsInPlace(values []string) {
	for i := 1; i < len(values); i++ {
		for j := i; j > 0 && values[j] < values[j-1]; j-- {
			values[j], values[j-1] = values[j-1], values[j]
		}
	}
}
