package jsrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Run executes one node's code over its input items, in this process: it is
// Prepare followed by Execute.
func (runner *Runner) Run(ctx context.Context, task Task) (Result, error) {
	job, host, err := runner.Prepare(task)
	if err != nil {
		return Result{}, err
	}
	return runner.Execute(ctx, job, host)
}

// Prepare turns a task into a job, which holds only data and so can cross
// into a worker process, and the host that answers what the job's code asks
// of the server while it runs. The input is encoded here, once, and checked
// against its cap before any VM exists. Prepare compiles and runs nothing.
func (runner *Runner) Prepare(task Task) (Job, Host, error) {
	limits := runner.limits.tighten(task.Limits)
	input, err := encodeItems(task.Items)
	if err != nil {
		return Job{}, Host{}, err
	}
	state, err := json.Marshal(task.Roots.snapshot(nil))
	if err != nil {
		return Job{}, Host{}, fmt.Errorf("the node's context cannot be handed to code: %w", err)
	}
	if size := int64(len(input) + len(state)); size > limits.MaxInputBytes {
		return Job{}, Host{}, named(ErrInputLimit, fmt.Sprintf(
			"the node's input is %s as JSON, more than the %s code may be given", byteSize(size), byteSize(limits.MaxInputBytes)))
	}
	job := Job{
		Source: task.Source, Mode: task.Mode.orDefault(), Limits: limits, Input: input, Roots: task.Roots,
		Origins: make([]*workflow.PairedItem, len(task.Items)), Files: map[string]workflow.BinaryRef{},
	}
	for index, item := range task.Items {
		job.Origins[index] = item.Paired
		for _, ref := range item.Binary {
			job.Files[ref.ID] = ref
		}
	}
	return job, hostFor(task.Roots), nil
}

// Execute runs a prepared job on a fresh VM.
//
// The work falls in three parts, and only the middle one is charged to the
// time limit:
//
//  1. Setup: the body is compiled (or found in the cache), a fresh VM is
//     created, the libraries the body uses are loaded, the input is parsed
//     inside it and the roots are installed.
//  2. The user's code: the call, every promise job it waits on, and turning
//     its result into JSON.
//  3. Decoding that JSON into items, in Go.
//
// The result carries what the code printed even when the run fails, because
// that is usually how its author finds out why.
func (runner *Runner) Execute(ctx context.Context, job Job, answers Host) (result Result, err error) {
	if err := ctx.Err(); err != nil {
		return Result{}, err
	}
	mode := job.Mode.orDefault()
	limits := runner.limits.tighten(job.Limits)
	ready, err := sharedPrograms.prepare(job.Source, mode)
	if err != nil {
		return Result{}, err
	}
	state, err := json.Marshal(job.Roots.snapshot(librariesFor(ready.analysis, runner.forcedLibrary)))
	if err != nil {
		return Result{}, fmt.Errorf("the node's context cannot be handed to code: %w", err)
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
	printed := &console{limit: limits.MaxConsoleBytes}
	defer func() { result.Console, result.ConsoleTruncated = printed.lines, printed.truncated }()

	// Setup. Nothing here is charged, and nothing here runs user code. The
	// roots, the modules and the libraries the body uses are installed in one
	// step, after the built-ins are bounded, so they use the bounded ones too.
	function, err := v.body(ready)
	if err != nil {
		return Result{}, err
	}
	parsed, err := v.parse(job.Input)
	if err != nil {
		return Result{}, err
	}
	if err := v.install(string(state), parsed, mode, host{node: answers.Node, pair: answers.Pair, console: printed.write}); err != nil {
		return Result{}, err
	}
	clock := newClock(limits.Timeout, func() { v.interrupt(timedOut(limits.Timeout)) })
	call := invocation{
		v: v, clock: clock, function: function, mode: mode, count: len(job.Origins),
		limits: limits, decode: &decoder{origins: job.Origins, files: job.Files},
	}

	if mode == ModeAllItems {
		items, _, err := call.run(ctx, 0, limits.MaxOutputBytes)
		return Result{Items: items, UserTime: clock.spent()}, err
	}
	var out []workflow.Item
	remaining := limits.MaxOutputBytes
	for index := range job.Origins {
		if index > 0 && runner.betweenItems != nil {
			runner.betweenItems()
		}
		items, size, err := call.run(ctx, index, remaining)
		if err != nil {
			return Result{Items: out, UserTime: clock.spent()}, forItem(err, index)
		}
		out = append(out, items...)
		remaining -= size
	}
	return Result{Items: out, UserTime: clock.spent()}, nil
}

// hostFor answers what the code asks of the server, from the roots.
func hostFor(roots Roots) Host {
	return Host{
		Node: func(name string) (string, bool) {
			if roots.Node == nil {
				return "", false
			}
			view, ok := roots.Node(name)
			if !ok {
				return "", false
			}
			if view.Items == nil {
				view.Items = []map[string]any{}
			}
			encoded, err := json.Marshal(view)
			if err != nil {
				return "", false
			}
			return string(encoded), true
		},
		Pair: func(name string, index int) (int, string) {
			if roots.Pair == nil {
				return -1, "item lineage is not available here"
			}
			return roots.Pair(name, index)
		},
	}
}

// invocation is one call of the user's code, with the clock running around
// exactly the part that runs it.
type invocation struct {
	v        *vm
	clock    *clock
	function value
	mode     Mode
	count    int
	limits   Limits
	decode   *decoder
}

// run calls the code for one item (or all of them), and reports the result's
// size as JSON, which is what the output limit measures.
func (call invocation) run(ctx context.Context, index int, maxOutput int64) ([]workflow.Item, int64, error) {
	call.clock.start()
	text, err := call.v.invoke(ctx, call.function, call.mode, index, call.count, maxOutput)
	exhausted := call.clock.stop()
	if err == nil && exhausted {
		err = timedOut(call.limits.Timeout)
	}
	if err != nil {
		return nil, 0, err
	}
	size := int64(len(text))
	if size > maxOutput {
		return nil, 0, named(ErrOutputLimit, fmt.Sprintf("code produced more output than the %s limit allows", byteSize(call.limits.MaxOutputBytes)))
	}
	items, err := call.decode.decode(text, call.mode == ModeEachItem)
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
