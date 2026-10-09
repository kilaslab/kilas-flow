package nodes_test

import (
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/nodes"
)

// fakeSheets is an in-memory Google Sheets: enough of the v4 API for the node's
// reads and writes to be judged by what is left in the grid, not only by which
// requests were sent. It keeps Google's observable habits that the node has to
// cope with: values come back without trailing empty cells or rows, a write
// outside the grid is refused, and an append lands after the first table.
type fakeSheets struct {
	t      *testing.T
	server *httptest.Server

	mu    sync.Mutex
	docID string
	tabs  []*fakeTab
	calls []fakeCall
	// failWith, when set, answers every call with this status.
	failWith int
	// failOnly, when set, narrows failWith to the calls whose "METHOD path"
	// matches it.
	failOnly *regexp.Regexp
}

type fakeTab struct {
	gid      int
	title    string
	rows     [][]any
	rowCount int
	colCount int
}

// fakeFormatted is a cell with a number format, such as a date: unformatted it
// is the serial number the sheet holds, formatted it is the text it shows.
type fakeFormatted struct {
	Raw   any
	Shown string
}

type fakeCall struct {
	Method string
	Path   string // decoded
	Query  url.Values
	Body   map[string]any
}

func (call fakeCall) String() string { return call.Method + " " + call.Path }

// newFakeSheets starts the stub and aims the node at it. The root is restored
// when the test ends.
func newFakeSheets(t *testing.T, docID string) *fakeSheets {
	t.Helper()
	fake := &fakeSheets{t: t, docID: docID}
	fake.server = httptest.NewServer(http.HandlerFunc(fake.serve))
	t.Cleanup(fake.server.Close)
	t.Cleanup(nodes.SetGoogleSheetsAPIRootForTest(fake.server.URL + "/v4"))
	return fake
}

// addTab adds a tab holding rows, with a grid exactly as large as the data
// plus slack extra rows.
func (fake *fakeSheets) addTab(gid int, title string, slack int, rows ...[]any) *fakeTab {
	tab := &fakeTab{gid: gid, title: title, rows: rows, colCount: 26}
	tab.rowCount = len(rows) + slack
	fake.tabs = append(fake.tabs, tab)
	return tab
}

// snapshot is a copy of one tab's data, trailing empties trimmed.
func (fake *fakeSheets) snapshot(title string) [][]any {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	for _, tab := range fake.tabs {
		if tab.title == title {
			return trimRows(tab.rows, false)
		}
	}
	fake.t.Fatalf("no tab %q", title)
	return nil
}

func (fake *fakeSheets) tab(title string) *fakeTab {
	for _, tab := range fake.tabs {
		if tab.title == title {
			return tab
		}
	}
	return nil
}

func (fake *fakeSheets) callsMatching(pattern string) []fakeCall {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	matcher := regexp.MustCompile(pattern)
	var found []fakeCall
	for _, call := range fake.calls {
		if matcher.MatchString(call.String()) {
			found = append(found, call)
		}
	}
	return found
}

func (fake *fakeSheets) callLog() string {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	lines := make([]string, 0, len(fake.calls))
	for _, call := range fake.calls {
		lines = append(lines, call.String())
	}
	return strings.Join(lines, "\n")
}

// endpoint is the host:port the egress policy must be told about.
func (fake *fakeSheets) endpoint() string {
	parsed, err := url.Parse(fake.server.URL)
	if err != nil {
		fake.t.Fatal(err)
	}
	return parsed.Host
}

// sheetsPolicy is the default egress policy with exactly one loopback endpoint
// granted. It never turns allow_private_networks on, so a request to any other
// loopback address is still refused.
func sheetsPolicy(t *testing.T, endpoint string) safehttp.Policy {
	t.Helper()
	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks, which would make these tests prove nothing")
	}
	if _, _, err := net.SplitHostPort(endpoint); err != nil {
		t.Fatalf("endpoint %q: %v", endpoint, err)
	}
	policy.AllowedPrivateEndpoints = []string{endpoint}
	return policy
}

var (
	valuesPath = regexp.MustCompile(`^/v4/spreadsheets/([^/]+)/values/(.+)$`)
	a1Pattern  = regexp.MustCompile(`^([A-Za-z]*)([0-9]*)(?::([A-Za-z]*)([0-9]*))?$`)
)

