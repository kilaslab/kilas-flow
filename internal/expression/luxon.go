package expression

import (
	"encoding/json"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// dateValue is what `$now`, `$today` and `DateTime` produce.
//
// It is a distinct type so a date function can refuse a receiver that is not a
// date instead of re-parsing whatever it was given. Two things about it are
// load-bearing and were both wrong before:
//
//   - It never escapes the evaluator as this struct. A lone `{{ $now }}` is
//     converted to a time.Time, and nested in an object it marshals as an ISO
//     string, because the old unexported struct marshalled to `{}` — a Set node
//     wrote the literal text `{}` where n8n wrote a timestamp, and the DateTime
//     and IF nodes refused it as "not a date".
//   - It renders as ISO with milliseconds and an offset, which is the string
//     n8n produces (`2026-09-19T15:36:41.792+07:00`) and what an upstream API
//     accepts. Dropping the milliseconds made every round-tripped timestamp
//     compare unequal.
type dateValue struct{ at time.Time }

// isoMilliLayout is n8n's rendering of a DateTime: ISO 8601, milliseconds,
// numeric offset.
const isoMilliLayout = "2006-01-02T15:04:05.000-07:00"

func (value dateValue) String() string { return value.at.Format(isoMilliLayout) }

// MarshalJSON keeps a date that ends up inside an object or a list readable.
func (value dateValue) MarshalJSON() ([]byte, error) {
	return json.Marshal(value.String())
}

// property reads a Luxon getter: `$now.year`, `$now.weekday`.
//
// These are properties rather than method calls in Luxon, so `$now.year` has to
// work and `$now.year()` has to not.
func (value dateValue) property(name string) (any, error) {
	at := value.at
	switch name {
	case "year":
		return float64(at.Year()), nil
	case "month":
		return float64(at.Month()), nil
	case "day":
		return float64(at.Day()), nil
	case "hour":
		return float64(at.Hour()), nil
	case "minute":
		return float64(at.Minute()), nil
	case "second":
		return float64(at.Second()), nil
	case "millisecond":
		return float64(at.Nanosecond() / int(time.Millisecond)), nil
	case "weekday":
		// Luxon numbers Monday as 1 and Sunday as 7, where Go numbers Sunday 0.
		weekday := int(at.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return float64(weekday), nil
	case "zoneName":
		return at.Location().String(), nil
	case "isValid":
		return true, nil
	case "offset":
		_, seconds := at.Zone()
		return float64(seconds / 60), nil
	default:
		return Undefined, nil
	}
}

// luxonFormat renders a date with Luxon's format tokens rather than Go's.
//
// `$now.format('yyyy')` came back as the literal text `yyyy` because the pattern
// went straight to time.Format, and `$today.plus({days: 14}).format("d. MMM. y")`
// is a real imported expression. Echoing the pattern is the worst of the two
// possible behaviours: the workflow keeps running and writes the wrong value.
//
// The subset below is what the corpus uses; a token this does not know is
// emitted as written, and text inside single quotes or square brackets is
// literal, which is Luxon's own escaping.
func luxonFormat(at time.Time, pattern string) string {
	var builder strings.Builder
	index := 0
	for index < len(pattern) {
		char := pattern[index]
		if char == '\'' || char == '[' {
			closing := byte('\'')
			if char == '[' {
				closing = ']'
			}
			end := index + 1
			for end < len(pattern) && pattern[end] != closing {
				if pattern[end] == '\\' {
					end++
				}
				end++
			}
			builder.WriteString(strings.ReplaceAll(pattern[index+1:min(end, len(pattern))], "''", "'"))
			index = end + 1
			continue
		}
		if !isFormatLetter(char) {
			builder.WriteByte(char)
			index++
			continue
		}
		token := string(char)
		for index+len(token) < len(pattern) && pattern[index+len(token)] == char {
			token += string(char)
		}
		builder.WriteString(luxonToken(at, token))
		index += len(token)
	}
	return builder.String()
}

func isFormatLetter(char byte) bool {
	return (char >= 'a' && char <= 'z') || (char >= 'A' && char <= 'Z')
}

func luxonToken(at time.Time, token string) string {
	switch token {
	case "y", "yyyy":
		return strconv.Itoa(at.Year())
	case "yy":
		return fmt.Sprintf("%02d", at.Year()%100)
	case "M", "L":
		return strconv.Itoa(int(at.Month()))
	case "MM", "LL":
		return fmt.Sprintf("%02d", int(at.Month()))
	case "MMM", "LLL":
		return at.Format("Jan")
	case "MMMM", "LLLL":
		return at.Format("January")
	case "d":
		return strconv.Itoa(at.Day())
	case "dd":
		return fmt.Sprintf("%02d", at.Day())
	case "E", "c":
		weekday := int(at.Weekday())
		if weekday == 0 {
			weekday = 7
		}
		return strconv.Itoa(weekday)
	case "EEE", "ccc":
		return at.Format("Mon")
	case "EEEE", "cccc":
		return at.Format("Monday")
	case "H":
		return strconv.Itoa(at.Hour())
	case "HH":
		return fmt.Sprintf("%02d", at.Hour())
	case "h":
		return strconv.Itoa(hour12(at))
	case "hh":
		return fmt.Sprintf("%02d", hour12(at))
	case "m":
		return strconv.Itoa(at.Minute())
	case "mm":
		return fmt.Sprintf("%02d", at.Minute())
	case "s":
		return strconv.Itoa(at.Second())
	case "ss":
		return fmt.Sprintf("%02d", at.Second())
	case "S":
		return strconv.Itoa(at.Nanosecond() / int(100*time.Millisecond))
	case "SSS":
		return fmt.Sprintf("%03d", at.Nanosecond()/int(time.Millisecond))
	case "a":
		if at.Hour() < 12 {
			return "AM"
		}
		return "PM"
	case "Z":
		return at.Format("-07:00")
	case "ZZ":
		return at.Format("-0700")
	case "z":
		return at.Location().String()
	case "q":
		return strconv.Itoa((int(at.Month())-1)/3 + 1)
	case "D":
		return fmt.Sprintf("%d/%d/%d", at.Month(), at.Day(), at.Year())
	case "DD":
		return at.Format("January 2, 2006")
	case "t":
		return fmt.Sprintf("%d:%02d %s", hour12(at), at.Minute(), luxonToken(at, "a"))
	case "tt":
		return fmt.Sprintf("%d:%02d:%02d %s", hour12(at), at.Minute(), at.Second(), luxonToken(at, "a"))
	case "T":
		return fmt.Sprintf("%02d:%02d", at.Hour(), at.Minute())
	case "TT":
		return fmt.Sprintf("%02d:%02d:%02d", at.Hour(), at.Minute(), at.Second())
	default:
		return token
	}
}

func hour12(at time.Time) int {
	hour := at.Hour() % 12
	if hour == 0 {
		return 12
	}
	return hour
}

// luxonLayout translates the parseable subset of Luxon's tokens into Go's
// layout so `DateTime.fromFormat` can read a pattern a workflow was written
// with.
func luxonLayout(pattern string) string {
	var builder strings.Builder
	index := 0
	for index < len(pattern) {
		char := pattern[index]
		if char == '\'' || char == '[' {
			closing := byte('\'')
			if char == '[' {
				closing = ']'
			}
			end := index + 1
			for end < len(pattern) && pattern[end] != closing {
				end++
			}
			builder.WriteString(pattern[min(index+1, len(pattern)):min(end, len(pattern))])
			index = end + 1
			continue
		}
		if !isFormatLetter(char) {
			builder.WriteByte(char)
			index++
			continue
		}
		token := string(char)
		for index+len(token) < len(pattern) && pattern[index+len(token)] == char {
			token += string(char)
		}
		switch token {
		case "y", "yyyy":
			builder.WriteString("2006")
		case "yy":
			builder.WriteString("06")
		case "M", "L":
			builder.WriteString("1")
		case "MM", "LL":
			builder.WriteString("01")
		case "MMM", "LLL":
			builder.WriteString("Jan")
		case "MMMM", "LLLL":
			builder.WriteString("January")
		case "d":
			builder.WriteString("2")
		case "dd":
			builder.WriteString("02")
		case "H":
			builder.WriteString("15")
		case "HH":
			builder.WriteString("15")
		case "h":
			builder.WriteString("3")
		case "hh":
			builder.WriteString("03")
		case "m":
			builder.WriteString("4")
		case "mm":
			builder.WriteString("04")
		case "s":
			builder.WriteString("5")
		case "ss":
			builder.WriteString("05")
		case "SSS":
			builder.WriteString(".000")
		case "a":
			builder.WriteString("PM")
		case "Z":
			builder.WriteString("-07:00")
		case "ZZ":
			builder.WriteString("-0700")
		default:
			builder.WriteString(token)
		}
		index += len(token)
	}
	return builder.String()
}

func startOfDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}

// parseDate reads the value types a workflow hands to a date function: an ISO
// string, epoch milliseconds, another date, or the item's own time value.
func parseDate(value any) (dateValue, error) {
	switch typed := value.(type) {
	case dateValue:
		return typed, nil
	case time.Time:
		return dateValue{at: typed}, nil
	case float64:
		return dateValue{at: time.UnixMilli(int64(typed)).UTC()}, nil
	case string:
		parsed, err := time.Parse(time.RFC3339, strings.TrimSpace(typed))
		if err != nil {
			parsed, err = time.Parse("2006-01-02", strings.TrimSpace(typed))
			if err != nil {
				return dateValue{}, fmt.Errorf("%q is not a date", typed)
			}
		}
		return dateValue{at: parsed}, nil
	default:
		return dateValue{}, fmt.Errorf("%s is not a date", describeValue(value))
	}
}
