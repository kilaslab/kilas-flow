package ai_test

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
)

// reviewSchema is the audit's reproduction: a closed object whose score has a
// range, whose sentiment is an enum, and whose every property is required.
const reviewSchema = `{
	"type": "object",
	"properties": {
		"sentiment": {"type": "string", "enum": ["positive", "negative", "neutral"]},
		"score": {"type": "integer", "minimum": 1, "maximum": 5},
		"topics": {"type": "array", "items": {"type": "string"}}
	},
	"required": ["sentiment", "score", "topics"],
	"additionalProperties": false
}`

func parseSchema(t *testing.T, text string) map[string]any {
	t.Helper()
	schema, _, err := ai.ParseOutputSchema(map[string]any{
		ai.OutputParserSchemaTypeKey: ai.OutputSchemaJSONSchema,
		ai.OutputParserSchemaKey:     text,
	})
	if err != nil {
		t.Fatalf("ParseOutputSchema(%s) error = %v", text, err)
	}
	return schema
}

func decode(t *testing.T, text string) any {
	t.Helper()
	var value any
	if err := json.Unmarshal([]byte(text), &value); err != nil {
		t.Fatalf("decode %s: %v", text, err)
	}
	return value
}

func TestValidateValueEnforcesRangesAndClosedObjects(t *testing.T) {
	t.Parallel()

	schema := parseSchema(t, reviewSchema)
	if err := ai.ValidateValue(schema, decode(t, `{"sentiment":"positive","score":4,"topics":["battery"]}`), "output"); err != nil {
		t.Fatalf("ValidateValue(valid) = %v", err)
	}
	for name, test := range map[string]struct {
		value string
		want  []string
	}{
		"above maximum":      {`{"sentiment":"positive","score":8,"topics":[]}`, []string{"output.score", "maximum"}},
		"below minimum":      {`{"sentiment":"positive","score":0,"topics":[]}`, []string{"output.score", "minimum"}},
		"extra property":     {`{"sentiment":"positive","score":3,"topics":[],"mood":"happy"}`, []string{"output", `"mood"`}},
		"outside the enum":   {`{"sentiment":"ecstatic","score":3,"topics":[]}`, []string{"output.sentiment"}},
		"missing required":   {`{"sentiment":"positive","topics":[]}`, []string{`"score"`}},
		"wrong item type":    {`{"sentiment":"positive","score":3,"topics":[7]}`, []string{"output.topics[0]"}},
		"fractional integer": {`{"sentiment":"positive","score":3.5,"topics":[]}`, []string{"output.score", "integer"}},
	} {
		err := ai.ValidateValue(schema, decode(t, test.value), "output")
		if err == nil {
			t.Errorf("%s: ValidateValue(%s) accepted it", name, test.value)
			continue
		}
		for _, want := range test.want {
			if !strings.Contains(err.Error(), want) {
				t.Errorf("%s: error = %q, want it to mention %s", name, err, want)
			}
		}
	}

	// Every violation is reported at once, so one repair turn can fix them
	// all instead of spending a retry per mistake.
	err := ai.ValidateValue(schema, decode(t, `{"sentiment":"ecstatic","score":8,"topics":[],"mood":"happy"}`), "output")
	if err == nil {
		t.Fatal("ValidateValue accepted three violations")
	}
	for _, want := range []string{"output.sentiment", "output.score", `"mood"`} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error = %q, want every violation including %s", err, want)
		}
	}
}

func TestValidateValueEnforcesStringAndArrayBounds(t *testing.T) {
	t.Parallel()

	schema := parseSchema(t, `{
		"type": "object",
		"properties": {
			"code": {"type": "string", "minLength": 2, "maxLength": 3, "pattern": "^[A-Z]+$"},
			"tags": {"type": "array", "minItems": 1, "maxItems": 2},
			"ratio": {"type": "number", "exclusiveMinimum": 0, "exclusiveMaximum": 1},
			"step": {"type": "number", "multipleOf": 0.5},
			"kind": {"const": "review"}
		}
	}`)
	if err := ai.ValidateValue(schema, decode(t, `{"code":"ÉÀ","tags":["a"],"ratio":0.5,"step":1.5,"kind":"review"}`), "output"); err == nil {
		// É is not in [A-Z]; the length is two code points, inside the bound.
		t.Error("ValidateValue accepted a string outside its pattern")
	}
	if err := ai.ValidateValue(schema, decode(t, `{"code":"AB","tags":["a"],"ratio":0.5,"step":1.5,"kind":"review"}`), "output"); err != nil {
		t.Errorf("ValidateValue(valid) = %v", err)
	}
	for name, value := range map[string]string{
		"too short":          `{"code":"A"}`,
		"too long":           `{"code":"ABCD"}`,
		"too few items":      `{"tags":[]}`,
		"too many items":     `{"tags":[1,2,3]}`,
		"at exclusive floor": `{"ratio":0}`,
		"at exclusive roof":  `{"ratio":1}`,
		"not a multiple":     `{"step":0.7}`,
		"not the constant":   `{"kind":"essay"}`,
	} {
		if err := ai.ValidateValue(schema, decode(t, value), "output"); err == nil {
			t.Errorf("%s: ValidateValue(%s) accepted it", name, value)
		}
	}
}

