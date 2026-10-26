package nodes_test

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// runChainKind runs one root chain through its registered executor, the way
// the engine would, with a fake model on the model port.
func runChainKind(t *testing.T, nodeType string, parameters map[string]any, providerURL string, items ...workflow.Item) (workflow.NodeOutput, error) {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	registry := aiRegistry(t)
	definition, found := registry.Lookup(nodeType, workflow.V(1))
	if !found {
		t.Fatalf("node type %q is not registered", nodeType)
	}
	// The compiler resolves a PortsFor definition against the node's own
	// parameters before the runner sees it; a test that skips this gives the
	// classifier its placeholder port list and the run a wrong arity.
	if definition.PortsFor != nil {
		inputs, outputs := definition.PortsFor(parameters, workflow.V(1))
		definition.Inputs, definition.Outputs = inputs, outputs
	}
	ir := workflow.IRNode{
		ID: "chain", Name: "Chain", Type: nodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
	}
	input := workflow.NodeInput{
		"main":  items,
		"model": {chainModelDescriptor(t, providerURL)},
	}
	executor, found := executors.Lookup(definition.ExecutorID)
	if !found {
		t.Fatalf("executor %q is not registered", definition.ExecutorID)
	}
	return executor.Execute(context.Background(), ir, input, engine.Request{Credentials: bearerResolver()})
}

// chainModelDescriptor builds the model-port item every chain kind reads.
func chainModelDescriptor(t *testing.T, providerURL string) workflow.Item {
	t.Helper()
	if providerURL == "" {
		t.Fatal("a chain test needs a fake provider to point the model at")
	}
	return workflow.Item{JSON: map[string]any{"$ai": map[string]any{
		"kind": "model", "model": "gpt-test", "baseUrl": providerURL, "credentialId": "cred-key",
	}}}
}

// answerWith is a fake provider that answers every model call with one fixed
// content string while recording each request body.
func answerWith(t *testing.T, bodies *[]map[string]any, content string) *httptest.Server {
	t.Helper()
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		decoded := map[string]any{}
		_ = json.Unmarshal(body, &decoded)
		*bodies = append(*bodies, decoded)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":` + jsonEscape(content) + `},"finish_reason":"stop"}]}`))
	}))
}

func jsonEscape(text string) string {
	encoded, err := json.Marshal(text)
	if err != nil {
		panic(err)
	}
	return string(encoded)
}

// chainRun is one test's run: a provider answering one fixed string, and the
// bodies it received.
type chainRun struct {
	provider *httptest.Server
	bodies   *[]map[string]any
}

func startChainRun(t *testing.T, answer string) *chainRun {
	t.Helper()
	run := &chainRun{bodies: &[]map[string]any{}}
	run.provider = answerWith(t, run.bodies, answer)
	t.Cleanup(run.provider.Close)
	return run
}

// TestRootChainsAreRegisteredWithTheirModelPort is the parity point: each n8n
// chain type finds its own KilasFlow type, with the chat model in its own slot
// the way every model consumer here takes it.
func TestRootChainsAreRegisteredWithTheirModelPort(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	for _, nodeType := range []string{
		nodes.InformationExtractorNodeType, nodes.TextClassifierNodeType,
		nodes.ChainSummarizationNodeType, nodes.ChainRetrievalQaNodeType,
		nodes.SentimentAnalysisNodeType,
	} {
		definition, found := registry.Get(nodeType, workflow.V(1))
		if !found {
			t.Errorf("node type %q is not registered", nodeType)
			continue
		}
		model, hasModel := portByName(definition.Inputs, "model")
		if !hasModel || model.Kind != workflow.ConnectionLanguageModel || !model.Required {
			t.Errorf("%s model input = %#v, want a required language-model slot", nodeType, definition.Inputs)
		}
		if _, hasMain := portByName(definition.Inputs, "main"); !hasMain {
			t.Errorf("%s declares no main input", nodeType)
		}
	}
}

func portByName(ports []workflow.Port, name string) (workflow.Port, bool) {
	for _, port := range ports {
		if port.Name == name {
			return port, true
		}
	}
	return workflow.Port{}, false
}

