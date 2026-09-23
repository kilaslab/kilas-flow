package cli

import (
	"bufio"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/mcp"
)

// TestMCPToolsAreTheCommandTree is the registry-level gate: every tool names a
// verb the binary implements, every verb the binary implements is a tool exactly
// once (bar the two server modes), and every property a tool publishes maps back
// to something the verb can be given.
//
// It is the test the ticket asks for — the tool list walked against the command
// tree — and it fails the moment the two drift: a verb added and not published,
// a tool published for a verb that was renamed, a flag the adapter cannot
// describe, or a positional argument whose declaration does not match what the
// verb's own code demands.
func TestMCPToolsAreTheCommandTree(t *testing.T) {
	verbs := registry()
	catalogue, err := buildMCPCatalogue(verbs)
	if err != nil {
		t.Fatalf("the command tree does not render as tools: %v", err)
	}

	byPath := make(map[string]Verb, len(verbs))
	for _, verb := range verbs {
		byPath[verb.Path] = verb
	}

	// Every tool names a real verb, and no verb is published twice.
	seen := make(map[string]bool, len(catalogue.descriptors))
	for _, descriptor := range catalogue.descriptors {
		tool, known := catalogue.byName[descriptor.Name]
		if !known {
			t.Fatalf("tool %q is published without an entry to dispatch through", descriptor.Name)
		}
		if seen[descriptor.Name] {
			t.Fatalf("tool %q is published twice", descriptor.Name)
		}
		seen[descriptor.Name] = true

		if _, registered := byPath[tool.verb.Path]; !registered {
			t.Errorf("tool %q maps onto %q, which is not a registered verb", descriptor.Name, tool.verb.Path)
		}
		if descriptor.Name != mcpToolName(tool.verb.Path) {
			t.Errorf("tool %q is named after %q, want %q", descriptor.Name, tool.verb.Path, mcpToolName(tool.verb.Path))
		}
		if descriptor.Description == "" {
			t.Errorf("tool %q has no description", descriptor.Name)
		}
		if descriptor.InputSchema["type"] != "object" {
			t.Errorf("tool %q has an input schema that is not an object", descriptor.Name)
		}
	}

	// Every verb is a tool exactly once, except the two that are this process's
	// own server modes.
	for _, verb := range verbs {
		if mcpServerModes[verb.Path] {
			if seen[mcpToolName(verb.Path)] {
				t.Errorf("verb %q is a server mode and must not be published as a tool", verb.Path)
			}

			continue
		}
		if !seen[mcpToolName(verb.Path)] {
			t.Errorf("verb %q is registered but is not published as a tool", verb.Path)
		}
	}
	if len(catalogue.descriptors) != len(verbs)-len(mcpServerModes) {
		t.Errorf("published %d tools for %d verbs, want %d", len(catalogue.descriptors), len(verbs), len(verbs)-len(mcpServerModes))
	}

	for name, tool := range catalogue.byName {
		t.Run(name, func(t *testing.T) {
			checkMCPTool(t, tool)
		})
	}
}

