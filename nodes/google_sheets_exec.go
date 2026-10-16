package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sheetsa1"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// GoogleSheetsExecutor runs the Google Sheets node's six operations.
//
// One Execute call is one batch against one spreadsheet and one tab: the
// document and the tab are read from the first item, as n8n's node does, while
// filters, mapped values and delete or clear spans are resolved per item. The
// sheet is read once per batch and the writes are sent as one request each,
// because Google allows about sixty reads a minute per user and a node that
// read the sheet once per item would spend that on a hundred rows.
type GoogleSheetsExecutor struct {
	google *GoogleClient
}

// NewGoogleSheetsExecutor builds the Sheets action node over the shared Google
// HTTP client, so it is held to the same egress policy as Drive and Gmail.
func NewGoogleSheetsExecutor(policy safehttp.Policy) *GoogleSheetsExecutor {
	return &GoogleSheetsExecutor{google: NewGoogleClient(policy)}
}

// Execute runs the whole batch as one operation, and so it is WholeBatch: the
// runner never splits it into one-item calls to resolve a tolerated failure,
// which would read the sheet again for every item. n8n's node does the same —
// the whole operation sits inside one try, and a failure under Continue on
// fail is a single error item for the node — so a failure here, when failures
// are tolerated, is a BatchFailure and not one error per input item.
func (executor *GoogleSheetsExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	output, err := executor.execute(ctx, ir, input, request)
	if err != nil && request.TolerateItemFailures && ctx.Err() == nil {
		return nil, &engine.BatchFailure{Err: err}
	}
	return output, err
}

