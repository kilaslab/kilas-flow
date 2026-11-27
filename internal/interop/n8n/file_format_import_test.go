package n8n_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// importNode imports one legacy node and returns what it became, with the
// result for the blocking-issue checks.
func importNode(t *testing.T, nodeType string, typeVersion float64, parameters string) (workflow.Node, n8n.ImportResult) {
	t.Helper()
	result := importFixture(t, legacyNodeFixture(nodeType, typeVersion, parameters))
	return nodeByName(result.Document, "Legacy"), result
}

// exportedNode exports an imported document and returns the n8n node it came
// back as.
func exportedLegacyNode(t *testing.T, result n8n.ImportResult) n8n.Node {
	t.Helper()
	exported, err := n8n.Export(result.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	for _, candidate := range exported.Document.Nodes {
		if candidate.Name == "Legacy" {
			return candidate
		}
	}
	t.Fatalf("the exported document has no node named Legacy")
	return n8n.Node{}
}

// executeImportedWithBinaries is executeImported with a binary store, for the
// nodes that read and write files.
func executeImportedWithBinaries(t *testing.T, imported workflow.Node, executorID string, input workflow.NodeInput, store engine.BinaryStore) workflow.NodeOutput {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	executor, found := executors.Lookup(executorID)
	if !found {
		t.Fatalf("executor %q is not registered", executorID)
	}
	definition, found := registry(t).Lookup(imported.Type, imported.TypeVersion)
	if !found {
		t.Fatalf("%s is not registered", imported.Type)
	}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: imported.ID, Name: imported.Name, Type: imported.Type, TypeVersion: imported.TypeVersion,
		Parameters: imported.Parameters, Definition: definition,
	}, input, engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func binaryStoreForImport(t *testing.T) engine.BinaryStore {
	t.Helper()
	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	return binary.For(store, "tenant-a", "exec-1")
}

func TestTheFileFormatTypesMatchTheNodePack(t *testing.T) {
	t.Parallel()

	if n8n.CryptoNodeType != nodes.CryptoNodeType {
		t.Fatalf("the adapter's mirrored types differ from the node pack's: crypto %s vs %s", n8n.CryptoNodeType, nodes.CryptoNodeType)
	}
}

// --- Crypto --------------------------------------------------------------------

func TestCryptoImportsRunsAndRoundTrips(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.crypto", 1,
		`{"action":"hmac","type":"SHA256","value":"The quick brown fox jumps over the lazy dog","secret":"key","dataPropertyName":"mac","encoding":"hex"}`)
	if imported.Type != n8n.CryptoNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.CryptoNodeType)
	}
	if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
		t.Fatalf("blocking issues %#v, want none", blocking)
	}

	output := executeImported(t, imported, nodes.CryptoExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{}}}})
	if output[0][0].JSON["mac"] != "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8" {
		t.Fatalf("output = %#v, want the published HMAC-SHA256 vector", output[0][0].JSON)
	}

	returned := exportedLegacyNode(t, result)
	if returned.Type != "n8n-nodes-base.crypto" || returned.TypeVersion != 1 {
		t.Fatalf("exported as %s v%v, want n8n-nodes-base.crypto v1", returned.Type, returned.TypeVersion)
	}
	want := map[string]any{
		"action": "hmac", "type": "SHA256", "value": "The quick brown fox jumps over the lazy dog",
		"secret": "key", "dataPropertyName": "mac", "encoding": "hex",
	}
	if !reflect.DeepEqual(returned.Parameters, want) {
		t.Fatalf("exported parameters = %#v, want %#v", returned.Parameters, want)
	}
}

// A hash node that n8n saved with every default left out still imports as the
// default hash: MD5, hex, into data.
func TestCryptoImportsAnAllDefaultsNodeAsTheDefaultHash(t *testing.T) {
	t.Parallel()

	imported, _ := importNode(t, "n8n-nodes-base.crypto", 1, `{"value":"hello"}`)
	if imported.Type != n8n.CryptoNodeType {
		t.Fatalf("imported as %q, want %q", imported.Type, n8n.CryptoNodeType)
	}
	output := executeImported(t, imported, nodes.CryptoExecutorID, workflow.NodeInput{"main": {{JSON: map[string]any{}}}})
	if output[0][0].JSON["data"] != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("output = %#v, want the MD5 of hello in data", output[0][0].JSON)
	}
}

func TestCryptoCarriesAnExpressionValueAsAnExpressionAndBack(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.crypto", 1, `{"action":"hash","type":"SHA256","value":"={{ $json.email }}"}`)
	value, _ := imported.Parameters["value"].(map[string]any)
	if value["mode"] != "expression" || value["value"] != "{{ $json.email }}" {
		t.Fatalf("value = %#v, want an expression marker", imported.Parameters["value"])
	}
	if returned := exportedLegacyNode(t, result); returned.Parameters["value"] != "={{ $json.email }}" {
		t.Fatalf("exported value = %#v, want the =-prefixed expression", returned.Parameters["value"])
	}
}

func TestCryptoRefusesASigningAlgorithmItDoesNotImplement(t *testing.T) {
	t.Parallel()

	imported, result := importNode(t, "n8n-nodes-base.crypto", 1, `{"action":"sign","algorithm":"RSA-MD5","value":"x","privateKey":"k"}`)
	if imported.Type != n8n.UnsupportedNodeType {
		t.Fatalf("imported as %q, want the unsupported placeholder", imported.Type)
	}
	blocking := blockingFor(result, "Legacy")
	if len(blocking) == 0 || !strings.Contains(blocking[0].Reason, "RSA-MD5") {
		t.Fatalf("blocking = %#v, want a reason naming RSA-MD5", blocking)
	}
	// The algorithms it does implement import as the node.
	imported, _ = importNode(t, "n8n-nodes-base.crypto", 1, `{"action":"sign","algorithm":"RSA-SHA512","value":"x","privateKey":"k"}`)
	if imported.Type != n8n.CryptoNodeType {
		t.Fatalf("RSA-SHA512 imported as %q, want %q", imported.Type, n8n.CryptoNodeType)
	}
}
