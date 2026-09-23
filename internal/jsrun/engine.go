package jsrun

// engine.go is the only file that imports goja's runtime (see
// internal/guardrails). Everything engine-specific lives here, so moving to
// the fallback engine is one new adapter and a green test suite.

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/dlclark/regexp2/v2"
	"github.com/dop251/goja"
	"github.com/dop251/goja/ast"
	"github.com/dop251/goja/parser"
)

// The regular-expression match timeout.
//
// goja runs a backtracking pattern inside one Go call that an interrupt
// cannot stop, and it reports regexp2's match timeout as "no match", which
// would silently change what a script computes. So the match timeout always
// covers the time-limit ceiling: a match can then only time out after the
// script's interrupt was delivered, and the script stops at its next
// instruction without ever seeing the wrong answer.
//
// The timeout is baked into a pattern when goja compiles it, so it is part of
// every compiled program's cache key, and compiling holds the read lock while
// raising it holds the write lock.
var matchTimeout = struct {
	sync.RWMutex
	value time.Duration
}{}

func init() {
	coverTimeLimit(DefaultLimits().Timeout)
}

// matchTimeoutFor is comfortably longer than the limit it covers.
func matchTimeoutFor(limit time.Duration) time.Duration {
	return limit + limit/10 + 100*time.Millisecond
}

// coverTimeLimit raises the match timeout so it covers limit. It only ever
// raises it: a lower timeout could fire inside a runner whose ceiling is
// higher.
func coverTimeLimit(limit time.Duration) {
	matchTimeout.Lock()
	defer matchTimeout.Unlock()
	if want := matchTimeoutFor(limit); want > matchTimeout.value {
		matchTimeout.value = want
		regexp2.DefaultMatchTimeout = want
	}
}

func currentMatchTimeout() time.Duration {
	matchTimeout.RLock()
	defer matchTimeout.RUnlock()
	return matchTimeout.value
}

// program is a compiled script, safe to run in many VMs at once.
type program struct {
	compiled *goja.Program
}

// compileProgram compiles a parsed body. Sloppy mode, because user code
// assigns undeclared variables as often as not.
func compileProgram(parsed *ast.Program, w wrapped) (*program, error) {
	matchTimeout.RLock()
	compiled, err := goja.CompileAST(parsed, false)
	matchTimeout.RUnlock()
	if err == nil {
		return &program{compiled: compiled}, nil
	}
	var syntax *goja.CompilerSyntaxError
	if errors.As(err, &syntax) {
		line, column := 0, 0
		if syntax.File != nil {
			position := syntax.File.Position(syntax.Offset)
			line, column = w.userLine(position.Line), position.Column
		}
		// The analyser refuses both of these first; this keeps the wording
		// right if a form it does not see reaches the compiler.
		switch {
		case strings.Contains(syntax.Message, "Async generators"):
			return nil, &UnsupportedError{Found: []Unsupported{{Subject: "uses an async generator", Line: line}}}
		case strings.HasPrefix(syntax.Message, "Invalid flags supplied to RegExp"):
			return nil, &UnsupportedError{Found: []Unsupported{{Subject: "uses a regular-expression flag this server does not support", Line: line}}}
		}
		return nil, &SyntaxError{Message: syntax.Message, Line: line, Column: column}
	}
	var reference *goja.CompilerReferenceError
	if errors.As(err, &reference) {
		line := 0
		if reference.File != nil {
			line = w.userLine(reference.File.Position(reference.Offset).Line)
		}
		return nil, &SyntaxError{Message: reference.Message, Line: line}
	}
	return nil, &SyntaxError{Message: err.Error()}
}

// compileTrusted compiles KilasFlow's own scripts and vendored libraries.
func compileTrusted(name, source string) (*program, error) {
	matchTimeout.RLock()
	defer matchTimeout.RUnlock()
	compiled, err := goja.Compile(name, source, false)
	if err != nil {
		return nil, fmt.Errorf("compiling %s: %w", name, err)
	}
	return &program{compiled: compiled}, nil
}

//go:embed js/runtime.js
var runtimeSource string

var runtimeKit = sync.OnceValues(func() (*program, error) {
	return compileTrusted("kilasflow:runtime", runtimeSource)
})

// value is a JavaScript value, opaque to the rest of the package.
type value struct {
	v goja.Value
}

