package jsrun

import (
	"sync"
	"time"
)

// clock is the user's time budget. It runs only while the user's code can
// run and is paused in between, so the items of "Run once for each item" mode
// share one budget without being charged for the Go work between them, and
// code idle while the server answers its helpers is not charged for the wait.
type clock struct {
	mu      sync.Mutex
	budget  time.Duration
	used    time.Duration
	running bool
	armedAt time.Time
	timer   *time.Timer
	expire  func()
}

func newClock(budget time.Duration, expire func()) *clock {
	return &clock{budget: budget, expire: expire}
}

// start resumes the clock. The timer fires when the rest of the budget is
// gone, which interrupts the script. A clock already running runs on.
func (c *clock) start() {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.running {
		return
	}
	c.running = true
	remaining := c.budget - c.used
	c.armedAt = time.Now()
	if remaining <= 0 {
		go c.expire()
		return
	}
	c.timer = time.AfterFunc(remaining, c.expire)
}

// stop pauses the clock and reports whether the budget is spent. A timer that
// fired between the code finishing and this call counts as spent, so the
// outcome does not depend on which of the two got there first. Stopping a
// paused clock only reports.
func (c *clock) stop() (exhausted bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.running {
		return c.used >= c.budget
	}
	c.running = false
	fired := false
	if c.timer != nil {
		fired = !c.timer.Stop()
		c.timer = nil
	}
	c.used += time.Since(c.armedAt)
	return fired || c.used >= c.budget
}

func (c *clock) spent() time.Duration {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.used
}
