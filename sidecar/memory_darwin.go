//go:build darwin

package sidecar

import (
	"os/exec"
	"strconv"
	"strings"
	"time"
)

func memoryPollInterval() time.Duration { return 250 * time.Millisecond }

// residentBytes reads the process's resident set from ps, which reports
// kilobytes on macOS.
func residentBytes(pid int) (uint64, bool) {
	out, err := exec.Command("ps", "-o", "rss=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return 0, false
	}
	value := strings.TrimSpace(string(out))
	if value == "" {
		return 0, false
	}
	kilobytes, err := strconv.ParseUint(value, 10, 64)
	if err != nil {
		return 0, false
	}
	return kilobytes << 10, true
}
