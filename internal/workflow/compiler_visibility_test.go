package workflow_test

import (
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// hidingCatalog is a catalogue narrowed to one tenant, as the node registry's
// tenant view is: some types exist but are withheld, and some versions of a
// visible type are refused.
//
// stubCatalog.Lookup accepts ANY version (it copies the requested version onto
// the definition), so this one refuses a configured (type, version) pair
// itself. Without that the unknown_version branch would be unreachable and an
// assertion about it would prove nothing.
type hidingCatalog struct {
	stubCatalog
	hidden         map[string]bool
	missingVersion map[string]bool
}

func versionKey(nodeType string, version workflow.TypeVersion) string {
	return nodeType + "@" + version.String()
}

func (catalog hidingCatalog) Lookup(nodeType string, version workflow.TypeVersion) (workflow.NodeDefinition, bool) {
	if catalog.hidden[nodeType] || catalog.missingVersion[versionKey(nodeType, version)] {
		return workflow.NodeDefinition{}, false
	}
	return catalog.stubCatalog.Lookup(nodeType, version)
}

func (catalog hidingCatalog) HasType(nodeType string) bool {
	return !catalog.hidden[nodeType] && catalog.stubCatalog.HasType(nodeType)
}

func (catalog hidingCatalog) Restricted(nodeType string) bool {
	return catalog.hidden[nodeType] && catalog.stubCatalog.HasType(nodeType)
}

var _ workflow.RestrictedCatalog = hidingCatalog{}

// scopingCatalog is a catalogue that can narrow itself, with a different view
// for each tenant.
type scopingCatalog struct {
	stubCatalog
	views map[string]workflow.Catalog
}

func (catalog *scopingCatalog) ForTenant(tenantID string) workflow.Catalog {
	return catalog.views[tenantID]
}

var _ workflow.TenantScoper = (*scopingCatalog)(nil)

func visibilityCatalog() stubCatalog {
	port := []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}}
	return stubCatalog{definitions: map[string]workflow.NodeDefinition{
		"test.trigger": {Type: "test.trigger", Version: workflow.V(1), Outputs: port},
		"test.visible": {Type: "test.visible", Version: workflow.V(1), Inputs: port, Outputs: port},
		"pack.scoped":  {Type: "pack.scoped", Version: workflow.V(1), Inputs: port, Outputs: port},
	}}
}

func documentReferencing(nodeType string, version workflow.TypeVersion) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_visibility", Name: "References one node",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "Trigger", Type: "test.trigger", TypeVersion: workflow.V(1)},
			{ID: "node", Name: "Node", Type: nodeType, TypeVersion: version},
		},
		Connections: []workflow.Connection{{
			ID: "c1", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
			Target: workflow.Endpoint{NodeID: "node", Port: "main"},
		}},
		Settings: map[string]any{},
	}
}

// issueFor returns the validation issue raised against one node, failing the
// test when the compile error is not a validation error or names no such node.
func issueFor(t *testing.T, err error, nodeID string) workflow.ValidationError {
	t.Helper()
	var validation *workflow.ValidationErrors
	if !errors.As(err, &validation) {
		t.Fatalf("Compile() error = %v, want *workflow.ValidationErrors", err)
	}
	for _, issue := range validation.Issues {
		if issue.NodeID == nodeID {
			return issue
		}
	}
	t.Fatalf("Compile() issues = %#v, none is about node %q", validation.Issues, nodeID)
	return workflow.ValidationError{}
}

