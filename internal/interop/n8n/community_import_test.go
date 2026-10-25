package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// communityFixture is a workflow whose one step is a community n8n node, of
// the type and parameters a test names.
func communityFixture(nodeType string, typeVersion float64, parameters string) string {
	return `{
	  "name": "Community",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Step","type":"` + nodeType + `","typeVersion":` + jsonFloat(typeVersion) + `,"position":[220,0],"parameters":` + parameters + `}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Step","type":"main","index":0}]]}}
	}`
}

// communityRegistry is the real catalogue plus a sidecar-provided Apify node
// and a pack-provided community node, each recording the npm identity it was
// installed from — the way sidecarnode.Convert and a source-declaring pack do.
func communityRegistry(t *testing.T) *node.Registry {
	t.Helper()
	catalogue := registry(t)
	community := []node.Definition{
		{
			Type: "sidecar.apify-nodes-apify.apify", Version: workflow.V(1),
			DisplayName: "Apify", Description: "Runs Apify actors.", Category: "Community",
			Group:      []node.NodeGroup{node.GroupOutput},
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Input"}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Output"}},
			ExecutorID: "sidecar.node",
			N8NSource:  &node.N8NSource{Package: "@apify/n8n-nodes-apify", Node: "apify"},
		},
		{
			Type: "pack.scrape", Version: workflow.V(1),
			DisplayName: "Scrape", Description: "Scrapes a page.", Category: "Web",
			Group:      []node.NodeGroup{node.GroupOutput},
			Inputs:     []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Input"}},
			Outputs:    []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain, DisplayName: "Output"}},
			ExecutorID: "routing",
			N8NSource:  &node.N8NSource{Package: "n8n-nodes-scrape", Node: "scrape"},
		},
	}
	for _, definition := range community {
		source := node.SourceSidecar
		if definition.Type == "pack.scrape" {
			source = node.SourcePack
		}
		if err := catalogue.RegisterFrom(source, definition); err != nil {
			t.Fatalf("RegisterFrom(%s) error = %v", definition.Type, err)
		}
	}
	return catalogue
}

// A community node this installation runs resolves onto the installed node,
// sidecar or pack, with its parameters carried under their own names and an
// expression marker read the way every mapped node reads one.
func TestACommunityNodeResolvesOntoTheInstalledNode(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		nodeType string
		wantType string
	}{
		{
			name:     "a scoped package resolves onto its sidecar node",
			nodeType: "@apify/n8n-nodes-apify.apify",
			wantType: "sidecar.apify-nodes-apify.apify",
		},
		{
			name:     "an unscoped package resolves too",
			nodeType: "n8n-nodes-scrape.scrape",
			wantType: "pack.scrape",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := n8n.Import([]byte(communityFixture(test.nodeType, 1,
				`{"actorId":"my-actor","runInput":"={{ $json.payload }}"}`)), communityRegistry(t))
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			step := nodeByName(result.Document, "Step")
			if step.Type != test.wantType {
				t.Fatalf("imported as %q, want %q", step.Type, test.wantType)
			}
			if step.TypeVersion != workflow.V(1) {
				t.Fatalf("typeVersion = %s, want the recorded 1", step.TypeVersion)
			}
			if step.Parameters["actorId"] != "my-actor" {
				t.Fatalf("actorId = %#v, want the parameter as it arrived", step.Parameters["actorId"])
			}
			runInput, _ := step.Parameters["runInput"].(map[string]any)
			if runInput["mode"] != "expression" || runInput["value"] != "{{ $json.payload }}" {
				t.Fatalf("runInput = %#v, want the expression carried with its marker read", step.Parameters["runInput"])
			}
			if blocking := blockingFor(result, "Step"); len(blocking) != 0 {
				t.Fatalf("blocking issues %#v, want none", blocking)
			}
		})
	}
}

// The resolution is version-checked the way a shared-version mapping is: a
// workflow asking for a node version the installation does not have is named,
// not silently resolved onto a different one.
func TestACommunityNodeIsVersionCheckedAgainstTheInstallation(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(communityFixture("@apify/n8n-nodes-apify.apify", 2, `{}`)), communityRegistry(t))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	step := nodeByName(result.Document, "Step")
	if step.Type != "sidecar.apify-nodes-apify.apify" {
		t.Fatalf("imported as %q, want the resolution to stand", step.Type)
	}
	blocking := blockingFor(result, "Step")
	if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, "version 2") {
		t.Fatalf("blocking issues = %#v, want the missing version named", blocking)
	}
}