// checkMCPTool asserts one tool's schema and its mapping back to argv.
func checkMCPTool(t *testing.T, tool *mcpTool) {
	t.Helper()

	properties, _ := tool.schema["properties"].(map[string]any)
	required, _ := tool.schema["required"].([]string)

	// The required list is exactly the verb's declared, non-optional arguments,
	// in order: a schema that required something else would ask a caller for an
	// argument the verb does not take.
	wantRequired := make([]string, 0, len(tool.args))
	for _, argument := range tool.args {
		if !argument.optional {
			wantRequired = append(wantRequired, argument.property)
		}
	}
	if strings.Join(required, ",") != strings.Join(wantRequired, ",") {
		t.Errorf("required = %v, want %v", required, wantRequired)
	}

	// Every property is one the dispatcher consumes, and the guarded property
	// is on guarded verbs — and on the escape hatch, whose confirm answers for
	// whichever operation id it resolves to (BUG-r1m83f) rather than for the
	// tool itself.
	for property := range properties {
		if property == mcpConfirmProperty {
			if !tool.verb.Guarded && tool.verb.Path != apiVerbPath {
				t.Errorf("tool %q carries confirm, but %q is neither guarded nor the escape hatch", tool.name, tool.verb.Path)
			}

			continue
		}
		if !toolKnows(tool, property) {
			t.Errorf("tool %q publishes the property %q, which no argument or flag of %q maps to", tool.name, property, tool.verb.Path)
		}
	}
	for _, flag := range tool.flags {
		if _, published := properties[flag.property]; !published {
			t.Errorf("tool %q does not publish --%s", tool.name, flag.name)
		}
	}
	if tool.verb.Guarded {
		if _, published := properties[mcpConfirmProperty]; !published {
			t.Errorf("guarded tool %q does not publish %s", tool.name, mcpConfirmProperty)
		}
		if !strings.Contains(tool.descriptor().Description, tool.verb.Refusal) {
			t.Errorf("tool %q does not say what it does: %q", tool.name, tool.descriptor().Description)
		}
	}
	if tool.verb.Path == apiVerbPath {
		if _, published := properties[mcpConfirmProperty]; !published {
			t.Errorf("the escape hatch tool %q does not publish %s", tool.name, mcpConfirmProperty)
		}
	}

	// A guarded tool and the escape hatch are both annotated destructive, per
	// the MCP 2026-07-28 `annotations` shape: a client's auto-approval policy
	// has to see the escape hatch as reaching the same operations the named
	// tools do. Every other tool carries no annotations yet (FEAT-0hdfzd).
	wantDestructive := tool.verb.Guarded || tool.verb.Path == apiVerbPath
	annotations := tool.descriptor().Annotations
	switch {
	case wantDestructive && (annotations == nil || annotations.DestructiveHint == nil || !*annotations.DestructiveHint):
		t.Errorf("tool %q is guarded or the escape hatch but is not annotated destructive: %+v", tool.name, annotations)
	case !wantDestructive && annotations != nil:
		t.Errorf("tool %q carries annotations %+v, want none", tool.name, annotations)
	}

	// A call that sets every property maps back onto the verb: the positionals
	// in the order the verb takes them, and each flag as its own command-line
	// spelling. Run it with both true and false, because a boolean flag is the
	// one kind where those two spellings differ (a bare --flag versus none).
	checkMCPToolArgv(t, tool, true)
	checkMCPToolArgv(t, tool, false)

	// A missing required argument is refused rather than sent as an empty
	// string, and an unknown property is refused rather than ignored.
	for _, argument := range tool.args {
		if argument.optional {
			continue
		}
		if _, err := tool.argv(map[string]any{}, nil); err == nil {
			t.Errorf("a call without %s was accepted", argument.property)
		}

		break
	}
	if _, err := tool.argv(map[string]any{"nonsense": true}, nil); err == nil {
		t.Error("a call with an unknown property was accepted")
	}
}