func (fake *fakeSheets) serve(w http.ResponseWriter, r *http.Request) {
	fake.mu.Lock()
	defer fake.mu.Unlock()

	var body map[string]any
	if raw, _ := io.ReadAll(r.Body); len(raw) > 0 {
		_ = json.Unmarshal(raw, &body)
	}
	fake.calls = append(fake.calls, fakeCall{Method: r.Method, Path: r.URL.Path, Query: r.URL.Query(), Body: body})

	if got := r.Header.Get("Authorization"); got != "Bearer token" {
		fake.t.Errorf("Authorization = %q, want the credential's bearer token", got)
	}
	if fake.failWith != 0 && (fake.failOnly == nil || fake.failOnly.MatchString(r.Method+" "+r.URL.Path)) {
		http.Error(w, `{"error":{"message":"stub failure"}}`, fake.failWith)
		return
	}

	path := r.URL.Path
	switch {
	case r.Method == http.MethodGet && path == "/v4/spreadsheets/"+fake.docID:
		fake.writeJSON(w, fake.metadata())
	case r.Method == http.MethodPost && path == "/v4/spreadsheets/"+fake.docID+":batchUpdate":
		if err := fake.structuralUpdate(body); err != nil {
			fake.fail(w, http.StatusBadRequest, err)
			return
		}
		fake.writeJSON(w, map[string]any{"spreadsheetId": fake.docID})
	case r.Method == http.MethodPost && path == "/v4/spreadsheets/"+fake.docID+"/values:batchUpdate":
		if err := fake.valuesBatchUpdate(body); err != nil {
			fake.fail(w, http.StatusBadRequest, err)
			return
		}
		fake.writeJSON(w, map[string]any{"spreadsheetId": fake.docID})
	case valuesPath.MatchString(path):
		match := valuesPath.FindStringSubmatch(path)
		if match[1] != fake.docID {
			fake.fail(w, http.StatusNotFound, fmt.Errorf("no spreadsheet %q", match[1]))
			return
		}
		fake.values(w, r, match[2], body)
	default:
		fake.fail(w, http.StatusNotFound, fmt.Errorf("unexpected request %s %s", r.Method, path))
	}
}

func (fake *fakeSheets) fail(w http.ResponseWriter, status int, err error) {
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"error": map[string]any{"message": err.Error()}})
}

func (fake *fakeSheets) writeJSON(w http.ResponseWriter, payload any) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(payload)
}

func (fake *fakeSheets) metadata() map[string]any {
	sheets := make([]any, 0, len(fake.tabs))
	for _, tab := range fake.tabs {
		sheets = append(sheets, map[string]any{"properties": map[string]any{
			"sheetId": tab.gid, "title": tab.title,
			"gridProperties": map[string]any{"rowCount": tab.rowCount, "columnCount": tab.colCount},
		}})
	}
	return map[string]any{"sheets": sheets}
}

// a1Range is a parsed range: 1-based, inclusive, zero meaning open.
type a1Range struct {
	tab                    *fakeTab
	row1, col1, row2, col2 int
}

func columnNumber(letters string) int {
	number := 0
	for _, letter := range strings.ToUpper(letters) {
		number = number*26 + int(letter-'A'+1)
	}
	return number
}

func (fake *fakeSheets) parseRange(text string) (a1Range, error) {
	title, cells := text, ""
	if strings.HasPrefix(text, "'") {
		end := -1
		for index := 1; index < len(text); index++ {
			if text[index] != '\'' {
				continue
			}
			if index+1 < len(text) && text[index+1] == '\'' {
				index++
				continue
			}
			end = index
			break
		}
		if end < 0 {
			return a1Range{}, fmt.Errorf("unterminated quote in %q", text)
		}
		title = strings.ReplaceAll(text[1:end], "''", "'")
		cells = strings.TrimPrefix(text[end+1:], "!")
	} else if before, after, found := strings.Cut(text, "!"); found {
		title, cells = before, after
	}
	tab := fake.tab(title)
	if tab == nil {
		return a1Range{}, fmt.Errorf("Unable to parse range: %s", text)
	}
	parsed := a1Range{tab: tab}
	if cells == "" {
		return parsed, nil
	}
	match := a1Pattern.FindStringSubmatch(cells)
	if match == nil {
		return a1Range{}, fmt.Errorf("Unable to parse range: %s", text)
	}
	parsed.col1, parsed.row1 = columnNumber(match[1]), atoiOr0(match[2])
	if match[3] != "" || match[4] != "" {
		parsed.col2, parsed.row2 = columnNumber(match[3]), atoiOr0(match[4])
	} else {
		parsed.col2, parsed.row2 = parsed.col1, parsed.row1
	}
	return parsed, nil
}

