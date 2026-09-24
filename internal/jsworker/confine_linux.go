//go:build linux

package jsworker

import (
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strings"
	"syscall"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
)

// workerUsersSupported says whether a worker may run as a user of its own.
const workerUsersSupported = true

// namespaces are what a worker is given of its own, besides its user: a PID
// namespace, in which the server and every other process has no number it
// could signal, trace or limit; a network namespace, which has only a
// loopback interface, and that down; and an IPC namespace, so no System V
// or POSIX queue is shared with anything outside.
const namespaces = syscall.CLONE_NEWPID | syscall.CLONE_NEWNET | syscall.CLONE_NEWIPC

// spawnProfiles are the ways to start a worker on Linux, strongest first.
// With a configured user, the server starts the worker as that user, in
// namespaces of its own where it may create them (CAP_SYS_ADMIN), and as
// that user alone where it may not; it never falls back to its own user.
// Without one, it asks for a user namespace, in which an unprivileged server
// may create the others, and falls back to starting the worker as it always
// has.
func spawnProfiles(uid, gid int) []spawnProfile {
	if uid > 0 && gid > 0 {
		user := fmt.Sprintf("%s (uid %d, gid %d)", layerOwnUser, uid, gid)
		credential := func(cmd *exec.Cmd) {
			// No supplementary group of the server's is kept.
			cmd.SysProcAttr.Credential = &syscall.Credential{Uid: uint32(uid), Gid: uint32(gid), Groups: []uint32{}}
		}
		return []spawnProfile{{
			asks: "a worker's own PID, network and IPC namespaces",
			apply: func(cmd *exec.Cmd) {
				credential(cmd)
				cmd.SysProcAttr.Cloneflags |= namespaces
			},
			gives:     confinement{Active: []string{user, layerPID, layerNetwork, layerIPC}},
			stepsDown: true,
		}, {
			asks:  fmt.Sprintf("a worker running as uid %d, gid %d", uid, gid),
			apply: credential,
			gives: confinement{Active: []string{user}, Missing: []string{layerPID, layerNetwork, layerIPC}},
		}}
	}
	return []spawnProfile{{
		asks: "a worker's own user, PID, network and IPC namespaces",
		// No user or group is mapped into the namespace. The worker is then
		// the kernel's overflow user and group there (65534, nobody), which
		// is not root, so it keeps no capability once it runs, not even over
		// its own namespaces. Nor could the server write a mapping: it is
		// undumpable, so the new process's /proc entries are not its to
		// write until that process runs.
		apply:     func(cmd *exec.Cmd) { cmd.SysProcAttr.Cloneflags |= syscall.CLONE_NEWUSER | namespaces },
		gives:     confinement{Active: []string{layerUserNamespace, layerPID, layerNetwork, layerIPC}},
		stepsDown: true,
	}, {
		asks:  "nothing",
		apply: func(*exec.Cmd) {},
		gives: confinement{Missing: []string{layerOwnUser, layerPID, layerNetwork, layerIPC}},
	}}
}

// confineSelf takes from a worker, once its runtime is up and before its
// first job, what it will never need: its dumpability, and every file,
// TCP connection and signal outside itself, through landlock.
func confineSelf() confinement {
	var confined confinement
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetDumpable, 0, 0); errno != 0 {
		confined.Missing = append(confined.Missing, layerUndumpable+": "+errno.Error())
	} else {
		confined.Active = append(confined.Active, layerUndumpable)
	}
	// What the runtime would otherwise read from a file the first time it
	// is used: the local time zone, which `new Date()` shows.
	_ = time.Local.String()
	if covers, err := restrictFilesystem(); err != nil {
		confined.Missing = append(confined.Missing, layerLandlock+": "+err.Error())
	} else {
		confined.Active = append(confined.Active, fmt.Sprintf("%s (%s)", layerLandlock, covers))
	}
	return confined
}

// zoneSources are where Go's time package looks for the time zone database
// on Linux. A worker may read these, and nothing else, so a script's
// Intl.DateTimeFormat or luxon zone resolves as it does in the server.
func zoneSources() []string {
	sources := []string{"/usr/share/zoneinfo", "/usr/share/lib/zoneinfo", "/usr/lib/locale/TZ", "/etc/zoneinfo"}
	if zoneinfo := os.Getenv("ZONEINFO"); zoneinfo != "" {
		sources = append(sources, zoneinfo)
	}
	return sources
}

