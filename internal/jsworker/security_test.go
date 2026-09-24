package jsworker

import (
	"context"
	"fmt"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// A worker runs jobs for every tenant in turn, each on a fresh VM, so what
// one job changes of the built-ins, the global object, a library or a
// module is gone in the next, whoever it runs for. The worker process keeps
// only compiled programs and the runtime's own tables, which no script can
// write to (FEAT-vjjs8t; internal/jsrun's security tests prove the same in
// process).
func TestOneTenantsJobCannotReachTheNextOnTheSameWorker(t *testing.T) {
	pool := newTestPool(t, Options{MaxConcurrent: 1})
	tenantA := jsrun.Roots{Workflow: jsrun.WorkflowInfo{ID: "wf_tenant_a", Name: "Tenant A"}, Env: map[string]string{"SECRET_OF_A": "a"}}
	tenantB := jsrun.Roots{Workflow: jsrun.WorkflowInfo{ID: "wf_tenant_b", Name: "Tenant B"}}
	if _, err := pool.Run(context.Background(), jsrun.Task{Roots: tenantA, Items: items("a"), Source: `Object.prototype.polluted = 'a'
Array.prototype.map = function () { return ['a'] }
JSON.parse = function () { return 'a' }
Promise.prototype.then = function () { return 'a' }
globalThis.leftBehind = $workflow.name + ' ' + $env.SECRET_OF_A
Buffer.prototype.toString = function () { return 'a' }
require('lodash').chunk = function () { return 'a' }
require('crypto').createHash = function () { return 'a' }
Settings.defaultZone = 'Asia/Tokyo'
Intl.NumberFormat.prototype.format = function () { return 'a' }
return items`}); err != nil {
		t.Fatalf("tenant A: Run() error = %v", err)
	}
	result, err := pool.Run(context.Background(), jsrun.Task{Roots: tenantB, Items: items("b"), Source: `const text = JSON.stringify([1, 2].map(n => n * 2))
return [{ json: {
  polluted: typeof ({}).polluted, parsed: JSON.parse(text).join(), global: typeof leftBehind,
  workflow: $workflow.name, env: JSON.stringify($env), buffer: Buffer.from('b').toString('hex'),
  chunk: require('lodash').chunk([1, 2, 3], 2).length, hash: require('crypto').createHash('sha1').update('').digest('hex').slice(0, 6),
  zone: DateTime.now().zoneName, number: new Intl.NumberFormat('en-US').format(1000), awaited: await Promise.resolve('b'),
} }]`})
	if err != nil {
		t.Fatalf("tenant B: Run() error = %v", err)
	}
	if starts := pool.started(); starts != 1 {
		t.Fatalf("%d workers started, want both jobs on one worker", starts)
	}
	got := fmt.Sprint(result.Items[0].JSON)
	if want := "map[awaited:b buffer:62 chunk:2 env:{} global:undefined hash:da39a3 number:1,000 parsed:2,4 polluted:undefined workflow:Tenant B zone:UTC]"; got != want {
		t.Fatalf("tenant B saw %s, want %s", got, want)
	}
}
