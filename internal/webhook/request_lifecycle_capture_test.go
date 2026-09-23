package webhook_test

import (
	"context"
	"net/http"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// memoryState is one route's lifecycle state, kept in memory.
type memoryState struct {
	mu     sync.Mutex
	values map[string]string
	saves  int
	clears int
}

func (state *memoryState) Load(context.Context) (map[string]string, error) {
	state.mu.Lock()
	defer state.mu.Unlock()
	loaded := make(map[string]string, len(state.values))
	for key, value := range state.values {
		loaded[key] = value
	}
	return loaded, nil
}

func (state *memoryState) Save(_ context.Context, values map[string]string) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.saves++
	state.values = make(map[string]string, len(values))
	for key, value := range values {
		state.values[key] = value
	}
	return nil
}

func (state *memoryState) Clear(context.Context) error {
	state.mu.Lock()
	defer state.mu.Unlock()
	state.clears++
	state.values = nil
	return nil
}

func (state *memoryState) kept() map[string]string {
	state.mu.Lock()
	defer state.mu.Unlock()
	return state.values
}

// subscriptionLifecycle registers with a service that answers with a
// subscription of its own, and checks and removes that subscription by its id.
func subscriptionLifecycle(capture map[string]string) webhook.RequestLifecycle {
	return webhook.RequestLifecycle{
		Check: &webhook.RequestDescriptor{
			Method: http.MethodGet, URL: "{{ .baseUrl }}/api/subscriptions/{{ .Captured.id }}", CredentialType: "wahaApi",
		},
		Set: &webhook.RequestDescriptor{
			Method: http.MethodPost, URL: "{{ .baseUrl }}/api/subscriptions", CredentialType: "wahaApi",
			Body: `{"url": "{{ .PublicURL }}"}`, Capture: capture,
		},
		Remove: &webhook.RequestDescriptor{
			Method: http.MethodDelete, URL: "{{ .baseUrl }}/api/subscriptions/{{ .Captured.id }}", CredentialType: "wahaApi",
		},
	}
}

// TestRequestLifecycleKeepsWhatSetAnsweredForCheckAndRemove: a service that
// names the subscription it made — or generates the secret it will sign with —
// says so once, in the answer to the registration. That answer is the only
// place the id exists, and `check` and `remove` are addressed by it.
//
// The id here holds a slash, a question mark and an at sign. It came from a
// service rather than the pack, so it is data, and it stays inside its segment
// exactly as a node parameter does. A large number keeps its digits: decoded
// as a float it would come back rounded, and address another subscription.
func TestRequestLifecycleKeepsWhatSetAnsweredForCheckAndRemove(t *testing.T) {
	t.Parallel()

	stub, server := newDescriptorStub(t)
	stub.answerWith(`{"data": {"id": "sub/42?x@y", "secret": "whsec_1"}, "serial": 12345678901234567890}`)
	state := &memoryState{}
	lifecycleContext := descriptorContext(t, server.URL, nil)
	lifecycleContext.State = state
	lifecycle := subscriptionLifecycle(map[string]string{"id": "data.id", "secret": "data.secret", "serial": "serial"})

	if err := lifecycle.Create(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Create() error = %v", err)
	}
	want := map[string]string{"id": "sub/42?x@y", "secret": "whsec_1", "serial": "12345678901234567890"}
	if got := state.kept(); !reflect.DeepEqual(got, want) {
		t.Fatalf("kept = %#v, want %#v", got, want)
	}

	exists, err := lifecycle.CheckExists(context.Background(), lifecycleContext)
	if err != nil || !exists {
		t.Fatalf("CheckExists() = %v, %v, want the captured subscription found", exists, err)
	}
	if err := lifecycle.Delete(context.Background(), lifecycleContext); err != nil {
		t.Fatalf("Delete() error = %v", err)
	}

	calls := stub.recorded()
	if len(calls) != 3 {
		t.Fatalf("calls = %#v, want set, check and remove", calls)
	}
	for index, method := range []string{http.MethodGet, http.MethodDelete} {
		call := calls[index+1]
		if call.method != method || call.target != "/api/subscriptions/sub%2F42%3Fx@y" {
			t.Fatalf("call = %#v, want %s of the captured subscription, kept inside its segment", call, method)
		}
	}
	if got := state.kept(); len(got) != 0 {
		t.Fatalf("kept after remove = %#v, want nothing: the subscription it named is gone", got)
	}
}

