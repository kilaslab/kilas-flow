package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
)

// Exit codes. They are the machine-readable half of the output contract: an
// agent that reads only the exit code still learns whether it should fix the
// invocation, stop and ask, wait, or report.
const (
	// ExitOK is success.
	ExitOK = 0
	// ExitError is a server, network or shape failure: report, do not retry
	// blindly.
	ExitFailure = 1
	// ExitUsage means the invocation itself was wrong.
	ExitUsage = 2
	// ExitRefused means the caller's authority was refused: a 403 or 401 from
	// the server, or a guarded verb refused by the CLI's own gates — without
	// --yes, or by a scoped token that may not do it. Error codes tell those
	// apart.
	ExitRefused = 3
	// ExitNotFound means the resource does not exist for this tenant.
	ExitNotFound = 4
	// ExitConflict means optimistic concurrency or an idempotency mismatch:
	// re-read, then decide.
	ExitConflict = 5
	// ExitNotReady means the instance cannot serve yet (503) or is throttling
	// (429): wait, then retry.
	ExitNotReady = 6
)

// ExitCodeDoc is one row of the exit-code contract as the docs state it.
type ExitCodeDoc struct {
	// Code is the process exit code.
	Code int
	// Meaning says what the code reports, in markdown.
	Meaning string
	// Response says what a caller should do on seeing it.
	Response string
}

// ExitCodes is the contract's list of exit codes, in order. It is the one place
// the meaning and the advised response are written down: reference/cli.md
// carries a rendering of it (RenderExitCodeReference), and a test fails when
// the page and this table differ or when an Exit* constant has no row.
func ExitCodes() []ExitCodeDoc {
	return []ExitCodeDoc{
		{ExitOK, "success", "continue"},
		{ExitFailure, "error (5xx, network, unexpected shape)", "report, do not retry blindly"},
		{ExitUsage, "usage error", "fix the invocation"},
		{ExitRefused, "**refused by authority** (`403`, `401`, a guarded verb without `--yes`, or a guarded verb on a scoped token)", "do not retry; ask the user or drop the step"},
		{ExitNotFound, "not found (`404`)", "the resource does not exist for this tenant"},
		{ExitConflict, "conflict (`409`) — optimistic concurrency, idempotency mismatch", "re-read, then decide"},
		{ExitNotReady, "not ready (`503`, or `429`) — migrations outstanding, subsystem unconfigured, throttled", "wait, then retry"},
	}
}

// StatusExit is one row of the mapping from an HTTP status to the exit code and
// error code a failed request gets.
type StatusExit struct {
	// Statuses is the status, or the statuses that share the row, in markdown.
	Statuses string
	// Exit is the process exit code.
	Exit int
	// ErrCode is error.code, or "" for a success.
	ErrCode string
}

// documentedStatuses are the statuses the contract names, in the order the
// table lists them. Every other status falls to the "other" rows below them.
var documentedStatuses = []int{
	http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent,
	http.StatusBadRequest, http.StatusUnauthorized, http.StatusForbidden, http.StatusNotFound,
	http.StatusConflict, http.StatusUnprocessableEntity, http.StatusTooManyRequests, http.StatusServiceUnavailable,
}

// StatusExits is the status mapping, built by asking exitForStatus rather than
// by restating it. Statuses that map to the same exit and code share a row.
func StatusExits() []StatusExit {
	type outcome struct {
		exit    int
		errCode string
	}
	rows := make([]StatusExit, 0, len(documentedStatuses)+2)
	labels := make([][]string, 0, len(documentedStatuses))
	seen := map[outcome]int{}

	for _, status := range documentedStatuses {
		exit, errCode := exitForStatus(status)
		key := outcome{exit, errCode}
		position, shared := seen[key]
		if !shared {
			position = len(rows)
			seen[key] = position
			rows = append(rows, StatusExit{Exit: exit, ErrCode: errCode})
			labels = append(labels, nil)
		}
		labels[position] = append(labels[position], fmt.Sprintf("`%d`", status))
	}
	for position := range rows {
		rows[position].Statuses = strings.Join(labels[position], ", ")
	}

	// 500 and 418 stand for the two fallbacks exitForStatus has: any other server
	// error, and any status the contract does not name at all.
	otherServer, otherServerCode := exitForStatus(http.StatusInternalServerError)
	other, otherCode := exitForStatus(http.StatusTeapot)

	return append(rows,
		StatusExit{Statuses: "any other `5xx`", Exit: otherServer, ErrCode: otherServerCode},
		StatusExit{Statuses: "any other status", Exit: other, ErrCode: otherCode},
	)
}

// refusal is one of the failures that share exit code 3, told apart by
// error.code.
type refusal struct {
	ErrCode string
	Means   string
}

// refusals are the three conditions behind exit code 3. The codes come from the
// same place the CLI raises them from: the two statuses exitForStatus maps, and
// the guard's own constant.
func refusals() []refusal {
	_, forbidden := exitForStatus(http.StatusForbidden)
	_, unauthorised := exitForStatus(http.StatusUnauthorized)

	return []refusal{
		{forbidden, "a `403`, or a guarded verb called with a scoped agent token: this credential may not do this, whatever the flags"},
		{unauthorised, "a `401`: no usable credential was presented"},
		{confirmationCode, "a guarded verb was called without `--yes`"},
	}
}

