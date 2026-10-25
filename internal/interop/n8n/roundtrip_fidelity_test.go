package n8n_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
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

// TestPinDataSurvivesImportAndExport is FEAT-jrrqs2's interop half: a pin
// names a node, and both sides key it by name, so it crosses as itself. An
// import used to drop it with a diagnostic saying the nodes that had it
// pinned would run for real — which they then did.
func TestPinDataSurvivesImportAndExport(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, `{
	  "name": "Pinned",
	  "pinData": {"Edit": [{"json":{"pinned":true}},{"json":{"second":2}}]},
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}},
	    {"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[220,0],
	     "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"a","value":"b","type":"string"}]}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node":"Edit","type":"main","index":0}]]}}
	}`)

	// Carried, not reported: a pin naming a node of the workflow is data the
	// run replays, and nothing about it was lost.
	pinned := imported.Document.PinData["Edit"]
	if len(pinned) != 2 || pinned[0].JSON["pinned"] != true || pinned[1].JSON["second"] != float64(2) {
		t.Errorf("Document.PinData[Edit] = %#v, want both pinned items", pinned)
	}
	for _, issue := range imported.Unsupported {
		if issue.Field == "pinData" {
			t.Errorf("import reported %#v, want the pin carried without a diagnostic", issue)
		}
	}

	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	// n8n's own shape: a list of {json: …} items under the node's name.
	encoded, err := json.Marshal(exported.Document.PinData["Edit"])
	if err != nil {
		t.Fatalf("marshal the exported pin: %v", err)
	}
	if string(encoded) != `[{"json":{"pinned":true}},{"json":{"second":2}}]` {
		t.Errorf("exported pinData = %s, want n8n's item shape", encoded)
	}

	// Export then import is a fixed point for the pin, and reports nothing.
	exportedJSON, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("marshal the export: %v", err)
	}
	reimported, err := n8n.Import(exportedJSON, registry(t))
	if err != nil {
		t.Fatalf("Import(export) error = %v", err)
	}
	if again := reimported.Document.PinData["Edit"]; len(again) != 2 || again[0].JSON["pinned"] != true {
		t.Errorf("re-imported pinData = %#v, want the exported items", again)
	}
	for _, issue := range reimported.Unsupported {
		if issue.Field == "pinData" {
			t.Errorf("importing the export reported %#v, want the pin carried without a diagnostic", issue)
		}
	}

	// A workflow authored here, pinned from the editor, exports its pin too.
	authored := imported.Document
	authored.PinData = map[string][]workflow.Item{"Edit": {{JSON: map[string]any{"pinned": true}}}}
	pinnedExport, err := n8n.Export(authored, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if encoded, _ := json.Marshal(pinnedExport.Document.PinData["Edit"]); string(encoded) != `[{"json":{"pinned":true}}]` {
		t.Errorf("exported authored pinData = %s, want n8n's item shape", encoded)
	}

	// A paste still drops the pin and says so: it lands under names the
	// workflow it joins may already use, and a pin keyed by the old name
	// would point at nothing.
	fragment, err := n8n.ImportFragment([]byte(`{
	  "nodes": [{"id":"b","name":"Edit","type":"n8n-nodes-base.set","typeVersion":3.4,"position":[0,0],
	    "parameters":{"mode":"manual","assignments":{"assignments":[{"id":"1","name":"a","value":"b","type":"string"}]}}}],
	  "connections": {},
	  "pinData": {"Edit": [{"json":{"pinned":true}}]}
	}`), registry(t))
	if err != nil {
		t.Fatalf("ImportFragment() error = %v", err)
	}
	if fragment.Document.PinData != nil {
		t.Errorf("a paste carried pinData = %#v, want it dropped", fragment.Document.PinData)
	}
	var dropped bool
	for _, issue := range fragment.Unsupported {
		if issue.Field == "pinData" && issue.Severity == n8n.SeverityDropped {
			dropped = true
		}
	}
	if !dropped {
		t.Errorf("a paste dropped pinData without a diagnostic: %#v", fragment.Unsupported)
	}
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

	// Export then import is a fixed point for the settings: the order the
	// export declares is the order this server runs in, so reading it back
	// reports nothing, and the next export carries the same settings.
	exportedJSON, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("marshal the export: %v", err)
	}
	reimported, err := n8n.Import(exportedJSON, registry(t))
	if err != nil {
		t.Fatalf("Import(export) error = %v", err)
	}
	for _, issue := range reimported.Unsupported {
		if strings.HasPrefix(issue.Field, "settings") {
			t.Errorf("importing the export reported %#v, want no settings issue", issue)
		}
	}
	again, err := n8n.Export(reimported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export(reimport) error = %v", err)
	}
	if fmt.Sprint(again.Document.Settings) != fmt.Sprint(settings) {
		t.Errorf("second export settings = %#v, want the first export's %#v", again.Document.Settings, settings)
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

// TestANodesNoteRoundTrips is TPL-12's notes half: all 24 node notes of
// template 5170 were dropped on import and so missing from the export. A note
// is documentation the author wrote on the node; it travels in the node's
// settings beside its error policy, and goes back to n8n as it came.
func TestANodesNoteRoundTrips(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, `{
	  "name": "Annotated",
	  "nodes": [
	    {"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{},
	     "notes":"Starts the nightly sync","notesInFlow":true}
	  ],
	  "connections": {}
	}`)
	for _, issue := range imported.Unsupported {
		if issue.Field == "notes" {
			t.Errorf("import reported the note as not carried: %#v", issue)
		}
	}
	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	node := exported.Document.Nodes[0]
	if node.Notes != "Starts the nightly sync" || !node.NotesInFlow {
		t.Errorf("exported node notes = %q, notesInFlow = %v, want the note back as written", node.Notes, node.NotesInFlow)
	}
}
