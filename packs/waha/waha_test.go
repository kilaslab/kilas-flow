package waha_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/sha512"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/interop/n8n/corpus"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
	"github.com/kilaslab/kilas-flow/packs/waha"
)

type registered struct {
	definitions *node.Registry
	routes      *routing.Registry
	executors   *engine.Registry
	options     *loadoptions.Resolver
	triggers    *nodepack.TriggerRegistry
	deliveries  *webhook.Registry
	lifecycles  *webhook.LifecycleRegistry
}

// actionPacks are the WAHA action node's versions; triggerPacks are the
// trigger's. Both node types ship from the same directory.
func actionPacks(t *testing.T) []*nodepack.Pack {
	t.Helper()
	return packsOfType(t, waha.NodeType)
}

func triggerPacks(t *testing.T) []*nodepack.Pack {
	t.Helper()
	return packsOfType(t, waha.TriggerNodeType)
}

func packsOfType(t *testing.T, nodeType string) []*nodepack.Pack {
	t.Helper()
	all, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
	matching := make([]*nodepack.Pack, 0, 2)
	for _, pack := range all {
		if pack.Type == nodeType {
			matching = append(matching, pack)
		}
	}
	if len(matching) == 0 {
		t.Fatalf("no %s pack", nodeType)
	}
	return matching
}

func install(t *testing.T, policy safehttp.Policy) registered {
	t.Helper()
	set := registered{
		definitions: node.NewRegistry(), routes: routing.NewRegistry(),
		executors: engine.NewRegistry(), options: loadoptions.NewResolver(policy, 0),
	}
	if err := set.executors.Register(routing.ExecutorID, routing.NewExecutor(policy, set.routes, set.definitions)); err != nil {
		t.Fatalf("Register(routing) error = %v", err)
	}
	set.triggers = nodepack.NewTriggerRegistry()
	set.deliveries = webhook.NewRegistry()
	set.lifecycles = webhook.NewLifecycleRegistry()
	if err := set.executors.Register(nodepack.TriggerExecutorID, nodepack.NewTriggerExecutor(set.triggers, policy)); err != nil {
		t.Fatalf("Register(trigger) error = %v", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: set.definitions, Routes: set.routes, Triggers: set.triggers,
		Deliveries: set.deliveries, Lifecycles: set.lifecycles,
		Executors: set.executors, Options: set.options,
	}); err != nil {
		t.Fatalf("waha.Register() error = %v", err)
	}
	return set
}

// The two published versions are not additive — operations move and parameters
// change between them — so each is generated from its own spec and the counts
// are read from those specs rather than pinned as numbers somebody typed.
func TestBothVersionsRegisterWithTheOperationCountOfTheirOwnSpec(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
	if len(packs) != 4 {
		t.Fatalf("Packs() = %d packs, want an action node and a trigger at both published versions", len(packs))
	}

	for _, pack := range actionPacks(t) {
		version := pack.Version.String()
		if pack.Type != waha.NodeType {
			t.Fatalf("pack v%s type = %q, want %q", version, pack.Type, waha.NodeType)
		}
		if _, found := set.definitions.Get(waha.NodeType, pack.Version); !found {
			t.Fatalf("v%s did not register", version)
		}

		spec := countSpecOperations(t, filepath.Join("..", "..", "third_party", "waha", "openapi-"+version+".json"))
		generated := 0
		for _, resource := range pack.Resources {
			generated += len(resource.Operations)
		}
		// The gap is named in REPORT-<version>.md, operation by operation.
		if generated > spec {
			t.Fatalf("v%s generated %d operations from a spec with %d", version, generated, spec)
		}
		if ratio := float64(generated) / float64(spec); ratio < 0.95 {
			t.Fatalf("v%s generated %d of %d operations (%.0f%%); the report has to explain a gap this size",
				version, generated, spec, ratio*100)
		}
		t.Logf("v%s: %d of %d operations, %d resources, %d parameters",
			version, generated, spec, len(pack.Resources), len(pack.Parameters))
	}
}

func countSpecOperations(t *testing.T, path string) int {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("read spec error = %v", err)
	}
	var document struct {
		Paths map[string]map[string]json.RawMessage `json:"paths"`
	}
	if err := json.Unmarshal(raw, &document); err != nil {
		t.Fatalf("parse spec error = %v", err)
	}
	count := 0
	for _, item := range document.Paths {
		for method := range item {
			switch method {
			case "get", "post", "put", "patch", "delete":
				count++
			}
		}
	}
	return count
}

// The whole native-first bet in one assertion: a customer's own workflow names
// a resource and an operation as literal strings, and the pack has to carry
// exactly those. Read from the fixtures, never from a hand-copied list — a list
// copied by hand is a list that agrees with itself.
func TestEveryResourceAndOperationInTheRealTemplatesExists(t *testing.T) {
	t.Parallel()

	loaded, err := corpus.Load()
	if err != nil {
		t.Skipf("the corpus is not present; materialise it with %s", corpus.SyncCommand)
	}
	fixtures := make([]corpus.Fixture, 0, len(loaded))
	for _, fixture := range loaded {
		if fixture.Source == corpus.SourceWAHATemplates {
			fixtures = append(fixtures, fixture)
		}
	}
	if len(fixtures) == 0 {
		t.Skipf("no WAHA templates in the corpus; materialise it with %s", corpus.SyncCommand)
	}

	available := map[string]map[string]bool{}
	for _, pack := range actionPacks(t) {
		for _, resource := range pack.Resources {
			if available[resource.Name] == nil {
				available[resource.Name] = map[string]bool{}
			}
			for _, operation := range resource.Operations {
				available[resource.Name][operation.Name] = true
			}
		}
	}

	seen := 0
	for _, fixture := range fixtures {
		var document struct {
			Nodes []struct {
				Type       string `json:"type"`
				Parameters struct {
					Resource  string `json:"resource"`
					Operation string `json:"operation"`
				} `json:"parameters"`
			} `json:"nodes"`
		}
		if err := json.Unmarshal(fixture.Payload, &document); err != nil {
			continue
		}
		for _, declared := range document.Nodes {
			if !strings.Contains(strings.ToLower(declared.Type), "waha") {
				continue
			}
			resource, operation := declared.Parameters.Resource, declared.Parameters.Operation
			if resource == "" || operation == "" {
				continue // a trigger, which carries neither
			}
			seen++
			if !available[resource][operation] {
				t.Errorf("%s uses resource %q operation %q, which no generated pack offers",
					fixture.Name, resource, operation)
			}
		}
	}
	if seen == 0 {
		t.Skip("no WAHA action node in the corpus to check against")
	}
	t.Logf("checked %d WAHA nodes across %d templates", seen, len(fixtures))
}

