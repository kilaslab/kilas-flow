package nodes_test

import (
	"bytes"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// openAICredentialWithURL is a stored openAiApi credential whose `url` field
// points the key at one endpoint, which is also the scope the key is held to.
func openAICredentialWithURL(baseURL string) *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenAI", Type: nodes.OpenAICredentialType,
		Fields: map[string]string{"apiKey": "sk-live-secret", "url": baseURL},
	}}
}

// geminiCredential is a stored googleGeminiApi credential scoped to the host
// the test's fake endpoint runs on.
func geminiCredential() *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "Google Gemini", Type: nodes.GoogleGeminiCredentialType,
		Fields:         map[string]string{"apiKey": "AIza-test-secret"},
		AllowedDomains: []string{"127.0.0.1"},
	}}
}

// appNodeIR builds one app node's compiled node, the way the engine would.
func appNodeIR(t *testing.T, nodeType, credentialType string, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, found := aiRegistry(t).Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	ir := workflow.IRNode{
		ID: "app", Name: "Provider Call", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	if credentialType != "" {
		ir.Credentials = map[string]string{credentialType: "cred-key"}
	}
	return ir
}

// runAppNode runs one provider app node through the registered executor, so a
// test exercises the same binding the engine would.
func runAppNode(t *testing.T, nodeType, credentialType string, parameters map[string]any, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodeType, workflow.V(1))
	return runExecutor(t, executors, definition.ExecutorID,
		appNodeIR(t, nodeType, credentialType, parameters), input, request)
}

// TestProviderAppNodesAreTheirOwnTypesWithTheirOwnCredentials is the parity
// point of the split: an imported workflow names `openAi` and `googleGemini`,
// and each has to find its own node type and its own credential, or it arrives
// as the blocking placeholder the ticket exists to remove.
func TestProviderAppNodesAreTheirOwnTypesWithTheirOwnCredentials(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	for _, want := range []struct {
		nodeType       string
		credentialType string
		baseURL        string
		defaultModel   string
	}{
		{nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, "https://api.openai.com/v1", "gpt-4o-mini"},
		{nodes.GoogleGeminiAppNodeType, nodes.GoogleGeminiCredentialType,
			"https://generativelanguage.googleapis.com/v1beta/openai", "gemini-2.5-flash"},
	} {
		definition, found := registry.Get(want.nodeType, workflow.V(1))
		if !found {
			t.Errorf("node type %q is not registered", want.nodeType)
			continue
		}
		if len(definition.Credentials) != 1 || definition.Credentials[0].Type != want.credentialType ||
			!definition.Credentials[0].Required {
			t.Errorf("%s credentials = %#v, want a required %s", want.nodeType, definition.Credentials, want.credentialType)
		}
		if _, known := credentials.Lookup(want.credentialType); !known {
			t.Errorf("credential type %q is not registered", want.credentialType)
		}
		byKey := map[string]node.PropertyDefinition{}
		for _, parameter := range definition.Parameters {
			byKey[parameter.Key] = parameter
		}
		if got := byKey["baseUrl"].Default; got != want.baseURL {
			t.Errorf("%s baseUrl default = %v, want %q", want.nodeType, got, want.baseURL)
		}
		locator, isLocator := byKey["modelId"].Default.(map[string]any)
		if !isLocator || locator["value"] != want.defaultModel {
			t.Errorf("%s model default = %#v, want a locator naming %q", want.nodeType, byKey["modelId"].Default, want.defaultModel)
		}
		if _, present := byKey["messages"]; !present {
			t.Errorf("%s declares no messages parameter", want.nodeType)
		}
		if _, present := byKey["jsonOutput"]; !present {
			t.Errorf("%s declares no jsonOutput parameter", want.nodeType)
		}
		// The options collection carries the same members the chat model
		// sub-nodes offer, so one surface teaches the other.
		options, hasOptions := byKey["options"]
		if !hasOptions {
			t.Errorf("%s declares no options collection", want.nodeType)
			continue
		}
		fields := map[string]bool{}
		for _, field := range options.Fields {
			fields[field.Key] = true
		}
		for _, key := range []string{"temperature", "maxTokens", "topP", "timeout", "maxRetries"} {
			if !fields[key] {
				t.Errorf("%s options is missing %s", want.nodeType, key)
			}
		}
	}
}

