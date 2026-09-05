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

// The date and time family.
const (
	DateTimeNodeType   = "kilasflow.dateTime"
	DateTimeExecutorID = "core.dateTime"
)

// Date & Time operations.
const (
	dateOperationCurrent  = "getCurrentDate"
	dateOperationAdd      = "addToDate"
	dateOperationSubtract = "subtractFromDate"
	dateOperationFormat   = "formatDate"
	dateOperationRound    = "roundDate"
	dateOperationExtract  = "extractDate"
	dateOperationCompare  = "getTimeBetweenDates"
)

// dateUnits are the units every arithmetic and rounding operation shares.
func dateUnits() []node.PropertyOption {
	return []node.PropertyOption{
		{Label: "Years", Value: "years"},
		{Label: "Months", Value: "months"},
		{Label: "Weeks", Value: "weeks"},
		{Label: "Days", Value: "days"},
		{Label: "Hours", Value: "hours"},
		{Label: "Minutes", Value: "minutes"},
		{Label: "Seconds", Value: "seconds"},
		{Label: "Milliseconds", Value: "milliseconds"},
	}
}

// dateTimeNode does date arithmetic without an expression.
//
// It exists for the same reason n8n's does: a workflow that needs "thirty days
// ago, formatted for this API" should not have to be a programmer to say so.
// Every operation takes an explicit timezone, because a date node whose answer
// depended on where the server ran would produce workflows that behave
// differently in staging and production for no visible reason.
func dateTimeNode() node.Definition {
	shownFor := func(operations ...string) []node.VisibilityCondition {
		conditions := make([]node.VisibilityCondition, 0, len(operations))
		for _, operation := range operations {
			conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
		}
		return conditions
	}
	return node.Definition{
		Type:        DateTimeNodeType,
		Version:     workflow.V(1),
		DisplayName: "Date & Time",
		Description: "Reads, shifts, rounds, formats and compares dates.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:calendar-clock"},
		IconColor:   "#0ea5e9",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: dateOperationCurrent,
				Options: []node.PropertyOption{
					{Label: "Get current date", Value: dateOperationCurrent},
					{Label: "Add to a date", Value: dateOperationAdd},
					{Label: "Subtract from a date", Value: dateOperationSubtract},
					{Label: "Format a date", Value: dateOperationFormat},
					{Label: "Round a date", Value: dateOperationRound},
					{Label: "Extract part of a date", Value: dateOperationExtract},
					{Label: "Compare two dates", Value: dateOperationCompare},
				},
			},
			{
				Key: "date", Label: "Date", Kind: node.PropertyString, Default: "={{ $json.date }}",
				Description: "The date to work on. Accepts ISO 8601, a Unix timestamp in seconds or " +
					"milliseconds, and the common human forms. Supports expressions.",
				VisibleWhen: shownFor(dateOperationAdd, dateOperationSubtract, dateOperationFormat,
					dateOperationRound, dateOperationExtract, dateOperationCompare),
			},
			{
				Key: "endDate", Label: "Second date", Kind: node.PropertyString,
				Description: "The date the first is compared against. Supports expressions.",
				VisibleWhen: shownFor(dateOperationCompare),
			},
			{
				Key: "duration", Label: "Amount", Kind: node.PropertyNumber, Default: 1,
				VisibleWhen: shownFor(dateOperationAdd, dateOperationSubtract),
			},
			{
				Key: "unit", Label: "Unit", Kind: node.PropertyOptions, Default: "days",
				Options:     dateUnits(),
				VisibleWhen: shownFor(dateOperationAdd, dateOperationSubtract, dateOperationCompare),
			},
			{
				Key: "roundTo", Label: "Round to", Kind: node.PropertyOptions, Default: "days",
				Options:     dateUnits(),
				VisibleWhen: shownFor(dateOperationRound),
			},
			{
				Key: "roundMode", Label: "Direction", Kind: node.PropertyOptions, Default: "roundDown",
				Options: []node.PropertyOption{
					{Label: "Round down (start of)", Value: "roundDown"},
					{Label: "Round up (start of the next)", Value: "roundUp"},
				},
				VisibleWhen: shownFor(dateOperationRound),
			},
			{
				Key: "part", Label: "Part", Kind: node.PropertyOptions, Default: "year",
				Options: []node.PropertyOption{
					{Label: "Year", Value: "year"},
					{Label: "Quarter", Value: "quarter"},
					{Label: "Month", Value: "month"},
					{Label: "Week", Value: "week"},
					{Label: "Day of year", Value: "dayOfYear"},
					{Label: "Day of month", Value: "day"},
					{Label: "Day of week", Value: "weekday"},
					{Label: "Hour", Value: "hour"},
					{Label: "Minute", Value: "minute"},
					{Label: "Second", Value: "second"},
				},
				VisibleWhen: shownFor(dateOperationExtract),
			},
			{
				Key: "format", Label: "Format", Kind: node.PropertyString, Default: "yyyy-MM-dd'T'HH:mm:ssZZ",
				Description: "Luxon format tokens, which is what n8n writes: yyyy-MM-dd, cccc for the weekday, " +
					"HH:mm for the time. Text inside single quotes is kept as written.",
				VisibleWhen: shownFor(dateOperationFormat),
			},
			{
				Key: "timezone", Label: "Timezone", Kind: node.PropertyString, Default: "UTC",
				Description: "IANA zone name, such as UTC or Asia/Jakarta. A date that already carries an " +
					"offset keeps the instant it names; only a date without one is read in this zone.",
			},
			{
				Key: "outputField", Label: "Output field", Kind: node.PropertyString, Default: "date",
				Description: "Which field on the outgoing item the result is written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     DateTimeExecutorID,
		Validate:       validateDateTimeConfiguration,
	}
}

