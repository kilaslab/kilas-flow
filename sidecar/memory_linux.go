//go:build linux

package sidecar

import (
	"os"
	"strconv"
	"strings"
	"time"
)

func memoryPollInterval() time.Duration { return 200 * time.Millisecond }

// residentBytes reads the process's resident set from /proc/<pid>/statm. The
// second field is resident pages.
func residentBytes(pid int) (uint64, bool) {
	raw, err := os.ReadFile("/proc/" + strconv.Itoa(pid) + "/statm")
	if err != nil {
		return 0, false
	}
	fields := strings.Fields(string(raw))
	if len(fields) < 2 {
		return 0, false
	}
	pages, err := strconv.ParseUint(fields[1], 10, 64)
	if err != nil {
		return 0, false
	}
	return pages * uint64(os.Getpagesize()), true
}
