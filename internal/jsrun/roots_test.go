package jsrun_test

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// These tests describe n8n's documented Code-node behaviour in KilasFlow's
// own words; none of them copies an n8n documentation snippet.

// lineageItems are input items that each carry a distinct origin, the way
// the runner stamps every item it hands a node.
func lineageItems(count int) []workflow.Item {
	items := numbered(count)
	for index := range items {
		items[index].Paired = &workflow.PairedItem{SourceNodeID: "source", ItemIndex: index}
	}
	return items
}

func pairedIndexes(items []workflow.Item) string {
	var out []string
	for _, item := range items {
		switch {
		case item.Paired == nil:
			out = append(out, "-")
		case item.Paired.Lost:
			out = append(out, "lost")
		default:
			out = append(out, fmt.Sprint(item.Paired.ItemIndex))
		}
	}
	return strings.Join(out, ",")
}

func runTask(t *testing.T, task jsrun.Task) (jsrun.Result, error) {
	t.Helper()
	return newRunner().Run(context.Background(), task)
}

// `const items = $input.all()` is the commonest first line of an n8n body. It
// redeclares a root, which is legal only because the roots live in a scope
// that encloses the body.
func TestUserCodeMayRedeclareEveryRoot(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const items = $input.all()",
		"let $workflow = 'mine'",
		"var $env = 2",
		"const $ = (name) => name",
		"return items.map(item => ({ json: { n: item.json.n, workflow: $workflow, node: $('x') } }))",
	}, "\n"), Items: numbered(2)})
	if len(result.Items) != 2 || result.Items[1].JSON["workflow"] != "mine" || result.Items[1].JSON["node"] != "x" {
		t.Fatalf("items = %#v", result.Items)
	}
	perItem := mustRun(t, newRunner(), jsrun.Task{
		Source: "const $json = { copied: $input.item.json.n }\nconst $itemIndex = 'shadowed'\nreturn { json: $json }",
		Mode:   jsrun.ModeEachItem, Items: numbered(2),
	})
	if perItem.Items[1].JSON["copied"] != float64(1) {
		t.Fatalf("items = %#v", perItem.Items)
	}
}

func TestItemsAndInputAllAreTheSameObjects(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return [{ json: { same: items === $input.all() && items[0] === $input.first() && items[2] === $input.last() && $input.item === items[0] } }]",
		Items:  numbered(3),
	})
	if result.Items[0].JSON["same"] != true {
		t.Fatalf("items = %#v, want items and $input to be the same objects", result.Items)
	}
}

func TestMutatingItemsInPlaceAndReturningThemKeepsTheChange(t *testing.T) {
	input := lineageItems(3)
	result := mustRun(t, newRunner(), jsrun.Task{Source: "for (const item of items) item.json.seen = true\nreturn items", Items: input})
	if len(result.Items) != 3 || result.Items[2].JSON["seen"] != true {
		t.Fatalf("items = %#v", result.Items)
	}
	if got := pairedIndexes(result.Items); got != "0,1,2" {
		t.Fatalf("lineage = %s, want each item to keep its own", got)
	}
}

func TestJsonIsOnlyAvailablePerItem(t *testing.T) {
	_, err := runTask(t, jsrun.Task{Source: "return [{ json: { value: $json.n } }]", Items: numbered(1)})
	if err == nil || !strings.Contains(err.Error(), "$json is only available when the code runs once for each item") {
		t.Fatalf("Run() error = %v, want $json refused by name in all-items mode", err)
	}
	_, err = runTask(t, jsrun.Task{Source: "return { json: { count: items.length } }", Mode: jsrun.ModeEachItem, Items: numbered(1)})
	if err == nil || !strings.Contains(err.Error(), "items is only available when the code runs once for all items") {
		t.Fatalf("Run() error = %v, want items refused by name in per-item mode", err)
	}
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return { json: { value: $json.n, index: $itemIndex, same: $json === $input.item.json } }",
		Mode:   jsrun.ModeEachItem, Items: numbered(3),
	})
	if result.Items[2].JSON["value"] != float64(2) || result.Items[2].JSON["index"] != float64(2) || result.Items[2].JSON["same"] != true {
		t.Fatalf("items = %#v", result.Items)
	}
}

