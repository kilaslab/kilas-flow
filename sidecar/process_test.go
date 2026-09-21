package sidecar

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/sidecar/sidecartest"
)

// fixturePath locates a script under sidecar/fixture relative to this test
// file, so the tests do not depend on the working directory.
func fixturePath(t *testing.T, name string) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller refused to say where this test lives")
	}
	return filepath.Join(filepath.Dir(file), "fixture", name)
}

// The argv posture is asserted without a Node binary: the permission flag, one
// fs-read grant per allowed path, and no other --allow-* grant.
func TestProcessArgvIsLockedDown(t *testing.T) {
	spec := ProcessSpec{Script: "/srv/sidecar/echo.js", HeapMB: 128, ReadPaths: []string{"/srv/packages"}}
	executable, argv := processArgv(spec, "/usr/local/bin/node", "/srv/sidecar/echo.js", []string{"/srv/packages"}, "tenant-a", "/run/s.sock")

	if executable != "/usr/local/bin/node" {
		t.Errorf("executable = %q, want the node path", executable)
	}
	want := []string{
		"--max-old-space-size=128",
		"--permission",
		"--allow-fs-read=/srv/sidecar/echo.js",
		"--allow-fs-read=/srv/packages",
		"/srv/sidecar/echo.js",
		"tenant-a",
		"/run/s.sock",
	}
	if strings.Join(argv, "\x00") != strings.Join(want, "\x00") {
		t.Errorf("argv = %q, want %q", argv, want)
	}
	for _, arg := range argv {
		if strings.HasPrefix(arg, "--allow-") && !strings.HasPrefix(arg, "--allow-fs-read=") {
			t.Errorf("argv grants %q: only fs-read may be granted", arg)
		}
	}
	if len(argv) > 1 && !strings.HasPrefix(argv[len(argv)-2], "tenant-") {
		t.Errorf("argv %q: the tenant must be passed last, after the script", argv)
	}
}

func TestProcessArgvUsesWrapper(t *testing.T) {
	spec := ProcessSpec{
		Script:  "/srv/sidecar/echo.js",
		Wrapper: []string{"/usr/bin/sandbox-exec", "-p", "(version 1)"},
	}
	executable, argv := processArgv(spec, "/usr/local/bin/node", "/srv/sidecar/echo.js", nil, "tenant-a", "/run/s.sock")
	if executable != "/usr/bin/sandbox-exec" {
		t.Errorf("executable = %q, want the wrapper", executable)
	}
	if len(argv) < 4 || argv[0] != "-p" || argv[2] != "/usr/local/bin/node" || argv[3] != "--permission" {
		t.Errorf("argv = %q, want the wrapper prefix then node", argv)
	}
}

// A script path that traverses a symlink is resolved before the fs-read grant
// is built: Node refuses to start when a component of the granted path is a
// symlink, which macOS's own temp dir is.
func TestSymlinkedScriptPathIsGranted(t *testing.T) {
	node := sidecartest.Node(t)
	script := fixturePath(t, "echo.js")

	linkDir := filepath.Join(t.TempDir(), "linked")
	if err := os.Symlink(filepath.Dir(script), linkDir); err != nil {
		t.Fatalf("os.Symlink: %v", err)
	}
	linkedScript := filepath.Join(linkDir, filepath.Base(script))

	limits := testLimits()
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(NewProcessSpawn(ProcessSpec{NodePath: node, Script: linkedScript, HeapMB: 64}), limits)
	defer pool.Close()

	result, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.echo", Items: []Item{{JSON: map[string]any{"name": "ada"}}}})
	if err != nil {
		t.Fatalf("Execute() through a symlinked script path error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("result = %+v, want one item", result.Items)
	}
}

// CheckNode rejects an old Node with the version it found, and accepts a new
// one. The stub is a shell script so the check itself needs no Node.
func TestCheckNodeRejectsOldNode(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("the stub is a shell script")
	}
	stub := filepath.Join(t.TempDir(), "node")
	if err := os.WriteFile(stub, []byte("#!/bin/sh\necho v20.1.0\n"), 0o700); err != nil {
		t.Fatalf("writing stub: %v", err)
	}
	version, err := CheckNode(stub)
	if version != "v20.1.0" {
		t.Errorf("version = %q, want the stub's version", version)
	}
	var callErr *CallError
	if !errors.As(err, &callErr) {
		t.Fatalf("CheckNode() error = %v, want *CallError", err)
	}
	if callErr.Code != CodeNoSidecar {
		t.Errorf("code = %q, want %q", callErr.Code, CodeNoSidecar)
	}
	if !strings.Contains(callErr.Detail, "Node 24") {
		t.Errorf("detail = %q, want it to name the requirement", callErr.Detail)
	}
}

