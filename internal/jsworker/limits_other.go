//go:build !linux

package jsworker

import (
	"os"
	"os/exec"
)

// Outside Linux a worker has no address-space limit, OOM preference,
// no_new_privs or parent-death signal, and the server is not made
// undumpable; a worker still exits when the server closes its stdin, and the
// pool still kills it past its deadline. Production runs on Linux.

func limitSelf(uint64) {}

func protectServer() {}

func selfExecutable() string {
	executable, err := os.Executable()
	if err != nil {
		return os.Args[0]
	}
	return executable
}

func prepareCommand(*exec.Cmd) {}

func startCommand(cmd *exec.Cmd) error { return cmd.Start() }