// A root the runtime does not support fails when it is used, in the one
// refusal sentence, even when the analyser could not see it coming.
func TestUnavailableRootsFailWithTheirName(t *testing.T) {
	for source, subject := range map[string]string{
		"return [{ json: { value: $jmespath({}, 'a') } }]":                                    "uses $jmespath",
		"return [{ json: { value: $prevNode.name } }]":                                        "uses $prevNode",
		"return [{ json: { value: $execution.customData } }]":                                 "uses $execution.customData",
		"return [{ json: { value: $secrets.vault } }]":                                        "uses $secrets",
		"return [{ json: { value: globalThis['$getWorkflow' + 'StaticData']('global') } }]":   "uses $getWorkflowStaticData",
		"const self = this\nreturn [{ json: { value: await self.helpers.httpRequest({}) } }]": "uses this.helpers.httpRequest",
	} {
		_, err := runTask(t, jsrun.Task{Source: source})
		if err == nil || !strings.Contains(err.Error(), "this node's code "+subject+", which this server does not run") {
			t.Errorf("%q: Run() error = %v, want it refused because it %s", source, err, subject)
		}
	}
}

// The Code node never reaches credentials, as in n8n: this.getCredentials is
// not a function, while this.helpers exists.
func TestThisHelpersExistsAndGetCredentialsDoesNot(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "const self = this\nreturn [{ json: { helpers: typeof self.helpers, credentials: typeof self.getCredentials } }]",
	})
	if result.Items[0].JSON["helpers"] != "object" || result.Items[0].JSON["credentials"] != "undefined" {
		t.Fatalf("items = %#v", result.Items)
	}
}

// The sandbox is exactly the ECMAScript built-ins plus these names. A global
// added without a decision fails here.
func TestTheSandboxExposesExactlyTheseGlobals(t *testing.T) {
	bare := jsrun.BareGlobalsForTest()
	added := func(mode jsrun.Mode) []string {
		result := mustRun(t, newRunner(), jsrun.Task{Source: "return { json: { names: Object.getOwnPropertyNames(globalThis) } }", Mode: mode, Items: numbered(1)})
		var names []string
		for _, name := range result.Items[0].JSON["names"].([]any) {
			if !slices.Contains(bare, name.(string)) {
				names = append(names, name.(string))
			}
		}
		slices.Sort(names)
		return names
	}
	shared := []string{"$", "$env", "$evaluateExpression", "$execution", "$getWorkflowStaticData", "$jmespath", "$node", "$nodeVersion", "$prevNode", "$runIndex", "$secrets", "$vars", "$workflow", "console"}
	for mode, own := range map[jsrun.Mode][]string{
		jsrun.ModeAllItems: {"$itemIndex", "$json"},
		jsrun.ModeEachItem: {"items"},
	} {
		want := append(slices.Clone(shared), own...)
		slices.Sort(want)
		if got := added(mode); !slices.Equal(got, want) {
			t.Errorf("%s: the runtime adds %v, want exactly %v", mode, got, want)
		}
	}
}

func TestAPlainObjectIsWrappedAsAnItem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "return [{ a: 1 }, { json: { b: 2 } }]"})
	if len(result.Items) != 2 || result.Items[0].JSON["a"] != float64(1) || result.Items[1].JSON["b"] != float64(2) {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestASingleObjectIsWrappedInAllItemsMode(t *testing.T) {
	for _, source := range []string{"return { json: { a: 1 } }", "return { a: 1 }"} {
		result := mustRun(t, newRunner(), jsrun.Task{Source: source})
		if len(result.Items) != 1 || result.Items[0].JSON["a"] != float64(1) {
			t.Fatalf("%q: items = %#v, want the one object as one item", source, result.Items)
		}
	}
}

func TestPerItemModeRefusesAnArray(t *testing.T) {
	_, err := runTask(t, jsrun.Task{Source: "return [$json]", Mode: jsrun.ModeEachItem, Items: numbered(2)})
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "must return one object [for item 0]") {
		t.Fatalf("Run() error = %v, want the list refused for item 0", err)
	}
}

