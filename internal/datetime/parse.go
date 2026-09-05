package datetime

import (
	"fmt"
	"strings"
	"time"
)

// acceptedLayouts are the forms a value may arrive in, most specific first.
//
// A workflow's dates come from other people's APIs, so the list is what those
// APIs actually send rather than one canonical form. RFC 3339 is first because
// it is both the commonest and the only one of these that is unambiguous.
var acceptedLayouts = []string{
	time.RFC3339Nano,
	time.RFC3339,
	"2006-01-02T15:04:05",
	"2006-01-02 15:04:05.999999999 -0700 MST",
	"2006-01-02 15:04:05Z07:00",
	"2006-01-02 15:04:05",
	"2006-01-02 15:04",
	"2006-01-02",
	"2006/01/02",
	"02/01/2006",
	time.RFC1123Z,
	time.RFC1123,
	time.RFC822Z,
	time.RFC822,
	"15:04:05",
	"15:04",
}

// Zone resolves an IANA zone name.
//
// An empty name is UTC, never the server's local zone: a workflow that formats
// a date must not produce different text on a laptop and in a container, and
// "local" is the only zone whose meaning changes with the deployment.
func Zone(name string) (*time.Location, error) {
	trimmed := strings.TrimSpace(name)
	if trimmed == "" {
		return time.UTC, nil
	}
	if strings.EqualFold(trimmed, "local") {
		return nil, fmt.Errorf("timezone %q depends on where this server runs; name a zone such as UTC or Asia/Jakarta", name)
	}
	location, err := time.LoadLocation(trimmed)
	if err != nil {
		return nil, fmt.Errorf("timezone %q is not a known IANA zone name", name)
	}
	return location, nil
}

// Parse reads a value into an instant, interpreting a zoneless form in the
// given zone.
//
// A value carrying its own offset keeps it: "2026-03-01T00:00:00Z" is midnight
// UTC no matter what zone the node is configured with, because the sender
// already said which instant they meant. Only a value with no offset is read
// as local to the zone, which is the only reading available for one.
func Parse(value any, location *time.Location) (time.Time, error) {
	if location == nil {
		location = time.UTC
	}
	switch typed := value.(type) {
	case nil:
		return time.Time{}, fmt.Errorf("a date is required")
	case time.Time:
		return typed.In(location), nil
	case float64:
		return epoch(int64(typed), location), nil
	case int:
		return epoch(int64(typed), location), nil
	case int64:
		return epoch(typed, location), nil
	case string:
		return parseText(typed, location)
	default:
		return time.Time{}, fmt.Errorf("%v is not a date", value)
	}
}

// epoch reads a number as seconds or milliseconds since 1970.
//
// The two are told apart by magnitude, which is a heuristic — but the boundary
// used here is the year 2001 in milliseconds and the year 33658 in seconds, so
// the ambiguous band contains no date any workflow is about.
func epoch(number int64, location *time.Location) time.Time {
	const millisecondThreshold = 100_000_000_000
	if number > millisecondThreshold || number < -millisecondThreshold {
		return time.UnixMilli(number).In(location)
	}
	return time.Unix(number, 0).In(location)
}

func parseText(value string, location *time.Location) (time.Time, error) {
	trimmed := strings.TrimSpace(value)
	if trimmed == "" {
		return time.Time{}, fmt.Errorf("a date is required")
	}
	for _, layout := range acceptedLayouts {
		// ParseInLocation, not Parse: a layout with no offset would otherwise
		// be read as UTC regardless of the zone the node was configured with.
		if parsed, err := time.ParseInLocation(layout, trimmed, location); err == nil {
			return parsed, nil
		}
	}
	return time.Time{}, fmt.Errorf("%q is not a date this server can read", value)
}
