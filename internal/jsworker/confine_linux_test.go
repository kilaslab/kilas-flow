//go:build linux

package jsworker

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"github.com/kilaslab/kilas-flow/internal/jsrun"
)

// What a probe is told to reach for, through its environment.
const (
	probeFileVariable    = "JSWORKER_PROBE_FILE"
	probeAddressVariable = "JSWORKER_PROBE_ADDRESS"
	probeServerVariable  = "JSWORKER_PROBE_SERVER"
	probeSleeperVariable = "JSWORKER_PROBE_SLEEPER"
	probeGroupVariable   = "JSWORKER_PROBE_GROUP"
)

// probeWorker starts as a worker does, confined as far as the platform
// allows, and then, for its one job, tries what a script that escaped the
// engine would: the server's files, the network, and other processes. Its
// one result item says how each attempt ended. In probe-per-thread mode it
// confines itself thread by thread, as it must on a kernel older than
// landlock's one-call form.
func probeWorker(mode string) int {
	if mode == "probe-per-thread" {
		landlockTSYNC = false
	}
	reader, writer := bufio.NewReader(os.Stdin), bufio.NewWriter(os.Stdout)
	if !greet(reader, writer) {
		return 2
	}
	run, _, err := readFrame(reader, maxServerHeader, maxServerBlob)
	if err != nil {
		return 2
	}
	output, err := json.Marshal([]map[string]any{{"json": probe(), "binary": map[string]any{}, "paired": nil}})
	if err != nil {
		return 2
	}
	done := message{Type: typeDone, Nonce: run.Nonce, Executed: &jsrun.Executed{}, OutputSizes: []int{len(output)}}
	if err := writeFrame(writer, done, string(output)); err != nil {
		return 2
	}
	_, _ = io.Copy(io.Discard, reader)
	return 0
}

// probe makes every attempt, in a worker, and says how each ended: "ok",
// or the error.
func probe() map[string]string {
	file := os.Getenv(probeFileVariable)
	server, _ := strconv.Atoi(os.Getenv(probeServerVariable))
	sleeper, _ := strconv.Atoi(os.Getenv(probeSleeperVariable))
	group, _ := strconv.Atoi(os.Getenv(probeGroupVariable))
	outcome := func(err error) string {
		if err != nil {
			var errno syscall.Errno
			if errors.As(err, &errno) {
				return errno.Error()
			}
			return err.Error()
		}
		return "ok"
	}
	socket := func(domain int) string {
		fd, err := syscall.Socket(domain, syscall.SOCK_STREAM, 0)
		if err == nil {
			syscall.Close(fd)
		}
		return outcome(err)
	}
	results := map[string]string{
		"uid": strconv.Itoa(os.Getuid()),
		"gid": strconv.Itoa(os.Getgid()),
	}
	results["userns"], _ = os.Readlink("/proc/self/ns/user")
	results["netns"], _ = os.Readlink("/proc/self/ns/net")
	_, err := os.ReadFile(file)
	results["read"] = outcome(err)
	results["readOnEveryThread"] = readOnManyThreads(file, outcome)
	_, err = os.ReadDir(filepath.Dir(file))
	results["list"] = outcome(err)
	results["create"] = outcome(os.WriteFile(filepath.Join(filepath.Dir(file), "planted"), []byte("x"), 0o600))
	results["remove"] = outcome(os.Remove(file))
	results["socket"] = socket(syscall.AF_INET)
	results["unixSocket"] = socket(syscall.AF_UNIX)
	connection, err := net.Dial("tcp", os.Getenv(probeAddressVariable))
	if err == nil {
		connection.Close()
	}
	results["dial"] = outcome(err)
	results["ptrace"] = ptraceOutcome(sleeper, outcome)
	// In a PID namespace of its own the only numbers that exist are the
	// worker's and its threads', and a container's small numbers can be one
	// of them: reaching that is reaching itself.
	itself := func(pid int, result string) string {
		if result == "ok" && syscall.Tgkill(os.Getpid(), pid, 0) == nil {
			return "itself"
		}
		return result
	}
	results["signalServer"] = itself(server, outcome(syscall.Kill(server, 0)))
	results["signalSleeper"] = itself(sleeper, outcome(syscall.Kill(sleeper, 0)))
	results["signalServerGroup"] = outcome(syscall.Kill(-group, 0))
	// Only reads the server's limit: setting one, such as RLIMIT_CPU to
	// zero, is how a worker could kill the server.
	var limit syscall.Rlimit
	_, _, errno := syscall.RawSyscall6(syscall.SYS_PRLIMIT64, uintptr(server), syscall.RLIMIT_NOFILE, 0, uintptr(unsafe.Pointer(&limit)), 0, 0)
	if errno != 0 {
		results["prlimitServer"] = errno.Error()
	} else {
		results["prlimitServer"] = itself(server, "ok")
	}
	return results
}

