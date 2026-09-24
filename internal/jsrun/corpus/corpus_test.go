package corpus

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// syncCommand materialises the corpus. A test that skips names it, so nobody
// has to go looking.
const syncCommand = "scripts/code-corpus-sync.sh (or make js-corpus)"

// dirEnv overrides where the fetched fixtures are read from.
const dirEnv = "KILASFLOW_CODE_CORPUS_DIR"

// fixtureDir is where scripts/code-corpus-sync.sh writes the fixtures.
func fixtureDir() string {
	if override := strings.TrimSpace(os.Getenv(dirEnv)); override != "" {
		return override
	}
	return "fixtures"
}

// fixture is one template as the sync script keeps it: its code-bearing
// nodes and what a run of them needs, and nothing else.
type fixture struct {
	Template    int                                `json:"template"`
	Nodes       []codeNode                         `json:"nodes"`
	NodeNames   []string                           `json:"nodeNames"`
	Connections map[string]map[string][][]nodeLink `json:"connections"`
	PinData     map[string][]json.RawMessage       `json:"pinData"`
}

// codeNode is a JavaScript Code node or a Sort node with a code comparator.
// Index is its position in the template's node list, which names it in the
// baseline without naming it in the author's words.
type codeNode struct {
	Index       int            `json:"index"`
	Name        string         `json:"name"`
	Type        string         `json:"type"`
	TypeVersion float64        `json:"typeVersion"`
	Parameters  map[string]any `json:"parameters"`
}

type nodeLink struct {
	Node string `json:"node"`
}

// body is one piece of JavaScript the runtime is measured on, with the task
// that runs it.
type body struct {
	// Key names the body in the baseline: "<template>/<node index>".
	Key string
	// HasSource is false for a Code node the template carries with no code
	// at all. n8n's default is an empty body, which fails there as it does
	// here, so there is nothing of the author's to measure.
	HasSource bool
	// Stubbed says the input was shaped from the body rather than taken from
	// the template's pinned data.
	Stubbed bool
	Task    jsrun.Task
	// Views are the other nodes' items the roots answer $('Name') with,
	// kept so the differential run hands Node the same ones.
	Views map[string][]map[string]any
}

// loadFixtures reads every fixture in dir, sorted by template id. A missing
// directory is no fixtures, not an error: a clean clone has none.
func loadFixtures(t *testing.T, dir string) []fixture {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading %s: %v", dir, err)
	}
	var fixtures []fixture
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			t.Fatalf("reading %s: %v", entry.Name(), err)
		}
		var current fixture
		if err := json.Unmarshal(raw, &current); err != nil {
			t.Fatalf("parsing %s: %v", entry.Name(), err)
		}
		fixtures = append(fixtures, current)
	}
	sort.Slice(fixtures, func(i, j int) bool { return fixtures[i].Template < fixtures[j].Template })
	return fixtures
}

// bodies turns the fixtures into the bodies to measure, in a stable order.
func bodies(fixtures []fixture, keyPrefix string) []body {
	var out []body
	for _, current := range fixtures {
		for _, node := range current.Nodes {
			key := keyPrefix + strconv.Itoa(current.Template) + "/" + strconv.Itoa(node.Index)
			out = append(out, current.body(node, key))
		}
	}
	return out
}

// body builds one node's task: its source and mode as the importer would
// read them, its input, and the roots it reaches other nodes through.
func (current fixture) body(node codeNode, key string) body {
	source, mode, present := "", jsrun.ModeAllItems, false
	if node.Type == "n8n-nodes-base.sort" {
		mode = jsrun.ModeComparator
		source, present = node.Parameters["code"].(string)
	} else {
		source, present = node.Parameters["jsCode"].(string)
		if configured, _ := node.Parameters["mode"].(string); configured == string(jsrun.ModeEachItem) {
			mode = jsrun.ModeEachItem
		}
	}

	stub := stubItems(source)
	input, pinned := current.pinned(current.upstream(node.Name))
	if !pinned {
		input = stub
	}
	views := map[string][]map[string]any{}
	for _, name := range current.NodeNames {
		if items, ok := current.pinned(name); ok {
			views[name] = jsonOf(items)
		} else {
			views[name] = jsonOf(stub)
		}
	}
	return body{
		Key:       key,
		HasSource: present,
		Stubbed:   !pinned,
		Views:     views,
		Task: jsrun.Task{
			Source: source,
			Mode:   mode,
			Items:  input,
			Roots:  corpusRoots(views),
		},
	}
}