// checkMCPToolArgv proves the argv a tool call builds is one the CLI's own
// parser reads back the way the caller meant, rather than checking it against
// a second call to flag.argv: that comparison is circular, because a bug in
// flag.argv would corrupt both sides of it the same way (which is exactly how
// the "--wait true" bug survived it). Feeding the generated argv through the
// real FlagSet cli.go itself builds — registerGlobalFlags plus verb.Flags,
// parsed with parseFlags — is the only check that exercises the parser an
// agent's call actually goes through.
func checkMCPToolArgv(t *testing.T, tool *mcpTool, boolSample bool) {
	t.Helper()

	arguments := make(map[string]any, len(tool.args)+len(tool.flags)+1)
	for _, argument := range tool.args {
		arguments[argument.property] = "value-" + argument.property
	}
	for _, flag := range tool.flags {
		arguments[flag.property] = mcpSampleValue(flag, boolSample)
	}
	if tool.verb.Guarded || tool.verb.Path == apiVerbPath {
		arguments[mcpConfirmProperty] = true
	}

	argv, err := tool.argv(arguments, nil)
	if err != nil {
		t.Fatalf("argv(%v): %v", arguments, err)
	}

	// The command line the tool builds is one the CLI's own parser resolves to
	// the same verb, which is what makes the tool a mapping rather than a
	// second entry point.
	verb, rest, ok := splitVerb(registry(), argv)
	if !ok || verb.Path != tool.verb.Path {
		t.Fatalf("argv %v resolves to %q (%v), want %q", argv, verb.Path, ok, tool.verb.Path)
	}

	fs := flag.NewFlagSet(verb.Path, flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	globals := registerGlobalFlags(fs)
	if verb.Flags != nil {
		verb.Flags(fs)
	}

	positional, err := parseFlags(fs, globals, rest)
	if err != nil {
		t.Fatalf("argv %v did not parse: %v", argv, err)
	}

	wantPositional := make([]string, 0, len(tool.args))
	for _, argument := range tool.args {
		wantPositional = append(wantPositional, arguments[argument.property].(string))
	}
	if strings.Join(positional, " ") != strings.Join(wantPositional, " ") {
		t.Errorf("argv %v parsed to positionals %v, want %v", argv, positional, wantPositional)
	}

	if (tool.verb.Guarded || tool.verb.Path == apiVerbPath) && !globals.Yes {
		t.Errorf("argv %v did not parse --yes", argv)
	}

	// Every boolean flag holds the sample value the caller gave it — the
	// assertion that catches "--wait true", where Go's flag package leaves
	// "true" as a stray positional and the flag itself keeps its default.
	for _, mf := range tool.flags {
		if mf.kind != mcpFlagBool {
			continue
		}

		registered := fs.Lookup(mf.name)
		if registered == nil {
			t.Fatalf("the real FlagSet has no --%s", mf.name)
		}
		getter, ok := registered.Value.(flag.Getter)
		if !ok {
			t.Fatalf("--%s is not a flag.Getter", mf.name)
		}
		held, ok := getter.Get().(bool)
		if !ok {
			t.Fatalf("--%s parsed as %T, want bool", mf.name, getter.Get())
		}
		if held != boolSample {
			t.Errorf("argv %v left --%s at %v, want %v", argv, mf.name, held, boolSample)
		}
	}
}

// toolKnows reports whether a property is one the tool's own metadata accounts
// for.
func toolKnows(tool *mcpTool, property string) bool {
	for _, argument := range tool.args {
		if argument.property == property {
			return true
		}
	}
	for _, flag := range tool.flags {
		if flag.property == property {
			return true
		}
	}

	return false
}

// mcpSampleValue is a value of the kind a flag takes, for the mapping test.
// boolValue is the sample a boolean flag carries, so the caller can drive the
// round trip with both true and false.
func mcpSampleValue(flag mcpFlag, boolValue bool) any {
	switch flag.kind {
	case mcpFlagBool:
		return boolValue
	case mcpFlagInteger:
		return float64(3)
	case mcpFlagNumber:
		return 1.5
	case mcpFlagList:
		return []any{"first=1", "second=2"}
	case mcpFlagDuration:
		// A schema-valid string that time.ParseDuration also accepts, since the
		// round trip now parses the generated argv with the real FlagSet rather
		// than only rendering it.
		return "45s"
	default:
		return "sample-" + flag.name
	}
}

// TestMCPGuardedToolsRequireConfirmation drives a real guarded verb through the
// protocol twice: without `confirm`, where the CLI's own guard refuses and
// nothing reaches the server, and with it, where the request is the one
// `kilasflow workflow activate <id> --yes` sends.
//
// The equivalence is the point. `confirm: true` is not a second permission
// system: it is `--yes`, and the adapter passes nothing when it is absent.
func TestMCPGuardedToolsRequireConfirmation(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
		"/":                    jsonBody(http.StatusOK, `{"id":"wf_1","name":"Orders","active":true}`),
	})

	// What the CLI itself sends, for the comparison below.
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"workflow", "activate", "wf_1", "--yes", "--url", srv.URL, "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("the CLI's own activate exited %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}
	direct := mutatingCalls(api.calls)
	if len(direct) != 1 {
		t.Fatalf("the CLI sent %d mutating requests, want one: %+v", len(direct), api.calls)
	}
	api.calls = nil

	t.Run("without confirm", func(t *testing.T) {
		session := startMCPServe(t, srv.URL)
		session.initialize()

		answer := session.call("workflow_activate", map[string]any{"workflow_id": "wf_1"})
		if answer.isError != true {
			t.Fatalf("isError = %v, want true (text=%q)", answer.isError, answer.text)
		}

		doc := envelope(t, answer.text)
		if failure := envelopeFailure(doc); failure != confirmationCode {
			t.Fatalf("error.code = %q, want %q (text=%q)", failure, confirmationCode, answer.text)
		}
		if !strings.Contains(answer.text, "--yes") {
			t.Errorf("the refusal does not say how to confirm: %q", answer.text)
		}
		if len(api.calls) != 0 {
			t.Fatalf("the refusal sent %d request(s): %+v, want none", len(api.calls), api.calls)
		}
	})

	t.Run("with confirm", func(t *testing.T) {
		session := startMCPServe(t, srv.URL)
		session.initialize()

		answer := session.call("workflow_activate", map[string]any{"workflow_id": "wf_1", "confirm": true})
		if answer.isError {
			t.Fatalf("the confirmed call failed: %q", answer.text)
		}
		if doc := envelope(t, answer.text); doc["ok"] != true {
			t.Fatalf("ok = %v, want true (text=%q)", doc["ok"], answer.text)
		}

		calls := mutatingCalls(api.calls)
		if len(calls) != 1 {
			t.Fatalf("the confirmed call made %d mutating requests, want one: %+v", len(calls), api.calls)
		}
		if calls[0].Method != direct[0].Method || calls[0].Path != direct[0].Path {
			t.Fatalf("the confirmed call sent %s %s, want the CLI's own %s %s",
				calls[0].Method, calls[0].Path, direct[0].Method, direct[0].Path)
		}
	})
}

