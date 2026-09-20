package cli

import (
	"encoding/json"
	"fmt"
	"io"
	"strings"
	"text/tabwriter"
)

// Envelope is the single JSON document every verb writes to stdout in JSON
// mode. Its shape is the contract an agent parses; see docs/reference/cli.md.
type Envelope struct {
	OK    bool           `json:"ok"`
	Data  any            `json:"data,omitempty"`
	Error *ErrorEnvelope `json:"error,omitempty"`
	Meta  Meta           `json:"meta"`
}

// Meta describes the invocation, not the payload.
type Meta struct {
	Operation  string `json:"operation"`
	DurationMs int64  `json:"durationMs"`
}

// ErrorEnvelope is the failure half of the envelope.
type ErrorEnvelope struct {
	Code    string       `json:"code"`
	Message string       `json:"message"`
	Status  int          `json:"status,omitempty"`
	Detail  *ErrorDetail `json:"detail,omitempty"`
}

// ErrorDetail carries the server's own explanation, unmodified.
type ErrorDetail struct {
	Problem json.RawMessage `json:"problem,omitempty"`
	Body    string          `json:"body,omitempty"`
	// Execution is the record of a run that ended badly, for the one verb that
	// waits for a run rather than merely starting it.
	Execution json.RawMessage `json:"execution,omitempty"`
	// Issues is every problem a verb found, for the verb that collects them all
	// instead of stopping at the first.
	Issues json.RawMessage `json:"issues,omitempty"`
}

// outputMode is how one invocation renders its result.
type outputMode int

const (
	// modeHuman is text for a person reading a terminal.
	modeHuman outputMode = iota
	// modeJSON is the single envelope on stdout.
	modeJSON
	// modeQuiet is the primary identifier alone, for shell pipelines.
	modeQuiet
)

// resolveMode decides how to render, from the flags and whether stdout is a
// terminal.
//
// --json wins outright: a caller that asked for an envelope gets one even when
// --quiet is also set. Otherwise --quiet prints the identifier alone, even on
// a pipe where JSON mode would otherwise apply, because that is the point of
// --quiet. With neither flag, a pipe means an agent or a script is reading, so
// the envelope is the safe default.
func resolveMode(flags *GlobalFlags, tty bool) outputMode {
	switch {
	case flags != nil && flags.JSON:
		return modeJSON
	case flags != nil && flags.Quiet:
		return modeQuiet
	case !tty:
		return modeJSON
	default:
		return modeHuman
	}
}

// writeEnvelope writes one JSON document followed by a newline.
func writeEnvelope(w io.Writer, envelope Envelope) {
	body, err := json.Marshal(envelope)
	if err != nil {
		fmt.Fprintf(w, `{"ok":false,"error":{"code":"output_error","message":%q},"meta":{"operation":"","durationMs":0}}`+"\n", err.Error())
		return
	}
	fmt.Fprintf(w, "%s\n", body)
}

// printJSONValue renders a payload a verb has no human form for.
func printJSONValue(w io.Writer, data any) {
	body, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		fmt.Fprintf(w, "%v\n", data)
		return
	}
	fmt.Fprintf(w, "%s\n", body)
}

// printKV writes aligned label/value lines.
func printKV(w io.Writer, pairs [][2]string) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	for _, pair := range pairs {
		fmt.Fprintf(table, "%s\t%s\n", pair[0], pair[1])
	}
	_ = table.Flush()
}

// printTable writes a header and rows as aligned columns.
func printTable(w io.Writer, header []string, rows [][]string) {
	table := tabwriter.NewWriter(w, 0, 0, 2, ' ', 0)
	fmt.Fprintln(table, strings.Join(header, "\t"))
	for _, row := range rows {
		fmt.Fprintln(table, strings.Join(row, "\t"))
	}
	_ = table.Flush()
}