// TestProviderAppValidationNamesWhatIsMissing: the save-time refusals a user
// can act on — the credential, the model, the operation pairing — rather than
// a run-time failure on the first item.
func TestProviderAppValidationNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	openAI, _ := registry.Lookup(nodes.OpenAIAppNodeType, workflow.V(1))
	gemini, _ := registry.Lookup(nodes.GoogleGeminiAppNodeType, workflow.V(1))
	attached := map[string]string{nodes.OpenAICredentialType: "cred-key"}
	geminiAttached := map[string]string{nodes.GoogleGeminiCredentialType: "cred-key"}

	if err := openAI.Validate(workflow.Node{Parameters: map[string]any{"modelId": modelLocator("gpt-4o-mini")}}); err == nil {
		t.Error("the OpenAI app node was accepted with no credential")
	}
	if err := openAI.Validate(workflow.Node{
		Credentials: attached, Parameters: map[string]any{"model": map[string]any{}},
	}); err == nil || !strings.Contains(err.Error(), "modelId is required") {
		t.Errorf("Validate() = %v, want a missing-model refusal", err)
	}
	if err := openAI.Validate(workflow.Node{
		Credentials: attached,
		Parameters: map[string]any{
			"modelId": modelLocator("gpt-4o-mini"), "resource": "image", "operation": "message",
		},
	}); err == nil || !strings.Contains(err.Error(), "image/message") {
		t.Errorf("Validate() = %v, want the operation pairing refused", err)
	}
	if err := openAI.Validate(workflow.Node{
		Credentials: attached,
		Parameters:  map[string]any{"modelId": modelLocator("gpt-4o-mini"), "resource": "text"},
	}); err == nil || !strings.Contains(err.Error(), "at least one message") {
		t.Errorf("Validate() = %v, want an empty message list refused", err)
	}
	if err := openAI.Validate(workflow.Node{
		Credentials: attached,
		Parameters: map[string]any{
			"modelId": modelLocator("gpt-4o-mini"), "resource": "text",
			"messages": map[string]any{"messageValues": []any{
				map[string]any{"role": "narrator", "content": "once upon a time"},
			}},
		},
	}); err == nil || !strings.Contains(err.Error(), "message 1") {
		t.Errorf("Validate() = %v, want the unknown role named", err)
	}
	if err := gemini.Validate(workflow.Node{
		Credentials: geminiAttached,
		Parameters: map[string]any{
			"modelId": modelLocator("gemini-2.5-flash"), "resource": "image",
		},
	}); err == nil || !strings.Contains(err.Error(), "image") {
		t.Errorf("Validate() = %v, want the image resource refused on the Gemini node", err)
	}
	if err := gemini.Validate(workflow.Node{
		Credentials: geminiAttached,
		Parameters: map[string]any{
			"modelId": modelLocator("gemini-2.5-flash"),
			"messages": map[string]any{"messageValues": []any{
				map[string]any{"role": "user", "content": "hello"},
			}},
		},
	}); err != nil {
		t.Errorf("Validate() = %v, want a configured Gemini node accepted", err)
	}
}