// TestMCPGuardedToolRunsWithAuthOff: MCP runs the same run() path a CLI
// invocation does, so the auth-off confirmation TestGuardedVerbWithAuthOffReachesTheServer
// proves for the CLI (BUG-y38bss's follow-up: a bare 401 from /auth/me is
// confirmed against the OpenAPI document's root security requirement before
// it is trusted) covers `confirm: true` too. /auth/me answers 401 and the
// document declares no security requirement, so the confirmed call reaches
// the server.
func TestMCPGuardedToolRunsWithAuthOff(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": problemBody(http.StatusUnauthorized, authOff401),
		operationsPath:         jsonBody(http.StatusOK, openAPIWithoutSecurity),
		"/":                    jsonBody(http.StatusOK, `{"id":"wf_1","name":"Orders","active":true}`),
	})

	session := startMCPServe(t, srv.URL)
	session.initialize()

	answer := session.call("workflow_activate", map[string]any{"workflow_id": "wf_1", "confirm": true})
	if answer.isError {
		t.Fatalf("the confirmed call failed: %q", answer.text)
	}
	if doc := envelope(t, answer.text); doc["ok"] != true {
		t.Fatalf("ok = %v, want true (text=%q)", doc["ok"], answer.text)
	}

	calls := mutatingCalls(api.calls)
	if len(calls) != 1 {
		t.Fatalf("the confirmed call made %d mutating requests, want one: %+v", len(calls), api.calls)
	}
	if calls[0].Method != http.MethodPost || calls[0].Path != apiPrefix+"/workflows/wf_1/activate" {
		t.Fatalf("the confirmed call sent %s %s, want POST %s", calls[0].Method, calls[0].Path, apiPrefix+"/workflows/wf_1/activate")
	}
}

// TestMCPAPIToolRequiresConfirmationForAGuardedOperation is the MCP half of
// BUG-r1m83f: the escape hatch tool reaches the same guarded operations the
// named tools do, so a call whose operation_id is one of them needs the same
// `confirm` the named tool would need — and gets a destructiveHint annotation
// because the id is not known until the call is made.
func TestMCPAPIToolRequiresConfirmationForAGuardedOperation(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{http.MethodPost, "/api/v1/workflows/{id}/activate", "activate-workflow"},
		)),
		"/api/v1/workflows/wf_1/activate": jsonBody(http.StatusOK, `{"id":"wf_1","name":"Orders","active":true}`),
	})

	t.Run("without confirm", func(t *testing.T) {
		session := startMCPServe(t, srv.URL)
		session.initialize()

		answer := session.call("api", map[string]any{
			"operation_id": "activate-workflow",
			"path":         []any{"id=wf_1"},
		})
		if !answer.isError {
			t.Fatalf("isError = %v, want true (text=%q)", answer.isError, answer.text)
		}

		doc := envelope(t, answer.text)
		if failure := envelopeFailure(doc); failure != confirmationCode {
			t.Fatalf("error.code = %q, want %q (text=%q)", failure, confirmationCode, answer.text)
		}
		if !strings.Contains(answer.text, "activate-workflow") || !strings.Contains(answer.text, "--yes") {
			t.Errorf("the refusal does not name the operation or say how to confirm: %q", answer.text)
		}
		if len(api.calls) != 0 {
			t.Fatalf("the refusal sent %d request(s): %+v, want none", len(api.calls), api.calls)
		}
	})

	t.Run("with confirm", func(t *testing.T) {
		session := startMCPServe(t, srv.URL)
		session.initialize()

		answer := session.call("api", map[string]any{
			"operation_id": "activate-workflow",
			"path":         []any{"id=wf_1"},
			"confirm":      true,
		})
		if answer.isError {
			t.Fatalf("the confirmed call failed: %q", answer.text)
		}
		if doc := envelope(t, answer.text); doc["ok"] != true {
			t.Fatalf("ok = %v, want true (text=%q)", doc["ok"], answer.text)
		}

		calls := mutatingCalls(api.calls)
		if len(calls) != 1 || calls[0].Method != http.MethodPost || calls[0].Path != apiPrefix+"/workflows/wf_1/activate" {
			t.Fatalf("the confirmed call sent %+v, want exactly one POST %s", calls, apiPrefix+"/workflows/wf_1/activate")
		}
	})
}

