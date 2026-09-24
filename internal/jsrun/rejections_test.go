package jsrun_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// An exception thrown from a callback, or a promise rejected with nothing to
// handle it, is uncaught. In n8n that ends the task runner's process and
// fails the node, provided the node is still running when the queue of
// promise jobs drains. The run fails the same way here, with the error the
// code threw (BUG-548bk9).

// sleep is a wait on a timer, which is where the queue of promise jobs
// drains while the code is still running.
const sleep = "await new Promise((resolve) => setTimeout(resolve, 10))\n"

func uncaught(t *testing.T, err error) *jsrun.ScriptError {
	t.Helper()
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || !script.Uncaught {
		t.Fatalf("Run() error = %v, want an uncaught script error", err)
	}
	return script
}

func TestAnUncaughtErrorFailsTheRun(t *testing.T) {
	for name, test := range map[string]struct {
		source, text string
		line         int
	}{
		"a throwing crypto callback": {
			source: "require('crypto').randomBytes(4, () => { throw new Error('lost') })\n" + sleep + "return items",
			text:   "Uncaught Error: lost [line 1]", line: 1,
		},
		"a rejection with no handler": {
			source: "const values = [1]\nPromise.reject(new TypeError('rejected'))\n" + sleep + "return items",
			text:   "Uncaught TypeError: rejected [line 2]", line: 2,
		},
		"a rejection with a reason that is not an Error": {
			source: "Promise.reject('plain string')\n" + sleep + "return items",
			text:   "Uncaught plain string",
		},
		"a handler attached after the queue drained": {
			source: "const late = Promise.reject(new Error('late'))\n" + sleep + "late.catch(() => {})\nreturn items",
			text:   "Uncaught Error: late [line 1]", line: 1,
		},
		"an async function nobody awaits": {
			source: "(async () => { throw new Error('async') })()\n" + sleep + "return items",
			text:   "Uncaught Error: async [line 1]", line: 1,
		},
		"a throwing reaction": {
			source: "Promise.resolve().then(() => { throw new Error('then') })\n" + sleep + "return items",
			text:   "Uncaught Error: then [line 1]", line: 1,
		},
		"a throwing timer": {
			source: "setTimeout(() => { throw new Error('timer') }, 0)\n" + sleep + "return items",
			text:   "Uncaught Error: timer [line 1]", line: 1,
		},
		"a throwing microtask": {
			source: "queueMicrotask(() => { throw new Error('micro') })\n" + sleep + "return items",
			text:   "Uncaught Error: micro [line 1]", line: 1,
		},
		"code that waits forever": {
			source: "Promise.reject(new Error('first'))\nawait new Promise(() => {})",
			text:   "Uncaught Error: first [line 1]", line: 1,
		},
	} {
		t.Run(name, func(t *testing.T) {
			_, err := runAll(t, newRunner(), test.source, numbered(1))
			script := uncaught(t, err)
			if err.Error() != test.text || script.Line != test.line {
				t.Fatalf("Run() error = %q (line %d), want %q (line %d)", err, script.Line, test.text, test.line)
			}
		})
	}
}

// The first uncaught error ends the run before the code gets any further,
// so a later throw of its own does not replace it.
func TestTheFirstUncaughtErrorEndsTheRun(t *testing.T) {
	_, err := runAll(t, newRunner(), "Promise.reject(new Error('other'))\n"+sleep+"throw new Error('mine')", nil)
	if uncaught(t, err); !strings.Contains(err.Error(), "other") {
		t.Fatalf("Run() error = %v, want the uncaught rejection, not the later throw", err)
	}
}

// A rejection is unhandled only if it is still unhandled once the queue of
// promise jobs has drained, and only if the code is still running then: the
// code's own result is delivered first.
func TestWhatIsNotAnUncaughtError(t *testing.T) {
	for name, source := range map[string]string{
		"a handler attached in the same turn":   "const p = Promise.reject(new Error('late'))\nawait null\np.catch(() => {})\n" + sleep + "return items",
		"an await that handles it":              "try { await Promise.reject(new Error('caught')) } catch (error) {}\n" + sleep + "return items",
		"a rejection as the code returns":       "Promise.reject(new Error('rejected'))\nreturn items",
		"a rejection a turn before it returns":  "Promise.reject(new Error('rejected'))\nawait null\nreturn items",
		"a callback that throws after it":       "require('crypto').randomBytes(4, () => { throw new Error('lost') })\nreturn items",
		"a timer that throws after it":          "setTimeout(() => { throw new Error('timer') }, 5)\nreturn items",
		"a returned promise that is awaited":    "return Promise.resolve(items)",
		"a promisified callback that is caught": "const wait = require('util').promisify((callback) => callback(new Error('no')))\nawait wait().catch(() => {})\n" + sleep + "return items",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := runAll(t, newRunner(), source, numbered(1)); err != nil {
				t.Fatalf("Run() error = %v, want the run to succeed", err)
			}
		})
	}
}

