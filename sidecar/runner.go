package sidecar

import (
	"context"
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"os"
	"path/filepath"
	"strings"
)

// runnerSource is the clean-room JavaScript runner, embedded so there is
// nothing to install beside the Go binary. It uses Node built-ins only and has
// no dependency of its own; the packages it loads are installed by the
// operator and are never shipped here. See runner/runner.cjs for the licence
// statement, the guard, and what the guard does not guarantee.
//
//go:embed runner/runner.cjs
var runnerSource embed.FS

const runnerFile = "runner/runner.cjs"

// RunnerContent returns the runner's bytes and their hex digest. The digest is
// taken over the exact bytes that run, so a deployment can log what it loaded
// and so the extracted file can be named after it.
func RunnerContent() ([]byte, string, error) {
	source, err := runnerSource.ReadFile(runnerFile)
	if err != nil {
		return nil, "", &CallError{Code: CodeNoSidecar, Detail: "the sidecar runner is missing from this build: " + err.Error()}
	}
	sum := sha256.Sum256(source)
	return source, hex.EncodeToString(sum[:]), nil
}

// ExtractRunner writes the embedded runner into runtimeDir and returns the
// path to run it from. The file name carries the digest of its content, so:
//
//   - two host processes extracting the same version write the same file,
//   - an upgrade writes a new file instead of rewriting one a running child is
//     still reading, and
//   - a partial write can never run, because the write is temp-then-rename.
//
// The path is symlink-resolved: Node's permission model refuses to start when
// a granted path traverses a symbolic link, and on macOS the platform temp
// directory is one.
func ExtractRunner(runtimeDir string) (string, error) {
	source, digest, err := RunnerContent()
	if err != nil {
		return "", err
	}
	if runtimeDir == "" {
		return "", &CallError{Code: CodeSpawnFailed, Detail: "the sidecar runner needs a runtime directory to be extracted into"}
	}
	if err := os.MkdirAll(runtimeDir, 0o700); err != nil {
		return "", &CallError{Code: CodeSpawnFailed, Detail: "the sidecar runtime directory could not be created: " + err.Error()}
	}
	path := filepath.Join(runtimeDir, "runner-"+digest[:12]+".cjs")
	if existing, err := os.ReadFile(path); err == nil && string(existing) == string(source) {
		return resolvePath(path)
	}

	temp, err := os.CreateTemp(runtimeDir, "runner-*.tmp")
	if err != nil {
		return "", &CallError{Code: CodeSpawnFailed, Detail: "the sidecar runner could not be written: " + err.Error()}
	}
	tempPath := temp.Name()
	defer os.Remove(tempPath)

	cleanup := func(reason string, cause error) (string, error) {
		temp.Close()
		return "", &CallError{Code: CodeSpawnFailed, Detail: reason + ": " + cause.Error()}
	}
	if _, err := temp.Write(source); err != nil {
		return cleanup("the sidecar runner could not be written", err)
	}
	if err := temp.Close(); err != nil {
		return cleanup("the sidecar runner could not be closed", err)
	}
	if err := os.Chmod(tempPath, 0o600); err != nil {
		return cleanup("the sidecar runner could not be given its mode", err)
	}
	if err := os.Rename(tempPath, path); err != nil {
		return cleanup("the sidecar runner could not be put in place", err)
	}
	return resolvePath(path)
}

// RunnerConfig describes the community packages one sidecar process loads and
// the limits it runs under. It is not the configuration file's shape: the
// composition root reads its own configuration and hands resolved values here.
type RunnerConfig struct {
	// NodePath is the operator-installed Node binary. Empty means PATH.
	NodePath string
	// RunnerPath is the extracted runner script, from ExtractRunner.
	RunnerPath string
	// PackagesDir holds the operator-installed packages, either directly or
	// under node_modules/, in the layout npm produces.
	PackagesDir string
	// Packages is the allowlist: the only package names a process loads.
	Packages []string
	// RuntimeDir is where the socket directory is created.
	RuntimeDir string
	// Wrapper prefixes the child's argv, for a deployment that runs the child
	// under a sandboxing launcher. Empty runs Node directly.
	Wrapper []string
	// HeapMB sets the child's JavaScript heap ceiling; MaxRSSMB is the RSS
	// ceiling the host watchdog enforces. Zero leaves each unset.
	HeapMB   int
	MaxRSSMB int
	// Diag receives the child's stdout and stderr. They are diagnostics only:
	// nothing the protocol depends on is ever read from them.
	Diag interface {
		Write([]byte) (int, error)
	}
}

// RunnerSpawn returns the SpawnFunc that starts the embedded runner. The role
// follows the tenant, which is the split Pool and Discover already make: an
// empty tenant asks a process for the catalogue, a tenant asks it for runs.
//
// A configuration that cannot work — no runner extracted, no packages
// directory, a packages directory that does not resolve — produces a spawn
// that fails with that diagnostic, so the operator reads it on the first run
// instead of watching a process fail to start.
func RunnerSpawn(config RunnerConfig) SpawnFunc {
	if config.RunnerPath == "" {
		return failingSpawn("the sidecar runner has not been extracted: call ExtractRunner first")
	}
	if config.PackagesDir == "" {
		return failingSpawn("no community packages directory is configured for the JavaScript sidecar")
	}
	packagesDir, err := resolvePath(config.PackagesDir)
	if err != nil {
		return failingSpawn("the community packages directory is not readable: " + err.Error())
	}

	packages := make([]string, 0, len(config.Packages))
	for _, name := range config.Packages {
		if name = strings.TrimSpace(name); name != "" {
			packages = appendUnique(packages, name)
		}
	}
	if len(packages) == 0 {
		return failingSpawn("no community packages are allowlisted for the JavaScript sidecar")
	}

	return NewProcessSpawn(ProcessSpec{
		NodePath:   config.NodePath,
		Script:     config.RunnerPath,
		HeapMB:     config.HeapMB,
		MaxRSSMB:   config.MaxRSSMB,
		Wrapper:    config.Wrapper,
		RuntimeDir: config.RuntimeDir,
		Diag:       config.Diag,
		// The runner resolves <packagesDir>/node_modules/<name> and
		// <packagesDir>/<name>, and reads each package's manifest and the
		// files that manifest names, so the packages directory is the one
		// grant. It is already symlink-resolved, and the flag carries the
		// same resolved path, because Node matches a granted path against the
		// resolved one.
		ReadPaths: []string{packagesDir},
		Args: func(tenant, socketPath string) []string {
			role := "run"
			if tenant == "" {
				role = "describe"
			}
			args := []string{"--role=" + role, "--tenant=" + tenant, "--socket=" + socketPath, "--packages-dir=" + packagesDir}
			for _, name := range packages {
				args = append(args, "--package="+name)
			}
			return args
		},
	})
}

// failingSpawn is the spawn a misconfigured sidecar gets: every run fails with
// the same named diagnostic and the host process is untouched.
func failingSpawn(detail string) SpawnFunc {
	err := &CallError{Code: CodeSpawnFailed, Detail: detail}
	return func(context.Context, string, string) (*Child, error) { return nil, err }
}
