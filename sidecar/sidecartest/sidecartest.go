// Package sidecartest locates the Node.js binary the real-process sidecar
// tests need.
//
// The sidecar tests run third-party JavaScript in a real Node process. On a
// machine without Node they skip with a message naming exactly what is
// missing, which is the honest posture for a deployment that declines the
// sidecar. KILASFLOW_TEST_REQUIRE_NODE=1 turns that skip into a failure so a
// CI job can prove the tests really ran instead of silently skipping.
//
// The package deliberately imports nothing from the sidecar: a test helper
// that pulled the host code in would let a compile error in the host hide
// itself behind a skipped test.
package sidecartest

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// MinimumNodeMajor is the oldest Node major the sidecar supports. Only Node
// 24.16 has been verified; the permission model has changed between majors, so
// the boot-time probe (CheckNode) is the compatibility check.
const MinimumNodeMajor = 24

// require reports whether a missing or too-old Node must fail the test rather
// than skip it.
func require(t testing.TB) bool {
	t.Helper()
	return os.Getenv("KILASFLOW_TEST_REQUIRE_NODE") == "1"
}

// Node returns the path to a Node binary new enough for the sidecar tests. It
// skips the test when Node is absent or older than MinimumNodeMajor, and
// fails instead of skipping when KILASFLOW_TEST_REQUIRE_NODE=1.
func Node(t testing.TB) string {
	t.Helper()
	if path, err := exec.LookPath("node"); err == nil {
		if version, err := nodeVersion(path); err == nil {
			if major := majorOf(version); major >= MinimumNodeMajor {
				return path
			}
			message := "node " + version + " is too old for the sidecar tests; they need Node " + strconv.Itoa(MinimumNodeMajor) + " or newer"
			if require(t) {
				t.Fatal(message)
			}
			t.Skip(message)
		}
		message := "node is on PATH but did not answer --version for the sidecar tests"
		if require(t) {
			t.Fatal(message)
		}
		t.Skip(message)
	}
	message := "node is not on PATH; install Node " + strconv.Itoa(MinimumNodeMajor) + " to run the sidecar tests"
	if require(t) {
		t.Fatal(message)
	}
	t.Skip(message)
	return ""
}

func nodeVersion(path string) (string, error) {
	out, err := exec.Command(path, "--version").Output()
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(string(out)), nil
}

func majorOf(version string) int {
	version = strings.TrimPrefix(strings.TrimSpace(version), "v")
	head, _, _ := strings.Cut(version, ".")
	major, err := strconv.Atoi(head)
	if err != nil {
		return 0
	}
	return major
}

// PackagesDir returns the directory holding the hand-written fixture packages
// the runner tests load. It is resolved from this file's own location, the
// same way the fixture test above finds its script, so the tests do not depend
// on where `go test` was invoked from.
func PackagesDir(t testing.TB) string {
	t.Helper()
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller refused to say where PackagesDir lives")
	}
	return filepath.Join(filepath.Dir(file), "..", "testdata", "packages")
}
