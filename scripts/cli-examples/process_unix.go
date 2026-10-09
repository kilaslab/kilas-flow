//go:build unix

package main

import (
	"errors"
	"os/exec"
	"syscall"
	"time"
)

// stopWithGroup makes a command that is cancelled stop for good.
//
// A command line is a tree: `sh -c 'a | b'` forks both stages. exec.CommandContext
// kills only the shell, and the stages go on holding the output pipe, so Wait
// does not return until they end by themselves. A stuck example then held the
// whole run long past its timeout. The command is started in its own process
// group and the whole group is killed instead; WaitDelay is the backstop for a
// child that left the group (a daemon, a setsid), after which the runner stops
// waiting for its output.
//
// Its own group also means a terminal's Ctrl-C reaches the runner alone, which
// then ends the commands in flight through its context.
func stopWithGroup(command *exec.Cmd, waitDelay time.Duration) {
	command.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	command.Cancel = func() error {
		// A negative pid signals the group the command leads.
		if err := syscall.Kill(-command.Process.Pid, syscall.SIGKILL); err != nil && !errors.Is(err, syscall.ESRCH) {
			return err
		}

		return nil
	}
	command.WaitDelay = waitDelay
}
