package sdk_test

import (
	"encoding/json"
	"errors"
	"strings"
	"testing"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

func echo(items []sdk.Item) ([]sdk.Item, error) { return items, nil }

func decodeEnvelope(t *testing.T, output []byte) map[string]any {
	t.Helper()
	var envelope map[string]any
	if err := json.Unmarshal(output, &envelope); err != nil {
		t.Fatalf("output is not JSON: %v\n%s", err, output)
	}
	return envelope
}

func TestHandlePassesItemsThrough(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[{"json":{"name":"ada"}}]`), echo)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	envelope := decodeEnvelope(t, output)
	items, ok := envelope["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("envelope = %v, want one item", envelope)
	}
	name := items[0].(map[string]any)["json"].(map[string]any)["name"]
	if name != "ada" {
		t.Errorf("name = %v, want ada", name)
	}
}

func TestHandleNormalisesNilToEmptyArray(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[]`), func([]sdk.Item) ([]sdk.Item, error) {
		return nil, nil
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	if !strings.Contains(string(output), `"items":[]`) {
		t.Errorf("output = %s, want an empty items array rather than null", output)
	}
}

func TestHandleReportsRunFailureAsErrorEnvelope(t *testing.T) {
	output, exit := sdk.Handle([]byte(`[]`), func([]sdk.Item) ([]sdk.Item, error) {
		return nil, errors.New("upstream refused the key")
	})
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero\n%s", output)
	}
	envelope := decodeEnvelope(t, output)
	if envelope["error"] != "upstream refused the key" {
		t.Errorf("envelope = %v, want the run message", envelope)
	}
}

func TestHandleReportsBadInputAsErrorEnvelope(t *testing.T) {
	output, exit := sdk.Handle([]byte(`not json`), echo)
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero\n%s", output)
	}
	envelope := decodeEnvelope(t, output)
	message, _ := envelope["error"].(string)
	if !strings.HasPrefix(message, "input could not be decoded: ") {
		t.Errorf("envelope = %v, want an input decoding diagnostic", envelope)
	}
}

func TestVersionIsPinned(t *testing.T) {
	if sdk.Version == "" {
		t.Error("Version must be set: the host keys cached artifacts on it")
	}
}

// envelope is what the host writes to a pack's stdin.
const envelope = `{
	"abi": "v1",
	"node": {"type": "core.packFetch", "version": 1, "name": "Fetch"},
	"parameters": {"url": "https://api.example.test/items"},
	"items": [{"json": {"name": "ada"}, "binary": {"data": {"id": "bin_1", "fileName": "a.csv", "size": 3}}}]
}`

// TestHandleAcceptsTheInvocationEnvelope is the compatibility decision: a pack
// written against the batch contract (example/echo) is handed the envelope by
// the pack executor, so the legacy entry point has to read it. It sees the
// items and nothing else.
func TestHandleAcceptsTheInvocationEnvelope(t *testing.T) {
	output, exit := sdk.Handle([]byte(envelope), echo)
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	envelope := decodeEnvelope(t, output)
	items, ok := envelope["items"].([]any)
	if !ok || len(items) != 1 {
		t.Fatalf("envelope = %v, want the one item", envelope)
	}
	name := items[0].(map[string]any)["json"].(map[string]any)["name"]
	if name != "ada" {
		t.Errorf("name = %v, want ada", name)
	}
}

// TestHandleCallSeesParametersAndCarriesBinaryReferences proves the capability
// entry point gets the whole call: the node, the resolved parameters and the
// items' payload references.
func TestHandleCallSeesParametersAndCarriesBinaryReferences(t *testing.T) {
	var seen sdk.Call
	output, exit := sdk.HandleCall([]byte(envelope), func(call sdk.Call) ([][]sdk.Item, error) {
		seen = call
		return [][]sdk.Item{call.Items, {{JSON: map[string]any{"second": true}}}}, nil
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	if seen.Node.Type != "core.packFetch" || seen.Node.Version != 1 || seen.Node.Name != "Fetch" {
		t.Errorf("node = %+v, want the envelope's node", seen.Node)
	}
	if seen.Parameters["url"] != "https://api.example.test/items" {
		t.Errorf("parameters = %v, want the envelope's parameters", seen.Parameters)
	}
	ref, ok := seen.Items[0].Binary["data"]
	if !ok || ref.ID != "bin_1" || ref.FileName != "a.csv" || ref.Size != 3 {
		t.Errorf("item binary = %v, want the envelope's reference", seen.Items[0].Binary)
	}

	envelope := decodeEnvelope(t, output)
	ports, ok := envelope["outputs"].([]any)
	if !ok || len(ports) != 2 {
		t.Fatalf("envelope = %v, want two output ports", envelope)
	}
	if len(ports[1].([]any)) != 1 {
		t.Errorf("second port = %v, want one item", ports[1])
	}
}

// TestHandleCallRefusesAnOlderABI proves a module does not silently run an
// invocation written for a contract it does not speak.
func TestHandleCallRefusesAnOlderABI(t *testing.T) {
	output, exit := sdk.HandleCall([]byte(`{"abi":"v0","items":[]}`), func(sdk.Call) ([][]sdk.Item, error) {
		return nil, nil
	})
	if exit == 0 {
		t.Fatalf("exit = 0, want non-zero\n%s", output)
	}
	message, _ := decodeEnvelope(t, output)["error"].(string)
	if !strings.Contains(message, `ABI "v0"`) {
		t.Errorf("error = %q, want it to name the ABI it was written for", message)
	}
}

// TestHandleCallNormalisesEmptyPorts keeps a port with no items an empty array
// rather than null, so the host's decoder never has to guess which nil means
// which.
func TestHandleCallNormalisesEmptyPorts(t *testing.T) {
	output, exit := sdk.HandleCall([]byte(`[]`), func(sdk.Call) ([][]sdk.Item, error) {
		return [][]sdk.Item{nil}, nil
	})
	if exit != 0 {
		t.Fatalf("exit = %d, want 0\n%s", exit, output)
	}
	if !strings.Contains(string(output), `"outputs":[[]]`) {
		t.Errorf("output = %s, want one empty port", output)
	}
}
