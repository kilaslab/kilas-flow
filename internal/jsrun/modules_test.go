package jsrun_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// require() answers for the modules the runtime ships and nothing else:
// there is no npm and no module directory. A name the analyser could not see
// is refused when it is required, in the same words.
func TestRequireReturnsTheShippedModulesOnly(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const util = require('util')",
		"const same = util === require('node:util')",
		"const promised = await util.promisify((value, done) => done(null, value * 2))(21)",
		"return [{ json: {",
		"  same, promised, formatted: util.format('%s has %d', 'list', 3),",
		"  inspected: util.inspect({ a: [1, { b: 'two' }] }), date: util.types.isDate(new Date()),",
		"} }]",
	}, "\n")})
	got := result.Items[0].JSON
	for key, want := range map[string]any{
		"same": true, "promised": float64(42), "formatted": "list has 3",
		"inspected": "{ a: [ 1, { b: 'two' } ] }", "date": true,
	} {
		if got[key] != want {
			t.Errorf("%s = %#v, want %#v", key, got[key], want)
		}
	}

	_, err := runAll(t, newRunner(), "const name = 'child_' + 'process'\nconst cp = require(name)\nreturn []", nil)
	if err == nil || !strings.Contains(err.Error(), `this node's code requires the module "child_process", which this server does not run`) {
		t.Fatalf("Run() error = %v, want the module refused in the Refusal sentence", err)
	}
}

// A vendored library runs inside a function, so it leaves no global behind;
// n8n's Code node offers lodash through require, not as `_`.
func TestALibraryLoadsWithoutLeavingAGlobal(t *testing.T) {
	result := mustRun(t, newRunner(), jsrun.Task{Source: strings.Join([]string{
		"const _ = require('lodash')",
		"return [{ json: { chunks: _.chunk([1, 2, 3], 2).length, global: typeof globalThis._, again: _ === require('lodash') } }]",
	}, "\n")})
	got := result.Items[0].JSON
	if got["chunks"] != float64(2) || got["global"] != "undefined" || got["again"] != true {
		t.Fatalf("items = %#v, want lodash working, cached, and no global _", got)
	}
}