// TestMCPToolResultNeverCarriesAnEchoedSecret: a tool result is the envelope's
// text, and it lands in the agent's transcript, so a server that echoes a
// refused credential back must lose the secret before the harness sees it.
func TestMCPToolResultNeverCarriesAnEchoedSecret(t *testing.T) {
	const secret = "TOPSECRET-123"
	echoed := `{"name":"x","type":"httpBearerAuth","secret":{"token":"` + secret + `"}}`

	srv, _ := mcpStub(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
		operationsPath: jsonBody(http.StatusOK, servedDocument(
			[3]string{http.MethodPost, "/api/v1/credentials", "create-credential"},
		)),
		"/api/v1/credentials": problemBody(http.StatusUnprocessableEntity,
			`{"title":"Unprocessable Entity","status":422,"detail":"validation failed","errors":[`+
				`{"message":"expected required property fields to be present","location":"body","value":`+echoed+`},`+
				`{"message":"unexpected property","location":"body.secret","value":`+echoed+`}]}`),
	})

	session := startMCPServe(t, srv.URL)
	session.initialize()

	answer := session.call("api", map[string]any{
		"operation_id": "create-credential",
		"body":         echoed,
		"confirm":      true,
	})
	if !answer.isError {
		t.Fatalf("isError = %v, want true (text=%q)", answer.isError, answer.text)
	}
	if strings.Contains(answer.text, secret) {
		t.Fatalf("the tool result carried the secret: %q", answer.text)
	}
	if !strings.Contains(answer.text, "expected required property fields") {
		t.Fatalf("the tool result lost the problem itself: %q", answer.text)
	}
}