func TestExplicitPairedItemWinsOverInference(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return [{ json: {}, pairedItem: 2 }, { json: {}, pairedItem: { item: 0 } }, { json: {}, pairedItem: [{ item: 1 }, { item: 2 }] }, items[1]]",
		Items:  lineageItems(3),
	})
	if got := pairedIndexes(result.Items); got != "2,0,lost,1" {
		t.Fatalf("lineage = %s, want the explicit pairing, lost for several sources, and identity for the last", got)
	}
	_, err := runTask(t, jsrun.Task{Source: "return [{ json: {}, pairedItem: 7 }]", Items: lineageItems(2)})
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "pointing at item 7") {
		t.Fatalf("Run() error = %v, want a pairedItem outside the input refused", err)
	}
}

// An item the code returns keeps the lineage of the input item it is, however
// the list was reordered or filtered. Position alone would pair a reversed
// list with the wrong items.
func TestReturnedInputItemsKeepTheirOwnLineage(t *testing.T) {
	for source, want := range map[string]string{
		"return items.reverse()":                                    "2,1,0",
		"return items.filter(item => item.json.n !== 1)":            "0,2",
		"return items.map(item => ({ json: item.json })).reverse()": "2,1,0",
		"return items.map(item => ({ json: { copy: 1 } }))":         "-,-,-",
	} {
		result := mustRun(t, newRunner(), jsrun.Task{Source: source, Items: lineageItems(3)})
		if got := pairedIndexes(result.Items); got != want {
			t.Errorf("%q: lineage = %s, want %s", source, got, want)
		}
	}
}

func TestPerItemOutputPairsWithItsItem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "return { json: { n: $json.n } }", Mode: jsrun.ModeEachItem, Items: lineageItems(3)})
	if got := pairedIndexes(result.Items); got != "0,1,2" {
		t.Fatalf("lineage = %s, want each output paired with its own item", got)
	}
}

func fileItems() []workflow.Item {
	return []workflow.Item{{
		JSON:   map[string]any{"name": "report"},
		Binary: map[string]workflow.BinaryRef{"file": {ID: "bin-1", FileName: "report.pdf", MediaType: "application/pdf", Size: 1234}},
	}}
}

func TestBinaryCrossesAsMetadataOnly(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "const file = items[0].binary.file\nreturn [{ json: { file, hasData: 'data' in file } }]",
		Items:  fileItems(),
	})
	file := result.Items[0].JSON["file"].(map[string]any)
	if file["id"] != "bin-1" || file["fileName"] != "report.pdf" || file["mimeType"] != "application/pdf" || file["fileExtension"] != "pdf" || file["fileSize"] != float64(1234) {
		t.Fatalf("binary metadata = %#v", file)
	}
	if result.Items[0].JSON["hasData"] != false {
		t.Fatal("the file's bytes crossed into the VM")
	}

	kept := mustRun(t, newRunner(), jsrun.Task{Source: "return items", Items: fileItems()})
	if kept.Items[0].Binary["file"].ID != "bin-1" {
		t.Fatalf("returning the item did not pass its file on: %#v", kept.Items[0].Binary)
	}
	dropped := mustRun(t, newRunner(), jsrun.Task{Source: "return items.map(item => ({ json: item.json }))", Items: fileItems()})
	if len(dropped.Items[0].Binary) != 0 {
		t.Fatalf("a file the code did not return was passed on: %#v", dropped.Items[0].Binary)
	}
}