// corpusRoots are the roots every body runs with: a fixed workflow and
// execution, UTC, and the other nodes' synthesised items. .item pairs by
// position, clamped to the node's last item, which is what a straight chain
// of one-item-per-item nodes answers.
func corpusRoots(views map[string][]map[string]any) jsrun.Roots {
	return jsrun.Roots{
		Workflow:  jsrun.WorkflowInfo{ID: "corpus", Name: "Code corpus", Active: true},
		Execution: jsrun.ExecutionInfo{ID: "1", Mode: "trigger"},
		Node: func(name string) (jsrun.NodeView, bool) {
			items, ok := views[name]
			if !ok {
				return jsrun.NodeView{}, false
			}
			return jsrun.NodeView{Items: items, Params: map[string]any{}}, true
		},
		Pair: func(name string, index int) (int, string) { return pairIndex(views, name, index) },
	}
}

func pairIndex(views map[string][]map[string]any, name string, index int) (int, string) {
	items, ok := views[name]
	if !ok {
		return -1, fmt.Sprintf("node %q has not run in this execution", name)
	}
	if len(items) == 0 {
		return -1, fmt.Sprintf("node %q produced no items", name)
	}
	if index >= len(items) {
		index = len(items) - 1
	}
	return index, ""
}

// upstream is the node whose main output feeds name, preferring one with
// pinned data, or "" when nothing does.
func (current fixture) upstream(name string) string {
	sources := make([]string, 0, len(current.Connections))
	for source := range current.Connections {
		sources = append(sources, source)
	}
	sort.Strings(sources)
	found := ""
	for _, source := range sources {
		for _, port := range current.Connections[source]["main"] {
			for _, link := range port {
				if link.Node != name {
					continue
				}
				if _, pinned := current.PinData[source]; pinned {
					return source
				}
				if found == "" {
					found = source
				}
			}
		}
	}
	return found
}

// pinned is a node's pinned items. n8n has written pinned items both as
// {json: …} and as the bare object, so both are read.
func (current fixture) pinned(name string) ([]workflow.Item, bool) {
	raw, ok := current.PinData[name]
	if !ok || name == "" || len(raw) == 0 {
		return nil, false
	}
	items := make([]workflow.Item, 0, len(raw))
	for _, entry := range raw {
		var object map[string]any
		if err := json.Unmarshal(entry, &object); err != nil || object == nil {
			return nil, false
		}
		if inner, ok := object["json"].(map[string]any); ok {
			object = inner
		}
		items = append(items, workflow.Item{JSON: object})
	}
	return items, true
}

func jsonOf(items []workflow.Item) []map[string]any {
	out := make([]map[string]any, len(items))
	for index, item := range items {
		out[index] = item.JSON
	}
	return out
}

// fieldPath finds the fields a body reads off an item: `$json.a.b`,
// `.json.a`, `.json['a b']`, with optional chaining.
var (
	fieldPath    = regexp.MustCompile(`(?:\$json|\.json)((?:\??\.[A-Za-z_$][\w$]*|\[\s*(?:'[^']*'|"[^"]*")\s*\])+)(\s*\()?`)
	fieldSegment = regexp.MustCompile(`\??\.([A-Za-z_$][\w$]*)|\[\s*(?:'([^']*)'|"([^"]*)")\s*\]`)
)

// stubValue is what a field the body reads holds in a synthesised item.
const stubValue = "sample"

