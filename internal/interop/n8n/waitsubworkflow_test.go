package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// TestWaitDefaultsFollowTheNodeVersion covers the defaults n8n omits.
//
// n8n does not write a parameter that still holds its default, and the two
// versions of the Wait node disagree about both of them: v1 waits an hour, v1.1
// waits five seconds. Reading the absence through one hard-coded pair turned
// "wait three seconds" into three hours and an omitted amount into no pause at
// all — a rate-limit pause that silently disappeared.
func TestWaitDefaultsFollowTheNodeVersion(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct {
		fixture        string
		amount, unit   any
		amountIsMarker bool
	}{
		"v1.1 with no amount waits n8n's five seconds": {
			fixture: `{"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
				"parameters":{"resume":"timeInterval"}}`,
			amount: float64(5), unit: "seconds",
		},
		"v1 with no amount waits n8n's one hour": {
			fixture: `{"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1,"position":[220,0],
				"parameters":{"resume":"timeInterval"}}`,
			amount: float64(1), unit: "hours",
		},
		"v1.1 with only an amount keeps n8n's seconds": {
			fixture: `{"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
				"parameters":{"resume":"timeInterval","amount":3}}`,
			amount: float64(3), unit: "seconds",
		},
		"an expression amount stays an expression": {
			fixture: `{"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
				"parameters":{"resume":"timeInterval","amount":"={{ $json.w }}"}}`,
			amountIsMarker: true, unit: "seconds",
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			result := importFixture(t, waitFixture(test.fixture))
			wait := nodeByName(result.Document, "Wait")
			if test.amountIsMarker {
				marker, _ := wait.Parameters["amount"].(map[string]any)
				if marker["value"] != "{{ $json.w }}" {
					t.Fatalf("amount = %#v, want the expression carried rather than left as raw text",
						wait.Parameters["amount"])
				}
			} else if wait.Parameters["amount"] != test.amount {
				t.Errorf("amount = %#v, want %#v", wait.Parameters["amount"], test.amount)
			}
			if wait.Parameters["unit"] != test.unit {
				t.Errorf("unit = %#v, want %#v", wait.Parameters["unit"], test.unit)
			}
		})
	}

	// A unit this server does not know would run a pause of the wrong length,
	// so it is refused rather than guessed at.
	result := importFixture(t, waitFixture(`{"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1.1,
		"position":[220,0],"parameters":{"resume":"timeInterval","amount":3,"unit":"fortnights"}}`))
	blocking := false
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking && issue.Field == "unit" {
			blocking = true
		}
	}
	if !blocking {
		t.Fatalf("unsupported = %#v, want a blocking issue about the unknown unit", result.Unsupported)
	}
}

// TestWaitLimitOptionsAreCarried covers n8n's own bound on how long a wait may
// last, which the executor reads under these names.
func TestWaitLimitOptionsAreCarried(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Bounded wait",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Wait","type":"n8n-nodes-base.wait","typeVersion":1.1,"position":[220,0],
	     "parameters":{"resume":"webhook","options":{"limitWaitTime":true,"limitType":"afterTimeInterval",
	       "limitAmount":2,"limitUnit":"hours"}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Wait","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	wait := nodeByName(result.Document, "Wait")
	if wait.Parameters["resume"] != "webhook" {
		t.Errorf("resume = %#v, want the durable mode carried", wait.Parameters["resume"])
	}
	if wait.Parameters["limitWaitTime"] != true || wait.Parameters["limitAmount"] != float64(2) ||
		wait.Parameters["limitUnit"] != "hours" || wait.Parameters["limitType"] != "afterTimeInterval" {
		t.Errorf("limit parameters = %#v, want n8n's bound carried under the executor's names", wait.Parameters)
	}

	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Wait" {
			continue
		}
		options, _ := node.Parameters["options"].(map[string]any)
		if options["limitWaitTime"] != true || options["limitAmount"] != float64(2) {
			t.Fatalf("exported options = %#v, want the bound written back where n8n keeps it", node.Parameters)
		}
	}
}

func waitFixture(node string) string {
	return `{
	  "name": "Waits",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    ` + node + `
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Wait","type":"main","index":0}]]}}
	}`
}

