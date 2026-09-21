package sidecar

import (
	"fmt"
	"sync"
	"time"
)

// The RSS watchdog exists because --max-old-space-size bounds only V8's old
// space: Buffer, ArrayBuffer and external memory are allocated outside it, so
// a heap-capped process can still grow to gigabytes of RSS and take the host
// with it. The watchdog is the only host-side memory boundary until the
// container itself.
//
// Polling is deliberately coarse. A tighter interval costs more than it
// buys: the run's own deadline and the container limit both bound the damage
// a fast allocator can do between two samples.

// startMemoryWatchdog polls the child's resident set while it lives and calls
// onExceed once when it passes maxRSSMB. Zero or negative disables it. The
// returned func stops the poller; it is safe to call more than once and safe
// to call from the goroutine that started it.
func startMemoryWatchdog(pid, maxRSSMB int, onExceed func(reason string)) func() {
	if maxRSSMB <= 0 || pid <= 0 {
		return func() {}
	}
	interval := memoryPollInterval()
	limit := uint64(maxRSSMB) << 20
	stop := make(chan struct{})
	var once sync.Once
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				return
			case <-ticker.C:
				rss, ok := residentBytes(pid)
				if !ok {
					continue
				}
				if rss > limit {
					onExceed(fmt.Sprintf("resident memory %d MB exceeded the limit of %d MB", rss>>20, maxRSSMB))
					return
				}
			}
		}
	}()
	return func() { once.Do(func() { close(stop) }) }
}
