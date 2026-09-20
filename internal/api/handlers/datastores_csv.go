package handlers

import (
	"bytes"
	"context"
	"encoding/csv"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/datastore"
)

// csvImportMaxBodyBytes is the explicit ceiling on a CSV import upload. Huma
// refuses a body that reaches it with a 413 that names the limit, and leaving
// the operation unset would inherit the one-mebibyte default nobody chose for
// a spreadsheet. Five mebibytes holds roughly fifty thousand ordinary rows.
const csvImportMaxBodyBytes = 5 * 1024 * 1024

// csvExportPageSize is the List limit one export page reads. It matches the
// row store's maximum so each page holds SQLite's single connection as
// briefly as one listing can, and the keyset cursor carries the position
// between pages.
const csvExportPageSize = 100

// csvTimestampLayout renders instants the way n8n's grid does: RFC 3339 with
// millisecond precision. The row store parses it back through RFC3339Nano,
// so an export re-imports to the same instant.
const csvTimestampLayout = "2006-01-02T15:04:05.000Z07:00"

// csvDateLayouts mirrors the row store's accepted date spellings, so a cell
// the import refuses is one the store would refuse too. Validation happens
// once here and a time.Time flows into Insert, so the two parses can never
// disagree about the same text.
var csvDateLayouts = []string{
	time.RFC3339Nano,
	"2006-01-02 15:04:05.999999999Z07:00",
	"2006-01-02T15:04:05.999999999Z07:00",
	"2006-01-02 15:04:05.999999999",
	"2006-01-02 15:04:05",
	"2006-01-02T15:04:05",
	"2006-01-02",
}

// csvReservedColumns mirrors the row store's reservation: id, createdAt and
// updatedAt are physical system columns and dryRunState is wire-only, all
// matched case-insensitively. It lives here rather than calling into the
// store because the header check must run before any row is written, and the
// store only refuses these names one Insert at a time.
var csvReservedColumns = map[string]string{
	"id":          "id",
	"createdat":   "createdAt",
	"updatedat":   "updatedAt",
	"dryrunstate": "dryRunState",
}

// CSVImportIssue is one row that kept its file out of the table. The severity
// reuses the workflow import report's vocabulary: a failed row blocks the
// whole import, so every issue here is blocking and nothing is half-written.
type CSVImportIssue struct {
	// Line is the one-based physical line where the record starts, the header
	// counted as line one.
	Line     int    `json:"line"`
	Column   string `json:"column"`
	Severity string `json:"severity"`
	Reason   string `json:"reason"`
}

// CSVImportReport is the whole answer to an upload: how many rows landed,
// how many blank lines were passed over, and which rows refused the file.
// Failed is never nil, so a client renders one shape on success and on
// refusal alike.
type CSVImportReport struct {
	Inserted int              `json:"inserted"`
	Skipped  int              `json:"skipped"`
	Failed   []CSVImportIssue `json:"failed"`
}

type exportDatastoreRowsInput struct {
	ID string `path:"id" minLength:"1" doc:"Datastore identifier"`
	// IncludeSystemColumns adds id, createdAt and updatedAt around the user
	// columns. It defaults to exclusion: a sheet a person will edit and hand
	// back must not carry the system columns, while a backup must.
	IncludeSystemColumns bool `query:"includeSystemColumns" doc:"Include the id, createdAt and updatedAt columns"`
}

type importDatastoreRowsInput struct {
	ID string `path:"id" minLength:"1" doc:"Datastore identifier"`
	// RawBody carries the file as text/csv rather than base64-in-JSON, which
	// would inflate the payload by a third and hide the media type from the
	// generated clients.
	RawBody []byte `contentType:"text/csv" doc:"The CSV file: a header row naming user columns, then one record per row"`
}

type importDatastoreRowsOutput struct {
	Body CSVImportReport
}

// csvDecodedRow is one validated record ready for Insert, carrying its
// one-based file line number for refusals raised mid-import.
type csvDecodedRow struct {
	line   int
	values map[string]any
}

