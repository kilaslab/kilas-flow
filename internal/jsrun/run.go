package jsrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Run executes one node's code over its input items, in this process: it is
// Prepare, Execute and Finish in turn.
func (runner *Runner) Run(ctx context.Context, task Task) (Result, error) {
	job, host, err := runner.Prepare(task)
	if err != nil {
		return Result{}, err
	}
	executed, err := runner.Execute(ctx, job, host)
	return job.Finish(executed, err)
}

// Prepare turns a task into a job, which holds only data and so can cross
// into a worker process, and the host that answers what the job's code asks
// of the server while it runs. The input is encoded here, once, and the job,
// everything that will cross, is checked against the input cap before any VM
// exists. Prepare compiles and runs nothing.
func (runner *Runner) Prepare(task Task) (Job, Host, error) {
	limits := runner.limits.tighten(task.Limits)
	input, err := encodeItems(task.Items)
	if err != nil {
		return Job{}, Host{}, err
	}
	job := Job{
		Source: task.Source, Mode: task.Mode.orDefault(), Limits: limits, Input: input, Roots: task.Roots,
		ContinueOnItemError: task.ContinueOnItemError, Count: len(task.Items),
		Origins: make([]*workflow.PairedItem, len(task.Items)), Files: map[string]workflow.BinaryRef{},
	}
	for index, item := range task.Items {
		job.Origins[index] = item.Paired
		for _, ref := range item.Binary {
			if _, seen := job.Files[ref.ID]; !seen {
				job.Files[ref.ID] = ref
				job.FileIDs = append(job.FileIDs, ref.ID)
			}
		}
	}
	sort.Strings(job.FileIDs)
	header, err := json.Marshal(job)
	if err != nil {
		return Job{}, Host{}, fmt.Errorf("the node's context cannot be handed to code: %w", err)
	}
	if size := int64(len(input) + len(header)); size > limits.MaxInputBytes {
		return Job{}, Host{}, named(ErrInputLimit, fmt.Sprintf(
			"the node's input is %s as JSON, more than the %s code may be given", byteSize(size), byteSize(limits.MaxInputBytes)))
	}
	return job, hostFor(task.Roots), nil
}

// Execute runs a prepared job on a fresh VM, where the code is to run: in
// this process, or in a worker. It returns what the code produced still as
// the JSON it returned, which Finish decodes against the job's input.
//
// The work falls in two parts, and only the second is charged to the time
// limit:
//
//  1. Setup: the body is compiled (or found in the cache), a fresh VM is
//     created, the libraries the body uses are loaded, the input is parsed
//     inside it and the roots are installed.
//  2. The user's code: the call, every promise job it waits on, and turning
//     its result into JSON.
//
// What the code printed is returned even when the run fails, because that is
// usually how its author finds out why.
func (runner *Runner) Execute(ctx context.Context, job Job, answers Host) (executed Executed, err error) {
	if err := ctx.Err(); err != nil {
		return Executed{}, err
	}
	defer func() { err = bounded(err) }()
	mode := job.Mode.orDefault()
	limits := runner.limits.tighten(job.Limits)
	ready, err := sharedPrograms.prepare(job.Source, mode)
	if err != nil {
		return Executed{}, err
	}
	state, err := json.Marshal(job.Roots.snapshot(librariesFor(ready.analysis, runner.forcedLibrary)))
	if err != nil {
		return Executed{}, fmt.Errorf("the node's context cannot be handed to code: %w", err)
	}

	if err := runner.acquire(ctx); err != nil {
		return Executed{}, err
	}
	defer runner.release()

	v, err := newVM(limits)
	if err != nil {
		return Executed{}, err
	}
	defer v.close()
	v.onInterrupt = runner.onInterrupt
	defer heapWatchdog.enter(runner.heapCeiling, v.interrupt)()
	defer context.AfterFunc(ctx, func() { v.interrupt(ctx.Err()) })()
	if runner.testHost != nil {
		runner.testHost(v)
	}
	printed := &console{limit: limits.MaxConsoleBytes}
	defer func() { executed.Console, executed.ConsoleTruncated = printed.lines, printed.truncated }()

	// Setup. Nothing here is charged, and nothing here runs user code. The
	// roots, the modules and the libraries the body uses are installed in one
	// step, after the built-ins are bounded, so they use the bounded ones too.
	function, err := v.body(ready)
	if err != nil {
		return Executed{}, err
	}
	parsed, err := v.parse(job.Input)
	if err != nil {
		return Executed{}, err
	}
	if err := v.install(string(state), parsed, mode, host{node: answers.Node, pair: answers.Pair, console: printed.write}); err != nil {
		return Executed{}, err
	}
	clock := newClock(limits.Timeout, func() { v.interrupt(timedOut(limits.Timeout)) })
	known := make(map[string]bool, len(job.FileIDs))
	for _, id := range job.FileIDs {
		known[id] = true
	}
	call := invocation{v: v, clock: clock, function: function, mode: mode, count: job.count(), limits: limits, files: known}

	if mode == ModeComparator {
		text, err := call.order(ready.analysis.Returns)
		return Executed{Outputs: []string{text}, UserTime: clock.spent()}, err
	}
	if mode == ModeAllItems {
		text, _, err := call.run(ctx, 0, limits.MaxOutputBytes)
		return Executed{Outputs: []string{text}, UserTime: clock.spent()}, err
	}
	outputs := make([]string, 0, job.count())
	var failures []ItemFailure
	remaining := limits.MaxOutputBytes
	for index := range job.count() {
		if index > 0 && runner.betweenItems != nil {
			runner.betweenItems()
		}
		text, size, err := call.run(ctx, index, remaining)
		if err != nil {
			err = bounded(forItem(err, index))
			if !job.ContinueOnItemError || !itemScoped(err) {
				return Executed{Outputs: outputs, Failures: failures, UserTime: clock.spent()}, err
			}
			// A failed item becomes an error item downstream, so what it
			// says counts against the output like what it would have
			// returned.
			if remaining -= int64(len(err.Error())); remaining < 0 {
				return Executed{Outputs: outputs, Failures: failures, UserTime: clock.spent()}, outputTooLarge(limits)
			}
			failures = append(failures, ItemFailure{Index: index, Error: EncodeError(err)})
			outputs = append(outputs, "")
			continue
		}
		outputs = append(outputs, text)
		remaining -= size
	}
	return Executed{Outputs: outputs, Failures: failures, UserTime: clock.spent()}, nil
}

