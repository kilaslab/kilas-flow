package jsrun

import (
	"time"

	"github.com/dop251/goja"
)

// Timers run on the execution's own loop, like the host calls: a Go timer
// posts the callback to the VM's goroutine through jobs, where await runs it.
// Every armed timer is pending work, so a script waiting on one is waiting on
// something that will happen rather than on nothing. No timer outlives its
// execution: closing the VM stops them all.

// maxTimers bounds the timers one run may have armed at once.
const maxTimers = 10_000

// maxTimerDelay is the longest delay Node accepts; a longer or invalid one
// fires after a millisecond, as in Node.
const maxTimerDelay = 1<<31 - 1

type timerEntry struct {
	timer     *time.Timer
	cancelled bool
}

// timerStart arms timer id to call fire(id) after a delay in milliseconds.
// It runs on the VM's goroutine.
func (v *vm) timerStart(call goja.FunctionCall) goja.Value {
	id := call.Argument(0).ToInteger()
	delay := call.Argument(1).ToInteger()
	fire, ok := goja.AssertFunction(call.Argument(2))
	if !ok {
		panic(v.rt.NewTypeError("jsrun: a timer needs a callback"))
	}
	if len(v.timers) >= maxTimers {
		panic(v.jsError(rangeError("the code has more than %d timers waiting at once", maxTimers)))
	}
	if delay < 1 || delay > maxTimerDelay {
		delay = 1
	}
	entry := &timerEntry{}
	v.pending++
	entry.timer = time.AfterFunc(time.Duration(delay)*time.Millisecond, func() {
		job := func() error {
			if entry.cancelled {
				return nil
			}
			delete(v.timers, id)
			_, err := fire(goja.Undefined(), v.rt.ToValue(id))
			return err
		}
		select {
		case v.jobs <- job:
		case <-v.done:
		}
	})
	v.timers[id] = entry
	return goja.Undefined()
}

// timerCancel disarms timer id. A timer that already fired has its job on
// the way; it is marked so the job does nothing, and await still counts it
// off when it arrives.
func (v *vm) timerCancel(call goja.FunctionCall) goja.Value {
	id := call.Argument(0).ToInteger()
	entry, ok := v.timers[id]
	if !ok {
		return goja.Undefined()
	}
	delete(v.timers, id)
	entry.cancelled = true
	if entry.timer.Stop() {
		v.pending--
	}
	return goja.Undefined()
}

// stopTimers disarms every timer when the VM closes.
func (v *vm) stopTimers() {
	for _, entry := range v.timers {
		entry.cancelled = true
		entry.timer.Stop()
	}
}