// registerDatastoreTransfer wires the CSV import and export beside the JSON
// row operations. The export pre-seeds its text/csv response the way the SSE
// adapter pre-seeds text/event-stream: a streamed Body function leaves no
// schema for Huma to infer, so the document states the media type itself.
func (handler *Datastores) registerDatastoreTransfer(api huma.API) {
	export := huma.Operation{
		OperationID: "export-datastore-rows",
		Method:      http.MethodGet,
		Path:        "/datastores/{id}/rows/export",
		Summary:     "Export rows as CSV",
		Description: "Streams the datastore's rows as RFC 4180 CSV in id order, one header row plus one record per row.",
		Tags:        []string{"Datastore rows"},
		Responses: map[string]*huma.Response{
			"200": {
				Description: "The datastore's rows as RFC 4180 CSV.",
				Content: map[string]*huma.MediaType{
					"text/csv": {Schema: &huma.Schema{Type: "string", Format: "binary"}},
				},
			},
		},
	}
	huma.Register(api, export, handler.ExportRows)
	huma.Register(api, huma.Operation{
		OperationID:  "import-datastore-rows",
		Method:       http.MethodPost,
		Path:         "/datastores/{id}/rows/import",
		Summary:      "Import rows from CSV",
		Description:  "Validates every record before writing any row: a file with a failed row imports nothing and reports each failure with its line number.",
		Tags:         []string{"Datastore rows"},
		MaxBodyBytes: csvImportMaxBodyBytes,
	}, handler.ImportRows)
}

// ExportRows streams the datastore's rows as CSV in id order. The header is
// exactly the user columns in catalogue order, with the system columns
// wrapped around them when asked for. Pages of at most csvExportPageSize rows
// cross the single SQLite connection one at a time instead of one large
// query holding it for the whole table.
func (handler *Datastores) ExportRows(ctx context.Context, input *exportDatastoreRowsInput) (*huma.StreamResponse, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	tenant := handler.tenants.Resolve(ctx).ID
	definition, err := handler.store.GetDatastore(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	header := csvExportHeader(input.IncludeSystemColumns, definition.Columns)
	first, err := handler.store.List(ctx, tenant, input.ID, datastore.RowQuery{Limit: csvExportPageSize})
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	filename := "datastore-" + strings.ReplaceAll(input.ID, `"`, "") + ".csv"
	return &huma.StreamResponse{
		Body: func(stream huma.Context) {
			stream.SetHeader("Content-Type", "text/csv; charset=utf-8")
			stream.SetHeader("Content-Disposition", `attachment; filename="`+filename+`"`)
			writer := csv.NewWriter(stream.BodyWriter())
			if err := writer.Write(header); err != nil {
				return
			}
			page := first
			for {
				for _, row := range page.Rows {
					if err := writer.Write(csvExportRecord(header, row)); err != nil {
						return
					}
				}
				writer.Flush()
				if err := writer.Error(); err != nil {
					return
				}
				if page.NextCursor == "" {
					return
				}
				next, err := handler.store.List(ctx, tenant, input.ID, datastore.RowQuery{
					Cursor: page.NextCursor,
					Limit:  csvExportPageSize,
				})
				if err != nil {
					return
				}
				page = next
			}
		},
	}, nil
}

// ImportRows validates every record of an uploaded CSV file before writing
// any row. A file with a failed row imports nothing and answers 200 with the
// report naming each failure; only a file the header check cannot read at
// all — undecodable, or carrying a reserved or unknown column — is a 422
// that names the cause. A body past the byte limit never reaches here: Huma
// refuses it with a 413 naming the limit.
func (handler *Datastores) ImportRows(ctx context.Context, input *importDatastoreRowsInput) (*importDatastoreRowsOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("datastore storage unavailable")
	}
	if err := handler.ownsDatastore(ctx, input.ID); err != nil {
		return nil, err
	}
	tenant := handler.tenants.Resolve(ctx).ID
	definition, err := handler.store.GetDatastore(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	rows, skipped, failed, err := decodeCSVImport(input.RawBody, definition.Columns, handler.store.CurrentLimits().MaxValueBytes)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	if len(failed) > 0 {
		return &importDatastoreRowsOutput{Body: CSVImportReport{Failed: failed, Skipped: skipped}}, nil
	}
	for index, decoded := range rows {
		if _, err := handler.store.Insert(ctx, tenant, input.ID, decoded.values); err != nil {
			// Validation already passed, so only the row-count ceiling or a
			// concurrent writer can land here. The import is partial, and the
			// refusal says exactly how partial rather than reading as clean.
			return nil, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"csv import stopped at file line %d: %s; %d of %d rows were imported before the failure",
				decoded.line, err.Error(), index, len(rows)))
		}
	}
	return &importDatastoreRowsOutput{Body: CSVImportReport{Inserted: len(rows), Skipped: skipped, Failed: []CSVImportIssue{}}}, nil
}