func TestReturningAnUnknownBinaryIsRefused(t *testing.T) {
	_, err := runTask(t, jsrun.Task{Source: "return [{ json: {}, binary: { file: { id: 'someone-elses' } } }]", Items: fileItems()})
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "naming a file this node was not given") {
		t.Fatalf("Run() error = %v, want a forged file refused", err)
	}
}

func TestConsoleCapturesEveryLevel(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"console.log('plain', 1, true, null, undefined)",
		"console.info('%s has %d items (%i%%)', 'list', 3, 42.7)",
		"console.warn({ a: 1, list: [1, 'two'] })",
		"console.error(new TypeError('broken'))",
		"console.debug('%j', { json: true })",
		"return []",
	}, "\n")})
	var got []string
	for _, line := range result.Console {
		got = append(got, line.Level+": "+strings.SplitN(line.Text, "\n", 2)[0])
	}
	want := []string{
		"log: plain 1 true null undefined",
		"info: list has 3 items (42%)",
		"warn: { a: 1, list: [ 1, 'two' ] }",
		"error: TypeError: broken",
		`debug: {"json":true}`,
	}
	if !slices.Equal(got, want) {
		t.Fatalf("console =\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
}

func TestConsoleIsCappedWithAMarker(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "for (let n = 0; n < 1000; n++) console.log('line ' + n + ' ' + 'x'.repeat(40))\nreturn []",
		Limits: jsrun.Limits{MaxConsoleBytes: 1024},
	})
	size := 0
	for _, line := range result.Console {
		size += len(line.Text) + 1
	}
	if !result.ConsoleTruncated || size > 1024 || len(result.Console) == 0 {
		t.Fatalf("truncated = %v, %d lines in %d bytes; want at most 1024 bytes kept and the rest marked dropped", result.ConsoleTruncated, len(result.Console), size)
	}
}

func TestConsoleInspectsCircularObjects(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "const a = { x: 1 }\na.self = a\nconsole.log(a)\nreturn []"})
	if len(result.Console) != 1 || result.Console[0].Text != "{ x: 1, self: [Circular] }" {
		t.Fatalf("console = %#v", result.Console)
	}
}

func TestConsoleIsKeptWhenTheCodeFails(t *testing.T) {
	result, err := runTask(t, jsrun.Task{Source: "console.log('about to fail')\nthrow new Error('failed')"})
	if err == nil || len(result.Console) != 1 || result.Console[0].Text != "about to fail" {
		t.Fatalf("Run() = %#v, %v; want the failure and what was printed before it", result.Console, err)
	}
}

func nodeRoots() jsrun.Roots {
	nodes := map[string]jsrun.NodeView{
		"Webhook": {Items: []map[string]any{{"v": "a"}, {"v": "b"}, {"v": "c"}}, Params: map[string]any{"path": "hook"}},
	}
	return jsrun.Roots{
		Workflow:  jsrun.WorkflowInfo{ID: "wf-1", Name: "Orders", Active: true},
		Execution: jsrun.ExecutionInfo{ID: "ex-1", Mode: "manual", ResumeURL: "https://kf.example/resume"},
		Env:       map[string]string{"REGION": "eu"},
		RunIndex:  2,
		Node: func(name string) (jsrun.NodeView, bool) {
			view, ok := nodes[name]
			return view, ok
		},
		// Item N of the input descends from Webhook's item N, except item 1,
		// whose lineage is unknown.
		Pair: func(name string, index int) (int, string) {
			if index == 1 {
				return -1, "no single item to pair with"
			}
			return index, ""
		},
	}
}

