//go:build !race

package jsrun_test

// interruptTolerance is how long a run may take to return once its interrupt
// was delivered.
const interruptTolerance = 50_000_000 // 50ms
