package expression

import (
	"fmt"
	"sort"
	"strconv"
	"time"
)

// nodeRootPrefix marks a root produced by the `$('Name')` form. It is not
// writable by a user — the parser mints it — so it cannot collide with a real
// root name.
const nodeRootPrefix = "$node\x00"

// fromAIRoot is the marker an AI agent fills in.
const fromAIRoot = "$fromAI"

// nodeItemsKey is where a node's item list lives on the value the grammar sees.
// It is the same word the function is called by, so `$('X').all()` and
// `$('X').all` read the same thing.
const nodeItemsKey = "all"

// NodeItem is one node's output as an expression sees it.
//
// The `json` wrapper is what n8n workflows are written against:
// `$node["X"].json.y` is the form every imported expression uses, and it failed
// on the `.json` step because a node's name mapped straight onto the item's
// fields.
type NodeItem struct {
	// JSON is the item's fields.
	JSON map[string]any
	// Binary names the item's attachments.
	Binary map[string]any
	// Items is every item the node's run produced, backing `.all()`.
	Items []map[string]any
	// ItemOrigins is one canonical OriginKey per entry of Items, so the runtime
	// can pair the current item with this node's items without re-deriving the
	// format.
	ItemOrigins []string
	// NodeID and RunIndex name the run Items came from, and PortOffsets is
	// where each output port's items start in Items. Together they let the
	// runtime read an item whose origin names this node's own item directly,
	// which is the only answer left when this node's own items have no
	// lineage — a Code node that changed the item count. All three are zero
	// in a checkpoint written before they existed, and zero never matches.
	NodeID      string
	RunIndex    int
	PortOffsets map[string]int
	// Paired is the item on this node that the current item descends from,
	// backing `.item` and the `.json` read. Nil when lineage could not be
	// established.
	Paired map[string]any
	// LineageReason explains a nil Paired, so `.item` can fail with something
	// a workflow author can act on.
	LineageReason string
	// Parameters is the node's own resolved configuration, backing
	// `$('X').params`. Nil when the runtime did not supply it.
	Parameters map[string]any
}

// OriginKey is the canonical name of one produced item: which node, which port,
// which run, which position. It is opaque to callers, who only build it on both
// sides and compare.
func OriginKey(nodeID, port string, runIndex, itemIndex int) string {
	return nodeID + "\x00" + port + "\x00" + strconv.Itoa(runIndex) + "\x00" + strconv.Itoa(itemIndex)
}

// WorkflowContext is the `$workflow` root.
type WorkflowContext struct {
	ID     string
	Name   string
	Active bool
	// Timezone is the workflow's settings.timezone. `$now`, `$today` and
	// `DateTime.local()` read the clock in it, which is what n8n does; using
	// UTC made `$today` fall on the wrong calendar day for most of the world.
	Timezone string
}

// FromAIRequest is what `$fromAI` resolves to: a marker that an agent fills
// this parameter, rather than a value read from the item.
type FromAIRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// envSource is the `$env` root.
//
// A distinct type rather than a plain map so that reading a key that is not
// there is a loud error naming the allowlist variable. The whole map is still
// available (`{{ $env }}`, `Object.keys($env)`), and a key that is present
// reads its value.
type envSource map[string]string

// inputSource is the `$input` root.
//
// n8n's `$input` is an object with `.item`, `.first()`, `.last()` and
// `.all()`. KilasFlow's was a bare map of port to items, so `$input.item` was
// silently undefined and `$input.first()` was an error. Both shapes resolve
// here: the ports are still readable by name, and the n8n API works on top.
type inputSource struct {
	ports   map[string][]map[string]any
	current map[string]any
}

// sourceName picks the port the n8n-style accessors read: `main` when it is
// there, otherwise the first port in a stable order.
func (source inputSource) sourceName() string {
	if _, found := source.ports["main"]; found {
		return "main"
	}
	names := make([]string, 0, len(source.ports))
	for name := range source.ports {
		names = append(names, name)
	}
	sort.Strings(names)
	if len(names) == 0 {
		return ""
	}
	return names[0]
}

func (source inputSource) items() []map[string]any {
	return source.ports[source.sourceName()]
}

// all is every item on the source port, wrapped so `.json` and a bare field
// both read.
func (source inputSource) all() []any {
	items := source.items()
	wrapped := make([]any, 0, len(items))
	for _, item := range items {
		wrapped = append(wrapped, itemWrapper(item, nil))
	}
	return wrapped
}

