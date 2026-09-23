package jsrun_test

import (
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

func analyze(t *testing.T, source string) (jsrun.Analysis, error) {
	t.Helper()
	return jsrun.Analyze(source, jsrun.ModeAllItems)
}

// refusedFor asserts a body is refused in the one Refusal sentence, naming
// what it uses.
func refusedFor(t *testing.T, source, subject string) {
	t.Helper()
	_, err := analyze(t, source)
	if !errors.Is(err, jsrun.ErrUnsupported) {
		t.Fatalf("%q: Analyze() error = %v, want it refused", source, err)
	}
	if !strings.HasPrefix(err.Error(), "this node's code "+subject) || !strings.Contains(err.Error(), "which this server does not run") {
		t.Fatalf("%q: error = %q, want the refusal to say it %s", source, err, subject)
	}
}

func accepted(t *testing.T, source string) jsrun.Analysis {
	t.Helper()
	analysis, err := analyze(t, source)
	if err != nil {
		t.Fatalf("%q: Analyze() error = %v, want it accepted", source, err)
	}
	return analysis
}

func TestTheRefusalSentenceIsTheOneForEveryLanguage(t *testing.T) {
	got := jsrun.Refusal("is written in Python", "Use native nodes.")
	if want := "this node's code is written in Python, which this server does not run. Use native nodes."; got != want {
		t.Fatalf("Refusal() = %q, want %q", got, want)
	}
}

// The analyser reads a syntax tree, not text, so what a string or a comment
// merely mentions is not what the code does.
func TestAnalysisIgnoresStringsAndComments(t *testing.T) {
	analysis := accepted(t, strings.Join([]string{
		`const text = "this.getCredentials() require('fs') /\\p{L}/u for await import"`,
		`// require('child_process'); this.helpers.httpRequest()`,
		`/* async function* never() {} */`,
		"return [{ json: { text } }]",
	}, "\n"))
	if len(analysis.Requires) != 0 || analysis.UsesLuxon {
		t.Fatalf("analysis = %#v, want nothing found in strings and comments", analysis)
	}
}

func TestAnAsyncGeneratorIsRefusedByName(t *testing.T) {
	refusedFor(t, "async function* numbers() { yield 1 }\nreturn []", "uses an async generator")
	refusedFor(t, "const source = { async *numbers() { yield 1 } }\nreturn []", "uses an async generator")
}

func TestTheVAndDRegexFlagsAreRefused(t *testing.T) {
	refusedFor(t, "return [{ json: { ok: /[a]/v.test('a') } }]", `uses the regular-expression flag 'v'`)
	refusedFor(t, "return [{ json: { ok: /a/d.test('a') } }]", `uses the regular-expression flag 'd'`)
}

// goja accepts \p{…} under the u flag and matches nothing, so the code would
// run and compute something else. Without the u flag \p is just "p", as in
// every engine, and a doubled backslash is not an escape at all.
func TestUnicodePropertyEscapesAreRefusedOnlyWithTheUFlag(t *testing.T) {
	refusedFor(t, `return [{ json: { ok: /\p{L}+/u.test('é') } }]`, `uses a regular expression with \p{…} property escapes`)
	refusedFor(t, `return [{ json: { ok: new RegExp('\\P{Lu}', 'u').test('a') } }]`, `uses a regular expression with \p{…} property escapes`)
	accepted(t, `return [{ json: { ok: /\p{L}/.test('p{L}') } }]`)
	accepted(t, `return [{ json: { ok: /\\p{L}/u.test('\\p{L}') } }]`)
}

func TestForAwaitAndModulesAreRefusedByNameNotAsSyntaxErrors(t *testing.T) {
	refusedFor(t, "for await (const value of values) {}\nreturn []", "uses for await")
	refusedFor(t, "import lodash from 'lodash'\nreturn []", "uses import or export")
	refusedFor(t, "export const answer = 42", "uses import or export")
	refusedFor(t, "const module = await import('lodash')\nreturn []", "uses import or export")
}

func TestRequireOfAModuleNotShippedIsRefused(t *testing.T) {
	refusedFor(t, "const fs = require('fs')\nreturn []", `requires the module "fs"`)
	refusedFor(t, "const transcript = require('youtube-transcript')\nreturn []", `requires the module "youtube-transcript"`)
	analysis := accepted(t, "const util = require('node:util')\nconst _ = require('lodash')\nconst { DateTime } = require('luxon')\nreturn []")
	if strings.Join(analysis.Requires, ",") != "lodash,luxon,util" {
		t.Fatalf("analysis = %#v, want the three shipped modules", analysis)
	}
	if !accepted(t, "const name = 'lodash'\nconst module = require(name)\nreturn []").DynamicRequire {
		t.Fatal("a require() of a variable was not reported as dynamic")
	}
}

// Credentials are never reachable from a Code node, as in n8n. A class or a
// function has its own `this`, so only the code's own `this` counts; an
// arrow function shares it.
func TestCredentialsAndHelpersAreRefusedOnlyOnTheCodesOwnThis(t *testing.T) {
	refusedFor(t, "const key = await this.getCredentials('api')\nreturn []", "calls this.getCredentials")
	refusedFor(t, "const get = () => this['getCredentials']\nreturn []", "calls this.getCredentials")
	refusedFor(t, "const page = await this.helpers.httpRequest({ url: 'https://example.com' })\nreturn []", "uses this.helpers")
	accepted(t, "class Client { credentials() { return this.getCredentials } }\nfunction helper() { return this.helpers }\nreturn []")
}

func TestStaticDataIsRefusedUntilItIsSupported(t *testing.T) {
	refusedFor(t, "const state = $getWorkflowStaticData('global')\nreturn []", "uses $getWorkflowStaticData")
}

func TestLuxonIsNoticedWhereverTheCodeNamesIt(t *testing.T) {
	for _, source := range []string{
		"return [{ json: { at: DateTime.now().toISO() } }]",
		"return [{ json: { at: $now.toISO() } }]",
		"const { DateTime } = require('luxon')\nreturn [{ json: { at: DateTime.now().toISO() } }]",
	} {
		if !accepted(t, source).UsesLuxon {
			t.Errorf("%q: UsesLuxon = false", source)
		}
	}
	if accepted(t, "return items").UsesLuxon {
		t.Error("a body that never names Luxon was marked as using it")
	}
}

// Positions are byte offsets. A body full of multi-byte characters must not
// look like it escapes its wrapper, and must still report its own lines.
func TestNonASCIICodeIsNotMistakenForAnEscape(t *testing.T) {
	source := "// Überprüfung für résumé 😀\nconst greeting = 'héllo wörld 😀'\nreturn [{ json: { greeting } }]"
	accepted(t, source)
	result := mustRun(t, newRunner(), jsTask(source))
	if result.Items[0].JSON["greeting"] != "héllo wörld 😀" {
		t.Fatalf("items = %#v", result.Items)
	}
	_, err := runAll(t, newRunner(), "// 😀😀😀\nconst x = 'é'\nnull.boom", nil)
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || script.Line != 3 {
		t.Fatalf("error = %v, want it on line 3", err)
	}
}

func jsTask(source string) jsrun.Task { return jsrun.Task{Source: source} }

// goja's parser and compiler recurse on nesting with no limit of their own,
// and a Go stack overflow cannot be recovered, so a body built to nest deeply
// would take the server down at save time. Some nesting is also quadratic to
// parse or compile. Such bodies are refused before goja does anything costly.
func TestOversizedOrDeeplyNestedCodeIsRefusedBeforeItCanHurtTheServer(t *testing.T) {
	for name, check := range map[string]struct {
		source, subject string
	}{
		"too long":    {strings.Repeat("x = 1\n", jsrun.MaxSourceBytes/6+1), "is longer than 128 KiB"},
		"arrow chain": {"return " + strings.Repeat("a=>", 1001) + "1", "has more than 1000 arrow functions"},
		// Parentheses leave no trace in the tree, so their depth is bounded by
		// the length cap instead; a nested literal keeps its depth.
		"array nesting": {"return " + strings.Repeat("[", 5000) + strings.Repeat("]", 5000), "nests more than 1000 levels deep"},
		"block nesting": {strings.Repeat("{", 20000) + strings.Repeat("}", 20000) + "\nreturn []", "nests more than 1000 levels deep"},
	} {
		start := time.Now()
		_, err := analyze(t, check.source)
		if !errors.Is(err, jsrun.ErrUnsupported) || !strings.Contains(err.Error(), check.subject) {
			t.Errorf("%s: Analyze() error = %v, want it refused because it %s", name, err, check.subject)
		}
		if elapsed := time.Since(start); elapsed > time.Second {
			t.Errorf("%s: refusing took %v; it must happen before the costly work", name, elapsed)
		}
	}
}
