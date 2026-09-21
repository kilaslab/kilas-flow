package sidecar

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

// ProcessSpec describes how one sidecar process is started. It is the only
// place the host decides the child's argv and environment, so every posture
// the ticket promises — the permission model, the fs-read grant, the
// environment allowlist, the memory ceiling — is decided here and nowhere
// else.
type ProcessSpec struct {
	// NodePath is the operator-supplied Node binary. Empty means "node from
	// PATH", which is what a test on a developer machine wants.
	NodePath string
	// Script is the JavaScript entry point the operator installed.
	Script string
	// Args builds the child's own arguments from the tenant and the socket
	// path. The host appends them after the script.
	Args func(tenant, socketPath string) []string
	// ReadPaths are extra files the child may read under Node's permission
	// model. The script itself is always granted. Nothing else is readable.
	ReadPaths []string
	// HeapMB sets node's --max-old-space-size. It is a heap ceiling, not an
	// RSS cap; MaxRSSMB is the RSS cap.
	HeapMB int
	// MaxRSSMB is the resident-set ceiling the host watchdog enforces. Zero
	// disables the watchdog.
	MaxRSSMB int
	// Wrapper prefixes the argv, for a deployment that runs the child under a
	// sandboxing launcher. Empty runs Node directly.
	Wrapper []string
	// RuntimeDir is where the socket directory is created when the caller
	// does not supply a socket path. Empty uses the platform temp dir.
	RuntimeDir string
	// Diag receives the child's stdout and stderr. They are diagnostics only:
	// nothing the protocol depends on is ever read from them.
	Diag interface {
		Write([]byte) (int, error)
	}
}

// CheckNode runs `path --version` and requires Node 24 or newer. An empty
// path falls back to PATH. The version string is returned even when the
// version is rejected, so a boot log can say what it found.
func CheckNode(path string) (string, error) {
	if path == "" {
		found, err := exec.LookPath("node")
		if err != nil {
			return "", &CallError{Code: CodeNoSidecar, Detail: "node is not on PATH: install Node 24 LTS for the JavaScript sidecar"}
		}
		path = found
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	out, err := exec.CommandContext(ctx, path, "--version").Output()
	if err != nil {
		return "", &CallError{Code: CodeNoSidecar, Detail: "could not run " + path + " --version: " + err.Error()}
	}
	version := strings.TrimSpace(string(out))
	if majorOfVersion(version) < 24 {
		return version, &CallError{Code: CodeNoSidecar, Detail: fmt.Sprintf("sidecar needs Node 24 or newer, found %s", version)}
	}
	return version, nil
}

func majorOfVersion(version string) int {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	head, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return major
}

// NewProcessSpawn builds the default SpawnFunc: listen first, then start a
// Node process whose argv carries the permission model and the fs-read grant
// and whose environment carries nothing from the host.
func NewProcessSpawn(spec ProcessSpec) SpawnFunc {
	return func(ctx context.Context, tenant, socketPath string) (*Child, error) {
		if !sidecarPlatformUnix {
			return nil, &CallError{Code: CodeNoSidecar, Detail: "the JavaScript sidecar is unix only"}
		}
		node := spec.NodePath
		if node == "" {
			found, err := exec.LookPath("node")
			if err != nil {
				return nil, &CallError{Code: CodeNoSidecar, Detail: "node is not on PATH: install Node 24 LTS for the JavaScript sidecar"}
			}
			node = found
		}
		script, err := resolvePath(spec.Script)
		if err != nil {
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar script is not readable: " + err.Error()}
		}
		readPaths := make([]string, 0, len(spec.ReadPaths))
		for _, path := range spec.ReadPaths {
			resolved, err := resolvePath(path)
			if err != nil {
				return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar read path is not readable: " + err.Error()}
			}
			readPaths = appendUnique(readPaths, resolved)
		}
		sockDir := ""
		if socketPath == "" {
			dir, err := socketDir(spec.RuntimeDir)
			if err != nil {
				return nil, &CallError{Code: CodeSpawnFailed, Detail: "no directory for the sidecar socket: " + err.Error()}
			}
			sockDir = dir
			socketPath = filepath.Join(dir, "sidecar.sock")
		}

		// Listen before starting the child. The child dials back the moment
		// it runs, so a listener created afterwards can lose that race on a
		// loaded machine.
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			removeDir(sockDir)
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar socket failed to listen: " + err.Error()}
		}

		executable, argv := processArgv(spec, node, script, readPaths, tenant, socketPath)
		cmd := exec.Command(executable, argv...)
		cmd.SysProcAttr = processAttr()
		cmd.WaitDelay = 2 * time.Second
		cmd.Env = childEnvironment()
		if spec.Diag != nil {
			cmd.Stdout = spec.Diag
			cmd.Stderr = spec.Diag
		}
		if err := cmd.Start(); err != nil {
			listener.Close()
			removeDir(sockDir)
			return nil, &CallError{Code: CodeSpawnFailed, Detail: "sidecar process failed to start: " + err.Error()}
		}
		pid := cmd.Process.Pid
		done := make(chan struct{})
		go func() {
			_ = cmd.Wait()
			close(done)
		}()

		var (
			conn        net.Conn
			watchReason atomic.Value
			stopWatch   func()
			once        sync.Once
		)
		var killFn = func() error {
			once.Do(func() {
				if conn != nil {
					_ = conn.Close()
				}
				if stopWatch != nil {
					stopWatch()
				}
				_ = killGroup(pid)
				select {
				case <-done:
				case <-time.After(2 * time.Second):
				}
				removeDir(sockDir)
			})
			return nil
		}

		accepted := make(chan net.Conn, 1)
		acceptErr := make(chan error, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				acceptErr <- err
				return
			}
			accepted <- conn
		}()
		select {
		case conn = <-accepted:
			// Exactly one connection: a socket is a single-client endpoint
			// and a second dial is a protocol violation, not a second run.
			listener.Close()
		case err := <-acceptErr:
			listener.Close()
			_ = killFn()
			return nil, spawnFailure("sidecar socket accept failed: "+err.Error(), cmd, spec.HeapMB)
		case <-ctx.Done():
			listener.Close()
			_ = killFn()
			return nil, spawnFailure("sidecar process never dialled back", cmd, spec.HeapMB)
		}

		stopWatch = startMemoryWatchdog(pid, spec.MaxRSSMB, func(reason string) {
			watchReason.Store(reason)
			_ = killGroup(pid)
		})

		diagnose := func(wait time.Duration) (string, string) {
			if reason, ok := watchReason.Load().(string); ok && reason != "" {
				return CodeMemoryLimit, reason
			}
			select {
			case <-done:
			case <-time.After(wait):
				return CodeSidecarCrash, "sidecar process did not exit within the diagnostic window"
			}
			if cmd.ProcessState == nil {
				return CodeSidecarCrash, "sidecar process state is unavailable"
			}
			return classifyExit(cmd.ProcessState, spec.HeapMB)
		}

		return &Child{Conn: conn, Kill: killFn, PID: pid, SocketDir: sockDir, Diagnose: diagnose}, nil
	}
}

