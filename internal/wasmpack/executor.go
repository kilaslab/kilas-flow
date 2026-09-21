package wasmpack

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// Catalog resolves the definition of a node type at a version, which is where
// the executor reads the ports a run's output is mapped onto.
type Catalog interface {
	Get(nodeType string, version workflow.TypeVersion) (node.Definition, bool)
}

// Executor runs module packs.
//
// It is one executor for every pack, registered under ExecutorID: the loader
// gives each module pack that executor ID, so a pack cannot name another
// binding and the engine has one place where a module is invoked.
type Executor struct {
	registry *Registry
	catalog  Catalog
	host     *Host
}

// NewExecutor builds the pack executor over one deployment's registry, node
// catalogue and egress policy.
func NewExecutor(registry *Registry, catalog Catalog, deps HostDeps) *Executor {
	return &Executor{registry: registry, catalog: catalog, host: NewHost(deps)}
}

// Execute runs one module pack node.
//
// Item mode (the default) invokes the module once per input item, so a module
// that fails one item fails that item; batch mode invokes it once with every
// item, which is what aggregation needs. Parameters are resolved with the same
// expression context every other executor uses, per item in item mode and once
// for the batch in batch mode — a parameter reading `$json.name` has to see the
// item it is being resolved for.
func (executor *Executor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	spec, ok := executor.registry.Lookup(ir.Type, ir.TypeVersion)
	if !ok {
		return nil, fmt.Errorf("no module is registered for %s v%s", ir.Type, ir.TypeVersion)
	}
	definition, ok := executor.catalog.Get(ir.Type, ir.TypeVersion)
	if !ok {
		return nil, fmt.Errorf("no node definition is registered for %s v%s", ir.Type, ir.TypeVersion)
	}

	items := input["main"]
	if spec.Mode == ModeBatch {
		ports, err := executor.runOnce(ctx, spec, definition, ir, items, input, request, -1)
		if err != nil {
			return nil, err
		}
		return ports, nil
	}

	// Item mode: one invocation per item, each carrying its own resolved
	// parameters, concatenated into the ports the module declares.
	ports := make([][]workflow.Item, len(spec.Outputs))
	for index, item := range items {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		produced, err := executor.runOnce(ctx, spec, definition, ir, []workflow.Item{item}, input, request, index)
		if err != nil {
			return nil, err
		}
		for port := range ports {
			if port < len(produced) {
				ports[port] = append(ports[port], produced[port]...)
			}
		}
	}
	return ports, nil
}

