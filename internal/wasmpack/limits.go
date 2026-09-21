package wasmpack

import (
	"fmt"
	"time"
)

// Limits bound one pack run.
//
// They are the pack's own, declared in its manifest and approved with it, but
// they are bounded by Ceilings: a manifest may ask for less than the ceiling
// and never more, because the operator approving a manifest has to be able to
// read what it asks for and because an unbounded pack would take the
// deployment's memory or its egress budget with it.
type Limits struct {
	// Timeout bounds the run's wall clock.
	Timeout time.Duration
	// MemoryPages bounds the module's linear memory in 64KiB WebAssembly pages.
	MemoryPages uint32
	// MaxOutputBytes bounds what the module may write to standard output.
	MaxOutputBytes int64
	// MaxHostCalls bounds how many times the module may call into the host.
	// Each call is work the host does on the pack's behalf, so this is the
	// budget the pack's capabilities are measured in.
	MaxHostCalls int
}

// DefaultLimits are what a pack gets when its manifest does not say: generous
// enough for a paginated API call, small enough that a runaway pack fails one
// node run rather than the process.
func DefaultLimits() Limits {
	return Limits{
		Timeout:        30 * time.Second,
		MemoryPages:    512, // 32 MiB
		MaxOutputBytes: 8 << 20,
		MaxHostCalls:   100,
	}
}

// Ceilings are the hard bounds no manifest may exceed.
//
// They are constants rather than configuration on purpose: raising one is a
// code change an operator can see in a release, not a YAML edit that widens
// every pack's reach at once.
func Ceilings() Limits {
	return Limits{
		Timeout:        10 * time.Minute,
		MemoryPages:    4096, // 256 MiB
		MaxOutputBytes: 64 << 20,
		MaxHostCalls:   10000,
	}
}

// Validate reports whether these limits are usable and inside the ceilings.
func (limits Limits) Validate() error {
	if limits.Timeout <= 0 {
		return fmt.Errorf("the run's time limit must be positive, got %s", limits.Timeout)
	}
	if limits.MemoryPages == 0 {
		return fmt.Errorf("the run's memory limit must be at least one page")
	}
	if limits.MaxOutputBytes <= 0 {
		return fmt.Errorf("the run's output limit must be positive, got %d", limits.MaxOutputBytes)
	}
	if limits.MaxHostCalls <= 0 {
		return fmt.Errorf("the run's host-call limit must be positive, got %d", limits.MaxHostCalls)
	}
	ceilings := Ceilings()
	switch {
	case limits.Timeout > ceilings.Timeout:
		return fmt.Errorf("the run's %s time limit is above the %s ceiling", limits.Timeout, ceilings.Timeout)
	case limits.MemoryPages > ceilings.MemoryPages:
		return fmt.Errorf("the run's %d-page memory limit is above the %d-page ceiling", limits.MemoryPages, ceilings.MemoryPages)
	case limits.MaxOutputBytes > ceilings.MaxOutputBytes:
		return fmt.Errorf("the run's %d-byte output limit is above the %d-byte ceiling", limits.MaxOutputBytes, ceilings.MaxOutputBytes)
	case limits.MaxHostCalls > ceilings.MaxHostCalls:
		return fmt.Errorf("the run's %d host-call limit is above the %d ceiling", limits.MaxHostCalls, ceilings.MaxHostCalls)
	}
	return nil
}

// withDefaults fills what a manifest left zero from DefaultLimits, so a zero
// field means "the shipped value" rather than "unbounded".
func (limits Limits) withDefaults() Limits {
	defaults := DefaultLimits()
	if limits.Timeout <= 0 {
		limits.Timeout = defaults.Timeout
	}
	if limits.MemoryPages == 0 {
		limits.MemoryPages = defaults.MemoryPages
	}
	if limits.MaxOutputBytes <= 0 {
		limits.MaxOutputBytes = defaults.MaxOutputBytes
	}
	if limits.MaxHostCalls <= 0 {
		limits.MaxHostCalls = defaults.MaxHostCalls
	}
	return limits
}
