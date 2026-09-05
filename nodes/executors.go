package nodes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/conditions"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/expression"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// RegisterExecutors installs native implementations for the core definitions.
//
// The HTTP policy and the database guard are both deployment decisions: a
// hosted install must refuse private targets, while a self-hosted one may
// legitimately call services on its own network, and the guard carries the
// install's own database paths so a SQLite credential can never open them.
func RegisterExecutors(registry *engine.Registry, httpPolicy safehttp.Policy, databaseGuard sqlnode.Guard, agentRuntime ai.AgentRuntime, agentMemory ai.Memory, codeCompiler runcode.Compiler, options ...ExecutorOption) error {
	settings := executorSettings{databaseCeiling: sqlnode.DefaultCeiling()}
	for _, option := range options {
		option(&settings)
	}
	for id, executor := range map[string]engine.Executor{
		"core.manual":                    engine.ExecutorFunc(executeManual),
		"core.set":                       engine.ExecutorFunc(executeSet),
		"core.if":                        engine.ExecutorFunc(executeIF),
		"core.merge":                     engine.ExecutorFunc(executeMerge),
		HTTPExecutorID:                   NewHTTPExecutor(httpPolicy),
		WebhookExecutorID:                engine.ExecutorFunc(executeWebhook),
		ScheduleExecutorID:               engine.ExecutorFunc(executeSchedule),
		RespondExecutorID:                engine.ExecutorFunc(executeRespond),
		PostgresExecutorID:               NewDatabaseExecutor(sqlnode.DriverPostgres, "postgres", databaseGuard, settings.databaseCeiling),
		MySQLExecutorID:                  NewDatabaseExecutor(sqlnode.DriverMySQL, "mysql", databaseGuard, settings.databaseCeiling),
		SQLiteExecutorID:                 NewDatabaseExecutor(sqlnode.DriverSQLite, "sqlite", databaseGuard, settings.databaseCeiling),
		ChatModelExecutorID:              engine.ExecutorFunc(executeChatModel),
		MemoryExecutorID:                 engine.ExecutorFunc(executeMemory),
		HTTPToolExecutorID:               engine.ExecutorFunc(executeHTTPTool),
		AgentExecutorID:                  NewAgentExecutor(agentRuntime, httpPolicy, agentMemory),
		CodeExecutorID:                   NewCodeExecutor(codeCompiler, runcode.NewMemoryCache(), runcode.DefaultLimits()),
		LoopExecutorID:                   engine.ExecutorFunc(executeLoop),
		StickyNoteExecutorID:             engine.ExecutorFunc(executeStickyNote),
		TelegramTriggerExecutorID:        NewTelegramTriggerExecutor(NewTelegramFileClient(httpPolicy)),
		SwitchExecutorID:                 engine.ExecutorFunc(executeSwitch),
		FilterExecutorID:                 engine.ExecutorFunc(executeFilter),
		LimitExecutorID:                  engine.ExecutorFunc(executeLimit),
		NoOpExecutorID:                   engine.ExecutorFunc(executeNoOp),
		AggregateExecutorID:              engine.ExecutorFunc(executeAggregate),
		SplitOutExecutorID:               engine.ExecutorFunc(executeSplitOut),
		SortExecutorID:                   engine.ExecutorFunc(executeSort),
		SummarizeExecutorID:              engine.ExecutorFunc(executeSummarize),
		RemoveDuplicatesExecutorID:       engine.ExecutorFunc(executeRemoveDuplicates),
		DateTimeExecutorID:               engine.ExecutorFunc(executeDateTime),
		WaitExecutorID:                   engine.ExecutorFunc(executeWait),
		ExecuteWorkflowExecutorID:        engine.ExecutorFunc(executeExecuteWorkflow),
		ExecuteWorkflowTriggerExecutorID: engine.ExecutorFunc(executeExecuteWorkflowTrigger),
		UnsupportedExecutorID:            engine.ExecutorFunc(executeUnsupported),
	} {
		if err := registry.Register(id, executor); err != nil {
			return err
		}
	}
	return nil
}

// ExecutorOption carries a deployment decision only some executors need.
//
// Variadic rather than another positional parameter: the signature already
// carries six, every caller in the repository and every host embedding this
// package would have to be edited to pass a value most of them do not care
// about, and the next such decision would repeat the argument.
type ExecutorOption func(*executorSettings)

