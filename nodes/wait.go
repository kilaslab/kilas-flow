package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/datetime"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// The wait family.
const (
	WaitNodeType   = "kilasflow.wait"
	WaitExecutorID = "core.wait"
)

// Wait resume modes.
const (
	waitResumeInterval     = "timeInterval"
	waitResumeSpecificTime = "specificTime"
	waitResumeWebhook      = "webhook"
	waitResumeForm         = "form"
)

// MaxWaitDuration bounds how long one Wait node may hold an execution.
//
// A ceiling exists because this wait is *in* the execution: the run occupies a
// worker slot and its own timeout for the whole of it. A wait that outlives the
// process — the one people actually want for "pause until tomorrow" — needs the
// execution to be suspended and resumed from storage, which is a different
// mechanism and a different ticket.
//
// One hour rather than something rounder: it is long enough for every polling
// and rate-limit pause a workflow legitimately needs, and short enough that a
// mistake costs one slot for one hour instead of one slot indefinitely. In
// practice the execution's own timeout usually binds first — on a stock
// install execution.default_timeout is 60s, so an hour-long wait is cut short
// by the run timing out rather than ending in a resume.
const MaxWaitDuration = time.Hour

// waitNode pauses an execution for a bounded time.
func waitNode() node.Definition {
	shownFor := func(modes ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(modes))
		for _, mode := range modes {
			conditions = append(conditions, node.VisibilityCondition{Key: "resume", Equals: mode})
		}
		return conditions
	}
	return node.Definition{
		Type:        WaitNodeType,
		Version:     workflow.V(1),
		DisplayName: "Wait",
		Description: "Pauses the workflow for up to " + MaxWaitDuration.String() +
			", then passes its items through unchanged.",
		Category:  "Flow",
		Group:     []node.NodeGroup{node.GroupTransform},
		Icon:      &node.NodeIcon{Light: "builtin:pause"},
		IconColor: "#f59e0b",
		Subtitle:  "{{ $parameter.resume }}",
		Inputs:    mainInput(),
		Outputs:   mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "resume", Label: "Resume", Kind: node.PropertyOptions, Required: true, Default: waitResumeInterval,
				Options: []node.PropertyOption{
					{Label: "After a time interval", Value: waitResumeInterval},
					{Label: "At a specified time", Value: waitResumeSpecificTime},
				},
				Description: "Resuming on a webhook call or a submitted form needs the execution to be " +
					"suspended to storage and woken again later, which this server does not do yet.",
			},
			{
				Key: "amount", Label: "Wait amount", Kind: node.PropertyNumber, Default: 1,
				VisibleWhen: shownFor(waitResumeInterval),
			},
			{
				Key: "unit", Label: "Wait unit", Kind: node.PropertyOptions, Default: "hours",
				Options: []node.PropertyOption{
					{Label: "Seconds", Value: "seconds"},
					{Label: "Minutes", Value: "minutes"},
					{Label: "Hours", Value: "hours"},
					{Label: "Days", Value: "days"},
				},
				VisibleWhen: shownFor(waitResumeInterval),
			},
			{
				Key: "dateTime", Label: "Date and time", Kind: node.PropertyString,
				Description: "When to resume. A time already past resumes immediately. Supports expressions.",
				VisibleWhen: shownFor(waitResumeSpecificTime),
			},
			{
				Key: "timezone", Label: "Timezone", Kind: node.PropertyString, Default: "UTC",
				Description: "IANA zone the date is read in, when it carries no offset of its own.",
				VisibleWhen: shownFor(waitResumeSpecificTime),
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     WaitExecutorID,
		Validate:       validateWaitConfiguration,
	}
}

func validateWaitConfiguration(n workflow.Node) error {
	switch mode := textParameter(n.Parameters, "resume"); mode {
	case "", waitResumeInterval:
		// The amount is checked rather than the unit: an expression may supply
		// either, and a negative literal is the mistake worth catching early.
		if amount, ok := n.Parameters["amount"].(float64); ok && amount < 0 {
			return fmt.Errorf("a wait cannot be negative")
		}
	case waitResumeSpecificTime:
		if n.Parameters["dateTime"] == nil {
			return fmt.Errorf("waiting until a specified time needs that time")
		}
	case waitResumeWebhook, waitResumeForm:
		return fmt.Errorf("resuming on %q needs the execution to be suspended to storage and woken later, "+
			"which this server does not do yet; use a time interval, or split the workflow at this point "+
			"and start the second half from a Webhook trigger", mode)
	default:
		return fmt.Errorf("resume mode %q is not supported", mode)
	}
	if zone := textParameter(n.Parameters, "timezone"); zone != "" && !expression.IsExpression(n.Parameters["timezone"]) {
		if _, err := datetime.Zone(zone); err != nil {
			return err
		}
	}
	return nil
}

// executeWait holds the execution, then passes its items through unchanged.
//
// One wait for the whole node rather than one per item: "wait an hour" said
// once over a hundred items means an hour, not a hundred hours, and the
// per-item reading is a mistake nobody would notice until a workflow that used
// to finish stopped finishing. The parameters are therefore resolved against
// the first item.
func executeWait(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	first := workflow.Item{JSON: map[string]any{}}
	if len(items) > 0 {
		first = items[0]
	}
	parameters, err := expression.Resolve(ir.Parameters, expressionContext(first, input, request, 0))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	pause, err := waitDuration(parameters, time.Now())
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if pause > MaxWaitDuration {
		return nil, fmt.Errorf("node %q: a wait of %s is longer than this server's limit of %s; the run holds "+
			"a worker for the whole of it, so a longer pause needs the execution suspended to storage",
			ir.Name, pause.Truncate(time.Second), MaxWaitDuration)
	}
	if pause > 0 {
		timer := time.NewTimer(pause)
		defer timer.Stop()
		select {
		case <-ctx.Done():
			// The node's own timeout, the execution's, or a cancellation. All
			// three mean the same thing here and the context says which.
			return nil, ctx.Err()
		case <-timer.C:
		}
	}
	return workflow.NodeOutput{items}, nil
}

// waitDuration works out how long to pause.
func waitDuration(parameters map[string]any, now time.Time) (time.Duration, error) {
	switch mode := textValue(parameters["resume"], waitResumeInterval); mode {
	case waitResumeInterval:
		amount := numberValue(parameters["amount"])
		if amount <= 0 {
			return 0, nil
		}
		unit, err := waitUnit(textValue(parameters["unit"], "hours"))
		if err != nil {
			return 0, err
		}
		return time.Duration(amount * float64(unit)), nil

	case waitResumeSpecificTime:
		location, err := datetime.Zone(textValue(parameters["timezone"], "UTC"))
		if err != nil {
			return 0, err
		}
		until, err := datetime.Parse(parameters["dateTime"], location)
		if err != nil {
			return 0, err
		}
		// A time already past is not an error: a workflow that runs daily and
		// waits until 09:00 is simply already there on a late run.
		if !until.After(now) {
			return 0, nil
		}
		return until.Sub(now), nil

	case waitResumeWebhook, waitResumeForm:
		return 0, fmt.Errorf("resuming on %q needs the execution to be suspended to storage and woken later, "+
			"which this server does not do yet", mode)
	default:
		return 0, fmt.Errorf("resume mode %q is not supported", mode)
	}
}

func waitUnit(unit string) (time.Duration, error) {
	switch unit {
	case "seconds":
		return time.Second, nil
	case "minutes":
		return time.Minute, nil
	case "hours":
		return time.Hour, nil
	case "days":
		return 24 * time.Hour, nil
	default:
		return 0, fmt.Errorf("wait unit %q is not supported", unit)
	}
}
