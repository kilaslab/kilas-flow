package runcode

import (
	"path/filepath"
	"strings"
	"testing"
)

// A deployment that mounts the toolchain and runs with a read-only root
// filesystem has exactly one writable place: the data volume the code cache
// already sits on. Pointing GOCACHE at it is what makes that deployment able
// to build at all, and it must never override a GOCACHE the operator set
// themselves.
func TestTheBuildEnvironmentPointsGoAtTheDataVolume(t *testing.T) {
	cacheDir := t.TempDir()
	compiler := &ToolchainCompiler{GoBinary: "go", CacheDir: cacheDir}

	t.Setenv("GOCACHE", "")
	env := compiler.buildEnv()
	if !contains(env, "GOCACHE="+cacheDir) {
		t.Errorf("buildEnv() = %v, want GOCACHE pointing at the code cache", env)
	}
	for _, wanted := range []string{"GOOS=wasip1", "GOARCH=wasm", "CGO_ENABLED=0", "GOPROXY=off"} {
		if !contains(env, wanted) {
			t.Errorf("buildEnv() = %v, want %s", env, wanted)
		}
	}

	// The go command refuses a relative GOCACHE, and code.cache_dir defaults to
	// a relative path, so the value handed to it is absolute however the
	// deployment spelled it. Getting this wrong is not a subtle failure: every
	// build in the shipped image dies with "GOCACHE is not an absolute path".
	relative := &ToolchainCompiler{GoBinary: "go", CacheDir: filepath.Join("data", "codecache")}
	got := gocache(relative.buildEnv())
	if !filepath.IsAbs(got) {
		t.Errorf("buildEnv() GOCACHE = %q, want an absolute path", got)
	}
	if !strings.HasSuffix(got, filepath.Join("data", "codecache")) {
		t.Errorf("buildEnv() GOCACHE = %q, want it to end in the configured directory", got)
	}

	// An operator's own GOCACHE wins: they may have a warm cache on a faster
	// volume, and silently redirecting it would throw that away.
	t.Setenv("GOCACHE", "/tmp/operators-own-cache")
	if env := compiler.buildEnv(); contains(env, "GOCACHE="+cacheDir) {
		t.Errorf("buildEnv() = %v, want the operator's GOCACHE left alone", env)
	}

	// No code cache configured means no GOCACHE is invented.
	plain := &ToolchainCompiler{GoBinary: "go"}
	t.Setenv("GOCACHE", "")
	if env := plain.buildEnv(); contains(env, "GOCACHE="+cacheDir) {
		t.Errorf("buildEnv() = %v, want no GOCACHE without a configured cache directory", env)
	}
}

func contains(env []string, wanted string) bool {
	for _, entry := range env {
		if entry == wanted {
			return true
		}
	}
	return false
}

// gocache returns the GOCACHE value an environment sets, or "".
//
// The last entry wins, which is how the go command reads a duplicated variable
// and how os/exec hands one to a child.
func gocache(env []string) string {
	value := ""
	for _, entry := range env {
		if got, found := strings.CutPrefix(entry, "GOCACHE="); found {
			value = got
		}
	}
	return value
}

// A compiler that was never given a binary still answers, and the environment
// helper is usable from a zero value.
func TestAZeroToolchainCompilerNamesTheDefaultBinary(t *testing.T) {
	compiler := &ToolchainCompiler{}
	if name := compiler.binaryName(); name != "go" {
		t.Errorf("binaryName() = %q, want the default", name)
	}
	var nothing *ToolchainCompiler
	if name := nothing.binaryName(); name != "go" {
		t.Errorf("binaryName() on a nil compiler = %q, want the default", name)
	}
}
