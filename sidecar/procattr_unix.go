//go:build unix

package sidecar

import (
	"fmt"
	"os"
	"syscall"
)

// sidecarPlatformUnix marks platforms where the process boundary is
// implemented. The unix build tags are what make the process group and the
// signal diagnosis compile elsewhere without pretending they work.
const sidecarPlatformUnix = true

// processAttr puts every child in its own process group, so killing one
// tenant's sidecar kills the whole tree it spawned and never touches the host
// or a sibling tenant's process.
func processAttr() *syscall.SysProcAttr {
	return &syscall.SysProcAttr{Setpgid: true}
}

// killGroup SIGKILLs the child's entire process group.
func killGroup(pid int) error {
	if pid <= 0 {
		return nil
	}
	return syscall.Kill(-pid, syscall.SIGKILL)
}

// classifyExit turns a terminated process's wait status into a named code and
// a diagnostic. SIGABRT is how V8 reports a heap OOM; SIGKILL was not sent by
// the host, so the only remaining sender is the OS or the container.
func classifyExit(state *os.ProcessState, heapMB int) (string, string) {
	status, ok := state.Sys().(syscall.WaitStatus)
	if !ok {
		return CodeSidecarCrash, "sidecar process exited without a wait status"
	}
	if status.Signaled() {
		switch status.Signal() {
		case syscall.SIGABRT:
			return CodeMemoryLimit, fmt.Sprintf("sidecar process aborted, most likely the JavaScript heap ceiling of %d MB", heapMB)
		case syscall.SIGKILL:
			return CodeSidecarCrash, "sidecar process was killed by the operating system, likely the container memory limit"
		default:
			return CodeSidecarCrash, "sidecar process was killed by signal " + status.Signal().String()
		}
	}
	return CodeSidecarCrash, fmt.Sprintf("sidecar process exited with status %d", status.ExitStatus())
}
