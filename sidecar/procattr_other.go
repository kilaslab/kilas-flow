//go:build !unix

package sidecar

import (
	"os"
	"syscall"
)

// sidecarPlatformUnix is false where the process boundary is not
// implemented: SpawnFunc returns no-sidecar naming the platform. The unix-only
// machinery (process groups, wait-status signals) does not compile here.
const sidecarPlatformUnix = false

func processAttr() *syscall.SysProcAttr { return nil }

func killGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	process, err := os.FindProcess(pid)
	if err != nil {
		return err
	}
	return process.Kill()
}

func classifyExit(state *os.ProcessState, heapMB int) (string, string) {
	return CodeSidecarCrash, "sidecar process exited"
}