// Official templates omit `session` and `chatId` entirely and rely on defaults
// the OpenAPI document does not contain. A pack that treated absent as empty
// would produce workflows that look correct, activate, and send nothing.
func TestTheInjectedDefaultsSurviveGeneration(t *testing.T) {
	t.Parallel()

	want := map[string]string{
		"session": "{{ $json.session }}",
		"chatId":  "{{ $json.payload.from }}",
	}
	for _, pack := range actionPacks(t) {
		found := map[string]bool{}
		for _, parameter := range pack.Parameters {
			expected, tracked := want[parameter.Key]
			if !tracked {
				continue
			}
			found[parameter.Key] = true
			marker, ok := parameter.Default.(map[string]any)
			if !ok {
				t.Fatalf("v%s %s default = %#v, want KilasFlow's explicit expression marker",
					pack.Version, parameter.Key, parameter.Default)
			}
			if marker["mode"] != "expression" || marker["value"] != expected {
				t.Fatalf("v%s %s default = %#v, want %q as an expression", pack.Version, parameter.Key, marker, expected)
			}
		}
		for key := range want {
			if !found[key] {
				t.Fatalf("v%s has no %q parameter at all", pack.Version, key)
			}
		}
	}
}

// The end of the chain: a WAHA credential, a generated operation, and a request
// that arrives with the key in the right header and the session resolved from
// the trigger item.
func TestSendTextRunsAgainstAStubWAHAServer(t *testing.T) {
	t.Parallel()

	var apiKey, path string
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey, path = r.Header.Get("X-Api-Key"), r.URL.Path
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"true_1@c.us_AAA","_data":{"ack":1}}`))
	}))
	defer server.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	set := install(t, policy)

	// A WAHA envelope, exactly as the trigger delivers it.
	output, err := sendText(t, set, server.URL, nil, item(t))
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if apiKey != "k-waha" {
		t.Fatalf("X-Api-Key = %q, want the credential applied as the WAHA header", apiKey)
	}
	if path != "/api/sendText" {
		t.Fatalf("path = %q, want the generated operation's URL under the credential's base URL", path)
	}
	// The defaults resolved from the trigger item rather than being empty.
	if sent["session"] != "default" {
		t.Fatalf("session = %#v, want the session from the trigger item", sent["session"])
	}
	if sent["chatId"] != "1@c.us" {
		t.Fatalf("chatId = %#v, want the sender from the trigger item", sent["chatId"])
	}
	if sent["text"] != "hi back" {
		t.Fatalf("text = %#v, want the configured text", sent["text"])
	}
	if output[0][0].JSON["id"] != "true_1@c.us_AAA" {
		t.Fatalf("item = %#v, want the WAHA response", output[0][0].JSON)
	}

	// What a recorded execution would hold. The key is applied as a header by
	// the credential path and never enters an item, so it cannot reach the
	// execution record through redaction's blind spots either.
	recorded, err := json.Marshal(map[string]any{
		"input":  execution.RedactMap(item(t)),
		"output": execution.RedactMap(output[0][0].JSON),
	})
	if err != nil {
		t.Fatalf("marshal error = %v", err)
	}
	if strings.Contains(string(recorded), "k-waha") {
		t.Fatalf("the recorded execution carries the API key: %s", recorded)
	}
	if !strings.Contains(string(recorded), "true_1@c.us_AAA") {
		t.Fatalf("the recorded execution lost the response: %s", recorded)
	}
}

// item is the trigger envelope the send-text test sends, so the recorded-shape
// assertion above reads the same data the request did.
func item(t *testing.T) map[string]any {
	t.Helper()
	return map[string]any{
		"session": "default",
		"payload": map[string]any{"from": "1@c.us", "body": "hello"},
	}
}

// `session` was on the redaction package's sensitive-key list, and redaction
// runs at webhook ingest and on every executions write with the runner
// rehydrating the trigger item from the redacted record. Every WAHA call would
// have gone to a session named `[redacted]`. This is the test that keeps it
// fixed.
func TestTheSessionSurvivesRedactionOnTheWayToTheRequest(t *testing.T) {
	t.Parallel()

	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"x"}`))
	}))
	defer server.Close()

	// The trigger item as it would be after a round trip through the execution
	// record, which is where the value used to be destroyed.
	envelope := map[string]any{
		"session": "sales-team",
		"payload": map[string]any{"from": "1@c.us"},
		// A real credential-shaped key alongside it, to prove redaction is
		// still doing its job rather than having been switched off.
		"apiKey": "must-not-survive",
	}
	rehydrated := execution.RedactMap(envelope)
	if rehydrated["apiKey"] != execution.RedactedValue {
		t.Fatalf("apiKey = %#v, want redaction still active", rehydrated["apiKey"])
	}
	if rehydrated["session"] != "sales-team" {
		t.Fatalf("session = %#v, want the WAHA session to survive redaction", rehydrated["session"])
	}

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	set := install(t, policy)
	if _, err := sendText(t, set, server.URL, nil, rehydrated); err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if sent["session"] != "sales-team" {
		t.Fatalf("session reached WAHA as %#v, want the real session name", sent["session"])
	}
}

// A credential's host scope is enforced on the resolved host exactly as it is
// for the HTTP Request node, because it is the same code.
func TestTheCredentialsAllowedDomainsAreEnforced(t *testing.T) {
	t.Parallel()

	contacted := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		contacted = true
		_, _ = w.Write([]byte(`{}`))
	}))
	defer server.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	set := install(t, policy)

	_, err := sendText(t, set, server.URL, []string{"waha.elsewhere.test"},
		map[string]any{"session": "default", "payload": map[string]any{"from": "1@c.us"}})
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Fatalf("Execute() error = %v, want the credential's host scope enforced", err)
	}
	if contacted {
		t.Fatal("the request was sent before the credential scope was checked")
	}
}

