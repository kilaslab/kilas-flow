package jsrun

import "testing"

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
