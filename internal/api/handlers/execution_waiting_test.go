package handlers

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// The engine is what answers the waiting lookup in a real server. If its
// signature drifted from the handler's, the type assertion in Get would fail
// silently and every waiting execution would lose its links.
var _ waitingLinker = (*engine.Service)(nil)

type waitingController struct {
	record execution.Record
	links  engine.ResumeLinks
}

func (controller *waitingController) Wake() {}

func (controller *waitingController) Get(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return controller.record, nil
}

func (controller *waitingController) Cancel(context.Context, repository.TenantScope, string) (execution.Record, error) {
	return controller.record, nil
}

func (controller *waitingController) WaitingLinks(context.Context, repository.TenantScope, string) (engine.ResumeLinks, bool) {
	return controller.links, true
}

// A timer wait answers with when it resumes and no approval page: there is no
// decision to record, and the page offered Approve and Reject buttons anyway.
func TestAWaitingTimerExecutionAnswersWithItsResumeTimeAndNoApprovalPage(t *testing.T) {
	resumeAt := time.Date(2026, 9, 23, 10, 5, 0, 0, time.UTC)
	controller := &waitingController{
		record: execution.Record{
			ID: "exec-timer", WorkflowID: "wf-1", WorkflowVersionID: "wfv-1",
			Status: execution.StatusWaiting, Trigger: execution.TriggerManual,
		},
		links: engine.ResumeLinks{ResumeURL: "/resume/tok", ResumeAt: &resumeAt},
	}
	handler := NewExecutions(controller, nil, nil, nil, nil)
	output, err := handler.Get(context.Background(), &executionPathInput{ID: "exec-timer"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	encoded, err := json.Marshal(output.Body)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var body map[string]json.RawMessage
	if err := json.Unmarshal(encoded, &body); err != nil {
		t.Fatalf("decode %s: %v", encoded, err)
	}
	if _, ok := body["approvalUrl"]; ok {
		t.Errorf("body = %s, want no approvalUrl for a timer wait", encoded)
	}
	if got := strings.Trim(string(body["resumeAt"]), `"`); got != "2026-09-23T10:05:00Z" {
		t.Errorf("resumeAt = %s, want the timer's deadline", body["resumeAt"])
	}
	if got := strings.Trim(string(body["resumeUrl"]), `"`); got != "/resume/tok" {
		t.Errorf("resumeUrl = %s, want the machine resume link", body["resumeUrl"])
	}

	// An approval wait links its page and has no resume time of its own.
	controller.links = engine.ResumeLinks{ResumeURL: "/resume/tok", ApprovalURL: "/approve/tok"}
	output, err = handler.Get(context.Background(), &executionPathInput{ID: "exec-timer"})
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if output.Body.ApprovalURL != "/approve/tok" || output.Body.ResumeAt != nil {
		t.Errorf("approval wait = (%q, %v), want the approval page and no resume time", output.Body.ApprovalURL, output.Body.ResumeAt)
	}
}