// member resolves `$input.<name>`: the n8n accessors, then a port by name.
func (source inputSource) member(name string) (any, error) {
	switch name {
	case "item":
		return itemWrapper(anyMap(source.current), nil), nil
	case "params":
		return Undefined, nil
	case "isExecuted":
		return true, nil
	}
	if items, found := source.ports[name]; found {
		wrapped := make([]any, 0, len(items))
		for _, item := range items {
			wrapped = append(wrapped, itemWrapper(item, nil))
		}
		return wrapped, nil
	}
	return Undefined, nil
}

// plain is `$input` as a parameter value: port name to the port's items, which
// is the shape the root had before the evaluator gave it n8n's object API, and
// the shape a Set node writes.
func (source inputSource) plain() map[string]any {
	ports := make(map[string]any, len(source.ports))
	for name, items := range source.ports {
		list := make([]any, len(items))
		for index, item := range items {
			list[index] = anyMap(item)
		}
		ports[name] = list
	}
	return ports
}

// fieldsOfEnv is `$env` as a plain object, which is what both a parameter value
// and JSON.stringify need.
func fieldsOfEnv(source envSource) map[string]any {
	fields := make(map[string]any, len(source))
	for key, value := range source {
		fields[key] = value
	}
	return fields
}

// itemWrapper exposes one item the way n8n does: `json` for the payload, and
// the payload's own fields for the bare reads KilasFlow documented.
func itemWrapper(fields map[string]any, extra map[string]any) map[string]any {
	wrapped := make(map[string]any, len(fields)+1)
	for key, value := range fields {
		wrapped[key] = value
	}
	wrapped["json"] = anyMap(fields)
	for key, value := range extra {
		wrapped[key] = value
	}
	return wrapped
}

// lineageError is what `.item` yields when provenance is unknown. Reading it
// fails with the reason rather than producing a plausible wrong item.
type lineageError struct{ reason string }

// Roots is the supported root allowlist, served to the editor so the client
// does not keep a second copy that drifts from this one. Keeping the list here
// is what makes adding a root in Go require no client change.
func Roots() []string {
	return []string{
		"$json", "$input", "$node", "$env", "$execution", "$workflow",
		"$itemIndex", "$runIndex", "$vars", "$now", "$today",
		fromAIRoot, "$items", "$(",
	}
}

// IsCallableRoot reports a root that is a function rather than a value, so a
// caller listing the surface does not have to keep its own copy of that set.
func IsCallableRoot(root string) bool {
	switch root {
	case "$(", fromAIRoot, "$items":
		return true
	default:
		return false
	}
}

func sortedStrings(values []string) []string {
	sort.Strings(values)
	return values
}

// resolveRoot reads a root that holds a value.
func resolveRoot(name string, ctx Context) (any, error) {
	switch name {
	case "$json":
		return anyMap(ctx.JSON), nil
	case "$input":
		return ctx.input(), nil
	case "$node":
		return nodeRootMap(ctx), nil
	case "$env":
		return envSource(ctx.Env), nil
	case "$execution":
		return map[string]any{
			"id":          ctx.Execution.ID,
			"mode":        ctx.Execution.Mode,
			"resumeUrl":   ctx.Execution.ResumeURL,
			"approvalUrl": ctx.Execution.ApprovalURL,
		}, nil
	case "$workflow":
		return map[string]any{
			"id":     ctx.Workflow.ID,
			"name":   ctx.Workflow.Name,
			"active": ctx.Workflow.Active,
		}, nil
	case "$itemIndex":
		return float64(ctx.ItemIndex), nil
	case "$runIndex":
		return float64(ctx.RunIndex), nil
	case "$vars":
		vars := make(map[string]any, len(ctx.Vars))
		for key, value := range ctx.Vars {
			vars[key] = value
		}
		return vars, nil
	case "$now":
		return dateValue{at: ctx.clock()}, nil
	case "$today":
		return dateValue{at: startOfDay(ctx.clock())}, nil
	case "$parameter":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", name)
		}
		return anyMap(ctx.Parameters), nil
	case "$value":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", name)
		}
		return ctx.Value, nil
	case "$credentials":
		if !ctx.AllowRouting {
			return nil, fmt.Errorf("expression root %q is only available in a node pack's routing templates", name)
		}
		// Only what the caller put here, which is only what the credential type
		// declared non-secret.
		fields := make(map[string]any, len(ctx.Credentials))
		for key, value := range ctx.Credentials {
			fields[key] = value
		}
		return fields, nil
	case fromAIRoot, "$items":
		return nil, fmt.Errorf("%s is a function and has to be called: %s(…)", name, name)
	default:
		return nil, fmt.Errorf("expression root %q is not supported", name)
	}
}