type executorSettings struct {
	databaseCeiling sqlnode.Ceiling
}

// WithDatabaseCeiling bounds what a workflow document may ask a database node
// for. It sits beside the guard: both are things the deployment decides and a
// document cannot override.
func WithDatabaseCeiling(ceiling sqlnode.Ceiling) ExecutorOption {
	return func(settings *executorSettings) {
		settings.databaseCeiling = ceiling
	}
}

func executeManual(ctx context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	return workflow.NodeOutput{{request.Input}}, nil
}

// executeSet builds each output item from the incoming one.
//
// Parameters are resolved *per item*, not once for the node. That is the whole
// difference between a Set node and a constant: an assignment reading
// `$json.name` has to see the item it is being written onto, and resolving once
// outside the loop writes the first item's value onto all of them.
func executeSet(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := validateSetConfiguration(workflow.Node{Parameters: node.Parameters}); err != nil {
		return nil, fmt.Errorf("Set %w", err)
	}

	items := make([]workflow.Item, 0, len(input["main"]))
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(node.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		built, err := setItem(node, resolved, item)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		// Duplicating multiplies everything downstream, which is why it is off
		// by default and named on the parameter.
		copies := 1
		if resolved["duplicateItem"] == true {
			if count, ok := numericParameter(resolved, "duplicateCount"); ok && count > 1 {
				copies = int(count)
			}
		}
		for copy := 0; copy < copies; copy++ {
			items = append(items, cloneItem(built))
		}
	}
	return workflow.NodeOutput{items}, nil
}

// setItem builds one output item.
func setItem(node workflow.IRNode, resolved map[string]any, item workflow.Item) (workflow.Item, error) {
	options, _ := resolved["options"].(map[string]any)
	dotted := true
	if declared, present := options["dotNotation"].(bool); present {
		dotted = declared
	}

	built := workflow.Item{JSON: map[string]any{}, Paired: item.Paired}
	if textOf(resolved["mode"]) == "raw" {
		// Raw mode replaces the item outright: the JSON *is* the output, and
		// the include choice is about the fields it did not mention.
		body, err := rawSetBody(resolved["jsonOutput"])
		if err != nil {
			return workflow.Item{}, err
		}
		for key, value := range includedFields(resolved, item) {
			built.JSON[key] = cloneValue(value)
		}
		for key, value := range body {
			built.JSON[key] = cloneValue(value)
		}
	} else {
		for key, value := range includedFields(resolved, item) {
			built.JSON[key] = cloneValue(value)
		}
		rows, err := readAssignments(resolved["assignments"])
		if err != nil {
			return workflow.Item{}, err
		}
		ignoreErrors, _ := options["ignoreConversionErrors"].(bool)
		// In order, so two rows writing the same field settle the way the user
		// arranged them rather than the way a map iterated.
		for _, row := range rows {
			value, err := row.coerce()
			if err != nil {
				if !ignoreErrors {
					return workflow.Item{}, err
				}
				// Asked to ignore: the value goes through as it arrived, which
				// is the only other honest answer.
				value = row.Value
			}
			writeField(built.JSON, row.Name, cloneValue(value), dotted)
		}
	}

	// Attachments follow the item unless told otherwise. n8n has both an
	// include and a strip flag and strip wins, which is what a user who set
	// both meant.
	includeBinary := true
	if declared, present := options["includeBinary"].(bool); present {
		includeBinary = declared
	}
	if strip, _ := options["stripBinary"].(bool); strip {
		includeBinary = false
	}
	if includeBinary && len(item.Binary) > 0 {
		built.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, reference := range item.Binary {
			built.Binary[key] = reference
		}
	}
	return built, nil
}

// includedFields is the part of the incoming item that survives.
func includedFields(resolved map[string]any, item workflow.Item) map[string]any {
	switch textOf(resolved["include"]) {
	case "none":
		return map[string]any{}
	case "selected":
		wanted := splitFieldList(textOf(resolved["includeFields"]))
		kept := make(map[string]any, len(wanted))
		for _, name := range wanted {
			if value, present := item.JSON[name]; present {
				kept[name] = value
			}
		}
		return kept
	case "except":
		dropped := map[string]bool{}
		for _, name := range splitFieldList(textOf(resolved["excludeFields"])) {
			dropped[name] = true
		}
		kept := make(map[string]any, len(item.JSON))
		for key, value := range item.JSON {
			if !dropped[key] {
				kept[key] = value
			}
		}
		return kept
	default:
		return item.JSON
	}
}

