package jsworker

import (
	"context"
	"fmt"
	"runtime"
	"slices"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BenchmarkCodeNodeUnderLoad is the Code node's load test (FEAT-vjjs8t): many
// executions at once through the real worker pool, as the server runs them,
// for a trivial body and for a transform over 1000 items. Each run is timed
// from the moment the execution asks for it to the moment it has its items:
// preparing the job, waiting for a worker, the worker's run and decoding the
// result. It reports the 50th, 95th and 99th percentile of that latency and
// the executions finished per second.
//
// The pool is sized as the server sizes it by default, one worker per CPU,
// and the concurrency levels go past that, where executions queue for a
// worker. Workers are started before the clock does, as a running server's
// already are. b.N is the number of executions per level, so a percentile
// needs enough of them:
//
//	make js-load
//
// runs it with 400 each and prints the machine's load before and after,
// which the numbers should be read against.
func BenchmarkCodeNodeUnderLoad(b *testing.B) {
	cpus := runtime.GOMAXPROCS(0)
	bodies := []struct {
		name   string
		source string
		items  []workflow.Item
	}{
		{"trivial", "return items", loadItems(1)},
		{"transform1000", `return items.map(item => ({ json: {
  id: item.json.id,
  name: item.json.name.trim().toUpperCase(),
  total: Math.round(item.json.price * item.json.quantity * 100) / 100,
  tags: item.json.tags.filter(tag => tag !== 'internal'),
  day: new Date(item.json.createdAt).toISOString().slice(0, 10),
} }))`, loadItems(1000)},
	}
	for _, body := range bodies {
		for _, concurrency := range slices.Compact([]int{1, 4, cpus, 4 * cpus}) {
			b.Run(fmt.Sprintf("%s/concurrency=%d", body.name, concurrency), func(b *testing.B) {
				pool := New(Options{MaxConcurrent: cpus})
				b.Cleanup(pool.Close)
				task := jsrun.Task{Source: body.source, Items: body.items}
				// Every worker the pool will use is started and has run
				// this body once.
				run := func() time.Duration {
					start := time.Now()
					result, err := pool.Run(context.Background(), task)
					if err != nil {
						b.Error(err)
					} else if len(result.Items) != len(body.items) {
						b.Errorf("got %d items, want %d", len(result.Items), len(body.items))
					}
					return time.Since(start)
				}
				var warm sync.WaitGroup
				for range cpus {
					warm.Go(func() { run() })
				}
				warm.Wait()

				latencies := make([]time.Duration, b.N)
				var next atomic.Int64
				var clients sync.WaitGroup
				b.ResetTimer()
				start := time.Now()
				for range concurrency {
					clients.Go(func() {
						for index := next.Add(1) - 1; index < int64(b.N); index = next.Add(1) - 1 {
							latencies[index] = run()
						}
					})
				}
				clients.Wait()
				elapsed := time.Since(start)
				b.StopTimer()

				slices.Sort(latencies)
				percentile := func(p float64) float64 {
					index := int(p*float64(len(latencies))+0.5) - 1
					index = max(0, min(index, len(latencies)-1))
					return float64(latencies[index]) / float64(time.Millisecond)
				}
				b.ReportMetric(percentile(0.50), "p50-ms")
				b.ReportMetric(percentile(0.95), "p95-ms")
				b.ReportMetric(percentile(0.99), "p99-ms")
				b.ReportMetric(float64(b.N)/elapsed.Seconds(), "execs/s")
				b.ReportMetric(0, "ns/op")
			})
		}
	}
}

// loadItems are count items shaped like the orders a real workflow reshapes.
func loadItems(count int) []workflow.Item {
	items := make([]workflow.Item, count)
	for index := range items {
		items[index] = workflow.Item{
			JSON: map[string]any{
				"id":        fmt.Sprintf("order-%05d", index),
				"name":      fmt.Sprintf("  customer %d  ", index),
				"price":     float64(index%97) + 0.99,
				"quantity":  float64(1 + index%5),
				"tags":      []any{"web", "internal", fmt.Sprintf("batch-%d", index%10)},
				"createdAt": time.Date(2026, 9, 1+index%28, 12, 0, 0, 0, time.UTC).Format(time.RFC3339),
			},
			Paired: &workflow.PairedItem{SourceNodeID: "source", ItemIndex: index},
		}
	}
	return items
}
