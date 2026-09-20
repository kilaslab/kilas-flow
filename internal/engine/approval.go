// Durable human-approval waits: suspend an execution, hand out an unguessable
// single-use resume token, and continue from the suspension point with the
// exact upstream data when the decision arrives.
//
// This file is the wait path's mechanism, not its integration. It deliberately
// does not touch Runner.Run or Service.runOnce: teaching those to stop needs
// the checkpoint column, the `waiting` status, the lease release and the
// ClaimNext exclusion, which belong to the execution-record change this file
// must not smuggle in. The integration contract is:
//
//  1. An executor that needs approval returns SuspendError from Execute. Its
//     message never carries the token: executor errors land in node-run
//     records and logs, and a token there would be a credential in storage.
//  2. The service catches SuspendError at the runOnce boundary, persists the
//     node runs produced so far (a workflow that waits four hours must show
//     four hours of evidence, not an empty inspector), writes the checkpoint
//     through its own column — never through the redacting payload() path,
//     which would hand the resumed run "[redacted]" where its data was —
//     sets the record to `waiting`, releases the lease, and publishes the
//     waiting event this registry already emitted at Issue.
//  3. The resume HTTP surface calls Resume with the caller's tenant and
//     whether the caller arrived on an embed session, then feeds the returned
//     Payload back into the runner at the suspending node. No completed node
//     runs twice, because the checkpoint carries the completed map.
//  4. A sweeper calls Sweep on a period and resolves what it reaps down the
//     timeout output or as a named failure, so no execution stays suspended
//     forever.
//
// Timeout interplay, stated once because two ceilings look like one: the
// in-process wait node (nodes/wait.go MaxWaitDuration, one hour) holds a
// worker for the whole pause, so execution.default_timeout — 60s stock —
// usually binds first and the run times out. A suspending wait releases the
// worker, so neither ceiling binds it; the ticket's ExpiresAt is its only
// deadline. The integrator should keep short waits in process (n8n holds
// below ~65s and only offloads above) and suspend above a configured
// threshold, because checkpointing a five-second wait costs two database
// round trips to save nothing.
//
// Tenancy and embedding: a token is looked up under the caller's tenant, and
// a mismatch answers exactly like an unknown token, so tokens cannot oracle
// other tenants' executions. Approval resume is refused to embedded sessions:
// the embed token restricts a request to one workflow and a set of scopes,
// and resuming someone else's approval from inside a host page is precisely
// the confused-deputy shape that restriction exists to stop.
package engine

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/events"
)

// Wait modes sharing the token machinery. Approval is the human mode built on
// resume-by-call; the timer modes suspend the same way and resume by deadline.
const (
	// WaitModeApproval suspends until a human approves or rejects.
	WaitModeApproval = "approval"
	// WaitModeInterval suspends for a duration, then resumes.
	WaitModeInterval = "interval"
	// WaitModeUntil suspends until an absolute time, then resumes.
	WaitModeUntil = "until"
	// WaitModeWebhook suspends until a call arrives on the resume URL.
	WaitModeWebhook = "webhook"
)

// EventExecutionWaiting is published when an execution suspends. Non-terminal
// by construction: events.Type.Terminal reports false for it, so a live feed
// stays open across the wait rather than closing and forcing a reconnect.
const EventExecutionWaiting = events.Type("execution.waiting")

// DefaultApprovalTTL applies when Issue is given no deadline. A day is long
// enough for a human to answer and short enough that a forgotten approval
// does not linger past any reasonable retention.
const DefaultApprovalTTL = 24 * time.Hour

// MaxWaitTTL caps a single suspension. A wait needs a configured limit that
// resolves — down a timeout output or as a named failure — and unbounded is
// not a limit.
const MaxWaitTTL = 7 * 24 * time.Hour

var (
	// ErrWaitNotFound reports an unknown token, or a token looked up under
	// the wrong tenant. Deliberately the same error for both: distinguishing
	// them would let a caller probe which executions exist in other tenants.
	ErrWaitNotFound = errors.New("approval request was not found")
	// ErrWaitConsumed reports a second call on a single-use token.
	ErrWaitConsumed = errors.New("approval request was already answered")
	// ErrWaitExpired reports a resume past the ticket's deadline.
	ErrWaitExpired = errors.New("approval request has expired")
	// ErrWaitEmbedDenied reports an approval resume attempted from an embedded
	// session. The ticket is silent on embedding, so embedding is denied.
	ErrWaitEmbedDenied = errors.New("approval resume is not available to embedded sessions")
)

