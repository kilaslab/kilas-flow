package cli

import (
	"bytes"
	"encoding/json"
	"flag"
	"io"
	"net/http"
	"net/url"
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
// caller named.
func debugVerbs() []Verb {
	return []Verb{
		{
			Path:      "debug eval",
			Operation: "eval-expression",
			Summary:   "evaluate an expression against an execution's stored node outputs (`--execution <id>`, `--node <nodeId>`)",
			Args:      []Arg{arg("expression")},
			Flags:     registerEvalFlags,
			Run:       runDebugEval,
			Human:     humanEvalResult,
		},
	}
}

// evalFlags name the execution the expression is evaluated against, and the
// node whose output narrows it.
type evalFlags struct {
	execution string
	node      string
}

// registerEvalFlags attaches --execution and --node.
func registerEvalFlags(fs *flag.FlagSet) any {
	flags := &evalFlags{}
	fs.StringVar(&flags.execution, "execution", "",
		"execution whose stored node outputs the expression is evaluated against; find one with kilasflow exec list")
	fs.StringVar(&flags.node, "node", "", "node whose output the expression is evaluated against; omit for the run's own context")

	return flags
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

	body := struct {
		Expression string `json:"expression"`
		NodeID     string `json:"nodeId,omitempty"`
	}{Expression: expression, NodeID: strings.TrimSpace(flags.node)}

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
