package sdk

import (
	"bytes"
	"encoding/json"
)

// Version is the guest contract version. It moves whenever the stdin/stdout
// envelope changes, and the host keys cached artifacts on it so a pack built
// against an older contract is rebuilt rather than run against today's
// wrapper.
//
// It covers the legacy batch contract — items in, items out — which is what
// example/echo speaks and what a pre-capability pack was built against. The
// invocation envelope and the host module are covered by ABIVersion instead,
// because they arrived without changing a byte of the batch contract.
const Version = "v1"

// Item is one workflow item as pack code sees it. The wire shape matches the
// Code node's (internal/runcode) on purpose: a pack is the same batch
// contract with an author-facing import path instead of an editor textarea.
type Item struct {
	JSON map[string]any `json:"json"`
	// Binary carries references to payloads rather than the payloads, exactly
	// as an item does inside the engine. Reading one needs the binary.read
	// capability; carrying a reference needs none.
	Binary map[string]BinaryRef `json:"binary,omitempty"`
}

// Handle runs run over one stdin document and renders the stdout document.
//
// It is pure — bytes in, bytes plus exit code out — so pack logic is testable
// without a sandbox: unit-test the run function, and call Handle for the
// envelope. Main is the only part that touches the process.
//
// Handle is the legacy entry point: run sees the items and nothing else. It
// still accepts the invocation envelope, so a pack built against the batch
// contract keeps working as a module pack — it simply cannot see parameters or
// call capabilities. A pack that wants either uses MainCall or MainPorts.
func Handle(input []byte, run func([]Item) ([]Item, error)) (output []byte, exit int) {
	call, err := decodeCall(input)
	if err != nil {
		return encodeError(err.Error()), 1
	}
	out, err := run(call.Items)
	if err != nil {
		return encodeError(err.Error()), 1
	}
	return renderItems(out), 0
}

// Main is the wasip1 entry point for a pack written against the batch
// contract. A pack's main package is two lines:
//
//	func main() { sdk.Main(run) }
func Main(run func([]Item) ([]Item, error)) {
	runMain(func(input []byte) ([]byte, int) {
		return Handle(input, run)
	})
}

func encodeError(message string) []byte {
	var buffer bytes.Buffer
	// Encoding a fixed-shape map never fails; if it somehow did there would
	// be nothing truthful left to say, so the error is dropped deliberately.
	_ = json.NewEncoder(&buffer).Encode(map[string]any{"error": message})
	return buffer.Bytes()
}