// vm is one execution's JavaScript VM. It is used by one goroutine, the one
// running the execution, except for interrupt, which is safe from any.
type vm struct {
	rt     *goja.Runtime
	limits Limits
	w      wrapped

	jsonParse goja.Callable
	normalise goja.Callable
	stringify goja.Callable
	describe  goja.Callable
	// installRoots builds the roots; run is what it returns, the one
	// function the user's code is called through.
	installRoots goja.Callable
	run          goja.Callable
	// errorTypes are the error constructors, captured before any user code,
	// that natives throw through.
	errorTypes map[string]goja.Value
	// timers are the armed timers, by the id the timers module gave them.
	timers map[int64]*timerEntry

	// jobs carries completions of asynchronous host work back to the VM's
	// goroutine, which is the only one allowed to settle a promise.
	jobs    chan func() error
	pending int

	hostCalls int
	hostCtx   context.Context
	stopHost  context.CancelFunc
	done      chan struct{}

	// wake is closed by the first interrupt, so a VM idle in await, where an
	// interrupt alone does nothing, notices it too.
	wake     chan struct{}
	wakeOnce sync.Once
	reasonMu sync.Mutex
	reason   error

	// onInterrupt is a test seam recording when an interrupt was delivered.
	onInterrupt func(time.Time)
}

// vmsCreated counts VMs, so a test can prove a refusal happened before one.
var vmsCreated atomic.Int64

func newVM(limits Limits) (*vm, error) {
	kit, err := runtimeKit()
	if err != nil {
		return nil, err
	}
	vmsCreated.Add(1)
	rt := goja.New()
	// eval and new Function parse at run time; without this they would read
	// a file named by a trailing sourceMappingURL comment.
	rt.SetParserOptions(parser.WithDisableSourceMaps)
	rt.SetMaxCallStackSize(limits.MaxCallDepth)
	hostCtx, stopHost := context.WithCancel(context.Background())
	v := &vm{
		rt: rt, limits: limits, timers: map[int64]*timerEntry{},
		jobs: make(chan func() error, 64), hostCtx: hostCtx, stopHost: stopHost,
		done: make(chan struct{}), wake: make(chan struct{}),
	}
	// Captured before any other code runs, so a script that replaces
	// JSON.parse or an error constructor cannot reach the runner's own.
	v.errorTypes = map[string]goja.Value{}
	for _, name := range []string{"Error", "TypeError", "RangeError"} {
		v.errorTypes[name] = rt.Get(name)
	}
	jsonObject := rt.Get("JSON").ToObject(rt)
	if v.jsonParse, err = callable(jsonObject.Get("parse")); err != nil {
		return nil, err
	}
	enableNodeGlobals(rt)
	helpers, err := rt.RunProgram(kit.compiled)
	if err != nil {
		return nil, fmt.Errorf("installing the runtime helpers: %w", err)
	}
	helperObject := helpers.ToObject(rt)
	for name, target := range map[string]*goja.Callable{
		"normalise": &v.normalise, "stringify": &v.stringify, "describe": &v.describe, "install": &v.installRoots,
	} {
		if *target, err = callable(helperObject.Get(name)); err != nil {
			return nil, err
		}
	}
	return v, nil
}

func callable(candidate goja.Value) (goja.Callable, error) {
	function, ok := goja.AssertFunction(candidate)
	if !ok {
		return nil, errors.New("jsrun: runtime helper is not a function")
	}
	return function, nil
}

// interrupt stops the script with reason. The first reason wins, so a limit
// that fired is not relabelled by the cancellation that follows it.
func (v *vm) interrupt(reason error) {
	v.reasonMu.Lock()
	if v.reason == nil {
		v.reason = reason
	}
	v.reasonMu.Unlock()
	if v.onInterrupt != nil {
		v.onInterrupt(time.Now())
	}
	v.rt.Interrupt(reason)
	v.wakeOnce.Do(func() { close(v.wake) })
}

func (v *vm) stopReason() error {
	v.reasonMu.Lock()
	defer v.reasonMu.Unlock()
	if v.reason == nil {
		return timedOut(v.limits.Timeout)
	}
	return v.reason
}

func (v *vm) close() {
	close(v.done)
	v.stopHost()
	v.stopTimers()
}

// load runs a trusted program: a library or KilasFlow's own setup.
func (v *vm) load(p *program) error {
	return guard(func() error {
		if _, err := v.rt.RunProgram(p.compiled); err != nil {
			return v.fail(err)
		}
		return nil
	})
}

