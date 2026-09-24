package nodes

import (
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// jsRootsOf builds a JavaScript Code node's roots from the request an
// expression reads, so `$('X').item` in code and in a `{{ }}` field of the
// same node pair with the same item, by the same rules.
//
// Other nodes are handed over through functions rather than copied, so a
// body that never names another node never pays to serialise it.
func jsRootsOf(ir workflow.IRNode, input workflow.NodeInput, request engine.Request) jsrun.Roots {
	items := input["main"]
	return jsrun.Roots{
		Workflow: jsrun.WorkflowInfo{ID: request.Workflow.ID, Name: request.Workflow.Name, Active: request.Workflow.Active},
		Execution: jsrun.ExecutionInfo{
			ID: request.Execution.ID, Mode: request.Execution.Mode,
			ResumeURL: request.Execution.ResumeURL, ApprovalURL: request.Execution.ApprovalURL,
		},
		Env:         request.Env,
		RunIndex:    request.RunIndex,
		NodeVersion: ir.TypeVersion.Float(),
		// The workflow's settings.timezone, which $now and $today read in an
		// expression too; Luxon and the Intl date formatting default to it.
		Timezone: request.Workflow.Timezone,
		Node: func(name string) (jsrun.NodeView, bool) {
			node, ok := request.NodeItems[name]
			if !ok {
				return jsrun.NodeView{}, false
			}
			return jsrun.NodeView{Items: node.Items, Params: node.Parameters, Outputs: node.OutputLengths, RunIndex: node.RunIndex}, true
		},
		Pair: func(name string, index int) (int, string) {
			node, ok := request.NodeItems[name]
			if !ok {
				return -1, "node \"" + name + "\" has not run in this execution"
			}
			var current workflow.Item
			if index >= 0 && index < len(items) {
				current = items[index]
			}
			return engine.PairedIndex(name, node, current, index)
		},
	}
}