// DefaultSpawn starts `node script tenant socketPath` with the fixture's
// positional arguments. It is NewProcessSpawn with the script as its only
// read path and no RSS cap, which is what the packaged echo fixture uses.
func DefaultSpawn(script string, heapMB int, diagLog interface {
	Write([]byte) (int, error)
}) SpawnFunc {
	return NewProcessSpawn(ProcessSpec{
		Script: script,
		HeapMB: heapMB,
		Diag:   diagLog,
		Args:   func(tenant, socketPath string) []string { return []string{tenant, socketPath} },
	})
}

// spawnFailure is the error a child that never dialled back produces. It
// carries the exit status when the wait status is known — the only honest
// signal a package that dies at load leaves behind — and points the operator
// at the child's own diagnostics, which go to the host log rather than into
// the protocol.
func spawnFailure(reason string, cmd *exec.Cmd, heapMB int) error {
	detail := reason
	if cmd.ProcessState != nil {
		if _, exit := classifyExit(cmd.ProcessState, heapMB); exit != "" {
			detail = reason + " — " + exit
		}
	}
	return &CallError{Code: CodeSpawnFailed, Detail: detail + "; see the sidecar log lines above"}
}

// processArgv builds the executable and argument vector for one child. It is
// pure so a test can assert the exact posture without a Node binary: the
// permission flag, one fs-read grant per allowed path, and no other --allow-*
// grant that would widen the boundary.
func processArgv(spec ProcessSpec, node, script string, readPaths []string, tenant, socketPath string) (string, []string) {
	executable := node
	var argv []string
	if len(spec.Wrapper) > 0 {
		executable = spec.Wrapper[0]
		argv = append(argv, spec.Wrapper[1:]...)
		argv = append(argv, node)
	}
	if spec.HeapMB > 0 {
		argv = append(argv, "--max-old-space-size="+strconv.Itoa(spec.HeapMB))
	}
	argv = append(argv, "--permission", "--allow-fs-read="+script)
	for _, path := range readPaths {
		if path == script {
			continue
		}
		argv = append(argv, "--allow-fs-read="+path)
	}
	argv = append(argv, script)
	childArgs := []string{tenant, socketPath}
	if spec.Args != nil {
		childArgs = spec.Args(tenant, socketPath)
	}
	argv = append(argv, childArgs...)
	return executable, argv
}

// childEnvironment is the entire environment a sidecar process receives. The
// host's environment holds the credential master key and the database DSN;
// third-party JavaScript never sees either.
func childEnvironment() []string {
	timezone := os.Getenv("TZ")
	if timezone == "" {
		timezone = "UTC"
	}
	return []string{"LANG=C.UTF-8", "TZ=" + timezone}
}

func resolvePath(path string) (string, error) {
	absolute, err := filepath.Abs(path)
	if err != nil {
		return "", err
	}
	return filepath.EvalSymlinks(absolute)
}

// socketDir creates the directory holding one process's socket, mode 0700.
// Unix socket paths are capped near a hundred bytes and the platform temp dir
// can already be long, so an overlong path falls back to /tmp rather than
// failing every spawn on machines with deep home directories.
func socketDir(runtimeDir string) (string, error) {
	base := runtimeDir
	if base == "" {
		base = os.TempDir()
	}
	dir, err := os.MkdirTemp(base, "kflow-sidecar-*")
	if err != nil {
		return "", err
	}
	if len(filepath.Join(dir, "sidecar.sock")) > 90 {
		removeDir(dir)
		dir, err = os.MkdirTemp("/tmp", "kflow-sidecar-*")
		if err != nil {
			return "", err
		}
	}
	if err := os.Chmod(dir, 0o700); err != nil {
		removeDir(dir)
		return "", err
	}
	return dir, nil
}

func removeDir(dir string) {
	if dir != "" {
		_ = os.RemoveAll(dir)
	}
}

func appendUnique(paths []string, path string) []string {
	for _, existing := range paths {
		if existing == path {
			return paths
		}
	}
	return append(paths, path)
}
