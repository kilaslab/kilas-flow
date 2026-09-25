---
id: BUG-fthahg
title: 'Tolerated failures and $(''X'') reads diverge from n8n: one error item per input, error as an object, all output ports joined'
status: testing
priority: medium
created: "2026-09-23T07:47:27Z"
updated: "2026-09-25T09:00:00Z"
---

# Description

Found by the whole-branch review of EPIC-tjnr1z (2026-09-23); visible through
Code-node JavaScript, but engine-wide. n8n's JS task runner
(packages/@n8n/task-runner/src/js-task-runner/js-task-runner.ts) was checked
for the first two:

1. A node-level tolerated failure (toleratedOutput in internal/engine/runner.go)
   emits one error item per input item. n8n's Code node in "Run once for all
   items" mode emits a single `{ json: { error: message } }` under
   continueOnFail, so a downstream node fires once, not once per input item.
2. An error item carries `error` as an object `{ message, node }`
   (errorItem). n8n's Code node writes `error` as the message string, in both
   modes, so `{{ $json.error }}` renders differently and
   `{{ $json.error.message }}` is undefined in n8n.
3. `$('X').all()`, `.first()` and `.last()` read every output port of X joined
   (nodeItemFor flattens them): both IF branches, and error-output items. n8n
   reads the main output's first port, so `$('IF').last()` does not return a
   false-branch item.

The stabilise sprint changes errorItem (it no longer copies Paired) and the
pairing code around nodeItemFor, so settle this after that merges.

# Steps to Reproduce

1. Manual → Code (all items, continueRegularOutput) that throws, fed 3 items →
   Set `{{ $json.error }}`: KilasFlow gives 3 items with an object; n8n gives 1
   item with the message.
2. IF with items on both branches → Code reading `$('IF').all().length`.

# Expected

What n8n does in each case.

# Actual

As described above.

# Acceptance Criteria
- [x] A tolerated node-level failure of a Code node in all-items mode emits the
      one error item n8n does (decide per node type whether this is the
      engine's rule or the Code node's).
- [x] Error items carry `error` in n8n's shape, or the difference is decided
      and documented.
- [x] `$('X')` reads the ports n8n reads, in expressions and in Code alike.

# Notes

## What n8n does (read from its source 2026-09-25, described in our words)

- **Code node, all items, continuing on failure.** The task runner runs the
  body once; if it throws and the node continues on failure, the node's
  answer is a single item whose json is just `error` set to the thrown
  error's message (a string). No input fields, no pairedItem of its own.
- **Code node, each item.** Each item that throws becomes an item whose json
  is just `error: <message string>`, paired with that input item.
- **Error output routing is the engine's, and runs before pairing is
  inferred.** Under continueErrorOutput the engine moves every main-output
  item that is an error item (json holds only error/message/details, or the
  item carries an error object) to the error output. When the item names a
  paired input item, the input item's fields are merged under the error
  item's; when it names none, it moves as it is. Pairing inference runs only
  afterwards, so the all-items Code error item reaches the error output with
  no input fields.
- **Pairing inferred afterwards.** One input item: everything pairs with it.
  Several input items and one output item on the only output: it pairs with
  the first input item (n8n guesses). KilasFlow's own rule says such an
  item's lineage is lost (items-and-lineage: a changed count is not guessed),
  and that rule is kept.
- **A node that throws while continuing on failure** (no handling of its
  own): the engine passes the node's input through and marks the run as
  errored. KilasFlow deliberately emits error items instead (execution-model
  docs); unchanged here.
- **The `error` field's shape is each node's own.** The Code node writes the
  message string; most nodes that handle continueOnFail themselves write the
  message string too; the HTTP Request node writes the error object, so
  `$json.error.message` works there; items carrying an error object have
  their json normalised to `{ error: message }` by the engine.