func (executor *GoogleSheetsExecutor) execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		return workflow.NodeOutput{nil}, nil
	}
	resolved := make([]workflow.IRNode, len(items))
	for index, item := range items {
		node, err := resolveGoogleIR(ir, item, input, request, index)
		if err != nil {
			return nil, err
		}
		resolved[index] = node
	}
	first := resolved[0].Parameters
	operation := textValue(first["operation"], SheetsOperationRead)
	switch operation {
	case SheetsOperationRead, SheetsOperationAppend, SheetsOperationAppendOrUpdate,
		SheetsOperationUpdate, SheetsOperationDelete, SheetsOperationClear:
	default:
		return nil, fmt.Errorf("node %q: unsupported google sheets operation %q", ir.Name, operation)
	}
	if authentication := textValue(first["authentication"], "oAuth2"); authentication != "oAuth2" {
		return nil, fmt.Errorf("node %q: authentication %q is not supported, only OAuth2", ir.Name, authentication)
	}
	if request.FromAIArguments != nil {
		// Run as an agent's tool form: refused before anything is sent to Google.
		if err := refuseSheetsToolRun(operation, ir.Parameters, resolved); err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
	}

	run := &sheetsRun{executor: executor, ctx: ctx, request: request, ir: ir, cache: map[string][][]any{}}
	docID, err := sheetsDocumentID(first["documentId"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	run.docID = docID
	ref, err := sheetsSheetRef(first["sheetName"])
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	tab, err := run.findTab(ref)
	if err != nil {
		return nil, err
	}

	var produced []workflow.Item
	switch operation {
	case SheetsOperationRead:
		produced, err = run.readAll(items, resolved, tab)
	case SheetsOperationAppend:
		produced, err = run.appendRows(items, resolved, tab)
	case SheetsOperationUpdate:
		produced, err = run.updateRows(items, resolved, tab, false)
	case SheetsOperationAppendOrUpdate:
		produced, err = run.updateRows(items, resolved, tab, true)
	case SheetsOperationDelete:
		produced, err = run.deleteSpans(items, resolved, tab)
	case SheetsOperationClear:
		produced, err = run.clearSpans(items, resolved, tab)
	}
	if err != nil {
		return nil, err
	}
	return workflow.NodeOutput{produced}, nil
}

// sheetsRun is the state of one Execute call.
type sheetsRun struct {
	executor *GoogleSheetsExecutor
	ctx      context.Context
	request  engine.Request
	ir       workflow.IRNode
	docID    string
	// credentialType is the credential the calls are signed with: the node's,
	// or the trigger's own.
	credentialType string
	tabs           []sheetsTab
	// cache holds values already read in this call, keyed by range and render
	// options. Every write empties it, so a read after a write is never stale.
	cache map[string][][]any
}

// sheetsTab is one tab of the spreadsheet, as Google describes it.
type sheetsTab struct {
	gid      int64
	title    string
	rowCount int
	colCount int
}

func (run *sheetsRun) errorf(format string, args ...any) error {
	return fmt.Errorf("node %q: %s", run.ir.Name, fmt.Sprintf(format, args...))
}

// --- Locators ------------------------------------------------------------------

var (
	sheetsDocumentIDPattern = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
	sheetsDocumentURL       = regexp.MustCompile(`/spreadsheets/d/([A-Za-z0-9_-]+)`)
	sheetsGidInURL          = regexp.MustCompile(`[#?&]gid=([0-9]+)`)
	sheetsGidText           = regexp.MustCompile(`^(?:gid=)?([0-9]+)$`)
)

// sheetsDocumentID reads the spreadsheet a locator names: its ID, or the ID
// inside a link. The ID goes into a URL path, so anything that is not the
// characters Google issues is refused rather than escaped into a request.
func sheetsDocumentID(raw any) (string, error) {
	locator, ok := property.ReadLocator(raw)
	if !ok {
		return "", fmt.Errorf("documentId is not set: choose the spreadsheet, or give its ID or link")
	}
	text := strings.TrimSpace(textValue(locator.Value, ""))
	if text == "" {
		return "", fmt.Errorf("documentId is empty: choose the spreadsheet, or give its ID or link")
	}
	if locator.Mode == "url" || strings.Contains(text, "/") {
		match := sheetsDocumentURL.FindStringSubmatch(text)
		if match == nil {
			return "", fmt.Errorf("documentId %q is not a spreadsheet link: it should look like https://docs.google.com/spreadsheets/d/<id>/edit", text)
		}
		return match[1], nil
	}
	if !sheetsDocumentIDPattern.MatchString(text) {
		return "", fmt.Errorf("documentId %q is not a spreadsheet ID: an ID holds letters, digits, - and _ only", text)
	}
	return text, nil
}

// sheetRef names a tab by its gid or by its title.
type sheetRef struct {
	gid   int64
	byGid bool
	name  string
}

// sheetsSheetRef reads the tab a locator names. Only the "name" mode takes its
// value as a title: a title that happens to be a number ("2024") is still a
// name there, and a gid everywhere else.
func sheetsSheetRef(raw any) (sheetRef, error) {
	locator, ok := property.ReadLocator(raw)
	if !ok {
		return sheetRef{}, fmt.Errorf("sheetName is not set: choose the tab, or give its name or gid")
	}
	text := strings.TrimSpace(textValue(locator.Value, ""))
	if text == "" {
		return sheetRef{}, fmt.Errorf("sheetName is empty: choose the tab, or give its name or gid")
	}
	gidOf := func(digits string) (sheetRef, error) {
		gid, err := strconv.ParseInt(digits, 10, 64)
		if err != nil {
			return sheetRef{}, fmt.Errorf("sheet id %q is not a number", digits)
		}
		return sheetRef{gid: gid, byGid: true}, nil
	}
	switch locator.Mode {
	case "name":
		return sheetRef{name: text}, nil
	case "url":
		match := sheetsGidInURL.FindStringSubmatch(text)
		if match == nil {
			return sheetRef{}, fmt.Errorf("sheetName %q has no #gid= part, so it does not name a tab", text)
		}
		return gidOf(match[1])
	case "id":
		match := sheetsGidText.FindStringSubmatch(text)
		if match == nil {
			return sheetRef{}, fmt.Errorf("sheetName %q is not a tab id: use the number after gid=", text)
		}
		return gidOf(match[1])
	}
	// The list mode stores the gid; a bare value or an unknown mode is read the
	// way a person would mean it.
	if match := sheetsGidText.FindStringSubmatch(text); match != nil {
		return gidOf(match[1])
	}
	if match := sheetsGidInURL.FindStringSubmatch(text); match != nil && strings.Contains(text, "/") {
		return gidOf(match[1])
	}
	return sheetRef{name: text}, nil
}

// colLetters is the column letter of a zero-based column index.
func colLetters(index int) string {
	letters := ""
	for number := index + 1; number > 0; number = (number - 1) / 26 {
		letters = string(rune('A'+(number-1)%26)) + letters
	}
	return letters
}

// colNumber is the 1-based number of a column letter, or 0 when the text is
// not letters.
func colNumber(letters string) int {
	letters = strings.ToUpper(strings.TrimSpace(letters))
	if letters == "" {
		return 0
	}
	number := 0
	for _, letter := range letters {
		if letter < 'A' || letter > 'Z' {
			return 0
		}
		number = number*26 + int(letter-'A') + 1
	}
	return number
}

// --- Google calls --------------------------------------------------------------

func (run *sheetsRun) documentURL() string {
	return sheetsAPIRoot + "/spreadsheets/" + url.PathEscape(run.docID)
}

func (run *sheetsRun) valuesURL(rangeA1 string) string {
	return run.documentURL() + "/values/" + url.PathEscape(rangeA1)
}

// call sends one request as the node's Sheets credential.
func (run *sheetsRun) call(method, rawURL string, query url.Values, body any, dest any) error {
	credentialType := run.credentialType
	if credentialType == "" {
		credentialType = GoogleSheetsCredentialType
	}
	return run.executor.google.json(run.ctx, run.request, run.ir, credentialType, method, rawURL, query, body, dest)
}

// findTab loads the spreadsheet's tabs once and finds the one a locator names.
func (run *sheetsRun) findTab(ref sheetRef) (sheetsTab, error) {
	if run.tabs == nil {
		var payload struct {
			Sheets []struct {
				Properties struct {
					SheetID        int64  `json:"sheetId"`
					Title          string `json:"title"`
					GridProperties struct {
						RowCount    int `json:"rowCount"`
						ColumnCount int `json:"columnCount"`
					} `json:"gridProperties"`
				} `json:"properties"`
			} `json:"sheets"`
		}
		query := url.Values{"fields": []string{"sheets.properties(sheetId,title,gridProperties(rowCount,columnCount))"}}
		if err := run.call(http.MethodGet, run.documentURL(), query, nil, &payload); err != nil {
			return sheetsTab{}, err
		}
		run.tabs = make([]sheetsTab, 0, len(payload.Sheets))
		for _, sheet := range payload.Sheets {
			run.tabs = append(run.tabs, sheetsTab{
				gid: sheet.Properties.SheetID, title: sheet.Properties.Title,
				rowCount: sheet.Properties.GridProperties.RowCount, colCount: sheet.Properties.GridProperties.ColumnCount,
			})
		}
	}
	for _, tab := range run.tabs {
		if ref.byGid && tab.gid == ref.gid || !ref.byGid && tab.title == ref.name {
			return tab, nil
		}
	}
	if ref.byGid {
		return sheetsTab{}, run.errorf("the spreadsheet has no tab with gid %d", ref.gid)
	}
	return sheetsTab{}, run.errorf("the spreadsheet has no tab named %q", ref.name)
}

// values reads a range. The answer is shared for the rest of the call until a
// write empties the cache.
func (run *sheetsRun) values(rangeA1, render, dateRender string) ([][]any, error) {
	key := rangeA1 + "\x00" + render + "\x00" + dateRender
	if cached, found := run.cache[key]; found {
		return cached, nil
	}
	query := url.Values{}
	if render != "" {
		query.Set("valueRenderOption", render)
	}
	if dateRender != "" {
		query.Set("dateTimeRenderOption", dateRender)
	}
	var payload struct {
		Values [][]any `json:"values"`
	}
	if err := run.call(http.MethodGet, run.valuesURL(rangeA1), query, nil, &payload); err != nil {
		return nil, err
	}
	run.cache[key] = payload.Values
	return payload.Values, nil
}

// wrote must follow every write: whatever was read before may no longer be true.
func (run *sheetsRun) wrote() { run.cache = map[string][][]any{} }

// --- Cells ---------------------------------------------------------------------

// sheetsCellText is a cell as the text a comparison sees. A number prints the
// way it is typed into a cell, so 28 matches "28" and 1.5 matches "1.5".
func sheetsCellText(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case bool:
		return strconv.FormatBool(typed)
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return ""
		}
		return string(encoded)
	}
}

