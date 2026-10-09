package cli

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// optionsRequest runs `node options` against a recording stub and returns the
// decoded request body the server received.
func optionsRequest(t *testing.T, stdin string, args ...string) map[string]any {
	t.Helper()

	api := newRecordingAPI(map[string]http.HandlerFunc{
		apiPrefix + "/node-types/kilasflow.lmChatOpenAi/load-options": jsonBody(http.StatusOK,
			`{"options":[{"label":"gpt","value":"gpt"}]}`),
	})
	srv := stubAPI(t, api.routesFor(t))

	full := append([]string{"node", "options", "kilasflow.lmChatOpenAi", "--property", "model", "--url", srv.URL, "--json"}, args...)
	code, _, stdout, stderr := runCLI(t, Env{
		Args:   full,
		Stdin:  strings.NewReader(stdin),
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	var body map[string]any
	if err := json.Unmarshal([]byte(api.last(t).Body), &body); err != nil {
		t.Fatalf("the request body is not JSON: %v (%q)", err, api.last(t).Body)
	}

	return body
}

// A property whose options depend on other parameters (a chat model's list
// depends on its base URL) can only load when those parameters reach the
// request. Before BUG-f85r5h the verb sent none, so the loader ran with an
// empty dependency set and the caller had to fall back to the escape hatch.
func TestNodeOptionsSendsEachParamAsAStringParameter(t *testing.T) {
	body := optionsRequest(t, "",
		"--param", "baseUrl=https://llm.example.test/v1", "--param", "region=eu", "--credential", "cred_1")

	parameters, _ := body["parameters"].(map[string]any)
	if len(parameters) != 2 || parameters["baseUrl"] != "https://llm.example.test/v1" || parameters["region"] != "eu" {
		t.Fatalf("body.parameters = %v, want baseUrl and region as given (body=%v)", body["parameters"], body)
	}
	if body["property"] != "model" || body["credentialId"] != "cred_1" {
		t.Errorf("body = %v, want the property and credential beside the parameters", body)
	}
}

func TestNodeOptionsKeepsAValueWithAnEqualsSignWhole(t *testing.T) {
	body := optionsRequest(t, "", "--param", "baseUrl=https://llm.example.test/v1?key=a=b")

	parameters, _ := body["parameters"].(map[string]any)
	if parameters["baseUrl"] != "https://llm.example.test/v1?key=a=b" {
		t.Fatalf("body.parameters = %v, want the value cut at the first = only", body["parameters"])
	}
}

func TestNodeOptionsParametersTakesTypedJSON(t *testing.T) {
	body := optionsRequest(t, "",
		"--parameters", `{"limit":10,"strict":true,"table":{"__rl":true,"mode":"list","value":"public"}}`)

	parameters, _ := body["parameters"].(map[string]any)
	table, _ := parameters["table"].(map[string]any)
	if parameters["limit"] != float64(10) || parameters["strict"] != true || table["value"] != "public" || table["__rl"] != true {
		t.Fatalf("body.parameters = %v, want the JSON object sent with its types intact", body["parameters"])
	}
}

func TestNodeOptionsParametersReadsAFileAndStdin(t *testing.T) {
	path := filepath.Join(t.TempDir(), "parameters.json")
	if err := os.WriteFile(path, []byte(`{"baseUrl":"https://from-file.example.test"}`), 0o600); err != nil {
		t.Fatalf("write the parameters file: %v", err)
	}

	fromFile, _ := optionsRequest(t, "", "--parameters", "@"+path)["parameters"].(map[string]any)
	if fromFile["baseUrl"] != "https://from-file.example.test" {
		t.Errorf("--parameters @file sent %v, want the file's object", fromFile)
	}

	fromStdin, _ := optionsRequest(t, `{"baseUrl":"https://from-stdin.example.test"}`, "--parameters", "-")["parameters"].(map[string]any)
	if fromStdin["baseUrl"] != "https://from-stdin.example.test" {
		t.Errorf("--parameters - sent %v, want the object read from stdin", fromStdin)
	}
}

// --param is the narrower, more explicit spelling, so it wins over the same key
// in the object: a script can keep a base document and override one value.
func TestNodeOptionsParamOverridesTheSameKeyOfParameters(t *testing.T) {
	body := optionsRequest(t, "", "--parameters", `{"baseUrl":"https://base.example.test","keep":1}`, "--param", "baseUrl=https://override.example.test")

	parameters, _ := body["parameters"].(map[string]any)
	if parameters["baseUrl"] != "https://override.example.test" || parameters["keep"] != float64(1) {
		t.Fatalf("body.parameters = %v, want --param to override baseUrl and keep the rest", body["parameters"])
	}
}

func TestNodeOptionsOmitsParametersWhenNoneAreGiven(t *testing.T) {
	body := optionsRequest(t, "")

	if _, present := body["parameters"]; present {
		t.Fatalf("body = %v, want no parameters member when none were given", body)
	}
}

func TestNodeOptionsRefusesParametersItCannotSendCleanly(t *testing.T) {
	for name, test := range map[string]struct {
		args []string
		want string
	}{
		"the same --param twice":                      {[]string{"--param", "baseUrl=a", "--param", "baseUrl=b"}, "baseUrl was given 2 times"},
		"a --param with no =":                         {[]string{"--param", "baseUrl"}, "want name=value"},
		"a --param with no name":                      {[]string{"--param", "=value"}, "want name=value"},
		"--parameters that is JSON but not an object": {[]string{"--parameters", `["a"]`}, "--parameters must be a JSON object"},
		"--parameters that is not JSON":               {[]string{"--parameters", `{baseUrl:`}, "--parameters is not valid JSON"},
		"--parameters from a missing file":            {[]string{"--parameters", "@/no/such/file.json"}, "could not read the document"},
	} {
		t.Run(name, func(t *testing.T) {
			api := newRecordingAPI(map[string]http.HandlerFunc{
				apiPrefix + "/node-types/kilasflow.lmChatOpenAi/load-options": jsonBody(http.StatusOK, `{"options":[]}`),
			})
			srv := stubAPI(t, api.routesFor(t))

			full := append([]string{"node", "options", "kilasflow.lmChatOpenAi", "--property", "model", "--url", srv.URL, "--json"}, test.args...)
			code, _, stdout, stderr := runCLI(t, Env{Args: full, Getenv: homeEnv(t.TempDir(), nil)})
			if code != ExitUsage {
				t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
			}
			if !strings.Contains(stdout+stderr, test.want) {
				t.Errorf("the refusal does not say %q (stdout=%q stderr=%q)", test.want, stdout, stderr)
			}
			if len(api.calls) != 0 {
				t.Fatalf("the stub saw %d request(s), want none: a malformed parameter must not reach the server", len(api.calls))
			}
		})
	}
}

// bootOptionsServer serves the real API with one node whose property loads from
// an internal loader that depends on baseUrl and answers with the value it was
// handed. The answer is the proof: a parameter that stopped at the CLI, or at
// the handler, would come back as an empty list.
func bootOptionsServer(t *testing.T) *httptest.Server {
	t.Helper()

	registry := node.NewRegistry()
	if err := registry.Register(node.Definition{
		Type: "test.model", Version: workflow.V(1),
		DisplayName: "Model", Category: "Test", ExecutorID: "test.exec",
		Group:   []node.NodeGroup{node.GroupTransform},
		Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		Parameters: []node.PropertyDefinition{
			{Key: "baseUrl", Label: "Base URL", Kind: node.PropertyString},
			{
				Key: "model", Label: "Model", Kind: node.PropertyString,
				LoadOptions: &node.OptionsLoader{
					Source: property.LoaderInternal, Name: "test.models", DependsOn: []string{"baseUrl"},
				},
			},
		},
	}); err != nil {
		t.Fatalf("Register() error = %v", err)
	}

	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := resolver.RegisterInternal("test.models", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		base := scope.Dependencies["baseUrl"]
		if base == "" {
			return loadoptions.Result{Options: []loadoptions.Option{}, Reason: "set baseUrl first"}, nil
		}

		return loadoptions.Result{Options: []loadoptions.Option{{Label: base, Value: base + "/model-a"}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}

	handler := api.NewServer(api.Deps{
		Config: config.Default(), Logger: slog.New(slog.NewTextHandler(io.Discard, nil)), DB: stubPinger{}, Version: "0.0.0-test",
		NodeRegistry: registry, OptionLoader: resolver,
		CredentialResolverFor: func(repository.TenantScope) loadoptions.CredentialResolver { return nil },
	}).Handler()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)

	return srv
}

// TestNodeOptionsLoadsADependentPropertyThroughTheRealServer proves the whole
// path rather than the wire alone: the flag becomes a request body, the handler
// puts the parameter into the loader's dependency scope, and the loader answers
// from it.
func TestNodeOptionsLoadsADependentPropertyThroughTheRealServer(t *testing.T) {
	srv := bootOptionsServer(t)

	load := func(extra ...string) map[string]any {
		t.Helper()
		args := append([]string{"node", "options", "test.model", "--property", "model", "--url", srv.URL, "--json"}, extra...)
		code, _, stdout, stderr := runCLI(t, Env{Args: args, Getenv: homeEnv(t.TempDir(), nil)})
		if code != ExitOK {
			t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
		}
		data, _ := envelope(t, stdout)["data"].(map[string]any)

		return data
	}

	// Without the parameter the loader has nothing to depend on: this is the
	// failure the ticket reported, and it is still what a caller who forgets
	// the flag gets, with the server's reason.
	empty := load()
	if options, _ := empty["options"].([]any); len(options) != 0 || empty["reason"] != "set baseUrl first" {
		t.Fatalf("without --param: data = %v, want an empty list and the loader's reason", empty)
	}

	for _, extra := range [][]string{
		{"--param", "baseUrl=https://llm.example.test"},
		{"--parameters", `{"baseUrl":"https://llm.example.test"}`},
	} {
		data := load(extra...)
		options, _ := data["options"].([]any)
		if len(options) != 1 {
			t.Fatalf("%v: data = %v, want the one option the loader built from baseUrl", extra, data)
		}
		option, _ := options[0].(map[string]any)
		if option["value"] != "https://llm.example.test/model-a" {
			t.Errorf("%v: option = %v, want a value built from the baseUrl that was sent", extra, option)
		}
	}
}
