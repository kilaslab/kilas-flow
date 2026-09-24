package jsworker

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"strings"
	"syscall"
	"time"
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

// profile is the profile workers start with now, and its place in the list:
// the strongest the kernel has not refused.
func (pool *Pool) profile() (spawnProfile, int) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	return pool.profiles[pool.profileAt], pool.profileAt
}

// nextProfile is the profile the next worker is started with: the one
// workers start with now, or, once reprobeAfter has passed since the kernel
// refused a stronger one, the strongest again, since a refusal can pass.
// One start in each interval asks.
func (pool *Pool) nextProfile() (spawnProfile, int) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	index := pool.profileAt
	if index > 0 && time.Since(pool.refusedAt) >= pool.reprobeAfter {
		pool.refusedAt = time.Now()
		index = 0
	}
	return pool.profiles[index], index
}

// fallback is the profile after the one at index, when the kernel refused to
// start a process with that one and it may be given up for the next. It
// settles nothing: a start that fails whatever the profile, such as a binary
// the worker may not run, says nothing about what the kernel grants.
func (pool *Pool) fallback(index int, err error) (spawnProfile, int, bool) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	if !pool.profiles[index].stepsDown || index+1 >= len(pool.profiles) || !refusedByKernel(err) {
		return spawnProfile{}, 0, false
	}
	return pool.profiles[index+1], index + 1, true
}

// settle keeps to the profile at index once a worker has started with it:
// what the kernel refused on the way there, refused, is why the stronger
// ones are missing. A worker started with a stronger profile than before,
// after a refusal passed, takes the pool back up.
func (pool *Pool) settle(index int, refused error) {
	pool.confineMu.Lock()
	defer pool.confineMu.Unlock()
	if refused == nil {
		if index < pool.profileAt {
			pool.profileAt, pool.refusal = index, ""
		}
		return
	}
	reason := refused.Error()
	var errno syscall.Errno
	if errors.As(refused, &errno) {
		reason = errno.Error()
	}
	pool.profileAt = index
	pool.refusal = fmt.Sprintf("the kernel refused %s: %s", pool.profiles[index-1].asks, reason)
	pool.refusedAt = time.Now()
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
