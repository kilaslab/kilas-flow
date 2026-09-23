package jsrun

import (
	"time"
	"unicode/utf8"
)

// ConsoleLine is one line a body printed with console.log, info, warn, error
// or debug.
type ConsoleLine struct {
	Level string
	Text  string
	At    time.Time
}

// consoleLevels are the levels a line may carry.
var consoleLevels = map[string]bool{"log": true, "info": true, "warn": true, "error": true, "debug": true}

// console keeps what a run printed, up to its limit. Past the limit the rest
// is dropped and the run is marked truncated, so a loop that logs every item
// cannot grow a trace row without bound.
type console struct {
	limit     int64
	used      int64
	lines     []ConsoleLine
	truncated bool
}

// write keeps one line and reports whether there is room for more.
func (c *console) write(level, text string) bool {
	if c.truncated {
		return false
	}
	if !consoleLevels[level] {
		level = "log"
	}
	size := int64(len(text)) + 1
	if c.used+size > c.limit {
		if room := c.limit - c.used - 1; room > 0 {
			c.lines = append(c.lines, ConsoleLine{Level: level, Text: truncateText(text, room), At: time.Now()})
		}
		c.used = c.limit
		c.truncated = true
		return false
	}
	c.used += size
	c.lines = append(c.lines, ConsoleLine{Level: level, Text: text, At: time.Now()})
	return true
}

// truncateText cuts text to at most size bytes without splitting a character.
func truncateText(text string, size int64) string {
	if int64(len(text)) <= size {
		return text
	}
	cut := int(size)
	for cut > 0 && !utf8.RuneStart(text[cut]) {
		cut--
	}
	return text[:cut]
}