func atoiOr0(text string) int {
	number, _ := strconv.Atoi(text)
	return number
}

// trimRows drops trailing empty cells from each row and trailing empty rows,
// as the real API does.
func trimRows(rows [][]any, keepLeadingEmpty bool) [][]any {
	trimmed := make([][]any, len(rows))
	for index, row := range rows {
		end := len(row)
		for end > 0 && (row[end-1] == nil || row[end-1] == "") {
			end--
		}
		trimmed[index] = make([]any, end)
		for position, cell := range row[:end] {
			if cell == nil {
				cell = "" // a hole inside a row reads back as empty text
			}
			trimmed[index][position] = cell
		}
	}
	for len(trimmed) > 0 && len(trimmed[len(trimmed)-1]) == 0 {
		trimmed = trimmed[:len(trimmed)-1]
	}
	return trimmed
}

func (fake *fakeSheets) values(w http.ResponseWriter, r *http.Request, encodedRange string, body map[string]any) {
	rangeText := encodedRange
	suffix := ""
	for _, candidate := range []string{":append", ":clear"} {
		if strings.HasSuffix(rangeText, candidate) {
			suffix = candidate
			rangeText = strings.TrimSuffix(rangeText, candidate)
		}
	}
	parsed, err := fake.parseRange(rangeText)
	if err != nil {
		fake.fail(w, http.StatusBadRequest, err)
		return
	}
	switch {
	case r.Method == http.MethodGet && suffix == "":
		window := fake.window(parsed, r.URL.Query().Get("valueRenderOption"))
		payload := map[string]any{"range": rangeText, "majorDimension": "ROWS"}
		if len(window) > 0 {
			payload["values"] = window
		}
		fake.writeJSON(w, payload)
	case r.Method == http.MethodPut && suffix == "":
		if err := fake.write(parsed, body["values"]); err != nil {
			fake.fail(w, http.StatusBadRequest, err)
			return
		}
		fake.writeJSON(w, map[string]any{"updatedRange": rangeText})
	case r.Method == http.MethodPost && suffix == ":append":
		tab := parsed.tab
		last := len(trimRows(tab.rows, false))
		parsed.row1 = last + 1
		if parsed.col1 == 0 {
			parsed.col1 = 1
		}
		if r.URL.Query().Get("insertDataOption") != "INSERT_ROWS" {
			fake.t.Errorf("append insertDataOption = %q, want INSERT_ROWS", r.URL.Query().Get("insertDataOption"))
		}
		if err := fake.write(parsed, body["values"]); err != nil {
			fake.fail(w, http.StatusBadRequest, err)
			return
		}
		fake.writeJSON(w, map[string]any{"updates": map[string]any{"updatedRange": rangeText}})
	case r.Method == http.MethodPost && suffix == ":clear":
		fake.clear(parsed)
		fake.writeJSON(w, map[string]any{"clearedRange": rangeText})
	default:
		fake.fail(w, http.StatusBadRequest, fmt.Errorf("unsupported %s on %s", r.Method, encodedRange))
	}
}

// window returns the cells a range covers, rendered as asked.
func (fake *fakeSheets) window(parsed a1Range, render string) [][]any {
	rows := parsed.tab.rows
	startRow, startCol := max(parsed.row1, 1), max(parsed.col1, 1)
	endRow := parsed.row2
	if endRow == 0 || endRow > len(rows) {
		endRow = len(rows)
	}
	window := make([][]any, 0, endRow)
	for rowNumber := startRow; rowNumber <= endRow; rowNumber++ {
		var out []any
		row := rows[rowNumber-1]
		endCol := parsed.col2
		if endCol == 0 || endCol > len(row) {
			endCol = len(row)
		}
		for colNumber := startCol; colNumber <= endCol; colNumber++ {
			cell := row[colNumber-1]
			if styled, ok := cell.(fakeFormatted); ok {
				// A cell with a number format: the sheet shows one thing and holds another.
				if render == "FORMATTED_VALUE" {
					cell = styled.Shown
				} else {
					cell = styled.Raw
				}
			}
			if render == "FORMATTED_VALUE" {
				switch typed := cell.(type) {
				case float64:
					cell = strconv.FormatFloat(typed, 'f', -1, 64)
				case bool:
					cell = strconv.FormatBool(typed)
				case nil:
					cell = ""
				}
			}
			if cell == nil {
				cell = ""
			}
			out = append(out, cell)
		}
		window = append(window, out)
	}
	return trimRows(window, true)
}

