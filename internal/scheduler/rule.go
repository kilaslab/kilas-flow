package scheduler

import (
	"fmt"
	"sort"
	"strconv"
	"strings"
)

// Interval is one entry of a Schedule Trigger's rule.
//
// The shape is n8n's, field for field, because an imported workflow carries it
// verbatim and a rule the editor writes has to read back the same way. A rule
// holds several of these at once: "every weekday at 09:00 and again at 17:00"
// is two intervals, not one expression.
type Interval struct {
	// Field selects which of the other members applies.
	Field string
	// SecondsInterval and friends are "every N of this unit".
	SecondsInterval int
	MinutesInterval int
	HoursInterval   int
	DaysInterval    int
	WeeksInterval   int
	MonthsInterval  int
	// TriggerAtDayOfMonth is 1-31, for a monthly interval.
	TriggerAtDayOfMonth int
	// TriggerAtDay holds weekdays as Sunday 0 through Saturday 6, n8n's
	// numbering and cron's.
	TriggerAtDay                   []int
	TriggerAtHour, TriggerAtMinute int
	// Expression is the raw cron of a custom interval.
	Expression string
}

// Interval field names, as n8n spells them.
const (
	FieldSeconds        = "seconds"
	FieldMinutes        = "minutes"
	FieldHours          = "hours"
	FieldDays           = "days"
	FieldWeeks          = "weeks"
	FieldMonths         = "months"
	FieldCronExpression = "cronExpression"
)

// KnownFields lists every interval kind, in the order the editor shows them.
func KnownFields() []string {
	return []string{FieldSeconds, FieldMinutes, FieldHours, FieldDays, FieldWeeks, FieldMonths, FieldCronExpression}
}

// ReadRule decodes a Schedule Trigger's `rule` parameter.
//
// A node stores the rule exactly as n8n does — `{"interval": [ … ]}` — so the
// importer has nothing to translate and an exported workflow needs no second
// encoding. Absent or empty is not an error here: the node's validator is what
// reports it, with a message about the node rather than about a map.
func ReadRule(value any) []Interval {
	rule, _ := value.(map[string]any)
	entries, _ := rule["interval"].([]any)
	intervals := make([]Interval, 0, len(entries))
	for _, entry := range entries {
		fields, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		intervals = append(intervals, withDefaults(Interval{
			Field:               strings.TrimSpace(text(fields["field"])),
			SecondsInterval:     whole(fields["secondsInterval"]),
			MinutesInterval:     whole(fields["minutesInterval"]),
			HoursInterval:       whole(fields["hoursInterval"]),
			DaysInterval:        whole(fields["daysInterval"]),
			WeeksInterval:       whole(fields["weeksInterval"]),
			MonthsInterval:      whole(fields["monthsInterval"]),
			TriggerAtDayOfMonth: whole(fields["triggerAtDayOfMonth"]),
			TriggerAtDay:        weekdays(fields["triggerAtDay"]),
			TriggerAtHour:       whole(fields["triggerAtHour"]),
			TriggerAtMinute:     whole(fields["triggerAtMinute"]),
			Expression:          strings.TrimSpace(text(fields["expression"])),
		}))
	}
	return intervals
}

// withDefaults fills in what an interval left out, matching n8n's own
// withIntervalDefaults. An unknown field becomes `days`, which is what n8n
// does rather than failing: a rule from a newer version still runs daily.
func withDefaults(interval Interval) Interval {
	if !known(interval.Field) {
		interval.Field = FieldDays
	}
	switch interval.Field {
	case FieldSeconds:
		interval.SecondsInterval = orDefault(interval.SecondsInterval, 30)
	case FieldMinutes:
		interval.MinutesInterval = orDefault(interval.MinutesInterval, 5)
	case FieldHours:
		interval.HoursInterval = orDefault(interval.HoursInterval, 1)
	case FieldDays:
		interval.DaysInterval = orDefault(interval.DaysInterval, 1)
	case FieldWeeks:
		interval.WeeksInterval = orDefault(interval.WeeksInterval, 1)
		if len(interval.TriggerAtDay) == 0 {
			interval.TriggerAtDay = []int{0}
		}
	case FieldMonths:
		interval.MonthsInterval = orDefault(interval.MonthsInterval, 1)
		interval.TriggerAtDayOfMonth = orDefault(interval.TriggerAtDayOfMonth, 1)
	}
	return interval
}

func known(field string) bool {
	for _, candidate := range KnownFields() {
		if field == candidate {
			return true
		}
	}
	return false
}

func orDefault(value, fallback int) int {
	if value <= 0 {
		return fallback
	}
	return value
}