// rawSetBody reads the JSON mode's whole-object body.
func rawSetBody(value any) (map[string]any, error) {
	switch typed := value.(type) {
	case map[string]any:
		return typed, nil
	case string:
		var decoded map[string]any
		if err := json.Unmarshal([]byte(strings.TrimSpace(typed)), &decoded); err != nil {
			return nil, fmt.Errorf("the JSON body is not a valid object: %w", err)
		}
		return decoded, nil
	case nil:
		return nil, fmt.Errorf("JSON mode needs a body")
	default:
		return nil, fmt.Errorf("the JSON body must be an object")
	}
}

// writeField writes one field, nesting on dots when asked to.
//
// n8n's dot notation defaults to on, so an assignment named `user.email` writes
// a nested object. Defaulting it off would make every imported Set with a
// dotted name subtly wrong in a way nobody notices until an HTTP node sends the
// wrong body.
func writeField(target map[string]any, name string, value any, dotted bool) {
	if !dotted || !strings.Contains(name, ".") {
		target[name] = value
		return
	}
	segments := strings.Split(name, ".")
	current := target
	for _, segment := range segments[:len(segments)-1] {
		next, ok := current[segment].(map[string]any)
		if !ok {
			next = map[string]any{}
			current[segment] = next
		}
		current = next
	}
	current[segments[len(segments)-1]] = value
}

// executeIF routes each item by its condition.
//
// The condition's *value* is resolved per item too. A comparison against
// `{{ $json.tier }}` is the ordinary way to compare two fields of one item, and
// against an unresolved marker it compares against an object and takes the same
// branch every time.
func executeIF(ctx context.Context, node workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	// Validated once, before any item, so a malformed condition is one error
	// rather than one per row.
	if _, err := readFilter(node.Parameters["conditions"], workflow.Item{}); err != nil {
		return nil, err
	}
	// IF filters, so an output item's position no longer matches its input's.
	// The runner only infers provenance when the counts match, and here they do
	// not — so each item keeps the origin it arrived with, which is the true
	// answer and is what makes a reach-back from either branch land on the
	// right item.
	trueItems, falseItems := []workflow.Item{}, []workflow.Item{}
	for index, item := range input["main"] {
		resolved, err := expression.Resolve(node.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		filter, err := readFilter(resolved["conditions"], item)
		if err != nil {
			return nil, err
		}
		matched, err := conditions.Evaluate(filter)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", node.Name, err)
		}
		routed := cloneItem(item)
		if routed.Paired == nil {
			routed.Paired = &workflow.PairedItem{SourceNodeID: node.ID, SourcePort: "main", ItemIndex: index}
		}
		if matched {
			trueItems = append(trueItems, routed)
		} else {
			falseItems = append(falseItems, routed)
		}
	}
	return workflow.NodeOutput{trueItems, falseItems}, nil
}