// readOnManyThreads reads the file from goroutines each locked to an OS
// thread of its own, so a thread the confinement missed would be found.
// It says "ok" if any read succeeded.
func readOnManyThreads(file string, outcome func(error) string) string {
	results := make(chan string, 16)
	release := make(chan struct{})
	for range cap(results) {
		go func() {
			runtime.LockOSThread()
			// Every goroutine holds its thread until all have read, so each
			// read is on a thread of its own.
			defer func() { <-release; runtime.UnlockOSThread() }()
			_, err := os.ReadFile(file)
			results <- outcome(err)
		}()
	}
	worst := ""
	for range cap(results) {
		if result := <-results; worst == "" || result == "ok" {
			worst = result
		}
	}
	close(release)
	return worst
}

// ptraceOutcome attaches to a process and lets it go again at once. ptrace
// requests must come from the thread that attached.
func ptraceOutcome(pid int, outcome func(error) string) string {
	runtime.LockOSThread()
	defer runtime.UnlockOSThread()
	if err := syscall.PtraceAttach(pid); err != nil {
		return outcome(err)
	}
	var status syscall.WaitStatus
	_, _ = syscall.Wait4(pid, &status, 0, nil)
	_ = syscall.PtraceDetach(pid)
	return "ok"
}

// A worker runs code written by workflow authors, so on Linux it is kept
// from what the server can reach: code that escaped the engine still cannot
// read or change the server's data, reach the network, or signal, trace or
// limit the server. Each layer is checked where it is in place;
// JSWORKER_EXPECT_CONFINED=1 says every one must be, as it is in a container
// that allows user namespaces on a kernel with landlock.
func TestAConfinedWorkerCannotReachTheServersFilesNetworkOrProcesses(t *testing.T) {
	t.Run("one call", func(t *testing.T) { testConfinement(t, "probe") })
	t.Run("thread by thread", func(t *testing.T) {
		if raceBuild {
			t.Skip("a build with cgo cannot restrict its threads one by one")
		}
		testConfinement(t, "probe-per-thread")
	})
}

func testConfinement(t *testing.T, mode string) {
	data := t.TempDir()
	secret := filepath.Join(data, "kilasflow.db")
	if err := os.WriteFile(secret, []byte("the server's data"), 0o600); err != nil {
		t.Fatal(err)
	}
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	sleeper := exec.Command(os.Args[0])
	sleeper.Env = []string{markerVariable + "=1", testModeVariable + "=sleep"}
	if err := sleeper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = sleeper.Process.Kill()
		_ = sleeper.Wait()
	})

	pool := newTestPool(t, Options{})
	pool.mode.Store(mode)
	pool.env = []string{
		probeFileVariable + "=" + secret,
		probeAddressVariable + "=" + listener.Addr().String(),
		probeServerVariable + "=" + strconv.Itoa(os.Getpid()),
		probeSleeperVariable + "=" + strconv.Itoa(sleeper.Process.Pid),
		probeGroupVariable + "=" + strconv.Itoa(syscall.Getpgrp()),
	}
	result, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	got := map[string]string{}
	for key, value := range result.Items[0].JSON {
		got[key], _ = value.(string)
	}
	profile, _ := pool.profile()
	pool.confineMu.Lock()
	worker, refusal := pool.workerConfinement, pool.refusal
	pool.confineMu.Unlock()
	namespaced := slices.Contains(profile.gives.Active, layerPID)
	landlocked := slices.ContainsFunc(worker.Active, func(layer string) bool { return strings.HasPrefix(layer, layerLandlock) })
	t.Logf("probe: %v", got)
	t.Logf("confinement: %v %v; worker %+v; %s", profile.gives.Active, profile.gives.Missing, worker, refusal)
	if os.Getenv("JSWORKER_EXPECT_CONFINED") == "1" && (!namespaced || !landlocked) {
		t.Fatalf("the worker is not wholly confined: namespaces %v, landlock %v", namespaced, landlocked)
	}
	refused := func(attempts ...string) {
		t.Helper()
		for _, attempt := range attempts {
			if got[attempt] == "ok" || got[attempt] == "" {
				// "itself" is a number that named the worker's own thread.
				t.Errorf("%s: %q, want it refused", attempt, got[attempt])
			}
		}
	}
	if landlocked {
		refused("read", "readOnEveryThread", "list", "create", "remove", "ptrace")
		if contents, err := os.ReadFile(secret); err != nil || string(contents) != "the server's data" {
			t.Errorf("the server's file after the probe: %q, %v", contents, err)
		}
		if _, err := os.Stat(filepath.Join(data, "planted")); !errors.Is(err, os.ErrNotExist) {
			t.Errorf("the worker planted a file in the server's data directory: %v", err)
		}
	}
	if !namespaced {
		return
	}
	// In its own namespaces the worker has no network but a loopback that is
	// down, and no number for the server or any process outside; a number
	// that names one of its own threads reaches only itself.
	refused("dial", "ptrace", "signalServerGroup", "signalSleeper", "signalServer", "prlimitServer")
	ownUser, _ := os.Readlink("/proc/self/ns/user")
	ownNetwork, _ := os.Readlink("/proc/self/ns/net")
	if got["netns"] == ownNetwork || (slices.Contains(profile.gives.Active, layerUserNamespace) && got["userns"] == ownUser) {
		t.Errorf("the worker shares the server's namespaces: user %s, network %s", got["userns"], got["netns"])
	}
	if slices.Contains(profile.gives.Active, layerUserNamespace) {
		// Unmapped in its namespace, the worker is the overflow user.
		overflow, _ := os.ReadFile("/proc/sys/kernel/overflowuid")
		if want := strings.TrimSpace(string(overflow)); got["uid"] != want || got["uid"] == strconv.Itoa(os.Getuid()) {
			t.Errorf("the worker runs as uid %s in its namespace, want the overflow user %s", got["uid"], want)
		}
	}
}

