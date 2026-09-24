//go:build !linux

package jsworker

// Outside Linux a worker is not confined, so there is nothing to probe.
func probeWorker(string) int { return 2 }
