package nodes_test

import (
	"bytes"
	"encoding/json"
	"flag"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// The editor cannot call node.Definition.PortsFor: it is a Go function, not
// data (`json:"-"`). web/src/lib/workflow-editor/ports.ts therefore mirrors the
// rules by hand, and a hand-written mirror drifts silently — the canvas drew
// every mode-dependent vector store and embeddings node with its static main
// ports until BUG-pc284d.
//
// testdata/ports_for.golden.json is what both sides are held to. This test
// recomputes it from the real registry and fails on any difference;
// ports.golden.test.ts feeds the same cases to resolvedPorts. A change to PortsFor fails here until
// the golden is regenerated (go test ./nodes -run TestPortsForGolden
// -update-ports-golden), and then fails the web suite until ports.ts follows.
var updatePortsGolden = flag.Bool("update-ports-golden", false, "rewrite nodes/testdata/ports_for.golden.json from the registry")

const portsGoldenPath = "testdata/ports_for.golden.json"

// portsGoldenMirroredByHand are the node types whose PortsFor ports.ts mirrors
// with tests of their own (ports.test.ts, 'resolvedPorts'). They are not in the
// golden because their labels are localised or their rule sets are parsed from
// free-form JSON. Listing a type here is a decision that ports.ts has a case for
// it.
var portsGoldenMirroredByHand = map[string]string{
	nodes.SwitchNodeType:    "switchOutputs in ports.ts",
	"kilasflow.merge":       "mergeInputs in ports.ts",
	nodes.DatastoreNodeType: "datastoreOutputs in ports.ts",
	// The classifier's categories, mirrored the way the Switch's rules are:
	// free-form rows the golden cannot enumerate. NOTE: ports.ts needs its
	// classifierOutputs case added beside this entry — the Go side landed
	// first (web/ is owned by another change in flight).
	nodes.TextClassifierNodeType: "classifierOutputs in ports.ts",
}

// portsGoldenNeighbours are node types the golden carries static ports for
// without resolving them: the nodes an n8n RAG template wires to a vector store
// or an embeddings node, so the web suite can place a store beside its real
// neighbours.
var portsGoldenNeighbours = []string{
	nodes.DocumentLoaderNodeType,
	nodes.TextSplitterNodeType,
	nodes.VectorStoreToolNodeType,
	nodes.AgentNodeType,
	"kilasflow.lmChatOpenAi",
	"kilasflow.manual",
}

type portsGolden struct {
	Comment     string                       `json:"comment"`
	Definitions map[string]portsGoldenStatic `json:"definitions"`
	Cases       []portsGoldenCase            `json:"cases"`
}

// portsGoldenStatic is what the node-types API serves for a type: the ports of
// an unconfigured node.
type portsGoldenStatic struct {
	Inputs  []workflow.Port `json:"inputs"`
	Outputs []workflow.Port `json:"outputs"`
}

// portsGoldenCase is one configuration and the ports Go resolves for it.
// Parameters is deliberately not omitempty: null, {} and a missing map are
// different documents on the wire.
type portsGoldenCase struct {
	Name       string          `json:"name"`
	NodeType   string          `json:"nodeType"`
	Parameters map[string]any  `json:"parameters"`
	Inputs     []workflow.Port `json:"inputs"`
	Outputs    []workflow.Port `json:"outputs"`
}

// portsGoldenStoreParameters are the configurations every vector store is
// resolved for. The values are what a JSON document delivers (numbers are float64), and
// the list spans every spelling vectorStoreModeOf accepts, the `operation`
// fallback, and the shapes it must not trip over.
type portsGoldenParameterCase struct {
	name       string
	parameters map[string]any
}

// portsGoldenKey is the identity of a configuration, so a derived case is not
// written twice beside a listed one.
func portsGoldenKey(t *testing.T, parameters map[string]any) string {
	t.Helper()
	encoded, err := json.Marshal(parameters)
	if err != nil {
		t.Fatalf("encoding %v: %v", parameters, err)
	}
	return string(encoded)
}

var portsGoldenStoreParameters = []portsGoldenParameterCase{
	{"no parameters", nil},
	{"empty parameters", map[string]any{}},
	{"operation insert", map[string]any{"operation": "insert"}},
	{"operation search", map[string]any{"operation": "search"}},
	{"operation delete", map[string]any{"operation": "delete"}},
	{"operation retrieve stays a search", map[string]any{"operation": "retrieve"}},
	{"operation getMany", map[string]any{"operation": "getMany"}},
	{"operation retrieve-as-tool", map[string]any{"operation": "retrieve-as-tool"}},
	{"operation unknown", map[string]any{"operation": "frobnicate"}},
	{"mode insert", map[string]any{"mode": "insert"}},
	{"mode Insert", map[string]any{"mode": "Insert"}},
	{"mode insert with a search operation", map[string]any{"mode": "insert", "operation": "search"}},
	{"mode getMany", map[string]any{"mode": "getMany"}},
	{"mode GETMANY", map[string]any{"mode": "GETMANY"}},
	{"mode get_many", map[string]any{"mode": "get_many"}},
	{"mode load", map[string]any{"mode": "load"}},
	{"mode search", map[string]any{"mode": "search"}},
	{"mode retrieve", map[string]any{"mode": "retrieve"}},
	{"mode Retrieve", map[string]any{"mode": "Retrieve"}},
	{"mode retrieve padded", map[string]any{"mode": "  retrieve  "}},
	{"mode retrieve over a search operation", map[string]any{"mode": "retrieve", "operation": "search"}},
	{"mode retrieve-as-tool", map[string]any{"mode": "retrieve-as-tool"}},
	{"mode retrieve_as_tool", map[string]any{"mode": "retrieve_as_tool"}},
	{"mode retrieveAsTool", map[string]any{"mode": "retrieveAsTool"}},
	{"mode delete", map[string]any{"mode": "delete"}},
	{"mode unknown", map[string]any{"mode": "frobnicate"}},
	// A mode key that holds nothing falls back to the operation, but the key
	// is still there, so the typed ports apply. ports.ts mirrors the key
	// check, not the value.
	{"empty mode over an insert operation", map[string]any{"mode": "", "operation": "insert"}},
	{"empty mode over a search operation", map[string]any{"mode": "", "operation": "search"}},
	{"blank mode over a delete operation", map[string]any{"mode": "   ", "operation": "delete"}},
	{"null mode over a search operation", map[string]any{"mode": nil, "operation": "search"}},
	{"null mode alone", map[string]any{"mode": nil}},
	{"numeric mode", map[string]any{"mode": float64(5)}},
	{"boolean mode", map[string]any{"mode": true}},
	{"object mode", map[string]any{"mode": map[string]any{"mode": "expression", "value": "={{ $json.m }}"}}},
	{"numeric operation", map[string]any{"operation": float64(3)}},
}

var portsGoldenEmbeddingsParameters = []portsGoldenParameterCase{
	{"no parameters", nil},
	{"empty parameters", map[string]any{}},
	{"model only", map[string]any{"model": "text-embedding-3-small"}},
	{"mode cluster", map[string]any{"mode": "cluster"}},
	{"mode Cluster", map[string]any{"mode": "Cluster"}},
	{"mode cluster padded", map[string]any{"mode": " cluster "}},
	{"mode sequential", map[string]any{"mode": "sequential"}},
	{"mode empty", map[string]any{"mode": ""}},
	{"mode null", map[string]any{"mode": nil}},
	{"mode unknown", map[string]any{"mode": "frobnicate"}},
	{"mode numeric", map[string]any{"mode": float64(1)}},
}

func portsGoldenRegistry(t *testing.T) *node.Registry {
	t.Helper()
	registry := aiRegistry(t)
	if err := nodes.RegisterVectorNodes(registry, ""); err != nil {
		t.Fatalf("RegisterVectorNodes() error = %v", err)
	}
	return registry
}

func buildPortsGolden(t *testing.T, registry *node.Registry) portsGolden {
	t.Helper()

	golden := portsGolden{
		Comment: "Generated by go test ./nodes -run TestPortsForGolden -update-ports-golden. " +
			"Read by web/src/lib/workflow-editor/ports.golden.test.ts. Do not edit by hand.",
		Definitions: map[string]portsGoldenStatic{},
	}
	resolve := func(nodeType string) node.Definition {
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found {
			t.Fatalf("%s is not registered", nodeType)
		}
		return definition
	}

	covered := []struct {
		nodeType string
		cases    []portsGoldenParameterCase
	}{
		{nodes.VectorStoreNodeType, portsGoldenStoreParameters},
		{nodes.VectorStorePGVectorNodeType, portsGoldenStoreParameters},
		{nodes.EmbeddingsNodeType, portsGoldenEmbeddingsParameters},
	}
	for _, entry := range covered {
		definition := resolve(entry.nodeType)
		if definition.PortsFor == nil {
			t.Fatalf("%s declares no PortsFor, so there is nothing to mirror", entry.nodeType)
		}
		golden.Definitions[entry.nodeType] = portsGoldenStatic{Inputs: definition.Inputs, Outputs: definition.Outputs}

		// The listed cases, then one per value the definition itself offers for a
		// parameter PortsFor reads. A mode or operation option added later has no
		// hand-written case, so without these it would resolve ports nobody pinned;
		// with them it changes the golden, and this test fails until the golden is
		// regenerated and the web suite has been shown the new case.
		cases := append([]portsGoldenParameterCase{}, entry.cases...)
		listed := map[string]bool{}
		for _, c := range cases {
			listed[portsGoldenKey(t, c.parameters)] = true
		}
		for _, key := range []string{"mode", "operation"} {
			for _, property := range definition.Parameters {
				if property.Key != key {
					continue
				}
				for _, option := range property.Options {
					parameters := map[string]any{key: option.Value}
					if listed[portsGoldenKey(t, parameters)] {
						continue
					}
					listed[portsGoldenKey(t, parameters)] = true
					cases = append(cases, portsGoldenParameterCase{name: "offered " + key + " " + option.Value, parameters: parameters})
				}
			}
		}
		for _, c := range cases {
			inputs, outputs := definition.PortsFor(c.parameters, workflow.V(1))
			golden.Cases = append(golden.Cases, portsGoldenCase{
				Name: c.name, NodeType: entry.nodeType, Parameters: c.parameters, Inputs: inputs, Outputs: outputs,
			})
		}
	}
	for _, nodeType := range portsGoldenNeighbours {
		definition := resolve(nodeType)
		golden.Definitions[nodeType] = portsGoldenStatic{Inputs: definition.Inputs, Outputs: definition.Outputs}
	}
	return golden
}

func encodePortsGolden(t *testing.T, golden portsGolden) []byte {
	t.Helper()
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(golden); err != nil {
		t.Fatalf("encoding the ports golden: %v", err)
	}
	return buffer.Bytes()
}

func TestPortsForGolden(t *testing.T) {
	registry := portsGoldenRegistry(t)
	want := encodePortsGolden(t, buildPortsGolden(t, registry))

	if *updatePortsGolden {
		if err := os.WriteFile(filepath.FromSlash(portsGoldenPath), want, 0o644); err != nil {
			t.Fatalf("writing %s: %v", portsGoldenPath, err)
		}
		return
	}
	got, err := os.ReadFile(filepath.FromSlash(portsGoldenPath))
	if err != nil {
		t.Fatalf("reading %s: %v — generate it with go test ./nodes -run TestPortsForGolden -update-ports-golden", portsGoldenPath, err)
	}
	if !bytes.Equal(got, want) {
		t.Errorf("%s no longer matches what PortsFor resolves. Regenerate it with go test ./nodes -run TestPortsForGolden -update-ports-golden, then make web/src/lib/workflow-editor/ports.ts agree: the web suite reads this file", portsGoldenPath)
	}
}

// TestEveryPortsForNodeIsMirroredByTheEditor is the census that keeps the next
// mode-dependent node from being drawn static. A definition that resolves its
// ports from its parameters must either be in the golden or be named in
// portsGoldenMirroredByHand, which is a promise that ports.ts has a case for it.
func TestEveryPortsForNodeIsMirroredByTheEditor(t *testing.T) {
	registry := portsGoldenRegistry(t)
	golden := buildPortsGolden(t, registry)

	inGolden := map[string]bool{}
	for _, c := range golden.Cases {
		inGolden[c.NodeType] = true
	}

	var unmirrored []string
	seen := map[string]bool{}
	for _, definition := range registry.List() {
		if definition.PortsFor == nil || seen[definition.Type] {
			continue
		}
		seen[definition.Type] = true
		if inGolden[definition.Type] {
			continue
		}
		if _, byHand := portsGoldenMirroredByHand[definition.Type]; byHand {
			continue
		}
		unmirrored = append(unmirrored, definition.Type)
	}
	sort.Strings(unmirrored)
	if len(unmirrored) > 0 {
		t.Errorf("node types that resolve their ports from their parameters in Go, but have no case in the editor's resolvedPorts (the canvas would draw their static ports): %s. "+
			"Add the type to the golden's case lists (and a case to ports.ts), or to portsGoldenMirroredByHand if ports.ts already mirrors it with its own tests",
			strings.Join(unmirrored, ", "))
	}

	// And the opposite direction: an entry that no longer resolves anything is a
	// stale promise.
	for nodeType := range portsGoldenMirroredByHand {
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found || definition.PortsFor == nil {
			t.Errorf("portsGoldenMirroredByHand lists %s, which no longer resolves its ports from its parameters", nodeType)
		}
	}
}