// TestOpenAIAppMessagesAModelThroughTheCompletionsEndpoint pins the request
// the text/message operation makes and the item it answers with.
func TestOpenAIAppMessagesAModelThroughTheCompletionsEndpoint(t *testing.T) {
	t.Parallel()

	var bodies []map[string]any
	var paths []string
	var auth []string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		auth = append(auth, r.Header.Get("Authorization"))
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		bodies = append(bodies, decoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"42"},"finish_reason":"stop"}],"usage":{"prompt_tokens":3,"completion_tokens":2,"total_tokens":5}}`))
	}))
	defer provider.Close()

	output, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, map[string]any{
		"model":   modelLocator("gpt-4o-mini"),
		"baseUrl": provider.URL,
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "system", "content": "You are terse."},
			map[string]any{"role": "user", "content": map[string]any{"mode": "expression", "value": "{{ $json.question }}"}},
		}},
		"options": map[string]any{"temperature": float64(0)},
	}, workflow.NodeInput{"main": {
		{JSON: map[string]any{"question": "What is 6x7?"}},
		{JSON: map[string]any{"question": "Again?"}},
	}}, engine.Request{Credentials: openAICredentialWithURL(provider.URL)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output[0]) != 2 || len(bodies) != 2 {
		t.Fatalf("output items = %d, provider calls = %d, want one model call per item", len(output[0]), len(bodies))
	}
	if paths[0] != "POST /chat/completions" {
		t.Fatalf("the model was called at %q, want POST /chat/completions", paths[0])
	}
	if auth[0] != "Bearer sk-live-secret" {
		t.Fatalf("Authorization = %q, want the credential's key as a bearer token", auth[0])
	}
	messages := promptContents(t, bodies[0])
	if len(messages) != 2 ||
		messages[0]["role"] != "system" || messages[0]["content"] != "You are terse." ||
		messages[1]["role"] != "user" || messages[1]["content"] != "What is 6x7?" {
		t.Fatalf("messages = %#v, want the stored rows in order with the expression resolved", messages)
	}
	if bodies[0]["model"] != "gpt-4o-mini" {
		t.Fatalf("model = %#v, want the node's model", bodies[0]["model"])
	}
	if bodies[0]["temperature"] != float64(0) {
		t.Fatalf("temperature = %#v, want the option the user set, zero included", bodies[0]["temperature"])
	}
	if _, sent := bodies[0]["response_format"]; sent {
		t.Fatalf("body = %#v, want no response_format unless JSON output was asked for", bodies[0])
	}
	item := output[0][0].JSON
	if item["content"] != "42" || item["role"] != "assistant" || item["model"] != "gpt-4o-mini" {
		t.Fatalf("output = %#v, want the model's answer under n8n's simplified keys", item)
	}
	if item["finishReason"] != "stop" {
		t.Errorf("finishReason = %#v, want the provider's", item["finishReason"])
	}
	usage, isUsage := item["usage"].(map[string]any)
	if !isUsage || usage["totalTokens"] != float64(5) {
		t.Errorf("usage = %#v, want the provider's accounting", item["usage"])
	}
	if second := promptContents(t, bodies[1]); second[1]["content"] != "Again?" {
		t.Errorf("second item's prompt = %#v, want its own expression resolved", second)
	}
}

// TestOpenAIAppJSONOutputAsksForAndParsesAnObject: the JSON ask has to reach
// the provider as a response format and as words (a provider refuses the
// format when the messages never mention JSON), and the parsed object has to
// land under `output`, where every structured answer in this product lands.
func TestOpenAIAppJSONOutputAsksForAndParsesAnObject(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = map[string]any{}
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"total\":3}"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	output, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, map[string]any{
		"model":      modelLocator("gpt-4o-mini"),
		"baseUrl":    provider.URL,
		"jsonOutput": true,
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "user", "content": "How many?"},
		}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: openAICredentialWithURL(provider.URL)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if format, sent := received["response_format"].(map[string]any); !sent || format["type"] != "json_object" {
		t.Fatalf("response_format = %#v, want the JSON object ask", received["response_format"])
	}
	messages := promptContents(t, received)
	if len(messages) != 2 || messages[0]["role"] != "system" ||
		!strings.Contains(messages[0]["content"].(string), "JSON") {
		t.Fatalf("messages = %#v, want the JSON ask stated in words before the rows", messages)
	}
	parsed, isObject := output[0][0].JSON["output"].(map[string]any)
	if !isObject || parsed["total"] != float64(3) {
		t.Fatalf("output = %#v, want the parsed JSON object under output", output[0][0].JSON)
	}
	if output[0][0].JSON["content"] != `{"total":3}` {
		t.Errorf("content = %#v, want the raw text kept beside the parsed object", output[0][0].JSON["content"])
	}
}

// TestOpenAIAppFallsBackToItsCredentialsBaseURL: a credential pointed at an
// OpenAI-compatible endpoint reaches the app node too, exactly as it reaches
// the chat model sub-node, without the node naming the address itself.
func TestOpenAIAppFallsBackToItsCredentialsBaseURL(t *testing.T) {
	t.Parallel()

	called := false
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		called = true
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hi"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	// baseUrl is left at the definition's default, which is the provider's
	// own address; the credential's url field is what actually routes the
	// call, and its host scope is what the key is held to.
	_, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, map[string]any{
		"modelId": modelLocator("gpt-4o-mini"),
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "user", "content": "hi"},
		}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: openAICredentialWithURL(provider.URL)})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if !called {
		t.Fatal("the credential's base URL was never called")
	}
}

// TestOpenAIAppAnalyzesAnImageFromBinary: the picture travels as the same
// data-URI part the agent and chain send, beside the prompt, in one
// completion whose answer is the item's content.
func TestOpenAIAppAnalyzesAnImageFromBinary(t *testing.T) {
	t.Parallel()

	var received map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		received = map[string]any{}
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"A red bicycle."},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	store := binaryStore(t)
	reference, err := store.Put("photo", "image/png", bytes.NewReader([]byte{0x89, 'P', 'N', 'G', 0x0d, 0x0a, 0x1a, 0x0a, 0x00}))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}

	parameters := map[string]any{
		"model":    modelLocator("gpt-4o-mini"),
		"baseUrl":  provider.URL,
		"resource": "image", "text": "What is in this picture?",
	}
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": reference}}}}
	output, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters, input,
		engine.Request{Credentials: openAICredentialWithURL(provider.URL), Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["content"] != "A red bicycle." {
		t.Fatalf("output = %#v, want the description as the content", output[0][0].JSON)
	}
	messages := promptContents(t, received)
	if len(messages) != 1 {
		t.Fatalf("messages = %#v, want one user turn", messages)
	}
	parts, isList := messages[0]["content"].([]any)
	if !isList || len(parts) != 2 {
		t.Fatalf("content = %#v, want a text part and an image part", messages[0]["content"])
	}
	text, _ := parts[0].(map[string]any)
	image, _ := parts[1].(map[string]any)
	imageURL, _ := image["image_url"].(map[string]any)
	if text["type"] != "text" || text["text"] != "What is in this picture?" {
		t.Errorf("text part = %#v, want the prompt", parts[0])
	}
	if image["type"] != "image_url" || !strings.HasPrefix(imageURL["url"].(string), "data:image/png;base64,") {
		t.Errorf("image part = %#v, want the binary as a data URI", parts[1])
	}

	// The failures a user can act on, named rather than discovered.
	_, err = runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: openAICredentialWithURL(provider.URL), Binaries: store})
	if err == nil || !strings.Contains(err.Error(), `binary "data"`) {
		t.Fatalf("Execute() = %v, want the missing binary named", err)
	}
	other, err := store.Put("note", "text/plain", strings.NewReader("not a picture"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	_, err = runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters,
		workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": other}}}},
		engine.Request{Credentials: openAICredentialWithURL(provider.URL), Binaries: store})
	if err == nil || !strings.Contains(err.Error(), "rather than an image") {
		t.Fatalf("Execute() = %v, want the non-image binary refused", err)
	}
}

// TestOpenAIAppTranscribesAudio: the multipart upload the transcription
// endpoint expects — a named file part, the model, the format — and the text
// it answers with, under the field n8n's own node leaves the item.
func TestOpenAIAppTranscribesAudio(t *testing.T) {
	t.Parallel()

	var paths []string
	var fileNames []string
	var fileBody []byte
	var fields []map[string]string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.Method+" "+r.URL.Path)
		if err := r.ParseMultipartForm(32 << 20); err != nil {
			t.Errorf("ParseMultipartForm() error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		entry := map[string]string{"model": r.FormValue("model"), "response_format": r.FormValue("response_format"), "language": r.FormValue("language")}
		fields = append(fields, entry)
		file, header, err := r.FormFile("file")
		if err != nil {
			t.Errorf("FormFile() error = %v", err)
			w.WriteHeader(http.StatusBadRequest)
			return
		}
		defer file.Close()
		fileNames = append(fileNames, header.Filename)
		fileBody, _ = io.ReadAll(file)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"text":"four two"}`))
	}))
	defer provider.Close()

	store := binaryStore(t)
	reference, err := store.Put("audio", "audio/mpeg", strings.NewReader("42"))
	if err != nil {
		t.Fatalf("Put() error = %v", err)
	}
	reference.FileName = "answer.mp3"

	parameters := map[string]any{
		"modelId": modelLocator("whisper-1"), "baseUrl": provider.URL,
		"resource": "audio", "language": "en",
	}
	input := workflow.NodeInput{"main": {{JSON: map[string]any{}, Binary: map[string]workflow.BinaryRef{"data": reference}}}}
	output, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters, input,
		engine.Request{Credentials: openAICredentialWithURL(provider.URL), Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["text"] != "four two" {
		t.Fatalf("output = %#v, want the transcription under n8n's key", output[0][0].JSON)
	}
	if len(paths) != 1 || paths[0] != "POST /audio/transcriptions" {
		t.Fatalf("called %v, want one POST to the transcription endpoint", paths)
	}
	if len(fields) != 1 || fields[0]["model"] != "whisper-1" || fields[0]["response_format"] != "json" || fields[0]["language"] != "en" {
		t.Fatalf("form fields = %#v, want the model, the JSON format and the language", fields)
	}
	if len(fileNames) != 1 || fileNames[0] != "answer.mp3" || string(fileBody) != "42" {
		t.Fatalf("file part = %q / %q, want the binary under its own file name", fileNames, fileBody)
	}

	// A provider refusal carries the status, and never the API key.
	failing := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"error":"upstream exploded"}`))
	}))
	defer failing.Close()
	_, err = runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType,
		map[string]any{
			"modelId": modelLocator("whisper-1"), "baseUrl": failing.URL, "resource": "audio",
		}, input, engine.Request{Credentials: openAICredentialWithURL(failing.URL), Binaries: store})
	if err == nil || !strings.Contains(err.Error(), "status 500") || strings.Contains(err.Error(), "sk-live-secret") {
		t.Fatalf("Execute() = %v, want the status reported without the key", err)
	}
}

// TestGoogleGeminiAppRunsThroughTheOpenAICompatibleEndpoint: the Gemini node's
// model call goes to /chat/completions on the compatibility endpoint's base
// URL, with the `models/` listing prefix stripped, and the JSON ask works the
// same way it does on the OpenAI node.
func TestGoogleGeminiAppRunsThroughTheOpenAICompatibleEndpoint(t *testing.T) {
	t.Parallel()

	var received map[string]any
	var auth string
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		auth = r.Header.Get("Authorization")
		body, _ := io.ReadAll(r.Body)
		received = map[string]any{}
		_ = json.Unmarshal(body, &received)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"mood\":\"sunny\"}"},"finish_reason":"stop"}],"usage":{"total_tokens":9}}`))
	}))
	defer provider.Close()

	output, err := runAppNode(t, nodes.GoogleGeminiAppNodeType, nodes.GoogleGeminiCredentialType, map[string]any{
		// The locator carries the catalogue's own spelling, `models/<id>`.
		"model":      modelLocator("models/gemini-2.5-flash"),
		"baseUrl":    provider.URL,
		"jsonOutput": true,
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "user", "content": "What is the mood today?"},
		}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: geminiCredential()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if received["model"] != "gemini-2.5-flash" {
		t.Fatalf("model = %#v, want the listing prefix stripped", received["model"])
	}
	if format, sent := received["response_format"].(map[string]any); !sent || format["type"] != "json_object" {
		t.Fatalf("response_format = %#v, want the JSON object ask", received["response_format"])
	}
	if auth != "Bearer AIza-test-secret" {
		t.Fatalf("Authorization = %q, want the Gemini key as a bearer token", auth)
	}
	parsed, isObject := output[0][0].JSON["output"].(map[string]any)
	if !isObject || parsed["mood"] != "sunny" {
		t.Fatalf("output = %#v, want the parsed object under output", output[0][0].JSON)
	}
}

