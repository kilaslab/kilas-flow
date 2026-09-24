package corpus

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"sort"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// The differential run: every accepted body, on the same input, under the
// embedded runtime and under Node.js, and the two answers diffed. It finds
// where goja and V8 compute different things from the same code, which is the
// failure this runtime refuses to have silently. It needs Node 24, so it is a
// developer's tool: `make js-diff`. It never runs in CI and nothing it prints
// is committed, because what it prints quotes the templates' data.
//
// The KilasFlow-authored control bodies must come out the same; a corpus body
// that differs is reported, and a real difference in the engines is filed as
// a bug with a minimal reproduction.

var jsDiff = flag.Bool("js-diff", false, "run the corpus under Node.js as well and diff the outputs (needs node 24)")

// diffCase is one body as the harness reads it.
type diffCase struct {
	Key       string                      `json:"key"`
	Source    string                      `json:"source"`
	Mode      string                      `json:"mode"`
	Items     []map[string]any            `json:"items"`
	Nodes     map[string][]map[string]any `json:"nodes"`
	Workflow  jsrun.WorkflowInfo          `json:"workflow"`
	Execution jsrun.ExecutionInfo         `json:"execution"`
	// ParseOnly asks Node only whether the body parses: jsrun said it does
	// not, so there is no run of ours to compare a run of theirs with.
	ParseOnly bool `json:"parseOnly,omitempty"`
}

// diffAnswer is what one side produced.
type diffAnswer struct {
	Key           string    `json:"key"`
	Items         any       `json:"items,omitempty"`
	Order         []int     `json:"order,omitempty"`
	Error         *errShape `json:"error,omitempty"`
	Console       []string  `json:"console"`
	Deterministic bool      `json:"deterministic"`
	// Parsed is Node's verdict on a ParseOnly case.
	Parsed bool `json:"parsed,omitempty"`
}

type errShape struct {
	Name    string `json:"name"`
	Message string `json:"message"`
	// words is what the error says, as "Name: message" without the place
	// jsrun adds, for comparing the wording with Node's. It is set on
	// jsrun's side only.
	words string
}

// Verdicts.
const (
	verdictSame             = "same"
	verdictDrift            = "different output"
	verdictNondeterministic = "nondeterministic under Node"
	verdictBothFailed       = "both failed"
	verdictOnlyJSRun        = "failed only under jsrun"
	verdictOnlyNode         = "failed only under Node"
	verdictParsesOnlyInNode = "parses only under Node"
	verdictNeitherParses    = "parses under neither"
)