func validateDateTimeConfiguration(n workflow.Node) error {
	operation := textParameter(n.Parameters, "operation")
	switch operation {
	case "", dateOperationCurrent, dateOperationAdd, dateOperationSubtract,
		dateOperationFormat, dateOperationRound, dateOperationExtract, dateOperationCompare:
	default:
		return fmt.Errorf("operation %q is not supported", operation)
	}
	// The zone is checked here, not at run time. A zone name is a fixed string
	// in almost every workflow, and finding out it was misspelled when the node
	// finally runs means finding out in production.
	if zone := textParameter(n.Parameters, "timezone"); zone != "" && !expression.IsExpression(n.Parameters["timezone"]) {
		if _, err := datetime.Zone(zone); err != nil {
			return err
		}
	}
	if textParameter(n.Parameters, "outputField") == "" && n.Parameters["outputField"] != nil {
		return fmt.Errorf("the output field needs a name")
	}
	return nil
}

// executeDateTime resolves and applies one operation per item.
func executeDateTime(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		// A Date & Time node with nothing upstream still has an answer for
		// "what time is it", which is the operation most likely to be first in
		// a workflow.
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		value, err := applyDateOperation(parameters, time.Now())
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		produced := cloneMap(item.JSON)
		if produced == nil {
			produced = map[string]any{}
		}
		// Dot notation on, as the Set node does, so `result.date` writes a
		// nested object rather than a key with a dot in its name.
		writeField(produced, defaultString(textValue(parameters["outputField"], ""), "date"), value, true)
		out = append(out, workflow.Item{JSON: produced, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{out}, nil
}

// applyDateOperation is the whole of the node's behaviour, over already
// resolved parameters.
func applyDateOperation(parameters map[string]any, startedAt time.Time) (any, error) {
	location, err := datetime.Zone(textValue(parameters["timezone"], "UTC"))
	if err != nil {
		return nil, err
	}
	operation := textValue(parameters["operation"], dateOperationCurrent)

	if operation == dateOperationCurrent {
		now := startedAt
		if now.IsZero() {
			now = time.Now()
		}
		return now.In(location).Format(time.RFC3339), nil
	}

	instant, err := datetime.Parse(parameters["date"], location)
	if err != nil {
		return nil, err
	}

	switch operation {
	case dateOperationAdd:
		return shift(instant, textValue(parameters["unit"], "days"), int(numberValue(parameters["duration"]))).Format(time.RFC3339), nil
	case dateOperationSubtract:
		return shift(instant, textValue(parameters["unit"], "days"), -int(numberValue(parameters["duration"]))).Format(time.RFC3339), nil
	case dateOperationFormat:
		return datetime.Format(instant, textValue(parameters["format"], "yyyy-MM-dd'T'HH:mm:ssZZ")), nil
	case dateOperationRound:
		return round(instant, textValue(parameters["roundTo"], "days"), textValue(parameters["roundMode"], "roundDown")).Format(time.RFC3339), nil
	case dateOperationExtract:
		return extract(instant, textValue(parameters["part"], "year")), nil
	case dateOperationCompare:
		other, err := datetime.Parse(parameters["endDate"], location)
		if err != nil {
			return nil, err
		}
		return compare(instant, other, textValue(parameters["unit"], "days")), nil
	default:
		return nil, fmt.Errorf("operation %q is not supported", operation)
	}
}

// shift moves an instant by whole units.
//
// Calendar units go through AddDate so "one month after 31 January" lands where
// a person expects rather than 31 days later, and so a shift across a daylight
// saving boundary keeps the wall-clock time. Clock units are added as
// durations, because that is what "add two hours" means.
func shift(instant time.Time, unit string, amount int) time.Time {
	switch unit {
	case "years":
		return instant.AddDate(amount, 0, 0)
	case "months":
		return instant.AddDate(0, amount, 0)
	case "weeks":
		return instant.AddDate(0, 0, 7*amount)
	case "days":
		return instant.AddDate(0, 0, amount)
	case "hours":
		return instant.Add(time.Duration(amount) * time.Hour)
	case "minutes":
		return instant.Add(time.Duration(amount) * time.Minute)
	case "seconds":
		return instant.Add(time.Duration(amount) * time.Second)
	case "milliseconds":
		return instant.Add(time.Duration(amount) * time.Millisecond)
	default:
		return instant
	}
}

// round moves an instant to the start of a unit, or to the start of the next.
func round(instant time.Time, unit, mode string) time.Time {
	down := startOf(instant, unit)
	if mode != "roundUp" {
		return down
	}
	// Already exactly on the boundary: rounding up still means the next one,
	// which is what "start of the next hour" says.
	return startOf(shift(down, nextUnit(unit), 1), unit)
}

// nextUnit is the unit one step of `unit` is made of, for rounding up.
func nextUnit(unit string) string {
	if unit == "weeks" {
		// A week's step is seven days; AddDate handles that directly.
		return "weeks"
	}
	return unit
}

func startOf(instant time.Time, unit string) time.Time {
	year, month, day := instant.Date()
	location := instant.Location()
	switch unit {
	case "years":
		return time.Date(year, time.January, 1, 0, 0, 0, 0, location)
	case "months":
		return time.Date(year, month, 1, 0, 0, 0, 0, location)
	case "weeks":
		// ISO weeks start on Monday, which is what Luxon and n8n use.
		offset := (int(instant.Weekday()) + 6) % 7
		start := time.Date(year, month, day, 0, 0, 0, 0, location)
		return start.AddDate(0, 0, -offset)
	case "days":
		return time.Date(year, month, day, 0, 0, 0, 0, location)
	case "hours":
		return instant.Truncate(time.Hour).In(location)
	case "minutes":
		return instant.Truncate(time.Minute).In(location)
	case "seconds":
		return instant.Truncate(time.Second).In(location)
	case "milliseconds":
		return instant.Truncate(time.Millisecond).In(location)
	default:
		return instant
	}
}

// extract returns one component as a number.
func extract(instant time.Time, part string) any {
	switch part {
	case "year":
		return float64(instant.Year())
	case "quarter":
		return float64((int(instant.Month())-1)/3 + 1)
	case "month":
		return float64(int(instant.Month()))
	case "week":
		_, week := instant.ISOWeek()
		return float64(week)
	case "dayOfYear":
		return float64(instant.YearDay())
	case "day":
		return float64(instant.Day())
	case "weekday":
		// Sunday 0 through Saturday 6, matching the Schedule Trigger's own
		// weekday numbering and cron's, so the two are never off by one.
		return float64(int(instant.Weekday()))
	case "hour":
		return float64(instant.Hour())
	case "minute":
		return float64(instant.Minute())
	case "second":
		return float64(instant.Second())
	default:
		return nil
	}
}

// compare reports how far the second date is from the first, in whole units.
//
// Signed, and second-minus-first, so a future date is positive: "how long until
// this" is the question people ask, and an unsigned answer would need a second
// field to say which way round it was.
func compare(left, right time.Time, unit string) float64 {
	switch unit {
	case "years":
		return float64(calendarMonths(left, right) / 12)
	case "months":
		return float64(calendarMonths(left, right))
	case "weeks":
		return float64(int(right.Sub(left).Hours()) / (24 * 7))
	case "days":
		return float64(int(right.Sub(left).Hours()) / 24)
	case "hours":
		return float64(int(right.Sub(left).Hours()))
	case "minutes":
		return float64(int(right.Sub(left).Minutes()))
	case "seconds":
		return float64(int(right.Sub(left).Seconds()))
	case "milliseconds":
		return float64(right.Sub(left).Milliseconds())
	default:
		return 0
	}
}

// calendarMonths counts whole months between two instants.
//
// Whole, not fractional: a difference of "1.97 months" is a number nobody has a
// use for, and truncating toward zero is what every other unit here does.
func calendarMonths(left, right time.Time) int {
	months := (right.Year()-left.Year())*12 + int(right.Month()) - int(left.Month())
	// The final month is only complete once the day of the month is reached.
	if months > 0 && right.Day() < left.Day() {
		months--
	}
	if months < 0 && right.Day() > left.Day() {
		months++
	}
	return months
}
