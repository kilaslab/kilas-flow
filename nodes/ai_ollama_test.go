package nodes_test

// The AI paths in this product were verifiable only with somebody's funded API
// key and a working internet connection, which is a suite that runs on one
// machine. This file runs them against a model on the developer's own hardware
// instead.
//
// It is opt-in. Set KILASFLOW_TEST_OLLAMA_BASE_URL to the OpenAI-compatible
// address Ollama serves and run:
//
//	ollama serve
//	ollama pull gemma4:12b-mlx
//	KILASFLOW_TEST_OLLAMA_BASE_URL=http://127.0.0.1:11434/v1 go test ./nodes/ -run Ollama -count=1
//
// The model costs roughly 7.7 GB on disk and several gigabytes resident while
// it answers, which is why nothing here runs in parallel: the constraint is the
// laptop also running an editor, a browser and a database, not the CPU. The
// first request after a load is far slower than the ones after it, so the
// fixture warms the model before anything is asserted — a per-test timeout
// tuned to warm performance would otherwise fail on the first test only.
//
// The pinned tag is an Apple MLX build and therefore Apple-Silicon-only; a
// linux/amd64 runner cannot pull it and will skip. Point
// KILASFLOW_TEST_OLLAMA_MODEL at a portable tag to run elsewhere — which is a
// visible configuration change rather than a silent behavioural difference,
// deliberately, because these assertions are calibrated against the pinned tag.
//
// What this proves: the node, the credential, the options loader, the agent
// loop, the tool invocation and the egress policy all work against a real
// OpenAI-compatible server. What it does not prove: anything about the quality
// of a model a customer will actually use. No local suite can, so a green run
// here is a claim about wiring and nothing more — which is also why every
// assertion below is on structure rather than on the words the model chose.

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
	"github.com/kilaslabs/kilas-flow/nodes"
)

const (
	ollamaBaseURLEnv = "KILASFLOW_TEST_OLLAMA_BASE_URL"
	ollamaModelEnv   = "KILASFLOW_TEST_OLLAMA_MODEL"
	// ollamaPinnedModel is the tag these assertions were calibrated against.
	// Naming it here rather than reading a bare environment variable means a
	// run against a different model is a change somebody made on purpose.
	ollamaPinnedModel = "gemma4:12b-mlx"
	// ollamaBudget is generous because a cold 12B model is slow and a suite
	// that fails on the first request teaches the team to rerun rather than to
	// read. The generic chat model node exposes no per-node timeout, so this
	// is what the agent falls back to for one run.
	ollamaBudget = 5 * time.Minute
)

// localModel is a reachable Ollama and the policy that admits it.
type localModel struct {
	baseURL string
	model   string
	policy  safehttp.Policy
}

// requireOllama resolves the local runtime or skips, saying exactly what to run.
//
// Every reason to skip is a machine-shaped one — no server, no model, an
// architecture that cannot serve this build — so a developer without the setup
// gets an instruction rather than a red suite they did not cause.
func requireOllama(t *testing.T) localModel {
	t.Helper()

	baseURL := strings.TrimSpace(os.Getenv(ollamaBaseURLEnv))
	if baseURL == "" {
		t.Skipf("set %s=http://127.0.0.1:11434/v1 to run against a local Ollama "+
			"(`ollama serve`, then `ollama pull %s` — about 7.7 GB on disk)",
			ollamaBaseURLEnv, ollamaPinnedModel)
	}
	wanted := strings.TrimSpace(os.Getenv(ollamaModelEnv))
	if wanted == "" {
		wanted = ollamaPinnedModel
	}

	runtime := localModel{baseURL: strings.TrimRight(baseURL, "/"), model: wanted}
	runtime.policy = privateEndpointPolicy(t, runtime.baseURL)

	// The catalogue doubles as the reachability check, so an unreachable server
	// and a missing model produce the same kind of instruction.
	catalogue, err := ollamaModels(t, runtime)
	if errors.Is(err, safehttp.ErrBlocked) {
		// Never a skip. The endpoint is named in the policy, so a refusal here
		// is the egress guard losing the ability to make an exception — the
		// exact regression this suite is placed to catch, and one that skipping
		// would hide behind a message about the developer's machine.
		t.Fatalf("the policy names %s and the guard refused it anyway: %v", runtime.baseURL, err)
	}
	if err != nil {
		t.Skipf("%s is set to %s but nothing answered there: %v (is `ollama serve` running?)",
			ollamaBaseURLEnv, runtime.baseURL, err)
	}
	if !containsString(catalogue, wanted) {
		t.Skipf("%s does not serve %q — run `ollama pull %s`. "+
			"The pinned tag is an Apple MLX build, so a non-Apple-Silicon machine has to set %s to a portable tag. "+
			"It offers: %s",
			runtime.baseURL, wanted, wanted, ollamaModelEnv, strings.Join(catalogue, ", "))
	}

	warmOllama(t, runtime)
	return runtime
}

