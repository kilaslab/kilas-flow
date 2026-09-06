// Resume HTTP surface: one unguessable single-use URL per suspended execution.
//
// Served from its own /resume prefix, beside /webhook, and never modelled as
// a webhook binding: the bindings route index is unique on (method, path), so
// every suspended execution contending for one route would collide there.
//
// The token is a bearer capability. A request carrying no identity resumes
// whoever the token names; a request carrying a tenant must own the wait or
// it answers exactly like an unknown token, so tokens cannot oracle other
// tenants' executions. Refusals are distinct and stable: unknown (404),
// already-answered (409) and expired (410) each keep their answer on repeat,
// and an embed-denied call (403) never consumes, so it stays resumable.
//
// Nothing here serves the checkpoint: it is run data, and these calls are
// answered to whoever holds the token link.
package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// ResumePrefix is the HTTP prefix the per-execution resume URLs live under.
const ResumePrefix = engine.ResumePrefix

// ResumeService is the narrow durable-wait surface the resume handler needs.
// The concrete worker remains independent from routing and its storage layer.
type ResumeService interface {
	WaitInfo(ctx context.Context, callerTenant, token string) (repository.Wait, error)
	ResumeApproval(ctx context.Context, callerTenant, token string, decision engine.ApprovalDecision, viaEmbed bool) (repository.Wait, execution.Record, error)
	ResumeCall(ctx context.Context, callerTenant, token string, output json.RawMessage, viaEmbed bool) (repository.Wait, execution.Record, error)
}

// Resume answers the per-execution resume URLs.
type Resume struct {
	service ResumeService
	tenants TenantResolver
}

// NewResume constructs the resume handler. A nil service leaves the prefix
// answering "unavailable" rather than half-working.
func NewResume(service ResumeService, tenants TenantResolver) *Resume {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Resume{service: service, tenants: tenants}
}

// Handler routes GET (info for the approval page) and POST (resume) under
// /resume/{token}. Any other shape under the prefix is not a resume URL.
func (handler *Resume) Handler() http.Handler {
	return http.HandlerFunc(handler.serve)
}

func (handler *Resume) serve(writer http.ResponseWriter, request *http.Request) {
	if handler.service == nil {
		writeResumeProblem(writer, http.StatusServiceUnavailable, "wait.unavailable", "approval resume is not configured on this instance")
		return
	}
	token := strings.TrimPrefix(request.URL.Path, ResumePrefix)
	token = strings.Trim(token, "/")
	if token == "" || strings.Contains(token, "/") {
		writeResumeProblem(writer, http.StatusNotFound, "wait.not_found", engine.ErrWaitNotFound.Error())
		return
	}
	switch request.Method {
	case http.MethodGet:
		handler.info(writer, request, token)
	case http.MethodPost:
		handler.resume(writer, request, token)
	default:
		writer.Header().Set("Allow", "GET, POST")
		writeResumeProblem(writer, http.StatusMethodNotAllowed, "wait.method_not_allowed", "resume URLs answer GET and POST")
	}
}

