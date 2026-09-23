package engine

import (
	"errors"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ItemOutcomes is what a whole-batch node that runs its items one at a time
// returns as its error when some of them failed: every input item's outcome,
// in input order. The JavaScript Code node's "Run once for each item" mode is
// one: its code must see the whole batch ($input.all(), $itemIndex), so the
// runner cannot split it into one-item calls to resolve a tolerated failure
// per item.
//
// A node that tolerates failures (Request.TolerateItemFailures) then
// continues past the failed items, as n8n does inside its item loop: each
// failed item becomes an error item where a per-item node's would, and the
// rest pass on what they produced. A node that does not fails with the first
// failure, which is also what the error's text and Unwrap are.
type ItemOutcomes []ItemOutcome

// ItemOutcome is one input item's result.
type ItemOutcome struct {
	// Items are what the item produced on the node's first output.
	Items []workflow.Item
	// Err is why the item failed, or nil when it succeeded.
	Err error
}

func (outcomes ItemOutcomes) Error() string {
	if first := outcomes.first(); first != nil {
		return first.Error()
	}
	return "no item failed"
}

func (outcomes ItemOutcomes) Unwrap() error { return outcomes.first() }

func (outcomes ItemOutcomes) first() error {
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			return outcome.Err
		}
	}
	return nil
}

func (outcomes ItemOutcomes) failed() int {
	count := 0
	for _, outcome := range outcomes {
		if outcome.Err != nil {
			count++
		}
	}
	return count
}

// toleratedOutcomes finds the per-item outcomes in a tolerated failure. They
// are used only when they account for every input item; otherwise the
// failure is the whole node's.
func toleratedOutcomes(cause error, policy retry, input workflow.NodeInput) (ItemOutcomes, bool) {
	if policy.onError == errorStop {
		return nil, false
	}
	var outcomes ItemOutcomes
	if !errors.As(cause, &outcomes) || len(outcomes) != len(input[mainPortName]) || outcomes.first() == nil {
		return nil, false
	}
	return outcomes, true
}

// assembleOutcomes lays the outcomes out as runPerItem lays out one-item
// calls: in input order, a failed item's error item on the error output under
// continueErrorOutput and in its place on the main output otherwise.
//
// It stamps them as runPerItem does too, one outcome at a time, because what
// an item produced, or the error item it failed with, descends from that item
// alone. Stamping the assembled output as a whole compares the node's whole
// input with each port by position: under continueErrorOutput neither port
// holds as many items as the node was given, so every item lost its lineage,
// and under continueRegularOutput an item that produced two items shifted
// every later one, so an error item paired with the next input item's lineage.
func (state *runState) assembleOutcomes(node workflow.IRNode, sources []workflow.IREdge, input workflow.NodeInput, outcomes ItemOutcomes, mode errorMode) workflow.NodeOutput {
	assembled := make(workflow.NodeOutput, len(node.Definition.Outputs))
	for index := range assembled {
		assembled[index] = []workflow.Item{}
	}
	if len(assembled) == 0 {
		return assembled
	}
	errorPort := errorPortIndex(node)
	items := input[mainPortName]
	for index, outcome := range outcomes {
		// Where this item's outcome starts on each port, so what it adds is
		// stamped against this item alone.
		before := make([]int, len(assembled))
		for port, laid := range assembled {
			before[port] = len(laid)
		}
		if outcome.Err == nil {
			assembled[0] = append(assembled[0], cloneItems(outcome.Items)...)
		} else {
			failed := errorItem(node, items[index], outcome.Err, mode == errorBranch)
			if errorPort >= 0 && mode == errorBranch {
				assembled[errorPort] = append(assembled[errorPort], failed)
			} else {
				assembled[0] = append(assembled[0], failed)
			}
		}
		state.stampPerItem(node, sources, items[index], assembled, before)
	}
	return assembled
}