// sheetsCellEmpty is a cell that holds nothing. Zero and false are values.
func sheetsCellEmpty(value any) bool { return value == nil || value == "" }

func sheetsMap(value any) map[string]any {
	typed, _ := value.(map[string]any)
	return typed
}

func clonePaired(origin *workflow.PairedItem) *workflow.PairedItem {
	if origin == nil {
		return nil
	}
	cloned := *origin
	return &cloned
}

// --- Read ----------------------------------------------------------------------

type sheetsFilter struct {
	column string
	value  string
}

func (run *sheetsRun) readAll(items []workflow.Item, resolved []workflow.IRNode, tab sheetsTab) ([]workflow.Item, error) {
	out := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		rows, err := run.readFor(resolved[index].Parameters, tab)
		if err != nil {
			return nil, err
		}
		for _, row := range rows {
			out = append(out, workflow.Item{JSON: row, Paired: clonePaired(item.Paired)})
		}
	}
	return out, nil
}

// readFor reads the rows one item asked for.
func (run *sheetsRun) readFor(parameters map[string]any, tab sheetsTab) ([]map[string]any, error) {
	options := sheetsMap(parameters["options"])
	location := sheetsMap(sheetsMap(options["dataLocationOnSheet"])["values"])
	definition := textValue(location["rangeDefinition"], "detectAutomatically")
	formatting := sheetsMap(sheetsMap(options["outputFormatting"])["values"])
	render := textValue(formatting["general"], "UNFORMATTED_VALUE")
	dateRender := textValue(formatting["date"], "FORMATTED_STRING")

	rangeA1 := sheetsa1.Quote(tab.title)
	firstRow := 1
	if definition == "specifyRangeA1" {
		if requested := sheetsa1.Region(textValue(location["range"], "")); requested != "" {
			rangeA1 += "!" + requested
			firstRow = sheetsa1.StartRow(requested)
		}
	}
	data, err := run.values(rangeA1, render, dateRender)
	if err != nil {
		return nil, err
	}
	if len(data) == 0 {
		return nil, nil
	}

	numbered := make([]numberedRow, len(data))
	for index, cells := range data {
		numbered[index] = numberedRow{number: firstRow + index, cells: cells}
	}
	headerAt, dataFrom := 0, 1
	switch definition {
	case "specifyRange":
		headerAt = intOr(location["headerRow"], 1) - 1
		dataFrom = intOr(location["firstDataRow"], 2) - 1
	case "specifyRangeA1":
	default:
		numbered = detectDataRows(numbered, textValue(location["readRowsUntil"], "lastRowInSheet") == "firstEmptyRow")
	}
	if headerAt < 0 || headerAt >= len(numbered) {
		return nil, run.errorf("the header row %d is not among the %d rows read", headerAt+1, len(numbered))
	}
	if dataFrom < headerAt {
		return nil, run.errorf("the first data row %d comes before the header row %d", dataFrom+1, headerAt+1)
	}

	width := 0
	for _, entry := range numbered {
		width = max(width, len(entry.cells))
	}
	keys := make([]string, width)
	for column := range keys {
		var header any
		if column < len(numbered[headerAt].cells) {
			header = numbered[headerAt].cells[column]
		}
		if text := sheetsCellText(header); text != "" {
			keys[column] = text
		} else {
			// A column with no heading is still data; its name says where it is.
			keys[column] = "col_" + strconv.Itoa(column+1)
		}
	}
	candidates := make([]map[string]any, 0, len(numbered))
	for _, entry := range numbered[min(dataFrom, len(numbered)):] {
		candidate := make(map[string]any, width+1)
		candidate["row_number"] = float64(entry.number)
		for column, key := range keys {
			var cell any = ""
			if column < len(entry.cells) && entry.cells[column] != nil {
				cell = entry.cells[column]
			}
			candidate[key] = cell
		}
		candidates = append(candidates, candidate)
	}

	filters := readFilters(parameters["filtersUI"])
	if len(filters) == 0 {
		return candidates, nil
	}
	known := map[string]bool{"row_number": true}
	for _, key := range keys {
		known[key] = true
	}
	for _, filter := range filters {
		if !known[filter.column] {
			return nil, run.errorf("the sheet has no column %q to filter on", filter.column)
		}
	}
	return matchFilters(candidates, filters, textValue(parameters["combineFilters"], "AND") == "OR",
		boolValue(options["returnFirstMatch"])), nil
}