// Finish turns what Execute produced into the task's result, in the process
// that prepared the job: the returned items are decoded against the input's
// lineage and files, which never leave it. What came back is checked against
// the job's own limits first, so a worker cannot hand back more than its code
// could have, nor name a file or an item its input did not have.
func (job Job) Finish(executed Executed, runErr error) (Result, error) {
	result := Result{UserTime: executed.UserTime}
	result.Console, result.ConsoleTruncated = job.console(executed)
	if job.Mode.orDefault() == ModeComparator {
		return job.finishOrder(result, executed, runErr)
	}
	eachItem := job.Mode.orDefault() == ModeEachItem
	if runErr != nil {
		// A per-item run that stopped keeps what the items before the failure
		// returned, which says how far it got.
		if eachItem && len(executed.Outputs) <= job.count() {
			decode := decoder{origins: job.Origins, files: job.Files}
			for _, text := range executed.Outputs {
				if items, err := decode.decode(text, true); err == nil {
					result.Items = append(result.Items, items...)
				}
			}
		}
		return result, runErr
	}
	want := 1
	if eachItem {
		want = job.count()
	}
	if len(executed.Outputs) != want {
		return result, EngineFaultError(fmt.Sprintf("it returned %d results for %d calls", len(executed.Outputs), want))
	}
	var size int64
	for _, text := range executed.Outputs {
		size += int64(len(text))
	}
	if size > job.Limits.MaxOutputBytes {
		return result, EngineFaultError(fmt.Sprintf("it returned %s of output, past the %s limit", byteSize(size), byteSize(job.Limits.MaxOutputBytes)))
	}
	failed := make(map[int]*WireError, len(executed.Failures))
	for _, failure := range executed.Failures {
		if !eachItem || !job.ContinueOnItemError || failure.Index < 0 || failure.Index >= want || failure.Error == nil || executed.Outputs[failure.Index] != "" {
			return result, EngineFaultError("it reported a failed item that cannot have failed")
		}
		failed[failure.Index] = failure.Error
	}
	decode := decoder{origins: job.Origins, files: job.Files}
	for index, text := range executed.Outputs {
		if failure, ok := failed[index]; ok {
			result.Outcomes = append(result.Outcomes, ItemOutcome{Error: failure})
			continue
		}
		items, err := decode.decode(text, eachItem)
		if err != nil {
			// The code's own results were checked where it ran; one that fails
			// here was not the code's.
			return result, EngineFaultError("its result did not match its input: " + err.Error())
		}
		result.Items = append(result.Items, items...)
		if eachItem && job.ContinueOnItemError {
			result.Outcomes = append(result.Outcomes, ItemOutcome{Items: items})
		}
	}
	return result, nil
}

