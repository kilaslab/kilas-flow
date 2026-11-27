// Package triggerfilter decides, before anything is queued, whether a trigger
// item is one its workflow wants to run for.
//
// A workflow listening to a high-volume source — every inbound message a host
// receives, every delivery to a busy webhook — usually cares about a slice of
// it: one channel, direct messages only, a keyword. Filtering inside the
// workflow works, but every event then becomes an execution: a queue slot, a
// worker claim, a history row and a run against the tenant's quota, all to
// discard the item at the first node. A trigger filter answers the same
// question at the boundary instead, so an event the workflow would discard
// never becomes an execution at all.
//
// # The shape is the IF node's
//
// The parameter is the same conditions value IF, Filter and Switch carry —
// n8n's rich filter shape — evaluated by the same evaluator, with its left and
// right values resolved as expressions against the trigger item. So
// `{{ $json.body.channel }}` in a webhook trigger's filter reads exactly what
// the same expression reads in the first node after the trigger.
//
// # A filter that cannot be evaluated filters out
//
// A condition that fails to evaluate — a strict type check refusing a value,
// an expression naming a field the item does not have under strict
// validation — keeps the event out, with the error as the reason. The
// parameter reads "only run when", and an event nobody could show matches is
// not one the author asked to run for. The reason is returned rather than
// swallowed so the boundary can log it and answer it.
package triggerfilter

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/conditions"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/property"
)

// ParameterKey is the trigger parameter holding the filter.
//
// Not `filter`: a pack trigger declares its own parameters, and a pack whose
// service has a "filter" setting of its own would collide with it. The key is
// reserved on every trigger that carries the property.
const ParameterKey = "triggerFilter"

// Property is the parameter definition every filterable trigger carries.
//
// Optional and empty by default: a trigger with no conditions runs for every
// event, which is what it did before the property existed.
func Property() property.PropertyDefinition {
	return property.PropertyDefinition{
		Key:   ParameterKey,
		Label: "Only run when",
		Kind:  property.KindConditions,
		Description: "Conditions checked against the trigger item before a run is queued. An event that does " +
			"not match is acknowledged and dropped without creating an execution. Leave empty to run for every event.",
	}
}

// Result is the outcome of checking one trigger item.
type Result struct {
	// Matched reports whether the item may start a run.
	Matched bool
	// Reason says why an item was filtered out. Empty when Matched.
	Reason string
}

// Check evaluates a trigger's filter against the item the trigger would emit.
//
// parameters is the trigger node's own configuration, as the binding or
// subscription carries it; an absent or empty filter matches. item is the
// trigger item, exposed to expressions as `$json`.
func Check(parameters map[string]any, item map[string]any) Result {
	declared, present := parameters[ParameterKey]
	if !present || declared == nil {
		return Result{Matched: true}
	}
	return CheckFilter(declared, item)
}

// CheckFilter evaluates a filter value already read out of a trigger's
// parameters.
func CheckFilter(declared any, item map[string]any) Result {
	if declared == nil {
		return Result{Matched: true}
	}
	if item == nil {
		item = map[string]any{}
	}
	resolved, err := expression.Resolve(map[string]any{ParameterKey: declared}, expression.Context{JSON: item})
	if err != nil {
		return Result{Reason: fmt.Sprintf("the trigger filter could not be evaluated: %v", err)}
	}
	shape, ok := resolved[ParameterKey].(map[string]any)
	if !ok {
		return Result{Reason: "the trigger filter is not a conditions object"}
	}
	filter, err := conditions.Parse(shape)
	if err != nil {
		return Result{Reason: fmt.Sprintf("the trigger filter is malformed: %v", err)}
	}
	matched, err := conditions.Evaluate(filter)
	if err != nil {
		return Result{Reason: fmt.Sprintf("the trigger filter could not be evaluated: %v", err)}
	}
	if !matched {
		return Result{Reason: "the event did not match the trigger filter"}
	}
	return Result{Matched: true}
}