// body runs the wrapper program, which only evaluates a function expression,
// and returns the function that runs the user's code.
func (v *vm) body(prepared *prepared) (function value, err error) {
	v.w = prepared.wrapped
	err = guard(func() error {
		compiled, err := v.rt.RunProgram(prepared.program.compiled)
		if err != nil {
			return v.fail(err)
		}
		function = value{compiled}
		return nil
	})
	return function, err
}

// parse runs the JSON.parse captured before any user code.
func (v *vm) parse(text string) (parsed value, err error) {
	err = guard(func() error {
		result, err := v.jsonParse(goja.Undefined(), v.rt.ToValue(text))
		if err != nil {
			return v.fail(err)
		}
		parsed = value{result}
		return nil
	})
	return parsed, err
}

// install builds the Code node's roots over the parsed input. It runs
// trusted code only, before any user code, and is not charged. The host
// callbacks are handed to the runtime's closure, not set as globals, so a
// script can reach them only through the roots that use them.
func (v *vm) install(state string, input value, mode Mode, h host) error {
	return guard(func() error {
		snapshot, err := v.jsonParse(goja.Undefined(), v.rt.ToValue(state))
		if err != nil {
			return v.fail(err)
		}
		callbacks := v.rt.NewObject()
		for name, function := range map[string]func(goja.FunctionCall) goja.Value{
			"node": func(call goja.FunctionCall) goja.Value {
				text, ok := h.node(call.Argument(0).String())
				if !ok {
					return goja.Null()
				}
				return v.rt.ToValue(text)
			},
			"pair": func(call goja.FunctionCall) goja.Value {
				index, reason := h.pair(call.Argument(0).String(), int(call.Argument(1).ToInteger()))
				if index < 0 {
					return v.rt.ToValue(reason)
				}
				return v.rt.ToValue(index)
			},
			"console": func(call goja.FunctionCall) goja.Value {
				return v.rt.ToValue(h.console(call.Argument(0).String(), call.Argument(1).String()))
			},
			"native":      v.callNative,
			"library":     v.loadLibrary,
			"timerStart":  v.timerStart,
			"timerCancel": v.timerCancel,
		} {
			if err := callbacks.Set(name, function); err != nil {
				return err
			}
		}
		factories := v.rt.NewObject()
		for _, name := range moduleOrder {
			compiled, err := moduleProgram(name)
			if err != nil {
				return err
			}
			factory, err := v.rt.RunProgram(compiled.compiled)
			if err != nil {
				return v.fail(err)
			}
			if err := factories.Set(name, factory); err != nil {
				return err
			}
		}
		api, err := v.installRoots(goja.Undefined(), snapshot, input.v, v.rt.ToValue(mode.orDefault() == ModeEachItem), callbacks, factories)
		if err != nil {
			return v.fail(err)
		}
		v.run, err = callable(api.ToObject(v.rt).Get("run"))
		return err
	})
}

// callNative runs a Go native for a module: native(name, ...args).
func (v *vm) callNative(call goja.FunctionCall) goja.Value {
	name := call.Argument(0).String()
	fn, ok := natives[name]
	if !ok {
		panic(v.rt.NewTypeError("jsrun: no native named " + name))
	}
	arguments := make([]any, 0, len(call.Arguments))
	for _, argument := range call.Arguments[1:] {
		arguments = append(arguments, exportNative(argument))
	}
	result, err := fn(arguments)
	if err != nil {
		panic(v.jsError(err))
	}
	converted, err := v.toJS(result)
	if err != nil {
		panic(v.jsError(err))
	}
	return converted
}

// exportNative turns a JavaScript value into a native's plain argument.
// Typed arrays and ArrayBuffers arrive as bytes.
func exportNative(value goja.Value) any {
	if value == nil || goja.IsUndefined(value) || goja.IsNull(value) {
		return nil
	}
	switch exported := value.Export().(type) {
	case goja.ArrayBuffer:
		return exported.Bytes()
	case []byte:
		return exported
	default:
		return exported
	}
}

// jsError is the JavaScript error a native's error is thrown as.
func (v *vm) jsError(err error) goja.Value {
	name, message := "Error", err.Error()
	var named *nativeError
	if errors.As(err, &named) {
		name, message = named.name, named.message
	}
	constructor, ok := v.errorTypes[name]
	if !ok {
		constructor = v.errorTypes["Error"]
	}
	thrown, failure := v.rt.New(constructor, v.rt.ToValue(message))
	if failure != nil {
		return v.rt.NewGoError(err)
	}
	return thrown
}

