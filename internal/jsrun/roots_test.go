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
		"const $json = 'first', $binary = {}, $itemIndex = 'index', $position = 'position'",
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

// In all-items mode the per-item roots read the first input item, as n8n's
// do: n8n builds them for item 0 and hands them to all-items code too.
// $json is that item's own json, $binary a copy of its files' metadata.
func TestAllItemsCodeReadsTheFirstItemThroughThePerItemRoots(t *testing.T) {
	input := numbered(2)
	input[0].Binary = map[string]workflow.BinaryRef{"file": {ID: "bin-1", FileName: "report.pdf", MediaType: "application/pdf", Size: 1234}}
	result := mustRun(t, newRunner(), jsrun.Task{Items: input, Source: strings.Join([]string{
		"const copy = $binary.file === items[0].binary.file",
		"$binary.file.fileName = 'changed.pdf'",
		"$json.touched = true",
		"return [{ json: { n: $json.n, index: $itemIndex, position: $position, same: $json === items[0].json,",
		"  touched: items[0].json.touched, name: items[0].binary.file.fileName, copy, type: $binary.file.mimeType, keys: Object.keys($binary.file) } }]",
	}, "\n")})
	got := result.Items[0].JSON
	if got["n"] != float64(0) || got["index"] != float64(0) || got["position"] != float64(0) || got["same"] != true || got["touched"] != true {
		t.Fatalf("roots = %#v, want the first item's json, index 0 and the same object as items[0].json", got)
	}
	if got["copy"] != false || got["name"] != "report.pdf" || got["type"] != "application/pdf" || fmt.Sprint(got["keys"]) != "[id fileName mimeType fileExtension fileSize]" {
		t.Fatalf("$binary = %#v, want a copy of the first item's file metadata", got)
	}

	// An item with no files has an empty $binary; no items leave both
	// undefined rather than failing.
	plain := mustRun(t, newRunner(), jsrun.Task{Items: numbered(1),
		Source: "return [{ json: { binary: JSON.stringify($binary) } }]"})
	empty := mustRun(t, newRunner(), jsrun.Task{Items: []workflow.Item{},
		Source: "return [{ json: { json: typeof $json, binary: typeof $binary, index: $itemIndex } }]"})
	if plain.Items[0].JSON["binary"] != "{}" || fmt.Sprint(empty.Items[0].JSON) != "map[binary:undefined index:0 json:undefined]" {
		t.Fatalf("items = %#v and %#v", plain.Items, empty.Items)
	}
}

func TestPerItemCodeReadsItsOwnItemThroughTheRoots(t *testing.T) {
	input := numbered(3)
	input[2].Binary = map[string]workflow.BinaryRef{"file": {ID: "bin-2", FileName: "b.txt", MediaType: "text/plain", Size: 2}}
	result := mustRun(t, newRunner(), jsrun.Task{
		Source: "return { json: { value: $json.n, index: $itemIndex, position: $position, same: $json === $input.item.json, file: ($binary.file || {}).fileName } }",
		Mode:   jsrun.ModeEachItem, Items: input,
	})
	got := result.Items[2].JSON
	if got["value"] != float64(2) || got["index"] != float64(2) || got["position"] != float64(2) || got["same"] != true || got["file"] != "b.txt" {
		t.Fatalf("items = %#v", result.Items)
	}
	if _, has := result.Items[0].JSON["file"]; has {
		t.Fatalf("item 0 = %#v, want no file: its $binary is its own", result.Items[0].JSON)
	}
	_, err := runTask(t, jsrun.Task{Source: "return { json: { count: items.length } }", Mode: jsrun.ModeEachItem, Items: numbered(1)})
	if err == nil || !strings.Contains(err.Error(), "items is only available when the code runs once for all items") {
		t.Fatalf("Run() error = %v, want items refused by name in per-item mode", err)
	}
}

