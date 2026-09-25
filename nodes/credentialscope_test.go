package nodes

import (
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

func credentialScopeLookup(records ...credentials.Record) CredentialLookup {
	byID := map[string]credentials.Record{}
	for _, record := range records {
		byID[record.ID] = record
	}
	return func(id string) (credentials.Record, bool, error) {
		record, found := byID[id]
		return record, found, nil
	}
}

func TestUnscopedCredentialIssuesNamesEveryNodeCarryingOne(t *testing.T) {
	lookup := credentialScopeLookup(
		credentials.Record{ID: "cred_any", Name: "Anywhere", Type: "httpHeaderAuth"},
		credentials.Record{ID: "cred_partner", Name: "Partner", Type: "httpHeaderAuth", AllowedDomains: []string{"partner.test"}},
		credentials.Record{ID: "cred_ai", Name: "OpenAI", Type: "openAiApi"},
		credentials.Record{ID: "cred_db", Name: "Warehouse", Type: "postgres"},
	)
	node := func(id, credentialType, credentialID string) workflow.Node {
		return workflow.Node{
			ID: id, Name: id, Type: "kilasflow.httpRequest", TypeVersion: workflow.V(1),
			Credentials: map[string]string{credentialType: credentialID},
		}
	}
	document := workflow.Document{Nodes: []workflow.Node{
		node("exfil", "httpHeaderAuth", "cred_any"),
		node("partner", "httpHeaderAuth", "cred_partner"),
		node("model", "openAiApi", "cred_ai"),
		node("query", "postgres", "cred_db"),
		node("gone", "httpHeaderAuth", "cred_deleted"),
	}}

	issues, err := UnscopedCredentialIssues(document, lookup)
	if err != nil {
		t.Fatalf("UnscopedCredentialIssues() error = %v", err)
	}
	if len(issues) != 1 {
		t.Fatalf("issues = %q, want exactly the node carrying the unscoped credential", issues)
	}
	for _, want := range []string{`"exfil"`, "cred_any", "allowed domains"} {
		if !strings.Contains(issues[0], want) {
			t.Errorf("issue %q does not name %q", issues[0], want)
		}
	}
	// The credential's name is withheld: the caller may not be allowed to know
	// it, and the id is what it attached.
	if strings.Contains(issues[0], "Anywhere") {
		t.Errorf("issue %q discloses the credential's name", issues[0])
	}
}

// A trigger that verifies arriving requests never sends its credential, so an
// unscoped one there is the documented way to protect an embedded workflow —
// and the same credential on a node that calls out is still refused.
func TestUnscopedCredentialIssuesLetsATriggerVerifyWithAnUnscopedCredential(t *testing.T) {
	lookup := credentialScopeLookup(credentials.Record{ID: "cred_hook", Name: "Hook key", Type: "httpHeaderAuth"})
	document := workflow.Document{Nodes: []workflow.Node{
		{ID: "hook", Name: "Webhook", Type: WebhookNodeType, Credentials: map[string]string{"httpHeaderAuth": "cred_hook"}},
		{ID: "form", Name: "Form", Type: FormTriggerNodeType, Credentials: map[string]string{"httpHeaderAuth": "cred_hook"}},
	}}
	if issues, err := UnscopedCredentialIssues(document, lookup); err != nil || len(issues) != 0 {
		t.Fatalf("issues = %q, err = %v; want a verifying trigger allowed", issues, err)
	}

	document.Nodes = append(document.Nodes, workflow.Node{
		ID: "exfil", Name: "exfil", Type: "kilasflow.httpRequest", Credentials: map[string]string{"httpHeaderAuth": "cred_hook"},
	})
	issues, err := UnscopedCredentialIssues(document, lookup)
	if err != nil || len(issues) != 1 || !strings.Contains(issues[0], `"exfil"`) {
		t.Fatalf("issues = %q, err = %v; want the node that calls out refused", issues, err)
	}
}

// A disabled node never runs, so nothing it carries is ever sent — the rule the
// compiler applies to a disabled node's credentials too. n8n imports routinely
// carry a switched-off HTTP node, and refusing it would block every save of the
// workflow. Enabling it is a save like any other, and is refused then.
func TestUnscopedCredentialIssuesSkipsADisabledNode(t *testing.T) {
	lookup := credentialScopeLookup(credentials.Record{ID: "cred_any", Name: "Anywhere", Type: "httpHeaderAuth"})
	leftover := workflow.Node{
		ID: "old", Name: "old", Type: "kilasflow.httpRequest", Disabled: true,
		Credentials: map[string]string{"httpHeaderAuth": "cred_any"},
	}
	document := workflow.Document{Nodes: []workflow.Node{leftover}}
	if issues, err := UnscopedCredentialIssues(document, lookup); err != nil || len(issues) != 0 {
		t.Fatalf("issues = %q, err = %v; want a disabled node skipped", issues, err)
	}

	document.Nodes[0].Disabled = false
	if issues, err := UnscopedCredentialIssues(document, lookup); err != nil || len(issues) != 1 {
		t.Fatalf("issues = %q, err = %v; want the node refused once it is enabled", issues, err)
	}
}

// The grant check is a different question and keeps asking it of a disabled
// node: whether a session may reference a credential at all does not depend on
// whether the node that references it is switched on, and a disabled node is
// one save away from running.
func TestEmbedScopeIssuesStillRefusesAnUngrantedCredentialOnADisabledNode(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{{
		ID: "old", Name: "old", Type: "kilasflow.httpRequest", Disabled: true,
		Credentials: map[string]string{"httpHeaderAuth": "cred_foreign"},
	}}}
	if issues := EmbedScopeIssues(document, embed.Confinement{}); len(issues) != 1 {
		t.Fatalf("issues = %q, want the ungranted credential refused on a disabled node", issues)
	}
}

func TestUnscopedCredentialIssuesFailsClosedOnALookupError(t *testing.T) {
	document := workflow.Document{Nodes: []workflow.Node{{
		ID: "n", Name: "n", Type: "kilasflow.httpRequest", Credentials: map[string]string{"httpHeaderAuth": "cred_1"},
	}}}
	broken := func(string) (credentials.Record, bool, error) {
		return credentials.Record{}, false, errors.New("database is gone")
	}
	if _, err := UnscopedCredentialIssues(document, broken); err == nil {
		t.Fatal("a lookup failure was read as no credential to check")
	}
}
