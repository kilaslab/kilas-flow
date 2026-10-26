package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
)

// refusingLimiter refuses every spend after the first, reporting a wait a
// client can act on.
type refusingLimiter struct {
	spends int
}

func (limiter *refusingLimiter) Allow(context.Context, string, string) (bool, time.Duration, error) {
	limiter.spends++
	if limiter.spends > 1 {
		return false, 9 * time.Second, nil
	}
	return true, 0, nil
}

// The run API spends a tenant's allowance before anything else: the first
// request runs, the second within the window is answered 429 with
// Retry-After and the named problem code, and no execution exists behind the
// refusal.
func TestTheRunAPIAnswers429PastTheTenantAllowance(t *testing.T) {
	limiter := &refusingLimiter{}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		deps.RateLimiter = limiter
	}, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Throttled run")

	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", keys["acme"],
		map[string]any{}, http.StatusAccepted)

	recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", keys["acme"], map[string]any{})
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body: %s)", recorder.Code, recorder.Body)
	}
	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "9" {
		t.Errorf("Retry-After = %q, want the limiter's whole seconds", retryAfter)
	}
	var problem struct {
		Status int `json:"status"`
		Errors []struct {
			Value struct {
				Code string `json:"code"`
			} `json:"value"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode the problem: %v (body: %s)", err, recorder.Body)
	}
	if problem.Status != http.StatusTooManyRequests || len(problem.Errors) != 1 || problem.Errors[0].Value.Code != "rate_limited" {
		t.Errorf("the problem = %+v, want the rate_limited code", problem)
	}

	// Exactly one execution exists: the refusal queued nothing. The idempotent
	// first run recorded its outcome; the refusal claims no key of its own.
	if executions := countExecutions(t, server.executions, "acme"); executions != 1 {
		t.Fatalf("executions = %d after the refused run, want 1", executions)
	}
}

// The events API spends the same way, before the fan-out reads a
// subscription or claims a delivery.
func TestTheEventsAPIAnswers429PastTheTenantAllowance(t *testing.T) {
	publisher := &recordingPublisher{}
	limiter := &refusingLimiter{}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		wireHostEvents(t, deps, publisher)
		deps.RateLimiter = limiter
	}, "acme")

	server.callJSON(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"type": "message.received",
	}, http.StatusAccepted)

	recorder := server.call(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"type": "message.received",
	})
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("status = %d, want 429 (body: %s)", recorder.Code, recorder.Body)
	}
	if retryAfter := recorder.Header().Get("Retry-After"); retryAfter != "9" {
		t.Errorf("Retry-After = %q, want the limiter's whole seconds", retryAfter)
	}
	if events := publisher.events(); len(events) != 1 {
		t.Fatalf("the fan-out saw %d publishes, want 1: the refusal reached nothing", len(events))
	}
}

// Another tenant's allowance is its own: the per-(tenant, bucket) limiter
// isolates by design, and a busy tenant never throttles its neighbours.
func TestARateLimitedTenantDoesNotThrottleItsNeighbour(t *testing.T) {
	perTenant := &perTenantLimiter{allowed: map[string]bool{"acme": false, "globex": true}}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		deps.RateLimiter = perTenant
	}, "acme", "globex")

	workflowID := server.createWorkflowAs(t, keys["acme"], "Acme run")
	if recorder := server.call(t, http.MethodPost, "/api/v1/workflows/"+workflowID+"/run", keys["acme"], map[string]any{}); recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("acme status = %d, want 429", recorder.Code)
	}
	neighbour := server.createWorkflowAs(t, keys["globex"], "Globex run")
	server.callJSON(t, http.MethodPost, "/api/v1/workflows/"+neighbour+"/run", keys["globex"], map[string]any{}, http.StatusAccepted)
}

// perTenantLimiter allows exactly the tenants its map names.
type perTenantLimiter struct {
	allowed map[string]bool
}

func (limiter *perTenantLimiter) Allow(_ context.Context, tenantID, _ string) (bool, time.Duration, error) {
	if limiter.allowed[tenantID] {
		return true, 0, nil
	}
	return false, 5 * time.Second, nil
}
