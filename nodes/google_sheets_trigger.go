package nodes

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sheetsa1"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// GoogleSheetsTriggerRowCap is the most data rows the trigger will watch. The
// cursor keeps a short fingerprint of every watched row, so the cap is what
// keeps that record from growing without bound; a sheet past it is refused by
// name rather than half watched.
const GoogleSheetsTriggerRowCap = 20000

// GoogleSheetsTriggerExecutor emits the polled row on a manual run, the way the
// other poll triggers do: the poller queues an execution carrying the row as
// its input, and the trigger node hands it on.
type GoogleSheetsTriggerExecutor struct{}

func (GoogleSheetsTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}

// GoogleSheetsPoller finds the rows added or changed since the last tick.
//
// It compares the sheet's values with a fingerprint of each row kept in the
// cursor, rather than diffing Drive revisions: the fingerprint needs only the
// spreadsheets scope and one values read per tick, where a revision diff needs
// the Drive scope and an export of the previous revision.
type GoogleSheetsPoller struct {
	google *GoogleClient
}

// NewGoogleSheetsPoller builds the Sheets trigger's poll handler.
func NewGoogleSheetsPoller(policy safehttp.Policy) *GoogleSheetsPoller {
	return &GoogleSheetsPoller{google: NewGoogleClient(policy)}
}

// sheetsPollCursor is what a tick leaves for the next one.
type sheetsPollCursor struct {
	Version int `json:"v"`
	// Doc and Gid say which tab the fingerprints belong to: pointing the
	// trigger somewhere else starts a new baseline instead of reporting every
	// row of the new tab as a change.
	Doc string `json:"doc,omitempty"`
	Gid int64  `json:"gid"`
	// Config fingerprints how the rows were read and what their fingerprints
	// were made from: the watched columns, how values and dates are rendered,
	// the range and the header and first data rows. Edit any of them and every
	// stored fingerprint is stale, so the next tick starts a new baseline
	// instead of reporting each row as changed.
	Config string `json:"config,omitempty"`
	// LastRow is the last data row there was, so a row past it is an addition.
	LastRow int `json:"lastRow"`
	// Rows maps a sheet row number to the fingerprint of its watched cells.
	Rows map[string]string `json:"rows"`
}

// polledRow is one data row of the sheet as this tick read it.
type polledRow struct {
	number int
	cells  []any
	hash   string
}

func (handler *GoogleSheetsPoller) Poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	unchanged := claim.Cursor.Cursor
	items, next, err := handler.poll(ctx, claim)
	if err != nil {
		return nil, unchanged, err
	}
	return items, next, nil
}

