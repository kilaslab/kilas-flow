package ai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/ai"
)

func TestParserFinishesThroughFormatTool(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn(ai.FormatFinalJSONResponse, `{"city":"Utrecht","tempC":19}`),
	}}
	sink, events := collect()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"city":  map[string]any{"type": "string"},
			"tempC": map[string]any{"type": "number"},
		},
		"required": []any{"city"},
	}
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "weather?",
		OutputSchema: schema,
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil {
		t.Fatalf("Output = %q, want JSON: %v", result.Output, err)
	}
	if output["city"] != "Utrecht" || output["tempC"] != float64(19) {
		t.Errorf("Output = %v, want the parsed object", output)
	}
	// The synthetic tool is offered to the model and recorded as a tool.
	if len(model.requests) != 1 || len(model.requests[0].Tools) != 1 {
		t.Fatalf("model saw %d tool definitions, want the single format tool", len(model.requests[0].Tools))
	}
	if model.requests[0].Tools[0].Name != ai.FormatFinalJSONResponse {
		t.Errorf("tool name = %q, want the contract name", model.requests[0].Tools[0].Name)
	}
	kinds := map[ai.EventKind]int{}
	for _, event := range *events {
		kinds[event.Kind]++
	}
	if kinds[ai.EventToolStarted] != 1 || kinds[ai.EventToolCompleted] != 1 {
		t.Errorf("tool events = %v, want one started and one completed", kinds)
	}
}

func TestParserFallsBackToPlainText(t *testing.T) {
	t.Parallel()

	// A smaller model answered without calling the tool, but the text parses.
	model := &fakeModel{responses: []ai.ModelResponse{
		answer("```json\n{\"city\":\"Utrecht\"}\n```"),
	}}
	sink, _ := collect()

	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "weather?",
		OutputSchema: schema,
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil || output["city"] != "Utrecht" {
		t.Errorf("Output = %q, want the fenced JSON parsed", result.Output)
	}
}

func TestParserRetriesThenFailsWithErrorAndRawText(t *testing.T) {
	t.Parallel()

	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn(ai.FormatFinalJSONResponse, `{"tempC":"hot"}`),
		toolTurn(ai.FormatFinalJSONResponse, `{"tempC":"hot"}`),
		toolTurn(ai.FormatFinalJSONResponse, `{"tempC":"hot"}`),
	}}
	sink, events := collect()

	schema := map[string]any{
		"type": "object",
		"properties": map[string]any{
			"tempC": map[string]any{"type": "number"},
		},
	}
	_, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "weather?",
		OutputSchema: schema, OutputMaxRetries: 2,
	}, sink)
	if err == nil {
		t.Fatal("Run() succeeded, want a validation failure")
	}
	// The failure carries both the validation error naming the path and the
	// raw text, so it is diagnosable.
	if !strings.Contains(err.Error(), "output.tempC") || !strings.Contains(err.Error(), `{"tempC":"hot"}`) {
		t.Errorf("Run() error = %v, want the path and the raw text", err)
	}
	failures := 0
	pathNamed := false
	for _, event := range *events {
		if event.Kind == ai.EventToolFailed && event.Tool == ai.FormatFinalJSONResponse {
			failures++
			if strings.Contains(event.Error, "output.tempC") {
				pathNamed = true
			}
		}
	}
	if failures != 3 {
		t.Errorf("format tool failures = %d, want 3 (two retries plus the fallback)", failures)
	}
	if !pathNamed {
		t.Errorf("no tool failure named output.tempC; events = %v", *events)
	}
}

func TestParserCoexistsWithRealTools(t *testing.T) {
	t.Parallel()

	weather := &fakeTool{name: "get_weather", result: `{"tempC":19}`}
	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn("get_weather", `{"city":"Utrecht"}`),
		toolTurn(ai.FormatFinalJSONResponse, `{"city":"Utrecht","tempC":19}`),
	}}
	sink, _ := collect()

	schema := map[string]any{"type": "object", "properties": map[string]any{"city": map[string]any{"type": "string"}}}
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "weather?",
		Tools: []ai.Tool{weather}, OutputSchema: schema,
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(weather.calls) != 1 {
		t.Fatalf("real tool calls = %d, want 1", len(weather.calls))
	}
	if result.ToolCalls != 2 {
		t.Errorf("ToolCalls = %d, want both the real tool and the format tool", result.ToolCalls)
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil || output["city"] != "Utrecht" {
		t.Errorf("Output = %q, want the parsed object", result.Output)
	}
}

func TestParseOutputSchemaRejectsBadShapes(t *testing.T) {
	t.Parallel()

	for name, parameters := range map[string]map[string]any{
		"invalid JSON":   {ai.OutputParserSchemaTypeKey: "jsonSchema", ai.OutputParserSchemaKey: "{nope"},
		"unknown type":   {ai.OutputParserSchemaTypeKey: "jsonSchema", ai.OutputParserSchemaKey: `{"type":"paragraph"}`},
		"missing schema": {ai.OutputParserSchemaTypeKey: "jsonSchema"},
		"bad example":    {ai.OutputParserSchemaTypeKey: "exampleJson", ai.OutputParserExampleKey: "[1,"},
		"bad source":     {ai.OutputParserSchemaTypeKey: "yaml"},
	} {
		if _, _, err := ai.ParseOutputSchema(parameters); err == nil {
			t.Errorf("%s was accepted", name)
		}
	}

	schema, _, err := ai.ParseOutputSchema(map[string]any{
		ai.OutputParserSchemaTypeKey: "exampleJson",
		ai.OutputParserExampleKey:    `{"city":"Utrecht","tempC":19}`,
	})
	if err != nil {
		t.Fatalf("ParseOutputSchema(example) error = %v", err)
	}
	if err := ai.ValidateValue(schema, map[string]any{"city": "Utrecht", "tempC": float64(19)}, "output"); err != nil {
		t.Errorf("ValidateValue(inferred) = %v", err)
	}
	if err := ai.ValidateValue(schema, map[string]any{"city": "Utrecht"}, "output"); err == nil {
		t.Error("ValidateValue accepted a missing required property")
	}
}