// callRoot reads a root that is a function.
func callRoot(name string, ctx Context, args []any) (any, error) {
	switch name {
	case fromAIRoot:
		if !ctx.AllowFromAI {
			return nil, fmt.Errorf("$fromAI is only available in a parameter an AI agent fills; it has no value here")
		}
		if len(args) == 0 {
			return nil, fmt.Errorf("$fromAI needs a parameter name")
		}
		request := FromAIRequest{Name: jsString(args[0])}
		if len(args) > 1 && !isNullish(args[1]) {
			request.Description = jsString(args[1])
		}
		return request, nil
	case "$items":
		if len(args) > 0 && !isNullish(args[0]) {
			return nodeRootValueItems(jsString(args[0]), ctx)
		}
		return ctx.input().all(), nil
	default:
		return nil, fmt.Errorf("expression root %q is not supported", name)
	}
}

// nodeRootValue resolves the `$('Name')` form.
func nodeRootValue(name string, ctx Context) (any, error) {
	item, found := ctx.NodeItems[name]
	if !found {
		// A node that has not run yet is a mistake in the workflow, not an
		// absent field: naming a node that never produced anything cannot be
		// what the author meant, so it fails rather than resolving to nothing.
		return nil, fmt.Errorf("$('%s') names a node that has not produced output in this run", name)
	}
	return nodeWrapper(item, ctx), nil
}

// nodeRootValueItems resolves `$items('Name')`, which is the list rather than
// the single paired item.
func nodeRootValueItems(name string, ctx Context) (any, error) {
	item, found := ctx.NodeItems[name]
	if !found {
		return nil, fmt.Errorf("$items('%s') names a node that has not produced output in this run", name)
	}
	return nodeWrapper(item, ctx)[nodeItemsKey], nil
}

// nodeRootMap is the `$node` root: every completed node by display name.
func nodeRootMap(ctx Context) map[string]any {
	nodes := make(map[string]any, len(ctx.NodeItems)+len(ctx.Nodes))
	for name, item := range ctx.NodeItems {
		nodes[name] = nodeWrapper(item, ctx)
	}
	for name, item := range ctx.Nodes {
		if _, already := nodes[name]; already {
			continue
		}
		nodes[name] = itemWrapper(anyMap(item), nil)
	}
	return nodes
}

// nodeWrapper exposes one node's output to the grammar.
//
// `.json` is the item the current item descends from — the paired item when the
// runtime established lineage, and otherwise the item at the same position,
// which is the same correspondence n8n uses. It used to be the FIRST item for
// every current item, which is silently wrong on any multi-item flow: a Split
// Out followed by `$node["Split Out"].json.v` repeated the first value three
// times instead of returning a, b, c.
//
// `.item` reads the paired-item lineage and refuses to fall back to the first
// item: returning the first item when lineage is unknown is correct only when
// every node processed exactly one item.
func nodeWrapper(item NodeItem, ctx Context) map[string]any {
	current := item.JSON
	switch {
	case item.Paired != nil:
		current = item.Paired
	case ctx.ItemIndex >= 0 && ctx.ItemIndex < len(item.Items):
		current = item.Items[ctx.ItemIndex]
	}
	all := make([]any, 0, len(item.Items))
	for _, entry := range item.Items {
		all = append(all, itemWrapper(entry, nil))
	}
	value := itemWrapper(anyMap(current), nil)
	value["binary"] = item.Binary
	value[nodeItemsKey] = all
	if item.Paired != nil {
		value["item"] = itemWrapper(item.Paired, nil)
	} else {
		reason := item.LineageReason
		if reason == "" {
			reason = "lineage is not available for this node"
		}
		value["item"] = lineageError{reason: reason}
	}
	if item.Parameters != nil {
		value["params"] = item.Parameters
	}
	value["isExecuted"] = true
	return value
}

// input is the `$input` root for this context.
func (ctx Context) input() inputSource {
	return inputSource{ports: ctx.Input, current: ctx.JSON}
}

// location is the zone a workflow's clock reads: its own settings.timezone,
// then UTC. An unparseable zone is UTC rather than an error, because the
// compiler refuses unknown zones at save time and an execution that is already
// running should not fail on the clock.
func (ctx Context) location() *time.Location {
	if ctx.Timezone == "" {
		return time.UTC
	}
	location, err := time.LoadLocation(ctx.Timezone)
	if err != nil {
		return time.UTC
	}
	return location
}

// clock is the instant `$now` and `$today` read. One instant per evaluation, so
// two expressions in the same parameter tree cannot disagree about the time.
func (ctx Context) clock() time.Time {
	if ctx.Now.IsZero() {
		return time.Now().In(ctx.location())
	}
	return ctx.Now.In(ctx.location())
}
