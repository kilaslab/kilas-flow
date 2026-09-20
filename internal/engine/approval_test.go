package engine_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
)

func waitForEvent(t *testing.T, sub *events.Subscription) events.Event {
	t.Helper()
	select {
	case event, ok := <-sub.Events():
		if !ok {
			t.Fatal("event stream closed before the waiting event arrived")
		}
		return event
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for the waiting event")
		return events.Event{}
	}
}

func TestApprovalWaitResumeContinue(t *testing.T) {
	t.Parallel()

	broker := events.NewBroker(events.BrokerOptions{})
	registry := engine.NewWaitRegistry(broker)
	sub := broker.Subscribe("tenant-a", "exec-1", 0)
	defer sub.Close()

	upstream := json.RawMessage(`{"items":[{"json":{"draft":"hello"}}]}`)
	ticket, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-1", WorkflowID: "wf-1",
		NodeID: "Approve", Mode: engine.WaitModeApproval, Payload: upstream,
		ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if len(ticket.Token) < 22 {
		t.Fatalf("token %q is too short to be unguessable", ticket.Token)
	}

	// The live feed sees the suspension — node and deadline, never the token
	// or the payload.
	event := waitForEvent(t, sub)
	if event.Type != engine.EventExecutionWaiting {
		t.Fatalf("event type = %q", event.Type)
	}
	if event.ExecutionID != "exec-1" || event.NodeID != "Approve" {
		t.Fatalf("event = %+v", event)
	}
	if strings.Contains(string(event.Data), ticket.Token) || strings.Contains(string(event.Data), "hello") {
		t.Fatalf("waiting event leaks token or payload: %s", event.Data)
	}
	if event.Type.Terminal() {
		t.Fatal("the waiting event is terminal; the feed would close mid-wait")
	}

	// Resume continues with the exact upstream data and records the decision
	// in the approval shape the next node branches on.
	decidedAt := time.Now().UTC().Truncate(time.Second)
	resumed, err := registry.Resume("tenant-a", ticket.Token, engine.ApprovalDecision{
		Approved: true, DecidedBy: "owner@example.com", RespondedAt: decidedAt, Note: "looks good",
	}, false)
	if err != nil {
		t.Fatalf("Resume() error = %v", err)
	}
	if string(resumed.Payload) != string(upstream) {
		t.Fatalf("resumed payload = %s, want the exact suspended data", resumed.Payload)
	}
	decision, found := resumed.Decision()
	if !found || !decision.Approved || decision.DecidedBy != "owner@example.com" {
		t.Fatalf("decision = %+v, %v", decision, found)
	}
	output := decision.Output()
	if output["approved"] != true || output["decidedBy"] != "owner@example.com" || output["note"] != "looks good" {
		t.Fatalf("decision output = %v", output)
	}
	if output["respondedAt"] != decidedAt.Format(time.RFC3339) {
		t.Fatalf("respondedAt = %v", output["respondedAt"])
	}

	// Single-use: a second call is refused with its own message.
	if _, err := registry.Resume("tenant-a", ticket.Token, engine.ApprovalDecision{}, false); !errors.Is(err, engine.ErrWaitConsumed) {
		t.Fatalf("second Resume() = %v, want ErrWaitConsumed", err)
	}
}

func TestResumeRefusalsAreDistinctAndStable(t *testing.T) {
	t.Parallel()

	registry := engine.NewWaitRegistry(nil)

	if _, err := registry.Resume("tenant-a", "no-such-token", engine.ApprovalDecision{}, false); !errors.Is(err, engine.ErrWaitNotFound) {
		t.Fatalf("unknown token = %v, want ErrWaitNotFound", err)
	}

	ticket, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-2", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	// A token looked up under the wrong tenant answers exactly like an
	// unknown one: no oracle for other tenants' executions.
	_, wrongTenantErr := registry.Resume("tenant-b", ticket.Token, engine.ApprovalDecision{}, false)
	_, unknownErr := registry.Resume("tenant-b", "no-such-token", engine.ApprovalDecision{}, false)
	if !errors.Is(wrongTenantErr, engine.ErrWaitNotFound) || wrongTenantErr.Error() != unknownErr.Error() {
		t.Fatalf("wrong tenant = %v, unknown = %v; want the identical refusal", wrongTenantErr, unknownErr)
	}

	expiring, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-3", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(40 * time.Millisecond),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	time.Sleep(80 * time.Millisecond)
	if _, err := registry.Resume("tenant-a", expiring.Token, engine.ApprovalDecision{}, false); !errors.Is(err, engine.ErrWaitExpired) {
		t.Fatalf("late Resume() = %v, want ErrWaitExpired", err)
	}
	// The refusal is stable: asking again does not become a different error.
	if _, err := registry.Resume("tenant-a", expiring.Token, engine.ApprovalDecision{}, false); !errors.Is(err, engine.ErrWaitExpired) {
		t.Fatalf("second late Resume() = %v, want ErrWaitExpired again", err)
	}
	if reaped := registry.Sweep(time.Now()); reaped != 1 {
		t.Fatalf("Sweep() = %d, want the one expired ticket", reaped)
	}
}

func TestApprovalResumeIsDeniedToEmbeddedSessions(t *testing.T) {
	t.Parallel()

	registry := engine.NewWaitRegistry(nil)
	ticket, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-4", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if _, err := registry.Resume("tenant-a", ticket.Token, engine.ApprovalDecision{Approved: true}, true); !errors.Is(err, engine.ErrWaitEmbedDenied) {
		t.Fatalf("embed Resume() = %v, want ErrWaitEmbedDenied", err)
	}
	// Denied before consuming: the legitimate resume still works afterwards.
	if _, err := registry.Resume("tenant-a", ticket.Token, engine.ApprovalDecision{Approved: true}, false); err != nil {
		t.Fatalf("Resume() after a denied embed call error = %v", err)
	}
}

func TestWaitNeedsABoundableDeadline(t *testing.T) {
	t.Parallel()

	registry := engine.NewWaitRegistry(nil)
	if _, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-5", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(-time.Minute),
	}); !errors.Is(err, engine.ErrWaitExpired) {
		t.Fatalf("past deadline = %v, want a refusal", err)
	}
	if _, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-5", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(30 * 24 * time.Hour),
	}); err == nil {
		t.Fatal("a wait past the maximum was issued; no execution may stay suspended forever")
	}
	ticket, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-5", NodeID: "Approve", Mode: engine.WaitModeApproval,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if until := time.Until(ticket.ExpiresAt); until <= 23*time.Hour || until > engine.DefaultApprovalTTL {
		t.Fatalf("default deadline = %s, want ~24h", until)
	}
}

func TestSuspendErrorNeverCarriesTheToken(t *testing.T) {
	t.Parallel()

	registry := engine.NewWaitRegistry(nil)
	ticket, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-6", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	suspend := &engine.SuspendError{Ticket: ticket}
	if strings.Contains(suspend.Error(), ticket.Token) {
		t.Fatal("the suspend signal carries the token; executor errors land in records and logs")
	}

	// Tokens are unique across tickets.
	other, err := registry.Issue(engine.IssueParams{
		TenantID: "tenant-a", ExecutionID: "exec-7", NodeID: "Approve",
		Mode: engine.WaitModeApproval, ExpiresAt: time.Now().Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if ticket.Token == other.Token {
		t.Fatal("two tickets share a token")
	}
}
