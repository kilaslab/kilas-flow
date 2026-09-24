package jsrun_test

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// The errors the engine's built-ins throw read as V8 words them.
// testdata/parity/errors.json is what Node 24 says for each body, and for
// each text handed to JSON.parse.

// keptWording lists the recorded bodies whose error keeps goja's words, and
// what jsrun says for each. V8's wording there depends on what jsrun cannot
// know faithfully: whether a value was null or undefined (goja reports both
// as undefined), how V8 prints an expression it has no simple form for, the
// value a callback was, or a key and value goja's message leaves out. A body
// that starts matching Node fails here, and its entry must be deleted.
var keptWording = map[string]string{
	"a missing method of a string literal":    "TypeError: Object has no member 'q'",
	"a missing method of an array literal":    "TypeError: Object has no member 'q'",
	"a missing method through a comma":        "TypeError: Value is not an object: undefined",
	"a missing method by a computed sum":      "TypeError: Object has no member 'kx'",
	"a tagged template of a missing function": "TypeError: Object has no member 'q'",
	"a callback that is not a function":       "TypeError: Value is not an object: 1",
	"a property of null":                      "TypeError: Cannot read properties of undefined (reading 'field')",
	"a method of null":                        "TypeError: Cannot read properties of undefined (reading 'map')",
	"a symbol property of undefined":          "TypeError: Cannot read properties of undefined (reading 'k')",
	"setting a property of undefined":         "TypeError: Cannot convert undefined or null to object",
	"destructuring undefined":                 "TypeError: Value is not object coercible",
	"iterating undefined":                     "TypeError: Cannot convert undefined or null to object",
	"iterating an object":                     "TypeError: object is not iterable",
	"the 'in' operator on a string":           "TypeError: Value is not an object: s",
}

type errorProbe struct {
	Name  string          `json:"name"`
	Code  string          `json:"code"`
	Want  json.RawMessage `json:"want"`
	Error string          `json:"error"`
}

// Each body runs twice: throwing uncaught, where the node fails with the
// error, and inside a try whose catch reads the error's name and message, as
// code that reports or branches on an error does.
func TestBuiltInErrorsAreWordedAsNodeWordsThem(t *testing.T) {
	var golden struct {
		Probes []errorProbe `json:"probes"`
	}
	loadGolden(t, "errors.json", &golden)
	if len(golden.Probes) == 0 {
		t.Fatal("the golden has no probes")
	}
	for _, probe := range golden.Probes {
		want := probe.Error
		if kept, ok := keptWording[probe.Name]; ok {
			want = kept
		}
		uncaught := uncaughtWords(t, probe.Code)
		caught := caughtWords(t, probe.Code)
		if want == "" {
			t.Errorf("%s: jsrun says %q uncaught and %q caught; Node says %q", probe.Name, uncaught, caught, probe.Error)
			continue
		}
		if uncaught != want {
			t.Errorf("%s, uncaught:\n got  %s\n want %s", probe.Name, uncaught, want)
		}
		if caught != want {
			t.Errorf("%s, caught:\n got  %s\n want %s", probe.Name, caught, want)
		}
	}
}

// uncaughtWords runs a body and returns the error it failed with, as
// "Name: message" without the place jsrun adds.
func uncaughtWords(t *testing.T, code string) string {
	t.Helper()
	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: code, Items: numbered(1)})
	var thrown *jsrun.ScriptError
	if !errors.As(err, &thrown) {
		return "no error: " + errorText(err)
	}
	return thrown.Name + ": " + thrown.Message
}

// caughtWords runs a body inside a try and returns what its catch read.
func caughtWords(t *testing.T, code string) string {
	t.Helper()
	body := "try {\n" + code + "\n} catch (error) {\n  return [{ json: { words: String(error.name) + ': ' + String(error.message), stack: String(error.stack) } }]\n}"
	result, err := newRunner().Run(context.Background(), jsrun.Task{Source: body, Items: numbered(1)})
	if err != nil {
		return "failed: " + err.Error()
	}
	if len(result.Items) != 1 {
		return "no error caught"
	}
	words, _ := result.Items[0].JSON["words"].(string)
	if stack, _ := result.Items[0].JSON["stack"].(string); !strings.HasPrefix(stack, words+"\n") {
		return "a stack that does not start with the message: " + stack
	}
	return words
}

func errorText(err error) string {
	if err == nil {
		return "<nil>"
	}
	return err.Error()
}