// TestMCPServeAnswersARealClient drives the adapter over real pipes with real
// JSON-RPC frames, the way a harness does: initialize, the initialized
// notification, a read-only call, and a listing.
//
// Nothing here reaches into the adapter's own types: the frames are what a
// client would write and read, and the assertions are on what came back.
func TestMCPServeAnswersARealClient(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		"/": func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			_, _ = io.WriteString(w, `[{"id":"wf_1","name":"Orders","active":false}]`)
		},
		// The document `api --list` reads to enumerate operations.
		operationsPath: jsonBody(http.StatusOK, `{"paths":{"/api/v1/workflows":{"get":{"operationId":"list-workflows"}}}}`),
		// What `run` posts to, for the boolean-argument live check below.
		apiPrefix + "/workflows/wf_1/run": jsonBody(http.StatusOK, `{"id":"exec_1","workflowId":"wf_1","status":"running"}`),
	})

	session := startMCPServe(t, srv.URL)

	handshake := session.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "harness", "version": "1"},
	})

	answer := mcpResult(t, handshake)
	if version, _ := answer["protocolVersion"].(string); version != "2025-06-18" {
		t.Errorf("protocolVersion = %q, want the client's own 2025-06-18", version)
	}
	info, _ := answer["serverInfo"].(map[string]any)
	if info["name"] != "kilasflow" {
		t.Errorf("serverInfo.name = %v, want kilasflow", info["name"])
	}
	if info["version"] != "0.0.0-test" {
		t.Errorf("serverInfo.version = %v, want the binary's own version", info["version"])
	}
	capabilities, _ := answer["capabilities"].(map[string]any)
	if _, declared := capabilities["tools"]; !declared {
		t.Errorf("capabilities = %v, want a tools capability", capabilities)
	}
	if instructions, _ := answer["instructions"].(string); instructions == "" {
		t.Error("the initialize answer carries no instructions")
	}

	// A notification is never answered. Sending one and then a request proves
	// it: the next frame on the wire is that request's answer, not a reply to
	// the notification.
	session.notify("notifications/initialized")
	if mcpResult(t, session.request("ping", nil)) == nil {
		t.Fatal("ping answered no result")
	}

	listing := mcpResult(t, session.request("tools/list", nil))
	tools, _ := listing["tools"].([]any)
	if len(tools) == 0 {
		t.Fatal("tools/list published no tools")
	}
	if len(tools) != len(registry())-len(mcpServerModes) {
		t.Errorf("tools/list published %d tools, want %d", len(tools), len(registry())-len(mcpServerModes))
	}

	names := make([]string, 0, len(tools))
	for _, entry := range tools {
		tool, _ := entry.(map[string]any)
		name, _ := tool["name"].(string)
		names = append(names, name)

		if _, described := tool["description"].(string); !described {
			t.Errorf("tool %q has no description", name)
		}
		schema, _ := tool["inputSchema"].(map[string]any)
		if schema["type"] != "object" {
			t.Errorf("tool %q has no object input schema", name)
		}
	}
	for _, wanted := range []string{"workflow_get", "workflow_activate", "datastore_columns_rename", "run", "help"} {
		if !slices.Contains(names, wanted) {
			t.Errorf("tools/list does not publish %q", wanted)
		}
	}
	if slices.Contains(names, "serve") || slices.Contains(names, "mcp_serve") {
		t.Errorf("tools/list publishes a server mode: %v", names)
	}

	// A read-only call with a flag: the verb reads a page and the flag reaches
	// the server as a query parameter, which is what "a tool call is the CLI's
	// own invocation" means.
	call := session.call("workflow_list", map[string]any{"limit": 5})
	if call.isError {
		t.Fatalf("workflow_list failed: %q", call.text)
	}

	doc := envelope(t, call.text)
	if doc["ok"] != true {
		t.Fatalf("ok = %v, want true (text=%q)", doc["ok"], call.text)
	}
	data, _ := doc["data"].(map[string]any)
	if count, _ := data["count"].(float64); count != 1 {
		t.Errorf("the call read %v items, want 1 (text=%q)", data["count"], call.text)
	}
	if len(api.calls) != 1 {
		t.Fatalf("the call made %d requests, want one: %+v", len(api.calls), api.calls)
	}
	if api.calls[0].Query != "limit=5" {
		t.Errorf("the call sent query %q, want limit=5", api.calls[0].Query)
	}

	// An unknown tool is a protocol error, not a tool result: the request named
	// something this server does not have.
	unknown := session.request("tools/call", map[string]any{"name": "workflow_obliterate"})
	if failure, _ := unknown["error"].(map[string]any); failure == nil {
		t.Fatalf("an unknown tool was answered with a result: %v", unknown)
	} else if code, _ := failure["code"].(float64); int(code) != mcp.CodeInvalidParams {
		t.Errorf("an unknown tool answered code %v, want %d", failure["code"], mcp.CodeInvalidParams)
	}

	// An argument the schema does not have is refused too, rather than quietly
	// dropped.
	invalid := session.request("tools/call", map[string]any{
		"name": "workflow_get", "arguments": map[string]any{"workflow_id": "wf_1", "wait": true},
	})
	if failure, _ := invalid["error"].(map[string]any); failure == nil {
		t.Fatalf("an unknown argument was accepted: %v", invalid)
	}

	// A boolean argument reaches its flag, in both directions: `--wait true`,
	// the spelling the adapter used to emit, is not a flag Go's own parser
	// accepts, so any of these calls failing is that regression back.
	runCall := session.call("run", map[string]any{"workflow_id": "wf_1", "wait": false})
	if runCall.isError {
		t.Fatalf("run with wait:false failed: %q", runCall.text)
	}
	if doc := envelope(t, runCall.text); doc["ok"] != true {
		t.Errorf("run with wait:false: ok = %v, want true (text=%q)", doc["ok"], runCall.text)
	}

	apiCall := session.call("api", map[string]any{"list": true})
	if apiCall.isError {
		t.Fatalf("api with list:true failed: %q", apiCall.text)
	}
	if doc := envelope(t, apiCall.text); doc["ok"] != true {
		t.Errorf("api with list:true: ok = %v, want true (text=%q)", doc["ok"], apiCall.text)
	} else if data, _ := doc["data"].(map[string]any); data["count"] != float64(1) {
		t.Errorf("api with list:true: data.count = %v, want 1 (text=%q)", data["count"], apiCall.text)
	}

	installCall := session.call("skills_install", map[string]any{"dry_run": true, "target": "dir:" + t.TempDir()})
	if installCall.isError {
		t.Fatalf("skills_install with dry_run:true failed: %q", installCall.text)
	}
	if doc := envelope(t, installCall.text); doc["ok"] != true {
		t.Errorf("skills_install with dry_run:true: ok = %v, want true (text=%q)", doc["ok"], installCall.text)
	}

	if session.stop() != ExitOK {
		t.Fatalf("mcp serve exited %d, want %d", session.exitCode, ExitOK)
	}
}

