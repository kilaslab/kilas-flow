package sdk

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"os"
)

// Call is one invocation of a pack.
//
// It is what the host's envelope carries, decoded: which node is running, the
// parameters the host resolved for this item (or this batch), and the items.
// Node and Parameters are empty for a pack written against the legacy batch
// contract, which is handed the items and nothing else.
type Call struct {
	Node       NodeInfo
	Parameters map[string]any
	Items      []Item
}

// HandleCall runs run over one invocation and renders the output document.
//
// It is pure — bytes in, bytes plus exit code out — so a pack's logic is
// testable without a sandbox. run receives one Call and returns one item slice
// per declared output port.
func HandleCall(input []byte, run func(Call) ([][]Item, error)) (output []byte, exit int) {
	call, err := decodeCall(input)
	if err != nil {
		return encodeError(err.Error()), 1
	}
	ports, err := run(call)
	if err != nil {
		return encodeError(err.Error()), 1
	}
	return renderPorts(ports), 0
}

// MainCall is the wasip1 entry point for a pack with one output port:
//
//	func main() { sdk.MainCall(run) }
//
// The output document is the single-port `{"items": [...]}` shape, which is
// what the legacy batch contract emits and what the host reads back for a pack
// whose definition declares one port.
func MainCall(run func(Call) ([]Item, error)) {
	runMain(func(input []byte) ([]byte, int) {
		call, err := decodeCall(input)
		if err != nil {
			return encodeError(err.Error()), 1
		}
		items, err := run(call)
		if err != nil {
			return encodeError(err.Error()), 1
		}
		return renderItems(items), 0
	})
}

// MainPorts is the wasip1 entry point for a pack with several output ports:
//
//	func main() { sdk.MainPorts(run) }
//
// The output document is `{"outputs": [[...], [...]]}`, one array per port in
// the order the node definition declares them.
func MainPorts(run func(Call) ([][]Item, error)) {
	runMain(func(input []byte) ([]byte, int) {
		return HandleCall(input, run)
	})
}

// decodeCall reads either stdin shape.
//
// A pack's stdin is the invocation envelope, but a pack written against the
// legacy batch contract — example/echo is one — is handed a bare item array by
// the same executor, because the executor has one wire format and the SDK
// tolerates the older contract rather than the host keeping two. The first
// byte tells them apart: an envelope is an object, a bare batch is an array.
func decodeCall(input []byte) (Call, error) {
	trimmed := bytes.TrimSpace(input)
	if len(trimmed) == 0 {
		return Call{}, fmt.Errorf("input could not be decoded: it was empty")
	}
	if trimmed[0] == '[' {
		var items []Item
		if err := json.Unmarshal(trimmed, &items); err != nil {
			return Call{}, fmt.Errorf("input could not be decoded: %w", err)
		}
		return Call{Items: items}, nil
	}
	var envelope Envelope
	if err := json.Unmarshal(trimmed, &envelope); err != nil {
		return Call{}, fmt.Errorf("input could not be decoded: %w", err)
	}
	if envelope.ABI != "" && envelope.ABI != ABIVersion {
		return Call{}, fmt.Errorf("input was written for ABI %q and this module speaks %q", envelope.ABI, ABIVersion)
	}
	return Call{Node: envelope.Node, Parameters: envelope.Parameters, Items: envelope.Items}, nil
}

// renderItems renders the single-port success document.
func renderItems(items []Item) []byte {
	if items == nil {
		items = []Item{}
	}
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(map[string]any{"items": items}); err != nil {
		return encodeError("output could not be encoded: " + err.Error())
	}
	return buffer.Bytes()
}

// renderPorts renders the multi-port success document.
func renderPorts(ports [][]Item) []byte {
	if ports == nil {
		ports = [][]Item{}
	}
	for index, port := range ports {
		if port == nil {
			// A port with no items is an empty array rather than null, so the
			// host's decoder does not have to know which nil means which.
			ports[index] = []Item{}
		}
	}
	var buffer bytes.Buffer
	if err := json.NewEncoder(&buffer).Encode(map[string]any{"outputs": ports}); err != nil {
		return encodeError("output could not be encoded: " + err.Error())
	}
	return buffer.Bytes()
}

// runMain is the shared wasip1 entry point: read stdin, render, exit.
//
// Reading stdin and exiting non-zero on failure mirror internal/runcode's
// wrapper exactly, so the host cannot tell a hand-built pack from one the
// toolchain compiled out of the editor.
func runMain(handle func([]byte) ([]byte, int)) {
	input, err := io.ReadAll(os.Stdin)
	if err != nil {
		_, _ = os.Stdout.Write(encodeError("input could not be read: " + err.Error()))
		os.Exit(1)
	}
	output, exit := handle(input)
	_, _ = os.Stdout.Write(output)
	if exit != 0 {
		os.Exit(exit)
	}
}