// The picker narrows to the chosen resource. Without this a pack holding one
// `operation` property offers every operation it has, and a user builds a node
// that fails at run time with "no request is declared for this pair".
func TestTheOperationPickerNarrowsToTheChosenResource(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := actionPacks(t)
	latest := packs[len(packs)-1]
	loader := operationLoader(t, set.definitions, latest.Version)

	if len(loader.DependsOn) != 1 || loader.DependsOn[0] != "resource" {
		t.Fatalf("dependsOn = %v, want the resource, so the list is discarded when it changes", loader.DependsOn)
	}
	if loader.Source != property.LoaderInternal {
		t.Fatalf("loader source = %q, want an internal lookup; a pack's own operations are not an outbound call", loader.Source)
	}

	result, err := set.options.Load(context.Background(), *loader,
		loadoptions.Scope{TenantID: "default", Dependencies: map[string]string{"resource": "Chatting"}}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	names := map[string]bool{}
	for _, option := range result.Options {
		names[option.Value] = true
	}
	if !names["Send Text"] {
		t.Fatalf("Chatting offers %d operations and not Send Text", len(result.Options))
	}
	if names["Get QR"] {
		t.Fatal("Chatting offers Get QR, which belongs to Auth")
	}

	// An unset resource says why rather than offering everything.
	empty, err := set.options.Load(context.Background(), *loader,
		loadoptions.Scope{TenantID: "default"}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(empty.Options) != 0 || empty.Reason == "" {
		t.Fatalf("with no resource: %d options, reason %q; want none and a reason", len(empty.Options), empty.Reason)
	}

	// And the two versions do not answer each other's questions.
	if operationLoader(t, set.definitions, packs[0].Version).Name == loader.Name {
		t.Fatal("both versions share one loader name, so one would answer for the other")
	}
}

func operationLoader(t *testing.T, definitions *node.Registry, version workflow.TypeVersion) *property.OptionsLoader {
	t.Helper()
	definition, found := definitions.Get(waha.NodeType, version)
	if !found {
		t.Fatalf("v%s did not register", version)
	}
	for _, parameter := range definition.Parameters {
		if parameter.Key != "operation" {
			continue
		}
		if parameter.LoadOptions == nil {
			t.Fatal("the operation property declares no loader, so the picker cannot narrow")
		}
		return parameter.LoadOptions
	}
	t.Fatal("the definition has no operation property")
	return nil
}

// sendText runs the pack's Chatting → Send Text operation against a stub.
func sendText(t *testing.T, set registered, baseURL string, domains []string, item map[string]any) (workflow.NodeOutput, error) {
	t.Helper()
	packs := actionPacks(t)
	latest := packs[len(packs)-1]
	definition, found := set.definitions.Lookup(waha.NodeType, latest.Version)
	if !found {
		t.Fatal("the pack did not register")
	}
	executor, _ := set.executors.Lookup(routing.ExecutorID)
	return executor.Execute(context.Background(), workflow.IRNode{
		ID: "waha-1", Name: "WAHA", Type: waha.NodeType, TypeVersion: latest.Version,
		Parameters: map[string]any{
			"resource": "Chatting", "operation": "Send Text", "text": "hi back",
		},
		Credentials: map[string]string{waha.CredentialType: "cred-1"},
		Definition:  definition,
	}, workflow.NodeInput{"main": {{JSON: item}}}, engine.Request{
		Credentials: stubCredential{baseURL: baseURL, domains: domains},
	})
}

type stubCredential struct {
	baseURL string
	domains []string
}

func (stub stubCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "WAHA", Type: "wahaApi",
		Fields:         map[string]string{"baseUrl": stub.baseURL, "apiKey": "k-waha"},
		AllowedDomains: stub.domains,
	}, nil
}

// The API key is a secret field, so it is never returned after storage and
// never reaches a recorded execution.
func TestTheAPIKeyIsSecretAndTheBaseURLIsNot(t *testing.T) {
	t.Parallel()

	definition, found := credentials.Lookup(waha.CredentialType)
	if !found {
		t.Fatalf("credential type %q is not registered", waha.CredentialType)
	}
	secret := map[string]bool{}
	for _, field := range definition.Fields {
		secret[field.Key] = field.Secret
	}
	if !secret["apiKey"] {
		t.Fatal("apiKey is not secret; it would be returned by the API after storage")
	}
	// Not a slip: the pack builds every URL from `{{ $credentials.baseUrl }}`,
	// and `$credentials` carries non-secret fields only.
	if secret["baseUrl"] {
		t.Fatal("baseUrl is secret, so the pack would have no address to call")
	}

	redacted := credentials.Redacted(waha.CredentialType, map[string]string{"baseUrl": "https://waha.test", "apiKey": "k-waha"})
	if redacted["apiKey"] == "k-waha" {
		t.Fatalf("Redacted() returned the key: %v", redacted)
	}
	if redacted["baseUrl"] != "https://waha.test" {
		t.Fatalf("Redacted() hid the base URL: %v", redacted)
	}
}

// A committed pack has to have come from the spec that is committed beside it.
// Determinism is proven in the generator's own tests; this is the other half —
// that these bytes were generated from these bytes.
func TestEachPackRecordsTheDigestOfTheSpecItCameFrom(t *testing.T) {
	t.Parallel()

	for _, pack := range actionPacks(t) {
		version := pack.Version.String()
		raw, err := os.ReadFile(filepath.Join("..", "..", "third_party", "waha", "openapi-"+version+".json"))
		if err != nil {
			t.Fatalf("read spec error = %v", err)
		}
		digest := sha256.Sum256(raw)
		if got := hex.EncodeToString(digest[:]); got != pack.Generator.SourceDigest {
			t.Fatalf("v%s was generated from a different document (%s, now %s); run `make node-packs`",
				version, pack.Generator.SourceDigest, got)
		}
		if !strings.Contains(pack.Generator.Source, "openapi-"+version) {
			t.Fatalf("v%s names source %q", version, pack.Generator.Source)
		}
	}
}

// A connection in an imported workflow is an output *index*, so the order of
// the ports is the contract. It comes from the document and is never sorted.
func TestTheTriggerPortOrderIsTheDocumentsEventOrder(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	for _, pack := range triggerPacks(t) {
		version := pack.Version.String()
		definition, found := set.definitions.Get(waha.TriggerNodeType, pack.Version)
		if !found {
			t.Fatalf("trigger v%s did not register", version)
		}
		if len(definition.Inputs) != 0 {
			t.Fatalf("trigger v%s has %d inputs, want none", version, len(definition.Inputs))
		}
		// One per event, plus the catch-all.
		if len(definition.Outputs) != len(pack.Trigger.Events)+1 {
			t.Fatalf("trigger v%s has %d outputs for %d events", version, len(definition.Outputs), len(pack.Trigger.Events))
		}
		for index, event := range pack.Trigger.Events {
			if definition.Outputs[index].Name != event {
				t.Fatalf("trigger v%s output %d = %q, want %q", version, index, definition.Outputs[index].Name, event)
			}
		}
		if last := definition.Outputs[len(definition.Outputs)-1].Name; last != pack.Trigger.CatchAll {
			t.Fatalf("trigger v%s last output = %q, want the catch-all", version, last)
		}
		t.Logf("trigger v%s: %d events + catch-all", version, len(pack.Trigger.Events))
	}

	// The two orderings diverge, which is exactly why the table is a property
	// of the version rather than of the node.
	packs := triggerPacks(t)
	older, newer := packs[0].Trigger.Events, packs[1].Trigger.Events
	diverged := false
	for index := range older {
		if index < len(newer) && older[index] != newer[index] {
			diverged = true
			t.Logf("orders diverge at index %d: %q then %q", index, older[index], newer[index])
			break
		}
	}
	if !diverged {
		t.Fatal("the two versions' event orders are identical; one of them was not generated from its own document")
	}
}

func triggerNode(t *testing.T, set registered, version workflow.TypeVersion, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, found := set.definitions.Lookup(waha.TriggerNodeType, version)
	if !found {
		t.Fatal("the trigger did not register")
	}
	return workflow.IRNode{
		ID: "waha-trigger", Name: "WAHA Trigger", Type: waha.TriggerNodeType, TypeVersion: version,
		Parameters: parameters, Credentials: map[string]string{waha.CredentialType: "cred-1"},
		Definition: definition,
	}
}

// One delivery, one branch. Every other port is empty, which is what makes the
// runner prune the twenty-five branches that were not taken.
func TestADeliveryReachesOnlyItsOwnEventsPort(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]
	executor, _ := set.executors.Lookup(nodepack.TriggerExecutorID)

	for _, event := range []string{"message", "message.ack", "session.status"} {
		output, err := executor.Execute(context.Background(),
			triggerNode(t, set, latest.Version, map[string]any{"path": "waha"}),
			workflow.NodeInput{},
			engine.Request{Input: workflow.Item{JSON: map[string]any{"event": event, "session": "default"}}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		want := latest.Trigger.PortIndex(event)
		for index, items := range output {
			if index == want {
				if len(items) != 1 || items[0].JSON["event"] != event {
					t.Fatalf("%s: port %d carried %#v, want the delivery", event, index, items)
				}
				continue
			}
			if len(items) != 0 {
				t.Fatalf("%s: port %d carried %d items, want none — every other branch must be pruned", event, index, len(items))
			}
		}
	}
}

// An event this build has never heard of is a new event in a newer service.
// Dropping it silently is how a workflow stops working after somebody else's
// upgrade.
func TestAnUnknownEventGoesToTheCatchAllRatherThanNowhere(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]
	executor, _ := set.executors.Lookup(nodepack.TriggerExecutorID)

	output, err := executor.Execute(context.Background(),
		triggerNode(t, set, latest.Version, map[string]any{"path": "waha"}),
		workflow.NodeInput{},
		engine.Request{Input: workflow.Item{JSON: map[string]any{"event": "invented.in.2027"}}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	catchAll := len(output) - 1
	if len(output[catchAll]) != 1 {
		t.Fatalf("the catch-all carried %d items, want the unrecognised delivery", len(output[catchAll]))
	}
	for index := range output[:catchAll] {
		if len(output[index]) != 0 {
			t.Fatalf("port %d also carried items", index)
		}
	}
}

// WAHA templates read `$json.event`, `$json.session` and `$json.payload` at the
// top level, not `$json.body.event`.
func TestTheDeliveryIsShapedAsTheEnvelopeTemplatesRead(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	kind := set.deliveries.Lookup(waha.TriggerNodeType)
	if kind.Shape != webhook.ShapeBodyAsItem {
		t.Fatalf("shape = %q, want the body at the top level", kind.Shape)
	}

	body := map[string]any{"event": "message", "session": "default", "payload": map[string]any{"from": "1@c.us"}}
	item := kind.Shape.Apply(webhook.Delivery{Body: body})
	for key, want := range map[string]any{"event": "message", "session": "default"} {
		if item[key] != want {
			t.Fatalf("item[%q] = %#v, want %#v at the top level", key, item[key], want)
		}
	}
	if _, wrapped := item["body"]; wrapped {
		t.Fatalf("item = %#v, want no wrapper path", item)
	}
}

// A signature is the only thing between a leaked URL and injected WhatsApp
// events — but a node with no secret configured must still receive, or every
// imported workflow would break on arrival.
func TestTheSignatureIsCheckedOnlyWhenASecretIsConfigured(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	kind := set.deliveries.Lookup(waha.TriggerNodeType)
	if kind.Verify == nil {
		t.Fatal("the trigger installs no verifier")
	}

	body := []byte(`{"event":"message"}`)
	mac := hmac.New(sha512.New, []byte("s3cret"))
	mac.Write(body)
	signature := hex.EncodeToString(mac.Sum(nil))

	delivery := func(secret, header string) webhook.Delivery {
		request := httptest.NewRequest(http.MethodPost, "/webhook/abc", nil)
		if header != "" {
			request.Header.Set("X-Webhook-Hmac", header)
		}
		parameters := map[string]any{}
		if secret != "" {
			parameters["hmacSecret"] = secret
		}
		return webhook.Delivery{
			Request: request, RawBody: body,
			Binding: repository.WebhookBinding{Parameters: parameters},
		}
	}

	if err := kind.Verify(delivery("", "")); err != nil {
		t.Fatalf("an unconfigured node refused a delivery: %v", err)
	}
	if err := kind.Verify(delivery("s3cret", signature)); err != nil {
		t.Fatalf("a correctly signed delivery was refused: %v", err)
	}
	if err := kind.Verify(delivery("s3cret", "")); err == nil {
		t.Fatal("a configured node accepted an unsigned delivery")
	}
	if err := kind.Verify(delivery("s3cret", strings.Repeat("0", len(signature)))); err == nil {
		t.Fatal("a configured node accepted a wrong signature")
	}
}

// WAHA retries, and a retried delivery must not run the workflow twice.
func TestTheTriggerDeclaresTheHeaderWAHARetriesWith(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	for _, pack := range triggerPacks(t) {
		definition, _ := set.definitions.Get(waha.TriggerNodeType, pack.Version)
		found := false
		for _, parameter := range definition.Parameters {
			if parameter.Key != "deliveryIdHeader" {
				continue
			}
			found = true
			if parameter.Default != "X-Webhook-Request-Id" {
				t.Fatalf("deliveryIdHeader default = %#v, want WAHA's own retry identifier", parameter.Default)
			}
		}
		if !found {
			t.Fatalf("trigger v%s declares no deliveryIdHeader, so a retry runs the workflow again", pack.Version)
		}
	}
}

// The executor emitting on one port is only half of it. A node cannot stop a
// downstream node from running, so this wires two events to two branches,
// delivers one, and asserts the other branch produced no node run at all.
func TestOnlyTheBranchWiredToTheDeliveredEventRuns(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	if err := nodes.RegisterAll(set.definitions); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if err := nodes.RegisterExecutors(set.executors, safehttp.DefaultPolicy(), sqlnode.Guard{}, ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]

	ir, err := workflow.Compile(workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_waha", Name: "Reply to messages",
		Nodes: []workflow.Node{
			{ID: "trigger", Name: "WAHA Trigger", Type: waha.TriggerNodeType, TypeVersion: latest.Version,
				Parameters: map[string]any{"path": "waha", "session": "default"},
				// The pack declares the credential required, and the compiler
				// now enforces that.
				Credentials: map[string]string{waha.CredentialType: "cred-1"}},
			{ID: "on-message", Name: "On message", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"branch": "message"}}},
			{ID: "on-ack", Name: "On ack", Type: "kilasflow.set", TypeVersion: workflow.V(1),
				Parameters: map[string]any{"assignments": map[string]any{"branch": "ack"}}},
		},
		Connections: []workflow.Connection{
			{ID: "c1", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "message"},
				Target: workflow.Endpoint{NodeID: "on-message", Port: "main"}},
			{ID: "c2", Kind: workflow.ConnectionMain,
				Source: workflow.Endpoint{NodeID: "trigger", Port: "message.ack"},
				Target: workflow.Endpoint{NodeID: "on-ack", Port: "main"}},
		},
		Settings: map[string]any{},
	}, set.definitions)
	if err != nil {
		t.Fatalf("Compile() error = %v", err)
	}

	result, err := engine.NewRunner(set.executors).Run(context.Background(), ir, engine.Request{
		TriggerNodeID: "trigger",
		Input:         workflow.Item{JSON: map[string]any{"event": "message", "session": "default"}},
	})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	// A pruned branch is recorded as skipped rather than omitted — the
	// inspector has to be able to tell "did not run" from "never reached" —
	// so the assertion is about which of them actually executed.
	executed := map[string]bool{}
	skipped := map[string]bool{}
	for _, run := range result.NodeRuns {
		if run.Skipped {
			skipped[run.NodeID] = true
			continue
		}
		executed[run.NodeID] = true
	}
	if !executed["on-message"] {
		t.Fatalf("the message branch did not run; executed = %v, skipped = %v", executed, skipped)
	}
	if executed["on-ack"] {
		t.Fatal("the ack branch ran on a message delivery; twenty-five other branches would too")
	}
	if !skipped["on-ack"] {
		t.Fatalf("the ack branch was neither run nor recorded as skipped: %v", skipped)
	}
	if output := result.NodeRuns[0].Output; len(output) != len(latest.Trigger.Events)+1 {
		t.Fatalf("the trigger produced %d ports, want one per event plus the catch-all", len(output))
	}
}

