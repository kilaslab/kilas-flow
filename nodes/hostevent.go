package nodes

import (
	"context"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// HostEventNodeType is the built-in trigger that starts a workflow when the
// host publishes an event through POST /api/v1/events. HostEventEventsLoader
// is the internal options loader its picker reads, registered at composition
// with the node registry the packs were loaded into.
const (
	HostEventNodeType     = "kilasflow.hostEvent"
	HostEventExecutorID   = "core.hostEvent"
	HostEventEventsLoader = "hostEvents"
	hostEventParameterKey = "events"
)

// hostEventTrigger starts a workflow from an event the host publishes.
//
// One output, not one per event: a workflow that listens for several types
// branches on `{{ $json.type }}` rather than wiring a port per event, which
// keeps the canvas honest about what varies — the payload, not the wiring.
// A pack trigger that opts into host events is the per-port shape, and gets
// it from its own declaration instead.
//
// The subscriptions are a parameter rather than fixed by the type because a
// host's event catalogue is the host's to declare, usually in a pack: the
// picker loads the types from every pack that declares them (see
// loadoptions.HostEvents), so the editor offers exactly the events this
// server knows about.
func hostEventTrigger() node.Definition {
	return node.Definition{
		Type:        HostEventNodeType,
		Version:     workflow.V(1),
		DisplayName: "Host Event",
		Description: "Starts a workflow when the host publishes an event this trigger listens for.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:box"},
		IconColor:   "#8b5cf6",
		Subtitle:    "{{ $parameter.events }}",
		Outputs:     mainOutput(),
		HostEvents: &node.HostEventDeclaration{
			Parameter: hostEventParameterKey,
		},
		Parameters: []node.PropertyDefinition{
			{
				Key: hostEventParameterKey, Label: "Events", Kind: node.PropertyMultiOptions, Required: true,
				Description: "The host event types this workflow starts for. The list is offered by the node packs " +
					"this server carries that declare a host event catalogue — the host's own product pack is the " +
					"usual author — and every active workflow listening for a published type runs when it is published.",
				LoadOptions: &node.OptionsLoader{
					Source: property.LoaderInternal, Name: HostEventEventsLoader,
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     HostEventExecutorID,
	}
}

// executeHostEvent emits the published event as the trigger item.
//
// The document was assembled at the events boundary: the event's own type,
// payload, occurredAt and eventId, so a workflow reads `{{ $json.payload }}`
// and branches on `{{ $json.type }}`.
func executeHostEvent(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}