func TestJSDiff(t *testing.T) {
	if !*jsDiff {
		t.Skip("dev-only; run it with make js-diff (needs node 24)")
	}
	node, err := exec.LookPath("node")
	if err != nil {
		t.Fatal("make js-diff needs node 24 on PATH")
	}
	version, err := exec.Command(node, "--version").Output()
	if err != nil || !strings.HasPrefix(string(version), "v24.") {
		t.Fatalf("make js-diff needs node 24, found %q (%v)", strings.TrimSpace(string(version)), err)
	}

	all := bodies(loadFixtures(t, controlDir), "control/")
	corpusBodies := bodies(loadFixtures(t, fixtureDir()), "")
	if len(corpusBodies) == 0 {
		t.Logf("no code corpus materialised, so only the control bodies are diffed; fetch it with: %s", syncCommand)
	}
	all = append(all, corpusBodies...)

	runner := jsrun.NewRunner(jsrun.Options{Limits: jsrun.DefaultLimits()})
	var cases []diffCase
	ours := map[string]diffAnswer{}
	for _, current := range all {
		if !current.HasSource {
			continue
		}
		_, err := jsrun.Analyze(current.Task.Source, current.Task.Mode)
		var syntax *jsrun.SyntaxError
		if errors.As(err, &syntax) {
			ours[current.Key] = diffAnswer{Key: current.Key, Error: &errShape{Name: "SyntaxError", Message: syntax.Error()}}
			cases = append(cases, diffCase{Key: current.Key, Source: current.Task.Source, Mode: string(current.Task.Mode), ParseOnly: true})
			continue
		}
		if err != nil {
			continue
		}
		ours[current.Key] = runOurs(runner, current)
		cases = append(cases, diffCase{
			Key: current.Key, Source: current.Task.Source, Mode: string(current.Task.Mode),
			Items: jsonOf(current.Task.Items), Nodes: current.Views,
			Workflow: current.Task.Roots.Workflow, Execution: current.Task.Roots.Execution,
		})
	}

	theirs := runNode(t, node, cases)
	counts := map[string]int{}
	consoleDiffers, sameWords := 0, 0
	for _, testCase := range cases {
		verdict, detail := compare(ours[testCase.Key], theirs[testCase.Key])
		if testCase.ParseOnly {
			verdict, detail = verdictNeitherParses, ""
			if theirs[testCase.Key].Parsed {
				verdict, detail = verdictParsesOnlyInNode, ours[testCase.Key].Error.Message
			}
		}
		counts[verdict]++
		if verdict == verdictBothFailed && ours[testCase.Key].Error.words == theirs[testCase.Key].Error.Name+": "+theirs[testCase.Key].Error.Message {
			sameWords++
		}
		if verdict == verdictSame && !reflect.DeepEqual(ours[testCase.Key].Console, theirs[testCase.Key].Console) {
			consoleDiffers++
			t.Logf("%s: the same output, but it printed differently: %s", testCase.Key, firstDifference("console", ours[testCase.Key].Console, theirs[testCase.Key].Console))
		}
		if verdict != verdictSame {
			t.Logf("%s: %s: %s", testCase.Key, verdict, detail)
		}
		if strings.HasPrefix(testCase.Key, "control/") && verdict != verdictSame {
			t.Errorf("control body %s: %s: %s", testCase.Key, verdict, detail)
		}
	}

	verdicts := make([]string, 0, len(counts))
	for verdict := range counts {
		verdicts = append(verdicts, verdict)
	}
	sort.Strings(verdicts)
	var summary strings.Builder
	fmt.Fprintf(&summary, "js-diff: %d bodies run under jsrun and Node %s\n", len(cases), strings.TrimSpace(string(version)))
	for _, verdict := range verdicts {
		fmt.Fprintf(&summary, "  %-30s %d\n", verdict, counts[verdict])
	}
	fmt.Fprintf(&summary, "  %-30s %d (of the same)\n", "console printed differently", consoleDiffers)
	fmt.Fprintf(&summary, "  %-30s %d (of both failed)\n", "worded as Node words it", sameWords)
	t.Log("\n" + summary.String())
}

// runOurs runs a body on the embedded runtime and shapes the answer as the
// harness shapes its own.
func runOurs(runner *jsrun.Runner, current body) diffAnswer {
	ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
	defer cancel()
	result, err := runner.Run(ctx, current.Task)
	answer := diffAnswer{Key: current.Key, Console: []string{}, Deterministic: true}
	for _, line := range result.Console {
		answer.Console = append(answer.Console, line.Level+": "+line.Text)
	}
	if err != nil {
		answer.Error = &errShape{Message: err.Error(), words: err.Error()}
		if script, ok := err.(*jsrun.ScriptError); ok {
			answer.Error.Name = script.Name
			answer.Error.words = script.Name + ": " + script.Message
		}
		return answer
	}
	if current.Task.Mode == jsrun.ModeComparator {
		answer.Order = result.Order
		return answer
	}
	items := make([]any, len(result.Items))
	for index, item := range result.Items {
		items[index] = canonical(item.JSON)
	}
	answer.Items = items
	return answer
}

