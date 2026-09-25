package nodes

import (
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/expression"
)

// This file is how an agent tool hands the model's $fromAI arguments to the
// parameters the author wrote, for every tool that evaluates those
// parameters: the HTTP Request, Workflow, Calculator and Data table tools.
//
// The model's values are data from end to end. An expression the author wrote
// is never rewritten with them; it is evaluated as written, with the
// arguments in its context (expression.Context.FromAIArguments), where
// $fromAI returns each argument as a value the expression computes with. The
// tools used to splice the values into the expression's source and evaluate
// the result, which let a model — or whoever wrote the text a model echoed —
// turn a value into code: a verbatim expression marker, `{{ … }}` around a
// call, a value spliced before `.toUpperCase()`, or a `}}` inside a literal
// that moved where a segment ended. `$env`, `$execution` and every upstream
// node's output are one such value away. Only a plain string, which nothing
// evaluates, has its $fromAI filled in place.

// fillToolFromAI prepares one tool call's parameters. It returns the
// parameters with their plain strings filled and every expression left as the
// author wrote it, and the arguments those expressions read through $fromAI.
// The parameters are a copy; the tool's own template is never written to,
// because a tool outlives one call.
func fillToolFromAI(parameters, supplied map[string]any) (map[string]any, map[string]any, error) {
	fromAI, err := toolFromAIArguments(parameters, supplied)
	if err != nil {
		return nil, nil, err
	}
	// SubstituteFromAI fills plain strings only and returns a copy; the
	// expressions keep the author's source.
	filled, err := ai.SubstituteFromAI(parameters, fromAI)
	if err != nil {
		return nil, nil, err
	}
	if err := checkToolFilled(parameters, filled, "parameters"); err != nil {
		return nil, nil, err
	}
	return filled, fromAI, nil
}

// toolFromAIArguments collects the value of every key the parameters declare
// through $fromAI: what the model supplied, or else the call's own default,
// parsed in its declared type so a number default arrives as a number. Keys
// the parameters never declare do not cross.
//
// A value that is or holds an expression marker is refused whatever type its
// call declared. The model is not held to the schema it was offered, and a
// marker filled whole into a plain string lands in the parameter tree, where
// the executor would evaluate it. A key with neither a value nor a default is
// refused by name before anything runs, as it was when the values were
// spliced.
func toolFromAIArguments(parameters, supplied map[string]any) (map[string]any, error) {
	calls, err := ai.ExtractFromAI(parameters)
	if err != nil {
		return nil, err
	}
	fromAI := make(map[string]any, len(calls))
	for _, call := range calls {
		if value, present := supplied[call.Key]; present && value != nil {
			if holdsExpressionMarker(value) {
				return nil, fmt.Errorf("tool argument %q holds an expression marker, which a tool never accepts", call.Key)
			}
			fromAI[call.Key] = value
			continue
		}
		if call.HasDefault {
			fromAI[call.Key] = call.Default
			continue
		}
		return nil, fmt.Errorf("tool argument %q was not supplied and $fromAI(%q, …) declares no default", call.Key, call.Key)
	}
	return fromAI, nil
}

// checkToolFilled refuses a filled parameter tree whose shape the model
// changed. Filling a plain string can only put a value where the string was,
// so a marker, or a map with other keys, where the author's template had
// neither is the model's doing — a value like
// {"mode": "$fromAI('m')", "value": "$fromAI('v')"} filled with "expression"
// and a template would otherwise be evaluated as one. The value a whole-call
// json argument fills in is checked for markers at any depth, for the same
// reason.
func checkToolFilled(template, filled any, path string) error {
	if !expression.IsExpression(template) && expression.IsExpression(filled) {
		return fmt.Errorf("%s became an expression once the model's values were filled in, and a tool never evaluates one the author did not write", path)
	}
	switch typed := template.(type) {
	case map[string]any:
		if expression.IsExpression(typed) {
			return nil
		}
		object, _ := filled.(map[string]any)
		if len(object) != len(typed) {
			return fmt.Errorf("%s changed its keys once the model's values were filled in", path)
		}
		for _, key := range sortedParameterKeys(typed) {
			value, present := object[key]
			if !present {
				return fmt.Errorf("%s changed its keys once the model's values were filled in", path)
			}
			if err := checkToolFilled(typed[key], value, path+"."+key); err != nil {
				return err
			}
		}
	case []any:
		list, _ := filled.([]any)
		if len(list) != len(typed) {
			return fmt.Errorf("%s changed its length once the model's values were filled in", path)
		}
		for index, nested := range typed {
			if err := checkToolFilled(nested, list[index], fmt.Sprintf("%s[%d]", path, index)); err != nil {
				return err
			}
		}
	default:
		if holdsExpressionMarker(filled) {
			return fmt.Errorf("%s became an expression once the model's values were filled in, and a tool never evaluates one the author did not write", path)
		}
	}
	return nil
}

// holdsExpressionMarker reports whether a value, at any depth, is or holds an
// expression marker.
func holdsExpressionMarker(value any) bool {
	switch typed := value.(type) {
	case map[string]any:
		if mode, _ := typed["mode"].(string); mode == "expression" {
			return true
		}
		for _, nested := range typed {
			if holdsExpressionMarker(nested) {
				return true
			}
		}
	case []any:
		for _, nested := range typed {
			if holdsExpressionMarker(nested) {
				return true
			}
		}
	}
	return false
}