- **`$('X').all()/.first()/.last()` default port.** When no branch is
  passed, n8n walks upstream from the node being evaluated, breadth first
  over main connections (the node's inputs in input order, then each
  input's connections in order), and takes the output of X on the first
  connection it meets whose source is X. When X is not upstream at all it
  reads output 0. `.all(branch, run)`, `.first(branch, run)` and
  `.last(branch, run)` take the same optional arguments. `$items(name)`
  still defaults to output 0. `.item` follows lineage, so the port rule
  does not touch it.

## Decisions

1. **One error item is the Code node's rule, signalled to the engine.** The
   Code node in all-items mode ran its batch as one call, so it returns its
   failure wrapped in `engine.BatchFailure`; a tolerated BatchFailure becomes
   one error item (not one per input item), on the error output under
   continueErrorOutput, with no input fields, as n8n's. Other nodes keep one
   error item per input item. Each-item mode is unchanged (ItemOutcomes).
   Lineage of the one item follows the runner's existing rule: paired with
   the input when there was one input item, lost when there were several.
2. **`error` is the message string for Code nodes only**, through a new
   definition flag (`ErrorAsMessage`) set on the Code (JavaScript) node and
   the imported Code node. Every other node keeps `{message, node}`: n8n's
   own shape differs per node (HTTP Request writes an object, where
   `$json.error.message` works today), so an engine-wide string would trade
   one divergence for another. Node-specific error items (Postgres, Data
   store) are untouched. The message text is still the run's error text.
3. **`$('X')` reads n8n's default port in expressions and Code alike.** The
   runner works out, per invocation, which output of each earlier node the
   node is connected to (the walk above, over the active graph; nodes off
   the active graph can never lead back to a node that ran) and hands it to
   both readers as `Request.NodeBranches` → `expression.Context.NodeBranches`
   and `jsrun.NodeView.Branch`. The expression debugger (eval.go) works it
   out from the compiled document. A node recorded without output lengths
   (an old checkpoint) is read whole, as before. In Code, `.first()` and
   `.last()` take `(branch, run)` like `.all()`; expressions keep taking no
   arguments (an argument there is still refused loudly, not misread).

## Plan

- RED tests: engine (BatchFailure → one item, both modes; default port for
  all/first/last in expressions incl. through an intermediate node and the
  unconnected fallback); nodes (Code all-items tolerated failure; `error`
  string in both modes; `$('IF').all()` in Code reads the connected branch).
- Implement engine BatchFailure + ErrorAsMessage + NodeBranches; expression
  Context.NodeBranches; jsrun NodeView.Branch + runtime.js; jscode wiring.
- Update e2e js-code spec, docs (code-javascript, expressions,
  expression-grammar, execution-model, items-and-lineage), CHANGELOG.
- `make js-corpus` / baseline if outcomes move.

## Progress 2026-09-25 (implemented, status testing)

- `engine.BatchFailure` (internal/engine/item_outcomes.go): the Code node
  wraps a failure of its all-items run in it; a tolerated one becomes one
  error item without input fields (toleratedOutput), and is not retried, as
  n8n's Code node catches it itself. It is classified before that early
  return, so a tolerated batch timeout still records `node.timeout`.
  Pre-run refusals (empty code, disabled runtime) stay plain errors.
- `ErrorAsMessage` on node.Definition / workflow.NodeDefinition, set on the
  Code (JavaScript) and imported Code nodes: `error` is the message string.
  (Superseded by the review fix below: the text is n8n's `boom [line 1]`,
  not `Error: boom [line 1]`.) Every other node keeps `{message, node}`;
  Postgres and Data store build their own error items and are untouched.
- `connectedOutputs` (internal/engine/node_branches.go) implements the
  upstream walk; the runner caches it per node per run and sets
  `Request.NodeBranches` per invocation; `ExpressionContext` passes it to
  `expression.Context.NodeBranches`; `jsRootsOf` passes it as
  `jsrun.NodeView.Branch`; the expression debugger works it out from the
  compiled revision (eval.go `traceBranches`). Within one input, edges from
  different source nodes are walked in node-ID order, where n8n walks them in
  the order its connections object lists them; this only matters when two
  different paths to X enter the same input.
- Code: `.first(branch, run)` / `.last(branch, run)` added beside `.all()`;
  `$node['X']` keeps pairing over every output's items. Expressions' `.all()`
  / `.first()` / `.last()` still take no arguments (an argument is refused
  loudly by the arity check, never misread) — a possible follow-up.
- JS corpus: rescored, no outcome moved (timing only), baseline left as is.
- Tests: internal/engine batch_failure_test.go, node_branch_test.go,
  eval_test.go (debugger); nodes jscode_run_test.go, jscode_lineage_test.go;
  jsrun roots_test.go updated to the new default; e2e js-code.spec.ts
  updated and run green.

## Review fixes 2026-09-25

- **Error item text is n8n's exactly.** n8n's task runner (secure mode, the
  default) turns what the code threw into an execution error whose message
  is the details from the stack's header row followed by `[line N]`: no error
  type (that goes to a separate description) and, since the runner builds it
  without an item index, no "for item". An empty message reads "Unknown
  error". So `throw new Error('boom')` gives `boom [line 1]`, a TypeError
  `Cannot read properties of undefined (reading 'id') [line 2]`, and a
  JSON.parse SyntaxError `Expected property name or '}' in JSON at position 1
  (line 1 column 2) [line 2]`, in both modes. jsrun's ScriptError and
  SyntaxError now answer `ItemMessage()` with that text, and the runner's
  errorItem prefers it for a message-shaped node. A code-level SyntaxError
  (code that does not parse) gives its message and line; it is refused at
  validate, so it rarely reaches a run. The run's own error text (the node's
  row) is unchanged and still names the type and item.
- **Only the code's own failure is a BatchFailure.** nodes/jscode.go
  `failedInTheCode`: a ScriptError, a SyntaxError or one of the code's limits
  (time, memory, output, host calls, call depth, invalid return, never
  settles, file, response, static data). The engine's fault, a closed pool, a
  worker that crashed or could not start, and input too large are plain
  errors, so retryOnFail retries them and continue-on-fail tolerates them per
  input item, as n8n's engine does for failures outside the runner's try.
- Follow-ups filed: expressions' `.all/.first/.last(branch, run)`, and the
  walk order of edges into one input.

# Related Files

# Attachments