// TestRequestLifecycleEscapesACapturedValueLikeAParameter: the families of
// data are an allowlist, so a family missing from it is written raw into the
// URL that carries the tenant's credential. A captured value is data a service
// sent, and lands exactly where a parameter with the same text would.
func TestRequestLifecycleEscapesACapturedValueLikeAParameter(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		url   string
		value string
		want  string
		fails string
	}{
		"in the path":  {url: "{{ .baseUrl }}/api/subscriptions/{{ .%s.x }}", value: "a/b?c@d", want: "/api/subscriptions/a%2Fb%3Fc@d"},
		"in the query": {url: "{{ .baseUrl }}/api/subscriptions?id={{ .%s.x }}", value: "a/b?c@d&e", want: "/api/subscriptions?id=a%2Fb%3Fc%40d%26e"},
		"a dot segment": {url: "{{ .baseUrl }}/api/subscriptions/{{ .%s.x }}", value: "..",
			fails: "another endpoint"},
		"where the host ends": {url: "{{ .baseUrl }}{{ .%s.x }}/api/subscriptions", value: "@evil.example",
			fails: "before its path"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			for _, family := range []string{"Parameter", "Captured"} {
				stub, server := newDescriptorStub(t)
				lifecycleContext := descriptorContext(t, server.URL, map[string]any{"x": testCase.value})
				lifecycleContext.State = &memoryState{values: map[string]string{"x": testCase.value}}
				lifecycle := webhook.RequestLifecycle{Check: &webhook.RequestDescriptor{
					Method: http.MethodGet, URL: strings.ReplaceAll(testCase.url, "%s", family), CredentialType: "wahaApi",
				}}

				_, err := lifecycle.CheckExists(context.Background(), lifecycleContext)
				if testCase.fails != "" {
					if err == nil || !strings.Contains(err.Error(), testCase.fails) {
						t.Fatalf("%s: CheckExists() error = %v, want a refusal mentioning %q", family, err, testCase.fails)
					}
					if calls := stub.recorded(); len(calls) != 0 {
						t.Fatalf("%s: calls = %#v, want nothing sent", family, calls)
					}
					continue
				}
				if err != nil {
					t.Fatalf("%s: CheckExists() error = %v", family, err)
				}
				if got := stub.onlyCall(t).target; got != testCase.want {
					t.Fatalf("%s: request target = %q, want %q", family, got, testCase.want)
				}
			}
		})
	}
}

// TestRequestLifecycleFailsARegistrationWhoseAnswerLacksACapture: a pack that
// captures an id is a pack whose `remove` needs it. Activating anyway would
// leave a registration nothing can ever remove, so activation fails, and it
// names the path the answer did not have. An empty id is no id: kept, it
// would address `remove` at the collection, `DELETE /api/subscriptions/`.
func TestRequestLifecycleFailsARegistrationWhoseAnswerLacksACapture(t *testing.T) {
	t.Parallel()

	for name, answer := range map[string]string{
		"missing":       `{"data": {"secret": "whsec_1"}}`,
		"empty":         `{"data": {"id": "", "secret": "whsec_1"}}`,
		"null":          `{"data": {"id": null, "secret": "whsec_1"}}`,
		"an object":     `{"data": {"id": {"value": 1}, "secret": "whsec_1"}}`,
		"not an object": `["sub-1"]`,
		"not JSON":      `sub-1`,
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newDescriptorStub(t)
			stub.answerWith(answer)
			state := &memoryState{}
			lifecycleContext := descriptorContext(t, server.URL, nil)
			lifecycleContext.State = state
			lifecycle := subscriptionLifecycle(map[string]string{"id": "data.id", "secret": "data.secret"})

			err := lifecycle.Create(context.Background(), lifecycleContext)
			if err == nil || !strings.Contains(err.Error(), "data.id") {
				t.Fatalf("Create() error = %v, want a failure naming data.id", err)
			}
			if state.saves != 0 {
				t.Fatalf("kept = %#v, want nothing kept from an answer that lacked a capture", state.kept())
			}
		})
	}
}

