package ai

import (
	"context"
	"sync"
)

// A tool that calls a model of its own — the Vector Store Tool answers from
// retrieved passages with one — spends tokens the agent's loop never sees in
// a ModelResponse. RecordToolUsage hands them to the run that invoked the
// tool, so an agent's usage is what the run actually cost rather than what
// its own turns did.

type toolUsageKey struct{}

// toolUsageTally is what the tool calls of one invocation spent. Locked
// because nothing stops a tool from calling models concurrently.
type toolUsageTally struct {
	mu    sync.Mutex
	usage Usage
}

// withToolUsage is the context one tool call runs under, and the tally its
// RecordToolUsage calls add to.
func withToolUsage(ctx context.Context) (context.Context, *toolUsageTally) {
	tally := &toolUsageTally{}
	return context.WithValue(ctx, toolUsageKey{}, tally), tally
}

func (tally *toolUsageTally) total() Usage {
	tally.mu.Lock()
	defer tally.mu.Unlock()
	return tally.usage
}

// RecordToolUsage adds the usage of a model call made inside a tool to the
// agent run that invoked the tool. Called anywhere else it does nothing.
func RecordToolUsage(ctx context.Context, usage Usage) {
	tally, ok := ctx.Value(toolUsageKey{}).(*toolUsageTally)
	if !ok {
		return
	}
	tally.mu.Lock()
	defer tally.mu.Unlock()
	tally.usage.Add(usage)
}