// A webhook that links to media is a webhook whose payload expires. Downloading
// it once, at the trigger, is what makes the media part of the run.
func TestAMediaBearingDeliveryAttachesAReferenceRatherThanBytes(t *testing.T) {
	t.Parallel()

	payload := []byte{0xff, 0xd8, 0xff, 0xe0, 'J', 'F', 'I', 'F'}
	var apiKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		apiKey = r.Header.Get("X-Api-Key")
		w.Header().Set("Content-Type", "image/jpeg")
		_, _ = w.Write(payload)
	}))
	defer server.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	set := install(t, policy)
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]
	executor, _ := set.executors.Lookup(nodepack.TriggerExecutorID)

	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	scoped := binary.For(store, "tenant-a", "exec-1")

	delivery := map[string]any{
		"event": "message", "session": "default",
		"payload": map[string]any{
			"from": "1@c.us",
			"media": map[string]any{
				"url": server.URL + "/api/files/photo.jpg", "mimetype": "image/jpeg", "filename": "photo.jpg",
			},
		},
	}
	request := engine.Request{
		Input:       workflow.Item{JSON: delivery},
		Binaries:    scoped,
		Credentials: stubCredential{baseURL: server.URL},
	}

	// Off by default: a busy session downloads a lot.
	output, err := executor.Execute(context.Background(),
		triggerNode(t, set, latest.Version, map[string]any{"path": "waha"}), workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[latest.Trigger.PortIndex("message")][0].Binary != nil {
		t.Fatal("media was downloaded without being asked for")
	}

	output, err = executor.Execute(context.Background(),
		triggerNode(t, set, latest.Version, map[string]any{"path": "waha", "downloadMedia": true}),
		workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() with downloadMedia error = %v", err)
	}
	item := output[latest.Trigger.PortIndex("message")][0]
	reference, attached := item.Binary["data"]
	if !attached {
		t.Fatalf("Binary = %#v, want the media attached", item.Binary)
	}
	if reference.FileName != "photo.jpg" || reference.MediaType != "image/jpeg" {
		t.Fatalf("reference = %#v, want the name and type from the delivery", reference)
	}
	if reference.Size != int64(len(payload)) {
		t.Fatalf("Size = %d, want %d", reference.Size, len(payload))
	}
	if apiKey != "k-waha" {
		t.Fatalf("X-Api-Key = %q, want the credential applied to the media fetch", apiKey)
	}

	// The reference, never the bytes.
	encoded, _ := json.Marshal(item.JSON)
	if bytes.Contains(encoded, payload) {
		t.Fatalf("the item carries the payload: %s", encoded)
	}
	body, _, err := scoped.Get(reference.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	defer body.Close()
	stored, _ := io.ReadAll(body)
	if !bytes.Equal(stored, payload) {
		t.Fatalf("stored = %#v, want the downloaded bytes", stored)
	}
}

// A webhook URL that names an internal address would turn this trigger into an
// SSRF gadget, and the URL comes from whoever is delivering.
func TestMediaFromAnInternalAddressIsRefused(t *testing.T) {
	t.Parallel()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte("secret"))
	}))
	defer server.Close()

	// The default policy refuses private networks; the test server is loopback.
	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]
	executor, _ := set.executors.Lookup(nodepack.TriggerExecutorID)

	store, err := binary.NewFileStore(t.TempDir(), 1<<20)
	if err != nil {
		t.Fatalf("NewFileStore() error = %v", err)
	}
	_, err = executor.Execute(context.Background(),
		triggerNode(t, set, latest.Version, map[string]any{"path": "waha", "downloadMedia": true}),
		workflow.NodeInput{}, engine.Request{
			Input: workflow.Item{JSON: map[string]any{
				"event":   "message",
				"payload": map[string]any{"media": map[string]any{"url": server.URL + "/secret"}},
			}},
			Binaries:    binary.For(store, "tenant-a", "exec-1"),
			Credentials: stubCredential{baseURL: server.URL},
		})
	if err == nil || !strings.Contains(err.Error(), "request target is not allowed") {
		t.Fatalf("Execute() error = %v, want the egress policy's refusal", err)
	}
}