// write puts a matrix at the range's top-left cell, refusing a write outside
// the grid the way the real API does.
func (fake *fakeSheets) write(parsed a1Range, raw any) error {
	matrix, _ := raw.([]any)
	startRow, startCol := max(parsed.row1, 1), max(parsed.col1, 1)
	tab := parsed.tab
	if startRow+len(matrix)-1 > tab.rowCount {
		return fmt.Errorf("Range (%s) exceeds grid limits. Max rows: %d", tab.title, tab.rowCount)
	}
	for rowOffset, rawRow := range matrix {
		cells, _ := rawRow.([]any)
		if startCol+len(cells)-1 > tab.colCount {
			return fmt.Errorf("Range (%s) exceeds grid limits. Max columns: %d", tab.title, tab.colCount)
		}
		for len(tab.rows) < startRow+rowOffset {
			tab.rows = append(tab.rows, nil)
		}
		row := tab.rows[startRow+rowOffset-1]
		for len(row) < startCol+len(cells)-1 {
			row = append(row, nil)
		}
		for colOffset, cell := range cells {
			if cell == nil {
				continue // null leaves the cell alone
			}
			row[startCol+colOffset-1] = cell
		}
		tab.rows[startRow+rowOffset-1] = row
	}
	return nil
}

func (fake *fakeSheets) clear(parsed a1Range) {
	rows := parsed.tab.rows
	startRow, startCol := max(parsed.row1, 1), max(parsed.col1, 1)
	endRow := parsed.row2
	if endRow == 0 || endRow > len(rows) {
		endRow = len(rows)
	}
	for rowNumber := startRow; rowNumber <= endRow; rowNumber++ {
		row := rows[rowNumber-1]
		endCol := parsed.col2
		if endCol == 0 || endCol > len(row) {
			endCol = len(row)
		}
		for colNumber := startCol; colNumber <= endCol; colNumber++ {
			row[colNumber-1] = nil
		}
	}
}

func (fake *fakeSheets) valuesBatchUpdate(body map[string]any) error {
	data, _ := body["data"].([]any)
	for _, raw := range data {
		entry, _ := raw.(map[string]any)
		rangeText, _ := entry["range"].(string)
		parsed, err := fake.parseRange(rangeText)
		if err != nil {
			return err
		}
		if err := fake.write(parsed, entry["values"]); err != nil {
			return err
		}
	}
	return nil
}

func (fake *fakeSheets) structuralUpdate(body map[string]any) error {
	requests, _ := body["requests"].([]any)
	for _, raw := range requests {
		request, _ := raw.(map[string]any)
		if appendDimension, ok := request["appendDimension"].(map[string]any); ok {
			tab := fake.tabByGid(appendDimension["sheetId"])
			if tab == nil {
				return fmt.Errorf("no sheet %v", appendDimension["sheetId"])
			}
			length := int(appendDimension["length"].(float64))
			if appendDimension["dimension"] == "ROWS" {
				tab.rowCount += length
			} else {
				tab.colCount += length
			}
			continue
		}
		if deleteDimension, ok := request["deleteDimension"].(map[string]any); ok {
			span, _ := deleteDimension["range"].(map[string]any)
			tab := fake.tabByGid(span["sheetId"])
			if tab == nil {
				return fmt.Errorf("no sheet %v", span["sheetId"])
			}
			start, end := int(span["startIndex"].(float64)), int(span["endIndex"].(float64))
			if span["dimension"] == "ROWS" {
				if start < len(tab.rows) {
					cut := min(end, len(tab.rows))
					tab.rows = append(tab.rows[:start], tab.rows[cut:]...)
				}
				tab.rowCount -= end - start
			} else {
				for index, row := range tab.rows {
					if start < len(row) {
						cut := min(end, len(row))
						tab.rows[index] = append(row[:start:start], row[cut:]...)
					}
				}
				tab.colCount -= end - start
			}
			continue
		}
		return fmt.Errorf("unsupported structural request %v", request)
	}
	return nil
}

func (fake *fakeSheets) tabByGid(raw any) *fakeTab {
	gid, ok := raw.(float64)
	if !ok {
		return nil
	}
	for _, tab := range fake.tabs {
		if tab.gid == int(gid) {
			return tab
		}
	}
	return nil
}
