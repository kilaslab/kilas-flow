package workflow_test

import (
	"errors"
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-gk7mf5: the n8n import reported nothing for a cycle that run and
// activate refuse. IllegalCycles is the compiler's own cycle rule, asked on its
// own, so an import report can name each cycle without a second, weaker copy
// of the rule that could disagree with the run.

func cycleCatalog() catalog {
	return catalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.loop": {
			Type: "kilasflow.loop", Version: workflow.V(1), LoopEntry: true,
			Inputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{
				{Name: "done", Kind: workflow.ConnectionMain},
				{Name: "loop", Kind: workflow.ConnectionMain},
			},
		},
		"kilasflow.step": {
			Type: "kilasflow.step", Version: workflow.V(1),
			Inputs:  []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
		"kilasflow.configured": {
			Type: "kilasflow.configured", Version: workflow.V(1),
			Inputs:             []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			Outputs:            []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
			RequiredParameters: []string{"url"},
		},
	}
}

func chain(nodes []workflow.Node, pairs ...[2]string) workflow.Document {
	connections := make([]workflow.Connection, 0, len(pairs))
	for index, pair := range pairs {
		source := workflow.Endpoint{NodeID: pair[0], Port: "main"}
		if pair[0] == "loop" {
			source.Port = "loop"
		}
		connections = append(connections, workflow.Connection{
			ID: string(rune('a'+index)) + "-edge", Kind: workflow.ConnectionMain,
			Source: source, Target: workflow.Endpoint{NodeID: pair[1], Port: "main"},
		})
	}
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: "wf_cycle", Name: "Cycle",
		Nodes: nodes, Connections: connections, Settings: map[string]any{},
	}
}

func step(id string) workflow.Node {
	return workflow.Node{ID: id, Name: id, Type: "kilasflow.step", TypeVersion: workflow.V(1)}
}

func refusesACycle(err error) bool {
	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		return false
	}
	for _, issue := range validationErrors.Issues {
		if issue.Code == workflow.ErrorInvalidTopology && issue.Path == "/connections" {
			return true
		}
	}
	return false
}

func TestIllegalCyclesNameTheCyclesCompileRefuses(t *testing.T) {
	manual := workflow.Node{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}
	loop := workflow.Node{ID: "loop", Name: "Loop", Type: "kilasflow.loop", TypeVersion: workflow.V(1)}

	for _, tc := range []struct {
		name     string
		document workflow.Document
		want     [][]string
	}{
		{
			// The shape of template 2536: a branch that routes back to the
			// node that decided it, through ordinary nodes.
			name: "a cycle through ordinary nodes",
			document: chain([]workflow.Node{manual, step("if"), step("wait"), step("code"), step("respond")},
				[2]string{"manual", "if"}, [2]string{"if", "wait"}, [2]string{"wait", "code"},
				[2]string{"code", "respond"}, [2]string{"respond", "if"}),
			want: [][]string{{"if", "wait", "code", "respond"}},
		},
		{
			name: "a back edge closing onto a loop",
			document: chain([]workflow.Node{manual, loop, step("body")},
				[2]string{"manual", "loop"}, [2]string{"loop", "body"}, [2]string{"body", "loop"}),
			want: nil,
		},
		{
			// Allowed onto the loop, and still refused between two nodes of its
			// body: the exception is for the edge that closes the loop, not for
			// everything inside it.
			name: "a cycle inside a loop's body",
			document: chain([]workflow.Node{manual, loop, step("a"), step("b")},
				[2]string{"manual", "loop"}, [2]string{"loop", "a"}, [2]string{"a", "b"},
				[2]string{"b", "a"}, [2]string{"b", "loop"}),
			want: [][]string{{"a", "b"}},
		},
		{
			// Template 3655 polls three services, each in a loop of its own.
			// Naming only the first would leave the author to find the others
			// one failed activation at a time.
			name: "two separate cycles",
			document: chain([]workflow.Node{manual, step("a"), step("b"), step("c"), step("d")},
				[2]string{"manual", "a"}, [2]string{"a", "b"}, [2]string{"b", "a"},
				[2]string{"b", "c"}, [2]string{"c", "d"}, [2]string{"d", "c"}),
			want: [][]string{{"a", "b"}, {"c", "d"}},
		},
		{
			name: "no cycle",
			document: chain([]workflow.Node{manual, step("a"), step("b")},
				[2]string{"manual", "a"}, [2]string{"a", "b"}),
			want: nil,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := workflow.IllegalCycles(tc.document, cycleCatalog())
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("IllegalCycles() = %v, want %v", got, tc.want)
			}
			// The whole point: the rule asked on its own agrees with Compile.
			_, err := workflow.Compile(tc.document, cycleCatalog())
			if refused := refusesACycle(err); refused != (len(got) > 0) {
				t.Errorf("Compile() refused a cycle = %t (error %v), but IllegalCycles() = %v", refused, err, got)
			}
		})
	}
}

// Compile checks the cycle only once nothing else is wrong, so a workflow with
// a missing parameter and a cycle is refused for the parameter first — and
// then for the cycle once the parameter is filled in. Asked on its own, the
// rule names the cycle straight away.
func TestIllegalCyclesAreNotHiddenByAnotherProblem(t *testing.T) {
	manual := workflow.Node{ID: "manual", Name: "Manual", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}
	fetch := workflow.Node{ID: "fetch", Name: "Fetch", Type: "kilasflow.configured", TypeVersion: workflow.V(1)}
	document := chain([]workflow.Node{manual, fetch, step("b")},
		[2]string{"manual", "fetch"}, [2]string{"fetch", "b"}, [2]string{"b", "fetch"})

	if got := workflow.IllegalCycles(document, cycleCatalog()); !reflect.DeepEqual(got, [][]string{{"fetch", "b"}}) {
		t.Errorf("IllegalCycles() = %v, want [[fetch b]]", got)
	}
	document.Nodes[1].Parameters = map[string]any{"url": "https://example.com"}
	if _, err := workflow.Compile(document, cycleCatalog()); !refusesACycle(err) {
		t.Errorf("Compile() error = %v, want the cycle refused once the parameter is set", err)
	}
}
