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
// pending, and that fails the run.
//
// Two kinds of promise are the runtime's own, and are never counted:
//
//   - the promise the code's body returns, which await consumes;
//   - a host call's promise.
//
// A promise the code derives from either is its own.
type rejections struct {
	next int64
	// unhandled holds each rejected promise with no handler, with the order
	// it was rejected in.
	unhandled map[*goja.Promise]int64
	// own holds the host calls' promises.
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

// ownPromise marks a promise the runtime made for itself.
func (v *vm) ownPromise(promise *goja.Promise) { v.rejected.own[promise] = true }

// consumed takes a settled promise out of the count, because the runner has
// read it.
func (v *vm) consumed(promise *goja.Promise) { delete(v.rejected.unhandled, promise) }

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