func (handler *GoogleSheetsPoller) poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	node := claim.Node
	parameters := node.Parameters
	event := textValue(parameters["event"], SheetsEventAnyUpdate)
	run := &sheetsRun{
		executor:       &GoogleSheetsExecutor{google: handler.google},
		ctx:            ctx,
		request:        claim.Request,
		ir:             irFromNode(node),
		cache:          map[string][][]any{},
		credentialType: GoogleSheetsTriggerCredentialType,
	}
	docID, err := sheetsDocumentID(parameters["documentId"])
	if err != nil {
		return nil, "", fmt.Errorf("node %q: %w", node.Name, err)
	}
	run.docID = docID
	ref, err := sheetsSheetRef(parameters["sheetName"])
	if err != nil {
		return nil, "", fmt.Errorf("node %q: %w", node.Name, err)
	}
	tab, err := run.findTab(ref)
	if err != nil {
		return nil, "", err
	}

	options := sheetsMap(parameters["options"])
	location := sheetsMap(sheetsMap(options["dataLocationOnSheet"])["values"])
	rangeA1 := sheetsa1.Quote(tab.title)
	// region is the cell range the settings name, empty for the whole tab. It
	// is what the config fingerprint holds, not the tab's title: the gid
	// identifies the tab, and a rename is not a change of what is watched.
	region := ""
	headerRow, firstDataRow := 1, 2
	ranged := false
	switch textValue(location["rangeDefinition"], "") {
	case "specifyRangeA1":
		if requested := sheetsa1.Region(textValue(location["range"], "")); requested != "" {
			region = requested
			rangeA1 += "!" + requested
			headerRow = sheetsa1.StartRow(requested)
			firstDataRow = headerRow + 1
			ranged = true
		}
	case "specifyRange":
		headerRow = max(intOr(location["headerRow"], 1), 1)
		firstDataRow = max(intOr(location["firstDataRow"], headerRow+1), 1)
	}
	// Row numbers are the sheet's own, so the first row read is the range's.
	firstRowRead := 1
	if ranged {
		firstRowRead = headerRow
	}

	render, dateRender := "UNFORMATTED_VALUE", "SERIAL_NUMBER"
	if event == SheetsEventRowAdded {
		render = textValue(options["valueRender"], "UNFORMATTED_VALUE")
		dateRender = textValue(options["dateTimeRenderOption"], "FORMATTED_STRING")
	}
	data, err := run.values(rangeA1, render, dateRender)
	if err != nil {
		return nil, "", err
	}

	// The header is the row the settings name; a sheet without one has nothing
	// to key a row by.
	headerIndex := headerRow - firstRowRead
	if headerIndex < 0 || headerIndex >= len(data) {
		return nil, "", fmt.Errorf("node %q: row %d of %q holds no column names, so there is nothing to watch the rows by", node.Name, headerRow, tab.title)
	}
	// Columns are named by the heading as the sheet shows it, as the picker
	// lists them. The values were read as numbers and serial dates, so a heading
	// that is not plain text is read again as shown; headings that are text read
	// the same either way, and then there is no second read.
	headerCells := data[headerIndex]
	if render != "FORMATTED_VALUE" && (render == "FORMULA" || !allText(headerCells)) {
		shown, err := run.values(sheetsa1.Quote(tab.title)+"!"+sheetsa1.HeaderRange(region, headerRow), "FORMATTED_VALUE", "")
		if err != nil {
			return nil, "", err
		}
		if len(shown) > 0 {
			headerCells = shown[0]
		}
	}
	var header []string
	for _, cell := range headerCells {
		header = append(header, sheetsCellText(cell))
	}
	if len(header) == 0 {
		return nil, "", fmt.Errorf("node %q: row %d of %q holds no column names, so there is nothing to watch the rows by", node.Name, headerRow, tab.title)
	}

	watched, err := watchedColumns(options["columnsToWatch"], header)
	if err != nil {
		return nil, "", fmt.Errorf("node %q: %w", node.Name, err)
	}

	rows := make([]polledRow, 0, len(data))
	for index := max(firstDataRow-firstRowRead, headerIndex+1); index < len(data); index++ {
		cells := data[index]
		empty := true
		for _, cell := range cells {
			if !sheetsCellEmpty(cell) {
				empty = false
				break
			}
		}
		if empty {
			continue
		}
		rows = append(rows, polledRow{number: firstRowRead + index, cells: cells, hash: rowFingerprint(cells, watched)})
		if len(rows) > GoogleSheetsTriggerRowCap {
			return nil, "", fmt.Errorf("node %q: %q has more than %d data rows, which is the most the Google Sheets Trigger watches. "+
				"Set Options > Data Location on Sheet to a smaller range", node.Name, tab.title, GoogleSheetsTriggerRowCap)
		}
	}

	config := sheetsTriggerConfig(options["columnsToWatch"], render, dateRender, region, headerRow, firstDataRow)
	current := sheetsPollCursor{Version: 1, Doc: docID, Gid: tab.gid, Config: config, Rows: make(map[string]string, len(rows))}
	for _, row := range rows {
		current.Rows[strconv.Itoa(row.number)] = row.hash
		current.LastRow = max(current.LastRow, row.number)
	}

	var previous sheetsPollCursor
	hasBaseline := false
	if strings.TrimSpace(claim.Cursor.Cursor) != "" {
		if json.Unmarshal([]byte(claim.Cursor.Cursor), &previous) == nil &&
			previous.Version == 1 && previous.Doc == docID && previous.Gid == tab.gid && previous.Config == config {
			hasBaseline = true
		}
	}
	encode := func() (string, error) {
		encoded, err := json.Marshal(current)
		return string(encoded), err
	}
	if !hasBaseline {
		// The first tick only records what is already there.
		next, err := encode()
		return nil, next, err
	}

	// Nothing changed: hand the stored cursor back as it is, rather than
	// encoding a long map into the same text again.
	if reflect.DeepEqual(previous, current) {
		return nil, claim.Cursor.Cursor, nil
	}

	var emitted []workflow.Item
	for _, row := range rows {
		oldHash, existed := previous.Rows[strconv.Itoa(row.number)]
		added := row.number > previous.LastRow || !existed
		changed := existed && oldHash != row.hash
		item := map[string]any{}
		switch event {
		case SheetsEventRowAdded:
			if row.number <= previous.LastRow {
				continue
			}
		case SheetsEventRowUpdate:
			if !changed || row.number > previous.LastRow {
				continue
			}
			item["row_number"] = float64(row.number)
		default:
			if !added && !changed {
				continue
			}
			item["row_number"] = float64(row.number)
			if added {
				item["change_type"] = "added"
			} else {
				item["change_type"] = "updated"
			}
		}
		for column, name := range header {
			if name == "" {
				name = "col_" + strconv.Itoa(column+1)
			}
			var cell any = ""
			if column < len(row.cells) && row.cells[column] != nil {
				cell = row.cells[column]
			}
			item[name] = cell
		}
		emitted = append(emitted, workflow.Item{JSON: item})
	}
	next, err := encode()
	return emitted, next, err
}

