package jsrun

import (
	"errors"
	"fmt"
	"strings"
	"time"
)

// The named failures of one run.
//
// The limits mean different things to whoever has to fix them, so each is a
// sentinel that errors.Is answers, inside a LimitError whose Detail is the
// sentence a user reads. The wording follows internal/runcode's, so the two
// Code nodes describe the same limit in the same words.
var (
	// ErrTimeLimit reports that the user's program ran past its time limit.
	ErrTimeLimit = errors.New("code exceeded its time limit")
	// ErrMemoryLimit reports that the server's memory ceiling for scripts was
	// reached while this script ran.
	ErrMemoryLimit = errors.New("code exceeded its memory limit")
	// ErrOutputLimit reports that the returned items are larger than allowed.
	ErrOutputLimit = errors.New("code produced more output than allowed")
	// ErrInputLimit reports that the node's input is larger than a script may
	// be handed. It is decided before any VM exists.
	ErrInputLimit = errors.New("code was given more input than allowed")
	// ErrHostCallLimit reports that the script called into the host, for
	// example to make an HTTP request, more often than allowed.
	ErrHostCallLimit = errors.New("code made more host calls than allowed")
	// ErrCallDepth reports runaway recursion. Unlike V8's RangeError, goja's
	// stack overflow cannot be caught by the script.
	ErrCallDepth = errors.New("code called functions nested deeper than allowed")
	// ErrInvalidReturn reports a returned value that is not items.
	ErrInvalidReturn = errors.New("code returned something that is not a list of items")
	// ErrNeverSettles reports a script waiting on a promise that nothing can
	// ever settle, such as `await new Promise(() => {})`.
	ErrNeverSettles = errors.New("code waits for something that can never finish")
	// ErrUnsupported reports a construct this runtime cannot run faithfully.
	ErrUnsupported = errors.New("code uses something this server does not run")
	// ErrEngineFault reports a failure inside the JavaScript engine itself.
	// It fails the run and never the server.
	ErrEngineFault = errors.New("the JavaScript engine failed")
)

// LimitError is a named failure: Detail is what a user reads, and Cause is
// the sentinel errors.Is matches.
type LimitError struct {
	Detail string
	Cause  error
}

func (e *LimitError) Error() string { return e.Detail }

func (e *LimitError) Unwrap() error { return e.Cause }

func named(cause error, detail string) error {
	return &LimitError{Detail: detail, Cause: cause}
}

// timedOut is the one time-limit failure, worded as runcode words it.
func timedOut(limit time.Duration) error {
	return named(ErrTimeLimit, fmt.Sprintf("code exceeded its %s time limit", limit))
}

// outOfMemory is the one memory-limit failure.
func outOfMemory(ceiling uint64) error {
	return named(ErrMemoryLimit, fmt.Sprintf(
		"code was stopped because the server's memory for scripts reached its %s ceiling", byteSize(int64(ceiling))))
}

// TimeLimitError, MemoryLimitError and EngineFaultError are the failures a
// worker pool reports when it had to stop a worker process itself, in the
// words the runtime uses when it stops a script.
func TimeLimitError(limit time.Duration) error { return timedOut(limit) }

// MemoryLimitError: see TimeLimitError.
func MemoryLimitError(ceiling uint64) error { return outOfMemory(ceiling) }

// EngineFaultError: see TimeLimitError. detail says what happened to the
// engine.
func EngineFaultError(detail string) error {
	return named(ErrEngineFault, fmt.Sprintf("the JavaScript engine failed while running this code (%s); this is a fault in the server, not in the code", detail))
}

// ScriptError is an error the user's code threw, located in the user's own
// coordinates: line 1 is the first line of the code as written in the node.
type ScriptError struct {
	// Name is the error's name, such as TypeError. It is empty when the code
	// threw something that is not an Error.
	Name    string
	Message string
	// Line and Column are 1-based, and 0 when the position is unknown.
	Line, Column int
	// ItemIndex is the item being processed in "Run once for each item"
	// mode, and -1 otherwise.
	ItemIndex int
	// Stack lists the user's own frames as "line:column", innermost first.
	// Frames inside the runtime and its libraries are left out.
	Stack []string
	// Uncaught marks an error nothing could catch: a callback's throw or a
	// promise rejected with no handler while the code was still running. It
	// ends the whole run, as it ends n8n's task runner, and belongs to no
	// item.
	Uncaught bool
}

func (e *ScriptError) Error() string {
	var text strings.Builder
	if e.Uncaught {
		text.WriteString("Uncaught ")
	}
	if e.Name != "" {
		text.WriteString(e.Name)
		text.WriteString(": ")
	}
	text.WriteString(e.Message)
	text.WriteString(location(e.Line, e.ItemIndex))
	return text.String()
}

// location renders where a failure happened, in the user's terms.
func location(line, item int) string {
	switch {
	case line > 0 && item >= 0:
		return fmt.Sprintf(" [line %d, for item %d]", line, item)
	case line > 0:
		return fmt.Sprintf(" [line %d]", line)
	case item >= 0:
		return fmt.Sprintf(" [for item %d]", item)
	}
	return ""
}

// SyntaxError is code that does not parse, located in the user's coordinates.
type SyntaxError struct {
	Message      string
	Line, Column int
}

func (e *SyntaxError) Error() string {
	if e.Line <= 0 {
		return "SyntaxError: " + e.Message
	}
	return fmt.Sprintf("SyntaxError: %s [line %d, column %d]", e.Message, e.Line, e.Column)
}

// Unsupported is one construct the runtime refuses, and where it is.
type Unsupported struct {
	// Subject completes "this node's code …", for example
	// `uses an async generator`.
	Subject string
	// Line is the user's line, or 0 when it applies to the whole body.
	Line int
}

// UnsupportedError carries every refused construct in a body. Its message is
// the first one's Refusal; the rest are there for a caller that lists them.
type UnsupportedError struct {
	Found []Unsupported
}

func (e *UnsupportedError) Error() string {
	first := e.Found[0]
	subject := first.Subject
	if first.Line > 0 {
		subject = fmt.Sprintf("%s (line %d)", subject, first.Line)
	}
	return Refusal(subject, UnsupportedAdvice)
}

func (e *UnsupportedError) Is(target error) bool { return target == ErrUnsupported }

// UnsupportedAdvice is what to do about a construct the runtime refuses.
const UnsupportedAdvice = "Rewrite that part of the code, or do the same work with native nodes."

// Refusal is the one sentence for code this server will not run, whatever
// the reason: a language it has no runtime for, a construct its JavaScript
// engine cannot run faithfully, a module it does not ship, or a runtime the
// operator turned off. The importer, a node's validation and a run all use
// it, so a user never sees three wordings for one situation and concludes
// they are three different problems.
//
// subject completes "this node's code …", for example "is written in
// Python" or "requires the module \"fs\"".
func Refusal(subject, alternative string) string {
	return fmt.Sprintf("this node's code %s, which this server does not run. %s", subject, alternative)
}