// executeMerge combines the streams it was given.
//
// It deliberately does not resolve expressions: its parameters are a mode, a
// count and a field list, and an expression there would name a mode that
// depends on the data, which is not something this node offers.
func executeMerge(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, _ engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	streams := mergeStreams(ir, input)
	mode := textOf(ir.Parameters["mode"])
	if mode == MergeCombine {
		// n8n's `combine` names a family; `combineBy` says which one.
		mode = textOf(ir.Parameters["combineBy"])
		if mode == "" {
			mode = MergeByFields
		}
	}

	switch mode {
	case "", MergeAppend:
		// Concatenation says nothing about where an item came from, so each
		// side keeps the provenance it arrived with rather than being
		// renumbered — flattening that would make a later lookup confidently
		// wrong instead of honestly unable.
		combined := []workflow.Item{}
		for _, stream := range streams {
			combined = append(combined, stream...)
		}
		return workflow.NodeOutput{combined}, nil

	case MergeChooseBranch:
		branch := 1
		if declared, ok := numericParameter(ir.Parameters, "chooseBranch"); ok {
			branch = int(declared)
		}
		if branch < 1 || branch > len(streams) {
			return nil, fmt.Errorf("node %q: branch %d is outside this node's %d inputs", ir.Name, branch, len(streams))
		}
		return workflow.NodeOutput{streams[branch-1]}, nil

	case MergeByPosition:
		return workflow.NodeOutput{mergeByPosition(streams)}, nil

	case MergeCombineAll:
		return workflow.NodeOutput{mergeCombineAll(streams)}, nil

	case MergeByFields:
		fields := splitFieldList(textOf(ir.Parameters["fieldsToMatch"]))
		if len(fields) == 0 {
			return nil, fmt.Errorf("node %q: combining by fields needs at least one field to match on", ir.Name)
		}
		return workflow.NodeOutput{mergeByFields(streams, fields, textOf(ir.Parameters["joinMode"]))}, nil

	default:
		return nil, fmt.Errorf("node %q: merge mode %q is not supported", ir.Name, mode)
	}
}

// mergeByPosition pairs the nth item of every stream.
//
// The result is as long as the *shortest* stream: pairing position 3 of one
// stream with nothing would produce an item that claims a correspondence
// nobody established.
func mergeByPosition(streams [][]workflow.Item) []workflow.Item {
	shortest := -1
	for _, stream := range streams {
		if shortest < 0 || len(stream) < shortest {
			shortest = len(stream)
		}
	}
	if shortest <= 0 {
		return []workflow.Item{}
	}
	combined := make([]workflow.Item, 0, shortest)
	for index := 0; index < shortest; index++ {
		row := make([]workflow.Item, 0, len(streams))
		for _, stream := range streams {
			row = append(row, stream[index])
		}
		combined = append(combined, mergeItems(row))
	}
	return combined
}

// mergeCombineAll is the cross join: every item of each stream with every item
// of the next.
func mergeCombineAll(streams [][]workflow.Item) []workflow.Item {
	combined := []workflow.Item{}
	for index, stream := range streams {
		if index == 0 {
			combined = append(combined, stream...)
			continue
		}
		crossed := make([]workflow.Item, 0, len(combined)*len(stream))
		for _, left := range combined {
			for _, right := range stream {
				crossed = append(crossed, mergeItems([]workflow.Item{left, right}))
			}
		}
		combined = crossed
	}
	return combined
}

// mergeByFields joins streams on equal values of the named fields.
//
// The join is left to right: input 1 against input 2, then that result against
// input 3. That is what n8n does and it is what makes a three-way join mean
// something rather than depending on which pair happened to be compared first.
func mergeByFields(streams [][]workflow.Item, fields []string, joinMode string) []workflow.Item {
	if len(streams) == 0 {
		return []workflow.Item{}
	}
	combined := streams[0]
	for _, right := range streams[1:] {
		combined = joinOnFields(combined, right, fields, joinMode)
	}
	return combined
}

func joinOnFields(left, right []workflow.Item, fields []string, joinMode string) []workflow.Item {
	index := make(map[string][]workflow.Item, len(right))
	for _, item := range right {
		key, ok := matchKey(item, fields)
		if !ok {
			continue
		}
		index[key] = append(index[key], item)
	}

	combined := []workflow.Item{}
	matchedRight := map[string]bool{}
	for _, item := range left {
		key, ok := matchKey(item, fields)
		partners := index[key]
		if !ok || len(partners) == 0 {
			// keepEverything and enrichInput1 both keep an unmatched left item;
			// keepMatches drops it, which is what a join means.
			if joinMode == "keepEverything" || joinMode == "enrichInput1" {
				combined = append(combined, item)
			}
			continue
		}
		matchedRight[key] = true
		for _, partner := range partners {
			combined = append(combined, mergeItems([]workflow.Item{item, partner}))
		}
	}
	if joinMode == "keepEverything" {
		// Unmatched right-hand items come last, in their own order, so the
		// result is stable rather than depending on map iteration.
		for _, item := range right {
			if key, ok := matchKey(item, fields); !ok || !matchedRight[key] {
				combined = append(combined, item)
			}
		}
	}
	return combined
}