// csvExportHeader is the download's first row: exactly the user columns in
// catalogue order, with id first and the timestamps last when the caller
// asked for the system columns — the same arrangement the grid renders.
func csvExportHeader(includeSystem bool, cols []datastore.ColumnDef) []string {
	header := make([]string, 0, len(cols)+3)
	if includeSystem {
		header = append(header, "id")
	}
	for _, col := range cols {
		header = append(header, col.Name)
	}
	if includeSystem {
		header = append(header, "createdAt", "updatedAt")
	}
	return header
}

// csvExportRecord renders one row against the header. Nil reads as the empty
// field; every other value renders in its column's own spelling so the file
// re-imports to the identical value.
func csvExportRecord(header []string, row datastore.Row) []string {
	record := make([]string, 0, len(header))
	for _, name := range header {
		record = append(record, csvExportCell(name, row[name]))
	}
	return record
}

// csvFormulaLeads reports whether text would be read as a formula rather than
// as text by Excel, LibreOffice or Sheets.
//
// A cell whose first character is =, +, - or @ is parsed as an expression, and
// TAB and CR are equally dangerous because a spreadsheet strips them during
// import and then sees the character behind them. Datastore rows are written by
// webhooks as often as by a person (BUG-fv5fer: a value arriving from an
// inbound request becomes an active formula the moment an operator opens the
// export), so the exported sheet — not the stored row — is where the trust
// boundary sits.
//
// The leading single quotes are skipped before the test, which is what makes
// the escaping below reversible: on the way back in, exactly one quote is
// stripped from text that csvFormulaLeads accepts, so "'=1+1" (a literal value
// starting with a quote) and "'=1+1" (the escaped spelling of "=1+1") cannot be
// confused with one another.
func csvFormulaLeads(text string) bool {
	for len(text) > 0 && text[0] == '\'' {
		text = text[1:]
	}
	if text == "" {
		return false
	}
	switch text[0] {
	case '=', '+', '-', '@', '\t', '\r':
		return true
	default:
		return false
	}
}

// csvNeutraliseCell prefixes the OWASP mitigation to any rendered cell a
// spreadsheet would otherwise evaluate.
//
// It runs on the final rendered text rather than inside the string branch, so
// there is exactly one place the rule lives and no branch can forget it: a
// negative number renders as "-5" and is escaped the same way a string is. The
// quote is an Excel text marker, not data — csvUnneutraliseCell removes it
// again on import — and what the row stores is never touched, because this runs
// on export only.
func csvNeutraliseCell(text string) string {
	if csvFormulaLeads(text) {
		return "'" + text
	}
	return text
}

// csvUnneutraliseCell reverses csvNeutraliseCell, so a datastore exported and
// re-imported holds the identical values.
//
// Without it the round trip is lossy in the direction that matters: a row
// holding "=1+1" or "-5" would come back as "'=1+1" or "'-5", and the numeric
// cell would not even import — "'-5" is not a number, so the whole file would be
// refused with one issue per negative value.
func csvUnneutraliseCell(cell string) string {
	if len(cell) > 1 && cell[0] == '\'' && csvFormulaLeads(cell[1:]) {
		return cell[1:]
	}
	return cell
}

func csvExportCell(name string, value any) string {
	return csvNeutraliseCell(csvRenderCell(value))
}

