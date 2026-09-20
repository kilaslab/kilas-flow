package cli

import (
	"encoding/json"
	"fmt"
	"net/http"
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
	// ExitRefused means the caller's authority was refused: a 403, a 401, or a
	// guarded verb without --yes. Error codes tell the three apart.
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
		return ExitRefused, "scope_denied"
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
