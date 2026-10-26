// Package hostevents is the host → workflow path: the fan-out that takes one
// published event and queues one execution per active workflow listening for
// it.
//
// A host embedding KilasFlow raises domain events — a message received, a
// contact tagged, a booking created — and wants every active workflow wired to
// that event to run, without keeping one webhook route per workflow or calling
// the run API per workflow (which runs the latest revision and ignores
// activation). Publishing one event here is that whole story in one request.
package hostevents

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Catalog is the slice of the node registry extraction needs. Declaring it
// here rather than taking *node.Registry keeps this package testable without
// one and says exactly what it reads, the same seam internal/webhook uses.
type Catalog interface {
	// Resolve rather than Get, so a document carrying n8n's own typeVersion
	// finds the definition the compiler will run it against.
	Resolve(nodeType string, version workflow.TypeVersion) (node.Definition, bool)
}

// Extract pulls a document's host-event subscriptions out of it: every
// enabled trigger node whose type declares host events, with the event types
// it listens for.
//
// The declaration on the definition says where the subscriptions live — a
// parameter the node carries, for the built-in trigger, or the type's own
// event list, for a pack trigger that opted in — so this stays free of
// node-type names exactly as webhook extraction does.
//
// A node that declares no subscriptions extracts none, the same way a webhook
// node with no path binds no route. The queue the fan-out writes is what
// makes an unlistened-for event visible as a no-op rather than an error.
func Extract(catalog Catalog) repository.HostEventExtractor {
	return func(document workflow.Document) []repository.HostEventTrigger {
		triggers := make([]repository.HostEventTrigger, 0, 1)
		for _, candidate := range document.Nodes {
			if candidate.Disabled {
				// A trigger its author switched off must not hear events: the
				// runner refuses to start a disabled trigger, so subscribing
				// it would publish an event that reaches a workflow whose
				// branch never runs.
				continue
			}
			definition, found := catalog.Resolve(candidate.Type, candidate.TypeVersion)
			if !found || definition.HostEvents == nil {
				continue
			}
			declaration := definition.HostEvents
			events := declaration.Events
			if declaration.Parameter != "" {
				events = eventList(candidate.Parameters[declaration.Parameter])
			}
			if len(events) == 0 {
				continue
			}
			triggers = append(triggers, repository.HostEventTrigger{
				NodeID:    candidate.ID,
				NodeType:  candidate.Type,
				Events:    events,
				EventPath: declaration.EventPath,
			})
		}
		return triggers
	}
}

// eventList reads a subscription parameter as a list of event types. A single
// string counts, because a hand-authored document is under no obligation to
// wrap one event type in an array.
func eventList(value any) []string {
	switch typed := value.(type) {
	case []string:
		return trimmed(typed)
	case []any:
		events := make([]string, 0, len(typed))
		for _, entry := range typed {
			if text, ok := entry.(string); ok {
				events = append(events, text)
			}
		}
		return trimmed(events)
	case string:
		return trimmed([]string{typed})
	default:
		return nil
	}
}

func trimmed(events []string) []string {
	listened := make([]string, 0, len(events))
	for _, event := range events {
		if trimmed := strings.TrimSpace(event); trimmed != "" {
			listened = append(listened, trimmed)
		}
	}
	return listened
}