// TestInformationExtractorStatesTheSchemaAndParsesTheAnswer: the attributes
// become a schema the model is told about up front — not a judge it never saw
// — and the parsed object lands under `output`.
func TestInformationExtractorStatesTheSchemaAndParsesTheAnswer(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, `{"fullName":"Budi Santoso","ageYears":34}`)
	output, err := runChainKind(t, nodes.InformationExtractorNodeType, map[string]any{
		"text": map[string]any{"mode": "expression", "value": "{{ $json.note }}"},
		"attributes": map[string]any{"values": []any{
			map[string]any{"name": "fullName", "type": "string"},
			map[string]any{"name": "ageYears", "type": "number", "description": "Age in whole years."},
		}},
	}, run.provider.URL, workflow.Item{JSON: map[string]any{"note": "Budi Santoso is 34 years old."}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	parsed, isObject := output[0][0].JSON["output"].(map[string]any)
	if !isObject || parsed["fullName"] != "Budi Santoso" || parsed["ageYears"] != float64(34) {
		t.Fatalf("output = %#v, want the extracted object under output", output[0][0].JSON)
	}
	messages := promptContents(t, (*run.bodies)[0])
	if len(messages) != 2 || messages[0]["role"] != "system" {
		t.Fatalf("messages = %#v, want the system turn then the prompt", messages)
	}
	prompt := messages[1]["content"].(string)
	if !strings.Contains(prompt, "fullName") || !strings.Contains(prompt, "ageYears") ||
		!strings.Contains(prompt, "JSON Schema") || !strings.Contains(prompt, "Budi Santoso is 34") {
		t.Fatalf("prompt = %q, want the text and the schema stated to the model", prompt)
	}
}

// TestInformationExtractorRepairsAnAnswerThatBreaksTheSchema: a wrong-shaped
// first answer is fed back to the model by the chain's own repair loop, and
// the repaired answer is the one the run returns.
func TestInformationExtractorRepairsAnAnswerThatBreaksTheSchema(t *testing.T) {
	t.Parallel()

	calls := 0
	var bodies []map[string]any
	provider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Content-Type", "application/json")
			_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"Budi, 34"},"finish_reason":"stop"}]}`))
			return
		}
		bodies = append(bodies, map[string]any{"repair": true})
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"{\"fullName\":\"Budi\"}"},"finish_reason":"stop"}]}`))
	}))
	defer provider.Close()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.InformationExtractorNodeType, workflow.V(1))
	ir := workflow.IRNode{
		ID: "extract", Name: "Extract", Type: nodes.InformationExtractorNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"text": "Budi",
			"attributes": map[string]any{"values": []any{
				map[string]any{"name": "fullName", "type": "string"},
			}},
		},
		Definition: definition,
	}
	executor, _ := executors.Lookup(definition.ExecutorID)
	output, err := executor.Execute(context.Background(), ir, workflow.NodeInput{
		"main":  {{JSON: map[string]any{}}},
		"model": {chainModelDescriptor(t, provider.URL)},
	}, engine.Request{Credentials: bearerResolver()})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if calls != 2 {
		t.Fatalf("calls = %d, want the failed answer repaired in one retry", calls)
	}
	if parsed, _ := output[0][0].JSON["output"].(map[string]any); parsed["fullName"] != "Budi" {
		t.Fatalf("output = %#v, want the repaired object", output[0][0].JSON)
	}
}

// TestInformationExtractorValidationNamesTheRow: an attribute list with no
// rows, a repeated name, or an unknown type is refused at save, naming the
// row.
func TestInformationExtractorValidationNamesTheRow(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.InformationExtractorNodeType, workflow.V(1))
	for name, parameters := range map[string]map[string]any{
		"no rows": {"text": "hi"},
		"repeated name": {"text": "hi", "attributes": map[string]any{"values": []any{
			map[string]any{"name": "a"}, map[string]any{"name": "a"},
		}}},
		"empty name": {"text": "hi", "attributes": map[string]any{"values": []any{
			map[string]any{"name": " "},
		}}},
		"unknown type": {"text": "hi", "attributes": map[string]any{"values": []any{
			map[string]any{"name": "a", "type": "vibe"},
		}}},
	} {
		if err := definition.Validate(workflow.Node{Parameters: parameters}); err == nil {
			t.Errorf("%s: Validate() accepted it", name)
		}
	}
}