// privateEndpointPolicy is the deployment's real posture plus one written-down
// endpoint. Private networks stay refused, which is what lets this suite catch
// a regression in the guard instead of running around it.
func privateEndpointPolicy(t *testing.T, endpoints ...string) safehttp.Policy {
	t.Helper()
	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks, which would make this suite prove nothing")
	}
	policy.Timeout = ollamaBudget
	for _, raw := range endpoints {
		policy.AllowedPrivateEndpoints = append(policy.AllowedPrivateEndpoints, hostPortOf(t, raw))
	}
	return policy
}

// hostPortOf is the `host:port` an allowance has to name for one URL. A URL
// that names no port is dialled on the scheme's own, so the entry says which.
func hostPortOf(t *testing.T, raw string) string {
	t.Helper()
	parsed, err := url.Parse(raw)
	if err != nil {
		t.Fatalf("parse %q: %v", raw, err)
	}
	if parsed.Port() != "" {
		return parsed.Host
	}
	if parsed.Scheme == "https" {
		return parsed.Hostname() + ":443"
	}
	return parsed.Hostname() + ":80"
}

// ollamaModels reads the catalogue through the policy client, so a refusal by
// the egress guard fails here rather than three layers deeper.
func ollamaModels(t *testing.T, runtime localModel) ([]string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	request, err := http.NewRequestWithContext(ctx, http.MethodGet, runtime.baseURL+"/models", nil)
	if err != nil {
		return nil, err
	}
	response, err := safehttp.NewClient(runtime.policy).Do(request)
	if err != nil {
		return nil, err
	}
	defer response.Body.Close()

	var payload struct {
		Data []struct {
			ID string `json:"id"`
		} `json:"data"`
	}
	if err := json.NewDecoder(response.Body).Decode(&payload); err != nil {
		return nil, err
	}
	names := make([]string, 0, len(payload.Data))
	for _, entry := range payload.Data {
		names = append(names, entry.ID)
	}
	return names, nil
}

// warmOllama pays the load cost once, before anything is timed or asserted.
func warmOllama(t *testing.T, runtime localModel) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), ollamaBudget)
	defer cancel()

	body, err := json.Marshal(map[string]any{
		"model":       runtime.model,
		"temperature": 0,
		"stream":      false,
		"max_tokens":  1,
		"messages":    []map[string]string{{"role": "user", "content": "hi"}},
	})
	if err != nil {
		t.Fatalf("encode warm-up: %v", err)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodPost,
		runtime.baseURL+"/chat/completions", bytes.NewReader(body))
	if err != nil {
		t.Fatalf("build warm-up: %v", err)
	}
	request.Header.Set("Content-Type", "application/json")

	response, err := safehttp.NewClient(runtime.policy).Do(request)
	if errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("the policy names %s and the guard refused it anyway: %v", runtime.baseURL, err)
	}
	if err != nil {
		t.Skipf("%s would not answer a completion: %v", runtime.baseURL, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		t.Skipf("%s answered %d loading %q", runtime.baseURL, response.StatusCode, runtime.model)
	}
}

// unauthenticatedBearer is the credential a local model server needs: one that
// exists, holding nothing. The node requires an httpBearerAuth credential, and
// an empty token means no Authorization header is sent at all — so this suite
// runs with no third-party API key anywhere in the environment.
func unauthenticatedBearer() *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-local", Name: "Local Ollama", Type: nodes.BearerCredentialType,
		Fields: map[string]string{"token": ""},
	}}
}

// bearerRecord answers the options loader with the same empty credential.
type bearerRecord struct{}

func (bearerRecord) Resolve(_ context.Context, _ string) (credentials.Record, map[string]string, error) {
	return credentials.Record{ID: "cred-local", Name: "Local Ollama", Type: nodes.BearerCredentialType},
		map[string]string{"token": ""}, nil
}

// TestOllamaFillsTheModelPickerFromItsOwnCatalogue proves the dynamic options
// loader works against something other than OpenAI. The picker exists so a typo
// in a model name is caught while the node is being configured rather than on
// the run that needed it, and a picker that only ever worked against one vendor
// would not have delivered that.
func TestOllamaFillsTheModelPickerFromItsOwnCatalogue(t *testing.T) {
	runtime := requireOllama(t)

	definition, found := aiRegistry(t).Get(nodes.ChatModelNodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodes.ChatModelNodeType)
	}
	var loader *property.OptionsLoader
	for _, parameter := range definition.Parameters {
		if parameter.Key == "model" {
			loader = parameter.LoadOptions
		}
	}
	if loader == nil {
		t.Fatal("the chat model node's model parameter has no options loader")
	}

	// The registered definition's own loader, not a hand-written one, so this
	// cannot pass while the editor's picker is broken.
	resolver := loadoptions.NewResolver(runtime.policy, time.Second)
	result, err := resolver.Load(context.Background(), *loader,
		loadoptions.Scope{Dependencies: map[string]string{"baseUrl": runtime.baseURL}},
		"cred-local", bearerRecord{})
	if err != nil {
		t.Fatalf("Load() error = %v, want the local catalogue", err)
	}
	if len(result.Options) == 0 {
		t.Fatalf("the picker offered nothing (%s)", result.Reason)
	}
	values := make([]string, 0, len(result.Options))
	for _, option := range result.Options {
		values = append(values, option.Value)
	}
	if !containsString(values, runtime.model) {
		t.Errorf("the picker offered %v, want %q among them", values, runtime.model)
	}
}

