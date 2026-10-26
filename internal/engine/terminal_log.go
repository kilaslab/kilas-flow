package engine

import (
	"context"
	"encoding/json"
	"log/slog"
	"strings"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/events"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// The levels ServiceDeps.ExecutionLogLevel accepts.
//
// They are thresholds on the terminal line, not levels the line is written at:
// a failed run is always WARN and every other terminal status INFO, so an
// operator's alert on level=WARN keeps meaning "a run failed" whatever else is
// configured.
const (
	// ExecutionLogInfo writes a line for every terminal execution. The default.
	ExecutionLogInfo = "info"
	// ExecutionLogWarn writes a line for failed executions only.
	ExecutionLogWarn = "warn"
	// ExecutionLogOff writes no terminal line.
	ExecutionLogOff = "off"
)

// maxLoggedErrorRunes caps the error text in a terminal line. A node can fail
// with a whole response body in its message, and one line per failed run must
// stay a line.
const maxLoggedErrorRunes = 1024

// normalizeExecutionLogLevel maps the configured value to one of the three
// levels. An unset or unknown value is the default rather than a silent
// switch-off: the config layer refuses a misspelling at startup, and a library
// caller that passes nothing gets the line.
func normalizeExecutionLogLevel(level string) string {
	switch strings.ToLower(strings.TrimSpace(level)) {
	case ExecutionLogWarn:
		return ExecutionLogWarn
	case ExecutionLogOff:
		return ExecutionLogOff
	default:
		return ExecutionLogInfo
	}
}

// settled announces a terminal execution: the log line, then the live event.
//
// This is the one place an execution that has reached a terminal state is
// reported, and every path that ends one goes through it — a worker finishing a
// run (manual, webhook, schedule, poll and retry runs are all claimed from the
// same queue and finished by the same runOnce), an inline sub-workflow call, a
// persistence failure, a queued or waiting run cancelled before any worker saw
// it, and a wait the sweeper expired. The trigger only decides how a run is
// queued; how it ends is this. It takes the record the store returned after the
// terminal write, so the line reports what was persisted — a cancellation that
// won the race with a finishing run is logged as cancelled, not as the success
// the worker meant to write.
//
// data is the event payload the caller always sent: the output of a run that
// completed, the error of one that did not.
func (service *Service) settled(ctx context.Context, record execution.Record, workflowName string, data json.RawMessage) {
	service.announce(ctx, record, workflowName, data, true)
}

// announce is settled with the line optional: a repeated cancel request for a
// run that is already cancelled re-sends the live event, as it always has,
// without writing a second line for one execution.
func (service *Service) announce(ctx context.Context, record execution.Record, workflowName string, data json.RawMessage, writeLine bool) {
	if writeLine {
		service.logTerminal(ctx, record, workflowName)
	}
	service.publishTerminal(record, data)
}

// crashSettlements is what a store offers when it can fail an execution as
// crashed inside its own claim (see repository.GORMExecutionStore.OnCrashSettled).
type crashSettlements interface {
	OnCrashSettled(func(execution.Record))
}

// watchCrashSettlements logs the executions the store ends as crashed.
//
// A run whose worker died past the reclaim cap is failed inside ClaimNext and
// skipped, so it never reaches runOnce and none of the paths above sees it.
// The store reports each one after its transaction commits; it is logged here
// and deliberately not published, as it never was. A store without the hook
// (a test fake) has no such transition to report.
func (service *Service) watchCrashSettlements() {
	store, ok := service.executions.(crashSettlements)
	if !ok {
		return
	}
	store.OnCrashSettled(func(record execution.Record) {
		service.logTerminal(context.Background(), record, "")
	})
}

// publishTerminal delivers the live event for a terminal execution.
func (service *Service) publishTerminal(record execution.Record, data json.RawMessage) {
	eventType := events.ExecutionCompleted
	switch record.Status {
	case execution.StatusFailed:
		eventType = events.ExecutionFailed
	case execution.StatusCancelled:
		eventType = events.ExecutionCancelled
	}
	service.publish(events.Event{
		TenantID: record.TenantID, ExecutionID: record.ID, WorkflowID: record.WorkflowID,
		Type: eventType, Status: record.Status, Data: data,
	})
}

// logTerminal writes the one structured line a terminal execution leaves in the
// server log: operators alert on logs, and a failed run used to leave none.
//
// It carries identifiers, the status, the failing node and the error, and
// nothing a run processed: no input, no output, no item. The error text is the
// one already on the persisted record, which the runner scrubbed of every
// secret the failing node resolved at the one boundary a node error crosses
// (see resolvedSecrets), so the log never holds a credential value the
// execution page does not.
func (service *Service) logTerminal(ctx context.Context, record execution.Record, workflowName string) {
	// Counted before the level switch, deliberately: the log level chooses
	// which lines appear, and the series are not lines — turning metrics off
	// with log.execution_level would be a switch labelled something else.
	if service.metrics != nil {
		service.metrics.ExecutionSettled(record.TenantID, string(record.Trigger), string(record.Status))
	}
	level := slog.LevelInfo
	if record.Status == execution.StatusFailed {
		level = slog.LevelWarn
	}
	switch service.executionLogLevel {
	case ExecutionLogOff:
		return
	case ExecutionLogWarn:
		if level < slog.LevelWarn {
			return
		}
	}
	if workflowName == "" {
		workflowName = service.workflowName(ctx, record)
	}
	attrs := []any{
		"status", string(record.Status),
		"execution", record.ID,
		"workflow", record.WorkflowID,
	}
	if workflowName != "" {
		attrs = append(attrs, "workflowName", workflowName)
	}
	attrs = append(attrs, "tenant", record.TenantID, "trigger", string(record.Trigger))
	if record.ParentExecutionID != "" {
		attrs = append(attrs, "parentExecution", record.ParentExecutionID)
	}
	if record.FinishedAt != nil && !record.StartedAt.IsZero() {
		attrs = append(attrs, "durationMs", record.FinishedAt.Sub(record.StartedAt).Milliseconds())
	}
	if record.Status == execution.StatusFailed {
		if nodeID := failingNode(record); nodeID != "" {
			attrs = append(attrs, "node", nodeID)
		}
	}
	if code, message := recordedError(record.Error); code != "" || message != "" {
		if code != "" {
			attrs = append(attrs, "errorCode", code)
		}
		if message != "" {
			attrs = append(attrs, "error", message)
		}
	}
	service.log.Log(ctx, level, "execution finished", attrs...)
}

// workflowName reads the name of the workflow a record belongs to when the
// caller had no document to take it from, which is a run cancelled or expired
// without a worker. It uses the active revision, so a workflow that was never
// activated, or that was renamed since the run, reads as unnamed or as its
// current name: the id is always on the line, and the name is a convenience.
func (service *Service) workflowName(ctx context.Context, record execution.Record) string {
	document, err := service.executions.ActiveDocument(ctx, repository.TenantScope{ID: record.TenantID}, record.WorkflowID)
	if err != nil {
		return ""
	}
	return document.Name
}

// failingNode is the node a failed execution stopped on: the last node run
// that failed. A node whose failure was tolerated also leaves a failed row, so
// the last one is the failure that ended the run, not an earlier one it
// survived. Empty when the run failed outside any node (a timeout between
// nodes, a trace that could not be persisted).
func failingNode(record execution.Record) string {
	var node string
	last := -1
	for _, run := range record.NodeRuns {
		if run.Status == execution.StatusFailed && run.Sequence >= last {
			node, last = run.NodeID, run.Sequence
		}
	}
	return node
}

// recordedError reads the code and message of a persisted execution error.
func recordedError(raw json.RawMessage) (code, message string) {
	if len(raw) == 0 || string(raw) == "null" {
		return "", ""
	}
	var decoded struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if err := json.Unmarshal(raw, &decoded); err != nil {
		return "", ""
	}
	return decoded.Code, truncateRunes(decoded.Message, maxLoggedErrorRunes)
}

func truncateRunes(text string, limit int) string {
	if utf8.RuneCountInString(text) <= limit {
		return text
	}
	runes := []rune(text)
	return string(runes[:limit]) + "..."
}