// classifierParameters is a three-category classifier used by the tests below.
func classifierParameters(extra map[string]any) map[string]any {
	parameters := map[string]any{
		"inputText": map[string]any{"mode": "expression", "value": "{{ $json.ticket }}"},
		"categories": map[string]any{"values": []any{
			map[string]any{"category": "billing", "description": "Money, invoices, refunds."},
			map[string]any{"category": "question", "description": "Asks how something works."},
			map[string]any{"category": "complaint", "description": "Unhappy about something."},
		}},
	}
	for key, value := range extra {
		parameters[key] = value
	}
	return parameters
}

// TestTextClassifierHasOnePortPerCategoryAndRoutesToIt: the ports come from
// the categories exactly as the Switch's come from its rules, and the model's
// answer sends the item, unchanged, down its category's branch.
func TestTextClassifierHasOnePortPerCategoryAndRoutesToIt(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, `{"classification":"question"}`)
	output, err := runChainKind(t, nodes.TextClassifierNodeType, classifierParameters(nil), run.provider.URL,
		workflow.Item{JSON: map[string]any{"ticket": "How do I export a report?"}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 3 {
		t.Fatalf("output streams = %d, want one per category", len(output))
	}
	if len(output[0]) != 0 || len(output[2]) != 0 {
		t.Fatalf("routing = %d/%d/%d, want the item only on the matching branch",
			len(output[0]), len(output[1]), len(output[2]))
	}
	if routed := output[1][0].JSON; routed["ticket"] != "How do I export a report?" {
		t.Fatalf("routed item = %#v, want the incoming item unchanged", routed)
	}
	prompt := promptContents(t, (*run.bodies)[0])[1]["content"].(string)
	for _, category := range []string{"billing", "question", "complaint"} {
		if !strings.Contains(prompt, category) {
			t.Errorf("prompt = %q, want the category %q stated to the model", prompt, category)
		}
	}

	// The ports the compiler builds are the same list, by name and label.
	definition, _ := aiRegistry(t).Lookup(nodes.TextClassifierNodeType, workflow.V(1))
	inputs, outputs := definition.PortsFor(classifierParameters(nil), workflow.V(1))
	if len(inputs) != 2 || len(outputs) != 3 {
		t.Fatalf("PortsFor = %d inputs, %d outputs, want the model port beside main and one output per category",
			len(inputs), len(outputs))
	}
	if outputs[1].Name != "1" || outputs[1].DisplayName != "question" {
		t.Fatalf("port 1 = %#v, want it named by position and labelled by category", outputs[1])
	}
	_, degraded := definition.PortsFor(map[string]any{"categories": map[string]any{"values": "not a list"}}, workflow.V(1))
	if len(degraded) != 1 {
		t.Fatalf("degraded ports = %#v, want one placeholder while the category set is unreadable", degraded)
	}
}

// TestTextClassifierFallsBackToTheNamedCategory: an author who names a
// fallback gets the unanswerable item routed there instead of a failed run;
// an author who names none gets the failure, named.
func TestTextClassifierFallsBackToTheNamedCategory(t *testing.T) {
	t.Parallel()

	parameters := classifierParameters(map[string]any{
		"options": map[string]any{"fallbackCategory": "question"},
	})
	run := startChainRun(t, "not json at all")
	_, err := runChainKind(t, nodes.TextClassifierNodeType, parameters, run.provider.URL,
		workflow.Item{JSON: map[string]any{"ticket": "asdfgh"}})
	if err != nil {
		t.Fatalf("Execute() error = %v, want the fallback to rescue the run", err)
	}
	if calls := len(*run.bodies); calls != 1+ai.DefaultOutputMaxRetries {
		t.Fatalf("calls = %d, want the repair loop to spend its retries before the fallback routes", calls)
	}

	definition, _ := aiRegistry(t).Lookup(nodes.TextClassifierNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: classifierParameters(map[string]any{
		"options": map[string]any{"fallbackCategory": "nowhere"},
	})}); err == nil || !strings.Contains(err.Error(), "fallback") {
		t.Fatalf("Validate() = %v, want a fallback outside the categories refused", err)
	}
}

// TestTextClassifierFailsNamedWithoutAFallback pins the other half of the
// fallback contract.
func TestTextClassifierFailsNamedWithoutAFallback(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, "not json at all")
	_, err := runChainKind(t, nodes.TextClassifierNodeType, classifierParameters(nil), run.provider.URL,
		workflow.Item{JSON: map[string]any{"ticket": "asdfgh"}})
	if err == nil || !strings.Contains(err.Error(), "did not match the required format") {
		t.Fatalf("Execute() = %v, want the schema failure to fail the run", err)
	}
}