// loadLibrary runs a vendored library in the VM and returns what it exports.
// It is called before the code runs for the libraries the analysis saw, and
// from require() for any other, where it is charged like the rest of the
// code's work.
func (v *vm) loadLibrary(call goja.FunctionCall) goja.Value {
	lib, ok := libraries[call.Argument(0).String()]
	if !ok {
		return goja.Undefined()
	}
	compiled, err := lib.program()
	if err != nil {
		panic(v.jsError(err))
	}
	exported, err := v.rt.RunProgram(compiled.compiled)
	if err != nil {
		// An interrupt or a stack overflow stays uncatchable; anything else
		// becomes an error the code can see.
		var interrupted *goja.InterruptedError
		var overflow *goja.StackOverflowError
		if errors.As(err, &interrupted) || errors.As(err, &overflow) {
			panic(err)
		}
		panic(v.jsError(err))
	}
	return exported
}

// guard runs one entry into the VM, turning a panic into an error. goja
// re-panics anything it does not recognise as a JavaScript error, so a bug in
// the engine surfaces as a Go panic, and that must fail one run, never the
// server. The VM is discarded afterwards either way.
func guard(entry func() error) (err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			err = EngineFaultError(fmt.Sprint(recovered))
		}
	}()
	return entry()
}

// invoke is one charged call: it runs the user's code for one item (or all
// of them), waits for it, and turns the result into JSON. Everything in it
// can run user code, which is why the caller's clock is running around all
// of it.
func (v *vm) invoke(ctx context.Context, function value, mode Mode, index, count int, maxOutput int64) (text string, err error) {
	err = guard(func() error {
		returned, err := v.run(goja.Undefined(), function.v, v.rt.ToValue(index))
		if err != nil {
			return v.fail(err)
		}
		settled, err := v.await(ctx, returned)
		if err != nil {
			return err
		}
		eachItem := mode.orDefault() == ModeEachItem
		items, err := v.normalise(goja.Undefined(), settled, v.rt.ToValue(eachItem), v.rt.ToValue(index), v.rt.ToValue(count))
		if err != nil {
			return v.fail(err)
		}
		encoded, err := v.stringify(goja.Undefined(), items, v.rt.ToValue(maxOutput))
		if err != nil {
			return v.fail(err)
		}
		text = encoded.String()
		return nil
	})
	return text, err
}

// await drives a promise to settlement on this goroutine. goja settles
// promise reactions only when the VM is entered, so completions of host work
// come back through jobs and are run here, where running a resolver also runs
// the continuations it unblocks.
func (v *vm) await(ctx context.Context, returned goja.Value) (goja.Value, error) {
	promise, ok := returned.Export().(*goja.Promise)
	if !ok {
		return returned, nil
	}
	for {
		switch promise.State() {
		case goja.PromiseStateFulfilled:
			return promise.Result(), nil
		case goja.PromiseStateRejected:
			return nil, v.thrown(promise.Result())
		}
		if v.pending == 0 && len(v.jobs) == 0 {
			return nil, named(ErrNeverSettles, "the code waits for a promise that nothing can ever settle")
		}
		select {
		case job := <-v.jobs:
			v.pending--
			if err := job(); err != nil {
				return nil, v.fail(err)
			}
		case <-v.wake:
			return nil, v.stopReason()
		case <-ctx.Done():
			v.interrupt(ctx.Err())
			return nil, v.stopReason()
		}
	}
}

// bindAsync installs a global host function that returns a promise. fn runs
// on its own goroutine with a context that ends with the VM; its result is
// handed back through jobs.
func (v *vm) bindAsync(name string, fn func(context.Context, []any) (any, error)) error {
	return v.rt.Set(name, func(call goja.FunctionCall) goja.Value {
		if !v.countHostCall() {
			return goja.Undefined()
		}
		arguments := make([]any, len(call.Arguments))
		for index, argument := range call.Arguments {
			arguments[index] = argument.Export()
		}
		promise, resolve, reject := v.rt.NewPromise()
		v.pending++
		go func() {
			result, err := hostCall(v.hostCtx, fn, arguments)
			job := func() error {
				if err != nil {
					return reject(v.rt.NewGoError(err))
				}
				settled, err := v.toJS(result)
				if err != nil {
					return reject(v.rt.NewGoError(err))
				}
				return resolve(settled)
			}
			select {
			case v.jobs <- job:
			case <-v.done:
			}
		}()
		return v.rt.ToValue(promise)
	})
}