// "No such node" and "a node you may not use" are different problems for
// whoever reads the diagnostic: one is a typo to fix, the other a request to
// make of the operator. Reporting both as an unregistered type would send a
// tenant hunting for a spelling mistake that is not there.
func TestCompileSaysNotAvailableRatherThanUnknownForARestrictedType(t *testing.T) {
	t.Parallel()

	catalog := hidingCatalog{
		stubCatalog:    visibilityCatalog(),
		hidden:         map[string]bool{"pack.scoped": true},
		missingVersion: map[string]bool{versionKey("test.visible", workflow.V(9)): true},
	}

	t.Run("a hidden type is not available, not unregistered", func(t *testing.T) {
		_, err := workflow.Compile(documentReferencing("pack.scoped", workflow.V(1)), catalog)
		issue := issueFor(t, err, "node")
		if issue.Code != workflow.ErrorNodeNotAvailable {
			t.Fatalf("code = %q, want %q", issue.Code, workflow.ErrorNodeNotAvailable)
		}
		if !strings.Contains(issue.Message, "not available") {
			t.Errorf("message = %q, want it to say the node is not available", issue.Message)
		}
		if strings.Contains(issue.Message, "not registered") {
			t.Errorf("message = %q, must not claim the type is unregistered", issue.Message)
		}
		if issue.Path != "/nodes/1/type" {
			t.Errorf("path = %q, want the node's type pointer", issue.Path)
		}
	})

	t.Run("a truly unknown type is still unknown", func(t *testing.T) {
		_, err := workflow.Compile(documentReferencing("pack.scopedd", workflow.V(1)), catalog)
		if issue := issueFor(t, err, "node"); issue.Code != workflow.ErrorUnknownNode {
			t.Fatalf("code = %q, want %q", issue.Code, workflow.ErrorUnknownNode)
		}
	})

	t.Run("a visible type at a refused version is an unknown version", func(t *testing.T) {
		_, err := workflow.Compile(documentReferencing("test.visible", workflow.V(9)), catalog)
		if issue := issueFor(t, err, "node"); issue.Code != workflow.ErrorUnknownVersion {
			t.Fatalf("code = %q, want %q", issue.Code, workflow.ErrorUnknownVersion)
		}
	})

	t.Run("a visible type at a version the catalogue accepts compiles", func(t *testing.T) {
		if _, err := workflow.Compile(documentReferencing("test.visible", workflow.V(1)), catalog); err != nil {
			t.Fatalf("Compile() error = %v, want the visible type accepted", err)
		}
	})
}

// The same document must compile for a tenant that may see the type and be
// refused for one that may not: the diagnostic follows the catalogue the
// compiler was handed, not the document.
func TestCompileDependsOnTheCatalogueItIsGiven(t *testing.T) {
	t.Parallel()

	visible := hidingCatalog{stubCatalog: visibilityCatalog()}
	hidden := hidingCatalog{stubCatalog: visibilityCatalog(), hidden: map[string]bool{"pack.scoped": true}}
	document := documentReferencing("pack.scoped", workflow.V(1))

	if _, err := workflow.Compile(document, visible); err != nil {
		t.Fatalf("Compile() for the tenant that may see the type = %v, want accepted", err)
	}
	_, err := workflow.Compile(document, hidden)
	if issue := issueFor(t, err, "node"); issue.Code != workflow.ErrorNodeNotAvailable {
		t.Fatalf("code = %q, want %q", issue.Code, workflow.ErrorNodeNotAvailable)
	}
}

