package jsworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
)

// The layers that confine a worker, as the operator's log names them. The
// user and the namespaces come from how the server starts a worker; the
// others the worker installs on itself before it reads its first job.
const (
	layerOwnUser       = "own user"
	layerUserNamespace = "user namespace"
	layerPID           = "PID namespace"
	layerNetwork       = "network namespace"
	layerIPC           = "IPC namespace"
	layerLandlock      = "landlock"
	layerUndumpable    = "undumpable"
)

// confinement is what keeps a worker from what the server can reach: the
// layers in place, and those missing, each with why when that is known.
type confinement struct {
	Active  []string `json:"active,omitempty"`
	Missing []string `json:"missing,omitempty"`
}

// spawnProfile is one way to start a worker: the attributes the server asks
// the kernel for, and the layers they give the worker once granted.
type spawnProfile struct {
	// asks names what the kernel is asked for, for the log when it refuses.
	asks  string
	apply func(*exec.Cmd)
	gives confinement
	// why says why the layers it does not give are missing, when that is
	// known before any worker starts.
	why string
	// stepsDown says whether a kernel that refuses this profile is asked for
	// the next one instead. A user the operator configured is never given up
	// for the server's own.
	stepsDown bool
}

// maxConfinementReport bounds a ready frame. A worker describes a handful
// of layers.
const maxConfinementReport = 16 << 10

// profile is the strongest profile not yet refused, and its place in the
// list.
func (pool *Pool) profile() (spawnProfile, int) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	return pool.profiles[pool.profileAt], pool.profileAt
}

// stepDown gives up the profile at index for the next one when the kernel
// refused to start a process with it, and reports whether there is one to
// try. It is settled once: every later worker starts with what was granted.
func (pool *Pool) stepDown(index int, err error) (spawnProfile, int, bool) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	refused := pool.profiles[index]
	if !refused.stepsDown || index+1 >= len(pool.profiles) || !refusedByKernel(err) {
		return spawnProfile{}, 0, false
	}
	if pool.profileAt == index {
		pool.profileAt = index + 1
		reason := err.Error()
		var errno syscall.Errno
		if errors.As(err, &errno) {
			reason = errno.Error()
		}
		pool.refusal = fmt.Sprintf("the kernel refused %s: %s", refused.asks, reason)
	}
	return pool.profiles[pool.profileAt], pool.profileAt, true
}

// refusedByKernel tells a kernel that will not grant what a profile asks for,
// such as a user namespace in a container whose seccomp profile forbids one,
// from a start that failed for any other reason.
func refusedByKernel(err error) bool {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.EACCES, syscall.EINVAL, syscall.ENOSPC, syscall.ENOSYS, syscall.EUSERS} {
		if errors.Is(err, errno) {
			return true
		}
	}
	return false
}

// logConfinement says how the workers are confined: once, and again only if
// that changes, as when the kernel stops granting a namespace. The worker's
// own report is what it says it installed; it is logged, never trusted.
func (pool *Pool) logConfinement(profile spawnProfile, reported *confinement) {
	var worker confinement
	if reported != nil {
		worker = *reported
	}
	active := append(append([]string{}, profile.gives.Active...), worker.Active...)
	missing := append(append([]string{}, profile.gives.Missing...), worker.Missing...)
	pool.confineMu.Lock()
	pool.workerConfinement = worker
	why := profile.why
	if pool.refusal != "" {
		why = pool.refusal
	}
	summary := strings.Join(active, ", ") + "|" + strings.Join(missing, "; ") + "|" + why
	if summary == pool.confinementLogged {
		pool.confineMu.Unlock()
		return
	}
	pool.confinementLogged = summary
	pool.confineMu.Unlock()

	level, message := slog.LevelInfo, "JavaScript workers are confined"
	switch {
	case len(active) == 0:
		level, message = slog.LevelWarn, "JavaScript workers are not confined"
	case len(missing) > 0:
		level, message = slog.LevelWarn, "JavaScript workers are only partly confined"
	}
	attrs := []any{"active", strings.Join(active, ", ")}
	if len(missing) > 0 {
		attrs = append(attrs, "missing", strings.Join(missing, "; "))
	}
	if why != "" {
		attrs = append(attrs, "why", why)
	}
	pool.logger.Log(context.Background(), level, message, attrs...)
}
