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
	StatusSucceeded  Status = "succeeded"
	StatusFailed     Status = "failed"
	StatusCancelled  Status = "cancelled"
)

// Trigger identifies how a workflow execution was requested.
type Trigger string

const (
	TriggerManual   Trigger = "manual"
	TriggerWebhook  Trigger = "webhook"
	TriggerSchedule Trigger = "schedule"
)

// Record is a redaction-ready execution record pinned to one immutable
// workflow revision. Input, output, and error must already be safe to persist.
type Record struct {
	ID                      string          `json:"id"`
	TenantID                string          `json:"tenantId"`
	WorkflowID              string          `json:"workflowId"`
	WorkflowVersionID       string          `json:"workflowVersionId"`
	Status                  Status          `json:"status"`
	Trigger                 Trigger         `json:"trigger"`
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
	ID          string          `json:"id"`
	TenantID    string          `json:"tenantId"`
	ExecutionID string          `json:"executionId"`
	NodeID      string          `json:"nodeId"`
	Attempt     int             `json:"attempt"`
	Sequence    int             `json:"sequence"`
	Status      Status          `json:"status"`
	Input       json.RawMessage `json:"input,omitempty"`
	Output      json.RawMessage `json:"output,omitempty"`
	Error       json.RawMessage `json:"error,omitempty"`
	StartedAt   time.Time       `json:"startedAt"`
	FinishedAt  *time.Time      `json:"finishedAt,omitempty"`
	// LeaseOwner fences trace writes to the worker claim that produced them.
	LeaseOwner string `json:"-"`
}