// A catalogue that cannot scope must come back untouched: the helper is on
// every compile path and has to be inert for the stubs and fakes that never
// heard of tenants.
func TestCatalogForNarrowsOnlyACatalogueThatCanScope(t *testing.T) {
	t.Parallel()

	t.Run("a plain catalogue is returned unchanged", func(t *testing.T) {
		plain := &stubCatalog{definitions: visibilityCatalog().definitions}
		if got := workflow.CatalogFor(plain, "acme"); got != workflow.Catalog(plain) {
			t.Fatalf("CatalogFor() = %#v, want the very catalogue it was given", got)
		}
	})

	t.Run("nil stays nil", func(t *testing.T) {
		if got := workflow.CatalogFor(nil, "acme"); got != nil {
			t.Fatalf("CatalogFor(nil) = %#v, want nil", got)
		}
	})

	acme := &stubCatalog{definitions: map[string]workflow.NodeDefinition{
		"pack.scoped": {Type: "pack.scoped", Version: workflow.V(1)},
	}}
	globex := &stubCatalog{definitions: map[string]workflow.NodeDefinition{}}
	scoper := &scopingCatalog{
		stubCatalog: visibilityCatalog(),
		views:       map[string]workflow.Catalog{"acme": acme, "globex": globex},
	}

	t.Run("a catalogue that can scope answers with the tenant's own view", func(t *testing.T) {
		if got := workflow.CatalogFor(scoper, "acme"); got != workflow.Catalog(acme) {
			t.Errorf("CatalogFor(acme) = %#v, want acme's view", got)
		}
		if got := workflow.CatalogFor(scoper, "globex"); got != workflow.Catalog(globex) {
			t.Errorf("CatalogFor(globex) = %#v, want globex's view", got)
		}
	})

	t.Run("a scoped view cannot be widened by asking again", func(t *testing.T) {
		view := workflow.CatalogFor(scoper, "globex")
		again := workflow.CatalogFor(view, "acme")
		if again != view {
			t.Fatalf("CatalogFor(view, acme) = %#v, want the same view back", again)
		}
		if _, found := again.Lookup("pack.scoped", workflow.V(1)); found {
			t.Error("a view narrowed to globex resolved a type only acme's view holds")
		}
	})
}

// A refused node type explains itself, and its wires must not be blamed for it
// either. The connection issue sorts before the node issue, so a run that
// carries only the first sentence of the failure — the engine does — would read
// "connection must reference registered source and target nodes" and send the
// author looking for a wiring mistake: the endpoints ARE registered, the tenant
// just may not use one of them.
func TestARefusedNodeTypeDoesNotBlameItsConnections(t *testing.T) {
	t.Parallel()

	catalog := hidingCatalog{
		stubCatalog: visibilityCatalog(),
		hidden:      map[string]bool{"pack.scoped": true},
	}

	t.Run("a type withheld from this tenant", func(t *testing.T) {
		_, err := workflow.Compile(documentReferencing("pack.scoped", workflow.V(1)), catalog)
		var validation *workflow.ValidationErrors
		if !errors.As(err, &validation) {
			t.Fatalf("Compile() error = %v, want *workflow.ValidationErrors", err)
		}
		if got := err.Error(); !strings.Contains(got, "not available to this workspace") {
			t.Errorf("first issue = %q, want the node's own diagnostic", got)
		}
		for _, issue := range validation.Issues {
			if strings.HasPrefix(issue.Path, "/connections/") {
				t.Errorf("issue about a connection to a withheld node = %#v, want none", issue)
			}
		}
	})

	t.Run("a type that is not registered at all", func(t *testing.T) {
		_, err := workflow.Compile(documentReferencing("pack.missing", workflow.V(1)), catalog)
		if got := err.Error(); !strings.Contains(got, "not registered") {
			t.Errorf("first issue = %q, want the node's own diagnostic", got)
		}
	})

	t.Run("a dangling connection is still reported", func(t *testing.T) {
		document := documentReferencing("pack.scoped", workflow.V(1))
		document.Connections = append(document.Connections, workflow.Connection{
			ID: "c2", Kind: workflow.ConnectionMain,
			Source: workflow.Endpoint{NodeID: "trigger", Port: "main"},
			Target: workflow.Endpoint{NodeID: "ghost", Port: "main"},
		})
		_, err := workflow.Compile(document, hidingCatalog{stubCatalog: visibilityCatalog()})
		validation, ok := err.(*workflow.ValidationErrors)
		if !ok {
			t.Fatalf("Compile() error = %v, want *workflow.ValidationErrors", err)
		}
		found := false
		for _, issue := range validation.Issues {
			if issue.Path == "/connections/1" && issue.Code == workflow.ErrorInvalidTopology {
				found = true
			}
		}
		if !found {
			t.Errorf("issues = %#v, want the dangling connection reported", validation.Issues)
		}
	})
}
