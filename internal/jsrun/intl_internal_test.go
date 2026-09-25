package jsrun

import (
	"bytes"
	"encoding/binary"
	"testing"
	"time"
)

// The hour width stylePatternPadsHour asks stylePattern for, over the whole
// cross-product of the three date locales, the -u-hc- extension a tag can
// carry, and the cycle an hourCycle/hour12 option can resolve to. Widths
// recorded from Node 24 with timeStyle: 'medium' at 05:45:30 UTC, an hour
// that shows the difference in either cycle family ("05:45:30" against
// "5:45:30"); the option sweep and the dates golden pin the strings
// themselves, while this pins the rule that decides them, which
// bestPattern's own matching would otherwise be free to re-pad back into
// agreement by accident (fix round 4, finding 1).
func TestTheStyleHourWidthRuleMatchesNode(t *testing.T) {
	for _, row := range []struct {
		locale, extension string
		// One digit per cycle, in the order h11, h12, h23, h24.
		widths string
	}{
		{"en-US", "", "1122"},
		{"en-US", "h11", "1122"},
		{"en-US", "h12", "1122"},
		{"en-US", "h23", "2222"},
		{"en-US", "h24", "2222"},
		{"en-CA", "", "1122"},
		{"en-CA", "h11", "1122"},
		{"en-CA", "h12", "1122"},
		{"en-CA", "h23", "2222"},
		{"en-CA", "h24", "2222"},
		{"en-GB", "", "2222"},
		{"en-GB", "h11", "1111"},
		{"en-GB", "h12", "1111"},
		{"en-GB", "h23", "2222"},
		{"en-GB", "h24", "2222"},
	} {
		locale := dateLocaleFor(row.locale)
		for index, cycle := range []string{"h11", "h12", "h23", "h24"} {
			want := row.widths[index] == '2'
			if got := stylePatternPadsHour(locale, row.extension, cycle); got != want {
				t.Errorf("%s%s at cycle %s: pads hour %v, want %v", row.locale, map[bool]string{true: "-u-hc-" + row.extension}[row.extension != ""], cycle, got, want)
			}
		}
	}
}

// A zone's name must not depend on which form of the tz database the host
// carries. Both forms describe the same offsets, and Node prints the same
// names on either, but they disagree about which side of the pair is the
// saving: Debian ships Europe/Dublin in the "vanguard" form, where Irish
// Standard Time is the standard offset and winter is an hour subtracted from
// it, while macOS and Go's embedded data ship the "rearguard" form, GMT
// standard with an hour added in summer. Taking the database's own daylight
// flag at face value swapped January's and July's names in the production
// image (BUG-a9d2hb), so this holds the same zone in both forms and asks for
// the names Node gives, which zones.json records as Dublin's.
func TestZoneNamesDoNotDependOnTheFormOfTheTzDatabase(t *testing.T) {
	gmtStandard := tzInterval{offset: 0, abbreviation: "GMT"}
	gmtSaving := tzInterval{offset: 0, daylight: true, abbreviation: "GMT"}
	irishStandard := tzInterval{offset: 3600, abbreviation: "IST"}
	irishSaving := tzInterval{offset: 3600, daylight: true, abbreviation: "IST"}
	january := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC)
	july := time.Date(2026, time.July, 15, 12, 0, 0, 0, time.UTC)
	for _, form := range []struct {
		name           string
		winter, summer tzInterval
	}{
		{"rearguard", gmtStandard, irishSaving},
		{"vanguard", gmtSaving, irishStandard},
	} {
		location, err := time.LoadLocationFromTZData("Europe/Dublin", dublinTZData(form.winter, form.summer))
		if err != nil {
			t.Fatalf("loading the %s form of Europe/Dublin: %v", form.name, err)
		}
		entry := zoneTable()["europe/dublin"]
		zone := &resolvedZone{canonical: entry.canonical, location: location, names: entry.names}
		for _, want := range []struct {
			at          time.Time
			style, name string
		}{
			{january, "short", "GMT"},
			{january, "long", "Greenwich Mean Time"},
			{july, "short", "GMT+1"},
			{july, "long", "Irish Standard Time"},
		} {
			got, err := zoneName(dateLocaleFor("en-US"), want.at.In(location), zone, want.style)
			if err != nil {
				t.Fatalf("%s form, %s %s: %v", form.name, want.at.Format("January"), want.style, err)
			}
			if got != want.name {
				t.Errorf("%s form, %s %s: got %q, want %q", form.name, want.at.Format("January"), want.style, got, want.name)
			}
		}
	}
}

