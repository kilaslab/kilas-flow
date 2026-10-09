//go:build !unix

package main

import (
	"os/exec"
	"time"
)

// stopWithGroup bounds how long Wait lingers for a killed command's output. A
// platform without process groups only kills the command itself.
func stopWithGroup(command *exec.Cmd, waitDelay time.Duration) {
	command.WaitDelay = waitDelay
}
