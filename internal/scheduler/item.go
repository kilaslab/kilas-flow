package scheduler

import (
	"fmt"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/datetime"
)

// TriggerItem is what a scheduled run starts with.
//
// The field names are n8n's, exactly — spaces, capitals and all — because
// imported workflows read them: an expression saying `$json['Day of week']` has
// to resolve, and a corpus template that filters on `Hour` has to see the
// string "09" rather than the number 9. Every value is a string for the same
// reason; that is what moment's format returns and what those expressions are
// written against.
//
// Two fields are KilasFlow's own and have no n8n counterpart: scheduleId, which
// is the only way to tell which of a node's several intervals fired, and
// scheduledAt, which is the due instant rather than the moment the run began.
// They are additions to n8n's set rather than replacements, so nothing an
// imported workflow reads is missing or renamed.
func TriggerItem(scheduleID string, dueAt time.Time, zone string) map[string]any {
	location, err := datetime.Zone(zone)
	if err != nil {
		// A stored zone that no longer resolves is not worth failing a run
		// over: the schedule already fired, and UTC is a wrong label rather
		// than a wrong time.
		location = time.UTC
	}
	local := dueAt.In(location)

	return map[string]any{
		"timestamp":     local.Format("2006-01-02T15:04:05.000-07:00"),
		"Readable date": readableDate(local),
		"Readable time": readableTime(local),
		"Day of week":   datetime.Format(local, "cccc"),
		"Year":          datetime.Format(local, "yyyy"),
		"Month":         datetime.Format(local, "MMMM"),
		"Day of month":  datetime.Format(local, "dd"),
		"Hour":          datetime.Format(local, "HH"),
		"Minute":        datetime.Format(local, "mm"),
		"Second":        datetime.Format(local, "ss"),
		"Timezone":      zoneLabel(local, location),

		"scheduleId":  scheduleID,
		"scheduledAt": local.UTC().Format(time.RFC3339),
	}
}

// readableDate is moment's `MMMM Do YYYY, h:mm:ss a`, which has an ordinal day
// and a lowercase meridiem — neither of which Luxon's vocabulary can spell.
func readableDate(local time.Time) string {
	return fmt.Sprintf("%s %s %s, %s",
		datetime.Format(local, "MMMM"),
		datetime.Ordinal(local.Day()),
		datetime.Format(local, "yyyy"),
		readableTime(local))
}

// readableTime is moment's `h:mm:ss a`.
func readableTime(local time.Time) string {
	return strings.ToLower(datetime.Format(local, "h:mm:ss a"))
}

// zoneLabel is n8n's `Area/City (UTC+07:00)`.
func zoneLabel(local time.Time, location *time.Location) string {
	return fmt.Sprintf("%s (UTC%s)", location.String(), datetime.Format(local, "ZZ"))
}
