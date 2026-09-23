package jsrun

import (
	"context"
	"errors"
	"time"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Engine runs Code-node JavaScript: a Runner in this process, or a pool of
// worker processes that each hold one.
type Engine interface {
	Run(ctx context.Context, task Task) (Result, error)
}

// Job is a task prepared by Runner.Prepare: only data, so it can cross into a
// worker process whole. What the code asks of the server while it runs is
// answered by the Host that Prepare returns alongside it.
type Job struct {
	Source string `json:"source"`
	Mode   Mode   `json:"mode"`
	// Limits are the task's, already tightened to the preparing runner's
	// ceiling. The runner that executes the job tightens them again to its
	// own.
	Limits Limits `json:"limits"`
	// Input is the items as the VM parses them, encoded once by Prepare. It
	// is the bulk of a job, so it travels beside the rest rather than inside
	// it.
	Input string `json:"-"`
	// Roots are the roots' data; their functions stay with the Host.
	Roots Roots `json:"roots"`
	// FileIDs are the IDs of the input's files, the only ones a returned item
	// may pass on.
	FileIDs []string `json:"fileIds,omitempty"`
	// ContinueOnItemError is Task.ContinueOnItemError.
	ContinueOnItemError bool `json:"continueOnItemError,omitempty"`
	// Origins are the input items' origins by index, and Files the input's
	// files by ID: what Finish decodes the result against. They never leave
	// the process that prepared the job; a worker sees only the count.
	Origins []*workflow.PairedItem        `json:"-"`
	Files   map[string]workflow.BinaryRef `json:"-"`
	// Count is how many input items the job has, for a worker, which has no
	// Origins.
	Count int `json:"count"`
}

// Executed is what Execute produced, before Finish decodes it: the code's
// results as the JSON it returned, which is exactly what the output limit
// measured, and what it printed.
type Executed struct {
	// Outputs are the JSON results of the code's calls, in order: one in
	// all-items mode, one per item in per-item mode, "" for an item that
	// failed when the job went on past failed items. They are the bulk of a
	// result, so they travel beside the rest rather than inside it.
	Outputs []string `json:"-"`
	// Failures are the items that failed when the job went on past them.
	Failures         []ItemFailure `json:"failures,omitempty"`
	Console          []ConsoleLine `json:"console,omitempty"`
	ConsoleTruncated bool          `json:"consoleTruncated,omitempty"`
	UserTime         time.Duration `json:"userTime"`
}

// ItemFailure is one item that failed in a job that went on past it.
type ItemFailure struct {
	Index int        `json:"index"`
	Error *WireError `json:"error"`
}

// WireError is a run's failure as it crosses from a worker process: enough
// to rebuild an error with the same text that errors.Is and errors.As answer
// the same way. Kind says which fields are set.
type WireError struct {
	// Kind is "limit" (a LimitError, Cause naming its sentinel), "script",
	// "syntax", "unsupported" or "other". A cancellation is never among
	// them: only the server's own context cancels a run.
	Kind string `json:"kind"`
	// Text is the whole error's text when a wrapped error extends it, and
	// always for "other"; otherwise the kind's fields say it.
	Text  string `json:"text,omitempty"`
	Cause string `json:"cause,omitempty"`
	// Detail is a LimitError's own text.
	Detail string `json:"detail,omitempty"`
	// Script, Syntax and Unsupported carry those errors' fields.
	Script      *ScriptError  `json:"script,omitempty"`
	Syntax      *SyntaxError  `json:"syntax,omitempty"`
	Unsupported []Unsupported `json:"unsupported,omitempty"`
}

// sentinels are the named failures a LimitError may carry, by the name that
// crosses the pipe.
var sentinels = []struct {
	name string
	err  error
}{
	{"time", ErrTimeLimit}, {"memory", ErrMemoryLimit}, {"output", ErrOutputLimit}, {"input", ErrInputLimit},
	{"hostCalls", ErrHostCallLimit}, {"callDepth", ErrCallDepth}, {"invalidReturn", ErrInvalidReturn},
	{"neverSettles", ErrNeverSettles}, {"unsupported", ErrUnsupported}, {"engine", ErrEngineFault},
}

// EncodeError describes err for the pipe, or returns nil for a nil error.
func EncodeError(err error) *WireError {
	if err == nil {
		return nil
	}
	wire := &WireError{Kind: "other", Text: err.Error()}
	var script *ScriptError
	var syntax *SyntaxError
	var unsupported *UnsupportedError
	var limit *LimitError
	switch {
	case errors.As(err, &script):
		wire.Kind, wire.Script = "script", script
	case errors.As(err, &syntax):
		wire.Kind, wire.Syntax = "syntax", syntax
	case errors.As(err, &unsupported):
		wire.Kind, wire.Unsupported = "unsupported", unsupported.Found
	case errors.As(err, &limit):
		wire.Kind, wire.Detail = "limit", limit.Detail
		wire.Cause = sentinelName(limit.Cause)
	default:
		// A sentinel wrapped some other way still crosses as one.
		if name := sentinelName(err); name != "" {
			wire.Kind, wire.Detail, wire.Cause = "limit", err.Error(), name
		}
	}
	if inner := wire.inner(); inner != nil && inner.Error() == wire.Text {
		wire.Text = ""
	}
	return wire
}

// Decode rebuilds the error. Its text is the original's, and errors.Is and
// errors.As find the same sentinels and types in it.
func (wire *WireError) Decode() error {
	if wire == nil {
		return nil
	}
	inner := wire.inner()
	switch {
	case inner == nil:
		return errors.New(wire.Text)
	case wire.Text == "" || inner.Error() == wire.Text:
		return inner
	}
	return &wrappedError{text: wire.Text, inner: inner}
}

// inner is the error the kind's fields describe, or nil for "other".
func (wire *WireError) inner() error {
	switch wire.Kind {
	case "script":
		if wire.Script != nil {
			script := *wire.Script
			return &script
		}
	case "syntax":
		if wire.Syntax != nil {
			syntax := *wire.Syntax
			return &syntax
		}
	case "unsupported":
		if len(wire.Unsupported) > 0 {
			return &UnsupportedError{Found: wire.Unsupported}
		}
	case "limit":
		return &LimitError{Detail: wire.Detail, Cause: sentinelByName(wire.Cause)}
	}
	return nil
}

func sentinelName(err error) string {
	for _, sentinel := range sentinels {
		if errors.Is(err, sentinel.err) {
			return sentinel.name
		}
	}
	return ""
}

// sentinelByName is the sentinel a name crossed as, or nil for a LimitError
// whose cause was none of them.
func sentinelByName(name string) error {
	for _, sentinel := range sentinels {
		if sentinel.name == name {
			return sentinel.err
		}
	}
	return nil
}

// wrappedError keeps a wrapped error's own text around the error it wraps.
type wrappedError struct {
	text  string
	inner error
}

func (e *wrappedError) Error() string { return e.text }

func (e *wrappedError) Unwrap() error { return e.inner }