// A kernel that refuses what the strongest profile asks for, as a container
// whose seccomp profile forbids user namespaces does, costs the workers that
// layer and nothing else: the pool asks for the next profile, keeps to it,
// and says once why.
func TestAWorkerStillStartsWhereTheKernelRefusesItsNamespaces(t *testing.T) {
	var logs lockedBuffer
	pool := newTestPool(t, Options{MaxRuns: 1, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	plain := pool.profiles[len(pool.profiles)-1]
	refused := spawnProfile{
		asks: "a thread for a process",
		// A new thread group must share its parent's signal handlers, so
		// the kernel answers EINVAL, as it answers EPERM for a namespace it
		// will not grant.
		apply:     func(cmd *exec.Cmd) { cmd.SysProcAttr.Cloneflags |= syscall.CLONE_THREAD },
		gives:     confinement{Active: []string{layerNetwork}},
		stepsDown: true,
	}
	pool.profiles = []spawnProfile{refused, plain}
	for range 2 {
		if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")}); err != nil {
			t.Fatalf("Run() error = %v", err)
		}
	}
	if built := pool.built.Load(); built != 3 {
		t.Errorf("%d commands built for two workers, want the refused profile tried once", built)
	}
	lines := confinementLines(logs.String())
	if len(lines) != 1 {
		t.Fatalf("confinement logged %d times, want once:\n%s", len(lines), logs.String())
	}
	if why := logAttr(lines[0], "why"); why != "the kernel refused a thread for a process: invalid argument" {
		t.Errorf("why = %q in %s", why, lines[0])
	}
	if !strings.Contains(lines[0], "level=WARN") || !strings.Contains(logAttr(lines[0], "missing"), layerNetwork) {
		t.Errorf("log = %s, want a warning naming the missing namespace", lines[0])
	}
}

// A user the operator configured is never given up for the server's own:
// when the kernel will not start a worker as that user, the run fails.
func TestAConfiguredUserIsNeverGivenUp(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("root may start a worker as any user")
	}
	pool := newTestPool(t, Options{UID: os.Getuid() + 1, GID: os.Getgid() + 1})
	// The server says so when it starts, not at the first run.
	if err := pool.Start(); err == nil || !strings.Contains(err.Error(), "CAP_SETUID") || !strings.Contains(err.Error(), "executable by") {
		t.Fatalf("Start() = %v, want it to say what the server needs", err)
	}
	_, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")})
	if !errors.Is(err, jsrun.ErrEngineFault) || !strings.Contains(err.Error(), "could not start") {
		t.Fatalf("Run() error = %v, want the worker refused", err)
	}
}

