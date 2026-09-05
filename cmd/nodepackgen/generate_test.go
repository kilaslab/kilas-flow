package main

import (
	"context"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

var update = flag.Bool("update", false, "rewrite the golden pack and report")

func fixture(t *testing.T) (*nodepack.Pack, *report, []byte) {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("testdata", "spec.json"))
	if err != nil {
		t.Fatalf("read spec error = %v", err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatalf("parse spec error = %v", err)
	}
	manifestRaw, err := os.ReadFile(filepath.Join("testdata", "manifest.json"))
	if err != nil {
		t.Fatalf("read manifest error = %v", err)
	}
	loaded, err := decodeManifest(manifestRaw)
	if err != nil {
		t.Fatalf("decode manifest error = %v", err)
	}
	pack, generated, err := generate(&doc, "testdata/spec.json", raw, loaded)
	if err != nil {
		t.Fatalf("generate() error = %v", err)
	}
	return pack, generated, raw
}

// The golden pack is the contract. `resource` and `operation` values are what an
// imported workflow matches against by literal string, so a change to either is
// a change that has to be looked at rather than absorbed.
func TestGeneratedPackMatchesTheGoldenFile(t *testing.T) {
	pack, generated, _ := fixture(t)

	encoded, err := encodePack(pack)
	if err != nil {
		t.Fatalf("encodePack() error = %v", err)
	}
	rendered := renderReport(generated)
	if *update {
		if err := os.WriteFile(filepath.Join("testdata", "pack.golden.json"), encoded, 0o644); err != nil {
			t.Fatalf("write golden pack error = %v", err)
		}
		if err := os.WriteFile(filepath.Join("testdata", "report.golden.md"), []byte(rendered), 0o644); err != nil {
			t.Fatalf("write golden report error = %v", err)
		}
		t.Skip("golden files rewritten")
	}

	for _, file := range []struct {
		name string
		got  string
	}{
		{"pack.golden.json", string(encoded)},
		{"report.golden.md", rendered},
	} {
		want, err := os.ReadFile(filepath.Join("testdata", file.name))
		if err != nil {
			t.Fatalf("read %s error = %v", file.name, err)
		}
		if string(want) != file.got {
			t.Errorf("%s differs; rerun with -update and review the diff", file.name)
		}
	}
}

// Map iteration must never reach the output, or a review diff would be noise
// and "regenerate and diff" would stop being a check anyone can run.
func TestRegeneratingAnUnchangedDocumentIsByteIdentical(t *testing.T) {
	first, _, _ := fixture(t)
	firstBytes, err := encodePack(first)
	if err != nil {
		t.Fatalf("encodePack() error = %v", err)
	}
	// Ten runs: one repeat can agree with a map's iteration order by luck.
	for attempt := 0; attempt < 10; attempt++ {
		again, _, _ := fixture(t)
		againBytes, err := encodePack(again)
		if err != nil {
			t.Fatalf("encodePack() error = %v", err)
		}
		if string(firstBytes) != string(againBytes) {
			t.Fatalf("run %d produced a different pack; map iteration reached the output", attempt)
		}
	}
}

// The strings an imported workflow has to match, asserted directly rather than
// only through the golden file, so a reviewer accepting a golden diff still
// sees this fail if the naming rule changes.
func TestResourceAndOperationNamesFollowTheFormat(t *testing.T) {
	pack, _, _ := fixture(t)

	names := map[string][]string{}
	for _, resource := range pack.Resources {
		for _, operation := range resource.Operations {
			names[resource.Name] = append(names[resource.Name], operation.Name)
		}
	}
	want := map[string][]string{
		// The tag's emoji is stripped; the operationId's controller segment is
		// dropped; a digit run is its own word.
		"Chatting": {"Get Messages 2", "Send Text"},
		// An operationId with no `_` keeps all of itself.
		"Sessions": {"Create", "Sessions List"},
		"Files":    nil,
	}
	for resource, wantOperations := range want {
		if wantOperations == nil {
			if _, present := names[resource]; present {
				t.Fatalf("resource %q was generated, want every one of its operations refused", resource)
			}
			continue
		}
		got := strings.Join(names[resource], ", ")
		if got != strings.Join(wantOperations, ", ") {
			t.Errorf("resource %q operations = %q, want %q", resource, got, strings.Join(wantOperations, ", "))
		}
	}
}

// A construct the generator cannot express is absent from the pack *and* named
// in the report. Absent alone is a pack that looks complete and is not.
func TestInexpressibleConstructsAreReportedAndAbsent(t *testing.T) {
	pack, generated, _ := fixture(t)

	joined := strings.Join(generated.Skipped, "\n")
	for _, want := range []string{
		"multipart/form-data", // a non-JSON body
		"oneOf/anyOf union",   // a union body
		"no tag",              // an operation belonging to no resource
	} {
		if !strings.Contains(joined, want) {
			t.Errorf("skipped operations = %q, want a line naming %q", joined, want)
		}
	}
	notes := strings.Join(generated.Notes, "\n")
	if !strings.Contains(notes, `Parameter "chatId"`) {
		t.Errorf("notes = %q, want the chatId kind conflict reported", notes)
	}

	encoded, _ := encodePack(pack)
	for _, absent := range []string{"Upload", "Either", "Untagged"} {
		if strings.Contains(string(encoded), `"name": "`+absent) {
			t.Errorf("pack contains %q, which the generator could not express", absent)
		}
	}
}

// Path, query and first-level body properties become parameters; a nested
// object becomes one JSON parameter rather than being flattened or dropped.
func TestParameterKindsAndPlacement(t *testing.T) {
	pack, _, _ := fixture(t)

	kinds := map[string]string{}
	required := map[string]bool{}
	for _, parameter := range pack.Parameters {
		kinds[parameter.Key] = string(parameter.Kind)
		required[parameter.Key] = parameter.Required
	}
	for key, want := range map[string]string{
		"text":        "string",
		"linkPreview": "boolean",
		"retries":     "number",
		"mentions":    "json",
		"meta":        "json",
		"attachment":  "json",
		"order":       "options",
		"limit":       "number",
		// chatId is a string path parameter in one operation and an integer
		// query parameter in another, so it degrades rather than picking one.
		"chatId": "string",
	} {
		if kinds[key] != want {
			t.Errorf("parameter %q kind = %q, want %q", key, kinds[key], want)
		}
	}
	if _, present := kinds["X-Trace"]; present {
		t.Error("a header parameter became a property; the interpreter cannot place one")
	}
	// Required only where every operation that shows it requires it: chatId is
	// optional as a query parameter, so it cannot be demanded.
	if required["chatId"] {
		t.Error("chatId is required, but one of its operations does not require it")
	}
	if !required["text"] {
		t.Error("text is required by its only operation and should stay required")
	}

	sends := map[string]string{}
	for _, resource := range pack.Resources {
		for _, operation := range resource.Operations {
			for _, send := range operation.Sends {
				sends[resource.Name+"/"+operation.Name+"/"+send.From] = send.Type
			}
		}
	}
	for key, want := range map[string]string{
		"Chatting/Send Text/text":        "body",
		"Chatting/Get Messages 2/chatId": "path",
		"Chatting/Get Messages 2/limit":  "query",
		"Sessions/Create/chatId":         "query",
	} {
		if sends[key] != want {
			t.Errorf("send %q = %q, want %q", key, sends[key], want)
		}
	}
}

// A pack registers under its own namespace and cannot claim an executor.
func TestAGeneratedPackRegistersAndCannotChooseItsExecutor(t *testing.T) {
	pack, _, _ := fixture(t)

	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(routing.ExecutorID, engine.ExecutorFunc(
		func(context.Context, workflow.IRNode, workflow.NodeInput, engine.Request) (workflow.NodeOutput, error) {
			return nil, nil
		})); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodepack.Register(definitions, routes, executors, nil, pack); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	definition, found := definitions.Get(pack.Type, pack.Version)
	if !found {
		t.Fatal("the pack did not register")
	}
	if definition.Source != node.SourcePack {
		t.Fatalf("Source = %q, want %q", definition.Source, node.SourcePack)
	}
	if definition.ExecutorID != routing.ExecutorID {
		t.Fatalf("ExecutorID = %q, want the routing interpreter", definition.ExecutorID)
	}
	if _, found := routes.Lookup(pack.Type, pack.Version); !found {
		t.Fatal("the routing description did not register alongside the definition")
	}

	// A pack whose binding is not installed is refused, which is what makes
	// "the pack and its runtime arrive together" enforceable rather than a
	// convention.
	bare := nodepack.Register(node.NewRegistry(), routing.NewRegistry(), engine.NewRegistry(), nil, pack)
	if bare == nil || !strings.Contains(bare.Error(), "has not installed") {
		t.Fatalf("Register() with no interpreter installed = %v, want a refusal", bare)
	}
}

// A pack cannot name a version the manifest did not choose, and the reserved
// cascade keys are not available to a generated parameter.
func TestLoadRefusesAPackThatReusesTheCascadeKeys(t *testing.T) {
	t.Parallel()

	_, _, err := nodepack.Load(&nodepack.Pack{
		Type: "x.y", Version: workflow.V(1), DisplayName: "X", Category: "X",
		Resources:  []nodepack.Resource{{Name: "R", Operations: []nodepack.Operation{{Name: "O", Method: "GET", URL: "/"}}}},
		Parameters: []nodepack.Parameter{{Key: "resource", Label: "Resource", Kind: "string"}},
	})
	if err == nil {
		t.Fatal("a parameter named `resource` was accepted, want the cascade key reserved")
	}
}

// The end of the claim: a document goes in, and a request comes out of the
// shared interpreter with no code written for this node anywhere.
func TestAGeneratedPackActuallyMakesTheRequestItDescribes(t *testing.T) {
	pack, _, _ := fixture(t)

	var method, path, query string
	var sent map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path, query = r.Method, r.URL.EscapedPath(), r.URL.RawQuery
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &sent)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":"m-1"}`))
	}))
	defer server.Close()

	// The manifest's base URL is a credential template; the test supplies a
	// credential whose non-secret half carries the server's address.
	pack.RequestDefaults.BaseURL = server.URL

	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	if err := executors.Register(routing.ExecutorID, routing.NewExecutor(policy, routes, definitions)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodepack.Register(definitions, routes, executors, nil, pack); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	definition, _ := definitions.Lookup(pack.Type, pack.Version)
	executor, _ := executors.Lookup(routing.ExecutorID)
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "pack-1", Name: "Fixture", Type: pack.Type, TypeVersion: pack.Version,
		Parameters: map[string]any{
			"resource": "Chatting", "operation": "Send Text",
			"chatId": "1@c.us", "text": "hello", "linkPreview": false,
		},
		Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}

	if method != http.MethodPost || path != "/api/sendText" {
		t.Fatalf("request = %s %s, want POST /api/sendText", method, path)
	}
	if query != "" {
		t.Fatalf("query = %q, want the body-only operation to send none", query)
	}
	if sent["chatId"] != "1@c.us" || sent["text"] != "hello" || sent["linkPreview"] != false {
		t.Fatalf("body = %#v, want the operation's own parameters", sent)
	}
	// A parameter belonging to another operation must not ride along.
	if _, present := sent["limit"]; present {
		t.Fatalf("body = %#v, want nothing from another operation", sent)
	}
	if len(output) != 1 || output[0][0].JSON["id"] != "m-1" {
		t.Fatalf("output = %#v, want the decoded response", output)
	}
}

// Picking an operation that belongs to another resource is a clear error, not a
// request to whichever same-named operation happened to be written last.
func TestAnOperationFromAnotherResourceIsRefused(t *testing.T) {
	pack, _, _ := fixture(t)

	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(routing.ExecutorID, routing.NewExecutor(safehttp.DefaultPolicy(), routes, definitions)); err != nil {
		t.Fatalf("Register() error = %v", err)
	}
	if err := nodepack.Register(definitions, routes, executors, nil, pack); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	definition, _ := definitions.Lookup(pack.Type, pack.Version)
	executor, _ := executors.Lookup(routing.ExecutorID)
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "pack-1", Name: "Fixture", Type: pack.Type, TypeVersion: pack.Version,
		Parameters: map[string]any{"resource": "Sessions", "operation": "Send Text"},
		Definition: definition,
	}, workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), `resource "Sessions" operation "Send Text"`) {
		t.Fatalf("Execute() error = %v, want the pair named", err)
	}
}