func TestNodeRootsReadWhatTheRunnerHandsThem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const hook = $('Webhook')",
		"return [{ json: {",
		"  count: hook.all().length, first: hook.first().json.v, last: hook.last().json.v,",
		"  item: hook.item.json.v, matching: hook.itemMatching(2).json.v, params: hook.params,",
		"  executed: hook.isExecuted, missing: $('Nope').isExecuted, legacy: $node['Webhook'].json.v,",
		"  workflow: $workflow, execution: $execution.id, resume: $execution.resumeUrl,",
		"  env: $env.REGION, vars: $vars, run: $runIndex,",
		"} }]",
	}, "\n"), Roots: nodeRoots(), Items: numbered(3)})
	got := result.Items[0].JSON
	for key, want := range map[string]any{
		"count": float64(3), "first": "a", "last": "c", "item": "a", "matching": "c", "executed": true, "missing": false,
		"legacy": "a", "execution": "ex-1", "resume": "https://kf.example/resume", "env": "eu", "run": float64(2),
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}
	if fmt.Sprint(got["params"]) != "map[path:hook]" || fmt.Sprint(got["workflow"]) != "map[active:true id:wf-1 name:Orders]" || fmt.Sprint(got["vars"]) != "map[]" {
		t.Errorf("params %v, workflow %v, vars %v", got["params"], got["workflow"], got["vars"])
	}
}

func TestNodeItemIsPairedPerItem(t *testing.T) {
	result, err := runTask(t, jsrun.Task{
		Source: "return { json: { from: $('Webhook').item.json.v } }",
		Mode:   jsrun.ModeEachItem, Roots: nodeRoots(), Items: numbered(3),
	})
	if err == nil || !strings.Contains(err.Error(), "no single item to pair with [line 1, for item 1]") {
		t.Fatalf("Run() error = %v, want item 1's unknown lineage named", err)
	}
	if len(result.Items) != 1 || result.Items[0].JSON["from"] != "a" {
		t.Fatalf("items before the failure = %#v", result.Items)
	}
}

func TestItemMatchingPairsTheNamedInputItem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return items.map((item, index) => ({ json: { from: index === 1 ? null : $('Webhook').itemMatching(index).json.v } }))",
		Roots:  nodeRoots(), Items: numbered(3),
	})
	if result.Items[2].JSON["from"] != "c" {
		t.Fatalf("items = %#v", result.Items)
	}
}

func TestAllWithABranchOrRunIsANamedError(t *testing.T) {
	_, err := runTask(t, jsrun.Task{Source: "return $('Webhook').all(1)", Roots: nodeRoots()})
	if err == nil || !strings.Contains(err.Error(), "which this server does not run") {
		t.Fatalf("Run() error = %v, want the branch refused", err)
	}
	_, err = runTask(t, jsrun.Task{Source: "return $('Nope').first()", Roots: nodeRoots()})
	if err == nil || !strings.Contains(err.Error(), `node "Nope" has not run in this execution`) {
		t.Fatalf("Run() error = %v, want the missing node named", err)
	}
}

func TestADynamicUnicodePropertyRegExpIsRefusedAtRunTime(t *testing.T) {
	for _, source := range []string{
		"const flags = 'u'\nreturn [{ json: { ok: new RegExp('\\\\p{L}', flags).test('é') } }]",
		"const pattern = '\\\\P{Lu}'\nreturn [{ json: { ok: RegExp(pattern, 'gu').test('a') } }]",
	} {
		_, err := runTask(t, jsrun.Task{Source: source})
		if err == nil || !strings.Contains(err.Error(), `uses a regular expression with \p{…} property escapes, which this server does not run`) {
			t.Errorf("%q: Run() error = %v, want it refused", source, err)
		}
	}
	result := mustRun(t, newRunner(), jsrun.Task{Source: "const pattern = 'a+'\nconst rx = new RegExp(pattern, 'g')\nreturn [{ json: { ok: rx.test('aa'), regexp: rx instanceof RegExp } }]"})
	if result.Items[0].JSON["ok"] != true || result.Items[0].JSON["regexp"] != true {
		t.Fatalf("an ordinary RegExp misbehaved: %#v", result.Items)
	}
}