// runNode hands every case to the harness at once and reads its answers.
func runNode(t *testing.T, node string, cases []diffCase) map[string]diffAnswer {
	t.Helper()
	input := filepath.Join(t.TempDir(), "cases.json")
	encoded, err := json.Marshal(cases)
	if err != nil {
		t.Fatalf("encoding the cases: %v", err)
	}
	if err := os.WriteFile(input, encoded, 0o600); err != nil {
		t.Fatalf("writing the cases: %v", err)
	}
	harness, err := filepath.Abs(filepath.Join("..", "..", "..", "scripts", "js-diff", "harness.mjs"))
	if err != nil {
		t.Fatal(err)
	}
	output := filepath.Join(filepath.Dir(input), "answers.json")
	command := exec.Command(node, harness, input, output)
	var stderr bytes.Buffer
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		t.Fatalf("the Node harness failed: %v\n%s", err, stderr.String())
	}
	raw, err := os.ReadFile(output)
	if err != nil {
		t.Fatalf("reading the Node harness's answers: %v\n%s", err, stderr.String())
	}
	var answers []diffAnswer
	if err := json.Unmarshal(raw, &answers); err != nil {
		t.Fatalf("reading the Node harness's answers: %v\n%s", err, stderr.String())
	}
	byKey := map[string]diffAnswer{}
	for _, answer := range answers {
		if answer.Items != nil {
			answer.Items = canonical(answer.Items)
		}
		byKey[answer.Key] = answer
	}
	return byKey
}

// canonical passes a value through JSON, so both sides are compared as the
// same Go types.
func canonical(value any) any {
	encoded, err := json.Marshal(value)
	if err != nil {
		return fmt.Sprintf("<unencodable: %v>", err)
	}
	var decoded any
	if err := json.Unmarshal(encoded, &decoded); err != nil {
		return fmt.Sprintf("<undecodable: %v>", err)
	}
	return decoded
}

func compare(ours, theirs diffAnswer) (verdict, detail string) {
	switch {
	case !theirs.Deterministic:
		return verdictNondeterministic, ""
	case ours.Error != nil && theirs.Error != nil:
		return verdictBothFailed, fmt.Sprintf("jsrun %q, Node %q", ours.Error.Message, theirs.Error.Name+": "+theirs.Error.Message)
	case ours.Error != nil:
		return verdictOnlyJSRun, ours.Error.Message
	case theirs.Error != nil:
		return verdictOnlyNode, theirs.Error.Name + ": " + theirs.Error.Message
	case !reflect.DeepEqual(ours.Order, theirs.Order):
		return verdictDrift, firstDifference("order", ours.Order, theirs.Order)
	case !reflect.DeepEqual(ours.Items, theirs.Items):
		return verdictDrift, firstDifference("items", ours.Items, theirs.Items)
	}
	return verdictSame, ""
}

// firstDifference names the first place two decoded JSON values differ.
func firstDifference(path string, ours, theirs any) string {
	ours, theirs = canonical(ours), canonical(theirs)
	switch left := ours.(type) {
	case map[string]any:
		right, ok := theirs.(map[string]any)
		if !ok {
			break
		}
		keys := map[string]bool{}
		for key := range left {
			keys[key] = true
		}
		for key := range right {
			keys[key] = true
		}
		names := make([]string, 0, len(keys))
		for key := range keys {
			names = append(names, key)
		}
		sort.Strings(names)
		for _, key := range names {
			if !reflect.DeepEqual(left[key], right[key]) {
				return firstDifference(path+"."+key, left[key], right[key])
			}
		}
	case []any:
		right, ok := theirs.([]any)
		if !ok || len(left) != len(right) {
			break
		}
		for index := range left {
			if !reflect.DeepEqual(left[index], right[index]) {
				return firstDifference(fmt.Sprintf("%s[%d]", path, index), left[index], right[index])
			}
		}
	}
	return fmt.Sprintf("%s: jsrun %s, Node %s", path, clip(ours), clip(theirs))
}

func clip(value any) string {
	encoded, _ := json.Marshal(value)
	if len(encoded) > 160 {
		return string(encoded[:160]) + "…"
	}
	return string(encoded)
}