// A zone that has settled on one offset keeps the standard name, even when
// the interval it left was flagged the other way and sat at a lower offset.
// Debian's Africa/Windhoek does that: since 2017 the clock has stayed at
// two hours ahead, which the file calls standard time, and the previous
// interval is a negative saving an hour lower. Reading that pair as daylight
// turned "Central Africa Time" into "GMT+02:00" (BUG-a9d2hb). Casablanca and
// El Aaiun carry no long or short name of their own, so they render as the
// offset either way; the golden covers those.
func TestASettledOffsetKeepsTheStandardName(t *testing.T) {
	location, err := time.LoadLocationFromTZData("Africa/Windhoek", settledTZData(
		tzInterval{offset: 3600, daylight: true, abbreviation: "WAT"},
		tzInterval{offset: 7200, abbreviation: "CAT"},
	))
	if err != nil {
		t.Fatalf("loading the settled form of Africa/Windhoek: %v", err)
	}
	entry := zoneTable()["africa/windhoek"]
	zone := &resolvedZone{canonical: entry.canonical, location: location, names: entry.names}
	at := time.Date(2026, time.January, 15, 12, 0, 0, 0, time.UTC).In(location)
	got, err := zoneName(dateLocaleFor("en-US"), at, zone, "long")
	if err != nil {
		t.Fatal(err)
	}
	if got != "Central Africa Time" {
		t.Errorf("got %q, want %q", got, "Central Africa Time")
	}
}

// tzInterval is one local time type of a tz database file: the offset it puts
// the clock at, whether the file calls that offset a daylight saving, and the
// abbreviation it prints.
type tzInterval struct {
	offset       int32
	daylight     bool
	abbreviation string
}

// dublinTZData encodes a version-1 tz database file for Europe/Dublin's
// offsets — the clock goes forward on the last Sunday of March and back on
// the last Sunday of October, both at 01:00 UTC — so that a test can load the
// zone in either form by varying which interval it hands in as the saving.
// Real files are used rather than the host's because the point is to have
// both forms on every host; only the years around the instants under test are
// covered, and there is no footer, so nothing outside them is meaningful.
func dublinTZData(winter, summer tzInterval) []byte {
	var transitions []int32
	var types []byte
	for year := 2020; year <= 2030; year++ {
		transitions = append(transitions, int32(lastSundayAtOne(year, time.March).Unix()), int32(lastSundayAtOne(year, time.October).Unix()))
		types = append(types, 1, 0)
	}
	return tzData(transitions, types, winter, summer)
}

// settledTZData encodes a zone that observed one saving and then stayed on
// the second interval for good: a transition into the saving, then one into
// the settled offset, and nothing after it. The second interval therefore
// has no end, which is the shape Africa/Windhoek has had since 2017.
func settledTZData(earlier, settled tzInterval) []byte {
	transitions := []int32{
		int32(time.Date(2016, time.April, 3, 1, 0, 0, 0, time.UTC).Unix()),
		int32(time.Date(2017, time.September, 3, 1, 0, 0, 0, time.UTC).Unix()),
	}
	return tzData(transitions, []byte{0, 1}, earlier, settled)
}

// tzData encodes a version-1 tz file with two local time types. transitions
// are the instants, in order, and types says which of the two types applies
// from each instant on.
func tzData(transitions []int32, types []byte, first, second tzInterval) []byte {
	abbreviations := &bytes.Buffer{}
	designations := make([]byte, 0, 2)
	for _, interval := range []tzInterval{first, second} {
		designations = append(designations, byte(abbreviations.Len()))
		abbreviations.WriteString(interval.abbreviation)
		abbreviations.WriteByte(0)
	}
	out := &bytes.Buffer{}
	out.WriteString("TZif")
	out.Write(make([]byte, 16))
	for _, count := range []int{0, 0, 0, len(transitions), 2, abbreviations.Len()} {
		_ = binary.Write(out, binary.BigEndian, uint32(count))
	}
	_ = binary.Write(out, binary.BigEndian, transitions)
	out.Write(types)
	for index, interval := range []tzInterval{first, second} {
		_ = binary.Write(out, binary.BigEndian, interval.offset)
		if interval.daylight {
			out.WriteByte(1)
		} else {
			out.WriteByte(0)
		}
		out.WriteByte(designations[index])
	}
	out.Write(abbreviations.Bytes())
	return out.Bytes()
}

// lastSundayAtOne is 01:00 UTC on the last Sunday of a month, when Europe's
// clocks change.
func lastSundayAtOne(year int, month time.Month) time.Time {
	last := time.Date(year, month+1, 1, 1, 0, 0, 0, time.UTC).AddDate(0, 0, -1)
	return last.AddDate(0, 0, -int(last.Weekday()))
}
