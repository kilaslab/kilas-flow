package jsrun

import (
	"errors"

	"github.com/dop251/goja"
)

// Uncaught errors (BUG-548bk9).
//
// In Node an exception thrown from a callback, or a promise rejected with
// nothing to handle it, is uncaught, and it ends the process. n8n runs a Code
// node in a task-runner process, so such an error fails the node, as long as
// the node is still running when Node looks. Node looks once the queue of
// promise jobs has drained. A rejection handled before then is not unhandled,
// and a node that returned in the same turn has already delivered its result.
//
// goja tells the VM when a promise is rejected with no handler and when one
// is handled later. The rejections still unhandled are kept in order.
// Whenever a VM entry returns, goja has drained its job queue. At that point
// await asks for the first of them while the code's promise is still
// pending, and that fails the run. A job, such as a timer's callback, that
// throws is uncaught in the same way.
//
// Every promise the code can reach counts, a host call's included: the code
// was handed it, so handling it is the code's job, as in Node. Only a promise
// the runtime makes and consumes itself is never counted: the one the code's
// body returns, which await reads, and any promise marked with ownPromise.
type rejections struct {
	next int64
	// unhandled holds each rejected promise with no handler, with the order
	// it was rejected in.
	unhandled map[*goja.Promise]int64
	// own holds the promises the runtime consumes itself.
	own map[*goja.Promise]bool
}

// trackRejections installs the tracker. It runs on the VM's goroutine, inside
// whichever entry rejects or handles the promise.
func (v *vm) trackRejections() {
	v.rejected = rejections{unhandled: map[*goja.Promise]int64{}, own: map[*goja.Promise]bool{}}
	v.rt.SetPromiseRejectionTracker(func(promise *goja.Promise, operation goja.PromiseRejectionOperation) {
		switch {
		case operation == goja.PromiseRejectionHandle:
			delete(v.rejected.unhandled, promise)
		case !v.rejected.own[promise]:
			v.rejected.next++
			v.rejected.unhandled[promise] = v.rejected.next
		}
	})
}

// ownPromise marks a promise the runtime makes and consumes itself, which
// the code never receives, so its rejection is never the code's. It must be
// called before the promise can be rejected.
func (v *vm) ownPromise(promise *goja.Promise) { v.rejected.own[promise] = true }

// consumed takes a settled promise out of the count, because the runner has
// read it.
func (v *vm) consumed(promise *goja.Promise) { delete(v.rejected.unhandled, promise) }

// failedJob is the error a job ended the run with. A job runs outside
// anything the code could catch it in, so what the code threw there is
// uncaught.
func (v *vm) failedJob(err error) error {
	var exception *goja.Exception
	thrown := errors.As(err, &exception)
	err = v.fail(err)
	var script *ScriptError
	if thrown && errors.As(err, &script) {
		script.Uncaught = true
	}
	return err
}

// uncaught is the first rejection still unhandled, as the error that ends the
// run, or nil if there is none.
func (v *vm) uncaught() error {
	var first *goja.Promise
	for promise, order := range v.rejected.unhandled {
		if first == nil || order < v.rejected.unhandled[first] {
			first = promise
		}
	}
	if first == nil {
		return nil
	}
	err := v.thrown(first.Result())
	var script *ScriptError
	if errors.As(err, &script) {
		script.Uncaught = true
	}
	return err
}
