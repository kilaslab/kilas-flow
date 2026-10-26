package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/hostevents"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// recordingPublisher is the fan-out behind the endpoint, recording what it
// was asked to publish and answering from a script.
type recordingPublisher struct {
	mu        sync.Mutex
	published []recordedEvent
	answer    hostevents.Result
}

// recordedEvent is one publish the stub saw.
type recordedEvent struct {
	tenant    string
	eventType string
	eventID   string
	payload   string
}

func (stub *recordingPublisher) Publish(_ context.Context, tenant repository.TenantScope, event hostevents.Event) (hostevents.Result, error) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.published = append(stub.published, recordedEvent{
		tenant:    tenant.ID,
		eventType: event.Type,
		eventID:   event.ID,
		payload:   string(event.Payload),
	})
	return stub.answer, nil
}

func (stub *recordingPublisher) events() []recordedEvent {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]recordedEvent(nil), stub.published...)
}

// Publishing an event is tenant-scoped and answers with what the fan-out did:
// the matched workflows with their executions, and the filtered ones with the
// reason.
func TestPublishingAHostEventReportsTheFanout(t *testing.T) {
	publisher := &recordingPublisher{answer: hostevents.Result{
		Matched: []hostevents.Matched{{WorkflowID: "wf_a", ExecutionID: "exec_1"}},
		Filtered: []hostevents.Filtered{
			{WorkflowID: "wf_b", Reason: hostevents.FilteredDuplicate, ExecutionID: "exec_0"},
		},
	}}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		wireHostEvents(t, deps, publisher)
	}, "acme", "globex")

	body := server.callJSON(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"eventId": "evt-1", "type": "message.received",
		"payload": map[string]any{"contactId": "c_1"},
	}, http.StatusAccepted)

	matched, _ := body["matched"].([]any)
	if len(matched) != 1 {
		t.Fatalf("matched = %v, want the one workflow", body["matched"])
	}
	filtered, _ := body["filtered"].([]any)
	if len(filtered) != 1 {
		t.Fatalf("filtered = %v, want the duplicate", body["filtered"])
	}

	// The publish reached the fan-out scoped to the caller's tenant, payload
	// and id carried through.
	events := publisher.events()
	if len(events) != 1 || events[0].tenant != "acme" || events[0].eventType != "message.received" ||
		events[0].eventID != "evt-1" || events[0].payload != `{"contactId":"c_1"}` {
		t.Fatalf("the fan-out saw %+v, want acme's message.received with its id and payload", events)
	}

	// Another tenant's publish is another tenant's event.
	server.callJSON(t, http.MethodPost, "/api/v1/events", keys["globex"], map[string]any{
		"type": "message.received",
	}, http.StatusAccepted)
	if events = publisher.events(); len(events) != 2 || events[1].tenant != "globex" {
		t.Fatalf("the fan-out saw %+v, want globex's publish second", events)
	}
}

// An instance without the fan-out wired answers 503 rather than accepting
// events nothing can run.
func TestPublishingWithoutAFanoutIsRefused(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	recorder := server.call(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"type": "message.received",
	})
	if recorder.Code != http.StatusServiceUnavailable {
		t.Errorf("status = %d, want 503 (body: %s)", recorder.Code, recorder.Body)
	}
}

// The endpoint is idempotent on Idempotency-Key or, when no key was sent, on
// the body's eventId: a retry replays the first outcome instead of publishing
// a second time.
func TestPublishingIsIdempotentOnTheKeyOrTheEventId(t *testing.T) {
	newServer := func(t *testing.T) (*authenticatedAPI, map[string]string, *recordingPublisher) {
		t.Helper()
		publisher := &recordingPublisher{}
		server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
			wireHostEvents(t, deps, publisher)
		}, "acme")
		return server, keys, publisher
	}

	t.Run("on the header", func(t *testing.T) {
		server, keys, publisher := newServer(t)
		for attempt := 0; attempt < 2; attempt++ {
			recorder := callWithHeaders(t, server, keys["acme"],
				map[string]string{"Idempotency-Key": "publish-1"},
				map[string]any{"type": "message.received"})
			if recorder.Code != http.StatusAccepted {
				t.Fatalf("attempt %d: status = %d (body: %s)", attempt, recorder.Code, recorder.Body)
			}
			if attempt == 1 && recorder.Header().Get("Idempotent-Replayed") != "true" {
				t.Errorf("retry carries Idempotent-Replayed = %q, want true", recorder.Header().Get("Idempotent-Replayed"))
			}
		}
		if events := publisher.events(); len(events) != 1 {
			t.Errorf("the fan-out saw %d publishes, want 1", len(events))
		}
	})

	t.Run("on the eventId when no header was sent", func(t *testing.T) {
		server, keys, publisher := newServer(t)
		for attempt := 0; attempt < 2; attempt++ {
			recorder := server.call(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
				"eventId": "evt-42", "type": "message.received",
			})
			if recorder.Code != http.StatusAccepted {
				t.Fatalf("attempt %d: status = %d (body: %s)", attempt, recorder.Code, recorder.Body)
			}
			if attempt == 1 && recorder.Header().Get("Idempotent-Replayed") != "true" {
				t.Errorf("retry carries Idempotent-Replayed = %q, want true", recorder.Header().Get("Idempotent-Replayed"))
			}
		}
		if events := publisher.events(); len(events) != 1 {
			t.Errorf("the fan-out saw %d publishes, want 1", len(events))
		}
	})

	t.Run("a key with a different request is a conflict", func(t *testing.T) {
		server, keys, _ := newServer(t)
		callWithHeaders(t, server, keys["acme"],
			map[string]string{"Idempotency-Key": "publish-2"},
			map[string]any{"type": "message.received"})
		recorder := callWithHeaders(t, server, keys["acme"],
			map[string]string{"Idempotency-Key": "publish-2"},
			map[string]any{"type": "contact.tagged"})
		if recorder.Code != http.StatusConflict {
			t.Errorf("status = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
		}
	})
}