// allText reports whether every cell is text, so reading it as shown or as
// stored gives the same answer.
func allText(cells []any) bool {
	for _, cell := range cells {
		if _, isText := cell.(string); !isText {
			return false
		}
	}
	return true
}

// headingList reads a list of column names as written. The shared stringList
// trims each entry, which would turn the heading "Email " into one that is not
// in the sheet.
func headingList(raw any) []string {
	var names []string
	switch typed := raw.(type) {
	case []string:
		for _, name := range typed {
			if name != "" {
				names = append(names, name)
			}
		}
	case []any:
		for _, entry := range typed {
			if name, ok := entry.(string); ok && name != "" {
				names = append(names, name)
			}
		}
	}
	return names
}

// watchedColumns resolves Columns to Watch to indexes into the header. An empty
// list watches every column; a name the sheet does not have is refused, because
// watching a column that is not there would never see a change.
func watchedColumns(raw any, header []string) ([]int, error) {
	names := headingList(raw)
	if len(names) == 0 {
		return nil, nil
	}
	indexes := make([]int, 0, len(names))
	for _, name := range names {
		position := -1
		for index, candidate := range header {
			if candidate == name {
				position = index
				break
			}
		}
		if position < 0 {
			return nil, fmt.Errorf("Columns to Watch names %q, which is not a column of the sheet", name)
		}
		indexes = append(indexes, position)
	}
	sort.Ints(indexes)
	return indexes, nil
}

// sheetsTriggerConfig is a digest of the settings a row's fingerprint depends
// on. The watched columns are sorted, so listing them in another order is not
// a change.
func sheetsTriggerConfig(watched any, render, dateRender, region string, headerRow, firstDataRow int) string {
	names := slices.Clone(headingList(watched))
	sort.Strings(names)
	encoded, _ := json.Marshal(struct {
		Watched      []string `json:"watched"`
		Render       string   `json:"render"`
		DateRender   string   `json:"dateRender"`
		Range        string   `json:"range"`
		HeaderRow    int      `json:"headerRow"`
		FirstDataRow int      `json:"firstDataRow"`
	}{names, render, dateRender, region, headerRow, firstDataRow})
	digest := sha256.Sum256(encoded)
	return hex.EncodeToString(digest[:])[:16]
}

// rowFingerprint is a short, stable digest of the cells a row is watched by.
func rowFingerprint(cells []any, watched []int) string {
	hasher := sha256.New()
	write := func(cell any) {
		hasher.Write([]byte(sheetsCellText(cell)))
		hasher.Write([]byte{0x1f})
	}
	if len(watched) == 0 {
		// Trailing empty cells are not part of a row: Google leaves them off.
		end := len(cells)
		for end > 0 && sheetsCellEmpty(cells[end-1]) {
			end--
		}
		for _, cell := range cells[:end] {
			write(cell)
		}
	} else {
		for _, index := range watched {
			var cell any
			if index < len(cells) {
				cell = cells[index]
			}
			write(cell)
		}
	}
	return hex.EncodeToString(hasher.Sum(nil))[:16]
}