// A root the runtime does not support fails when it is used, in the one
// refusal sentence, even when the analyser could not see it coming.
func TestUnavailableRootsFailWithTheirName(t *testing.T) {
	for source, subject := range map[string]string{
		"return [{ json: { value: $jmespath({}, 'a') } }]":                                "uses $jmespath",
		"return [{ json: { value: $prevNode.name } }]":                                    "uses $prevNode",
		"return [{ json: { value: $input.params.operation } }]":                           "uses $input.params",
		"return [{ json: { value: $input.context.noItemsLeft } }]":                        "uses $input.context",
		"return [{ json: { value: $execution.customData } }]":                             "uses $execution.customData",
		"return [{ json: { value: $secrets.vault } }]":                                    "uses $secrets",
		"const self = this\nreturn [{ json: { value: await self.helpers.request({}) } }]": "uses this.helpers.request",
		"return [{ json: { value: await this.helpers['copy' + 'BinaryFile']() } }]":       "uses this.helpers.copyBinaryFile",
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
	// Intl is new too: goja has none, and the runtime provides it. The Luxon
	// globals are there whether or not the code names Luxon; the library
	// itself loads only when one is first read.
	shared := []string{"$", "$env", "$items", "$evaluateExpression", "$execution", "$getWorkflowStaticData", "$jmespath", "$node", "$nodeVersion", "$now", "$prevNode", "$runIndex", "$secrets", "$today", "$vars", "$workflow",
		"DateTime", "Duration", "Info", "Interval", "Intl", "Settings", "console", "crypto", "require",
		"Buffer", "DOMException", "TextDecoder", "TextEncoder", "URL", "URLSearchParams", "atob", "btoa", "queueMicrotask", "structuredClone",
		"setTimeout", "setInterval", "setImmediate", "clearTimeout", "clearInterval", "clearImmediate"}
	// The other mode's roots are not globals at all, as in n8n.
	for _, mode := range []jsrun.Mode{jsrun.ModeAllItems, jsrun.ModeEachItem} {
		want := slices.Clone(shared)
		slices.Sort(want)
		if got := added(mode); !slices.Equal(got, want) {
			t.Errorf("%s: the runtime adds %v, want exactly %v", mode, got, want)
		}
	}
}

// items, the one root per-item code does not have, is undefined there, as in
// n8n, so code written for both modes can ask `typeof`; using it uncaught
// says what to use instead. All-items code has every per-item root.
func TestTheOtherModesRootsAreUndefinedAndSayWhatToUseInstead(t *testing.T) {
	each := mustRun(t, newRunner(), jsrun.Task{Mode: jsrun.ModeEachItem, Items: numbered(1),
		Source: "return { json: { items: typeof items, json: typeof $json } }"})
	all := mustRun(t, newRunner(), jsrun.Task{Items: numbered(1),
		Source: "return [{ json: { json: typeof $json, index: typeof $itemIndex, items: typeof items } }]"})
	if got := fmt.Sprint(each.Items[0].JSON, all.Items[0].JSON); got != "map[items:undefined json:object] map[index:number items:object json:object]" {
		t.Fatalf("typeof = %s", got)
	}
	_, err := runTask(t, jsrun.Task{Mode: jsrun.ModeEachItem, Items: numbered(1), Source: "return { json: { n: items.length } }"})
	if err == nil || !strings.Contains(err.Error(), "ReferenceError: items is only available when the code runs once for all items; use $input.item or $json") {
		t.Fatalf("Run() error = %v, want the advice", err)
	}
}

// Per-item code also has `item`, the current input item itself, as n8n's
// does; all-items code has none, as in n8n.
func TestPerItemCodeHasTheCurrentItemAsItem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Mode: jsrun.ModeEachItem, Items: numbered(2),
		Source: "return { json: { n: item.json.n, same: item === $input.item && item.json === $json } }"})
	if result.Items[1].JSON["n"] != float64(1) || result.Items[1].JSON["same"] != true {
		t.Fatalf("items = %#v", result.Items)
	}
	all := mustRun(t, newRunner(), jsrun.Task{Items: numbered(1), Source: "return [{ json: { item: typeof item } }]"})
	if all.Items[0].JSON["item"] != "undefined" {
		t.Fatalf("items = %#v, want no item root in all-items code", all.Items)
	}
	redeclared := mustRun(t, newRunner(), jsrun.Task{Mode: jsrun.ModeEachItem, Items: numbered(1),
		Source: "const item = $input.item\nreturn item"})
	if redeclared.Items[0].JSON["n"] != float64(0) {
		t.Fatalf("items = %#v", redeclared.Items)
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

// n8n drops an item whose per-item code returns null, which is how a Code
// node filters; returning nothing at all is still refused.
func TestReturningNullInPerItemModeDropsTheItem(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "return $itemIndex % 2 ? null : $json", Mode: jsrun.ModeEachItem, Items: numbered(4)})
	if len(result.Items) != 2 {
		t.Fatalf("items = %#v, want the two even ones", result.Items)
	}
	_, err := runTask(t, jsrun.Task{Source: "if ($itemIndex === 1) return\nreturn $json", Mode: jsrun.ModeEachItem, Items: numbered(2)})
	if !errors.Is(err, jsrun.ErrInvalidReturn) || !strings.Contains(err.Error(), "[for item 1]") {
		t.Fatalf("Run() error = %v, want undefined refused for item 1", err)
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

// Items built from nothing descend from the node's only input item when there
// was just one, which is n8n's own rule for an output without pairedItem. A
// Code node that turns one item into several is the common case: downstream,
// `$('Code').item` must still find the item each one came from.
func TestItemsBuiltFromASingleInputItemDescendFromIt(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: "return [1, 2, 3].map(n => ({ json: { n } }))", Items: lineageItems(1)})
	if got := pairedIndexes(result.Items); got != "0,0,0" {
		t.Fatalf("lineage = %s, want every item paired with the one input item", got)
	}
	result = mustRun(t, newRunner(), jsrun.Task{Source: "return [1, 2, 3].map(n => ({ json: { n } }))", Items: lineageItems(2)})
	if got := pairedIndexes(result.Items); got != "-,-,-" {
		t.Fatalf("lineage = %s, want no guess when there were several input items", got)
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
		// An IF node on its third run: two items went out on true, one on
		// false. The running node hangs off false.
		"IF": {Items: []map[string]any{{"v": "t1"}, {"v": "t2"}, {"v": "f1"}}, Outputs: []int{2, 1}, Branch: 1, RunIndex: 2},
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

// .all(branch, run) and $items(name, output, run) read one output of the
// node's latest run, the only run kept, as n8n reads them: named by its
// number or as n8n's -1, the latest run is read; an earlier one is refused
// rather than answered with the latest, and a run or output the node does
// not have is an error. $items reads output 0 by default; .all(), .first()
// and .last() with no branch read the output the running node is connected
// to, as n8n's do, and take the same branch and run otherwise.
func TestAllAndItemsReadOneOutputOfTheLatestRun(t *testing.T) {
	roots := nodeRoots()
	roots.RunIndex = 2
	result := mustRun(t, newRunner(), jsrun.Task{Roots: roots, Source: strings.Join([]string{
		"const values = (list) => list.map((item) => item.json.v).join(',')",
		"return [{ json: {",
		"  items: values($items('IF')), second: values($items('IF', 1)), lockstep: values($items('IF', 1, $runIndex)), last: values($items('IF', null, -1)),",
		"  all: values($('IF').all()), branch: values($('IF').all(1)), latest: values($('IF').all(0, 2)), same: $items('IF', 1) === $('IF').all(1, -1),",
		"  first: $('IF').first().json.v, lastItem: $('IF').last().json.v, firstTrue: $('IF').first(0).json.v, lastTrue: $('IF').last(0, -1).json.v,",
		"} }]",
	}, "\n")})
	got := result.Items[0].JSON
	for key, want := range map[string]any{
		"items": "t1,t2", "second": "f1", "lockstep": "f1", "last": "t1,t2", "all": "f1", "branch": "f1", "latest": "t1,t2", "same": true,
		"first": "f1", "lastItem": "f1", "firstTrue": "t1", "lastTrue": "t2",
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}

	for source, want := range map[string]string{
		"return $items('IF', 0, 0)":      `this node's code reads run 0 of node "IF", but only its latest run, 2, is kept, which this server does not run`,
		"return $('IF').all(0, 1)":       `this node's code reads run 1 of node "IF", but only its latest run, 2, is kept, which this server does not run`,
		"return $items('IF', 0, 3)":      `$items() names run 3 of node "IF", which has no such run`,
		"return $items('IF', 0, null)":   `$items() names run null of node "IF", which has no such run`,
		"return $items('IF', 2)":         `$items() names output 2 of node "IF", which has no such output`,
		"return $('IF').all(2)":          `$("IF").all() names output 2 of node "IF", which has no such output`,
		"return $('IF').last(2)":         `$("IF").last() names output 2 of node "IF", which has no such output`,
		"return $('IF').first(0, 1)":     `this node's code reads run 1 of node "IF", but only its latest run, 2, is kept, which this server does not run`,
		"return $('Webhook').all(1)":     `$("Webhook").all() names output 1 of node "Webhook", which has no such output`,
		"return $items('Webhook', 0, 1)": `$items() names run 1 of node "Webhook", which has no such run`,
		"return $('Nope').first()":       `node "Nope" has not run in this execution`,
	} {
		_, err := runTask(t, jsrun.Task{Source: source, Roots: roots})
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: Run() error = %v, want %q", source, err, want)
		}
	}
}

