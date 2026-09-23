package handlers

import (
	"reflect"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/events"
)

// Every name the engine or an executor publishes must reach an EventSource
// by name. A name missing from the schema map goes out as an unnamed
// `message` frame that listeners by name never see, and huma prints a stack
// trace for each one — the canvas chat could not stream an agent's tokens
// because every ai.* event was lost this way.
func TestEveryPublishedEventNameIsRegistered(t *testing.T) {
	published := []events.Type{
		events.ExecutionStarted,
		events.ExecutionCompleted,
		events.ExecutionFailed,
		events.ExecutionCancelled,
		engine.EventExecutionWaiting,
		events.NodeStarted,
		events.NodeOutput,
		events.NodeCompleted,
		events.NodeFailed,
		events.WorkflowSaved,
		events.Type(engine.ResponseEventName),
		events.Type(ai.EventModelStarted),
		events.Type(ai.EventModelDelta),
		events.Type(ai.EventModelCompleted),
		events.Type(ai.EventToolStarted),
		events.Type(ai.EventToolCompleted),
		events.Type(ai.EventToolFailed),
		events.Type(ai.EventAgentCompleted),
		events.Type(ai.EventAgentFailed),
	}
	schemas := executionEventSchemas()
	for _, name := range published {
		schema, ok := schemas[string(name)]
		if !ok {
			t.Errorf("event %q has no SSE schema entry", name)
			continue
		}
		typed := typedEvent(ExecutionEvent{ExecutionID: "exec_1"}, name)
		if reflect.TypeOf(typed) != reflect.TypeOf(schema) {
			t.Errorf("typedEvent(%q) = %T, want %T", name, typed, schema)
		}
	}
}

// A name nobody registered yet still leaves as a named frame, so a client can
// listen for it and huma never logs an "unknown event type" trace.
func TestAnUnregisteredEventNameFallsBackToANamedFrame(t *testing.T) {
	typed := typedEvent(ExecutionEvent{ExecutionID: "exec_1"}, events.Type("something.new"))
	schema, ok := executionEventSchemas()[OtherEventName]
	if !ok {
		t.Fatalf("the fallback name %q has no schema entry", OtherEventName)
	}
	if reflect.TypeOf(typed) != reflect.TypeOf(schema) {
		t.Fatalf("typedEvent(unknown) = %T, want the fallback %T", typed, schema)
	}
}
