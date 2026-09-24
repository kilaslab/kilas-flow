package jsrun_test

import (
	"context"
	"errors"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// fruit are five items whose `n` has a tie (b and d) and whose `s` sorts
// differently by code unit than by dictionary ('Zebra' before 'apple').
func fruit() []workflow.Item {
	return itemsOf(
		map[string]any{"n": float64(3), "s": "pear", "t": "a"},
		map[string]any{"n": float64(1), "s": "apple", "t": "b"},
		map[string]any{"n": float64(2), "s": "Zebra", "t": "c"},
		map[string]any{"n": float64(1), "s": "banana", "t": "d"},
		map[string]any{"n": float64(10), "s": "apple", "t": "e"},
	)
}

func sortWith(t *testing.T, source string, items []workflow.Item) (jsrun.Result, error) {
	t.Helper()
	return newRunner().Run(context.Background(), jsrun.Task{Source: source, Mode: jsrun.ModeComparator, Items: items})
}

func mustSort(t *testing.T, source string, items []workflow.Item) []int {
	t.Helper()
	result, err := sortWith(t, source, items)
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if result.Items != nil {
		t.Fatalf("items = %#v, want none: a comparator returns an order", result.Items)
	}
	return result.Order
}

// A comparator sorts as JavaScript's Array.prototype.sort does with it, and a
// tie keeps the order the items arrived in, as V8's stable sort does in n8n.
func TestAComparatorSortsNumbersAndKeepsTiesInTheirOrder(t *testing.T) {
	order := mustSort(t, "return a.json.n - b.json.n;", fruit())
	if want := []int{1, 3, 2, 0, 4}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
	descending := mustSort(t, "return b.json.n - a.json.n;", fruit())
	if want := []int{4, 0, 2, 1, 3}; !slices.Equal(descending, want) {
		t.Fatalf("descending order = %v, want %v", descending, want)
	}
}

func TestAComparatorComparesStringsByCodeUnit(t *testing.T) {
	order := mustSort(t, "return a.json.s < b.json.s ? -1 : a.json.s > b.json.s ? 1 : 0;", fruit())
	if want := []int{2, 1, 4, 3, 0}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want %v", order, want)
	}
}

// a and b are whole items, as n8n hands them: json, and binary when the item
// has a file. `items` is the whole list, as it is in n8n's comparator.
func TestAComparatorSeesWholeItemsAndTheList(t *testing.T) {
	input := fruit()
	input[4].Binary = map[string]workflow.BinaryRef{"data": {ID: "bin_1", FileName: "a.txt", MediaType: "text/plain", Size: 1}}
	source := "if (items.length !== 5 || !('json' in a) || !('json' in b)) throw new Error('not items');\n" +
		"const fileOf = item => (item.binary ? 1 : 0);\n" +
		"return fileOf(b) - fileOf(a) || items.indexOf(a) - items.indexOf(b);"
	order := mustSort(t, source, input)
	if want := []int{4, 0, 1, 2, 3}; !slices.Equal(order, want) {
		t.Fatalf("order = %v, want the item with a file first, then the rest as they came: %v", order, want)
	}
}

func TestAComparatorOverNoItemsIsAnEmptyOrder(t *testing.T) {
	order := mustSort(t, "return 0;", nil)
	if order == nil || len(order) != 0 {
		t.Fatalf("order = %#v, want an empty order", order)
	}
}

// What a comparator throws is located in the user's own lines, as a Code
// node's is.
func TestAComparatorThatThrowsNamesTheLine(t *testing.T) {
	_, err := sortWith(t, "const x = 1;\nthrow new TypeError('boom ' + x);", fruit())
	var script *jsrun.ScriptError
	if !errors.As(err, &script) {
		t.Fatalf("Run() error = %v, want a ScriptError", err)
	}
	if script.Name != "TypeError" || script.Message != "boom 1" || script.Line != 2 {
		t.Fatalf("error = %#v, want TypeError boom 1 on line 2", script)
	}
	if err.Error() != "TypeError: boom 1 [line 2]" {
		t.Fatalf("error text = %q", err)
	}
}

// A comparator must answer with a number. n8n's engine would read a boolean,
// a string or nothing as some number and sort by it, but in an order that
// depends on the engine's own sorting algorithm, which is not this one: the
// same workflow would sort differently here, with nothing to say so. It is
// refused instead, naming what came back, the items and the line.
func TestAComparatorThatDoesNotReturnANumberIsANamedError(t *testing.T) {
	for source, want := range map[string]string{
		"return a.json.n > b.json.n;":                     "a boolean",
		"\nreturn String(a.json.n - b.json.n);":           "a string",
		"return a.json.missing - b.json.n;":               "NaN",
		"if (a.json.n > 100) return 1;":                   "nothing",
		"return null;":                                    "null",
		"return 1n;":                                      "a bigint",
		"if (a.json.n > b.json.n) return 1;\nreturn '0';": "a string",
	} {
		_, err := sortWith(t, source, fruit())
		if !errors.Is(err, jsrun.ErrInvalidReturn) {
			t.Errorf("%q: Run() error = %v, want ErrInvalidReturn", source, err)
			continue
		}
		if !strings.Contains(err.Error(), "the comparator returned "+want+" comparing item ") || !strings.Contains(err.Error(), "it must return a number") {
			t.Errorf("%q: error = %q, want it to name %s, the items and the rule", source, err, want)
		}
	}
	// With exactly one return statement of its own, the line is known.
	_, err := sortWith(t, "const same = a.json.n === b.json.n;\nreturn same ? 0 : a.json.n > b.json.n;", fruit())
	if err == nil || !strings.HasSuffix(err.Error(), " [line 2]") {
		t.Fatalf("Run() error = %v, want it located on line 2", err)
	}
	// A comparator that falls off its end answered with nothing, not with
	// its one return, so no line is given for it.
	_, err = sortWith(t, "if (a.json.n > 100) {\n  return 1;\n}\n", fruit())
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "returned nothing") || strings.Contains(err.Error(), "[line") {
		t.Fatalf("Run() error = %v, want nothing named and no line", err)
	}
	// With two, it is not: which one answered cannot be told afterwards.
	_, err = sortWith(t, "if (a.json.n > b.json.n) return 1;\nreturn '0';", fruit())
	if err == nil || strings.Contains(err.Error(), "[line") {
		t.Fatalf("Run() error = %v, want no line when two returns could have answered", err)
	}
}