// ApprovalDecision is the human outcome recorded on resume. It renders onto
// the node's output as {approved, respondedAt, decidedBy, note}, matching the
// shape n8n's approval nodes emit so an imported workflow branches the same
// way here.
type ApprovalDecision struct {
	Approved    bool
	DecidedBy   string
	RespondedAt time.Time
	Note        string
}

// Output renders the decision as the suspending node's structured output.
func (decision ApprovalDecision) Output() map[string]any {
	respondedAt := decision.RespondedAt
	if respondedAt.IsZero() {
		respondedAt = time.Now().UTC()
	}
	return map[string]any{
		"approved":    decision.Approved,
		"respondedAt": respondedAt.UTC().Format(time.RFC3339),
		"decidedBy":   decision.DecidedBy,
		"note":        decision.Note,
	}
}

// WaitTicket is one suspended execution awaiting resume.
type WaitTicket struct {
	// Token is the opaque single-use resume credential: 16 bytes of entropy,
	// base64url-encoded. It travels in the resume URL, never in an error, an
	// event, or a log line.
	Token       string
	TenantID    string
	ExecutionID string
	WorkflowID  string
	NodeID      string
	Mode        string
	// Payload is the exact upstream data the run held at suspension, handed
	// back on Resume so the run continues where it stopped. Stored as given:
	// the durable integrator must keep it out of the redacting payload()
	// path, which would corrupt it with "[redacted]" on the way back.
	Payload   json.RawMessage
	CreatedAt time.Time
	ExpiresAt time.Time

	consumed bool
	decision *ApprovalDecision
}

// SuspendError is what an executor returns to suspend instead of succeed or
// fail. The runner catches it at the attempt boundary — without retrying, and
// without recording a failure — snapshots the run into Checkpoint, and lets
// the service persist the wait durably. Its message never carries a token.
type SuspendError struct {
	Ticket *WaitTicket
	// Mode names the wait (approval, interval, until, webhook). Set by the
	// suspending executor; the service refuses a suspension without one.
	Mode string
	// ExpiresAt bounds the suspension. Zero takes DefaultApprovalTTL at
	// the service boundary; past MaxWaitTTL out fails the run.
	ExpiresAt time.Time
	// NodeID and Checkpoint are filled by the runner at catch time: the
	// suspending node and the exact run state a resumed run continues from.
	NodeID     string
	Checkpoint []byte
}

func (err *SuspendError) Error() string {
	node := ""
	if err != nil {
		node = err.NodeID
		if err.Ticket != nil {
			node = err.Ticket.NodeID
		}
	}
	return fmt.Sprintf("node %q is waiting for approval", node)
}

// IssueParams are the fields of one suspension.
type IssueParams struct {
	TenantID    string
	ExecutionID string
	WorkflowID  string
	NodeID      string
	Mode        string
	Payload     json.RawMessage
	// ExpiresAt is the ticket's deadline. Zero selects DefaultApprovalTTL. A
	// deadline in the past, or past MaxWaitTTL out, is refused: a wait that
	// cannot resolve must never be issued.
	ExpiresAt time.Time
}

// WaitRegistry issues and redeems resume tokens in process memory, safe for
// concurrent use.
//
// It is not what the server runs. Waits are durable now: Service.runOnce
// suspends an execution to storage with status `waiting` and a resume URL, and
// internal/engine/wait_service.go redeems the token and re-queues the run, so a
// restart resumes rather than drops. Nothing outside this package's tests
// constructs a WaitRegistry, and a caller that wired it up instead would get
// exactly the old behaviour — tokens lost on the next deploy — which is why
// this comment exists rather than a caller. Deleting the type (and the tests
// that keep it alive) is the other half of the audit finding; it is left to a
// pass that can take the decision on the engine package as a whole.
type WaitRegistry struct {
	mu      sync.Mutex
	tickets map[string]*WaitTicket
	broker  *events.Broker
	now     func() time.Time
}

// NewWaitRegistry builds a registry publishing waiting events to broker, which
// may be nil. Event delivery is best effort everywhere in this codebase; a run
// must suspend whether or not anyone is watching.
func NewWaitRegistry(broker *events.Broker) *WaitRegistry {
	return &WaitRegistry{tickets: map[string]*WaitTicket{}, broker: broker, now: time.Now}
}

