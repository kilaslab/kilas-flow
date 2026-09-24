//go:build linux

package jsworker

import (
	"os"
	"os/exec"
	"runtime"
	"sync"
	"syscall"
)

const (
	prSetDumpable   = 4
	prSetNoNewPrivs = 38
)

// limitSelf is run by a worker on itself, before its first job. The
// address-space limit turns an allocation no watchdog could stop into this
// worker failing to allocate, and oom_score_adj makes it the kernel's first
// choice when memory runs out anyway, so neither reaches the server.
// no_new_privs keeps it from gaining a privilege through anything it could
// execute. The race detector reserves terabytes of address space, so a race
// build keeps no address-space limit. It then confines itself, and reports
// how.
func limitSelf(addressSpace uint64) confinement {
	if addressSpace > 0 && !raceBuild {
		_ = syscall.Setrlimit(syscall.RLIMIT_AS, &syscall.Rlimit{Cur: addressSpace, Max: addressSpace})
	}
	_ = os.WriteFile("/proc/self/oom_score_adj", []byte("1000"), 0)
	_, _, _ = syscall.RawSyscall6(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0, 0, 0, 0)
	return confineSelf()
}

var protectOnce sync.Once

// protectServer marks the server not dumpable, which puts its /proc entries
// (environ, mem, fd) out of reach of other processes running as its user,
// workers included: a worker's own environment holds nothing, and neither
// may the server's be read through /proc. It also means the server leaves no
// core dump.
func protectServer() {
	protectOnce.Do(func() {
		_, _, _ = syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0)
	})
}

// selfExecutable is the running binary, even after an upgrade replaced or
// removed the file it was started from, so a worker always speaks the
// server's protocol.
func selfExecutable() string { return "/proc/self/exe" }

// prepareCommand makes the kernel kill a worker when the server dies, even
// when the worker is inside a built-in that never reads its stdin again. A
// worker also starts a session of its own, so it is in no process group of
// the server's: a signal it sent to its own group, kill(0), reaches only
// itself.
func prepareCommand(cmd *exec.Cmd) {
	if cmd.SysProcAttr == nil {
		cmd.SysProcAttr = &syscall.SysProcAttr{}
	}
	cmd.SysProcAttr.Pdeathsig = syscall.SIGKILL
	cmd.SysProcAttr.Setsid = true
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