// TestRequestLifecycleSendsNothingThatNeedsAValueItNeverCaptured: after a
// remove, or before the first registration, there is no subscription id. A
// check addressed by it has nothing to find, so the trigger is not registered
// and `set` runs; a remove addressed by it has nothing to remove. Neither
// sends a request with the placeholder's text in its URL.
func TestRequestLifecycleSendsNothingThatNeedsAValueItNeverCaptured(t *testing.T) {
	t.Parallel()

	for name, state := range map[string]webhook.LifecycleStateStore{
		"nothing captured": &memoryState{},
		"no state at all":  nil,
		"another key only": &memoryState{values: map[string]string{"secret": "whsec_1"}},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newDescriptorStub(t)
			lifecycleContext := descriptorContext(t, server.URL, nil)
			lifecycleContext.State = state
			lifecycle := subscriptionLifecycle(nil)

			exists, err := lifecycle.CheckExists(context.Background(), lifecycleContext)
			if err != nil || exists {
				t.Fatalf("CheckExists() = %v, %v, want not registered", exists, err)
			}
			if err := lifecycle.Delete(context.Background(), lifecycleContext); err != nil {
				t.Fatalf("Delete() error = %v", err)
			}
			if calls := stub.recorded(); len(calls) != 0 {
				t.Fatalf("calls = %#v, want nothing sent", calls)
			}
		})
	}
}

// TestRequestLifecycleRefusesToCaptureWithNowhereToKeepIt: a registration
// whose id cannot be kept is one nothing can remove, so it is not made.
func TestRequestLifecycleRefusesToCaptureWithNowhereToKeepIt(t *testing.T) {
	t.Parallel()

	stub, server := newDescriptorStub(t)
	lifecycle := subscriptionLifecycle(map[string]string{"id": "data.id"})
	err := lifecycle.Create(context.Background(), descriptorContext(t, server.URL, nil))
	if err == nil || !strings.Contains(err.Error(), "keep") {
		t.Fatalf("Create() error = %v, want a refusal saying the values cannot be kept", err)
	}
	if calls := stub.recorded(); len(calls) != 0 {
		t.Fatalf("calls = %#v, want nothing sent", calls)
	}
}

// TestRequestLifecycleReadsANestedSuccessPath: a check reads its answer with
// the same dotted path a capture does. Telegram's getWebhookInfo answers with
// the URL under result.url, not at the top.
func TestRequestLifecycleReadsANestedSuccessPath(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		answer string
		want   bool
	}{
		"this route":    {answer: `{"ok": true, "result": {"url": "https://flows.example.test/webhook/abc123"}}`, want: true},
		"another route": {answer: `{"ok": true, "result": {"url": "https://elsewhere.example.test/hook"}}`, want: false},
		"no such path":  {answer: `{"ok": true, "result": {}}`, want: false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newDescriptorStub(t)
			stub.answerWith(testCase.answer)
			lifecycle := webhook.RequestLifecycle{Check: &webhook.RequestDescriptor{
				Method: http.MethodGet, URL: "{{ .baseUrl }}/getWebhookInfo", CredentialType: "wahaApi",
				SuccessJSONPath: "result.url",
			}}
			exists, err := lifecycle.CheckExists(context.Background(), descriptorContext(t, server.URL, nil))
			if err != nil || exists != testCase.want {
				t.Fatalf("CheckExists() = %v, %v, want %v", exists, err, testCase.want)
			}
		})
	}
}