// stubRoutes is the binding reader the coordinator walks.
type stubRoutes struct{ bindings []repository.WebhookBinding }

func (stub stubRoutes) WebhookRoutes(context.Context, repository.TenantScope, string) ([]repository.WebhookBinding, error) {
	return stub.bindings, nil
}

func coordinator(t *testing.T, set registered, binding repository.WebhookBinding, credential engine.CredentialResolver) *webhook.Coordinator {
	t.Helper()
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return webhook.NewCoordinator(set.lifecycles, stubRoutes{bindings: []repository.WebhookBinding{binding}},
		policy, func(string) engine.CredentialResolver { return credential },
		"https://flows.example.test", nil)
}

// An imported workflow with auto-registration off is active and receives
// nothing until somebody pastes the URL into WAHA. An activation that only said
// "active" would leave that looking exactly like a workflow that is listening.
func TestActivationSaysWhatItDidNotDo(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]

	binding := repository.WebhookBinding{
		NodeID: "trigger", NodeType: waha.TriggerNodeType, Route: "abc123",
		Parameters: map[string]any{"session": "default", "autoRegister": false},
	}
	declared := map[string]string{waha.TriggerNodeType: latest.Trigger.Lifecycle.ID}

	notices, err := coordinator(t, set, binding, nil).Activated(context.Background(), "tenant-a", "wf_1", declared)
	if err != nil {
		t.Fatalf("Activated() error = %v", err)
	}
	if len(notices) != 1 {
		t.Fatalf("notices = %#v, want one naming the URL to paste", notices)
	}
	if !strings.Contains(notices[0].Message, "https://flows.example.test/webhook/abc123") {
		t.Fatalf("notice = %q, want the exact URL", notices[0].Message)
	}
	if notices[0].NodeID != "trigger" {
		t.Fatalf("notice node = %q, want the trigger it is about", notices[0].NodeID)
	}
}

