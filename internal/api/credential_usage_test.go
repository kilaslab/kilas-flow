package api_test

import (
	"context"
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

type credentialUsageView struct {
	WorkflowID string   `json:"workflowId"`
	Name       string   `json:"name"`
	Active     bool     `json:"active"`
	Nodes      []string `json:"nodes"`
}

type credentialWithUsage struct {
	ID     string                `json:"id"`
	UsedBy []credentialUsageView `json:"usedBy"`
}

// gmailWorkflow saves a draft whose Gmail trigger attaches credentialID.
func gmailWorkflow(t *testing.T, server *authenticatedAPI, tenantID, workflowID, name, credentialID string) {
	t.Helper()
	if _, err := server.workflows.SaveDraft(context.Background(), repository.TenantScope{ID: tenantID}, workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: workflowID, Name: name,
		Nodes: []workflow.Node{{
			ID: "gmail", Name: "Inbox watcher", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
			Credentials: map[string]string{nodes.GmailCredentialType: credentialID},
			Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
		}},
		Connections: []workflow.Connection{}, Settings: map[string]any{},
	}); err != nil {
		t.Fatalf("SaveDraft(%s) error = %v", workflowID, err)
	}
}

func createGmailCredential(t *testing.T, server *authenticatedAPI, key, name string) string {
	t.Helper()
	return createdCredentialID(t, auditCall(t, server, http.MethodPost, "/api/v1/credentials", key, map[string]any{
		"name": name, "type": nodes.GmailCredentialType, "fields": map[string]string{"clientId": "cid"},
	}, nil))
}

func getCredentialWithUsage(t *testing.T, server *authenticatedAPI, key, credentialID string) (credentialWithUsage, map[string]any) {
	t.Helper()
	recorder := auditCall(t, server, http.MethodGet, "/api/v1/credentials/"+credentialID, key, nil, nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET credential = %d (body: %s)", recorder.Code, recorder.Body)
	}
	var typed credentialWithUsage
	var raw map[string]any
	if err := json.Unmarshal(recorder.Body.Bytes(), &typed); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode: %v", err)
	}
	return typed, raw
}

func TestGetCredentialListsTheWorkflowsUsingIt(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	key := keys["acme"]
	used := createGmailCredential(t, server, key, "Mailbox")
	unused := createGmailCredential(t, server, key, "Spare")
	gmailWorkflow(t, server, "acme", "wf_usage_api", "Support inbox", used)

	got, _ := getCredentialWithUsage(t, server, key, used)
	if len(got.UsedBy) != 1 {
		t.Fatalf("usedBy = %+v, want the one workflow", got.UsedBy)
	}
	if user := got.UsedBy[0]; user.WorkflowID != "wf_usage_api" || user.Name != "Support inbox" || user.Active ||
		len(user.Nodes) != 1 || user.Nodes[0] != "Inbox watcher" {
		t.Fatalf("usedBy[0] = %+v", user)
	}

	_, raw := getCredentialWithUsage(t, server, key, unused)
	if _, present := raw["usedBy"]; present {
		t.Fatalf("an unused credential reported usedBy = %v, want the field absent", raw["usedBy"])
	}

	// The listing never computes usage: it would scan every workflow once per
	// credential on every editor load.
	listed := auditCall(t, server, http.MethodGet, "/api/v1/credentials", key, nil, nil)
	if listed.Code != http.StatusOK || strings.Contains(listed.Body.String(), "usedBy") {
		t.Fatalf("list = %d %s, want no usedBy", listed.Code, listed.Body)
	}
}