// Validate reports an interval the trigger cannot run, using n8n 1.3's ranges.
func (interval Interval) Validate() error {
	switch interval.Field {
	case FieldSeconds:
		if interval.SecondsInterval < 1 || interval.SecondsInterval > 59 {
			return fmt.Errorf("seconds must be in range 1-59")
		}
	case FieldMinutes:
		if interval.MinutesInterval < 1 || interval.MinutesInterval > 59 {
			return fmt.Errorf("minutes must be in range 1-59")
		}
	case FieldHours:
		if interval.HoursInterval < 1 || interval.HoursInterval > 23 {
			return fmt.Errorf("hours must be in range 1-23")
		}
	case FieldDays:
		if interval.DaysInterval < 1 || interval.DaysInterval > 31 {
			return fmt.Errorf("days must be in range 1-31")
		}
	case FieldMonths:
		if interval.MonthsInterval < 1 {
			return fmt.Errorf("months must be larger than 0")
		}
	case FieldCronExpression:
		if interval.Expression == "" {
			return fmt.Errorf("a custom interval needs a cron expression")
		}
	}
	// The trigger-at fields are n8n's own inputs and it bounds them in the
	// editor rather than at run time; bounding them here as well is what stops
	// an imported or API-written rule producing a cron nothing can parse.
	if interval.TriggerAtHour < 0 || interval.TriggerAtHour > 23 {
		return fmt.Errorf("the trigger hour must be in range 0-23")
	}
	if interval.TriggerAtMinute < 0 || interval.TriggerAtMinute > 59 {
		return fmt.Errorf("the trigger minute must be in range 0-59")
	}
	if interval.Field == FieldMonths && (interval.TriggerAtDayOfMonth < 1 || interval.TriggerAtDayOfMonth > 31) {
		return fmt.Errorf("the day of the month must be in range 1-31")
	}
	for _, day := range interval.TriggerAtDay {
		if day < 0 || day > 6 {
			return fmt.Errorf("a weekday must be in range 0-6, Sunday to Saturday")
		}
	}
	if spec, err := interval.Cron(); err != nil {
		return err
	} else if err := Validate(spec); err != nil {
		return err
	}
	return nil
}

// Cron renders the interval as a cron specification.
//
// Every interval becomes cron, including the visual ones, because cron is what
// this deployment's scheduler runs and a second scheduling engine beside it
// would be two things to keep correct. What that costs is stated in Anchoring
// below and reported as an import diagnostic, never silently absorbed.
func (interval Interval) Cron() (string, error) {
	switch interval.Field {
	case FieldCronExpression:
		if interval.Expression == "" {
			return "", fmt.Errorf("a custom interval needs a cron expression")
		}
		return interval.Expression, nil
	case FieldSeconds:
		return fmt.Sprintf("*/%d * * * * *", interval.SecondsInterval), nil
	case FieldMinutes:
		return fmt.Sprintf("*/%d * * * *", interval.MinutesInterval), nil
	case FieldHours:
		return fmt.Sprintf("%d %s * * *", interval.TriggerAtMinute, every(interval.HoursInterval)), nil
	case FieldDays:
		return fmt.Sprintf("%d %d %s * *", interval.TriggerAtMinute, interval.TriggerAtHour, every(interval.DaysInterval)), nil
	case FieldWeeks:
		return fmt.Sprintf("%d %d * * %s", interval.TriggerAtMinute, interval.TriggerAtHour, weekdayList(interval.TriggerAtDay)), nil
	case FieldMonths:
		return fmt.Sprintf("%d %d %d %s *", interval.TriggerAtMinute, interval.TriggerAtHour,
			interval.TriggerAtDayOfMonth, every(interval.MonthsInterval)), nil
	default:
		return "", fmt.Errorf("interval %q is not supported", interval.Field)
	}
}

// Anchoring reports how an interval's meaning differs from n8n's, or "" when
// it does not.
//
// n8n counts an interval from the moment the workflow was activated and keeps
// the count in the node's own state; cron counts from the calendar. For every
// interval of one — every day, every month — the two agree exactly. For a
// larger interval they do not, and this is the sentence that says so.
//
// It is a diagnostic rather than a refusal because the schedule still runs at
// the right time of day on the right kind of day; only which of them is picked
// differs. Refusing would take a working workflow away over a difference the
// author can see and accept.
func (interval Interval) Anchoring() string {
	switch interval.Field {
	case FieldDays:
		if interval.DaysInterval > 1 {
			return fmt.Sprintf("every %d days runs on days 1, %d, %d… of each month and restarts at each month boundary, "+
				"rather than counting from when the workflow was activated",
				interval.DaysInterval, 1+interval.DaysInterval, 1+2*interval.DaysInterval)
		}
	case FieldWeeks:
		if interval.WeeksInterval > 1 {
			return fmt.Sprintf("every %d weeks has no cron equivalent; it was imported as every week on the same days",
				interval.WeeksInterval)
		}
	case FieldMonths:
		if interval.MonthsInterval > 1 {
			return fmt.Sprintf("every %d months counts from January rather than from when the workflow was activated",
				interval.MonthsInterval)
		}
	}
	return ""
}

func every(step int) string {
	if step <= 1 {
		return "*"
	}
	return "*/" + strconv.Itoa(step)
}

// weekdayList renders the selected weekdays, deduplicated and in order, so two
// rules selecting the same days produce the same cron and therefore the same
// stored row.
func weekdayList(days []int) string {
	if len(days) == 0 {
		return "*"
	}
	seen := make(map[int]struct{}, len(days))
	unique := make([]int, 0, len(days))
	for _, day := range days {
		if _, exists := seen[day]; exists {
			continue
		}
		seen[day] = struct{}{}
		unique = append(unique, day)
	}
	sort.Ints(unique)
	rendered := make([]string, 0, len(unique))
	for _, day := range unique {
		rendered = append(rendered, strconv.Itoa(day))
	}
	return strings.Join(rendered, ",")
}

func text(value any) string {
	switch typed := value.(type) {
	case string:
		return typed
	case nil:
		return ""
	default:
		return fmt.Sprintf("%v", typed)
	}
}

func whole(value any) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		parsed, err := strconv.Atoi(strings.TrimSpace(typed))
		if err != nil {
			return 0
		}
		return parsed
	default:
		return 0
	}
}

func weekdays(value any) []int {
	switch typed := value.(type) {
	case []any:
		days := make([]int, 0, len(typed))
		for _, entry := range typed {
			days = append(days, whole(entry))
		}
		return days
	case []int:
		return typed
	case float64, int, string:
		return []int{whole(typed)}
	default:
		return nil
	}
}