// A community type nothing covers stays the honest placeholder, and the reason
// names the npm package to install rather than failing silently — scoped or
// unscoped. A type from one of n8n's own packages keeps the generic reason:
// there is no sidecar to install n8n-nodes-base into.
func TestAnUnresolvedCommunityNodeNamesThePackageToInstall(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nodeType   string
		wantNeedle string
	}{
		{
			name:       "a node the installed package does not carry",
			nodeType:   "@apify/n8n-nodes-apify.scraper",
			wantNeedle: "Install npm package @apify/n8n-nodes-apify in the sidecar",
		},
		{
			name:       "a package nothing here runs",
			nodeType:   "n8n-nodes-dhl.dhl",
			wantNeedle: "Install npm package n8n-nodes-dhl in the sidecar",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			result, err := n8n.Import([]byte(communityFixture(test.nodeType, 1, `{}`)), communityRegistry(t))
			if err != nil {
				t.Fatalf("Import() error = %v", err)
			}
			if step := nodeByName(result.Document, "Step"); step.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", step.Type)
			}
			blocking := blockingFor(result, "Step")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason to say %q", blocking, test.wantNeedle)
			}
		})
	}

	t.Run("an n8n-nodes-base type is not a missing package", func(t *testing.T) {
		t.Parallel()
		result, err := n8n.Import([]byte(communityFixture("n8n-nodes-base.emailSend", 2, `{}`)), communityRegistry(t))
		if err != nil {
			t.Fatalf("Import() error = %v", err)
		}
		blocking := blockingFor(result, "Step")
		if len(blocking) != 1 {
			t.Fatalf("blocking issues = %#v, want one", blocking)
		}
		if strings.Contains(blocking[0].Reason, "sidecar") {
			t.Fatalf("reason = %q, want no sidecar install advice for one of n8n's own nodes", blocking[0].Reason)
		}
	})
}

// A tenant the community node is scoped away from does not resolve it: the
// import falls to the placeholder naming the package, exactly as it would
// without the install, because that is the tenant's truth.
func TestACommunityNodeScopedAwayFromTheTenantDoesNotResolve(t *testing.T) {
	t.Parallel()

	catalogue := communityRegistry(t)
	if err := catalogue.ScopeTo("sidecar.apify-nodes-apify.apify", []string{"tenant-a"}); err != nil {
		t.Fatalf("ScopeTo() error = %v", err)
	}
	result, err := n8n.Import([]byte(communityFixture("@apify/n8n-nodes-apify.apify", 1, `{}`)),
		workflow.CatalogFor(catalogue, "tenant-b"))
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	if step := nodeByName(result.Document, "Step"); step.Type != n8n.UnsupportedNodeType {
		t.Fatalf("imported as %q, want the unsupported placeholder for a scoped-away tenant", step.Type)
	}
	if blocking := blockingFor(result, "Step"); len(blocking) != 1 ||
		!strings.Contains(blocking[0].Reason, "Install npm package @apify/n8n-nodes-apify in the sidecar") {
		t.Fatalf("blocking issues = %#v, want the install advice", blockingFor(result, "Step"))
	}
}

// The export half: a resolved community node goes back out as the n8n node it
// came from, parameters and expression markers restored, so a round trip
// returns the workflow to the type it was authored against.
func TestAResolvedCommunityNodeExportsAsItsN8NType(t *testing.T) {
	t.Parallel()

	catalogue := communityRegistry(t)
	payload := communityFixture("@apify/n8n-nodes-apify.apify", 1,
		`{"actorId":"my-actor","runInput":"={{ $json.payload }}"}`)
	result, err := n8n.Import([]byte(payload), catalogue)
	if err != nil {
		t.Fatalf("Import() error = %v", err)
	}
	exported, err := n8n.Export(result.Document, catalogue)
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, exportedNode := range exported.Document.Nodes {
		if exportedNode.Name != "Step" {
			continue
		}
		if exportedNode.Type != "@apify/n8n-nodes-apify.apify" {
			t.Fatalf("exported as %q, want the community n8n type", exportedNode.Type)
		}
		if exportedNode.TypeVersion != 1 {
			t.Fatalf("exported typeVersion = %v, want 1", exportedNode.TypeVersion)
		}
		if exportedNode.Parameters["actorId"] != "my-actor" {
			t.Fatalf("exported actorId = %#v", exportedNode.Parameters["actorId"])
		}
		if runInput, _ := exportedNode.Parameters["runInput"].(string); runInput != "={{ $json.payload }}" {
			t.Fatalf("exported runInput = %#v, want the expression marker restored", exportedNode.Parameters["runInput"])
		}
	}
}