// runOnce invokes the module once and maps its answer onto the declared ports.
//
// itemIndex is the item whose expressions are resolved; -1 means the batch
// call, whose parameters resolve against the first item (there is no single
// item a batch belongs to, and resolving against none would leave every
// `$json` parameter empty).
func (executor *Executor) runOnce(ctx context.Context, spec Spec, definition node.Definition, ir workflow.IRNode, items []workflow.Item, input workflow.NodeInput, request engine.Request, itemIndex int) ([][]workflow.Item, error) {
	resolutionIndex := itemIndex
	if resolutionIndex < 0 {
		resolutionIndex = 0
	}
	resolutionItem := workflow.Item{JSON: map[string]any{}}
	if len(items) > 0 {
		resolutionItem = items[resolutionIndex]
	}
	parameters, err := expression.Resolve(ir.Parameters, request.ExpressionContext(resolutionItem, input, resolutionIndex))
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	// The pack ABI carries a whole-numbered node version, and every node type
	// in this tree has one. A fractional version is refused rather than
	// truncated: a pack told it is running v4 when the graph says v4.2 would
	// branch on the wrong thing.
	version, whole := wholeVersion(ir.TypeVersion)
	if !whole {
		return nil, fmt.Errorf("node %q: this pack ABI carries whole-numbered node versions, and %s is not one", ir.Name, ir.TypeVersion)
	}
	envelope := sdk.Envelope{
		ABI: sdk.ABIVersion,
		Node: sdk.NodeInfo{
			Type: ir.Type, Version: version, Name: ir.Name,
		},
		Parameters: parameters,
		Items:      make([]sdk.Item, 0, len(items)),
	}
	binary := make([]workflow.BinaryRef, 0, 4)
	for _, item := range items {
		converted := sdk.Item{JSON: item.JSON}
		if len(item.Binary) > 0 {
			converted.Binary = map[string]sdk.BinaryRef{}
			for name, ref := range item.Binary {
				converted.Binary[name] = sdk.BinaryRef{
					ID: ref.ID, FileName: ref.FileName, MediaType: ref.MediaType, Size: ref.Size,
				}
				binary = append(binary, ref)
			}
		}
		envelope.Items = append(envelope.Items, converted)
	}
	stdin, err := json.Marshal(envelope)
	if err != nil {
		return nil, fmt.Errorf("node %q: encode the pack's invocation: %w", ir.Name, err)
	}

	outcome, _, err := executor.host.Invoke(ctx, Invocation{
		Module:      spec.Module,
		Caps:        spec.Caps,
		Limits:      spec.Limits,
		IR:          ir,
		Request:     request,
		Stdin:       stdin,
		InputBinary: binary,
		Subject:     fmt.Sprintf("node %q (pack %s)", ir.Name, ir.Type),
	})
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return decodePorts(spec, definition, outcome.Stdout, ir.Name)
}

// decodePorts reads the module's answer into the node's output ports.
//
// Two shapes are accepted, and both come from the SDK rather than from a
// convention invented here: one list of items (what MainCall and Handle write)
// lands on the first port, and a list per port (what MainPorts writes) lands on
// the ports the manifest declared, in order. A document naming more ports than
// the manifest declared is refused: a pack that writes to a port the operator
// never approved is not a pack this deployment runs.
func decodePorts(spec Spec, definition node.Definition, stdout []byte, nodeName string) ([][]workflow.Item, error) {
	trimmed := strings.TrimSpace(string(stdout))
	if trimmed == "" {
		return nil, fmt.Errorf("node %q: the pack wrote nothing to standard output", nodeName)
	}
	var ports [][]sdk.Item
	if err := json.Unmarshal([]byte(trimmed), &ports); err != nil {
		var single []sdk.Item
		if err := json.Unmarshal([]byte(trimmed), &single); err != nil {
			return nil, fmt.Errorf("node %q: the pack's output is neither a list of items nor a list per port: %v", nodeName, err)
		}
		ports = [][]sdk.Item{single}
	}
	if len(ports) > len(spec.Outputs) {
		return nil, fmt.Errorf("node %q: the pack wrote to %d ports but its manifest declares %d (%s)",
			nodeName, len(ports), len(spec.Outputs), strings.Join(spec.Outputs, ", "))
	}
	// The engine's ports come from the definition, so the order the module
	// wrote in is the order the compiler resolved.
	outputs := make([][]workflow.Item, len(definition.Outputs))
	for index, port := range ports {
		converted := make([]workflow.Item, 0, len(port))
		for _, item := range port {
			built := workflow.Item{JSON: item.JSON}
			if built.JSON == nil {
				built.JSON = map[string]any{}
			}
			if len(item.Binary) > 0 {
				built.Binary = map[string]workflow.BinaryRef{}
				for name, ref := range item.Binary {
					built.Binary[name] = workflow.BinaryRef{
						ID: ref.ID, FileName: ref.FileName, MediaType: ref.MediaType, Size: ref.Size,
					}
				}
			}
			converted = append(converted, built)
		}
		outputs[index] = converted
	}
	return outputs, nil
}

// wholeVersion reports the version as an int when it is whole.
func wholeVersion(version workflow.TypeVersion) (int, bool) {
	value := version.Float()
	rounded := int(value)
	if float64(rounded) != value {
		return 0, false
	}
	return rounded, true
}