func TestParseOutputSchemaAcceptsNullableAndUnionTypes(t *testing.T) {
	t.Parallel()

	schema := parseSchema(t, `{
		"type": "object",
		"properties": {
			"nickname": {"type": ["string", "null"]},
			"id": {"anyOf": [{"type": "integer", "minimum": 1}, {"type": "string", "pattern": "^[a-z]+$"}]},
			"contact": {"oneOf": [
				{"type": "object", "properties": {"email": {"type": "string"}}, "required": ["email"]},
				{"type": "object", "properties": {"phone": {"type": "string"}}, "required": ["phone"]}
			]},
			"legacy": {"type": "string", "nullable": true}
		}
	}`)
	for _, value := range []string{
		`{"nickname":null}`,
		`{"nickname":"Bo"}`,
		`{"id":3}`,
		`{"id":"abc"}`,
		`{"contact":{"email":"a@b.c"}}`,
		`{"legacy":null}`,
	} {
		if err := ai.ValidateValue(schema, decode(t, value), "output"); err != nil {
			t.Errorf("ValidateValue(%s) = %v", value, err)
		}
	}
	for name, test := range map[string]struct{ value, path string }{
		"nickname number":      {`{"nickname":5}`, "output.nickname"},
		"id matches neither":   {`{"id":0}`, "output.id"},
		"id bad string":        {`{"id":"ABC"}`, "output.id"},
		"contact matches two":  {`{"contact":{"email":"a@b.c","phone":"1"}}`, "output.contact"},
		"contact matches none": {`{"contact":{}}`, "output.contact"},
	} {
		err := ai.ValidateValue(schema, decode(t, test.value), "output")
		if err == nil {
			t.Errorf("%s: ValidateValue(%s) accepted it", name, test.value)
			continue
		}
		if !strings.Contains(err.Error(), test.path) {
			t.Errorf("%s: error = %q, want it to name %s", name, err, test.path)
		}
	}
}

func TestParseOutputSchemaRejectsMalformedKeywords(t *testing.T) {
	t.Parallel()

	for name, test := range map[string]struct{ schema, path string }{
		"unknown type in a list":   {`{"type":["string","text"]}`, "jsonSchema.type"},
		"empty type list":          {`{"type":[]}`, "jsonSchema.type"},
		"anyOf not a list":         {`{"anyOf":{"type":"string"}}`, "jsonSchema.anyOf"},
		"oneOf member not object":  {`{"oneOf":["string"]}`, "jsonSchema.oneOf[0]"},
		"nested anyOf bad type":    {`{"anyOf":[{"type":"paragraph"}]}`, "jsonSchema.anyOf[0].type"},
		"additionalProperties str": {`{"additionalProperties":"no"}`, "jsonSchema.additionalProperties"},
		"minimum not a number":     {`{"minimum":"1"}`, "jsonSchema.minimum"},
		"maxLength negative":       {`{"maxLength":-1}`, "jsonSchema.maxLength"},
		"pattern does not compile": {`{"pattern":"(["}`, "jsonSchema.pattern"},
		"enum not a list":          {`{"enum":"a"}`, "jsonSchema.enum"},
	} {
		_, _, err := ai.ParseOutputSchema(map[string]any{
			ai.OutputParserSchemaTypeKey: ai.OutputSchemaJSONSchema,
			ai.OutputParserSchemaKey:     test.schema,
		})
		if err == nil {
			t.Errorf("%s: %s was accepted", name, test.schema)
			continue
		}
		if !strings.Contains(err.Error(), test.path) {
			t.Errorf("%s: error = %q, want it to name %s", name, err, test.path)
		}
	}
}