// $items is n8n's older spelling of the same reads: with no name the node's
// own input, with one that node's items, as $('Name').all() gives them.
func TestTheLegacyItemsRootReadsTheInputOrANamedNode(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const hook = $items('Webhook')",
		"return [{ json: { input: $items() === items && $items(null) === items, count: $items().length, same: hook === $('Webhook').all(),",
		"  values: hook.map((item) => item.json.v), first: $items('Webhook', 0, 0)[0].json.v, nullOutput: $items('Webhook', null).length } }]",
	}, "\n"), Roots: nodeRoots(), Items: numbered(2)})
	got := result.Items[0].JSON
	if got["input"] != true || got["count"] != float64(2) || got["same"] != true || fmt.Sprint(got["values"]) != "[a b c]" || got["first"] != "a" || got["nullOutput"] != float64(3) {
		t.Fatalf("items = %#v", got)
	}
	perItem := mustRun(t, newRunner(), jsrun.Task{Mode: jsrun.ModeEachItem, Roots: nodeRoots(), Items: numbered(2),
		Source: "return { json: { count: $items().length, hook: $items('Webhook')[$itemIndex].json.v } }"})
	if perItem.Items[1].JSON["count"] != float64(2) || perItem.Items[1].JSON["hook"] != "b" {
		t.Fatalf("items = %#v", perItem.Items)
	}

	_, err := runTask(t, jsrun.Task{Source: "return $items('Nope')", Roots: nodeRoots()})
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