// TestTheRegisteredTriggerDeclaresABoundLifecycle is composition's own boot
// check, run against the real pack.
//
// It is here because the merge replaces the trigger's descriptor block: the
// node definition carries the lifecycle ID that `nodepack.Register` read before
// the block was cleared, and clearing it a moment too early would leave a
// trigger that saves, activates, and silently never registers with WAHA.
func TestTheRegisteredTriggerDeclaresABoundLifecycle(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	for _, pack := range packs {
		declared := pack.Trigger.Lifecycle.ID
		if declared == "" {
			t.Fatalf("the %s pack declares no lifecycle", pack.Version)
		}
		bound := set.definitions.LifecycleIDs()
		if len(bound) != 1 || bound[0] != declared {
			t.Fatalf("registered definitions declare %#v, want the pack's %q", bound, declared)
		}
		if err := webhook.VerifyLifecycleBindings(bound, set.lifecycles); err != nil {
			t.Fatalf("VerifyLifecycleBindings() error = %v", err)
		}
	}
}

// sessionCall is one request a stub session saw.
type sessionCall struct {
	method string
	path   string
	apiKey string
}

// sessionStub is a WAHA session: a document of its own that holds a webhook
// list beside settings this pack has no business touching, and the requests it
// received.
//
// Stateful on purpose. The bug this covers is only visible in what the session
// is left holding, not in what one request said.
type sessionStub struct {
	mu       sync.Mutex
	document string
	calls    []sessionCall
}

func newSessionStub(t *testing.T, document string) (*sessionStub, *httptest.Server) {
	t.Helper()
	stub := &sessionStub{document: document}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		stub.mu.Lock()
		stub.calls = append(stub.calls, sessionCall{method: r.Method, path: r.URL.Path, apiKey: r.Header.Get("X-Api-Key")})
		if r.Method == http.MethodPut {
			stub.document = string(raw)
		}
		document := stub.document
		stub.mu.Unlock()

		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, document)
	}))
	t.Cleanup(server.Close)
	return stub, server
}

func (stub *sessionStub) recorded() []sessionCall {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]sessionCall(nil), stub.calls...)
}

// writes counts the writes of the session document. WAHA stops and restarts a
// session when its configuration is PUT, so the number of writes is part of
// what activation is allowed to cost.
func (stub *sessionStub) writes() int {
	writes := 0
	for _, call := range stub.recorded() {
		if call.method == http.MethodPut {
			writes++
		}
	}
	return writes
}

func (stub *sessionStub) decode(t *testing.T) map[string]any {
	t.Helper()
	stub.mu.Lock()
	defer stub.mu.Unlock()
	document := map[string]any{}
	if err := json.Unmarshal([]byte(stub.document), &document); err != nil {
		t.Fatalf("session document %q error = %v", stub.document, err)
	}
	return document
}

// webhooks is the session's own webhook list.
func (stub *sessionStub) webhooks(t *testing.T) []map[string]any {
	t.Helper()
	config, _ := stub.decode(t)["config"].(map[string]any)
	raw, _ := config["webhooks"].([]any)
	entries := make([]map[string]any, 0, len(raw))
	for _, entry := range raw {
		if object, isObject := entry.(map[string]any); isObject {
			entries = append(entries, object)
		}
	}
	return entries
}

// entryFor is the session's entry for a route, or nil.
func (stub *sessionStub) entryFor(t *testing.T, route string) map[string]any {
	t.Helper()
	for _, entry := range stub.webhooks(t) {
		if url, _ := entry["url"].(string); strings.Contains(url, route) {
			return entry
		}
	}
	return nil
}

// settings is everything in the session document except its webhook list —
// which is exactly what a rewrite destroys.
func (stub *sessionStub) settings(t *testing.T) map[string]any {
	t.Helper()
	document := stub.decode(t)
	if config, isObject := document["config"].(map[string]any); isObject {
		delete(config, "webhooks")
	}
	return document
}

// configuredSession is a session somebody else set up: another workflow's
// webhook, and the proxy, engine and own fields WAHA's PUT replaces.
const configuredSession = `{
  "name": "sales",
  "status": "WORKING",
  "engine": {"type": "NOWEB"},
  "proxy": {"server": "http://proxy.example.test:3128", "enabled": true},
  "config": {"webhooks": [{"url": "https://someone-else.example.test/hook", "events": ["message"]}], "debug": true}
}`

