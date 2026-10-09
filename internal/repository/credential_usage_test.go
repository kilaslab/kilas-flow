package repository_test

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// seedCredentialRow writes a credential row under a fixed id, for a fixture
// whose workflow names that id: activation refuses a credential the tenant
// does not have. The payload is never opened by these tests.
func seedCredentialRow(t *testing.T, db *database.DB, tenant repository.TenantScope, credentialID, credentialType string) {
	t.Helper()
	now := time.Now().UTC()
	if err := db.Exec("INSERT INTO credentials (id, tenant_id, name, type, payload, public_fields, allowed_domains, created_at, updated_at)"+
		" VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)",
		credentialID, tenant.ID, "Seeded "+credentialID, credentialType, []byte{0}, []byte(`{}`), []byte(`[]`), now, now).Error; err != nil {
		t.Fatalf("seed credential %s: %v", credentialID, err)
	}
}

func usageCatalogue(t *testing.T) *node.Registry {
	t.Helper()
	catalogue := node.NewRegistry()
	if err := nodes.RegisterAll(catalogue); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return catalogue
}

// gmailNode is a Gmail trigger attaching credentialID under its one type.
func gmailNode(id, name, credentialID string) workflow.Node {
	return workflow.Node{
		ID: id, Name: name, Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
		Credentials: map[string]string{nodes.GmailCredentialType: credentialID},
		Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
	}
}

func manualNode() workflow.Node {
	return workflow.Node{ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual", TypeVersion: workflow.V(1)}
}

func usageDocument(id, name string, nodeList ...workflow.Node) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion, ID: id, Name: name,
		Nodes: nodeList, Connections: []workflow.Connection{}, Settings: map[string]any{},
	}
}

func mustGmailCredential(t *testing.T, store *repository.GORMCredentialStore, tenant repository.TenantScope, name string) credentials.Record {
	t.Helper()
	created, err := store.Create(context.Background(), tenant, credentials.Record{
		Name: name, Type: nodes.GmailCredentialType, Fields: map[string]string{"clientId": "cid"},
	})
	if err != nil {
		t.Fatalf("Create(%s) error = %v", name, err)
	}
	return created
}

func mustSave(t *testing.T, store *repository.GORMWorkflowStore, tenant repository.TenantScope, document workflow.Document) {
	t.Helper()
	if _, err := store.SaveDraft(context.Background(), tenant, document); err != nil {
		t.Fatalf("SaveDraft(%s) error = %v", document.ID, err)
	}
}

// The workflows a credential is "used by" are the ones that would break if it
// went: a draft that attaches it and a live version that attaches it, each
// once, with the nodes involved. A revision nobody would run again, another
// tenant's workflow and a deleted workflow are not users.
func TestCredentialUsersFindsDraftAndActiveReferences(t *testing.T) {
	eachDriver(t, assertCredentialUsers)
}

func assertCredentialUsers(t *testing.T, db *database.DB) {
	ctx := context.Background()
	catalogue := usageCatalogue(t)
	secrets := repository.NewCredentialStore(db.DB, stateCipher(t, 0x66))
	workflows := repository.NewWorkflowStore(db.DB)
	tenant, other := repository.TenantScope{ID: "drv-usage"}, repository.TenantScope{ID: "drv-usage-other"}
	used := mustGmailCredential(t, secrets, tenant, "Mailbox")
	unused := mustGmailCredential(t, secrets, tenant, "Spare")

	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_alpha", "Alpha", gmailNode("g1", "Inbox watcher", used.ID)))

	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_beta", "Beta", gmailNode("g2", "Mail", used.ID)))
	if _, err := workflows.Activate(ctx, tenant, "wf_drv_usage_beta", catalogue); err != nil {
		t.Fatalf("Activate(beta) error = %v", err)
	}
	// The live version still attaches it after the draft stopped doing so.
	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_beta", "Beta", manualNode()))

	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_history", "History", gmailNode("g3", "Old", used.ID)))
	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_history", "History", manualNode()))

	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_gone", "Gone", gmailNode("g4", "Deleted", used.ID)))
	if err := workflows.Delete(ctx, tenant, "wf_drv_usage_gone"); err != nil {
		t.Fatalf("Delete(gone) error = %v", err)
	}
	mustSave(t, workflows, other, usageDocument("wf_drv_usage_other", "Other tenant", gmailNode("g5", "Guess", used.ID)))
	mustSave(t, workflows, tenant, usageDocument("wf_drv_usage_plain", "Plain", manualNode()))

	users, err := secrets.CredentialUsers(ctx, tenant, used.ID)
	if err != nil {
		t.Fatalf("CredentialUsers() error = %v", err)
	}
	want := []repository.CredentialUser{
		{WorkflowID: "wf_drv_usage_alpha", Name: "Alpha", Active: false, Nodes: []string{"Inbox watcher"}},
		{WorkflowID: "wf_drv_usage_beta", Name: "Beta", Active: true, Nodes: []string{"Mail"}},
	}
	if !reflect.DeepEqual(users, want) {
		t.Fatalf("CredentialUsers() = %+v, want %+v", users, want)
	}

	none, err := secrets.CredentialUsers(ctx, tenant, unused.ID)
	if err != nil || len(none) != 0 {
		t.Fatalf("CredentialUsers(unused) = %+v, %v, want none", none, err)
	}
	if leaked, err := secrets.CredentialUsers(ctx, other, used.ID); err != nil || len(leaked) != 1 || leaked[0].WorkflowID != "wf_drv_usage_other" {
		t.Fatalf("CredentialUsers() under the other tenant = %+v, %v, want only its own workflow", leaked, err)
	}
}

