package sdk

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
)

// Version is the guest contract version. It moves whenever the stdin/stdout
// envelope changes, and the host keys cached artifacts on it so a pack built
// against an older contract is rebuilt rather than run against today's
// wrapper.
const Version = "v1"

// Item is one workflow item as pack code sees it. The wire shape matches the
// Code node's (internal/runcode) on purpose: a pack is the same batch
// contract with an author-facing import path instead of an editor textarea.
type Item struct {
	JSON map[string]any `json:"json"`
}

// Handle runs run over one stdin document and renders the stdout document.
//
// It is pure — bytes in, bytes plus exit code out — so pack logic is testable
// without a sandbox: unit-test the run function, and call Handle for the
// envelope. Main is the only part that touches the process.
func Handle(input []byte, run func([]Item) ([]Item, error)) (output []byte, exit int) {
	var items []Item
	decoder := json.NewDecoder(bytes.NewReader(input))
	if err := decoder.Decode(&items); err != nil {
		return encodeError("input could not be decoded: " + err.Error()), 1
	}
	out, err := run(items)
	if err != nil {
		return encodeError(err.Error()), 1
	}
	if out == nil {
		out = []Item{}
	}
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(map[string]any{"items": out}); err != nil {
		return encodeError("output could not be encoded: " + err.Error()), 1
	}
	return buffer.Bytes(), 0
}

// Main is the wasip1 entry point for a pack module. A pack's main package is
// two lines:
//
//	func main() { sdk.Main(run) }
//
// Reading stdin and exiting non-zero on failure mirror internal/runcode's
// wrapper exactly, so the host cannot tell a hand-built pack from one the
// toolchain compiled out of the editor.
func Main(run func([]Item) ([]Item, error)) {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		_, _ = os.Stdout.Write(encodeError("input could not be read: " + err.Error()))
		os.Exit(1)
	}
	output, exit := Handle(input, run)
	_, _ = os.Stdout.Write(output)
	if exit != 0 {
		os.Exit(exit)
	}
}

func encodeError(message string) []byte {
	var buffer bytes.Buffer
	// Encoding a fixed-shape map never fails; if it somehow did there would
	// be nothing truthful left to say, so the error is dropped deliberately.
	_ = json.NewEncoder(&buffer).Encode(map[string]any{"error": message})
	return buffer.Bytes()
}