// TestMCPServeRefusesWhatTheCLIWouldRefuse: the protocol's error channel is not
// a second error contract. A verb that fails answers with its own envelope and
// isError set, which is what a client reads, and an unfinished line ends the
// session without a protocol message ever being written to stdout.
func TestMCPServeRefusesWhatTheCLIWouldRefuse(t *testing.T) {
	srv, _ := mcpStub(t, map[string]http.HandlerFunc{
		"/": problemBody(http.StatusNotFound, `{"title":"not found","status":404}`),
	})

	session := startMCPServe(t, srv.URL)
	session.initialize()

	// A 404 from the server is the CLI's exit 4 and its `not_found` code,
	// carried through the envelope rather than translated.
	answer := session.call("workflow_get", map[string]any{"workflow_id": "wf_missing"})
	if !answer.isError {
		t.Fatalf("a missing workflow was not reported as an error: %q", answer.text)
	}

	doc := envelope(t, answer.text)
	if doc["ok"] != false {
		t.Fatalf("ok = %v, want false", doc["ok"])
	}
	if failure := envelopeFailure(doc); failure != "not_found" {
		t.Errorf("error.code = %q, want not_found (text=%q)", failure, answer.text)
	}

	// A missing required argument is refused before the verb runs, and says
	// which argument the verb takes.
	missing := session.request("tools/call", map[string]any{"name": "workflow_get", "arguments": map[string]any{}})
	failure, _ := missing["error"].(map[string]any)
	if failure == nil {
		t.Fatalf("a call without its workflow id was accepted: %v", missing)
	}
	if message, _ := failure["message"].(string); !strings.Contains(message, "workflow id") {
		t.Errorf("the refusal is %q, want it to name the workflow id", message)
	}
}

// TestMCPDeclaredArgumentsAreTheVerbsOwn proves the declaration the tool schemas
// are built from: a verb that declares positional arguments refuses a command
// line without them, with the usage error that names the first one.
//
// It is what keeps Verb.Args from being a second, drifting description of the
// verbs. A declaration that named an argument the verb does not require would
// let the invocation through to the stub and fail here on the exit code; one
// that named it differently would fail on the message.
func TestMCPDeclaredArgumentsAreTheVerbsOwn(t *testing.T) {
	srv, api := mcpStub(t, map[string]http.HandlerFunc{
		apiPrefix + "/auth/me": jsonBody(http.StatusOK, identityWithoutScopes),
		"/":                    jsonBody(http.StatusOK, `{"id":"ok_1","name":"ok"}`),
	})

	declared := 0

	for _, verb := range registry() {
		if len(verb.Args) == 0 || mcpServerModes[verb.Path] {
			continue
		}

		declared++

		t.Run(verb.Path, func(t *testing.T) {
			args := append(strings.Fields(verb.Path), "--url", srv.URL, "--token", "test-key", "--json")
			if verb.Guarded {
				// Consent first, so what this drives is the verb's own
				// argument check rather than the guard in front of it.
				args = append(args, "--yes")
			}

			code, handled, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
			if !handled {
				t.Fatalf("the verb fell through to the server path (stdout=%q stderr=%q)", stdout, stderr)
			}
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
			}

			doc := envelope(t, stdout)
			if failure := envelopeFailure(doc); failure != "usage" {
				t.Fatalf("error.code = %q, want usage (stdout=%q)", failure, stdout)
			}

			message, _ := doc["error"].(map[string]any)["message"].(string)
			if !strings.Contains(message, "no "+verb.Args[0].Name) {
				t.Errorf("the refusal is %q, want it to name %q", message, verb.Args[0].Name)
			}
			if calls := mutatingCalls(api.calls); len(calls) != 0 {
				t.Fatalf("the refusal sent %d request(s): %+v, want none", len(calls), calls)
			}
		})
	}

	if declared == 0 {
		t.Fatal("no verb declares a positional argument")
	}
}

// mcpStub serves a stub API and records what reached it.
func mcpStub(t *testing.T, routes map[string]http.HandlerFunc) (*httptest.Server, *recordingAPI) {
	t.Helper()

	api := newRecordingAPI(routes)

	return stubAPI(t, api.routesFor(t)), api
}

// mcpAnswer is one tool result as the test reads it.
type mcpAnswer struct {
	text    string
	isError bool
}

// mcpSession is a client of `mcp serve`: frames in on stdin, frames out on
// stdout, and nothing else on either.
type mcpSession struct {
	t        *testing.T
	to       *io.PipeWriter
	frames   chan string
	exit     chan int
	exitCode int
	stopped  bool
	nextID   int
	stderr   *strings.Builder
}

// startMCPServe runs `mcp serve` over pipes, with the endpoint the caller names
// as the server's own configuration.
func startMCPServe(t *testing.T, baseURL string) *mcpSession {
	t.Helper()

	serverIn, clientOut := io.Pipe()
	outReader, outWriter := io.Pipe()

	stderr := &strings.Builder{}
	session := &mcpSession{
		t:      t,
		to:     clientOut,
		frames: make(chan string, 8),
		exit:   make(chan int, 1),
		stderr: stderr,
	}

	env := Env{
		Args:    []string{"mcp", "serve", "--url", baseURL, "--token", "test-key"},
		Stdin:   serverIn,
		Stdout:  outWriter,
		Stderr:  stderr,
		Getenv:  homeEnv(t.TempDir(), nil),
		Version: "0.0.0-test",
	}

	go func() {
		code, _ := run(registry(), env)
		_ = outWriter.Close()
		session.exit <- code
	}()

	go func() {
		scanner := bufio.NewScanner(outReader)
		scanner.Buffer(make([]byte, 0, 64<<10), 8<<20)
		for scanner.Scan() {
			session.frames <- scanner.Text()
		}
		close(session.frames)
	}()

	t.Cleanup(func() {
		_ = clientOut.Close()
		_ = outReader.Close()

		if session.stopped {
			return
		}

		select {
		case <-session.exit:
		case <-time.After(10 * time.Second):
			t.Errorf("mcp serve did not exit when its input ended (stderr=%q)", stderr.String())
		}
	})

	return session
}