// A scoped key may read a single credential, but it is confined to some
// workflows and must not learn the names of the others through a credential
// they share.
func TestGetCredentialHidesUsageFromAScopedKey(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	used := createGmailCredential(t, server, keys["acme"], "Mailbox")
	gmailWorkflow(t, server, "acme", "wf_usage_scoped", "Payroll", used)

	_, scoped, err := server.store.CreateScopedAPIKey(context.Background(), repository.TenantScope{ID: "acme"},
		"agent", []embed.Scope{embed.ScopeRead}, "", nil)
	if err != nil {
		t.Fatalf("CreateScopedAPIKey() error = %v", err)
	}
	_, raw := getCredentialWithUsage(t, server, scoped, used)
	if _, present := raw["usedBy"]; present {
		t.Fatalf("a scoped key was shown usedBy = %v", raw["usedBy"])
	}
	if got, _ := getCredentialWithUsage(t, server, keys["acme"], used); len(got.UsedBy) != 1 {
		t.Fatalf("the unscoped key lost usedBy: %+v", got.UsedBy)
	}
}

// Deleting a credential a workflow uses is refused with the workflows named,
// so the person deleting it learns what would break. force=true deletes it
// anyway, and the trail says it was forced.
func TestDeletingAnInUseCredentialIsRefusedUnlessForced(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	key := keys["acme"]
	used := createGmailCredential(t, server, key, "Mailbox")
	gmailWorkflow(t, server, "acme", "wf_delete_a", "Support inbox", used)
	gmailWorkflow(t, server, "acme", "wf_delete_b", "Billing inbox", used)

	refused := auditCall(t, server, http.MethodDelete, "/api/v1/credentials/"+used, key, nil, nil)
	if refused.Code != http.StatusConflict {
		t.Fatalf("delete of an in-use credential = %d (body: %s), want 409", refused.Code, refused.Body)
	}
	var problem struct {
		Detail string `json:"detail"`
		Errors []struct {
			Message  string `json:"message"`
			Location string `json:"location"`
		} `json:"errors"`
	}
	if err := json.Unmarshal(refused.Body.Bytes(), &problem); err != nil {
		t.Fatalf("decode the problem: %v", err)
	}
	if !strings.Contains(problem.Detail, "2 workflows") || !strings.Contains(problem.Detail, "force=true") {
		t.Errorf("detail = %q, want the count and how to force it", problem.Detail)
	}
	if len(problem.Errors) != 2 || problem.Errors[0].Location != "workflows/wf_delete_b" || problem.Errors[1].Location != "workflows/wf_delete_a" ||
		!strings.Contains(problem.Errors[0].Message, "Billing inbox") || !strings.Contains(problem.Errors[0].Message, "Inbox watcher") {
		t.Errorf("errors = %+v, want one per workflow, by name, naming its nodes", problem.Errors)
	}
	if still := auditCall(t, server, http.MethodGet, "/api/v1/credentials/"+used, key, nil, nil); still.Code != http.StatusOK {
		t.Fatalf("the refused delete removed the credential: GET = %d", still.Code)
	}

	unused := createGmailCredential(t, server, key, "Spare")
	if recorder := auditCall(t, server, http.MethodDelete, "/api/v1/credentials/"+unused, key, nil, nil); recorder.Code != http.StatusNoContent {
		t.Fatalf("delete of an unused credential = %d (body: %s), want 204", recorder.Code, recorder.Body)
	}

	if forced := auditCall(t, server, http.MethodDelete, "/api/v1/credentials/"+used+"?force=true", key, nil, nil); forced.Code != http.StatusNoContent {
		t.Fatalf("forced delete = %d (body: %s), want 204", forced.Code, forced.Body)
	}
	if gone := auditCall(t, server, http.MethodGet, "/api/v1/credentials/"+used, key, nil, nil); gone.Code != http.StatusNotFound {
		t.Fatalf("GET after the forced delete = %d, want 404", gone.Code)
	}
	events := credentialEventsOf(t, server, "acme", used)
	if len(events) == 0 || events[0].Action != repository.CredentialEventDeleted || events[0].Detail != "forced; used by 2 workflows" {
		t.Fatalf("newest event = %+v, want a forced delete naming the count", events)
	}
}
