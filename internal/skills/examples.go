package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"path"
	"sort"
	"strings"
)

// The example kinds a skill can carry. A skill teaches a document shape, and a
// shape that is only described is a shape an agent gets wrong once before it
// gets it right (BUG-x28fsx, CLI-5), so a skill carries fenced JSON that the
// product itself accepts:
//
//	```json kilasflow-workflow
//	{ a canonical workflow document }
//	```
//
//	```json kilasflow-request create-credential
//	{ a request body for that operation id }
//	```
//
// The first is checked by compiling it through `workflow validate`, the second
// by sending it to the operation, both in `go test`. A fence whose info string
// carries neither marker is ordinary prose and is never read.
const (
	ExampleWorkflow = "kilasflow-workflow"
	ExampleRequest  = "kilasflow-request"
)

// Example is one validated JSON block a skill carries.
type Example struct {
	// Path is repository-relative: skills/<name>/SKILL.md or
	// skills/<name>/references/<file>.md.
	Path string
	// Kind is ExampleWorkflow or ExampleRequest.
	Kind string
	// Operation is the operation id of a request example, and empty otherwise.
	Operation string
	// Line is the 1-based line of the opening fence, so a failure names it.
	Line int
	// JSON is the block's text.
	JSON string
}

// Examples lists the examples one skill carries, in document order: the body
// first, then each reference file in name order.
//
// A fence that claims to be an example and is malformed is an error rather
// than an example that is silently skipped, because a skipped example is a
// shape nobody validates.
func Examples(skill Skill) ([]Example, error) {
	documents := []struct {
		name string
		text string
	}{{name: skill.Path, text: skill.Body}}

	references := append([]string(nil), skill.References...)
	sort.Strings(references)
	for _, reference := range references {
		documents = append(documents, struct {
			name string
			text string
		}{name: path.Join(path.Dir(skill.Path), reference), text: skill.ReferenceText[reference]})
	}

	var found []Example
	for _, document := range documents {
		examples, err := examplesIn(document.name, document.text)
		if err != nil {
			return nil, err
		}
		found = append(found, examples...)
	}

	return found, nil
}

// examplesIn reads one document's fenced blocks.
//
// Every fence is walked to its close, claimed or not, so the closing fence of a
// block this reader does not own is never read as the opening one of the next.
func examplesIn(name, text string) ([]Example, error) {
	var found []Example

	lines := strings.Split(text, "\n")
	for index := 0; index < len(lines); index++ {
		trimmed := strings.TrimSpace(lines[index])
		if !strings.HasPrefix(trimmed, "```") {
			continue
		}

		example, claims, err := exampleOpening(name, index+1, strings.TrimSpace(strings.TrimPrefix(trimmed, "```")))
		if err != nil {
			return nil, err
		}

		closing := -1
		for next := index + 1; next < len(lines); next++ {
			if strings.HasPrefix(strings.TrimSpace(lines[next]), "```") {
				closing = next

				break
			}
		}
		if closing < 0 {
			if claims {
				return nil, fmt.Errorf("%s:%d: the example fence is never closed", name, index+1)
			}

			break
		}

		if claims {
			example.JSON = strings.Join(lines[index+1:closing], "\n")
			found = append(found, example)
		}
		index = closing
	}

	return found, nil
}

// exampleOpening reads a fence's info string. claims is false for a fence that
// is not an example at all; an error is a fence that says it is one and gets it
// wrong.
func exampleOpening(name string, line int, info string) (Example, bool, error) {
	words := strings.Fields(info)
	if len(words) < 2 || words[0] != "json" || !strings.HasPrefix(words[1], "kilasflow-") {
		return Example{}, false, nil
	}

	example := Example{Path: name, Line: line, Kind: words[1]}
	switch example.Kind {
	case ExampleWorkflow:
		if len(words) != 2 {
			return Example{}, false, fmt.Errorf("%s:%d: a %s fence takes no operation id, got %q", name, line, ExampleWorkflow, info)
		}
	case ExampleRequest:
		if len(words) != 3 {
			return Example{}, false, fmt.Errorf("%s:%d: a %s fence names exactly one operation id: ```json %s <operation-id>, got %q", name, line, ExampleRequest, ExampleRequest, info)
		}
		example.Operation = words[2]
	default:
		return Example{}, false, fmt.Errorf("%s:%d: %q is not an example kind; the kinds are %s and %s", name, line, example.Kind, ExampleWorkflow, ExampleRequest)
	}

	return example, true, nil
}