// TestSplitInBatchesV1WiresTheBodyToTheLoopPort covers the version-dependent
// port semantics.
//
// v3 split one batch output into `done` (0) and `loop` (1); v1 and v2 have a
// single output that emits each batch. Resolving positionally put the loop body
// on `done`, so the body ran once at the end — when the compiler accepted the
// graph at all, which usually it did not, and the error named scheduling rather
// than the loop.
func TestSplitInBatchesV1WiresTheBodyToTheLoopPort(t *testing.T) {
	t.Parallel()

	for _, version := range []string{"1", "2"} {
		result := importFixture(t, splitInBatchesFixture(version))
		port := ""
		for _, connection := range result.Document.Connections {
			if connection.Target.NodeID == "p" {
				port = connection.Source.Port
			}
		}
		if port != "loop" {
			t.Errorf("typeVersion %s: body port = %q, want loop", version, port)
		}
		blocking := false
		for _, issue := range result.Unsupported {
			if issue.Severity == n8n.SeverityBlocking && strings.Contains(issue.Reason, "noItemsLeft") {
				blocking = true
			}
		}
		if !blocking {
			t.Errorf("typeVersion %s: unsupported = %#v, want a blocking issue naming the exit that "+
				"needs rewiring", version, result.Unsupported)
		}
	}

	// v3 is the shape this server's loop already is: done is 0, loop is 1, and
	// nothing is reported.
	result := importFixture(t, splitInBatchesFixture("3"))
	ports := map[string]string{}
	for _, connection := range result.Document.Connections {
		ports[connection.Target.NodeID] = connection.Source.Port
	}
	// v3's slot 0 is `done` and slot 1 is `loop`, which is the order this
	// server's loop declares.
	if ports["p"] != "done" || ports["q"] != "loop" {
		t.Errorf("v3 ports = %#v, want p on done and q on loop", ports)
	}
	for _, issue := range result.Unsupported {
		if issue.Severity == n8n.SeverityBlocking {
			t.Fatalf("v3 reported a blocking issue: %+v", issue)
		}
	}
}

// TestSplitInBatchesCarriesTheLoopBound covers the iteration ceiling: n8n's
// loop has none, and an imported loop that would legitimately run a few hundred
// batches must not inherit a lower default and die halfway.
func TestSplitInBatchesCarriesTheLoopBound(t *testing.T) {
	t.Parallel()

	result := importFixture(t, splitInBatchesFixture("3"))
	loop := nodeByName(result.Document, "Split In Batches")
	if loop.Parameters["maxIterations"] != float64(workflow.MaxLoopIterations) {
		t.Errorf("maxIterations = %#v, want the instance ceiling %d",
			loop.Parameters["maxIterations"], workflow.MaxLoopIterations)
	}
	if loop.Parameters["batchSize"] != float64(2) {
		t.Errorf("batchSize = %#v, want it carried", loop.Parameters["batchSize"])
	}
}

func splitInBatchesFixture(version string) string {
	return `{
	  "name": "Batched",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"l","name":"Split In Batches","type":"n8n-nodes-base.splitInBatches","typeVersion":` + version + `,
	     "position":[220,0],"parameters":{"batchSize":2}},
	    {"id":"p","name":"Process","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[440,0],
	     "parameters":{"assignments":{"assignments":[{"id":"x","name":"seen","type":"boolean","value":true}]}}},
	    {"id":"q","name":"Finished","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[660,0],"parameters":{}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Split In Batches","type":"main","index":0}]]},
	    "Split In Batches": {"main": [
	      [{"node":"Process","type":"main","index":0}],
	      [{"node":"Finished","type":"main","index":0}]
	    ]},
	    "Process": {"main": [[{"node":"Split In Batches","type":"main","index":0}]]}
	  }
	}`
}