// RenderExitCodeReference is the markdown reference/cli.md holds between its
// generated:exit-codes markers: the exit-code table, the status mapping, and the
// three refusals that share code 3. The prose around it is hand-written.
func RenderExitCodeReference() string {
	var out strings.Builder

	out.WriteString("| Code | Meaning | Agent response |\n| --- | --- | --- |\n")
	for _, row := range ExitCodes() {
		fmt.Fprintf(&out, "| %d | %s | %s |\n", row.Code, row.Meaning, row.Response)
	}

	out.WriteString("\nThe mapping from an HTTP status to the exit code and `error.code` is part of the contract:\n\n")
	out.WriteString("| HTTP status | Exit | `error.code` |\n| --- | --- | --- |\n")
	for _, row := range StatusExits() {
		code := "—"
		if row.ErrCode != "" {
			code = "`" + row.ErrCode + "`"
		}
		fmt.Fprintf(&out, "| %s | %d | %s |\n", row.Statuses, row.Exit, code)
	}

	fmt.Fprintf(&out, "\nCode %d is the important one, because it is not one condition. "+
		"Three different refusals share it and are told apart by `error.code`:\n\n", ExitRefused)
	out.WriteString("| `error.code` | Means |\n| --- | --- |\n")
	for _, row := range refusals() {
		fmt.Fprintf(&out, "| `%s` | %s |\n", row.ErrCode, row.Means)
	}

	return strings.TrimRight(out.String(), "\n")
}

// ExitError is the CLI's failure value. A verb returns it (or an error that
// wraps one) to choose the process exit code and the error envelope.
type ExitError struct {
	// Code is the process exit code.
	Code int
	// ErrCode is the stable machine-readable name in error.code.
	ErrCode string
	// Message is the one-line human-readable reason.
	Message string
	// Status is the HTTP status, when the failure came from a response.
	Status int
	// Problem is the RFC 9457 problem document, carried verbatim.
	Problem json.RawMessage
	// Body is a non-JSON error body, truncated to maxErrorBodyBytes.
	Body string
	// Execution is the record of a run that ended badly. It is what
	// `run --wait` carries so a caller learns why the run failed from the same
	// document as the exit code, without a second request.
	Execution json.RawMessage
	// Issues is a verb's complete list of problems, for the one verb that
	// reports every problem at once rather than the first: a failure whose
	// message could only ever name one of them must still carry them all.
	Issues json.RawMessage
}

// Error implements error.
func (e *ExitError) Error() string { return e.Message }

// usageError builds the failure a bad invocation produces: exit 2, code
// "usage".
func usageError(format string, args ...any) *ExitError {
	return &ExitError{Code: ExitUsage, ErrCode: "usage", Message: fmt.Sprintf(format, args...)}
}

// outputWriteError builds the failure a refused write of the caller's own
// output produces: exit 1, code "output_error".
//
// The invocation was right and the environment would not take it — a full
// disk, a read-only mount, a directory the process may not write — which is
// exit 1, "report, do not retry blindly", not the exit 2 a caller reads as
// "fix the invocation". configWriteError classifies the same class the same
// way for the configuration file.
func outputWriteError(format string, args ...any) *ExitError {
	return &ExitError{Code: ExitFailure, ErrCode: "output_error", Message: fmt.Sprintf(format, args...)}
}

// scopeDeniedCode is the error.code a refusal by authority carries.
//
// Two things produce it: the server's 403, mapped by exitForStatus, and the
// CLI's own authority gate, which refuses a guarded verb for a scoped agent
// token before the operation is attempted (see requireAuthority). They share
// the name because they mean the same thing to a caller — "I may not do this,
// however I ask" — and the same exit code, 3, that the confirmation refusal
// uses.
const scopeDeniedCode = "scope_denied"

// exitForStatus maps an HTTP status onto the exit code and error code contract.
//
// 400 and 422 both mean "fix the invocation"; 401 and 403 both mean "you may
// not", and the error code is what separates them. Statuses the contract does
// not name fall back to ExitFailure, because reporting an unnamed status is
// better than pretending it is one the caller has a rule for.
func exitForStatus(status int) (int, string) {
	switch status {
	case http.StatusOK, http.StatusCreated, http.StatusAccepted, http.StatusNoContent:
		return ExitOK, ""
	case http.StatusBadRequest, http.StatusUnprocessableEntity:
		return ExitUsage, "bad_request"
	case http.StatusUnauthorized:
		return ExitRefused, "unauthenticated"
	case http.StatusForbidden:
		return ExitRefused, scopeDeniedCode
	case http.StatusNotFound:
		return ExitNotFound, "not_found"
	case http.StatusConflict:
		return ExitConflict, "conflict"
	case http.StatusTooManyRequests:
		return ExitNotReady, "rate_limited"
	case http.StatusServiceUnavailable:
		return ExitNotReady, "not_ready"
	}

	switch {
	case status >= 500:
		return ExitFailure, "server_error"
	case status >= 200 && status < 300:
		return ExitOK, ""
	default:
		return ExitFailure, "http_error"
	}
}