// The listener exists before the child starts, so a child that dials as its
// first act always finds it — thirty cold starts under the race detector.
func TestListenBeforeStart(t *testing.T) {
	node := sidecartest.Node(t)
	script := fixturePath(t, "dial.js")
	limits := testLimits()
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(NewProcessSpawn(ProcessSpec{NodePath: node, Script: script, HeapMB: 64}), limits)
	defer pool.Close()

	for i := 0; i < 30; i++ {
		tenant := fmt.Sprintf("tenant-%d", i)
		if _, err := pool.Execute(context.Background(), Request{Tenant: tenant, Node: "fixture.dial"}); err != nil {
			t.Fatalf("cold start %d error = %v", i, err)
		}
	}
}

// The child's environment is an allowlist. The credential master key is
// present in the host's environment and must not reach third-party
// JavaScript; macOS injects one extra key of its own.
func TestChildEnvironmentIsAllowlisted(t *testing.T) {
	node := sidecartest.Node(t)
	t.Setenv("KILASFLOW_ENCRYPTION_KEY", "leak-canary-master-key")

	limits := testLimits()
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(NewProcessSpawn(ProcessSpec{NodePath: node, Script: fixturePath(t, "env.js"), HeapMB: 64}), limits)
	defer pool.Close()

	result, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.env"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(result.Items) != 1 {
		t.Fatalf("result = %+v, want one item", result.Items)
	}
	allowed := map[string]bool{"LANG": true, "TZ": true}
	if runtime.GOOS == "darwin" {
		// The OS injects this into every process, even with an empty Env.
		allowed["__CF_USER_TEXT_ENCODING"] = true
	}
	keys, _ := result.Items[0].JSON["keys"].([]any)
	if len(keys) == 0 {
		t.Fatal("the child reported no environment keys; the fixture did not run")
	}
	for _, key := range keys {
		name, _ := key.(string)
		if !allowed[name] {
			t.Errorf("child environment contains %q, which is not on the allowlist", name)
		}
	}
	leaked, _ := result.Items[0].JSON["leaked"].([]any)
	if len(leaked) != 0 {
		t.Errorf("child environment leaked the canary: %v", leaked)
	}
	if !strings.Contains(strings.Join(anyStrings(keys), ","), "LANG") {
		t.Errorf("keys = %v, want LANG to be present", keys)
	}
}

// The permission model denies spawning, out-of-grant reads and any write.
func TestChildCannotSpawnOrReadOutsideItsGrant(t *testing.T) {
	node := sidecartest.Node(t)
	script := fixturePath(t, "perm.js")
	hostPath, err := os.Executable()
	if err != nil {
		t.Fatalf("os.Executable: %v", err)
	}
	runtimeDir := t.TempDir()
	writePath := filepath.Join(runtimeDir, "write-attempt")

	limits := testLimits()
	limits.SpawnTimeout = 15 * time.Second
	pool := NewPool(NewProcessSpawn(ProcessSpec{
		NodePath: node, Script: script, HeapMB: 64, RuntimeDir: runtimeDir,
		Args: func(tenant, socketPath string) []string {
			return []string{tenant, socketPath, hostPath, writePath}
		},
	}), limits)
	defer pool.Close()

	result, err := pool.Execute(context.Background(), Request{Tenant: "tenant-a", Node: "fixture.perm"})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	attempts, _ := result.Items[0].JSON["attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("the fixture reported no attempts: %+v", result.Items[0].JSON)
	}
	seen := map[string]string{}
	for _, entry := range attempts {
		record, _ := entry.(map[string]any)
		name, _ := record["name"].(string)
		verdict, _ := record["result"].(string)
		seen[name] = verdict
	}
	for _, name := range []string{"child_process", "fs_read", "fs_write", "process_binding"} {
		if seen[name] != "ERR_ACCESS_DENIED" {
			t.Errorf("%s = %q, want ERR_ACCESS_DENIED (%v)", name, seen[name], seen)
		}
	}
}

func anyStrings(values []any) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if text, ok := value.(string); ok {
			out = append(out, text)
		}
	}
	return out
}
