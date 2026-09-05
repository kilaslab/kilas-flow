package datetime

import (
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Format renders an instant using Luxon's token vocabulary.
//
// Luxon rather than Go's reference-date layout because that is what a workflow
// author writes: n8n's Date & Time node takes Luxon strings, and an imported
// workflow carries them verbatim. Translating here means an import does not
// have to rewrite anything, and a format that is wrong is wrong in the same way
// it was wrong in n8n.
//
// Text inside single quotes is emitted literally, as Luxon does, so a format
// can contain a word that would otherwise be read as tokens.
func Format(instant time.Time, layout string) string {
	var out strings.Builder
	for index := 0; index < len(layout); {
		if layout[index] == '\'' {
			literal, width := quotedLiteral(layout[index:])
			out.WriteString(literal)
			index += width
			continue
		}
		token, width := nextToken(layout[index:])
		if width == 0 {
			out.WriteByte(layout[index])
			index++
			continue
		}
		out.WriteString(renderToken(instant, token))
		index += width
	}
	return out.String()
}

// quotedLiteral reads a '…' run, returning its contents and how much of the
// layout it consumed. Luxon spells a literal quote as ” inside a literal.
func quotedLiteral(layout string) (string, int) {
	if len(layout) > 1 && layout[1] == '\'' {
		return "'", 2
	}
	end := strings.IndexByte(layout[1:], '\'')
	if end < 0 {
		// Unterminated: the rest of the layout is literal, which is a kinder
		// answer than emitting a stray quote and then formatting the words.
		return layout[1:], len(layout)
	}
	return layout[1 : 1+end], end + 2
}

// nextToken reads the longest run of one repeated letter, which is how Luxon's
// grammar works: `M`, `MM`, `MMM` and `MMMM` are four different tokens.
func nextToken(layout string) (string, int) {
	first := layout[0]
	if !isTokenLetter(first) {
		return "", 0
	}
	width := 1
	for width < len(layout) && layout[width] == first {
		width++
	}
	return layout[:width], width
}

func isTokenLetter(character byte) bool {
	return (character >= 'a' && character <= 'z') || (character >= 'A' && character <= 'Z')
}

func renderToken(instant time.Time, token string) string {
	switch token {
	case "yyyy", "y":
		return strconv.Itoa(instant.Year())
	case "yy":
		return fmt.Sprintf("%02d", instant.Year()%100)
	case "M":
		return strconv.Itoa(int(instant.Month()))
	case "MM":
		return fmt.Sprintf("%02d", int(instant.Month()))
	case "MMM":
		return instant.Format("Jan")
	case "MMMM":
		return instant.Format("January")
	case "d":
		return strconv.Itoa(instant.Day())
	case "dd":
		return fmt.Sprintf("%02d", instant.Day())
	case "c", "E":
		// Luxon numbers the ISO week from Monday as 1; Go's Weekday puts
		// Sunday at 0, which is the same list rotated.
		return strconv.Itoa(isoWeekday(instant))
	case "ccc", "EEE":
		return instant.Format("Mon")
	case "cccc", "EEEE":
		return instant.Format("Monday")
	case "H":
		return strconv.Itoa(instant.Hour())
	case "HH":
		return fmt.Sprintf("%02d", instant.Hour())
	case "h":
		return strconv.Itoa(hour12(instant))
	case "hh":
		return fmt.Sprintf("%02d", hour12(instant))
	case "m":
		return strconv.Itoa(instant.Minute())
	case "mm":
		return fmt.Sprintf("%02d", instant.Minute())
	case "s":
		return strconv.Itoa(instant.Second())
	case "ss":
		return fmt.Sprintf("%02d", instant.Second())
	case "S":
		return strconv.Itoa(instant.Nanosecond() / int(time.Millisecond))
	case "SSS":
		return fmt.Sprintf("%03d", instant.Nanosecond()/int(time.Millisecond))
	case "a":
		if instant.Hour() < 12 {
			return "AM"
		}
		return "PM"
	case "Z":
		return offsetLabel(instant, "")
	case "ZZ":
		return offsetLabel(instant, ":")
	case "ZZZ":
		return instant.Format("MST")
	case "ZZZZ":
		return instant.Location().String()
	case "o":
		return strconv.Itoa(instant.YearDay())
	case "ooo":
		return fmt.Sprintf("%03d", instant.YearDay())
	case "q":
		return strconv.Itoa((int(instant.Month())-1)/3 + 1)
	case "W":
		_, week := instant.ISOWeek()
		return strconv.Itoa(week)
	case "WW":
		_, week := instant.ISOWeek()
		return fmt.Sprintf("%02d", week)
	default:
		// An unknown token is emitted as written rather than dropped. A format
		// that comes out with `QQQ` in it is a bug someone can see; one that
		// silently loses a field is a bug they find in a report months later.
		return token
	}
}

// isoWeekday numbers Monday 1 through Sunday 7.
func isoWeekday(instant time.Time) int {
	if day := int(instant.Weekday()); day != 0 {
		return day
	}
	return 7
}

func hour12(instant time.Time) int {
	hour := instant.Hour() % 12
	if hour == 0 {
		return 12
	}
	return hour
}

// offsetLabel renders the zone offset as +07:00 or +0700.
func offsetLabel(instant time.Time, separator string) string {
	_, offset := instant.Zone()
	sign := "+"
	if offset < 0 {
		sign, offset = "-", -offset
	}
	return fmt.Sprintf("%s%02d%s%02d", sign, offset/3600, separator, (offset%3600)/60)
}

// Ordinal renders 1st, 2nd, 3rd, 4th — the `Do` in the readable date a
// Schedule Trigger emits, which Luxon has no token for.
func Ordinal(number int) string {
	suffix := "th"
	// 11th, 12th and 13th are the exceptions the naive rule gets wrong.
	if number%100 < 11 || number%100 > 13 {
		switch number % 10 {
		case 1:
			suffix = "st"
		case 2:
			suffix = "nd"
		case 3:
			suffix = "rd"
		}
	}
	return strconv.Itoa(number) + suffix
}
