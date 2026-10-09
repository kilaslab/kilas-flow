// Package sheetsa1 holds the few pieces of A1 notation the Google Sheets node
// and its pickers both read, so the two cannot disagree about where a range
// starts or what a sheet name looks like.
package sheetsa1

import (
	"strconv"
	"strings"
)

// Quote is a tab title as the sheet part of an A1 range: single-quoted, with a
// quote inside the title doubled.
func Quote(title string) string {
	return "'" + strings.ReplaceAll(title, "'", "''") + "'"
}

// Region is the cell part of an A1 range: what follows the "!" that ends a sheet
// name. That "!" is the last one outside single quotes, because a tab can be
// called "a!b". A range with no sheet name comes back as it is.
func Region(text string) string {
	text = strings.TrimSpace(text)
	quoted, cut := false, -1
	for index := 0; index < len(text); index++ {
		switch text[index] {
		case '\'':
			quoted = !quoted
		case '!':
			if !quoted {
				cut = index
			}
		}
	}
	if cut < 0 {
		return text
	}
	return text[cut+1:]
}

// HeaderRange is the single row of a range that holds the column names, across
// the columns the range covers: B2:D with its header on row 2 is B2:D2. A range
// that names no columns is the whole row.
func HeaderRange(region string, row int) string {
	letters := func(cell string) string {
		cell = strings.TrimLeft(strings.TrimSpace(cell), "$")
		end := 0
		for end < len(cell) && (cell[end] >= 'A' && cell[end] <= 'Z' || cell[end] >= 'a' && cell[end] <= 'z') {
			end++
		}
		return strings.ToUpper(cell[:end])
	}
	first, last, _ := strings.Cut(region, ":")
	startColumn, endColumn := letters(first), letters(last)
	if endColumn == "" {
		endColumn = startColumn
	}
	if startColumn == "" {
		startColumn = endColumn
	}
	if startColumn == "" {
		return strconv.Itoa(row) + ":" + strconv.Itoa(row)
	}
	return startColumn + strconv.Itoa(row) + ":" + endColumn + strconv.Itoa(row)
}

// StartRow is the row a range begins on: 4 for C4:E7, 1 for A:F or anything it
// cannot read. The text is the cell part, without a sheet name.
func StartRow(region string) int {
	trimmed := strings.TrimLeft(strings.TrimSpace(region), "$")
	trimmed = strings.TrimLeft(trimmed, "ABCDEFGHIJKLMNOPQRSTUVWXYZabcdefghijklmnopqrstuvwxyz")
	trimmed = strings.TrimLeft(trimmed, "$")
	digits := ""
	for _, character := range trimmed {
		if character < '0' || character > '9' {
			break
		}
		digits += string(character)
	}
	row, err := strconv.Atoi(digits)
	if err != nil || row < 1 {
		return 1
	}
	return row
}
