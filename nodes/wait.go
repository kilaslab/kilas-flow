package nodes

import (
	"context"
	"fmt"
	"time"

	"github.com/kilaslab/kilas-flow/internal/datetime"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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

// Wait limit types: the two shapes a call-resumed wait's limit takes, named
// after n8n's own limitType values so an imported node reads the same here.
const (
	waitLimitAfterInterval = "afterTimeInterval"
	waitLimitAtTime        = "atSpecifiedTime"
)

// MaxWaitDuration bounds how long one Wait node may hold an execution.
//
// The bound is the engine's own MaxWaitTTL, not the hour it used to be, and
// the reason is the suspension: a Wait is durable state, not a held worker.
// The execution is parked in storage with a checkpoint and the worker moves
// on, so "pause until tomorrow" costs nothing while it waits. The old hour
// refused exactly the waits this mechanism was built for — drip campaigns,
// follow-ups, a review that happens tomorrow — and refused them for a reason
// (one worker slot held for an hour) that stopped being true.
//
// A ceiling still exists, and deliberately: a wait that cannot resolve is not
// a wait but a leak, so anything past the TTL fails at composition time with
// the limit named rather than parking an execution nobody will ever resume.
const MaxWaitDuration = engine.MaxWaitTTL

// waitNode pauses an execution for a bounded time.
func waitNode() node.Definition {
	shownFor := func(modes ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(modes))
		for _, mode := range modes {
			conditions = append(conditions, node.VisibilityCondition{Key: "resume", Equals: mode})
		}
		return conditions
	}
	shownForCall := func() []node.VisibilityCondition {
		// A limit belongs to the modes that wait for something to arrive: the
		// timer modes already carry their own deadline in the pause itself.
		return []node.VisibilityCondition{
			{Key: "resume", Equals: waitResumeWebhook},
			{Key: "resume", Equals: waitResumeForm},
		}
	}
	// Different keys are AND'd and same-key entries are OR'd, so this reads
	// "a call-resumed wait, with a limit, of this kind".
	shownForLimit := func(limitTypes ...string) []node.VisibilityCondition {
		conditions := append(shownForCall(), node.VisibilityCondition{Key: "limitWaitTime", Equals: true})
		for _, limitType := range limitTypes {
			conditions = append(conditions, node.VisibilityCondition{Key: "limitType", Equals: limitType})
		}
		return conditions
	}
	return node.Definition{
		Type:        WaitNodeType,
		Version:     workflow.V(1),
		DisplayName: "Wait",
		Description: "Pauses the workflow, then passes its items through unchanged. A time pause suspends " +
			"the execution to storage, so hours or days cost no worker while it waits.",
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
					{Label: "On webhook call", Value: waitResumeWebhook},
					{Label: "On form submitted", Value: waitResumeForm},
				},
				Description: "How the wait ends. A webhook call resumes when something posts to the run's " +
					"$execution.resumeUrl; a form submission resumes from this installation's approval page, " +
					"where a human approves or rejects. Both park the execution in storage, so nothing holds " +
					"a worker while they wait.",
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
			{
				Key: "limitWaitTime", Label: "Limit wait time", Kind: node.PropertyBoolean, Default: false,
				Description: "End the wait at a limit even if nothing calls it. Without a limit a wait nobody " +
					"answers ends as a named failure at the execution's wait lifetime, which is a day.",
				VisibleWhen: shownForCall(),
			},
			{
				Key: "limitType", Label: "Limit type", Kind: node.PropertyOptions, Default: waitLimitAfterInterval,
				Options: []node.PropertyOption{
					{Label: "After time interval", Value: waitLimitAfterInterval},
					{Label: "At specified time", Value: waitLimitAtTime},
				},
				VisibleWhen: shownForLimit(),
			},
			{
				Key: "limitAmount", Label: "Limit amount", Kind: node.PropertyNumber, Default: 1,
				Description: "How long to wait before resuming on the limit. Supports expressions.",
				VisibleWhen: shownForLimit(waitLimitAfterInterval),
			},
			{
				Key: "limitUnit", Label: "Limit unit", Kind: node.PropertyOptions, Default: "hours",
				Options: []node.PropertyOption{
					{Label: "Seconds", Value: "seconds"},
					{Label: "Minutes", Value: "minutes"},
					{Label: "Hours", Value: "hours"},
					{Label: "Days", Value: "days"},
				},
				VisibleWhen: shownForLimit(waitLimitAfterInterval),
			},
			{
				Key: "limitAt", Label: "Limit at", Kind: node.PropertyString,
				Description: "When the limit passes. A time already past resumes immediately. Write the offset " +
					"(2026-01-01T09:00:00+07:00) so the instant is unambiguous. Supports expressions.",
				VisibleWhen: shownForLimit(waitLimitAtTime),
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
		// Both are durable suspensions the engine already knows how to hold
		// and resume, so there is nothing to refuse here beyond the limit the
		// author may have asked for. A wait with no limit is not refused
		// either: the engine's own wait lifetime bounds it, as a named
		// failure, which is a worse outcome than a limit but not an invalid
		// node.
		if boolValue(n.Parameters["limitWaitTime"]) {
			if textValue(n.Parameters["limitType"], waitLimitAfterInterval) == waitLimitAtTime {
				if n.Parameters["limitAt"] == nil {
					return fmt.Errorf("a wait limit at a specified time needs that time")
				}
			} else if amount, ok := n.Parameters["limitAmount"].(float64); ok && amount < 0 {
				return fmt.Errorf("a wait limit cannot be negative")
			}
		}
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

// executeWait suspends the execution, then passes its items through unchanged
// when it resumes.
//
// One wait for the whole node rather than one per item: "wait an hour" said
// once over a hundred items means an hour, not a hundred hours, and the
// per-item reading is a mistake nobody would notice until a workflow that used
// to finish stopped finishing. The parameters are therefore resolved against
// the first item. A pause of zero passes straight through; anything longer
// returns SuspendError so the worker and the lease are released and the run
// continues from storage, surviving a restart.
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

	suspended, err := waitSuspension(parameters, time.Now())
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if suspended == nil {
		// Nothing to wait for: a pause of zero and a deadline already past
		// both mean the node is simply done, exactly as n8n treats them.
		return workflow.NodeOutput{items}, nil
	}
	return nil, suspended
}

// waitSuspension reads resolved Wait parameters into the suspension they ask
// for, or nil for a wait that does not suspend at all.
//
// The mode it returns is the mode the service acts on: the timer modes resolve
// by deadline, while webhook and approval waits resolve when something calls
// their resume URL. A call-resumed wait with a limit is deliberately *not* one
// of the call modes — its limit is what resumes it, so it is held as a timer
// and a call to the resume URL simply ends it early.
func waitSuspension(parameters map[string]any, now time.Time) (*engine.SuspendError, error) {
	switch resume := textValue(parameters["resume"], waitResumeInterval); resume {
	case waitResumeInterval, waitResumeSpecificTime:
		pause, err := waitDuration(parameters, now)
		if err != nil {
			return nil, err
		}
		if pause > MaxWaitDuration {
			return nil, fmt.Errorf("a wait of %s is longer than this server's limit of %s",
				pause.Truncate(time.Second), MaxWaitDuration)
		}
		if pause <= 0 {
			return nil, nil
		}
		mode := engine.WaitModeInterval
		if resume == waitResumeSpecificTime {
			mode = engine.WaitModeUntil
		}
		return &engine.SuspendError{Mode: mode, ExpiresAt: now.Add(pause)}, nil

	case waitResumeWebhook, waitResumeForm:
		waitMode := engine.WaitModeWebhook
		if resume == waitResumeForm {
			// This installation's form surface is the approval page: a human
			// approves or rejects, and the run continues with that decision.
			waitMode = engine.WaitModeApproval
		}
		limit, limitMode, err := waitLimit(parameters, now)
		if err != nil {
			return nil, err
		}
		if limit.IsZero() {
			// No deadline of its own: the service applies the execution's wait
			// lifetime, and the sweeper fails the run by name when it passes
			// rather than leaving it suspended forever.
			return &engine.SuspendError{Mode: waitMode}, nil
		}
		if !limit.After(now) {
			return nil, nil
		}
		if limit.Sub(now) > MaxWaitDuration {
			return nil, fmt.Errorf("a wait of %s is longer than this server's limit of %s",
				limit.Sub(now).Truncate(time.Second), MaxWaitDuration)
		}
		return &engine.SuspendError{Mode: limitMode, ExpiresAt: limit}, nil

	default:
		return nil, fmt.Errorf("resume mode %q is not supported", resume)
	}
}

// waitLimit reads the limit a call-resumed wait resumes on when nothing calls
// it. An unset limit returns the zero time and an empty mode, which is what
// leaves the wait bounded by the execution's own wait lifetime.
func waitLimit(parameters map[string]any, now time.Time) (time.Time, string, error) {
	if !boolValue(parameters["limitWaitTime"]) {
		return time.Time{}, "", nil
	}
	if textValue(parameters["limitType"], waitLimitAfterInterval) == waitLimitAtTime {
		until, err := datetime.Parse(parameters["limitAt"], time.UTC)
		if err != nil {
			return time.Time{}, "", err
		}
		return until, engine.WaitModeUntil, nil
	}
	amount := numberValue(parameters["limitAmount"])
	if amount <= 0 {
		// A limit of zero is a limit already reached, like a zero pause: the
		// node is done the moment it runs.
		return now, engine.WaitModeInterval, nil
	}
	unit, err := waitUnit(textValue(parameters["limitUnit"], "hours"))
	if err != nil {
		return time.Time{}, "", err
	}
	return now.Add(time.Duration(amount * float64(unit))), engine.WaitModeInterval, nil
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
		// Dispatched by waitSuspension before this is reached: a call-resumed
		// wait's pause is its limit, not a duration of its own.
		return 0, fmt.Errorf("resume mode %q waits for a call, not a duration", mode)
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
