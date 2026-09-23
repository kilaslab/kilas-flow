//go:build !linux

package jsworker

import "os/exec"

// Outside Linux a worker has no address-space limit, OOM preference or
// parent-death signal; it still exits when the server closes its stdin, and
// the pool still kills it past its deadline. Production runs on Linux.

func limitSelf(uint64) {}

func prepareCommand(*exec.Cmd) {}

func startCommand(cmd *exec.Cmd) error { return cmd.Start() }
