package n8n_test

import (
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// exportedNode finds a node of an exported n8n document by its name.
func exportedNode(t *testing.T, document n8n.Document, name string) n8n.Node {
	t.Helper()
	for _, candidate := range document.Nodes {
		if candidate.Name == name {
			return candidate
		}
	}
	t.Fatalf("the export has no node named %q", name)
	return n8n.Node{}
}

// TestAnExportedWorkflowDeclaresTheExecutionOrderItRunsIn is TPL-12's settings
// half. KilasFlow runs n8n's v1 order, and a file without the key opens in n8n
// as a legacy workflow that runs in the v0 order — a different behaviour than
// the one it had here. The settings the import carried still go back out.
func TestAnExportedWorkflowDeclaresTheExecutionOrderItRunsIn(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, `{
	  "name": "Jakarta nightly",
	  "settings": {"executionOrder": "v1", "timezone": "Asia/Jakarta", "executionTimeout": 120},
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}}
	  ],
	  "connections": {}
	}`)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	settings := exported.Document.Settings
	if settings["executionOrder"] != "v1" {
		t.Errorf("settings = %#v, want executionOrder v1", settings)
	}
	if settings["timezone"] != "Asia/Jakarta" {
		t.Errorf("settings = %#v, want the imported timezone kept", settings)
	}
	if fmt.Sprint(settings["executionTimeout"]) != "120" {
		t.Errorf("settings = %#v, want the imported execution timeout kept", settings)
	}

	// A workflow authored here, with no settings at all, says the same thing.
	authored := imported.Document
	authored.Settings = map[string]any{}
	bare, err := n8n.Export(authored, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if bare.Document.Settings["executionOrder"] != "v1" {
		t.Errorf("settings = %#v, want executionOrder v1 on a workflow with no settings", bare.Document.Settings)
	}
}

// TestAnIfNodeGoesBackAtTheVersionItWasAuthoredAt is TPL-12's version half: an
// If at 2.2 or 2.3 was exported at 2, a downgrade n8n opens with the older
// type-validation default. The translator writes the v2 filter shape every one
// of these versions reads, so the authored version is kept.
func TestAnIfNodeGoesBackAtTheVersionItWasAuthoredAt(t *testing.T) {
	t.Parallel()

	for _, version := range []float64{2, 2.1, 2.2, 2.3} {
		t.Run(fmt.Sprint(version), func(t *testing.T) {
			t.Parallel()
			imported := importFixture(t, fmt.Sprintf(`{
			  "name": "If round trip",
			  "nodes": [
			    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
			    {"id":"b","name":"If","type":"n8n-nodes-base.if","typeVersion":%v,"position":[220,0],
			     "parameters":{"conditions":{"options":{"caseSensitive":true,"leftValue":"","typeValidation":"strict","version":2},
			       "conditions":[{"id":"c1","leftValue":"={{ $json.a }}","rightValue":"b","operator":{"type":"string","operation":"equals"}}],
			       "combinator":"and"},"options":{}}}
			  ],
			  "connections": {"Manual": {"main": [[{"node":"If","type":"main","index":0}]]}}
			}`, version))
			exported, err := n8n.Export(imported.Document, registry(t))
			if err != nil {
				t.Fatalf("Export() error = %v", err)
			}
			if got := exportedNode(t, exported.Document, "If").TypeVersion; got != version {
				t.Errorf("If exported at typeVersion %v, want the authored %v", got, version)
			}
			for _, issue := range exported.Lossy {
				if issue.NodeName == "If" && issue.Field == "typeVersion" {
					t.Errorf("lossy = %#v, want no version change reported", issue)
				}
			}
		})
	}

	// Version 1 is a different parameter shape, which the import rewrites into
	// the v2 filter; it goes back at the version that shape belongs to.
	imported := importFixture(t, `{
	  "name": "IF v1",
	  "nodes": [{"id":"b","name":"If","type":"n8n-nodes-base.if","typeVersion":1,"position":[0,0],
	    "parameters":{"conditions":{"number":[{"value1":"10","value2":3,"operation":"larger"}]}}}],
	  "connections": {}
	}`)
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if got := exportedNode(t, exported.Document, "If").TypeVersion; got != 2 {
		t.Errorf("an If imported at v1 exported at %v, want 2, the shape the translator writes", got)
	}
}