// CheckExampleShape refuses an example that is not the shape its kind promises,
// before anything is sent to a server.
//
// It is the cheap half of the gate: it catches a document written from memory
// (a missing position, a connection without a kind, an `id` the server owns)
// with a message naming the field, while the product's own compiler, driven by
// the example harness, says whether the graph is one that runs.
func CheckExampleShape(example Example) error {
	where := fmt.Sprintf("%s:%d", example.Path, example.Line)

	switch example.Kind {
	case ExampleWorkflow:
		return checkWorkflowShape(where, example.JSON)
	case ExampleRequest:
		if strings.TrimSpace(example.Operation) == "" {
			return fmt.Errorf("%s: a request example names no operation id", where)
		}
		if _, err := decodeObject(example.JSON); err != nil {
			return fmt.Errorf("%s: the request body for %s: %w", where, example.Operation, err)
		}

		return nil
	default:
		return fmt.Errorf("%s: %q is not an example kind", where, example.Kind)
	}
}

// documentKeys are the top-level members of a workflow document a write sends.
var documentKeys = []string{"schemaVersion", "name", "nodes", "connections", "settings"}

func checkWorkflowShape(where, text string) error {
	document, err := decodeObject(text)
	if err != nil {
		return fmt.Errorf("%s: %w", where, err)
	}

	for _, key := range documentKeys {
		if _, found := document[key]; !found {
			return fmt.Errorf("%s: the document has no top-level %q", where, key)
		}
	}
	// The server owns the workflow id: the write schema never exposes it, and a
	// client that sends a document read back from `get` drops it first.
	if _, found := document["id"]; found {
		return fmt.Errorf("%s: the document carries a top-level \"id\", which the server assigns; leave it out", where)
	}

	nodes, err := objectList(document["nodes"])
	if err != nil {
		return fmt.Errorf("%s: nodes: %w", where, err)
	}
	if len(nodes) < 2 {
		return fmt.Errorf("%s: the example has %d nodes; a connected example needs at least 2", where, len(nodes))
	}
	for index, node := range nodes {
		for _, key := range []string{"id", "name", "type", "typeVersion", "position"} {
			if _, found := node[key]; !found {
				return fmt.Errorf("%s: nodes[%d] has no %q", where, index, key)
			}
		}
		position, ok := node["position"].(map[string]any)
		if !ok {
			return fmt.Errorf("%s: nodes[%d].position must be an object {\"x\": n, \"y\": n}, got %T", where, index, node["position"])
		}
		for _, axis := range []string{"x", "y"} {
			if _, ok := position[axis].(float64); !ok {
				return fmt.Errorf("%s: nodes[%d].position must carry a numeric %q", where, index, axis)
			}
		}
	}

	connections, err := objectList(document["connections"])
	if err != nil {
		return fmt.Errorf("%s: connections: %w", where, err)
	}
	if len(connections) < 1 {
		return fmt.Errorf("%s: the example has no connection; a connected example needs at least 1", where)
	}
	for index, connection := range connections {
		for _, key := range []string{"id", "kind"} {
			if _, found := connection[key]; !found {
				return fmt.Errorf("%s: connections[%d] has no %q", where, index, key)
			}
		}
		for _, end := range []string{"source", "target"} {
			endpoint, ok := connection[end].(map[string]any)
			if !ok {
				return fmt.Errorf("%s: connections[%d].%s must be an object {\"nodeId\": …, \"port\": …}", where, index, end)
			}
			for _, key := range []string{"nodeId", "port"} {
				if value, ok := endpoint[key].(string); !ok || value == "" {
					return fmt.Errorf("%s: connections[%d].%s has no %q", where, index, end, key)
				}
			}
		}
	}

	return nil
}

// decodeObject reads one JSON object and nothing after it.
func decodeObject(text string) (map[string]any, error) {
	decoder := json.NewDecoder(bytes.NewReader([]byte(text)))
	var object map[string]any
	if err := decoder.Decode(&object); err != nil {
		return nil, fmt.Errorf("not a JSON object: %w", err)
	}
	if object == nil {
		return nil, fmt.Errorf("not a JSON object: got null")
	}
	if decoder.More() {
		return nil, fmt.Errorf("more than one JSON value in the block")
	}

	return object, nil
}

// objectList reads a JSON array whose members are all objects.
func objectList(value any) ([]map[string]any, error) {
	items, ok := value.([]any)
	if !ok {
		return nil, fmt.Errorf("must be an array, got %T", value)
	}

	objects := make([]map[string]any, 0, len(items))
	for index, item := range items {
		object, ok := item.(map[string]any)
		if !ok {
			return nil, fmt.Errorf("[%d] must be an object, got %T", index, item)
		}
		objects = append(objects, object)
	}

	return objects, nil
}