// bindingFor is one trigger node's binding, as activation resolves it.
func bindingFor(route string, autoRegister bool, extra map[string]any) repository.WebhookBinding {
	parameters := map[string]any{
		"session": "sales", "autoRegister": autoRegister,
		"$credentials": map[string]any{waha.CredentialType: "cred-1"},
	}
	for key, value := range extra {
		parameters[key] = value
	}
	return repository.WebhookBinding{
		NodeID: "trigger", NodeType: waha.TriggerNodeType, Route: route, Parameters: parameters,
	}
}

// declaredLifecycle is the hook ID the trigger node declares to activation.
func declaredLifecycle(t *testing.T) map[string]string {
	t.Helper()
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]
	return map[string]string{waha.TriggerNodeType: latest.Trigger.Lifecycle.ID}
}

// Auto-registration is opt-in because it writes to a customer's own WAHA
// instance, and importing a workflow should not do that silently.
//
// When it is on, the registration merges: the session keeps its other webhook,
// its proxy, its engine and its own fields, and activating twice does not write
// twice — because writing WAHA's session document stops and restarts a live
// WhatsApp session.
func TestAutoRegistrationIsOffByDefaultAndInstallsTheURLWhenTurnedOn(t *testing.T) {
	t.Parallel()

	stub, server := newSessionStub(t, configuredSession)
	set := install(t, safehttp.DefaultPolicy())
	declared := declaredLifecycle(t)
	credential := stubCredential{baseURL: server.URL}

	off := bindingFor("abc123", false, nil)
	if _, err := coordinator(t, set, off, credential).Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
		t.Fatalf("Activated() error = %v", err)
	}
	if calls := stub.recorded(); len(calls) != 0 {
		t.Fatalf("activation called WAHA %d times with auto-registration off: %#v", len(calls), calls)
	}

	on := bindingFor("abc123", true, nil)
	notices, err := coordinator(t, set, on, credential).Activated(context.Background(), "tenant-a", "wf_1", declared)
	if err != nil {
		t.Fatalf("Activated() error = %v", err)
	}
	if len(notices) != 0 {
		t.Fatalf("notices = %#v, want none when the URL was installed", notices)
	}

	writes := 0
	for _, call := range stub.recorded() {
		if call.method != http.MethodPut {
			continue
		}
		writes++
		if call.path != "/api/sessions/sales" {
			t.Fatalf("PUT path = %q, want the session the node names", call.path)
		}
		if call.apiKey != "k-waha" {
			t.Fatalf("X-Api-Key = %q, want the credential applied", call.apiKey)
		}
	}
	if writes != 1 {
		t.Fatalf("activation wrote the session %d times, want one: %#v", writes, stub.recorded())
	}

	installed := stub.entryFor(t, "abc123")
	if installed == nil {
		t.Fatalf("session webhooks = %#v, want this workflow's URL installed", stub.webhooks(t))
	}
	if installed["url"] != "https://flows.example.test/webhook/abc123" {
		t.Fatalf("installed entry = %#v, want this workflow's own URL", installed)
	}
	if !reflect.DeepEqual(installed["events"], []any{"*"}) {
		t.Fatalf("installed entry = %#v, want every event of the session", installed)
	}
	if stub.entryFor(t, "someone-else") == nil {
		t.Fatalf("session webhooks = %#v, want the other workflow's webhook kept", stub.webhooks(t))
	}
	want := decodedDocument(t, configuredSession)
	delete(want["config"].(map[string]any), "webhooks")
	if !reflect.DeepEqual(stub.settings(t), want) {
		t.Fatalf("session = %s, want everything but the webhook list untouched:\n%s",
			mustEncodeJSON(t, stub.settings(t)), mustEncodeJSON(t, want))
	}

	// Activating an already-active workflow re-checks rather than re-registers,
	// so the session is not restarted again.
	if _, err := coordinator(t, set, on, credential).Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
		t.Fatalf("second Activated() error = %v", err)
	}
	if writes := stub.writes(); writes != 1 {
		t.Fatalf("second activation wrote the session: %#v", stub.recorded())
	}
	if entries := stub.webhooks(t); len(entries) != 2 {
		t.Fatalf("session webhooks = %#v, want this route once", entries)
	}
}

func decodedDocument(t *testing.T, document string) map[string]any {
	t.Helper()
	decoded := map[string]any{}
	if err := json.Unmarshal([]byte(document), &decoded); err != nil {
		t.Fatalf("decode session document error = %v", err)
	}
	return decoded
}

func mustEncodeJSON(t *testing.T, value any) string {
	t.Helper()
	encoded, err := json.Marshal(value)
	if err != nil {
		t.Fatalf("encode error = %v", err)
	}
	return string(encoded)
}

// TestTheRegisteredWebhookCarriesTheHMACKeyOnlyWhenASecretIsConfigured: WAHA
// signs with config.webhooks[].hmac.key, and the node refuses any delivery it
// cannot verify, so a secret the node has and WAHA does not sign with makes
// every delivery fail — and an hmac entry that signs with the text of a
// template would fail them the other way round.
func TestTheRegisteredWebhookCarriesTheHMACKeyOnlyWhenASecretIsConfigured(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		extra map[string]any
		want  any
	}{
		"with a secret":    {extra: map[string]any{"hmacSecret": "topsecret"}, want: map[string]any{"key": "topsecret"}},
		"without a secret": {want: nil},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			stub, server := newSessionStub(t, configuredSession)
			set := install(t, safehttp.DefaultPolicy())
			credential := stubCredential{baseURL: server.URL}
			binding := bindingFor("abc123", true, testCase.extra)
			if _, err := coordinator(t, set, binding, credential).Activated(context.Background(), "tenant-a", "wf_1", declaredLifecycle(t)); err != nil {
				t.Fatalf("Activated() error = %v", err)
			}

			installed := stub.entryFor(t, "abc123")
			if installed == nil {
				t.Fatalf("session webhooks = %#v, want this workflow's URL installed", stub.webhooks(t))
			}
			hmac, present := installed["hmac"]
			if testCase.want == nil {
				if present {
					t.Fatalf("installed entry = %#v, want no hmac key at all", installed)
				}
				return
			}
			if !reflect.DeepEqual(hmac, testCase.want) {
				t.Fatalf("installed hmac = %#v, want %#v", hmac, testCase.want)
			}
		})
	}
}