// TestChainSummarizationAnswersOnceForAllItemsInTheTogetherMode: the default
// mode makes exactly one model call over every item's text, which is why the
// node is WholeBatch.
func TestChainSummarizationAnswersOnceForAllItemsInTheTogetherMode(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, "Two invoices and one refund.")
	items := []workflow.Item{
		{JSON: map[string]any{"chatInput": "Invoice 1001 for EUR 12."}},
		{JSON: map[string]any{"chatInput": "Invoice 1002 for EUR 30. Refund of EUR 5."}},
	}
	output, err := runChainKind(t, nodes.ChainSummarizationNodeType, map[string]any{
		"operationMode": nodes.SummaryModeNode,
	}, run.provider.URL, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(*run.bodies) != 1 {
		t.Fatalf("provider calls = %d, want exactly one for the whole batch", len(*run.bodies))
	}
	prompt := promptContents(t, (*run.bodies)[0])[1]["content"].(string)
	if !strings.Contains(prompt, "Invoice 1001") || !strings.Contains(prompt, "Refund of EUR 5") {
		t.Fatalf("prompt = %q, want every item's text in the one call", prompt)
	}
	if len(output[0]) != 1 || output[0][0].JSON["text"] != "Two invoices and one refund." {
		t.Fatalf("output = %#v, want the one summary under the chain's key", output[0])
	}

	// Each-item mode is one call and one item per input.
	perItem := startChainRun(t, "Short.")
	output, err = runChainKind(t, nodes.ChainSummarizationNodeType, map[string]any{
		"operationMode":   nodes.SummaryModePerItem,
		"textToSummarize": map[string]any{"mode": "expression", "value": "{{ $json.chatInput }}"},
	}, perItem.provider.URL, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(*perItem.bodies) != 2 || len(output[0]) != 2 {
		t.Fatalf("calls = %d, items = %d, want one call and one item per input", len(*perItem.bodies), len(output[0]))
	}

	definition, _ := aiRegistry(t).Lookup(nodes.ChainSummarizationNodeType, workflow.V(1))
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"operationMode": "telepathic"}}); err == nil {
		t.Error("Validate() accepted an unknown operation mode")
	}
	empty := startChainRun(t, "never sent")
	_, err = runChainKind(t, nodes.ChainSummarizationNodeType, nil, empty.provider.URL, workflow.Item{JSON: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "text to summarize is empty") {
		t.Fatalf("Execute() = %v, want the empty text named", err)
	}
}

