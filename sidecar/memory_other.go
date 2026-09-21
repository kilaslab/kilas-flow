//go:build !linux && !darwin

package sidecar

import "time"

func memoryPollInterval() time.Duration { return time.Second }

// residentBytes has no portable implementation on this platform, so the
// watchdog reports nothing rather than guessing. The deployment notes say the
// container is the memory bound where this is unavailable.
func residentBytes(pid int) (uint64, bool) { return 0, false }
