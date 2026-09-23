//go:build race

package jsrun_test

// The race detector slows every instruction the VM runs, so the latency a
// test tolerates between an interrupt and the run returning is wider. The
// limits themselves are never widened: tests run under the shipped defaults.
const interruptTolerance = 250_000_000 // 250ms
