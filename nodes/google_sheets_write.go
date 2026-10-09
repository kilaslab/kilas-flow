package nodes

import (
	"encoding/json"
	"math"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// writeContext is what append, update and appendOrUpdate need to know about the
// tab before they touch it: the options of the first item, and the sheet as it
// stands, read once.
type writeContext struct {
	tab          sheetsTab
	valueInput   string
	headerRow    int // 1-based
	firstDataRow int // 1-based
	extraData    string
	useAppend    bool
	existing     [][]any
	header       []string
}

func (run *sheetsRun) writeContext(parameters map[string]any, tab sheetsTab) (*writeContext, error) {
	options := sheetsMap(parameters["options"])
	location := sheetsMap(sheetsMap(options["locationDefine"])["values"])
	context := &writeContext{
		tab:          tab,
		valueInput:   textValue(options["cellFormat"], "USER_ENTERED"),
		headerRow:    max(intOr(location["headerRow"], 1), 1),
		firstDataRow: max(intOr(location["firstDataRow"], 2), 1),
		extraData:    textValue(options["handlingExtraData"], "insertInNewColumn"),
		useAppend:    boolValue(options["useAppend"]),
	}
	switch context.valueInput {
	case "USER_ENTERED", "RAW":
	default:
		return nil, run.errorf("cell format %q is not supported: use USER_ENTERED or RAW", context.valueInput)
	}
	switch context.extraData {
	case "insertInNewColumn", "ignoreIt", "error":
	default:
		return nil, run.errorf("handling extra fields %q is not supported: use insertInNewColumn, ignoreIt or error", context.extraData)
	}
	existing, err := run.values(a1Quote(tab.title), "FORMATTED_VALUE", "")
	if err != nil {
		return nil, err
	}
	context.existing = existing
	context.readHeader()
	return context, nil
}

// readHeader reads the column names out of the header row.
func (context *writeContext) readHeader() {
	context.header = nil
	if context.headerRow-1 >= len(context.existing) {
		return
	}
	for _, cell := range context.existing[context.headerRow-1] {
		context.header = append(context.header, sheetsCellText(cell))
	}
}

// itemData is one input item's contribution to a write: the values by column,
// and whether they came from automatic mapping.
type itemData struct {
	values map[string]any
	auto   bool
}

// mappingOf decodes the columns parameter. A node that never stored one is the
// editor's default, mapping each column by hand with nothing set.
func mappingOf(parameters map[string]any) property.Mapping {
	mapping, ok := property.ReadMapping(parameters["columns"])
	if !ok {
		return property.Mapping{Mode: property.MappingManual, Values: map[string]any{}}
	}
	return mapping
}

// itemValues reads what one item writes. Mapped values with no value are
// written as empty text, so a cleared field clears the cell instead of being
// skipped.
func (run *sheetsRun) itemValues(index int, item workflow.Item, mapping property.Mapping, forceAuto bool) (itemData, error) {
	if forceAuto || mapping.Mode == property.MappingAuto {
		values := make(map[string]any, len(item.JSON))
		for key, value := range item.JSON {
			values[key] = value
		}
		return itemData{values: values, auto: true}, nil
	}
	if len(mapping.Values) == 0 {
		return itemData{}, run.errorf("item %d maps no columns, so it has nothing to write: map at least one", index)
	}
	values := make(map[string]any, len(mapping.Values))
	for column, value := range mapping.Values {
		if value == nil {
			value = ""
		}
		values[column] = value
	}
	return itemData{values: values}, nil
}

// cellOf is a value as a cell: structured data is written as its JSON text.
func cellOf(value any) any {
	switch typed := value.(type) {
	case map[string]any, []any:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return sheetsCellText(typed)
		}
		return string(encoded)
	default:
		return value
	}
}

// rowFor lays one item's values out in header order. A column the item does
// not carry is empty text, or null when the append call is used so the cell is
// left alone.
func rowFor(header []string, values map[string]any, missing any) []any {
	cells := make([]any, len(header))
	for position, name := range header {
		value, present := values[name]
		if !present || value == nil || name == "row_number" {
			cells[position] = missing
			continue
		}
		cells[position] = cellOf(value)
	}
	return cells
}

