// Package execution owns durable workflow-run and node-run records.
package execution

import (
	"encoding/json"
	"time"
)

// Status is shared by executions and node runs.
type Status string

const (
	StatusQueued     Status = "queued"
	StatusRunning    Status = "running"
	StatusCancelling Status = "cancelling"
	// StatusWaiting is a suspended execution: it holds no worker and no
	// lease, and ClaimNext cannot see it until a resume re-queues it.
	// Non-terminal, so the live event feed stays open across the wait
	// rather than closing and forcing a reconnect.
	StatusWaiting   Status = "waiting"
	StatusSucceeded Status = "succeeded"
	// StatusSkipped is a node the runner never invoked because no incoming item
	// channel delivered anything — the untaken arm of a branch. It is neither a
	// success nor a failure, and recording it as either would misread a pruned
	// branch as one that ran.
	StatusSkipped   Status = "skipped"
	StatusFailed    Status = "failed"
	StatusCancelled Status = "cancelled"
)

// Trigger identifies how a workflow execution was requested.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerWebhook  Trigger = "webhook"
	TriggerSchedule Trigger = "schedule"
	// TriggerSubworkflow is a run another workflow started.
	//
	// Its own value rather than reusing manual: a sub-workflow run has a parent
	// and is not something a person asked for directly, and a history that
	// labelled it "manual" would be telling the reader a lie about who ran it.
	TriggerSubworkflow Trigger = "subworkflow"
)

// Record is a redaction-ready execution record pinned to one immutable
// workflow revision. Input, output, and error must already be safe to persist.
type Record struct {
	ID                string  `json:"id"`
	TenantID          string  `json:"tenantId"`
	WorkflowID        string  `json:"workflowId"`
	WorkflowVersionID string  `json:"workflowVersionId"`
	Status            Status  `json:"status"`
	Trigger           Trigger `json:"trigger"`
	// TriggerNodeID names the trigger node this run started from.
	//
	// A workflow may declare several trigger roots and only one fires on a
	// given run, so `trigger` alone — manual, webhook or schedule — no longer
	// says which. It is empty for a run that starts from every root, which is
	// what a manual run means.
	TriggerNodeID string `json:"triggerNodeId,omitempty"`
	// ParentExecutionID is the run that called this one, for a sub-workflow.
	//
	// Empty for every other kind. It is what makes a chain of calls readable
	// in history: without it a sub-workflow execution is an orphan that appears
	// beside its parent with nothing saying they belong together.
	ParentExecutionID       string          `json:"parentExecutionId,omitempty"`
	Input                   json.RawMessage `json:"input,omitempty"`
	Output                  json.RawMessage `json:"output,omitempty"`
	Error                   json.RawMessage `json:"error,omitempty"`
	StartedAt               time.Time       `json:"startedAt"`
	FinishedAt              *time.Time      `json:"finishedAt,omitempty"`
	CancellationRequestedAt *time.Time      `json:"cancellationRequestedAt,omitempty"`
	NodeRuns                []NodeRun       `json:"nodeRuns,omitempty"`
	// LeaseOwner is an internal fencing token copied from durable storage when
	// a worker claims a record. It is intentionally never part of the API.
	LeaseOwner string `json:"-"`
}

// NodeRun is one attempt to execute a node inside an execution. The sequence
// keeps logs deterministic even when later scheduling becomes concurrent.
type NodeRun struct {
	ID          string `json:"id"`
	TenantID    string `json:"tenantId"`
	ExecutionID string `json:"executionId"`
	NodeID      string `json:"nodeId"`
	Attempt     int    `json:"attempt"`
	// RunIndex is the Nth time this node ran in the execution, distinct from
	// Attempt, which counts retries of one run.
	RunIndex   int             `json:"runIndex"`
	Sequence   int             `json:"sequence"`
	Status     Status          `json:"status"`
	Input      json.RawMessage `json:"input,omitempty"`
	Output     json.RawMessage `json:"output,omitempty"`
	Error      json.RawMessage `json:"error,omitempty"`
	StartedAt  time.Time       `json:"startedAt"`
	FinishedAt *time.Time      `json:"finishedAt,omitempty"`
	// LeaseOwner fences trace writes to the worker claim that produced them.
	LeaseOwner string `json:"-"`
}