// finishOrder takes a comparator's order, once it is known to be one: every
// input item exactly once, whatever a worker sent back.
func (job Job) finishOrder(result Result, executed Executed, runErr error) (Result, error) {
	if runErr != nil {
		return result, runErr
	}
	if len(executed.Outputs) != 1 {
		return result, EngineFaultError(fmt.Sprintf("it returned %d results for 1 call", len(executed.Outputs)))
	}
	order := []int{}
	if err := json.Unmarshal([]byte(executed.Outputs[0]), &order); err != nil || len(order) != job.count() {
		return result, EngineFaultError("its sort order does not name every item once")
	}
	placed := make([]bool, len(order))
	for _, index := range order {
		if index < 0 || index >= len(order) || placed[index] {
			return result, EngineFaultError("its sort order does not name every item once")
		}
		placed[index] = true
	}
	result.Order = order
	return result, nil
}

// console is what the code printed, held to the job's console limit again.
func (job Job) console(executed Executed) ([]ConsoleLine, bool) {
	kept := &console{limit: job.Limits.MaxConsoleBytes}
	for _, line := range executed.Console {
		if !kept.add(line.Level, line.Text, line.At) {
			break
		}
	}
	return kept.lines, executed.ConsoleTruncated || kept.truncated
}

// count is how many input items the job has.
func (job Job) count() int { return job.Count }

// itemScoped reports a failure that belongs to one item: what its code threw,
// or a value that is not an item. The VM is left as a successful item leaves
// it, so the next item can run. Everything else ends the run, an uncaught
// error included.
func itemScoped(err error) bool {
	var script *ScriptError
	if errors.As(err, &script) {
		return !script.Uncaught
	}
	return errors.Is(err, ErrInvalidReturn)
}

// The most of a thrown error a failure carries. Code can throw a message of
// any size, and a failure is stored with the node's run and shown in the
// editor, where the start of a message is what helps.
const (
	maxErrorText   = 4 << 10
	maxStackFrames = 32
)

// bounded cuts a thrown error down to what a failure carries.
func bounded(err error) error {
	var script *ScriptError
	if !errors.As(err, &script) {
		return err
	}
	if len(script.Message) > maxErrorText {
		script.Message = truncateText(script.Message, maxErrorText) + "…"
	}
	if len(script.Stack) > maxStackFrames {
		script.Stack = script.Stack[:maxStackFrames]
	}
	return err
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
	// files are the IDs of the input's files, the only ones a returned item
	// may pass on.
	files map[string]bool
}

// run calls the code for one item (or all of them) and returns its result as
// JSON, with its size, which is what the output limit measures.
func (call invocation) run(ctx context.Context, index int, maxOutput int64) (string, int64, error) {
	call.clock.start()
	text, err := call.v.invoke(ctx, call.function, call.mode, index, call.count, maxOutput)
	exhausted := call.clock.stop()
	if err == nil && exhausted {
		err = timedOut(call.limits.Timeout)
	}
	if err != nil {
		return "", 0, err
	}
	size := int64(len(text))
	if size > maxOutput {
		return "", 0, outputTooLarge(call.limits)
	}
	if err := checkFiles(text, call.files, call.mode == ModeEachItem); err != nil {
		return "", 0, err
	}
	return text, size, nil
}

// order sorts the input with a comparator, with the clock running around
// the sort, and returns the order as JSON. returns are the lines of the
// comparator's own return statements: once one has answered, which it was
// cannot be told, so a wrong answer is located only when there is one.
func (call invocation) order(returns []int) (string, error) {
	call.clock.start()
	text, err := call.v.sortOrder(call.function)
	exhausted := call.clock.stop()
	if err == nil && exhausted {
		err = timedOut(call.limits.Timeout)
	}
	var invalid *LimitError
	if len(returns) == 1 && errors.As(err, &invalid) && errors.Is(invalid.Cause, ErrInvalidReturn) {
		invalid.Detail += location(returns[0], -1)
	}
	if err != nil {
		return "", err
	}
	if int64(len(text)) > call.limits.MaxOutputBytes {
		return "", outputTooLarge(call.limits)
	}
	return text, nil
}

func outputTooLarge(limits Limits) error {
	return named(ErrOutputLimit, fmt.Sprintf("code produced more output than the %s limit allows", byteSize(limits.MaxOutputBytes)))
}

// forItem names the item a per-item failure happened on. An uncaught error
// may come from an earlier item's callback, so it names none.
func forItem(err error, index int) error {
	var script *ScriptError
	if errors.As(err, &script) {
		if !script.Uncaught {
			script.ItemIndex = index
		}
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