func TestParserRetriesAnOutOfRangeScore(t *testing.T) {
	t.Parallel()

	// The audit's run 2: a score of 8 against maximum 5 must be retried, not
	// returned as success.
	model := &fakeModel{responses: []ai.ModelResponse{
		toolTurn(ai.FormatFinalJSONResponse, `{"sentiment":"positive","score":8,"topics":["battery"]}`),
		toolTurn(ai.FormatFinalJSONResponse, `{"sentiment":"positive","score":5,"topics":["battery"]}`),
	}}
	sink, _ := collect()
	result, err := ai.NewLoopRuntime().Run(context.Background(), ai.AgentRequest{
		Model: model, ModelName: "test-model", Input: "review?",
		OutputSchema: parseSchema(t, reviewSchema), OutputMaxRetries: 2,
	}, sink)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if len(model.requests) != 2 {
		t.Fatalf("model calls = %d, want the out-of-range answer retried once", len(model.requests))
	}
	var output map[string]any
	if err := json.Unmarshal([]byte(result.Output), &output); err != nil || output["score"] != float64(5) {
		t.Errorf("Output = %q, want the corrected score", result.Output)
	}
	retry := model.requests[1].Messages
	repair := retry[len(retry)-1]
	if !strings.Contains(repair.Content, "output.score") || !strings.Contains(repair.Content, "maximum") {
		t.Errorf("repair turn = %q, want it to name the score and its maximum", repair.Content)
	}
}

// TestParseOutputSchemaWrapsAnItemListItemsSchema is the Item List Output
// Parser: the author describes one item and the model is held to a list of
// them — the item's shape validated per member, by the same validator every
// other schema goes through.
func TestParseOutputSchemaWrapsAnItemListItemsSchema(t *testing.T) {
	t.Parallel()

	fromSchema, retries, err := ai.ParseOutputSchema(map[string]any{
		ai.OutputParserSchemaTypeKey: ai.OutputSchemaItemList,
		ai.OutputParserSchemaKey:     `{"type":"object","properties":{"sku":{"type":"string"}},"required":["sku"],"additionalProperties":false}`,
	})
	if err != nil {
		t.Fatalf("ParseOutputSchema(item list from a schema) error = %v", err)
	}
	if retries != ai.DefaultOutputMaxRetries {
		t.Errorf("retries = %d, want the default", retries)
	}
	if fromSchema["type"] != "array" {
		t.Fatalf("schema = %#v, want an array at the top", fromSchema)
	}
	for name, value := range map[string]string{
		"a list of matching items": `[{"sku":"A"},{"sku":"B"}]`,
		"an empty list":            `[]`,
	} {
		if err := ai.ValidateValue(fromSchema, decode(t, value), "output"); err != nil {
			t.Errorf("%s: ValidateValue(%s) = %v", name, value, err)
		}
	}
	for name, value := range map[string]string{
		"not a list":                `{"sku":"A"}`,
		"a member missing the key":  `[{}]`,
		"a member with a stray key": `[{"sku":"A","qty":2}]`,
	} {
		if err := ai.ValidateValue(fromSchema, decode(t, value), "output"); err == nil {
			t.Errorf("%s: ValidateValue(%s) accepted it", name, value)
		}
	}

	fromExample, _, err := ai.ParseOutputSchema(map[string]any{
		ai.OutputParserSchemaTypeKey: ai.OutputSchemaItemList,
		ai.OutputParserExampleKey:    `{"sku":"A","qty":2}`,
	})
	if err != nil {
		t.Fatalf("ParseOutputSchema(item list from an example) error = %v", err)
	}
	if err := ai.ValidateValue(fromExample, decode(t, `[{"sku":"B","qty":3}]`), "output"); err != nil {
		t.Errorf("ValidateValue(a list shaped like the example) = %v", err)
	}
	if err := ai.ValidateValue(fromExample, decode(t, `[{"sku":"B"}]`), "output"); err == nil {
		t.Error("ValidateValue accepted a list missing a property the example carries")
	}
}

// TestParseOutputSchemaNamesTheItemListRequirements: an item-list parser with
// neither field, and an unknown source naming the third option it could have
// used.
func TestParseOutputSchemaNamesTheItemListRequirements(t *testing.T) {
	t.Parallel()

	_, _, err := ai.ParseOutputSchema(map[string]any{ai.OutputParserSchemaTypeKey: ai.OutputSchemaItemList})
	if err == nil || !strings.Contains(err.Error(), "item list") {
		t.Fatalf("ParseOutputSchema() = %v, want the item-list requirement named", err)
	}
	_, _, err = ai.ParseOutputSchema(map[string]any{ai.OutputParserSchemaTypeKey: "telepathy"})
	if err == nil || !strings.Contains(err.Error(), ai.OutputSchemaItemList) {
		t.Fatalf("ParseOutputSchema() = %v, want the unknown source to name the options", err)
	}
}