// resumeInfoResource is what the approval page renders: who waits, on what,
// until when — never the checkpoint and never the token.
type resumeInfoResource struct {
	ExecutionID string    `json:"executionId"`
	WorkflowID  string    `json:"workflowId"`
	NodeID      string    `json:"nodeId"`
	Mode        string    `json:"mode"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

func (handler *Resume) info(writer http.ResponseWriter, request *http.Request, token string) {
	wait, err := handler.service.WaitInfo(request.Context(), handler.tenants.Resolve(request.Context()).ID, token)
	if err != nil {
		writeResumeRefusal(writer, err)
		return
	}
	// Refused without consuming: the info call never marks anything, so each
	// refusal is stable and asking again gets the same answer.
	now := time.Now().UTC()
	if wait.ConsumedAt != nil {
		writeResumeProblem(writer, http.StatusConflict, "wait.answered", engine.ErrWaitConsumed.Error())
		return
	}
	if !now.Before(wait.ExpiresAt) {
		writeResumeProblem(writer, http.StatusGone, "wait.expired", engine.ErrWaitExpired.Error())
		return
	}
	writeResumeJSON(writer, http.StatusOK, resumeInfoResource{
		ExecutionID: wait.ExecutionID, WorkflowID: wait.WorkflowID,
		NodeID: wait.NodeID, Mode: wait.Mode, ExpiresAt: wait.ExpiresAt,
	})
}

// resumeDecision is the human outcome the approval page posts.
type resumeDecision struct {
	Approved  bool   `json:"approved"`
	DecidedBy string `json:"decidedBy,omitempty"`
	Note      string `json:"note,omitempty"`
}

// resumeAcceptedResource is the machine answer to a consumed token.
type resumeAcceptedResource struct {
	ExecutionID string `json:"executionId"`
	Status      string `json:"status"`
}

func (handler *Resume) resume(writer http.ResponseWriter, request *http.Request, token string) {
	ctx := request.Context()
	// Like the registry: unknown answers before embed is even considered, so
	// a denied call and a wrong link stay distinguishable without consuming.
	wait, err := handler.service.WaitInfo(ctx, handler.tenants.Resolve(ctx).ID, token)
	if err != nil {
		writeResumeRefusal(writer, err)
		return
	}
	_, viaEmbed := middleware.EmbedSessionFrom(ctx)
	if viaEmbed {
		writeResumeProblem(writer, http.StatusForbidden, "wait.embed_denied", engine.ErrWaitEmbedDenied.Error())
		return
	}
	tenant := handler.tenants.Resolve(ctx).ID
	if wait.Mode == "" || wait.Mode == engine.WaitModeApproval {
		var decision resumeDecision
		if request.ContentLength != 0 {
			if err := json.NewDecoder(request.Body).Decode(&decision); err != nil {
				writeResumeProblem(writer, http.StatusBadRequest, "wait.bad_request", "approval resume wants {approved, decidedBy?, note?}")
				return
			}
		}
		_, record, err := handler.service.ResumeApproval(ctx, tenant, token, engine.ApprovalDecision{
			Approved:    decision.Approved,
			DecidedBy:   decision.DecidedBy,
			RespondedAt: time.Now().UTC(),
			Note:        decision.Note,
		}, viaEmbed)
		if err != nil {
			writeResumeRefusal(writer, err)
			return
		}
		writeResumeJSON(writer, http.StatusOK, resumeAcceptedResource{ExecutionID: record.ID, Status: string(record.Status)})
		return
	}
	var output json.RawMessage
	if request.ContentLength != 0 {
		var raw json.RawMessage
		if err := json.NewDecoder(request.Body).Decode(&raw); err != nil {
			writeResumeProblem(writer, http.StatusBadRequest, "wait.bad_request", "resume wants the suspending node's output")
			return
		}
		output = raw
	}
	_, record, err := handler.service.ResumeCall(ctx, tenant, token, output, viaEmbed)
	if err != nil {
		writeResumeRefusal(writer, err)
		return
	}
	writeResumeJSON(writer, http.StatusOK, resumeAcceptedResource{ExecutionID: record.ID, Status: string(record.Status)})
}

// writeResumeRefusal maps the durable-wait refusal vocabulary onto distinct
// HTTP statuses: unknown (404), already-answered (409), expired (410) and
// embed-denied (403). Anything else is a server fault, never a refusal.
func writeResumeRefusal(writer http.ResponseWriter, err error) {
	switch {
	case errors.Is(err, repository.ErrWaitNotFound) || errors.Is(err, engine.ErrWaitNotFound):
		writeResumeProblem(writer, http.StatusNotFound, "wait.not_found", engine.ErrWaitNotFound.Error())
	case errors.Is(err, repository.ErrWaitConsumed) || errors.Is(err, engine.ErrWaitConsumed):
		writeResumeProblem(writer, http.StatusConflict, "wait.answered", engine.ErrWaitConsumed.Error())
	case errors.Is(err, repository.ErrWaitExpired) || errors.Is(err, engine.ErrWaitExpired):
		writeResumeProblem(writer, http.StatusGone, "wait.expired", engine.ErrWaitExpired.Error())
	case errors.Is(err, engine.ErrWaitEmbedDenied):
		writeResumeProblem(writer, http.StatusForbidden, "wait.embed_denied", engine.ErrWaitEmbedDenied.Error())
	default:
		writeResumeProblem(writer, http.StatusInternalServerError, "wait.failed", "approval resume failed")
	}
}

type resumeProblem struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

func writeResumeProblem(writer http.ResponseWriter, status int, code, message string) {
	writeResumeJSON(writer, status, resumeProblem{Code: code, Message: message})
}

func writeResumeJSON(writer http.ResponseWriter, status int, value any) {
	writer.Header().Set("Content-Type", "application/json")
	writer.WriteHeader(status)
	_ = json.NewEncoder(writer).Encode(value)
}
