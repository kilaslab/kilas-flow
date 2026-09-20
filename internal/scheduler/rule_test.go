package scheduler_test

import (
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/scheduler"
)

func TestAnIntervalBecomesTheCronItMeans(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		stored map[string]any
		want   string
	}{
		"every 30 seconds": {
			stored: map[string]any{"field": "seconds", "secondsInterval": float64(30)},
			want:   "*/30 * * * * *",
		},
		"every 5 minutes": {
			stored: map[string]any{"field": "minutes", "minutesInterval": float64(5)},
			want:   "*/5 * * * *",
		},
		"every 2 hours at half past": {
			stored: map[string]any{"field": "hours", "hoursInterval": float64(2), "triggerAtMinute": float64(30)},
			want:   "30 */2 * * *",
		},
		"daily at 09:15": {
			stored: map[string]any{"field": "days", "daysInterval": float64(1),
				"triggerAtHour": float64(9), "triggerAtMinute": float64(15)},
			want: "15 9 * * *",
		},
		"weekdays at 09:00": {
			stored: map[string]any{"field": "weeks", "weeksInterval": float64(1),
				"triggerAtDay":  []any{float64(1), float64(2), float64(3), float64(4), float64(5)},
				"triggerAtHour": float64(9)},
			want: "0 9 * * 1,2,3,4,5",
		},
		// The same days in a different order and repeated: one cron, so two
		// rules a user would call identical produce one stored row.
		"weekdays deduplicated and ordered": {
			stored: map[string]any{"field": "weeks",
				"triggerAtDay": []any{float64(5), float64(1), float64(5)}, "triggerAtHour": float64(9)},
			want: "0 9 * * 1,5",
		},
		"the 1st of every month at 06:00": {
			stored: map[string]any{"field": "months", "monthsInterval": float64(1),
				"triggerAtDayOfMonth": float64(1), "triggerAtHour": float64(6)},
			want: "0 6 1 * *",
		},
		"a custom expression is carried as written": {
			stored: map[string]any{"field": "cronExpression", "expression": "0 9 * * 1-5"},
			want:   "0 9 * * 1-5",
		},
		// n8n's own default when a field is missing or unrecognised.
		"an unknown field falls back to daily": {
			stored: map[string]any{"field": "fortnights"},
			want:   "0 0 * * *",
		},
	} {
		t.Run(name, func(t *testing.T) {
			intervals := scheduler.ReadRule(map[string]any{"interval": []any{testCase.stored}})
			if len(intervals) != 1 {
				t.Fatalf("ReadRule() = %#v, want one interval", intervals)
			}
			got, err := intervals[0].Cron()
			if err != nil {
				t.Fatalf("Cron() error = %v", err)
			}
			if got != testCase.want {
				t.Errorf("Cron() = %q, want %q", got, testCase.want)
			}
			if err := scheduler.Validate(got); err != nil {
				t.Errorf("the generated cron does not parse: %v", err)
			}
		})
	}
}

func TestARuleCarriesEveryIntervalNotJustTheFirst(t *testing.T) {
	t.Parallel()

	// "Every weekday at 09:00 and again at 17:00" is one trigger with two
	// rules. The importer used to return on the first one carrying a cron
	// expression and drop the rest without a word.
	intervals := scheduler.ReadRule(map[string]any{"interval": []any{
		map[string]any{"field": "days", "triggerAtHour": float64(9)},
		map[string]any{"field": "days", "triggerAtHour": float64(17)},
		map[string]any{"field": "cronExpression", "expression": "*/10 * * * *"},
	}})
	if len(intervals) != 3 {
		t.Fatalf("ReadRule() = %#v, want all three intervals", intervals)
	}
	want := []string{"0 9 * * *", "0 17 * * *", "*/10 * * * *"}
	for index, interval := range intervals {
		got, err := interval.Cron()
		if err != nil {
			t.Fatalf("interval %d Cron() error = %v", index, err)
		}
		if got != want[index] {
			t.Errorf("interval %d = %q, want %q", index, got, want[index])
		}
	}
}

func TestAnIntervalOutOfRangeIsRefusedTheWayN8NRefusesIt(t *testing.T) {
	t.Parallel()

	for name, stored := range map[string]map[string]any{
		"seconds above 59":            {"field": "seconds", "secondsInterval": float64(120)},
		"minutes above 59":            {"field": "minutes", "minutesInterval": float64(90)},
		"hours above 23":              {"field": "hours", "hoursInterval": float64(48)},
		"days above 31":               {"field": "days", "daysInterval": float64(45)},
		"an hour above 23":            {"field": "days", "triggerAtHour": float64(25)},
		"a minute above 59":           {"field": "days", "triggerAtMinute": float64(75)},
		"a weekday above 6":           {"field": "weeks", "triggerAtDay": []any{float64(9)}},
		"a day of month at 40":        {"field": "months", "triggerAtDayOfMonth": float64(40)},
		"a custom with no expression": {"field": "cronExpression", "expression": "  "},
	} {
		t.Run(name, func(t *testing.T) {
			intervals := scheduler.ReadRule(map[string]any{"interval": []any{stored}})
			if len(intervals) != 1 {
				t.Fatalf("ReadRule() = %#v, want one interval", intervals)
			}
			if err := intervals[0].Validate(); err == nil {
				t.Fatalf("Validate() accepted %#v", stored)
			}
		})
	}
}