// TestProviderAppNodesRefuseTheWrongCredentialAndTheWrongHost: the app node
// authenticates with its provider's own credential type, held to its own
// domain scope — a bearer token of another kind or a key aimed at another
// host is refused before anything is sent.
func TestProviderAppNodesRefuseTheWrongCredentialAndTheWrongHost(t *testing.T) {
	t.Parallel()

	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		t.Error("the provider was reached with a credential that should have been refused")
		w.WriteHeader(http.StatusUnauthorized)
	}))
	defer provider.Close()

	parameters := map[string]any{
		"modelId": modelLocator("gpt-4o-mini"), "baseUrl": provider.URL,
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "user", "content": "hi"},
		}},
	}
	_, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: bearerResolver()})
	if err == nil || !strings.Contains(err.Error(), "credential") {
		t.Fatalf("Execute() = %v, want a wrong-type credential refused", err)
	}

	scoped := &stubCredentials{credential: engine.Credential{
		ID: "cred-key", Name: "OpenAI", Type: nodes.OpenAICredentialType,
		Fields:         map[string]string{"apiKey": "sk-live-secret"},
		AllowedDomains: []string{"api.openai.com"},
	}}
	_, err = runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, parameters,
		workflow.NodeInput{"main": {{JSON: map[string]any{}}}},
		engine.Request{Credentials: scoped})
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Fatalf("Execute() = %v, want the host scope enforced", err)
	}
}