// restrictFilesystem puts the worker in a landlock domain that handles every
// filesystem right its kernel knows, and grants only reading the time zone
// database; and, where the kernel knows them, every TCP bind and connect,
// and signals and abstract Unix sockets outside the domain. Landlock also
// keeps a process in a domain from tracing one outside it. Files already
// open, the worker's pipes, are not affected. It reports what the domain
// covers.
func restrictFilesystem() (string, error) {
	abi, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, 0, 0, unix.LANDLOCK_CREATE_RULESET_VERSION)
	if errno != 0 {
		return "", fmt.Errorf("not available in this kernel (%v)", errno)
	}
	attr := unix.LandlockRulesetAttr{Access_fs: handledFileAccess(int(abi))}
	covers := []string{"files"}
	if abi >= 4 {
		attr.Access_net = unix.LANDLOCK_ACCESS_NET_BIND_TCP | unix.LANDLOCK_ACCESS_NET_CONNECT_TCP
		covers = append(covers, "TCP")
	}
	if abi >= 6 {
		attr.Scoped = unix.LANDLOCK_SCOPE_ABSTRACT_UNIX_SOCKET | unix.LANDLOCK_SCOPE_SIGNAL
		covers = append(covers, "signals")
	}
	// A kernel accepts fields it does not know, as long as they are zero.
	ruleset, _, errno := unix.Syscall(unix.SYS_LANDLOCK_CREATE_RULESET, uintptr(unsafe.Pointer(&attr)), unsafe.Sizeof(attr), 0)
	if errno != 0 {
		return "", fmt.Errorf("the ruleset was refused (%v)", errno)
	}
	defer unix.Close(int(ruleset))
	for _, source := range zoneSources() {
		if err := allowReading(int(ruleset), source); err != nil {
			return "", err
		}
	}
	if err := restrictAllThreads(int(ruleset)); err != nil {
		return "", err
	}
	return strings.Join(covers, ", "), nil
}

// handledFileAccess is every filesystem right a landlock ABI knows.
func handledFileAccess(abi int) uint64 {
	access := uint64(unix.LANDLOCK_ACCESS_FS_EXECUTE | unix.LANDLOCK_ACCESS_FS_WRITE_FILE | unix.LANDLOCK_ACCESS_FS_READ_FILE |
		unix.LANDLOCK_ACCESS_FS_READ_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_DIR | unix.LANDLOCK_ACCESS_FS_REMOVE_FILE |
		unix.LANDLOCK_ACCESS_FS_MAKE_CHAR | unix.LANDLOCK_ACCESS_FS_MAKE_DIR | unix.LANDLOCK_ACCESS_FS_MAKE_REG |
		unix.LANDLOCK_ACCESS_FS_MAKE_SOCK | unix.LANDLOCK_ACCESS_FS_MAKE_FIFO | unix.LANDLOCK_ACCESS_FS_MAKE_BLOCK |
		unix.LANDLOCK_ACCESS_FS_MAKE_SYM)
	if abi >= 2 {
		access |= unix.LANDLOCK_ACCESS_FS_REFER
	}
	if abi >= 3 {
		access |= unix.LANDLOCK_ACCESS_FS_TRUNCATE
	}
	if abi >= 5 {
		access |= unix.LANDLOCK_ACCESS_FS_IOCTL_DEV
	}
	return access
}

// allowReading grants reading what lies beneath path, when it exists.
func allowReading(ruleset int, path string) error {
	fd, err := unix.Open(path, unix.O_PATH|unix.O_CLOEXEC, 0)
	if err != nil {
		return nil
	}
	defer unix.Close(fd)
	var stat unix.Stat_t
	if err := unix.Fstat(fd, &stat); err != nil {
		return nil
	}
	access := uint64(unix.LANDLOCK_ACCESS_FS_READ_FILE)
	if stat.Mode&unix.S_IFMT == unix.S_IFDIR {
		access |= unix.LANDLOCK_ACCESS_FS_READ_DIR
	}
	rule := unix.LandlockPathBeneathAttr{Allowed_access: access, Parent_fd: int32(fd)}
	if _, _, errno := unix.Syscall6(unix.SYS_LANDLOCK_ADD_RULE, uintptr(ruleset), unix.LANDLOCK_RULE_PATH_BENEATH, uintptr(unsafe.Pointer(&rule)), 0, 0, 0); errno != 0 {
		return fmt.Errorf("reading %s could not be granted (%v)", path, errno)
	}
	return nil
}

// landlockTSYNC says whether to ask the kernel to restrict every thread in
// one call. Tests turn it off to take the path older kernels take.
var landlockTSYNC = true

// restrictAllThreads enforces the ruleset on every thread of the worker: a
// landlock domain belongs to a thread, and the Go runtime has several by
// now. A kernel that can do it in one call does; otherwise the runtime
// makes the call on each thread, which it cannot do in a build with cgo.
// Every thread needs no_new_privs first.
func restrictAllThreads(ruleset int) error {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if _, _, errno := syscall.AllThreadsSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 && errno != syscall.ENOTSUP {
		return fmt.Errorf("no_new_privs could not be set (%v)", errno)
	}
	if _, _, errno := syscall.RawSyscall(syscall.SYS_PRCTL, prSetNoNewPrivs, 1, 0); errno != 0 {
		return fmt.Errorf("no_new_privs could not be set (%v)", errno)
	}
	if landlockTSYNC {
		_, _, errno := unix.Syscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), unix.LANDLOCK_RESTRICT_SELF_TSYNC, 0)
		if errno == 0 {
			return nil
		}
		// A kernel before the flag refuses it as an invalid argument.
		if errno != syscall.EINVAL {
			return fmt.Errorf("the domain was refused (%v)", errno)
		}
	}
	if _, _, errno := syscall.AllThreadsSyscall(unix.SYS_LANDLOCK_RESTRICT_SELF, uintptr(ruleset), 0, 0); errno != 0 {
		if errno == syscall.ENOTSUP {
			return fmt.Errorf("this kernel cannot restrict every thread at once, and a build with cgo cannot restrict them one by one")
		}
		return fmt.Errorf("the domain was refused (%v)", errno)
	}
	return nil
}