// csvRenderCell is one value in its column's own spelling: the text of a
// string, and for everything else the spelling the import path parses back.
func csvRenderCell(value any) string {
	if value == nil {
		return ""
	}
	switch typed := value.(type) {
	case string:
		return typed
	case []byte:
		// A raw driver value that slipped past normalisation still reads
		// as its text rather than Go's decimal byte list.
		return string(typed)
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case float32:
		return strconv.FormatFloat(float64(typed), 'f', -1, 64)
	case int64:
		return strconv.FormatInt(typed, 10)
	case int:
		return strconv.Itoa(typed)
	case time.Time:
		return typed.Format(csvTimestampLayout)
	default:
		return fmt.Sprintf("%v", value)
	}
}

// decodeCSVImport parses and validates a whole upload before any row is
// written. It returns the rows ready for Insert, the count of blank lines
// passed over, and one issue per failed record. Structural failures — no
// header, a reserved or unknown column — are errors; per-record failures are
// issues, so the caller reports every bad row in one answer instead of the
// first.
func decodeCSVImport(body []byte, cols []datastore.ColumnDef, maxValueBytes int) ([]csvDecodedRow, int, []CSVImportIssue, error) {
	// Excel writes UTF-8 CSV with a leading byte-order mark that encoding/csv
	// does not strip. Left in place it glues itself to the first header cell,
	// which then matches no column and shifts every value one place — with no
	// error anywhere, so the customer finds it in their own data weeks later.
	body = bytes.TrimPrefix(body, []byte("\xef\xbb\xbf"))
	reader := csv.NewReader(bytes.NewReader(body))
	reader.FieldsPerRecord = -1
	header, err := reader.Read()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, 0, nil, errors.New("csv import needs a header row naming the datastore's columns, but the file is empty")
		}
		return nil, 0, nil, fmt.Errorf("csv import cannot read the header row: %s", csvParseDetail(err))
	}
	targets := make([]datastore.ColumnDef, len(header))
	seen := map[string]string{}
	for index, cell := range header {
		if cell == "" {
			return nil, 0, nil, fmt.Errorf("csv import header column %d is empty: every header cell must name a column", index+1)
		}
		if canonical, reserved := csvReservedColumns[strings.ToLower(cell)]; reserved {
			return nil, 0, nil, fmt.Errorf("csv import header carries reserved column %q (system column %q): system columns are never imported", cell, canonical)
		}
		var match *datastore.ColumnDef
		for i := range cols {
			if strings.EqualFold(cols[i].Name, cell) {
				match = &cols[i]
				break
			}
		}
		if match == nil {
			return nil, 0, nil, fmt.Errorf("csv import header names unknown column %q", cell)
		}
		lowered := strings.ToLower(match.Name)
		if first, dup := seen[lowered]; dup {
			return nil, 0, nil, fmt.Errorf("csv import header names column %q twice (as %q and %q): one header cell per column", match.Name, first, cell)
		}
		seen[lowered] = cell
		targets[index] = *match
	}
	var rows []csvDecodedRow
	var failed []CSVImportIssue
	skipped := 0
	for {
		record, err := reader.Read()
		if err != nil {
			if errors.Is(err, io.EOF) {
				break
			}
			// A parse error names its own start line; the record is nil on
			// this path, so FieldPos has nothing to point at.
			line := 0
			var parseErr *csv.ParseError
			if errors.As(err, &parseErr) {
				line = parseErr.StartLine
			}
			return nil, 0, nil, fmt.Errorf("csv import cannot read file line %d: %s", line, csvParseDetail(err))
		}
		line := csvRecordLine(reader, record)
		if len(record) < len(header) && csvRecordBlank(record) {
			// A short line holding only spaces or delimiters is whitespace
			// noise, not a row. A full-width record of empty fields is
			// different: it is one row of empty values, which decodes to
			// NULLs (or the empty string) below. Truly empty lines never
			// reach this loop at all — encoding/csv skips them the way
			// every CSV tool does, trailing newline included.
			skipped++
			continue
		}
		values, issue := csvDecodeRecord(line, record, header, targets, maxValueBytes)
		if issue != nil {
			failed = append(failed, *issue)
			continue
		}
		rows = append(rows, csvDecodedRow{line: line, values: values})
	}
	if rows == nil {
		rows = []csvDecodedRow{}
	}
	if failed == nil {
		failed = []CSVImportIssue{}
	}
	return rows, skipped, failed, nil
}

