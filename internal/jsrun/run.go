package jsrun

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Run executes one node's code over its input items.
//
// The work falls in three parts, and only the middle one is charged to the
// time limit:
//
//  1. Setup: the body is compiled (or found in the cache), the input is
//     encoded and checked against its cap before any VM exists, a fresh VM
//     is created, the libraries the body uses are loaded and the input is
//     parsed inside it.
//  2. The user's code: the call, every promise job it waits on, and turning
//     its result into JSON.
//  3. Decoding that JSON into items, in Go.
func (runner *Runner) Run(ctx context.Context, task Task) (Result, error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	mode := task.Mode.orDefault()
	limits := runner.limits.tighten(task.Limits)
	ready, err := sharedPrograms.prepare(task.Source, mode)
	if err != nil {
		return Result{}, err
	}
	input, err := encodeItems(task.Items)
	if err != nil {
		return Result{}, err
	}
	if int64(len(input)) > limits.MaxInputBytes {
		return Result{}, named(ErrInputLimit, fmt.Sprintf(
			"the node's input is %s as JSON, more than the %s code may be given", byteSize(int64(len(input))), byteSize(limits.MaxInputBytes)))
	}

	if err := runner.acquire(ctx); err != nil {
		return Result{}, err
	}
	defer runner.release()

	v, err := newVM(limits)
	if err != nil {
		return Result{}, err
	}
	defer v.close()
	v.onInterrupt = runner.onInterrupt
	defer heapWatchdog.enter(runner.heapCeiling, v.interrupt)()
	defer context.AfterFunc(ctx, func() { v.interrupt(ctx.Err()) })()
	if runner.testHost != nil {
		runner.testHost(v)
	}

	// Setup. Nothing here is charged, and nothing here runs user code.
	for _, lib := range librariesFor(ready.analysis, runner.forcedLibrary) {
		compiled, err := lib.program()
		if err != nil {
			return Result{}, err
		}
		if err := v.load(compiled); err != nil {
			return Result{}, err
		}
	}
	function, err := v.body(ready)
	if err != nil {
		return Result{}, err
	}
	parsed, err := v.parse(input)
	if err != nil {
		return Result{}, err
	}
	clock := newClock(limits.Timeout, func() { v.interrupt(timedOut(limits.Timeout)) })

	if mode == ModeAllItems {
		items, _, err := runner.charged(ctx, v, clock, function, mode, map[string]any{"items": parsed}, limits.MaxOutputBytes, limits)
		return Result{Items: items, UserTime: clock.spent()}, err
	}

	// The per-item values are read before any user code has run, so no
	// accessor a script defines can run between items, off the clock.
	perItem := make([]value, len(task.Items))
	for index := range perItem {
		perItem[index] = v.element(v.index(parsed, index), "json")
	}
	var out []workflow.Item
	remaining := limits.MaxOutputBytes
	for index := range perItem {
		if index > 0 && runner.betweenItems != nil {
			runner.betweenItems()
		}
		roots := map[string]any{"$json": perItem[index], "$itemIndex": index}
		items, size, err := runner.charged(ctx, v, clock, function, mode, roots, remaining, limits)
		if err != nil {
			return Result{Items: out, UserTime: clock.spent()}, forItem(err, index)
		}
		out = append(out, items...)
		remaining -= size
	}
	return Result{Items: out, UserTime: clock.spent()}, nil
}

// charged is one call of the user's code with the clock running around it.
// It reports the result's size as JSON, which is what the output limit
// measures.
func (runner *Runner) charged(ctx context.Context, v *vm, clock *clock, function value, mode Mode, roots map[string]any, maxOutput int64, limits Limits) ([]workflow.Item, int64, error) {
	clock.start()
	text, err := v.invoke(ctx, function, mode, roots, maxOutput)
	exhausted := clock.stop()
	if err == nil && exhausted {
		err = timedOut(limits.Timeout)
	}
	if err != nil {
		return nil, 0, err
	}
	size := int64(len(text))
	if size > maxOutput {
		return nil, 0, named(ErrOutputLimit, fmt.Sprintf("code produced more output than the %s limit allows", byteSize(limits.MaxOutputBytes)))
	}
	items, err := decodeItems(text)
	return items, size, err
}

// forItem names the item a per-item failure happened on.
func forItem(err error, index int) error {
	var script *ScriptError
	if errors.As(err, &script) {
		script.ItemIndex = index
		return script
	}
	var invalid *LimitError
	if errors.As(err, &invalid) && errors.Is(invalid.Cause, ErrInvalidReturn) {
		invalid.Detail += location(0, index)
	}
	return err
}

func (runner *Runner) acquire(ctx context.Context) error {
	select {
	case runner.slots <- struct{}{}:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (runner *Runner) release() { <-runner.slots }

// wireItem is an item as it crosses into and out of the VM.
type wireItem struct {
	JSON map[string]any `json:"json"`
}

func encodeItems(items []workflow.Item) (string, error) {
	wire := make([]wireItem, len(items))
	for index, item := range items {
		wire[index].JSON = item.JSON
		if wire[index].JSON == nil {
			wire[index].JSON = map[string]any{}
		}
	}
	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	if err := encoder.Encode(wire); err != nil {
		return "", fmt.Errorf("the node's input cannot be handed to code: %w", err)
	}
	return buffer.String(), nil
}

func decodeItems(text string) ([]workflow.Item, error) {
	var wire []wireItem
	if err := json.Unmarshal([]byte(text), &wire); err != nil {
		return nil, fmt.Errorf("jsrun: decoding the code's result: %w", err)
	}
	items := make([]workflow.Item, len(wire))
	for index, item := range wire {
		items[index].JSON = item.JSON
		if items[index].JSON == nil {
			items[index].JSON = map[string]any{}
		}
	}
	return items, nil
}