// TestSplitInBatchesKeepsAndNamesItsResetOption is the other half of the loop
// contract: the value n8n restarts a running loop with is kept so a round trip
// returns the node as authored, and the diagnostic says what does not happen
// rather than claiming the value was dropped.
func TestSplitInBatchesKeepsAndNamesItsResetOption(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Batched",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"l","name":"Split In Batches","type":"n8n-nodes-base.splitInBatches","typeVersion":3,
	     "position":[220,0],"parameters":{"batchSize":2,"options":{"reset":"={{ $runIndex > 0 }}"}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Split In Batches","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	loop := nodeByName(result.Document, "Split In Batches")
	reset, _ := loop.Parameters["reset"].(map[string]any)
	if reset["value"] != "{{ $runIndex > 0 }}" || reset["mode"] != "expression" {
		t.Errorf("reset = %#v, want the expression kept (without n8n's marker)", loop.Parameters["reset"])
	}

	reported := false
	for _, issue := range result.Unsupported {
		if issue.Field != "options.reset" {
			continue
		}
		reported = true
		if issue.Severity != n8n.SeverityLossy {
			t.Errorf("options.reset severity = %q, want %q: the value is carried, what it does is not",
				issue.Severity, n8n.SeverityLossy)
		}
		if strings.Contains(issue.Reason, "was not carried") {
			t.Errorf("options.reset reason = %q, want it to say the value is kept and only its effect is missing",
				issue.Reason)
		}
	}
	if !reported {
		t.Error("options.reset was carried without a diagnostic; the restart semantics are not implemented")
	}
}

// TestExecuteWorkflowInputMappingShapesTheSubWorkflowInput covers the caller's
// "define using fields below" mapper.
//
// It was never read, so every modern sub-workflow call sent its raw items and a
// sub-workflow that routes on a mapped field had nothing to route on.
func TestExecuteWorkflowInputMappingShapesTheSubWorkflowInput(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Caller",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Call","type":"n8n-nodes-base.executeWorkflow","typeVersion":1.2,"position":[220,0],
	     "parameters":{"workflowId":{"mode":"id","value":"wf_target"},
	       "workflowInputs":{"mappingMode":"defineBelow","value":{"id":"={{ $json.id }}",
	         "label":"=L-{{ $json.name }}"}}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Call","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	call := nodeByName(result.Document, "Call")
	fields, _ := call.Parameters["inputFields"].(map[string]any)
	if len(fields) != 2 {
		t.Fatalf("inputFields = %#v, want the declared mapping carried", call.Parameters["inputFields"])
	}
	id, _ := fields["id"].(map[string]any)
	if id["value"] != "{{ $json.id }}" {
		t.Errorf("id mapping = %#v, want the expression carried", fields["id"])
	}
	label, _ := fields["label"].(map[string]any)
	if label["value"] != "L-{{ $json.name }}" {
		t.Errorf("label mapping = %#v, want the template carried", fields["label"])
	}

	// The mapping goes back to n8n in the shape it was read from.
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	for _, node := range exported.Document.Nodes {
		if node.Name != "Call" {
			continue
		}
		mapper, _ := node.Parameters["workflowInputs"].(map[string]any)
		if mapper["mappingMode"] != "defineBelow" {
			t.Fatalf("exported workflowInputs = %#v, want the mapper written back", node.Parameters["workflowInputs"])
		}
		value, _ := mapper["value"].(map[string]any)
		if value["id"] != "={{ $json.id }}" {
			t.Errorf("exported id = %#v, want the `=` prefix restored", value["id"])
		}
	}
}

// TestExecuteWorkflowTriggerDeclaredInputsSurvive covers n8n 1.1's default
// input source: it is workflowInputs, exports omit it, and reading only an
// explicit value imported every such trigger as passthrough with its
// declarations discarded.
func TestExecuteWorkflowTriggerDeclaredInputsSurvive(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Sub",
	  "nodes": [
	    {"id":"a","name":"When Executed","type":"n8n-nodes-base.executeWorkflowTrigger","typeVersion":1.1,
	     "position":[0,0],"parameters":{"workflowInputs":{"values":[
	       {"name":"requestId","type":"number"},{"name":"label","type":"string"}]}}},
	    {"id":"b","name":"Work","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[220,0],"parameters":{}}
	  ],
	  "connections": {"When Executed": {"main": [[{"node":"Work","type":"main","index":0}]]}}
	}`

	result := importFixture(t, fixture)
	trigger := nodeByName(result.Document, "When Executed")
	if trigger.Parameters["inputSource"] != "fields" {
		t.Errorf("inputSource = %#v, want fields for a trigger that declares them",
			trigger.Parameters["inputSource"])
	}
	declared, _ := trigger.Parameters["workflowInputs"].(map[string]any)
	values, _ := declared["values"].([]any)
	if len(values) != 2 {
		t.Fatalf("workflowInputs = %#v, want the declared fields kept", trigger.Parameters["workflowInputs"])
	}

	// A trigger with nothing declared is still passthrough: claiming `fields`
	// for it would describe a contract nobody wrote.
	plain := importFixture(t, `{
	  "name": "Plain",
	  "nodes": [
	    {"id":"a","name":"When Executed","type":"n8n-nodes-base.executeWorkflowTrigger","typeVersion":1.1,
	     "position":[0,0],"parameters":{}}
	  ],
	  "connections": {}
	}`)
	if nodeByName(plain.Document, "When Executed").Parameters["inputSource"] != "passthrough" {
		t.Errorf("inputSource = %#v, want passthrough for an undeclared trigger",
			nodeByName(plain.Document, "When Executed").Parameters["inputSource"])
	}
}

// TestNodeNamesAreKeptVerbatim covers names n8n allows and this adapter used to
// trim.
//
// n8n keys connections by the name as written, so trimming here and looking the
// raw name up dropped every edge of a node called "Get ScreenShot ".
func TestNodeNamesAreKeptVerbatim(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Spaces",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Get ScreenShot ","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[220,0],"parameters":{}},
	    {"id":"c","name":"After","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[440,0],"parameters":{}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node":"Get ScreenShot ","type":"main","index":0}]]},
	    "Get ScreenShot ": {"main": [[{"node":"After","type":"main","index":0}]]}
	  }
	}`

	result := importFixture(t, fixture)
	if len(result.Document.Connections) != 2 {
		t.Fatalf("connections = %#v, want both edges kept", result.Document.Connections)
	}
	if got := nodeByName(result.Document, "Get ScreenShot ").Name; got != "Get ScreenShot " {
		t.Errorf("name = %q, want the trailing space kept", got)
	}
	for _, issue := range result.Unsupported {
		if strings.Contains(issue.Reason, "is not a node in this workflow") {
			t.Fatalf("an edge was dropped for a name n8n matched exactly: %+v", issue)
		}
	}
}

// TestMergeInputsPastTheSecondKeepTheirSlot covers the export side of a Merge
// with three or more inputs.
//
// The static table only knew input1 and input2, so every edge into a Merge's
// third and later inputs was written to n8n slot 0 — a nine-input Merge
// exported with one input carrying all nine wires.
func TestMergeInputsPastTheSecondKeepTheirSlot(t *testing.T) {
	t.Parallel()

	const fixture = `{
	  "name": "Three-way merge",
	  "nodes": [
	    {"id":"a","name":"A","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"B","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[0,120],"parameters":{}},
	    {"id":"c","name":"C","type":"n8n-nodes-base.noOp","typeVersion":1,"position":[0,240],"parameters":{}},
	    {"id":"m","name":"Merge","type":"n8n-nodes-base.merge","typeVersion":3,"position":[220,0],
	     "parameters":{"mode":"append","numberInputs":3}}
	  ],
	  "connections": {
	    "A": {"main": [[{"node":"Merge","type":"main","index":0}]]},
	    "B": {"main": [[{"node":"Merge","type":"main","index":1}]]},
	    "C": {"main": [[{"node":"Merge","type":"main","index":2}]]}
	  }
	}`

	imported := importFixture(t, fixture)
	ports := map[string]string{}
	for _, connection := range imported.Document.Connections {
		ports[connection.Source.NodeID] = connection.Target.Port
	}
	if ports["a"] != "input1" || ports["b"] != "input2" || ports["c"] != "input3" {
		t.Fatalf("imported ports = %#v, want input1/input2/input3", ports)
	}

	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatal(err)
	}
	// The Merge's input is the *target* index of each edge; the source slot is
	// the upstream node's own single output.
	indexes := map[string]int{}
	for source, kinds := range exported.Document.Connections {
		for _, slots := range kinds {
			for _, targets := range slots {
				for _, target := range targets {
					indexes[source] = target.Index
				}
			}
		}
	}
	for name, want := range map[string]int{"A": 0, "B": 1, "C": 2} {
		if indexes[name] != want {
			t.Errorf("exported %s input slot = %d, want %d", name, indexes[name], want)
		}
	}
}
