package wasmpack_test

import (
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/wasmpack"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// TestALegacyPackRunsWithTheEnvelopeAndNoCapabilities pins the compatibility
// decision the ABI rests on: the pack executor writes one wire format — the
// invocation envelope — and a pack built against the older batch contract
// (example/echo, which is what sdk.Main produces) still runs, seeing the items
// and nothing else.
//
// It is also the "declares none, granted none" case end to end: echo imports no
// host function at all, so this run has no host module and no capability, and
// it still produces its items.
func TestALegacyPackRunsWithTheEnvelopeAndNoCapabilities(t *testing.T) {
	module := exampleGuest(t, "echo")
	host := wasmpack.NewHost(wasmpack.HostDeps{Policy: safehttp.DefaultPolicy(), Modules: testModules()})

	envelope, err := json.Marshal(sdk.Envelope{
		ABI:  sdk.ABIVersion,
		Node: sdk.NodeInfo{Type: "core.packEcho", Version: 1, Name: "Echo"},
		// A legacy pack cannot see parameters, which is exactly the point:
		// they are carried and ignored.
		Parameters: map[string]any{"greeting": "hallo"},
		Items:      []sdk.Item{{JSON: map[string]any{"name": "ada"}}},
	})
	if err != nil {
		t.Fatalf("encoding the envelope failed: %v", err)
	}

	outcome, _, err := runGuest(t, host, module, wasmpack.Invocation{Stdin: envelope})
	if err != nil {
		t.Fatalf("running the legacy pack failed with %v", err)
	}
	var produced struct {
		Items []sdk.Item `json:"items"`
	}
	if err := json.Unmarshal(outcome.Stdout, &produced); err != nil {
		t.Fatalf("the pack's output is not JSON: %v\n%s", err, outcome.Stdout)
	}
	if len(produced.Items) != 1 {
		t.Fatalf("output = %s, want one item", outcome.Stdout)
	}
	if produced.Items[0].JSON["greeting"] != "hello, ada" {
		t.Errorf("item = %v, want the pack's greeting", produced.Items[0].JSON)
	}
}
