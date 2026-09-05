package waha_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/execution"
	"github.com/kilaslabs/kilas-flow/internal/interop/n8n/corpus"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/packs/waha"
)

type registered struct {
	definitions *node.Registry
	routes      *routing.Registry
	executors   *engine.Registry
	options     *loadoptions.Resolver
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
	if err := waha.Register(set.definitions, set.routes, set.executors, set.options); err != nil {
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
	if len(packs) != 2 {
		t.Fatalf("Packs() = %d packs, want both published versions", len(packs))
	}

	for _, pack := range packs {
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

	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
	available := map[string]map[string]bool{}
	for _, pack := range packs {
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

	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
	want := map[string]string{
		"session": "{{ $json.session }}",
		"chatId":  "{{ $json.payload.from }}",
	}
	for _, pack := range packs {
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
	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
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
	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
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

	packs, err := waha.Packs()
	if err != nil {
		t.Fatalf("Packs() error = %v", err)
	}
	for _, pack := range packs {
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
