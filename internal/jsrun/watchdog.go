package jsrun

import (
	"runtime"
	"runtime/metrics"
	"sync"
	"time"
)

// watchdog stops running scripts when the process's live heap passes a
// ceiling. goja keeps JavaScript objects on the Go heap and cannot say which
// VM owns what, so the watchdog cannot single out the script that grew: it
// stops every script running when the ceiling is crossed. Those runs fail
// with ErrMemoryLimit; the server, and everything that is not a script,
// carries on.
//
// After firing it waits for the stopped scripts to be released and collects
// once before judging the heap again. Otherwise the memory of a script it
// just stopped, not yet collected, would stop the next script to start.
type watchdog struct {
	interval time.Duration
	read     func() uint64

	mu      sync.Mutex
	entries map[*watched]struct{}
	stop    chan struct{}
	// draining counts stopped scripts not yet released, and collect records
	// that their memory has not been collected since.
	draining int
	collect  bool
}

type watched struct {
	ceiling   uint64
	interrupt func(error)
	fired     bool
}

var heapWatchdog = &watchdog{interval: 10 * time.Millisecond, read: heapObjectBytes}

// heapObjectBytes is the heap occupied by objects, live or not yet swept.
func heapObjectBytes() uint64 {
	sample := []metrics.Sample{{Name: "/memory/classes/heap/objects:bytes"}}
	metrics.Read(sample)
	if sample[0].Value.Kind() != metrics.KindUint64 {
		return 0
	}
	return sample[0].Value.Uint64()
}

// enter watches one script until the returned function is called. The
// sampler runs only while at least one script is watched.
func (w *watchdog) enter(ceiling uint64, interrupt func(error)) (leave func()) {
	entry := &watched{ceiling: ceiling, interrupt: interrupt}
	w.mu.Lock()
	if w.entries == nil {
		w.entries = map[*watched]struct{}{}
	}
	w.entries[entry] = struct{}{}
	if w.stop == nil {
		w.stop = make(chan struct{})
		go w.loop(w.stop)
	}
	w.mu.Unlock()
	return func() {
		w.mu.Lock()
		defer w.mu.Unlock()
		delete(w.entries, entry)
		if entry.fired {
			w.draining--
		}
		if len(w.entries) == 0 && w.stop != nil {
			close(w.stop)
			w.stop = nil
		}
	}
}

func (w *watchdog) loop(stop chan struct{}) {
	ticker := time.NewTicker(w.interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
		}
		if w.tick() {
			runtime.GC()
		}
	}
}

// tick samples the heap once, or reports that it is time to collect what
// stopped scripts left behind.
func (w *watchdog) tick() (collect bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	if w.draining > 0 {
		return false
	}
	if w.collect {
		w.collect = false
		return true
	}
	live := w.read()
	for entry := range w.entries {
		if entry.fired || live <= entry.ceiling {
			continue
		}
		entry.fired = true
		w.draining++
		w.collect = true
		entry.interrupt(outOfMemory(entry.ceiling))
	}
	return false
}
