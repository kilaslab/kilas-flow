package expression

import (
	"fmt"
	"sort"
	"strings"
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
	// Paired is the item on this node that the current item descends from,
	// backing `.item`. Nil when lineage could not be established.
	Paired map[string]any
	// LineageReason explains a nil Paired, so `.item` can fail with something
	// a workflow author can act on.
	LineageReason string
}

// Roots is the supported root allowlist, served to the editor so the client
// does not keep a second copy that drifts from this one. Keeping the list here
// is what makes adding a root in Go require no client change.
func Roots() []string {
	return []string{
		"$json", "$input", "$node", "$env", "$execution", "$workflow",
		"$itemIndex", "$now", "$today", "$fromAI", "$(",
	}
}

func sortedStrings(values []string) []string {
	sort.Strings(values)
	return values
}

// WorkflowContext is the `$workflow` root.
type WorkflowContext struct {
	ID     string
	Name   string
	Active bool
}

// FromAIRequest is what `$fromAI` resolves to: a marker that an agent fills
// this parameter, rather than a value read from the item.
type FromAIRequest struct {
	Name        string `json:"name"`
	Description string `json:"description,omitempty"`
}

// nodeRootValue resolves the `$('Name')` form.
func nodeRootValue(root string, ctx Context) (any, error) {
	name := strings.TrimPrefix(root, nodeRootPrefix)
	item, found := ctx.NodeItems[name]
	if !found {
		// A node that has not run yet is a mistake in the workflow, not an
		// absent field: naming a node that never produced anything cannot be
		// what the author meant, so it fails rather than resolving to nothing.
		return nil, fmt.Errorf("$('%s') names a node that has not produced output in this run", name)
	}
	return nodeItemValue(item), nil
}

// nodeItemValue exposes one node's output to the grammar.
//
// `.item`, `.first()`, `.last()` and `.all()` are the four ways n8n workflows
// read another node. `.item` reads the paired-item lineage and refuses to fall
// back to the first item: returning the first item when lineage is unknown is
// correct only when every node processed exactly one item, and silently wrong
// otherwise.
func nodeItemValue(item NodeItem) map[string]any {
	all := make([]any, 0, len(item.Items))
	for _, entry := range item.Items {
		all = append(all, wrapNodeItem(entry))
	}
	value := map[string]any{
		"json":       anyMap(item.JSON),
		"binary":     item.Binary,
		nodeItemsKey: all,
	}
	if item.Paired != nil {
		value["item"] = wrapNodeItem(item.Paired)
	} else {
		reason := item.LineageReason
		if reason == "" {
			reason = "lineage is not available for this node"
		}
		value["item"] = lineageError{reason: reason}
	}
	return value
}

func wrapNodeItem(fields map[string]any) map[string]any {
	return map[string]any{"json": anyMap(fields)}
}

// lineageError is what `.item` yields when provenance is unknown. Reading it
// fails with the reason rather than producing a plausible wrong item.
type lineageError struct{ reason string }

func startOfDay(now time.Time) time.Time {
	return time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, now.Location())
}
