package embed

import (
	"testing"
	"time"
)

func confinementIssuer(t *testing.T) *Issuer {
	t.Helper()
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index + 3)
	}
	issuer, err := NewIssuer(key, []string{"https://host.example"}, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func TestAConfinementIsNormalisedAndCarriedInTheToken(t *testing.T) {
	issuer := confinementIssuer(t)
	_, token, err := issuer.Issue(Request{
		TenantID: "standalone", WorkflowID: "wf_1",
		Scopes: []Scope{ScopeRead}, Origin: "https://host.example",
		Confinement: Confinement{
			Credentials: []string{" cred_2 ", "cred_1", "cred_1", ""},
			Datastores: []DatastoreRef{
				{ID: "ds_2"}, {ID: "ds_1", Name: "Metrics"}, {Name: "Metrics"}, {ID: "ds_1", Name: "Metrics"}, {},
			},
			Workflows: []string{"", "wf_2"},
		},
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}

	session, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	want := []string{"cred_1", "cred_2"}
	if len(session.Confinement.Credentials) != len(want) {
		t.Fatalf("credentials = %v, want %v", session.Confinement.Credentials, want)
	}
	for index := range want {
		if session.Confinement.Credentials[index] != want[index] {
			t.Fatalf("credentials = %v, want %v (sorted and deduplicated)", session.Confinement.Credentials, want)
		}
	}
	// A blank entry is dropped, and an exact duplicate is collapsed. A name-only
	// entry is NOT the same reference as one that also carries an id: a By-Name
	// locator is resolved live, so the two are checked against different halves.
	if len(session.Confinement.Datastores) != 3 {
		t.Fatalf("datastores = %#v, want three distinct entries", session.Confinement.Datastores)
	}
	byName := 0
	for _, ref := range session.Confinement.Datastores {
		if ref.Name == "Metrics" {
			byName++
		}
		if ref.ID == "" && ref.Name == "" {
			t.Errorf("a blank datastore entry survived normalisation: %#v", session.Confinement.Datastores)
		}
	}
	if byName != 2 {
		t.Errorf("datastores = %#v, want the name kept on both entries that carried it", session.Confinement.Datastores)
	}
	// A workflow may always call itself, so recursion needs no second entry.
	wantWorkflows := []string{"wf_1", "wf_2"}
	if len(session.Confinement.Workflows) != len(wantWorkflows) {
		t.Fatalf("workflows = %v, want %v (including the session's own)", session.Confinement.Workflows, wantWorkflows)
	}
	for index := range wantWorkflows {
		if session.Confinement.Workflows[index] != wantWorkflows[index] {
			t.Fatalf("workflows = %v, want %v", session.Confinement.Workflows, wantWorkflows)
		}
	}
}

func TestAConfinementWithoutEntriesAllowsNothing(t *testing.T) {
	var confinement Confinement
	if !confinement.Empty() {
		t.Fatal("a zero confinement is not empty")
	}
	if confinement.AllowsCredential("cred_1") {
		t.Error("an empty confinement allowed a credential")
	}
	// The name half is checked where a run resolves names, and pinned there
	// (nodes.TestEmbedScopeIssuesComparesGrantedNamesTheWayARunResolvesThem).
	if confinement.AllowsDatastoreID("ds_1") {
		t.Error("an empty confinement allowed a data table")
	}
	if confinement.AllowsWorkflow("wf_1") {
		t.Error("an empty confinement allowed a workflow")
	}
	// A blank reference is never a match, or a node with no credential chosen
	// would be refused as if it named something.
	if confinement.AllowsCredential("") {
		t.Error("an empty confinement matched a blank credential")
	}
}

// A name is compared where a run resolves names, by the same resolver (see
// nodes.EmbedScopeIssues); the confinement itself answers for ids, and a name
// never stands in for one.
func TestAConfinementNeverMatchesANameAsAnId(t *testing.T) {
	confinement := Confinement{Datastores: []DatastoreRef{{ID: "ds_1", Name: "Metrics"}}}
	if confinement.AllowsDatastoreID("") {
		t.Error("a blank table was allowed")
	}
	// An entry that carries only a name never matches an id and the reverse:
	// the two halves are different references, not interchangeable.
	if confinement.AllowsDatastoreID("Metrics") {
		t.Error("a name matched an id")
	}
	if (Confinement{Datastores: []DatastoreRef{{Name: "Metrics"}}}).AllowsDatastoreID("") {
		t.Error("a blank id matched a name-only entry")
	}
}

func TestASessionMintedWithoutAConfinementVerifies(t *testing.T) {
	// A token minted before the field existed carries none and must still
	// verify: it reads as the strictest confinement rather than as an error.
	issuer := confinementIssuer(t)
	_, token, err := issuer.Issue(Request{
		TenantID: "standalone", WorkflowID: "wf_1",
		Scopes: []Scope{ScopeRead}, Origin: "https://host.example",
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	session, err := issuer.Verify(token)
	if err != nil {
		t.Fatalf("Verify() error = %v", err)
	}
	if len(session.Confinement.Workflows) != 1 || session.Confinement.Workflows[0] != "wf_1" {
		t.Fatalf("workflows = %v, want only the session's own workflow", session.Confinement.Workflows)
	}
	if session.Confinement.AllowsCredential("cred_1") {
		t.Error("a session minted with no confinement allowed a credential")
	}
	_ = time.Now
}