type numberedRow struct {
	number int
	cells  []any
}

func intOr(value any, fallback int) int {
	switch typed := value.(type) {
	case float64:
		return int(typed)
	case int:
		return typed
	case string:
		if parsed, err := strconv.Atoi(strings.TrimSpace(typed)); err == nil {
			return parsed
		}
	}
	return fallback
}

// detectDataRows trims what a person would not call the table: columns with no
// heading and no data, the empty rows above the table, and then either
// everything from the first empty row on, or every empty row.
func detectDataRows(rows []numberedRow, stopAtFirstEmpty bool) []numberedRow {
	width := 0
	for _, entry := range rows {
		width = max(width, len(entry.cells))
	}
	keep := make([]int, 0, width)
	for column := 0; column < width; column++ {
		// Only an explicit empty heading can drop a column: a heading row Google
		// cut short is not an empty heading.
		first := rows[0].cells
		if column < len(first) && first[column] == "" {
			hasData := false
			for _, entry := range rows[1:] {
				if column < len(entry.cells) && !sheetsCellEmpty(entry.cells[column]) {
					hasData = true
					break
				}
			}
			if !hasData {
				continue
			}
		}
		keep = append(keep, column)
	}
	narrowed := make([]numberedRow, len(rows))
	for index, entry := range rows {
		cells := make([]any, len(keep))
		for position, column := range keep {
			if column < len(entry.cells) {
				cells[position] = entry.cells[column]
			} else {
				cells[position] = ""
			}
		}
		narrowed[index] = numberedRow{number: entry.number, cells: cells}
	}
	isEmpty := func(entry numberedRow) bool {
		for _, cell := range entry.cells {
			if !sheetsCellEmpty(cell) {
				return false
			}
		}
		return true
	}
	for len(narrowed) > 0 && isEmpty(narrowed[0]) {
		narrowed = narrowed[1:]
	}
	if len(narrowed) == 0 {
		return narrowed
	}
	trimmed := narrowed[:1]
	for _, entry := range narrowed[1:] {
		if isEmpty(entry) {
			if stopAtFirstEmpty {
				break
			}
			continue
		}
		trimmed = append(trimmed, entry)
	}
	return trimmed
}