// The analyser reads a comparator in its own wrapper, so what it refuses is
// refused in the one sentence, and the comparator's own returns are found.
func TestAComparatorIsAnalysedInItsOwnWrapper(t *testing.T) {
	_, err := jsrun.Analyze("return /\\p{L}/u.test(a.json.s) ? -1 : 1;", jsrun.ModeComparator)
	if !errors.Is(err, jsrun.ErrUnsupported) || !strings.HasPrefix(err.Error(), "this node's code uses a regular expression with \\p{…} property escapes (line 1), which this server does not run.") {
		t.Fatalf("Analyze() error = %v, want the refusal sentence", err)
	}
	analysis, err := jsrun.Analyze("if (a.json.n === b.json.n) {\n  return 0;\n}\nconst later = () => { return 5; };\nfunction inner() { return 6; }\nreturn a.json.n - b.json.n;", jsrun.ModeComparator)
	if err != nil {
		t.Fatalf("Analyze() error = %v", err)
	}
	if want := []int{2, 6}; !slices.Equal(analysis.Returns, want) {
		t.Fatalf("returns = %v, want the comparator's own, %v", analysis.Returns, want)
	}
	// A comparator is a plain function, as it is in n8n, so await is a
	// syntax error rather than something that waits.
	var syntax *jsrun.SyntaxError
	if _, err := jsrun.Analyze("return (await Promise.resolve(a.json.n)) - b.json.n;", jsrun.ModeComparator); !errors.As(err, &syntax) {
		t.Fatalf("Analyze() error = %v, want a syntax error for await", err)
	}
	// Code that closes its wrapper is refused, as in a Code node.
	if _, err := jsrun.Analyze("return 0; }; return function (a, b) { return 0;", jsrun.ModeComparator); !errors.As(err, &syntax) || !strings.Contains(err.Error(), "closes the function it runs in") {
		t.Fatalf("Analyze() error = %v, want the wrapper escape refused", err)
	}
}

// A comparator runs under the same clock as a Code node: the time it spends
// sorting is the user's, and the limit stops it.
func TestAComparatorIsStoppedByTheTimeLimit(t *testing.T) {
	err, overrun := stoppedPromptly(t, newRunner(), jsrun.Task{
		Source: "while (true) {}\nreturn 0;", Mode: jsrun.ModeComparator, Items: fruit(),
		Limits: jsrun.Limits{Timeout: 100 * time.Millisecond},
	})
	if !errors.Is(err, jsrun.ErrTimeLimit) {
		t.Fatalf("Run() error = %v, want ErrTimeLimit", err)
	}
	if overrun > time.Second {
		t.Fatalf("the run returned %v after its interrupt", overrun)
	}
}

// The order comes back from wherever the code ran, a worker included, and is
// used only if it is a true ordering of the input: every item exactly once.
func TestAnOrderThatIsNotAPermutationOfTheInputIsRefused(t *testing.T) {
	job, _, err := newRunner().Prepare(jsrun.Task{Source: "return 0;", Mode: jsrun.ModeComparator, Items: fruit()})
	if err != nil {
		t.Fatalf("Prepare() error = %v", err)
	}
	for _, output := range []string{`[0,1,2,3]`, `[0,1,2,3,3]`, `[0,1,2,3,5]`, `[0,1,2,3,-1]`, `{"0":1}`, `[0,1,2,3,4.5]`} {
		if _, err := job.Finish(jsrun.Executed{Outputs: []string{output}}, nil); !errors.Is(err, jsrun.ErrEngineFault) {
			t.Errorf("%s: Finish() error = %v, want an engine fault", output, err)
		}
	}
	result, err := job.Finish(jsrun.Executed{Outputs: []string{`[4,3,2,1,0]`}}, nil)
	if err != nil || !slices.Equal(result.Order, []int{4, 3, 2, 1, 0}) {
		t.Fatalf("Finish() = %v, %v; want the order", result.Order, err)
	}
}

// A comparator can change the list and the built-ins it was given; neither
// reaches the order the runtime works out and hands back.
func TestAComparatorCannotSpoilTheOrderItHandsBack(t *testing.T) {
	for _, prefix := range []string{
		"Array.prototype.toJSON = function () { return [0, 0, 0, 0, 0]; };",
		"Array.prototype.join = function () { return '0,0,0,0,0'; };",
		"items.length = 0;",
		"items.reverse();",
		"items[0] = items[1];",
	} {
		result, err := sortWith(t, prefix+"\nreturn a.json.n - b.json.n;", fruit())
		if err != nil {
			t.Errorf("%q: Run() error = %v", prefix, err)
			continue
		}
		if want := []int{1, 3, 2, 0, 4}; !slices.Equal(result.Order, want) {
			t.Errorf("%q: order = %v, want %v", prefix, result.Order, want)
		}
	}
}