// extraColumns lists the keys items carry that the header does not, in a fixed
// order. Items are unordered maps here, so the new columns come out sorted.
func extraColumns(header []string, data []itemData) []string {
	known := map[string]bool{"row_number": true}
	for _, name := range header {
		known[name] = true
	}
	extra := map[string]bool{}
	for _, entry := range data {
		if !entry.auto {
			continue
		}
		for key := range entry.values {
			if !known[key] {
				extra[key] = true
			}
		}
	}
	names := make([]string, 0, len(extra))
	for name := range extra {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// prepareColumns settles the header before rows are laid out: automatic
// mapping either grows it, ignores the extra fields, or refuses them, and a
// hand-mapped column the sheet does not have is refused.
func (run *sheetsRun) prepareColumns(context *writeContext, data []itemData) error {
	extra := extraColumns(context.header, data)
	anyAuto := false
	for _, entry := range data {
		anyAuto = anyAuto || entry.auto
	}
	if anyAuto {
		switch context.extraData {
		case "error":
			if len(extra) > 0 {
				return run.errorf("the input has fields that match no column of the sheet (%s). "+
					"Change Handling extra fields in input to ignore them or to add them as new columns", strings.Join(extra, ", "))
			}
		case "insertInNewColumn":
			if len(extra) > 0 || len(context.header) == 0 {
				if err := run.growHeader(context, extra); err != nil {
					return err
				}
			}
		}
	}
	if len(context.header) == 0 {
		return run.errorf("row %d of %q holds no column names, so there is nothing to map the values by", context.headerRow, context.tab.title)
	}
	for _, entry := range data {
		if entry.auto {
			continue
		}
		for column := range entry.values {
			if column != "row_number" && !slices.Contains(context.header, column) {
				return run.errorf("column %q is mapped but the sheet no longer has it: it was renamed or removed after this node was configured", column)
			}
		}
	}
	return nil
}

// growHeader writes the header row with the new columns added, making room in
// the grid first when it is too narrow.
func (run *sheetsRun) growHeader(context *writeContext, extra []string) error {
	names := append(slices.Clone(context.header), extra...)
	if len(names) == 0 {
		return run.errorf("there are no column names to write: the sheet has no header row and the input has no fields")
	}
	if err := run.ensureGrid(&context.tab, context.headerRow, len(names)); err != nil {
		return err
	}
	cells := make([]any, len(names))
	for index, name := range names {
		cells[index] = name
	}
	rangeA1 := a1Quote(context.tab.title) + "!" + strconv.Itoa(context.headerRow) + ":" + strconv.Itoa(context.headerRow)
	if err := run.putValues(rangeA1, [][]any{cells}, "RAW"); err != nil {
		return err
	}
	for len(context.existing) < context.headerRow {
		context.existing = append(context.existing, nil)
	}
	context.existing[context.headerRow-1] = cells
	context.header = names
	return nil
}

// ensureGrid adds the rows and columns a write needs and the tab does not have.
func (run *sheetsRun) ensureGrid(tab *sheetsTab, rows, columns int) error {
	requests := make([]any, 0, 2)
	if rows > tab.rowCount {
		requests = append(requests, map[string]any{"appendDimension": map[string]any{
			"sheetId": tab.gid, "dimension": "ROWS", "length": rows - tab.rowCount,
		}})
	}
	if columns > tab.colCount {
		requests = append(requests, map[string]any{"appendDimension": map[string]any{
			"sheetId": tab.gid, "dimension": "COLUMNS", "length": columns - tab.colCount,
		}})
	}
	if len(requests) == 0 {
		return nil
	}
	if err := run.call(http.MethodPost, run.documentURL()+":batchUpdate", nil, map[string]any{"requests": requests}, nil); err != nil {
		return err
	}
	tab.rowCount = max(tab.rowCount, rows)
	tab.colCount = max(tab.colCount, columns)
	for index := range run.tabs {
		if run.tabs[index].gid == tab.gid {
			run.tabs[index] = *tab
		}
	}
	run.wrote()
	return nil
}

func (run *sheetsRun) putValues(rangeA1 string, values [][]any, valueInput string) error {
	body := map[string]any{"range": rangeA1, "majorDimension": "ROWS", "values": values}
	err := run.call(http.MethodPut, run.valuesURL(rangeA1), url.Values{"valueInputOption": []string{valueInput}}, body, nil)
	run.wrote()
	return err
}

// appendValues writes rows after the last used row of the tab. By default that
// row is counted here and the rows are written at exactly that place, because
// Google's own append call finds "the table" and puts rows after the first gap
// it meets; useAppend opts into that call for fewer requests.
func (run *sheetsRun) appendValues(context *writeContext, rows [][]any) error {
	if len(rows) == 0 {
		return nil
	}
	if context.useAppend {
		query := url.Values{"valueInputOption": []string{context.valueInput}, "insertDataOption": []string{"INSERT_ROWS"}}
		err := run.call(http.MethodPost, run.valuesURL(a1Quote(context.tab.title))+":append", query, map[string]any{"values": rows}, nil)
		run.wrote()
		return err
	}
	last := len(context.existing)
	if last < context.headerRow && len(context.header) > 0 {
		last = context.headerRow
	}
	first := last + 1
	width := 0
	for _, cells := range rows {
		width = max(width, len(cells))
	}
	if err := run.ensureGrid(&context.tab, first+len(rows)-1, width); err != nil {
		return err
	}
	rangeA1 := a1Quote(context.tab.title) + "!" + strconv.Itoa(first) + ":" + strconv.Itoa(first+len(rows)-1)
	err := run.putValues(rangeA1, rows, context.valueInput)
	for _, cells := range rows {
		context.existing = append(context.existing, cells)
	}
	return err
}

// passThrough returns the input items as the operation's output, which is what
// automatic mapping and clear emit.
func passThrough(items []workflow.Item) []workflow.Item {
	out := make([]workflow.Item, len(items))
	for index, item := range items {
		out[index] = workflow.Item{JSON: item.JSON, Binary: item.Binary, Paired: clonePaired(item.Paired)}
	}
	return out
}

// mappedOutput returns what hand-mapped columns emit: the values each item
// sent, which is also what the next node can use to see what was written.
func mappedOutput(items []workflow.Item, data []itemData) []workflow.Item {
	out := make([]workflow.Item, len(items))
	for index, item := range items {
		out[index] = workflow.Item{JSON: data[index].values, Paired: clonePaired(item.Paired)}
	}
	return out
}

// --- Append --------------------------------------------------------------------

func (run *sheetsRun) appendRows(items []workflow.Item, resolved []workflow.IRNode, tab sheetsTab) ([]workflow.Item, error) {
	context, err := run.writeContext(resolved[0].Parameters, tab)
	if err != nil {
		return nil, err
	}
	// An empty sheet has no columns to map by hand, so its first rows define them.
	forceAuto := len(context.existing) == 0
	data := make([]itemData, len(items))
	for index, item := range items {
		data[index], err = run.itemValues(index, item, mappingOf(resolved[index].Parameters), forceAuto)
		if err != nil {
			return nil, err
		}
	}
	if err := run.prepareColumns(context, data); err != nil {
		return nil, err
	}
	missing := any("")
	if context.useAppend {
		missing = nil
	}
	rows := make([][]any, len(data))
	for index, entry := range data {
		rows[index] = rowFor(context.header, entry.values, missing)
	}
	if err := run.appendValues(context, rows); err != nil {
		return nil, err
	}
	if data[0].auto {
		return passThrough(items), nil
	}
	return mappedOutput(items, data), nil
}

// --- Update and appendOrUpdate -------------------------------------------------

func (run *sheetsRun) updateRows(items []workflow.Item, resolved []workflow.IRNode, tab sheetsTab, upsert bool) ([]workflow.Item, error) {
	parameters := resolved[0].Parameters
	context, err := run.writeContext(parameters, tab)
	if err != nil {
		return nil, err
	}
	mapping := mappingOf(parameters)
	if len(mapping.MatchingColumns) == 0 {
		return nil, run.errorf("a matching column is required to find the row: set one in Columns")
	}
	match := mapping.MatchingColumns[0]
	forceAuto := upsert && len(context.existing) == 0
	if !forceAuto && len(context.header) == 0 {
		return nil, run.errorf("row %d of %q holds no column names, so there is nothing to map the values by", context.headerRow, tab.title)
	}
	data := make([]itemData, len(items))
	for index, item := range items {
		data[index], err = run.itemValues(index, item, mappingOf(resolved[index].Parameters), forceAuto)
		if err != nil {
			return nil, err
		}
	}
	if err := run.prepareColumns(context, data); err != nil {
		return nil, err
	}

	// The values of the matching column, as the sheet had them when the batch
	// began: every item is matched against this snapshot, so a row one item adds
	// is not a match for the next.
	var keyValues []string
	byRowNumber := match == "row_number"
	if !byRowNumber {
		keyIndex := slices.Index(context.header, match)
		if keyIndex < 0 {
			return nil, run.errorf("the sheet has no column %q, so rows cannot be matched on it", match)
		}
		for rowIndex := context.firstDataRow - 1; rowIndex < len(context.existing); rowIndex++ {
			var cell any
			if keyIndex < len(context.existing[rowIndex]) {
				cell = context.existing[rowIndex][keyIndex]
			}
			keyValues = append(keyValues, sheetsCellText(cell))
		}
	}

	type cellWrite struct {
		rangeA1 string
		value   any
	}
	var writes []cellWrite
	var appended [][]any
	updated := false
	for index, entry := range data {
		matchValue, hasMatch := entry.values[match]
		if hasMatch && matchValue == nil {
			hasMatch = false
		}
		// An item whose match value is empty text found nothing to match: it
		// must not land on the first row whose match cell is empty too.
		if entry.auto && hasMatch && sheetsCellText(matchValue) == "" {
			hasMatch = false
		}
		if !entry.auto && (!hasMatch || sheetsCellText(matchValue) == "") {
			return nil, run.errorf("item %d: the value of the matching column %q is empty, so no row can be found", index, match)
		}
		rowNumber := 0
		switch {
		case !hasMatch:
		case byRowNumber:
			number, ok := wholeNumber(matchValue)
			if !ok || number < 1 {
				return nil, run.errorf("item %d: row_number %v is not a row of the sheet", index, matchValue)
			}
			rowNumber = number
		default:
			if position := slices.Index(keyValues, sheetsCellText(matchValue)); position >= 0 {
				rowNumber = context.firstDataRow + position
			}
		}
		if rowNumber == 0 {
			if upsert {
				appended = append(appended, rowFor(context.header, entry.values, ""))
			}
			continue
		}
		for column, name := range context.header {
			if name == match || name == "row_number" {
				continue
			}
			value, present := entry.values[name]
			if !present || value == nil {
				continue
			}
			writes = append(writes, cellWrite{
				rangeA1: a1Quote(tab.title) + "!" + colLetters(column) + strconv.Itoa(rowNumber),
				value:   cellOf(value),
			})
			updated = true
		}
	}

	if len(writes) > 0 {
		cells := make([]any, 0, len(writes))
		for _, write := range writes {
			cells = append(cells, map[string]any{"range": write.rangeA1, "values": [][]any{{write.value}}})
		}
		body := map[string]any{"valueInputOption": context.valueInput, "data": cells}
		err := run.call(http.MethodPost, run.documentURL()+"/values:batchUpdate", nil, body, nil)
		run.wrote()
		if err != nil {
			return nil, err
		}
	}
	if err := run.appendValues(context, appended); err != nil {
		return nil, err
	}

	if data[0].auto {
		return passThrough(items), nil
	}
	if !upsert && !updated {
		return nil, nil
	}
	return mappedOutput(items, data), nil
}

// wholeNumber reads a row number out of the shapes an expression leaves it in.
func wholeNumber(value any) (int, bool) {
	switch typed := value.(type) {
	case float64:
		if typed != math.Trunc(typed) || math.IsInf(typed, 0) || math.IsNaN(typed) {
			return 0, false
		}
		return int(typed), true
	case int:
		return typed, true
	case string:
		number, err := strconv.Atoi(strings.TrimSpace(typed))
		return number, err == nil
	}
	return 0, false
}

// --- Delete and clear ----------------------------------------------------------

func (run *sheetsRun) deleteSpans(items []workflow.Item, resolved []workflow.IRNode, tab sheetsTab) ([]workflow.Item, error) {
	requests := make([]any, 0, len(items))
	for index := range items {
		parameters := resolved[index].Parameters
		kind := textValue(parameters["toDelete"], "rows")
		count := intOr(parameters["numberToDelete"], 1)
		if count < 1 {
			return nil, run.errorf("item %d: the number to delete must be at least 1", index)
		}
		var dimension string
		var start int // zero-based
		switch kind {
		case "rows":
			dimension = "ROWS"
			start = intOr(parameters["startIndex"], 2) - 1
			if start < 0 {
				return nil, run.errorf("item %d: the start row must be at least 1", index)
			}
		case "columns":
			dimension = "COLUMNS"
			number := colNumber(textValue(parameters["startIndex"], "A"))
			if number < 1 {
				return nil, run.errorf("item %d: the start column %q is not a column letter", index, textValue(parameters["startIndex"], ""))
			}
			start = number - 1
		default:
			return nil, run.errorf("item %d: delete %q is not supported, use rows or columns", index, kind)
		}
		requests = append(requests, map[string]any{"deleteDimension": map[string]any{"range": map[string]any{
			"sheetId": tab.gid, "dimension": dimension, "startIndex": start, "endIndex": start + count,
		}}})
	}
	// One request: the spans apply in order, which is what separate requests
	// would do, so a later item sees the rows an earlier one removed.
	err := run.call(http.MethodPost, run.documentURL()+":batchUpdate", nil, map[string]any{"requests": requests}, nil)
	run.wrote()
	if err != nil {
		return nil, err
	}
	return []workflow.Item{{JSON: map[string]any{"success": true}}}, nil
}

func (run *sheetsRun) clearSpans(items []workflow.Item, resolved []workflow.IRNode, tab sheetsTab) ([]workflow.Item, error) {
	for index := range items {
		parameters := resolved[index].Parameters
		kind := textValue(parameters["clear"], "wholeSheet")
		title := a1Quote(tab.title)
		var rangeA1 string
		switch kind {
		case "wholeSheet":
			rangeA1 = title
			if boolValue(parameters["keepFirstRow"]) {
				if tab.rowCount < 2 {
					continue
				}
				rangeA1 = title + "!2:" + strconv.Itoa(tab.rowCount)
			}
		case "specificRows":
			start := intOr(parameters["startIndex"], 1)
			count := intOr(parameters["rowsToDelete"], 1)
			if start < 1 || count < 1 {
				return nil, run.errorf("item %d: the start row and the number of rows must be at least 1", index)
			}
			rangeA1 = title + "!" + strconv.Itoa(start) + ":" + strconv.Itoa(start+count-1)
		case "specificColumns":
			start := colNumber(textValue(parameters["startIndex"], "A"))
			count := intOr(parameters["columnsToDelete"], 1)
			if start < 1 || count < 1 {
				return nil, run.errorf("item %d: the start column must be a column letter and the number of columns at least 1", index)
			}
			rangeA1 = title + "!" + colLetters(start-1) + ":" + colLetters(start+count-2)
		case "specificRange":
			region := strings.TrimSpace(textValue(parameters["range"], "A:F"))
			region = a1Region(region)
			if region == "" {
				return nil, run.errorf("item %d: the range to clear is empty", index)
			}
			rangeA1 = title + "!" + region
		default:
			return nil, run.errorf("item %d: clear %q is not supported", index, kind)
		}
		err := run.call(http.MethodPost, run.valuesURL(rangeA1)+":clear", nil, map[string]any{}, nil)
		run.wrote()
		if err != nil {
			return nil, err
		}
	}
	return passThrough(items), nil
}