// A server that may change users starts every worker as the one configured,
// with none of its own groups, and in namespaces of its own.
func TestAConfiguredUserRunsTheWorkers(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("only root may start a worker as another user here")
	}
	// The configured user must be able to run the binary, which the test
	// binary's own directory does not allow.
	shared, err := os.MkdirTemp("", "jsworker-shared-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(shared) })
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	copied := filepath.Join(shared, "jsworker.test")
	if err := os.WriteFile(copied, binary, 0o755); err != nil || os.Chmod(shared, 0o755) != nil {
		t.Fatalf("copying the test binary: %v", err)
	}

	pool := newTestPool(t, Options{UID: 4242, GID: 4343})
	pool.mode.Store("probe")
	pool.binary = copied
	result, err := pool.Run(context.Background(), jsrun.Task{Source: "return items", Items: items("a")})
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if got := result.Items[0].JSON; got["uid"] != "4242" || got["gid"] != "4343" {
		t.Fatalf("the worker ran as %v:%v, want 4242:4343", got["uid"], got["gid"])
	}
	profile, _ := pool.profile()
	t.Logf("confinement: %v; %s", profile.gives.Active, pool.refusal)
}

// A start that fails whatever the profile, such as a binary the worker may
// not run, says nothing about what the kernel grants: the stronger profile is
// kept, and is what the next worker starts with once the binary is fixed.
func TestAStartThatFailsWithEveryProfileKeepsTheStrongest(t *testing.T) {
	var logs lockedBuffer
	pool := newTestPool(t, Options{Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	strongest := spawnProfile{
		asks:      "nothing more",
		apply:     func(*exec.Cmd) {},
		gives:     confinement{Active: []string{layerNetwork}},
		stepsDown: true,
	}
	pool.profiles = []spawnProfile{strongest, pool.profiles[len(pool.profiles)-1]}
	shared := t.TempDir()
	binary, err := os.ReadFile(os.Args[0])
	if err != nil {
		t.Fatal(err)
	}
	pool.binary = filepath.Join(shared, "jsworker.test")
	if err := os.WriteFile(pool.binary, binary, 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); !errors.Is(err, jsrun.ErrEngineFault) {
		t.Fatalf("Run() with a binary nobody may run = %v, want it to fail", err)
	}
	if err := os.Chmod(pool.binary, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatalf("Run() once the binary may run = %v", err)
	}
	if _, index := pool.profile(); index != 0 {
		t.Errorf("the pool starts workers with profile %d, want the strongest kept", index)
	}
	if lines := confinementLines(logs.String()); len(lines) != 1 || !strings.Contains(lines[0], "level=INFO") {
		t.Errorf("confinement logged as %q, want once, fully confined", lines)
	}
}

// A refusal can pass, as when the kernel's count of user namespaces was
// exhausted for a while: the pool asks for the stronger profile again after
// a while, and says so when it is granted.
func TestARefusedProfileIsAskedForAgainLater(t *testing.T) {
	var logs lockedBuffer
	pool := newTestPool(t, Options{MaxRuns: 1, Logger: slog.New(slog.NewTextHandler(&logs, nil))})
	var refuse atomic.Bool
	refuse.Store(true)
	strongest := spawnProfile{
		asks: "a thread for a process",
		apply: func(cmd *exec.Cmd) {
			if refuse.Load() {
				cmd.SysProcAttr.Cloneflags |= syscall.CLONE_THREAD
			}
		},
		gives:     confinement{Active: []string{layerNetwork}},
		stepsDown: true,
	}
	pool.profiles = []spawnProfile{strongest, pool.profiles[len(pool.profiles)-1]}
	pool.reprobeAfter = time.Millisecond
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatalf("Run() while refused = %v", err)
	}
	if _, index := pool.profile(); index != 1 {
		t.Fatalf("profile %d after a refusal, want the next", index)
	}
	refuse.Store(false)
	time.Sleep(5 * time.Millisecond)
	if _, err := pool.Run(context.Background(), jsrun.Task{Source: "return items"}); err != nil {
		t.Fatalf("Run() once granted = %v", err)
	}
	if _, index := pool.profile(); index != 0 {
		t.Fatalf("profile %d once the kernel grants the strongest again, want 0", index)
	}
	lines := confinementLines(logs.String())
	if len(lines) != 2 || !strings.Contains(lines[0], "level=WARN") || !strings.Contains(lines[1], "level=INFO") || strings.Contains(lines[1], "why=") {
		t.Fatalf("confinement logged as %q, want the refusal, then the recovery", lines)
	}
}