// A workflow that attaches a credential this tenant does not have is refused
// at activation, naming the node, the credential type and the id, instead of
// activating and failing on the first run. A disabled node is never run, so
// what it attaches does not matter.
func TestActivationRefusesAnUnknownCredential(t *testing.T) {
	eachDriver(t, assertActivationRefusesUnknownCredential)
}

func assertActivationRefusesUnknownCredential(t *testing.T, db *database.DB) {
	ctx := context.Background()
	catalogue := usageCatalogue(t)
	secrets := repository.NewCredentialStore(db.DB, stateCipher(t, 0x67))
	workflows := repository.NewWorkflowStore(db.DB)
	tenant, other := repository.TenantScope{ID: "drv-activate-cred"}, repository.TenantScope{ID: "drv-activate-cred-other"}
	real := mustGmailCredential(t, secrets, tenant, "Mailbox")
	foreign := mustGmailCredential(t, secrets, other, "Someone else's")

	for name, credentialID := range map[string]string{
		"a deleted or never-created credential": "cred_drv_missing",
		"another tenant's credential":           foreign.ID,
	} {
		document := usageDocument("wf_drv_activate_dangling", "Dangling", gmailNode("gmail", "Inbox watcher", credentialID))
		mustSave(t, workflows, tenant, document)
		_, err := workflows.Activate(ctx, tenant, "wf_drv_activate_dangling", catalogue)
		if err == nil {
			t.Fatalf("%s: Activate() succeeded", name)
		}
		var validation *workflow.ValidationErrors
		if !errors.As(err, &validation) {
			t.Fatalf("%s: Activate() error = %T %v, want validation errors", name, err, err)
		}
		message := err.Error()
		for _, want := range []string{`"Inbox watcher"`, "gmail", nodes.GmailCredentialType, credentialID} {
			if !strings.Contains(message, want) {
				t.Errorf("%s: refusal %q does not name %s", name, message, want)
			}
		}
		if len(validation.Issues) != 1 || validation.Issues[0].NodeID != "gmail" ||
			validation.Issues[0].Path != "/nodes/0/credentials/"+nodes.GmailCredentialType {
			t.Errorf("%s: issues = %+v, want one issue on the node's credential", name, validation.Issues)
		}
	}
	stored, err := workflows.Get(ctx, tenant, "wf_drv_activate_dangling")
	if err != nil || stored.Active {
		t.Fatalf("the refused workflow is active = %t, %v", stored.Active, err)
	}

	disabled := gmailNode("gmail", "Inbox watcher", "cred_drv_missing")
	disabled.Disabled = true
	mustSave(t, workflows, tenant, usageDocument("wf_drv_activate_disabled", "Disabled", manualNode(), disabled))
	if _, err := workflows.Activate(ctx, tenant, "wf_drv_activate_disabled", catalogue); err != nil {
		t.Fatalf("a disabled node's missing credential refused activation: %v", err)
	}

	mustSave(t, workflows, tenant, usageDocument("wf_drv_activate_real", "Real", gmailNode("gmail", "Inbox watcher", real.ID)))
	if _, err := workflows.Activate(ctx, tenant, "wf_drv_activate_real", catalogue); err != nil {
		t.Fatalf("a workflow whose credential exists was refused: %v", err)
	}
}
