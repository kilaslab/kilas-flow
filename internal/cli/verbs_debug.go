package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
)

// debugVerbs are the verbs that answer a question about a run without starting
// another one.
//
// `debug eval` is deliberately not a guardrail case. The expression is
// evaluated server-side against an execution the caller can already read: the
// stored node outputs are what `exec get` and the node-run trace return to the
// same caller, the grammar is the one every workflow document is already
// evaluated against, and the environment the evaluator exposes is the
// runtime's allowlist. So it is an ordinary read-shaped verb whose authority is
// the scope the server asks for, and a refusal is the server's to make — a 403
// exits 3 with `scope_denied`, a 404 exits 4, exactly as every other read does.
//
// The execution is named by --execution and not defaulted. The route makes the
// execution the context source, so resolving "the newest execution" silently
// would let a caller evaluate against a run it did not mean — a guess dressed
// as a default. --node is the optional narrowing within the execution the
// caller named: the expression is then evaluated as that node's parameters
// were, against an item it received, and --run and --item pick which.
func debugVerbs() []Verb {
	return []Verb{
		{
			Path:      "debug eval",
			Operation: "eval-expression",
			Summary:   "evaluate an expression against an execution's stored trace, as a node's parameters saw it (`--execution <id>`, `--node <nodeId>`, `--run <n>`, `--item <n>`)",
			Args:      []Arg{arg("expression")},
			Flags:     registerEvalFlags,
			Run:       runDebugEval,
			Human:     humanEvalResult,
		},
	}
}

// evalFlags name the execution the expression is evaluated against, and the
// node, run and item whose parameters it stands in for.
type evalFlags struct {
	execution string
	node      string
	run       position
	item      position
}

// registerEvalFlags attaches --execution, --node, --run and --item.
func registerEvalFlags(fs *flag.FlagSet) any {
	flags := &evalFlags{run: position{name: "run"}, item: position{name: "item"}}
	fs.StringVar(&flags.execution, "execution", "",
		"execution whose stored node outputs the expression is evaluated against; find one with kilasflow exec list")
	fs.StringVar(&flags.node, "node", "",
		"node id whose parameters the expression stands in for: $json is an item the node received; omit for the run's own context")
	fs.Var(&flags.run, "run",
		"which run of --node, from 0 as $runIndex counts them (default: the last run that received items)")
	fs.Var(&flags.item, "item", "which item of that run's input is $json, from 0 as $itemIndex counts them (default 0)")

	return flags
}

// position is a --run or --item flag: a whole number from 0, remembered as
// given or not, because 0 is a run and an item of its own and leaving the flag
// out leaves the server's default in charge.
type position struct {
	name  string
	value *int
}

func (picked *position) String() string {
	if picked == nil || picked.value == nil {
		return ""
	}

	return strconv.Itoa(*picked.value)
}

func (picked *position) Set(raw string) error {
	value, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || value < 0 {
		return fmt.Errorf("--%s takes a whole number from 0, not %q", picked.name, raw)
	}
	picked.value = &value

	return nil
}

// Get makes the flag a flag.Getter holding an int, which is how the MCP
// adapter knows to publish it as an integer rather than a list of strings. A
// flag not given reads as 0 there; the request leaves it out all the same.
func (picked *position) Get() any {
	if picked.value == nil {
		return 0
	}

	return *picked.value
}

// runDebugEval evaluates one expression against one execution's context.
//
// The expression is the positional argument because it is what the caller is
// asking, and it is refused when empty rather than sent: the API would answer
// an empty expression with 422, which the exit contract maps to "fix the
// invocation" anyway.
func runDebugEval(ctx *Context, args []string) error {
	expression, err := requireOneID(args, "expression")
	if err != nil {
		return err
	}

	flags, ok := ctx.VerbFlags.(*evalFlags)
	if !ok {
		return usageError("the debug eval verb was registered without its flags")
	}

	executionID := strings.TrimSpace(flags.execution)
	if executionID == "" {
		return usageError(
			"no execution: pass --execution <id>, or read the id of the run to inspect with `kilasflow exec list`")
	}

	node := strings.TrimSpace(flags.node)
	if node == "" && (flags.run.value != nil || flags.item.value != nil) {
		return usageError("--run and --item pick a run and an item of one node: pass --node <nodeId> as well")
	}

	body := struct {
		Expression string `json:"expression"`
		NodeID     string `json:"nodeId,omitempty"`
		Run        *int   `json:"run,omitempty"`
		Item       *int   `json:"item,omitempty"`
	}{Expression: expression, NodeID: node, Run: flags.run.value, Item: flags.item.value}

	encoded, err := json.Marshal(body)
	if err != nil {
		return usageError("could not render the eval request: %v", err)
	}

	resp, err := ctx.Client.Do(ctx.Ctx, http.MethodPost,
		apiPath("/executions/"+url.PathEscape(executionID)+"/eval"), nil, nil, encoded)
	if err != nil {
		return err
	}

	ctx.Data = jsonOrText(resp.Body)
	// The value is what a caller asked for, so it is what --quiet prints: the
	// envelope around it is for the fields --quiet drops, and the type is one
	// of them.
	ctx.Primary = evalValue(resp.Body)

	return nil
}

// evalValue renders the value an expression evaluated to as compact JSON, and
// reports nothing when the answer carried none.
func evalValue(body []byte) string {
	var answer struct {
		Value json.RawMessage `json:"value"`
	}
	if err := json.Unmarshal(body, &answer); err != nil || len(answer.Value) == 0 {
		return ""
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, answer.Value); err != nil {
		return ""
	}

	return compact.String()
}

// humanEvalResult prints the type and then the value, because the reason to ask
// by hand is to see both: "1" and "\"1\"" are the same question answered two
// ways.
func humanEvalResult(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var answer struct {
		Value json.RawMessage `json:"value"`
		Type  string          `json:"type"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		printJSONValue(w, data)

		return
	}

	var compact bytes.Buffer
	if err := json.Compact(&compact, answer.Value); err != nil {
		compact.Write(answer.Value)
	}

	printKV(w, [][2]string{
		{"type", answer.Type},
		{"value", compact.String()},
	})
}