// hostCall runs one asynchronous host function on its own goroutine. A panic
// there is outside every guard on the VM's goroutine and would end the
// process, so it is turned into the call's error, which rejects the promise
// the script is waiting on.
func hostCall(ctx context.Context, fn func(context.Context, []any) (any, error), arguments []any) (result any, err error) {
	defer func() {
		if recovered := recover(); recovered != nil {
			result, err = nil, fmt.Errorf("the host call failed (%v); this is a fault in the server, not in the code", recovered)
		}
	}()
	return fn(ctx, arguments)
}

// countHostCall charges one host call, and stops the script when the budget
// is spent.
func (v *vm) countHostCall() bool {
	v.hostCalls++
	if v.hostCalls <= v.limits.MaxHostCalls {
		return true
	}
	v.interrupt(named(ErrHostCallLimit, fmt.Sprintf("code made more than %d host calls", v.limits.MaxHostCalls)))
	return false
}

// toJS turns a host result into a plain JavaScript value. Structured values
// go through JSON, so a script gets ordinary objects rather than live views
// of Go maps.
func (v *vm) toJS(result any) (goja.Value, error) {
	switch result := result.(type) {
	case nil:
		return goja.Undefined(), nil
	case string, bool, int, int64, float64:
		return v.rt.ToValue(result), nil
	case []byte:
		return v.rt.ToValue(v.rt.NewArrayBuffer(result)), nil
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	parsed, err := v.jsonParse(goja.Undefined(), v.rt.ToValue(string(encoded)))
	if err != nil {
		return nil, err
	}
	return parsed, nil
}

// fail turns an error from goja into this package's errors.
func (v *vm) fail(err error) error {
	var interrupted *goja.InterruptedError
	if errors.As(err, &interrupted) {
		return v.stopReason()
	}
	var overflow *goja.StackOverflowError
	if errors.As(err, &overflow) {
		return named(ErrCallDepth, fmt.Sprintf("code called functions nested more than %d deep", v.limits.MaxCallDepth))
	}
	var exception *goja.Exception
	if errors.As(err, &exception) {
		return v.thrown(exception.Value())
	}
	return err
}

// userFrame matches one frame of the user's code in a goja stack trace.
var userFrame = regexp.MustCompile(`(?:^|[\s(])` + sourceName + `:(\d+):(\d+)\(\d+\)`)

// thrown describes what the script threw, in the user's coordinates.
func (v *vm) thrown(thrown goja.Value) error {
	described, err := v.describe(goja.Undefined(), thrown)
	if err != nil {
		var interrupted *goja.InterruptedError
		if errors.As(err, &interrupted) {
			return v.stopReason()
		}
		return &ScriptError{Message: "the code threw a value that cannot be shown as text", ItemIndex: -1}
	}
	fields := described.ToObject(v.rt)
	text := func(name string) string {
		field := fields.Get(name)
		if field == nil || goja.IsUndefined(field) {
			return ""
		}
		return field.String()
	}
	switch text("kind") {
	case "output":
		return named(ErrOutputLimit, fmt.Sprintf("code produced more output than the %s limit allows", byteSize(v.limits.MaxOutputBytes)))
	case "invalid":
		return named(ErrInvalidReturn, text("message"))
	}
	failure := &ScriptError{Name: text("name"), Message: text("message"), ItemIndex: -1}
	for _, frame := range userFrame.FindAllStringSubmatch(text("stack"), -1) {
		line, _ := strconv.Atoi(frame[1])
		column, _ := strconv.Atoi(frame[2])
		// The wrapper's own frames sit on its prelude and trailer lines.
		if line <= 1 || line-1 > v.w.lines {
			continue
		}
		if failure.Line == 0 {
			failure.Line, failure.Column = line-1, column
		}
		failure.Stack = append(failure.Stack, fmt.Sprintf("%d:%d", line-1, column))
	}
	return failure
}

func byteSize(size int64) string {
	switch {
	case size >= 1<<20 && size%(1<<20) == 0:
		return fmt.Sprintf("%d MiB", size>>20)
	case size >= 1<<10 && size%(1<<10) == 0:
		return fmt.Sprintf("%d KiB", size>>10)
	}
	return fmt.Sprintf("%d B", size)
}