// csvDecodeRecord validates one record against the header. A short record
// blames its first missing column, a long one the surplus, and a wrongly
// typed cell its own column — so every issue names the column a person must
// fix.
func csvDecodeRecord(line int, record, header []string, targets []datastore.ColumnDef, maxValueBytes int) (map[string]any, *CSVImportIssue) {
	fail := func(column, reason string) (map[string]any, *CSVImportIssue) {
		return nil, &CSVImportIssue{Line: line, Column: column, Severity: "blocking", Reason: reason}
	}
	if len(record) < len(header) {
		return fail(header[len(record)], fmt.Sprintf("row has %d values but the header names %d columns", len(record), len(header)))
	}
	if len(record) > len(header) {
		return fail(header[len(header)-1], fmt.Sprintf("row has %d values but the header names %d columns", len(record), len(header)))
	}
	values := make(map[string]any, len(header))
	for index, cell := range record {
		column := targets[index]
		// Undo the export's formula neutralisation before anything reads the
		// cell: the quote is a spreadsheet marker this API wrote, not part of
		// the value, and a typed column cannot parse its own spelling with the
		// marker still attached.
		cell = csvUnneutraliseCell(cell)
		if column.Type == datastore.ColumnString {
			if size := len(cell); size > maxValueBytes {
				return fail(column.Name, fmt.Sprintf("value is %d bytes, past the maximum %d", size, maxValueBytes))
			}
			values[column.Name] = cell
			continue
		}
		text := strings.TrimSpace(cell)
		if text == "" {
			// CSV cannot tell an empty field from a missing one. Numbers,
			// booleans and dates read it as NULL; strings keep the empty
			// text, which is the only spelling that round-trips.
			continue
		}
		switch column.Type {
		case datastore.ColumnNumber:
			parsed, err := strconv.ParseFloat(text, 64)
			if err != nil {
				return fail(column.Name, fmt.Sprintf("value %q is not a number", cell))
			}
			values[column.Name] = parsed
		case datastore.ColumnBoolean:
			parsed, ok := csvParseBool(text)
			if !ok {
				return fail(column.Name, fmt.Sprintf("value %q is not a boolean (true or false)", cell))
			}
			values[column.Name] = parsed
		case datastore.ColumnDate:
			parsed, err := csvParseDate(text)
			if err != nil {
				return fail(column.Name, fmt.Sprintf("value %q is not a date", cell))
			}
			values[column.Name] = parsed
		default:
			return fail(column.Name, fmt.Sprintf("column has unknown type %q", string(column.Type)))
		}
	}
	return values, nil
}

// csvRecordBlank reports whether a record is a blank line wearing a record's
// clothes: encoding/csv already skips truly empty lines, but a line holding
// only spaces or only delimiters still arrives, and counting it skipped is
// more honest than failing the file over whitespace.
func csvRecordBlank(record []string) bool {
	for _, field := range record {
		if strings.TrimSpace(field) != "" {
			return false
		}
	}
	return true
}

// csvRecordLine is the one-based physical line where a record starts.
// FieldPos panics on an empty record, so a record with no fields reads as
// line zero rather than taking the handler down with it.
func csvRecordLine(reader *csv.Reader, record []string) int {
	if len(record) == 0 {
		return 0
	}
	line, _ := reader.FieldPos(0)
	return line
}

// csvParseBool accepts the spellings the row editor's coercion accepts, so a
// sheet and the dialog agree about what a boolean looks like.
func csvParseBool(text string) (bool, bool) {
	switch strings.ToLower(text) {
	case "true", "1", "yes":
		return true, true
	case "false", "0", "no":
		return false, true
	default:
		return false, false
	}
}

func csvParseDate(text string) (time.Time, error) {
	for _, layout := range csvDateLayouts {
		if parsed, err := time.Parse(layout, text); err == nil {
			return parsed.UTC(), nil
		}
	}
	return time.Time{}, errors.New("not a date")
}

// csvParseDetail flattens a csv.ParseError to its bare message: the callers
// already name the line, and the error's own text would repeat it. Bare
// errors pass through untouched.
func csvParseDetail(err error) string {
	var parseErr *csv.ParseError
	if errors.As(err, &parseErr) {
		return parseErr.Err.Error()
	}
	return err.Error()
}
