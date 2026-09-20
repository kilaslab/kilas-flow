package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// snippet is the start of a response body, enough to say what came back without
// printing a whole hosted page into a failure message.
func snippet(recorder *httptest.ResponseRecorder) string {
	body := strings.Join(strings.Fields(recorder.Body.String()), " ")
	if len(body) > 80 {
		body = body[:80] + "..."
	}
	return body
}

// serve sends one request through the harness's handler and returns the answer.
func (h harness) serve(method, url string) *httptest.ResponseRecorder {
	recorder := httptest.NewRecorder()
	body := strings.NewReader(`{}`)
	h.handler.ServeHTTP(recorder, httptest.NewRequest(method, url, body))
	return recorder
}

// A path is the label an author gave an endpoint, not an address. Two tenants
// importing one template hold the same label, and a request that names only
// that label must therefore reach neither of them: which workflow it would have
// run — the other tenant's credentials and datastores included — would depend
// on which row the database happened to return.
//
// The state under test is a binding with no minted route, which is what a
// database migrated from the schema that predates routes held. The lookup used
// to fall back to matching the label for those rows, so a caller guessing a
// label ran a stranger's workflow.
func TestADeliveryAddressedByPathLabelNeverRunsAnyTenantsWorkflow(t *testing.T) {
	h := newHarness(t)
	ctx := context.Background()
	other := repository.TenantScope{ID: "tenant-b"}

	document := webhookDocument("Shared", map[string]any{
		"path": "shared-label", "httpMethod": http.MethodPost, "responseMode": "immediate",
	})
	first := h.activate(t, document)
	secondDraft, err := h.workflows.SaveDraft(ctx, other, document)
	if err != nil {
		t.Fatalf("SaveDraft(tenant-b) error = %v", err)
	}
	second, err := h.workflows.Activate(ctx, other, secondDraft.ID, h.registry)
	if err != nil {
		t.Fatalf("Activate(tenant-b) error = %v", err)
	}

	routeOf := func(tenant repository.TenantScope, workflowID string) string {
		t.Helper()
		routes, err := h.workflows.WebhookRoutes(ctx, tenant, workflowID)
		if err != nil || len(routes) != 1 {
			t.Fatalf("WebhookRoutes(%s, %s) = %#v, %v; want one route", tenant.ID, workflowID, routes, err)
		}
		if routes[0].Path != "shared-label" {
			t.Fatalf("path label = %q, want the two tenants to share %q", routes[0].Path, "shared-label")
		}
		return routes[0].Route
	}
	firstRoute := routeOf(h.tenant, first.ID)
	secondRoute := routeOf(other, second.ID)
	if firstRoute == secondRoute || firstRoute == "shared-label" || secondRoute == "shared-label" {
		t.Fatalf("routes = %q and %q, want two distinct minted routes that are not the label", firstRoute, secondRoute)
	}

	// deliverTo posts to one route and asserts that exactly one execution was
	// queued and that it is the owner's — visible to that tenant only.
	deliverTo := func(name, route string, owner repository.TenantScope, workflowID string, stranger repository.TenantScope) {
		t.Helper()
		before := h.queuedCount()
		recorder := h.serve(http.MethodPost, "/webhook/"+route)
		if recorder.Code != http.StatusOK {
			t.Fatalf("%s: status = %d (body: %s), want 200", name, recorder.Code, snippet(recorder))
		}
		if got := h.queuedCount(); got != before+1 {
			t.Fatalf("%s: %d executions queued, want exactly one more than the %d before", name, got, before)
		}
		record, err := h.runtime.Get(ctx, owner, h.lastExecution(t))
		if err != nil {
			t.Fatalf("%s: Get() as the owner error = %v", name, err)
		}
		if record.WorkflowID != workflowID {
			t.Errorf("%s: delivery ran workflow %s, want %s", name, record.WorkflowID, workflowID)
		}
		if _, err := h.runtime.Get(ctx, stranger, h.lastExecution(t)); err == nil {
			t.Errorf("%s: the other tenant can read the execution, want it to belong to the owner only", name)
		}
	}

	// Addressed by its own route, each tenant runs its own workflow.
	deliverTo("first tenant by route", firstRoute, h.tenant, first.ID, other)
	deliverTo("second tenant by route", secondRoute, other, second.ID, h.tenant)

	// Make the first tenant's binding one that has no minted route: the shape a
	// database predating routes carries. The second tenant keeps its own.
	if err := h.db.Exec("UPDATE webhook_bindings SET route = '' WHERE tenant_id = ?", h.tenant.ID).Error; err != nil {
		t.Fatalf("blank the first tenant's route: %v", err)
	}

	// Addressed by the label, no tenant's workflow runs. The delivery and the
	// preflight both resolve through the route, so both are refused.
	queued := h.queuedCount()
	for _, method := range []string{http.MethodPost, http.MethodOptions} {
		recorder := h.serve(method, "/webhook/shared-label")
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s /webhook/shared-label: status = %d (body: %s), want 404 — a label is not an address", method, recorder.Code, snippet(recorder))
		}
	}
	if got := h.queuedCount(); got != queued {
		// Name whose workflow it was, which is what makes this a cross-tenant
		// failure rather than a miscount.
		ran := "an execution that is not readable as the first tenant's"
		if record, err := h.runtime.Get(ctx, h.tenant, h.lastExecution(t)); err == nil {
			ran = "the first tenant's workflow " + record.WorkflowID
		}
		t.Fatalf("a delivery addressed by label queued %d execution(s), running %s; want none", got-queued, ran)
	}

	// The second tenant's own route is unaffected, and still runs only its own
	// workflow.
	deliverTo("second tenant by route after the label was refused", secondRoute, other, second.ID, h.tenant)
}

// The hosted page a form trigger serves is looked up by route as well, through
// a different query than the delivery uses, so it gets its own proof. A form
// whose binding has no minted route used to serve its page on the label.
func TestAHostedPageAddressedByPathLabelIsNotServed(t *testing.T) {
	h := newHarness(t)
	active := h.activate(t, formDocument(map[string]any{
		"path": "label-page", "formTitle": "Label page",
		"formFields": map[string]any{"values": []any{
			map[string]any{"fieldLabel": "Name", "fieldType": "text"},
		}},
		"responseMode": "immediate",
	}))
	url := h.url(t, active)
	if url == "/webhook/label-page" {
		t.Fatalf("the form was bound on its label %q, want a minted route", url)
	}

	// The control: through its route the page is served, so the refusal below is
	// about the label and not about a form that never worked.
	if page := h.serve(http.MethodGet, url); page.Code != http.StatusOK || !strings.Contains(page.Body.String(), "Label page") {
		t.Fatalf("GET %s status = %d (body: %s), want the page", url, page.Code, snippet(page))
	}

	// The form binds GET and POST; blank both, which the (method, route) index
	// allows because the methods differ.
	if err := h.db.Exec("UPDATE webhook_bindings SET route = '' WHERE workflow_id = ?", active.ID).Error; err != nil {
		t.Fatalf("blank the form's routes: %v", err)
	}

	for _, method := range []string{http.MethodGet, http.MethodPost} {
		recorder := h.serve(method, "/webhook/label-page")
		if recorder.Code != http.StatusNotFound {
			t.Errorf("%s /webhook/label-page: status = %d (body: %s), want 404 rather than the hosted page", method, recorder.Code, snippet(recorder))
		}
	}
	if got := h.queuedCount(); got != 0 {
		t.Errorf("a submission addressed by label queued %d execution(s), want none", got)
	}
}
