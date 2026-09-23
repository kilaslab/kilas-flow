//go:build linux

package jsworker

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

// limitSelf is run by a worker on itself, before its first job. The
// address-space limit turns an allocation no watchdog could stop into this
// worker failing to allocate, and oom_score_adj makes it the kernel's first
// choice when memory runs out anyway, so neither reaches the server. The race
// detector reserves terabytes of address space, so a race build keeps no
// address-space limit.
func limitSelf(addressSpace uint64) {
	if addressSpace > 0 && !raceBuild {
		_ = syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: addressSpace, Max: addressSpace})
	}
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0)
}

// prepareCommand makes the kernel kill a worker when the server dies, even
// when the worker is inside a built-in that never reads its stdin again.
func prepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
}

type spawn struct {
	cmd  *exec.Cmd
	done chan error
}

var (
	spawns      = make(chan spawn)
	spawnerOnce sync.Once
)

// startCommand starts every worker from one OS thread that lives as long as
// the server. The parent-death signal fires when the thread that started the
// child exits, not the process, and Go retires threads.
func startCommand(cmd *exec.Cmd) error {
	spawnerOnce.Do(func() {
		go func() {
			// Never unlocked, and the loop never ends, so the thread is never
			// retired.
			runtime.LockOSThread()
			for request := range spawns {
				request.done <- request.cmd.Start()
			}
		}()
	})
	done := make(chan error, 1)
	spawns <- spawn{cmd: cmd, done: done}
	return <-done
}