// Issue suspends one execution and returns its single-use resume token. The
// event it publishes carries the node and the deadline — never the token and
// never the payload, both of which would put run data on a live feed.
func (registry *WaitRegistry) Issue(params IssueParams) (*WaitTicket, error) {
	if strings.TrimSpace(params.TenantID) == "" {
		return nil, fmt.Errorf("wait needs a tenant")
	}
	if strings.TrimSpace(params.ExecutionID) == "" {
		return nil, fmt.Errorf("wait needs an execution")
	}
	if strings.TrimSpace(params.NodeID) == "" {
		return nil, fmt.Errorf("wait needs a node")
	}
	now := registry.clock()
	expiresAt := params.ExpiresAt
	if expiresAt.IsZero() {
		expiresAt = now.Add(DefaultApprovalTTL)
	}
	if !expiresAt.After(now) {
		return nil, fmt.Errorf("%w: the deadline is in the past", ErrWaitExpired)
	}
	if expiresAt.Sub(now) > MaxWaitTTL {
		return nil, fmt.Errorf("wait of %s exceeds the maximum of %s", expiresAt.Sub(now).Truncate(time.Second), MaxWaitTTL)
	}
	token, err := newWaitToken()
	if err != nil {
		return nil, err
	}
	ticket := &WaitTicket{
		Token: token, TenantID: params.TenantID, ExecutionID: params.ExecutionID,
		WorkflowID: params.WorkflowID, NodeID: params.NodeID, Mode: params.Mode,
		Payload: params.Payload, CreatedAt: now.UTC(), ExpiresAt: expiresAt.UTC(),
	}
	registry.mu.Lock()
	registry.tickets[ticket.Token] = ticket
	registry.mu.Unlock()
	registry.publish(ticket)
	return ticket, nil
}

// Resume redeems one token for the tenant that owns it. The checks run in an
// order that refuses without consuming: an embed-denied, already-answered, or
// expired call never marks anything, so each refusal is stable — asking again
// gets the same answer, not the next error in the list.
func (registry *WaitRegistry) Resume(tenantID, token string, decision ApprovalDecision, viaEmbed bool) (*WaitTicket, error) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	ticket, found := registry.tickets[token]
	if !found || ticket.TenantID != tenantID {
		return nil, ErrWaitNotFound
	}
	if viaEmbed {
		return nil, ErrWaitEmbedDenied
	}
	if ticket.consumed {
		return nil, ErrWaitConsumed
	}
	if !registry.clock().Before(ticket.ExpiresAt) {
		return nil, ErrWaitExpired
	}
	ticket.consumed = true
	decided := decision
	ticket.decision = &decided
	return ticket, nil
}

// Sweep drops expired unconsumed tickets and reports how many. The integrator
// calls it on a period and resolves what it reaps — down a timeout output or
// as a named failure — which is what keeps any execution from staying
// suspended forever.
func (registry *WaitRegistry) Sweep(now time.Time) int {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	reaped := 0
	for token, ticket := range registry.tickets {
		if !ticket.consumed && !now.Before(ticket.ExpiresAt) {
			delete(registry.tickets, token)
			reaped++
		}
	}
	return reaped
}

// Decision returns the outcome recorded by Resume, if any.
func (ticket *WaitTicket) Decision() (ApprovalDecision, bool) {
	if ticket == nil || ticket.decision == nil {
		return ApprovalDecision{}, false
	}
	return *ticket.decision, true
}

func (registry *WaitRegistry) clock() time.Time {
	if registry != nil && registry.now != nil {
		return registry.now()
	}
	return time.Now()
}

func (registry *WaitRegistry) publish(ticket *WaitTicket) {
	if registry == nil || registry.broker == nil {
		return
	}
	data, _ := json.Marshal(map[string]any{
		"nodeId":    ticket.NodeID,
		"mode":      ticket.Mode,
		"expiresAt": ticket.ExpiresAt.UTC().Format(time.RFC3339),
	})
	registry.broker.Publish(events.Event{
		TenantID: ticket.TenantID, ExecutionID: ticket.ExecutionID, WorkflowID: ticket.WorkflowID,
		NodeID: ticket.NodeID, Type: EventExecutionWaiting, Data: data,
	})
}

// newWaitToken mints 16 bytes of entropy. Sixteen, like the webhook route
// segments elsewhere in this codebase: unguessable in practice, short enough
// for a URL a human taps on a phone.
func newWaitToken() (string, error) {
	entropy := make([]byte, 16)
	if _, err := rand.Read(entropy); err != nil {
		return "", fmt.Errorf("mint approval token: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(entropy), nil
}