// TestOpenAIAppModelErrorsFailTheNode: the adapter's own status error reaches
// the caller wrapped with the node's name, and the model events the chain
// publishes reach the feed from an app node too.
func TestOpenAIAppModelErrorsFailTheNode(t *testing.T) {
	t.Parallel()

	attempts := 0
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		attempts++
		w.WriteHeader(http.StatusTooManyRequests)
		_, _ = w.Write([]byte(`{"error":"slow down"}`))
	}))
	defer provider.Close()

	var published []string
	_, err := runAppNode(t, nodes.OpenAIAppNodeType, nodes.OpenAICredentialType, map[string]any{
		"modelId": modelLocator("gpt-4o-mini"), "baseUrl": provider.URL,
		"messages": map[string]any{"messageValues": []any{
			map[string]any{"role": "user", "content": "hi"},
		}},
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{
		Credentials: openAICredentialWithURL(provider.URL),
		Events:      func(event engine.NodeEvent) { published = append(published, event.Name) },
	})
	if err == nil || !strings.Contains(err.Error(), "status 429") {
		t.Fatalf("Execute() = %v, want the provider's status reported", err)
	}
	if attempts != 3 {
		t.Fatalf("attempts = %d, want the default two retries", attempts)
	}
	if !containsString(published, string(ai.EventModelStarted)) ||
		!containsString(published, string(ai.EventAgentFailed)) {
		t.Fatalf("events = %v, want the model start and failure published", published)
	}
}