// callWithHeaders sends one POST with extra request headers beside the API
// key, for the headers a test needs that the call helper does not carry.
func callWithHeaders(t *testing.T, server *authenticatedAPI, key string, headers map[string]string, body any) *httptest.ResponseRecorder {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("encode request body: %v", err)
	}
	request := httptest.NewRequest(http.MethodPost, "/api/v1/events", bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Authorization", "Bearer "+key)
	for name, value := range headers {
		request.Header.Set(name, value)
	}
	recorder := httptest.NewRecorder()
	server.handler.ServeHTTP(recorder, request)
	return recorder
}

// A publish without a key and without an eventId is not idempotent — every
// request is a new event, which is the host's choice to make.
func TestPublishingWithoutAKeyOrEventIdIsNotIdempotent(t *testing.T) {
	publisher := &recordingPublisher{}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		wireHostEvents(t, deps, publisher)
	}, "acme")
	for range 2 {
		server.callJSON(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
			"type": "message.received",
		}, http.StatusAccepted)
	}
	if events := publisher.events(); len(events) != 2 {
		t.Errorf("the fan-out saw %d publishes, want 2", len(events))
	}
}

// The event's type is required: an event without one has nothing to match
// subscriptions against.
func TestPublishingAnEventWithoutATypeIsRefused(t *testing.T) {
	publisher := &recordingPublisher{}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		wireHostEvents(t, deps, publisher)
	}, "acme")
	recorder := server.call(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"payload": map[string]any{"contactId": "c_1"},
	})
	if recorder.Code != http.StatusUnprocessableEntity {
		t.Errorf("status = %d, want 422 (body: %s)", recorder.Code, recorder.Body)
	}
	if events := publisher.events(); len(events) != 0 {
		t.Errorf("the fan-out saw %+v, want nothing", events)
	}
}

// wireHostEvents installs a publisher and a real idempotency service behind
// the events endpoint, so a test's publishes behave the way a deployment's do.
func wireHostEvents(t *testing.T, deps *api.Deps, publisher *recordingPublisher) {
	t.Helper()
	service, err := idempotency.NewService(repository.NewIdempotencyStore(deps.DB.(*database.DB).DB), idempotency.Options{
		Retention: time.Hour,
		Log:       slog.New(slog.NewTextHandler(io.Discard, nil)),
	})
	if err != nil {
		t.Fatalf("idempotency.NewService() error = %v", err)
	}
	deps.HostEvents = publisher
	deps.Idempotency = service
}

// An event nobody listens for is accepted and matches nothing: a no-op, not
// an error, because a host publishes its domain whether or not a workflow is
// wired yet.
func TestPublishingAnEventNobodyListensForMatchesNothing(t *testing.T) {
	publisher := &recordingPublisher{}
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		wireHostEvents(t, deps, publisher)
	}, "acme")
	body := server.callJSON(t, http.MethodPost, "/api/v1/events", keys["acme"], map[string]any{
		"type": "booking.created",
	}, http.StatusAccepted)
	if matched, _ := body["matched"].([]any); len(matched) != 0 {
		t.Errorf("matched = %v, want an empty list", body["matched"])
	}
	if filtered, _ := body["filtered"].([]any); len(filtered) != 0 {
		t.Errorf("filtered = %v, want an empty list", body["filtered"])
	}
}