// initialize performs the handshake every client starts with.
func (s *mcpSession) initialize() {
	s.t.Helper()

	s.request("initialize", map[string]any{
		"protocolVersion": "2025-06-18",
		"capabilities":    map[string]any{},
		"clientInfo":      map[string]any{"name": "harness", "version": "1"},
	})
	s.notify("notifications/initialized")
}

// request writes one request frame and reads its answer.
func (s *mcpSession) request(method string, params any) map[string]any {
	s.t.Helper()

	s.nextID++
	id := s.nextID
	frame := map[string]any{"jsonrpc": "2.0", "id": id, "method": method}
	if params != nil {
		frame["params"] = params
	}
	s.send(frame)

	answer := s.next()
	if got, _ := answer["id"].(float64); int(got) != id {
		s.t.Fatalf("the answer to %s carries id %v, want %d (frame=%v)", method, answer["id"], id, answer)
	}

	return answer
}

// notify writes one notification frame. Nothing is read: a notification has no
// answer.
func (s *mcpSession) notify(method string) {
	s.t.Helper()

	s.send(map[string]any{"jsonrpc": "2.0", "method": method})
}

// call runs one tool and reads the result.
func (s *mcpSession) call(name string, arguments map[string]any) mcpAnswer {
	s.t.Helper()

	answer := s.request("tools/call", map[string]any{"name": name, "arguments": arguments})
	if failure, refused := answer["error"]; refused {
		s.t.Fatalf("tools/call %s was refused by the protocol: %v", name, failure)
	}

	result, ok := answer["result"].(map[string]any)
	if !ok {
		s.t.Fatalf("tools/call %s answered no result: %v", name, answer)
	}

	content, _ := result["content"].([]any)
	if len(content) == 0 {
		s.t.Fatalf("tools/call %s answered no content: %v", name, result)
	}
	block, _ := content[0].(map[string]any)
	text, _ := block["text"].(string)
	isError, _ := result["isError"].(bool)

	return mcpAnswer{text: text, isError: isError}
}

// send writes one frame.
func (s *mcpSession) send(frame map[string]any) {
	s.t.Helper()

	encoded, err := json.Marshal(frame)
	if err != nil {
		s.t.Fatalf("could not encode %v: %v", frame, err)
	}
	if _, err := fmt.Fprintf(s.to, "%s\n", encoded); err != nil {
		s.t.Fatalf("could not write to mcp serve: %v", err)
	}
}

// next reads one frame, or fails the test rather than hanging.
func (s *mcpSession) next() map[string]any {
	s.t.Helper()

	select {
	case frame, open := <-s.frames:
		if !open {
			s.t.Fatalf("mcp serve closed stdout without answering (stderr=%q)", s.stderr.String())
		}

		var decoded map[string]any
		if err := json.Unmarshal([]byte(frame), &decoded); err != nil {
			s.t.Fatalf("mcp serve wrote a frame that is not JSON: %v (frame=%q)", err, frame)
		}

		return decoded
	case <-time.After(15 * time.Second):
		s.t.Fatalf("mcp serve did not answer within 15s (stderr=%q)", s.stderr.String())

		return nil
	}
}

// stop closes the client's end and waits for the server to exit.
func (s *mcpSession) stop() int {
	s.t.Helper()

	_ = s.to.Close()

	select {
	case code := <-s.exit:
		s.exitCode = code
		s.stopped = true

		return code
	case <-time.After(15 * time.Second):
		s.t.Fatalf("mcp serve did not exit (stderr=%q)", s.stderr.String())

		return -1
	}
}

// mcpResult is a response's result, failing when the answer was an error.
func mcpResult(t *testing.T, answer map[string]any) map[string]any {
	t.Helper()

	if failure, refused := answer["error"]; refused {
		t.Fatalf("the request was refused: %v", failure)
	}

	result, ok := answer["result"].(map[string]any)
	if !ok {
		t.Fatalf("the answer carries no result: %v", answer)
	}

	return result
}