// TestChainRetrievalQaAnswersFromTheItemsReferenceText: the question and the
// context travel in one prompt, the answer is plain text under `text`, and a
// context field of the wrong shape is refused by name.
func TestChainRetrievalQaAnswersFromTheItemsReferenceText(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, "You export a report from Settings, then Reports.")
	output, err := runChainKind(t, nodes.ChainRetrievalQaNodeType, map[string]any{
		"prompt": "How do I export a report?",
	}, run.provider.URL, workflow.Item{JSON: map[string]any{
		"context": []any{"Settings has an Exports tab.", "Reports can be exported as CSV."},
	}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if output[0][0].JSON["text"] != "You export a report from Settings, then Reports." {
		t.Fatalf("output = %#v, want the answer under the chain's key", output[0][0].JSON)
	}
	prompt := promptContents(t, (*run.bodies)[0])[1]["content"].(string)
	if !strings.Contains(prompt, "Reference text:") ||
		!strings.Contains(prompt, "Exports tab") || !strings.Contains(prompt, "How do I export a report?") {
		t.Fatalf("prompt = %q, want the reference text beside the question", prompt)
	}

	_, err = runChainKind(t, nodes.ChainRetrievalQaNodeType, map[string]any{"prompt": "hi"},
		run.provider.URL, workflow.Item{JSON: map[string]any{"context": 41}})
	if err == nil || !strings.Contains(err.Error(), `context field "context"`) {
		t.Fatalf("Execute() = %v, want the misshapen context field named", err)
	}
	_, err = runChainKind(t, nodes.ChainRetrievalQaNodeType, nil, run.provider.URL, workflow.Item{JSON: map[string]any{}})
	if err == nil || !strings.Contains(err.Error(), "question is empty") {
		t.Fatalf("Execute() = %v, want the empty question named", err)
	}
}

// TestSentimentAnalysisAnswersOnTheFixedScale: the three sentiments and a
// score, held to by a schema the model is told about, under `output`.
func TestSentimentAnalysisAnswersOnTheFixedScale(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, `{"sentiment":"positive","score":0.9}`)
	output, err := runChainKind(t, nodes.SentimentAnalysisNodeType, map[string]any{
		"inputText": "The new release is wonderful.",
	}, run.provider.URL, workflow.Item{JSON: map[string]any{}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	parsed, isObject := output[0][0].JSON["output"].(map[string]any)
	if !isObject || parsed["sentiment"] != "positive" || parsed["score"] != float64(0.9) {
		t.Fatalf("output = %#v, want the sentiment object under output", output[0][0].JSON)
	}
	prompt := promptContents(t, (*run.bodies)[0])[1]["content"].(string)
	if !strings.Contains(prompt, "positive") || !strings.Contains(prompt, "neutral") || !strings.Contains(prompt, "negative") {
		t.Fatalf("prompt = %q, want the scale stated to the model", prompt)
	}
}

// TestTheChainsRefuseARunWithoutAModel: the model port is required, and the
// refusal names it rather than a nil pointer.
func TestTheChainsRefuseARunWithoutAModel(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	registry := aiRegistry(t)
	definition, _ := registry.Lookup(nodes.InformationExtractorNodeType, workflow.V(1))
	executor, _ := executors.Lookup(definition.ExecutorID)
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "extract", Name: "Extract", Type: nodes.InformationExtractorNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"text":       "hi",
			"attributes": map[string]any{"values": []any{map[string]any{"name": "a"}}},
		},
		Definition: definition,
	}, workflow.NodeInput{"main": {{JSON: map[string]any{}}}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "model port") {
		t.Fatalf("Execute() = %v, want a missing-model rejection", err)
	}
}

// TestTheItemListParserRunsThroughTheChainLoop: the Item List Output Parser is
// the parser node's third source, and an answer that is a list of matching
// items parses through the same chain loop every parser runs through.
func TestTheItemListParserRunsThroughTheChainLoop(t *testing.T) {
	t.Parallel()

	run := startChainRun(t, `[{"sku":"A","qty":2},{"sku":"B","qty":3}]`)

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), ai.NewLoopRuntime(), nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	registry := aiRegistry(t)
	parserDefinition, _ := registry.Lookup(nodes.OutputParserNodeType, workflow.V(1))
	parser, _ := executors.Lookup(parserDefinition.ExecutorID)
	parserOutput, err := parser.Execute(context.Background(), workflow.IRNode{
		ID: "parser", Name: "Parser", Type: nodes.OutputParserNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"schemaType":  ai.OutputSchemaItemList,
			"exampleJson": `{"sku":"A","qty":3}`,
		},
		Definition: parserDefinition,
	}, workflow.NodeInput{}, engine.Request{})
	if err != nil {
		t.Fatalf("parser Execute() error = %v", err)
	}
	descriptor, _ := parserOutput[0][0].JSON["$ai"].(map[string]any)
	schema, _ := descriptor["schema"].(map[string]any)
	if schema["type"] != "array" {
		t.Fatalf("descriptor schema = %#v, want the item schema wrapped in an array", schema)
	}

	chainDefinition, _ := registry.Lookup(nodes.ChainNodeType, workflow.V(1))
	chain, _ := executors.Lookup(chainDefinition.ExecutorID)
	output, err := chain.Execute(context.Background(), workflow.IRNode{
		ID: "chain", Name: "Chain", Type: nodes.ChainNodeType, TypeVersion: workflow.V(1),
		Parameters: map[string]any{"promptType": "define", "text": "List the line items."},
		Definition: chainDefinition,
	}, workflow.NodeInput{
		"main":         {{JSON: map[string]any{}}},
		"model":        {chainModelDescriptor(t, run.provider.URL)},
		"outputParser": {parserOutput[0][0]},
	}, engine.Request{Credentials: bearerResolver()})
	if err != nil {
		t.Fatalf("chain Execute() error = %v", err)
	}
	list, isList := output[0][0].JSON["output"].([]any)
	if !isList || len(list) != 2 {
		t.Fatalf("output = %#v, want the parsed list of items under output", output[0][0].JSON)
	}
}