// What the code itself throws is its result, which the runner consumes: it
// fails its own item, and does not come back as uncaught while a later item
// runs.
func TestTheCodesOwnFailureIsNotUncaught(t *testing.T) {
	_, err := runAll(t, newRunner(), sleep+"throw new Error('mine')", nil)
	var script *jsrun.ScriptError
	if !errors.As(err, &script) || script.Uncaught || err.Error() != "Error: mine [line 2]" {
		t.Fatalf("Run() error = %v, want the code's own error", err)
	}

	result, err := newRunner().Run(context.Background(), jsrun.Task{
		Source: "if ($itemIndex === 0) throw new Error('first item')\n" + sleep + "return $input.item",
		Items:  numbered(2), Mode: jsrun.ModeEachItem, ContinueOnItemError: true,
	})
	if err != nil || len(result.Outcomes) != 2 || result.Outcomes[0].Error == nil || result.Outcomes[1].Error != nil {
		t.Fatalf("Run() = %#v, %v; want item 0 failed and item 1 through", result.Outcomes, err)
	}
}

// An uncaught error belongs to no item: in n8n it ends the whole runner, so
// it ends the run even when failed items are tolerated. One an earlier item
// left behind surfaces while a later item is still running, and is not
// blamed on it.
func TestAnUncaughtErrorIsNotAnItemsFailure(t *testing.T) {
	for name, test := range map[string]struct{ source, text string }{
		"a rejection while the item runs": {
			source: "if ($itemIndex === 1) Promise.reject(new Error('stray'))\n" + sleep + "return $input.item",
			text:   "Uncaught Error: stray [line 1]",
		},
		"a rejection as an earlier item returns": {
			source: "if ($itemIndex === 0) { Promise.reject(new Error('stray')); return $input.item }\n" + sleep + "return $input.item",
			text:   "Uncaught Error: stray [line 1]",
		},
		"a timer an earlier item armed": {
			source: "if ($itemIndex === 0) { setTimeout(() => { throw new Error('timer') }, 0); return $input.item }\n" + sleep + "return $input.item",
			text:   "Uncaught Error: timer [line 1]",
		},
	} {
		t.Run(name, func(t *testing.T) {
			result, err := newRunner().Run(context.Background(), jsrun.Task{
				Source: test.source, Items: numbered(3), Mode: jsrun.ModeEachItem, ContinueOnItemError: true,
			})
			script := uncaught(t, err)
			if script.ItemIndex != -1 || err.Error() != test.text || len(result.Outcomes) != 0 {
				t.Fatalf("Run() = %d outcomes, error %q, item %d; want the run ended with %q", len(result.Outcomes), err, script.ItemIndex, test.text)
			}
		})
	}
}

// A helper's promise is handed to the code, so it is the code's to handle,
// as in Node: one that fails with no handler is uncaught, and one the code
// awaits is not.
func TestAHelpersPromiseIsTheCodes(t *testing.T) {
	failing := &fakeHelpers{http: func(context.Context, jsrun.HTTPRequest) (jsrun.HTTPResponse, []byte, error) {
		return jsrun.HTTPResponse{}, nil, errors.New("the host said no")
	}}
	request := "this.helpers.httpRequest({ url: 'https://example.com' })"
	for _, source := range []string{request + "\n" + sleep + "return items", request + ".then(() => {})\n" + sleep + "return items"} {
		_, err := helperRun(t, failing, source)
		if uncaught(t, err); !strings.Contains(err.Error(), "the host said no") {
			t.Fatalf("Run() error = %v, want the helper's failure uncaught", err)
		}
	}
	if _, err := helperRun(t, failing, "try { await "+request+" } catch (error) {}\n"+sleep+"return items"); err != nil {
		t.Fatalf("Run() error = %v, want an awaited helper's failure handled", err)
	}
}

// The flag crosses from a worker process with the rest of the error.
func TestAnUncaughtErrorCrossesTheWire(t *testing.T) {
	_, err := runAll(t, newRunner(), "Promise.reject(new Error('stray'))\n"+sleep+"return items", nil)
	decoded := jsrun.EncodeError(err).Decode()
	if uncaught(t, decoded); decoded.Error() != err.Error() {
		t.Fatalf("decoded %q, want %q", decoded, err)
	}
}