func readFilters(raw any) []sheetsFilter {
	list, _ := sheetsMap(raw)["values"].([]any)
	filters := make([]sheetsFilter, 0, len(list))
	for _, entry := range list {
		fields := sheetsMap(entry)
		// A column is named by its heading as written, spaces included.
		column := textValue(fields["lookupColumn"], "")
		if column == "" {
			continue
		}
		filters = append(filters, sheetsFilter{column: column, value: sheetsCellText(fields["lookupValue"])})
	}
	return filters
}

// matchFilters applies the filters the way n8n does. OR lists the rows each
// filter finds, filter by filter, and with only the first match wanted stops at
// the first row found at all. AND walks the rows once.
func matchFilters(candidates []map[string]any, filters []sheetsFilter, anyOf, firstOnly bool) []map[string]any {
	matches := func(candidate map[string]any, filter sheetsFilter) bool {
		return sheetsCellText(candidate[filter.column]) == filter.value
	}
	var found []map[string]any
	if anyOf {
		added := map[int]bool{}
	filtersLoop:
		for _, filter := range filters {
			for index, candidate := range candidates {
				if !matches(candidate, filter) {
					continue
				}
				if !added[index] {
					added[index] = true
					found = append(found, candidate)
				}
				if firstOnly {
					break filtersLoop
				}
			}
		}
		return found
	}
	for _, candidate := range candidates {
		all := true
		for _, filter := range filters {
			if !matches(candidate, filter) {
				all = false
				break
			}
		}
		if !all {
			continue
		}
		found = append(found, candidate)
		if firstOnly {
			break
		}
	}
	return found
}