func TestAnIntervalCronCannotAnchorSaysSo(t *testing.T) {
	t.Parallel()

	// Cron counts from the calendar and n8n counts from activation. For an
	// interval of one they agree exactly and there is nothing to report.
	for _, quiet := range []map[string]any{
		{"field": "days", "daysInterval": float64(1)},
		{"field": "weeks", "weeksInterval": float64(1)},
		{"field": "months", "monthsInterval": float64(1)},
		{"field": "minutes", "minutesInterval": float64(5)},
	} {
		intervals := scheduler.ReadRule(map[string]any{"interval": []any{quiet}})
		if note := intervals[0].Anchoring(); note != "" {
			t.Errorf("%#v reported a difference that does not exist: %s", quiet, note)
		}
	}

	for name, stored := range map[string]map[string]any{
		"every 3 days":   {"field": "days", "daysInterval": float64(3)},
		"every 2 weeks":  {"field": "weeks", "weeksInterval": float64(2)},
		"every 6 months": {"field": "months", "monthsInterval": float64(6)},
	} {
		t.Run(name, func(t *testing.T) {
			intervals := scheduler.ReadRule(map[string]any{"interval": []any{stored}})
			if intervals[0].Anchoring() == "" {
				t.Errorf("%#v differs from n8n and said nothing", stored)
			}
		})
	}
}

func TestASchedulePinnedToAnHourFiresOnceAcrossTheFallBack(t *testing.T) {
	t.Parallel()

	// 2026-11-01 in New York: 01:00 to 02:00 happens twice, EDT then EST. A
	// 01:30 schedule matches both, and firing twice would run a nightly job
	// twice a year with nothing in the workflow to explain it.
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}
	spec := "TZ=America/New_York 30 1 * * *"

	before := time.Date(2026, time.October, 31, 12, 0, 0, 0, newYork)
	first, err := scheduler.Next(spec, before)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if got := first.In(newYork).Format("2006-01-02 15:04 MST"); got != "2026-11-01 01:30 EDT" {
		t.Fatalf("first run = %s, want the pre-shift 01:30", got)
	}

	second, err := scheduler.Next(spec, first)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	// The next run is the following day, not the repeated 01:30 an hour later.
	if got := second.In(newYork).Format("2006-01-02 15:04 MST"); got != "2026-11-02 01:30 EST" {
		t.Errorf("second run = %s, want the next day rather than the repeated hour", got)
	}
	if gap := second.Sub(first); gap < 23*time.Hour {
		t.Errorf("the two runs are %s apart, so the repeated wall-clock hour fired twice", gap)
	}
}

func TestAFrequentScheduleKeepsRunningThroughTheFallBack(t *testing.T) {
	t.Parallel()

	// The mirror of the test above. "Every fifteen minutes" is not pinned to a
	// wall-clock hour, so the repeated hour is simply an extra hour of running
	// and skipping an occurrence would be the bug.
	newYork, err := time.LoadLocation("America/New_York")
	if err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}
	spec := "TZ=America/New_York */15 * * * *"

	at := time.Date(2026, time.November, 1, 1, 30, 0, 0, newYork)
	next, err := scheduler.Next(spec, at)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if gap := next.Sub(at); gap != 15*time.Minute {
		t.Errorf("gap = %s, want the ordinary fifteen minutes", gap)
	}
}

func TestASchedulesZoneDecidesWhatNineInTheMorningMeans(t *testing.T) {
	t.Parallel()

	if _, err := time.LoadLocation("Asia/Jakarta"); err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}
	after := time.Date(2026, time.September, 5, 0, 0, 0, 0, time.UTC)

	utc, err := scheduler.Next("0 9 * * *", after)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	jakarta, err := scheduler.Next(scheduler.InZone("0 9 * * *", "Asia/Jakarta"), after)
	if err != nil {
		t.Fatalf("Next() error = %v", err)
	}
	if utc.Format(time.RFC3339) != "2026-09-05T09:00:00Z" {
		t.Errorf("UTC run = %s, want 09:00Z", utc.Format(time.RFC3339))
	}
	// Jakarta is UTC+7 all year, so nine there is two in the morning here.
	if jakarta.Format(time.RFC3339) != "2026-09-05T02:00:00Z" {
		t.Errorf("Jakarta run = %s, want 02:00Z", jakarta.Format(time.RFC3339))
	}
}

func TestTheTriggerItemCarriesN8NsFieldSet(t *testing.T) {
	t.Parallel()

	if _, err := time.LoadLocation("Asia/Jakarta"); err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}
	// 2026-09-05T02:04:07Z is 09:04:07 in Jakarta, a Saturday.
	item := scheduler.TriggerItem("sched_1", time.Date(2026, time.September, 5, 2, 4, 7, 0, time.UTC), "Asia/Jakarta")

	for field, want := range map[string]string{
		"Readable date": "September 5th 2026, 9:04:07 am",
		"Readable time": "9:04:07 am",
		"Day of week":   "Saturday",
		"Year":          "2026",
		"Month":         "September",
		"Day of month":  "05",
		"Hour":          "09",
		"Minute":        "04",
		"Second":        "07",
		"Timezone":      "Asia/Jakarta (UTC+07:00)",
		"timestamp":     "2026-09-05T09:04:07.000+07:00",
	} {
		if got := item[field]; got != want {
			t.Errorf("%q = %#v, want %q", field, got, want)
		}
	}
	// KilasFlow's own additions, which n8n has no counterpart for.
	if item["scheduleId"] != "sched_1" {
		t.Errorf("scheduleId = %#v, want the row it fired from", item["scheduleId"])
	}
	if item["scheduledAt"] != "2026-09-05T02:04:07Z" {
		t.Errorf("scheduledAt = %#v, want the due instant in UTC", item["scheduledAt"])
	}
}