// stubItems is the input a body gets when its template pinned none: one item
// shaped from the fields the body reads, each holding a string, nested as the
// body nests them. A path that ends in a call is a method of the value, not a
// field, so the call is left off. It is a stand-in for real data, so a body
// that fails on it may only have failed on the stand-in; the scoreboard
// reports those failures by what went wrong, for exactly that reason.
func stubItems(source string) []workflow.Item {
	root := map[string]any{}
	for _, match := range fieldPath.FindAllStringSubmatch(source, -1) {
		var path []string
		for _, segment := range fieldSegment.FindAllStringSubmatch(match[1], -1) {
			path = append(path, segment[1]+segment[2]+segment[3])
		}
		if match[2] != "" && len(path) > 0 {
			path = path[:len(path)-1]
		}
		if len(path) > 0 && path[len(path)-1] == "length" {
			path = path[:len(path)-1]
		}
		place(root, path)
	}
	return []workflow.Item{{JSON: root}}
}

// place writes a stub value at path, turning a leaf into an object when a
// longer path runs through it.
func place(root map[string]any, path []string) {
	current := root
	for index, name := range path {
		last := index == len(path)-1
		existing, present := current[name]
		if last {
			if !present {
				current[name] = stubValue
			}
			return
		}
		next, isObject := existing.(map[string]any)
		if !isObject {
			next = map[string]any{}
			current[name] = next
		}
		current = next
	}
}

// ---- Tests of the instrument itself -------------------------------------------

// controlDir holds KilasFlow-authored bodies in the fixture format. They are
// always present, so the instrument is exercised on a clean clone, and they
// must parse, pass analysis and run on every build.
const controlDir = "testdata/control"

func TestControlBodiesRun(t *testing.T) {
	fixtures := loadFixtures(t, controlDir)
	if len(fixtures) == 0 {
		t.Fatal("no control fixtures; the instrument has nothing to prove itself on")
	}
	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.DefaultLimits()})
	for _, current := range bodies(fixtures, "control/") {
		measured := measure(t, runner, current)
		if !measured.Parsed || !measured.Accepted || !measured.Ran {
			t.Errorf("%s: parsed=%t accepted=%t ran=%t (%s)", current.Key, measured.Parsed, measured.Accepted, measured.Ran, measured.detail)
		}
	}
}

func TestStubItemsFollowTheFieldsABodyReads(t *testing.T) {
	source := strings.Join([]string{
		"const who = $json.user.name.toUpperCase();",
		"const tags = items[0].json['the tags'].length;",
		"const deep = $input.first().json?.body?.message?.text;",
		"return $json.user.id;",
	}, "\n")
	got, err := json.Marshal(stubItems(source)[0].JSON)
	if err != nil {
		t.Fatal(err)
	}
	want := `{"body":{"message":{"text":"sample"}},"the tags":"sample","user":{"id":"sample","name":"sample"}}`
	if string(got) != want {
		t.Errorf("stub = %s, want %s", got, want)
	}
}

func TestABodyReadsItsUpstreamNodesPinnedData(t *testing.T) {
	current := fixture{
		Template:  1,
		Nodes:     []codeNode{{Index: 1, Name: "Code", Type: "n8n-nodes-base.code", Parameters: map[string]any{"jsCode": "return $json.x", "mode": "runOnceForEachItem"}}},
		NodeNames: []string{"Trigger", "Code"},
		Connections: map[string]map[string][][]nodeLink{
			"Trigger": {"main": {{{Node: "Code"}}}},
		},
		PinData: map[string][]json.RawMessage{"Trigger": {json.RawMessage(`{"json":{"x":1}}`), json.RawMessage(`{"x":2}`)}},
	}
	measured := bodies([]fixture{current}, "")[0]
	if measured.Key != "1/1" || measured.Stubbed || measured.Task.Mode != jsrun.ModeEachItem {
		t.Fatalf("body = %+v", measured)
	}
	if len(measured.Task.Items) != 2 || measured.Task.Items[0].JSON["x"] != float64(1) || measured.Task.Items[1].JSON["x"] != float64(2) {
		t.Errorf("input = %+v, want the two pinned items", measured.Task.Items)
	}
}