// JSON.parse fails with V8's message for every recorded text, and parses
// the ones V8 parses. The texts arrive as the node's input, so no escaping
// stands between the golden and the code.
func TestJSONParseFailsWithNodesMessages(t *testing.T) {
	var golden struct {
		JSON []struct {
			Text  string  `json:"text"`
			Error *string `json:"error"`
		} `json:"json"`
		JSONValues []errorProbe `json:"jsonValues"`
	}
	loadGolden(t, "errors.json", &golden)
	if len(golden.JSON) == 0 {
		t.Fatal("the golden has no JSON texts")
	}
	values := make([]map[string]any, len(golden.JSON))
	for index, entry := range golden.JSON {
		values[index] = map[string]any{"text": entry.Text}
	}
	items := itemsOf(values...)
	result := mustRun(t, newRunner(), jsrun.Task{Items: items, Source: strings.Join([]string{
		"return items.map((item) => {",
		"  try { JSON.parse(item.json.text); return { json: { error: null } } }",
		"  catch (error) { return { json: { error: error.name + ': ' + error.message } } }",
		"})",
	}, "\n")})
	for index, entry := range golden.JSON {
		got, _ := result.Items[index].JSON["error"].(string)
		want := ""
		if entry.Error != nil {
			want = *entry.Error
		}
		if got != want {
			t.Errorf("JSON.parse(%q):\n got  %q\n want %q", entry.Text, got, want)
		}
	}

	for _, probe := range golden.JSONValues {
		result, err := newRunner().Run(context.Background(), jsrun.Task{Source: probe.Code, Items: numbered(1)})
		if probe.Error != "" {
			var thrown *jsrun.ScriptError
			if !errors.As(err, &thrown) || thrown.Name+": "+thrown.Message != probe.Error {
				t.Errorf("%s: got %v, want %s", probe.Name, err, probe.Error)
			}
			continue
		}
		if err != nil {
			t.Errorf("%s: Run() error = %v", probe.Name, err)
			continue
		}
		got := make([]map[string]any, len(result.Items))
		for index, item := range result.Items {
			got[index] = item.JSON
		}
		if !sameJSON(got, probe.Want) {
			encoded, _ := json.Marshal(got)
			t.Errorf("%s:\n got  %s\n want %s", probe.Name, encoded, probe.Want)
		}
	}
}

// The rewording reaches every mode's code: a body run once for each item,
// and a Sort comparator, whose wrapper is a different function.
func TestErrorsAreWordedAsNodeWordsThemInEveryMode(t *testing.T) {
	_, err := newRunner().Run(context.Background(), jsrun.Task{Source: "const value = {}\nreturn value.map(1)", Mode: jsrun.ModeEachItem, Items: numbered(1)})
	var thrown *jsrun.ScriptError
	if !errors.As(err, &thrown) || thrown.Message != "value.map is not a function" {
		t.Errorf("each item: Run() = %v, want value.map is not a function", err)
	}
	result := mustRun(t, newRunner(), jsrun.Task{Mode: jsrun.ModeEachItem, Items: numbered(1),
		Source: "try { $json.missing.field } catch (error) { return { json: { m: error.message } } }"})
	if result.Items[0].JSON["m"] != "Cannot read properties of undefined (reading 'field')" {
		t.Errorf("each item, caught: %#v", result.Items)
	}
	_, err = newRunner().Run(context.Background(), jsrun.Task{Source: "return a.json.n.compare(b)", Mode: jsrun.ModeComparator, Items: numbered(2)})
	if !errors.As(err, &thrown) || thrown.Message != "a.json.n.compare is not a function" {
		t.Errorf("comparator: Run() = %v, want a.json.n.compare is not a function", err)
	}
}

// What code does with a caught error sees V8's words throughout: a nested
// catch, a rethrow (the message is not reworded twice), the console, and a
// catch that destructures the error, which reads it before any statement
// runs and so keeps goja's words.
func TestACaughtErrorReadsTheSameEverywhere(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"function inner() { try { return undefined.x } catch (error) { throw error } }",
		"let outer",
		"try { inner() } catch (error) { outer = error.message; console.log(error) }",
		"let destructured",
		"try { null.y } catch ({ message }) { destructured = message }",
		"return [{ json: { outer, destructured } }]",
	}, "\n")})
	if result.Items[0].JSON["outer"] != "Cannot read properties of undefined (reading 'x')" {
		t.Errorf("rethrown: %#v", result.Items[0].JSON)
	}
	if result.Items[0].JSON["destructured"] != "Cannot read property 'y' of undefined" {
		t.Errorf("destructured: %#v, want goja's words", result.Items[0].JSON)
	}
	if len(result.Console) != 1 || !strings.HasPrefix(result.Console[0].Text, "TypeError: Cannot read properties of undefined (reading 'x')\n") {
		t.Errorf("console = %#v", result.Console)
	}
}

// The function the catch clauses call is a parameter of the compiled
// wrapper only, under a name no source can spell: the code cannot see it as
// a global, among the wrapper's parameters, or in its text.
func TestTheRewordingFunctionIsOutOfTheCodesReach(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const global = typeof globalThis['kilasflow:reword']",
		"const names = Object.getOwnPropertyNames(globalThis).filter((name) => name.includes('reword'))",
		"return [{ json: { global, names } }]",
	}, "\n")})
	if result.Items[0].JSON["global"] != "undefined" || len(result.Items[0].JSON["names"].([]any)) != 0 {
		t.Errorf("the code can see the rewording function: %#v", result.Items[0].JSON)
	}
}

// The JSON scanner keeps its own stack of open brackets rather than
// recursing, so a deeply nested text that goja's parse rejects is worded
// without any call depth of its own.
func TestJSONParseOfADeepTextFailsCleanly(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const deep = '['.repeat(5000) + ']'.repeat(4999)",
		"try { JSON.parse(deep) } catch (error) { return [{ json: { m: error.message } }] }",
	}, "\n")})
	if m, _ := result.Items[0].JSON["m"].(string); !strings.HasPrefix(m, "Expected ',' or ']' after array element in JSON at position 9999") {
		t.Errorf("message = %q", m)
	}
}