// TestDeactivationRemovesOnlyThisWorkflowsWebhook: leaving this route behind
// makes WAHA retry a delivery that can only answer 404, and unregistering
// somebody else's workflow because it shares the session is the same bug as
// registration's, on the way out.
func TestDeactivationRemovesOnlyThisWorkflowsWebhook(t *testing.T) {
	t.Parallel()

	stub, server := newSessionStub(t, configuredSession)
	set := install(t, safehttp.DefaultPolicy())
	declared := declaredLifecycle(t)
	credential := stubCredential{baseURL: server.URL}
	binding := bindingFor("abc123", true, nil)

	if _, err := coordinator(t, set, binding, credential).Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
		t.Fatalf("Activated() error = %v", err)
	}
	coordinator(t, set, binding, credential).Deactivated(context.Background(), "tenant-a", "wf_1", declared)

	if installed := stub.entryFor(t, "abc123"); installed != nil {
		t.Fatalf("session webhooks = %#v, want this workflow's URL gone", stub.webhooks(t))
	}
	if stub.entryFor(t, "someone-else") == nil {
		t.Fatalf("session webhooks = %#v, want the other workflow's webhook kept", stub.webhooks(t))
	}
	want := decodedDocument(t, configuredSession)
	delete(want["config"].(map[string]any), "webhooks")
	if !reflect.DeepEqual(stub.settings(t), want) {
		t.Fatalf("session = %s, want everything but the webhook list untouched", mustEncodeJSON(t, stub.settings(t)))
	}

	// Deactivating again must not restart the session to change nothing.
	writes := stub.writes()
	coordinator(t, set, binding, credential).Deactivated(context.Background(), "tenant-a", "wf_1", declared)
	if stub.writes() != writes {
		t.Fatalf("second deactivation wrote the session: %#v", stub.recorded())
	}
}

// TestASecondWorkflowOnTheSameSessionDoesNotEvictTheFirst is the impact this
// fix is for: two workflows on one WAHA session used to evict each other, and
// the evicted one kept receiving retries it could only answer with a 404.
func TestASecondWorkflowOnTheSameSessionDoesNotEvictTheFirst(t *testing.T) {
	t.Parallel()

	stub, server := newSessionStub(t, configuredSession)
	set := install(t, safehttp.DefaultPolicy())
	declared := declaredLifecycle(t)
	credential := stubCredential{baseURL: server.URL}

	for _, route := range []string{"first111", "second222"} {
		binding := bindingFor(route, true, nil)
		if _, err := coordinator(t, set, binding, credential).Activated(context.Background(), "tenant-a", "wf_1", declared); err != nil {
			t.Fatalf("Activated(%s) error = %v", route, err)
		}
	}

	for _, route := range []string{"first111", "second222", "someone-else"} {
		if stub.entryFor(t, route) == nil {
			t.Fatalf("session webhooks = %#v, want %s still registered", stub.webhooks(t), route)
		}
	}
	if entries := stub.webhooks(t); len(entries) != 3 {
		t.Fatalf("session webhooks = %#v, want three entries", entries)
	}
}

// The binding comes from the definition the pack registered, not from a node
// type named at the composition root — which is what lets a pack ship a trigger
// at all.
func TestTheTriggerBindsItsRouteThroughRegistryDrivenExtraction(t *testing.T) {
	t.Parallel()

	set := install(t, safehttp.DefaultPolicy())
	packs := triggerPacks(t)
	latest := packs[len(packs)-1]

	extract := webhook.Extract(set.definitions, func(path string) string { return strings.TrimSpace(path) })
	triggers := extract(workflow.Document{
		Nodes: []workflow.Node{{
			ID: "trigger", Name: "WAHA Trigger", Type: waha.TriggerNodeType, TypeVersion: latest.Version,
			Parameters:  map[string]any{"path": "sales-inbox", "session": "sales", "deliveryIdHeader": "X-Webhook-Request-Id"},
			Credentials: map[string]string{waha.CredentialType: "cred-1"},
		}},
	})
	if len(triggers) != 1 {
		t.Fatalf("extracted %d bindings, want one", len(triggers))
	}
	bound := triggers[0]
	if bound.Path != "sales-inbox" || bound.Method != http.MethodPost {
		t.Fatalf("binding = %s %s, want the node's own path and WAHA's method", bound.Method, bound.Path)
	}
	// The retry header travels with the binding, so the HTTP boundary can
	// deduplicate without re-reading the workflow document.
	if bound.Parameters["deliveryIdHeader"] != "X-Webhook-Request-Id" {
		t.Fatalf("binding parameters = %#v, want the retry header carried", bound.Parameters)
	}
	references, _ := bound.Parameters["$credentials"].(map[string]any)
	if references[waha.CredentialType] != "cred-1" {
		t.Fatalf("binding credentials = %#v, want the WAHA credential reference", bound.Parameters["$credentials"])
	}
}

// The claim this whole phase rests on, measured rather than asserted: no real
// WAHA template is blocked by a WAHA node any more.
//
// Before this work, seven of the thirteen failed to compile on a WAHA node
// itself — three on the action node, four on the trigger. What remains is other
// n8n nodes KilasFlow does not have yet, which is a different backlog.
func TestNoWAHATemplateIsBlockedByAWAHANode(t *testing.T) {
	t.Parallel()

	loaded, err := corpus.Load()
	if err != nil {
		t.Skipf("the corpus is not present; materialise it with %s", corpus.SyncCommand)
	}
	set := install(t, safehttp.DefaultPolicy())
	if err := nodes.RegisterAll(set.definitions); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}

	checked := 0
	for _, fixture := range loaded {
		if fixture.Source != corpus.SourceWAHATemplates {
			continue
		}
		checked++
		imported, err := n8n.Import(fixture.Payload, set.definitions)
		if err != nil {
			t.Errorf("%s did not import: %v", fixture.Name, err)
			continue
		}
		// Every WAHA node has to arrive as a native node rather than as the
		// unsupported placeholder, whichever package form the template used.
		for _, node := range imported.Document.Nodes {
			if node.Type != n8n.UnsupportedNodeType {
				continue
			}
			original, _ := node.Parameters["originalType"].(string)
			if strings.Contains(strings.ToLower(original), "waha") {
				t.Errorf("%s: %s imported as an unsupported placeholder", fixture.Name, original)
			}
		}
		// And compiling has to fail, when it fails, on something that is not a
		// WAHA node.
		if _, err := workflow.Compile(imported.Document, set.definitions); err != nil {
			if strings.Contains(strings.ToLower(err.Error()), "waha") {
				t.Errorf("%s is blocked by a WAHA node: %v", fixture.Name, firstLine(err))
			}
		}
	}
	if checked == 0 {
		t.Skip("no WAHA templates in the corpus")
	}
	t.Logf("checked %d WAHA templates", checked)
}

func firstLine(err error) string {
	text := err.Error()
	if index := strings.IndexByte(text, '\n'); index >= 0 {
		text = text[:index]
	}
	return text
}