// matchKey renders the values of the join fields. An item missing one of them
// cannot match: a missing field is not a value that happens to be equal to
// another missing field.
func matchKey(item workflow.Item, fields []string) (string, bool) {
	parts := make([]string, 0, len(fields))
	for _, field := range fields {
		value := itemPath(item.JSON, field)
		if value == nil {
			return "", false
		}
		encoded, err := json.Marshal(value)
		if err != nil {
			return "", false
		}
		parts = append(parts, string(encoded))
	}
	return strings.Join(parts, "\x00"), true
}

// mergeItems folds several items into one.
//
// Later fields win, which is what "combine" means everywhere else in this
// product. Binary attachments are carried the same way, so an item that had a
// photo keeps it through a join.
func mergeItems(items []workflow.Item) workflow.Item {
	merged := workflow.Item{JSON: map[string]any{}}
	for _, item := range items {
		for key, value := range item.JSON {
			merged.JSON[key] = cloneValue(value)
		}
		for key, reference := range item.Binary {
			if merged.Binary == nil {
				merged.Binary = map[string]workflow.BinaryRef{}
			}
			merged.Binary[key] = reference
		}
	}
	// The correspondence between a combined item and any one of its sources is
	// genuinely unknown, and saying so is better than pointing at the first.
	return merged
}

func splitFieldList(value string) []string {
	fields := make([]string, 0, 2)
	for _, entry := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			fields = append(fields, trimmed)
		}
	}
	return fields
}

func validateSetConfiguration(node workflow.Node) error {
	switch mode := textOf(node.Parameters["mode"]); mode {
	case "", "manual":
	case "raw":
		// The body may be an expression, which is a marker rather than an
		// object until it is resolved — so only its absence is a save-time
		// error.
		if node.Parameters["jsonOutput"] == nil {
			return fmt.Errorf("JSON mode needs a body")
		}
		return nil
	default:
		return fmt.Errorf("mode %q is not supported", mode)
	}
	switch include := textOf(node.Parameters["include"]); include {
	case "", "all", "none", "selected", "except":
	default:
		return fmt.Errorf("include %q is not supported", include)
	}
	rows, err := readAssignments(node.Parameters["assignments"])
	if err != nil {
		return err
	}
	for _, row := range rows {
		if strings.TrimSpace(row.Name) == "" {
			return fmt.Errorf("assignment keys must not be empty")
		}
		if row.Type != "" && !property.KnownAssignmentType(row.Type) {
			return fmt.Errorf("assignment %q declares type %q, which is not one of %v",
				row.Name, row.Type, property.AssignmentTypes())
		}
	}
	return nil
}

func validateIFConfiguration(node workflow.Node) error {
	_, err := readFilter(node.Parameters["conditions"], workflow.Item{})
	return err
}

func validateMergeConfiguration(node workflow.Node) error {
	return validateMergeMode(node)
}

// cloneItem is the twin of the runner's, and carries provenance for the same
// reason: an executor that maps items one to one loses the correspondence the
// moment it appends to a fresh slice unless the clone brings it along.
func cloneItem(item workflow.Item) workflow.Item {
	cloned := workflow.Item{JSON: cloneMap(item.JSON)}
	if item.Binary != nil {
		cloned.Binary = make(map[string]workflow.BinaryRef, len(item.Binary))
		for key, value := range item.Binary {
			cloned.Binary[key] = value
		}
	}
	if item.Paired != nil {
		paired := *item.Paired
		cloned.Paired = &paired
	}
	return cloned
}

func cloneItems(items []workflow.Item) []workflow.Item {
	cloned := make([]workflow.Item, len(items))
	for index, item := range items {
		cloned[index] = cloneItem(item)
	}
	return cloned
}

func cloneMap(source map[string]any) map[string]any {
	cloned := make(map[string]any, len(source))
	for key, value := range source {
		cloned[key] = cloneValue(value)
	}
	return cloned
}

func cloneValue(value any) any {
	switch typed := value.(type) {
	case map[string]any:
		return cloneMap(typed)
	case []any:
		cloned := make([]any, len(typed))
		for index, item := range typed {
			cloned[index] = cloneValue(item)
		}
		return cloned
	default:
		return value
	}
}