// TestOllamaAnswersAnAgentThroughOneAllowedPrivateEndpoint is the end-to-end
// claim: a chat model node pointed at a model on this machine completes a run
// with the SSRF guard in its production posture and no API key anywhere.
func TestOllamaAnswersAnAgentThroughOneAllowedPrivateEndpoint(t *testing.T) {
	runtime := requireOllama(t)

	resolver := unauthenticatedBearer()
	descriptor := runLocalChatModel(t, runtime, resolver, nil)
	agent := nodes.NewAgentExecutor(ai.NewLoopRuntime(), runtime.policy, nil)

	output, err := runAgentWith(t, agent, descriptor, resolver)
	if err != nil {
		t.Fatalf("Execute() error = %v, want a completed run", err)
	}
	// Structure, never wording: an assertion on the sentence a model chose
	// fails on the next model update and teaches the team to distrust the suite.
	if len(output) == 0 || len(output[0]) != 1 {
		t.Fatalf("agent output = %#v, want one item", output)
	}
	reply, isText := output[0][0].JSON["output"].(string)
	if !isText || strings.TrimSpace(reply) == "" {
		t.Errorf("agent output = %#v, want a non-empty reply", output[0][0].JSON)
	}
}

// TestOllamaEmitsOpenAIFormatToolCalls is the load-bearing capability check.
// The agent suites are built on the assumption that this model calls tools in
// OpenAI's format; a model that chats well but never emits a tool call would
// make them unbuildable, and finding that out here is much cheaper than finding
// it out one ticket later.
func TestOllamaEmitsOpenAIFormatToolCalls(t *testing.T) {
	runtime := requireOllama(t)

	var toolPath string
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		toolPath = r.URL.Path
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"tempC":19}`))
	}))
	defer upstream.Close()

	// Two endpoints, each named. The tool's server is a second private address
	// and gets no allowance from the model's — which is the whole point of the
	// list naming one host and port at a time.
	policy := privateEndpointPolicy(t, runtime.baseURL, upstream.URL)
	withTool := localModel{baseURL: runtime.baseURL, model: runtime.model, policy: policy}

	resolver := unauthenticatedBearer()
	descriptor := runLocalChatModel(t, withTool, resolver, nil)
	agent := nodes.NewAgentExecutor(ai.NewLoopRuntime(), policy, nil)

	definition, _ := aiRegistry(t).Lookup(nodes.AgentNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "agent", Name: "AI Agent", Type: nodes.AgentNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"prompt": "What is the weather in Utrecht? Use the get_weather tool to find out.",
		},
		Definition: definition,
	}
	output, err := agent.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {descriptor},
		"tools": {{JSON: map[string]any{"$ai": map[string]any{
			"kind": "tool", "name": "get_weather", "nodeName": "Weather",
			"description": "Look up the current temperature for a city. Takes a city name.",
			"parameters": map[string]any{
				"method": "GET",
				"url":    map[string]any{"mode": "expression", "value": upstream.URL + "/weather/{{ $json.city }}"},
			},
			"credentials": map[string]any{},
		}}}},
	}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("Execute() error = %v, want a completed run", err)
	}
	calls, counted := output[0][0].JSON["toolCalls"].(float64)
	if !counted || calls < 1 {
		t.Fatalf("toolCalls = %#v, want the model to have called the tool at least once", output[0][0].JSON["toolCalls"])
	}
	if toolPath == "" {
		t.Error("the tool's own server was never reached, so no tool call ran end to end")
	}
}

// runLocalChatModel runs the OpenAI-compatible chat model node against the
// local runtime and returns the descriptor it hands the agent.
//
// Temperature is pinned to zero for consistency of behaviour — not of text,
// which is why nothing here asserts on wording — and streaming is off so the
// assertions are about the model rather than about transport framing.
func runLocalChatModel(t *testing.T, runtime localModel, resolver *stubCredentials, extra map[string]any) workflow.Item {
	t.Helper()

	parameters := map[string]any{
		"model": runtime.model, "baseUrl": runtime.baseURL,
		"temperature": float64(0), "stream": false,
	}
	for key, value := range extra {
		parameters[key] = value
	}

	definition, found := aiRegistry(t).Lookup(nodes.ChatModelNodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodes.ChatModelNodeType)
	}
	ir := workflow.IRNode{
		ID: "model", Name: "Chat Model", Type: nodes.ChatModelNodeType, TypeVersion: workflow.V(1),
		Parameters:  parameters,
		Credentials: map[string]string{nodes.BearerCredentialType: "cred-local"},
		Definition:  definition,
	}
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, runtime.policy, sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	output, err := runExecutor(t, executors, nodes.ChatModelExecutorID, ir,
		workflow.NodeInput{}, engine.Request{Credentials: resolver})
	if err != nil {
		t.Fatalf("chat model node error = %v", err)
	}
	if len(output) == 0 || len(output[0]) != 1 {
		t.Fatalf("chat model emitted %#v, want one descriptor item", output)
	}
	return output[0][0]
}
