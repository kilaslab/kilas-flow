package jsrun

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/dop251/goja"
)

// The helpers' side of the engine: a helper call handed to a goroutine and
// settled back on the VM's, and the static data read and written back.

// startHostCall is the runtime's call(request, bytes): it hands one helper's
// request to the host on a goroutine of its own and returns a promise the
// answer settles through the job loop. The code runs on meanwhile, and
// several calls may be in flight at once. Every call is charged against
// MaxHostCalls.
func (v *vm) startHostCall(answer func(context.Context, HostRequest) HostAnswer) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		var request HostRequest
		if err := json.Unmarshal([]byte(call.Argument(0).String()), &request); err != nil {
			panic(v.rt.NewTypeError("jsrun: a helper built a request that does not decode: " + err.Error()))
		}
		if data := exportNative(call.Argument(1)); data != nil {
			bytes, ok := data.([]byte)
			if !ok {
				panic(v.rt.NewTypeError("jsrun: a helper's data must be bytes"))
			}
			// Copied: the goroutine reads it while the code may write to the
			// buffer it came from.
			request.Data = append([]byte(nil), bytes...)
		}
		if !v.countHostCall() {
			// The script is being stopped; the promise it gets never settles.
			promise, _, _ := v.rt.NewPromise()
			return v.rt.ToValue(promise)
		}
		if size := int64(len(request.Data)); size > MaxFileBytes {
			v.interrupt(FileLimitError(dataName(request.Method), size))
			promise, _, _ := v.rt.NewPromise()
			return v.rt.ToValue(promise)
		}
		promise, resolve, reject := v.rt.NewPromise()
		v.pending++
		v.hostPending++
		go func() {
			answered := askHost(v.hostCtx, answer, request)
			job := func() error {
				v.hostPending--
				return v.settle(answered, resolve, reject)
			}
			select {
			case v.jobs <- job:
			case <-v.done:
			}
		}()
		return v.rt.ToValue(promise)
	}
}

// dataName says what a request's bytes are, for the limit error.
func dataName(method string) string {
	if method == HelperHTTPRequest {
		return "a request body"
	}
	return "a file"
}

// askHost runs one helper call on its goroutine. A panic there is outside
// every guard on the VM's goroutine and would end the process, so it becomes
// the call's failure, which rejects the promise the code holds.
func askHost(ctx context.Context, answer func(context.Context, HostRequest) HostAnswer, request HostRequest) (answered HostAnswer) {
	defer func() {
		if recovered := recover(); recovered != nil {
			answered = HostAnswer{Failure: fmt.Sprintf("the helper failed (%v); this is a fault in the server, not in the code", recovered)}
		}
	}()
	if answer == nil {
		return HostAnswer{Failure: "this.helpers." + request.Method + " is not available here"}
	}
	return answer(ctx, request)
}

// settle hands a helper's answer to the code. A named limit stops the code
// instead: the job leaves the VM interrupted, and await reports why.
func (v *vm) settle(answer HostAnswer, resolve, reject func(any) error) error {
	if answer.Error != nil {
		v.interrupt(answer.Error.Decode())
		return nil
	}
	if answer.Failure != "" {
		return reject(v.jsError(errors.New(answer.Failure)))
	}
	result := v.rt.NewObject()
	if answer.HTTP != nil {
		response, err := v.toJS(answer.HTTP)
		if err != nil {
			return reject(v.jsError(err))
		}
		if err := result.Set("response", response); err != nil {
			return err
		}
	}
	if answer.File != nil {
		v.files[answer.File.ID] = true
		file, err := v.toJS(answer.File)
		if err != nil {
			return reject(v.jsError(err))
		}
		if err := result.Set("file", file); err != nil {
			return err
		}
	}
	if err := result.Set("data", v.rt.NewArrayBuffer(answer.Data)); err != nil {
		return err
	}
	return resolve(result)
}

// readStaticData is the runtime's staticData(kind): the workflow's static
// data of one kind, as JSON, which the runtime parses once per run.
func (v *vm) readStaticData(read func(string) (string, error)) func(goja.FunctionCall) goja.Value {
	return func(call goja.FunctionCall) goja.Value {
		if read == nil {
			return v.rt.ToValue("{}")
		}
		text, err := read(call.Argument(0).String())
		if err != nil {
			panic(v.jsError(err))
		}
		return v.rt.ToValue(text)
	}
}

// exportStaticData writes back, as JSON, the static data the code was
// handed. The code may have replaced what the object holds, never the object
// itself, and what it holds must still be an object within the cap.
func (v *vm) exportStaticData() (static map[string]string, err error) {
	err = guard(func() error {
		state, err := v.staticState(goja.Undefined())
		if err != nil {
			return v.fail(err)
		}
		kinds := state.ToObject(v.rt)
		for _, kind := range []string{staticDataGlobal, staticDataNodeScope} {
			data := kinds.Get(kind)
			if data == nil || goja.IsUndefined(data) {
				continue
			}
			encoded, err := v.stringify(goja.Undefined(), data, v.rt.ToValue(MaxStaticDataBytes))
			if err != nil {
				if err := v.fail(err); !errors.Is(err, ErrOutputLimit) {
					return err
				}
				return StaticDataLimitError()
			}
			text := encoded.String()
			if len(text) > MaxStaticDataBytes {
				return StaticDataLimitError()
			}
			if len(text) == 0 || text[0] != '{' {
				return named(ErrInvalidReturn, fmt.Sprintf("the %s static data is no longer an object", kind))
			}
			if static == nil {
				static = map[string]string{}
			}
			static[kind] = text
		}
		return nil
	})
	return static, err
}
