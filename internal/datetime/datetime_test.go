package datetime_test

import (
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/datetime"
)

func TestFormatRendersLuxonTokens(t *testing.T) {
	t.Parallel()

	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}
	// A Saturday, in the 36th ISO week, day 248 of the year.
	instant := time.Date(2026, time.September, 5, 21, 4, 7, 123_000_000, jakarta)

	for layout, want := range map[string]string{
		"yyyy-MM-dd": "2026-09-05",
		"yy":         "26",
		"M/d/y":      "9/5/2026",
		"MMM":        "Sep",
		"MMMM":       "September",
		"ccc":        "Sat",
		"cccc":       "Saturday",
		"c":          "6",
		"HH:mm:ss":   "21:04:07",
		"h:mm a":     "9:04 PM",
		"hh":         "09",
		"SSS":        "123",
		"ZZ":         "+07:00",
		"Z":          "+0700",
		"ZZZZ":       "Asia/Jakarta",
		"ooo":        "248",
		"q":          "3",
		"WW":         "36",
		// A literal keeps its words rather than formatting them: without the
		// quotes, "at" would render as "AM" plus a stray t.
		"cccc 'at' HH:mm": "Saturday at 21:04",
		"''":              "'",
		// An unknown token comes out as written. A format with QQQ in it is a
		// bug someone can see; one that silently drops a field is one they find
		// in a report months later.
		"yyyy QQQ": "2026 QQQ",
	} {
		t.Run(layout, func(t *testing.T) {
			if got := datetime.Format(instant, layout); got != want {
				t.Errorf("Format(%q) = %q, want %q", layout, got, want)
			}
		})
	}
}

func TestOrdinalHandlesTheTeens(t *testing.T) {
	t.Parallel()

	for number, want := range map[int]string{
		1: "1st", 2: "2nd", 3: "3rd", 4: "4th",
		11: "11th", 12: "12th", 13: "13th",
		21: "21st", 22: "22nd", 23: "23rd", 31: "31st",
		101: "101st", 111: "111th", 112: "112th",
	} {
		if got := datetime.Ordinal(number); got != want {
			t.Errorf("Ordinal(%d) = %q, want %q", number, got, want)
		}
	}
}

func TestParseReadsWhatOtherPeoplesAPIsSend(t *testing.T) {
	t.Parallel()

	jakarta, err := time.LoadLocation("Asia/Jakarta")
	if err != nil {
		t.Skipf("the zone database is not available: %v", err)
	}

	for name, testCase := range map[string]struct {
		value any
		want  string
	}{
		"RFC 3339 keeps its own offset":   {value: "2026-09-05T00:00:00Z", want: "2026-09-05T00:00:00Z"},
		"an explicit offset is not moved": {value: "2026-09-05T09:00:00+09:00", want: "2026-09-05T00:00:00Z"},
		// The only reading available for a value with no offset is the zone the
		// node was configured with. Reading it as UTC would silently shift
		// every date a customer's API sends without one.
		"a zoneless timestamp is local to the zone": {value: "2026-09-05 09:00:00", want: "2026-09-05T02:00:00Z"},
		"a bare date is local to the zone":          {value: "2026-09-05", want: "2026-09-04T17:00:00Z"},
		"a slash date":                              {value: "2026/09/05", want: "2026-09-04T17:00:00Z"},
		"unix seconds":                              {value: float64(1_767_225_600), want: "2026-01-01T00:00:00Z"},
		"unix milliseconds":                         {value: float64(1_767_225_600_000), want: "2026-01-01T00:00:00Z"},
		"a time.Time passes through":                {value: time.Date(2026, 9, 5, 0, 0, 0, 0, time.UTC), want: "2026-09-05T00:00:00Z"},
	} {
		t.Run(name, func(t *testing.T) {
			got, err := datetime.Parse(testCase.value, jakarta)
			if err != nil {
				t.Fatalf("Parse(%#v) error = %v", testCase.value, err)
			}
			if got.UTC().Format(time.RFC3339) != testCase.want {
				t.Errorf("Parse(%#v) = %s, want %s", testCase.value, got.UTC().Format(time.RFC3339), testCase.want)
			}
		})
	}

	for name, value := range map[string]any{
		"empty":        "",
		"nil":          nil,
		"not a date":   "next Tuesday-ish",
		"a whole item": map[string]any{"date": "2026-09-05"},
	} {
		t.Run("refuses "+name, func(t *testing.T) {
			if _, err := datetime.Parse(value, jakarta); err == nil {
				t.Errorf("Parse(%#v) accepted a value that is not a date", value)
			}
		})
	}
}

func TestZoneRefusesWhatDependsOnTheServer(t *testing.T) {
	t.Parallel()

	if location, err := datetime.Zone(""); err != nil || location != time.UTC {
		t.Errorf("Zone(\"\") = (%v, %v), want UTC — never the server's local zone", location, err)
	}
	// "Local" is the one zone whose meaning changes with the deployment, so a
	// workflow using it would format differently on a laptop and in a container.
	if _, err := datetime.Zone("Local"); err == nil {
		t.Error("Zone(\"Local\") was accepted")
	}
	if _, err := datetime.Zone("Asia/Jakata"); err == nil {
		t.Error("Zone() accepted a misspelled zone")
	}
}
