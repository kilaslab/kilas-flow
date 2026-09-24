//go:build !linux

package jsworker

import (
	"os"
	"os/exec"
)

// Outside Linux a worker has no address-space limit, OOM preference,
// no_new_privs or parent-death signal, and the server is not made
// undumpable; a worker still exits when the server closes its stdin, and the
// pool still kills it past its deadline. Nor is a worker confined: it runs
// as the server's user, with its files and network. Production runs on
// Linux.

// workerUsersSupported says whether a worker may run as a user of its own.
const workerUsersSupported = false

func limitSelf(uint64) confinement { return confinement{} }

// spawnProfiles has one way to start a worker outside Linux: as the server
// does, with nothing taken away.
func spawnProfiles(int, int) []spawnProfile {
	return []spawnProfile{{
		asks:  "nothing",
		apply: func(*exec.Cmd) {},
		gives: confinement{Missing: []string{layerOwnUser, layerPID, layerNetwork, layerIPC, layerLandlock, layerUndumpable}},
		why:   "workers are confined on Linux only",
	}}
}

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
