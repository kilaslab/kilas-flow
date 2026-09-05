package guardrails_test

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// KilasFlow reimplements n8n's interchange format in Go and takes no n8n code.
// Every n8n package — n8n-workflow, n8n-core, n8n-nodes-base, the n8n CLI and
// the @n8n/* packages — declares "LicenseRef-n8n-sustainable-use", whose terms
// this Apache-2.0, white-label, multi-tenant, embedded product cannot meet. So
// no n8n package may appear in any manifest, and no build may read the
// reference checkout.
//
// The failure this guards against is quiet: a dependency added under time
// pressure to get one type definition, or a script that reads the reference
// checkout because it was the fastest fixture to hand. Neither looks like a
// licence violation in a diff. See .pine/memory/licensing.md.
//
// Every check here is scoped to tracked files. The boundary is about what
// enters the repository, so an untracked local download — a vendored API
// reference bundle, a scratch fixture — is deliberately out of scope, and
// scoping this way also keeps dependency trees and build output from producing
// noise no one would act on.

// foreignPathMarkers are substrings that only appear in a build input when it
// reaches outside the repository for bytes. The reference checkouts are the
// licence-relevant case — reading n8n by hand is the entire technique, reading
// it from a build step would make foreign source a build input — but the rule
// is stated portably, because an absolute path into somebody's home directory
// never belongs in a build input on any machine.
//
// Stored as fragments and joined at run time so that this file, which must name
// the patterns it hunts for, does not trip its own check.
var foreignPathMarkers = []struct{ prefix, suffix, why string }{
	{"/Us", "ers/", "an absolute path into a home directory is machine-specific"},
	{"/ho", "me/", "an absolute path into a home directory is machine-specific"},
	{"mitra", "chat/n8n", "the reference checkouts are read-only specification material, never a build input"},
}

// The WAHA clone deliberately has no marker of its own. Its package name,
// @devlikeapro/n8n-nodes-waha, is a node *type* string that appears in real
// workflow JSON and therefore in fixtures and tests — it is format fact that
// must be nameable. A path to the clone is still caught, because reaching it
// from a build input requires an absolute path and those are matched above.

// buildInputGlobs are the file kinds that can pull bytes in at build or test
// time. Prose may name the reference checkout; these may not read it.
var buildInputGlobs = []string{
	"*.go", "*.ts", "*.js", "*.svelte", "*.mjs", "*.cjs",
	"Makefile", "Dockerfile", "*.mk", "*.sh", "*.yaml", "*.yml",
}

// guardrailsDir is exempt from the content scan. It holds only these tests, and
// a checker cannot both name a forbidden pattern and forbid naming it.
const guardrailsDir = "internal/guardrails"

// repoRoot walks up from the test's working directory to the directory holding
// go.mod, so the test is independent of where `go test` was invoked from.
func repoRoot(t *testing.T) string {
	t.Helper()
	dir, err := os.Getwd()
	if err != nil {
		t.Fatalf("Getwd() error = %v", err)
	}
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			t.Fatal("no go.mod found above the working directory")
		}
		dir = parent
	}
}

// trackedFiles lists every path git has under version control, which is exactly
// the set of bytes this repository distributes.
func trackedFiles(t *testing.T, root string) []string {
	t.Helper()
	command := exec.Command("git", "-C", root, "ls-files", "-z")
	out, err := command.Output()
	if err != nil {
		t.Skipf("cannot list tracked files (%v); the licence boundary is checked against a git work tree", err)
	}
	var paths []string
	for _, entry := range strings.Split(string(out), "\x00") {
		if entry != "" {
			paths = append(paths, entry)
		}
	}
	if len(paths) == 0 {
		t.Fatal("git reported no tracked files; the discovery step is broken")
	}
	return paths
}

// dependencyNames returns every declared dependency of a package.json, across
// all four dependency blocks, because a devDependency ships no bytes but still
// puts the package in the tree a generator could read.
func dependencyNames(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s: %v", path, err)
	}
	var manifest map[string]json.RawMessage
	if err := json.Unmarshal(raw, &manifest); err != nil {
		t.Fatalf("parsing %s: %v", path, err)
	}
	var names []string
	for _, block := range []string{"dependencies", "devDependencies", "peerDependencies", "optionalDependencies"} {
		body, ok := manifest[block]
		if !ok {
			continue
		}
		var entries map[string]any
		if err := json.Unmarshal(body, &entries); err != nil {
			t.Fatalf("parsing %s of %s: %v", block, path, err)
		}
		for name := range entries {
			names = append(names, name)
		}
	}
	return names
}

// forbiddenDependency reports whether a dependency name belongs to n8n.
//
// It matches the name only, never file contents: seventy-nine tracked files
// legitimately contain the string "n8n" because interoperating with a format
// means naming it, and a check that fires on those is a check that gets
// deleted.
func forbiddenDependency(name string) bool {
	lower := strings.ToLower(name)
	return lower == "n8n" ||
		strings.HasPrefix(lower, "n8n-") ||
		strings.HasPrefix(lower, "n8n/") ||
		strings.HasPrefix(lower, "@n8n/") ||
		strings.Contains(lower, "/n8n-") ||
		strings.Contains(lower, "/n8n/")
}

func isBuildInput(path string) bool {
	base := filepath.Base(path)
	for _, glob := range buildInputGlobs {
		if matched, _ := filepath.Match(glob, base); matched {
			return true
		}
	}
	return false
}

// forbiddenModuleRequires returns every n8n module required by a go.mod, with
// the line number it sits on.
//
// It is a pure function over the file's bytes so that the check itself can be
// tested against a fixture. Proving it by editing the real go.mod is not
// possible: an unresolvable require stops the module loading, so `go test`
// never reaches the assertion and the check appears to pass.
func forbiddenModuleRequires(content string) []moduleRequire {
	var found []moduleRequire
	for index, line := range strings.Split(content, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "" || strings.HasPrefix(trimmed, "//") {
			continue
		}
		// A require line, inside a block or standalone, names the module first.
		fields := strings.Fields(strings.TrimPrefix(trimmed, "require "))
		if len(fields) == 0 {
			continue
		}
		if forbiddenDependency(fields[0]) {
			found = append(found, moduleRequire{Line: index + 1, Path: fields[0]})
		}
	}
	return found
}

type moduleRequire struct {
	Line int
	Path string
}

// TestNoN8NDependencyInGoModule proves no Go dependency is an n8n package.
func TestNoN8NDependencyInGoModule(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join(repoRoot(t), "go.mod"))
	if err != nil {
		t.Fatalf("reading go.mod: %v", err)
	}
	for _, required := range forbiddenModuleRequires(string(raw)) {
		t.Errorf("go.mod:%d declares the n8n dependency %q; n8n packages are LicenseRef-n8n-sustainable-use and this module is Apache-2.0 (see .pine/memory/licensing.md)", required.Line, required.Path)
	}
}

// TestForbiddenModuleRequiresDetectsN8N proves the go.mod check fires, which
// the check running against the real go.mod can never show on its own.
func TestForbiddenModuleRequiresDetectsN8N(t *testing.T) {
	const fixture = `module github.com/kilaslabs/kilas-flow

go 1.27

require (
	github.com/go-chi/chi/v5 v5.2.1
	n8n-workflow v2.34.0
	// n8n-core v2.34.0 — commented out, still not allowed to count
)

require n8n-core v2.34.0
`
	found := forbiddenModuleRequires(fixture)
	var paths []string
	for _, required := range found {
		paths = append(paths, required.Path)
	}
	want := []string{"n8n-workflow", "n8n-core"}
	if len(paths) != len(want) {
		t.Fatalf("forbiddenModuleRequires() = %v, want %v", paths, want)
	}
	for index, path := range want {
		if paths[index] != path {
			t.Errorf("forbiddenModuleRequires()[%d] = %q, want %q", index, paths[index], path)
		}
	}
	// A commented-out require is prose, not a dependency; flagging it would
	// train people to delete the check.
	for _, required := range found {
		if required.Line == 8 {
			t.Errorf("flagged the commented-out require on line 8")
		}
	}
}

// TestForbiddenDependencyMatchesNamesNotProse pins the matcher's shape. Seventy-nine
// tracked files legitimately contain "n8n" because interoperating with a format
// means naming it, so the matcher must key on dependency names alone.
func TestForbiddenDependencyMatchesNamesNotProse(t *testing.T) {
	forbidden := []string{
		"n8n",
		"n8n-workflow",
		"n8n-core",
		"n8n-nodes-base",
		"@n8n/n8n-nodes-langchain",
		"@n8n/node-cli",
		"@devlikeapro/n8n-nodes-waha",
		"N8N-Workflow",
	}
	for _, name := range forbidden {
		if !forbiddenDependency(name) {
			t.Errorf("forbiddenDependency(%q) = false, want true", name)
		}
	}
	allowed := []string{
		"svelte",
		"@sveltejs/kit",
		"github.com/kilaslabs/kilas-flow",
		"typescript",
		"connection8nine",
		"my-n8n8-thing",
	}
	for _, name := range allowed {
		if forbiddenDependency(name) {
			t.Errorf("forbiddenDependency(%q) = true, want false", name)
		}
	}
}

// TestNoN8NDependencyInNodeManifests proves the same for every tracked
// package.json, discovered rather than listed so a manifest added later is
// covered without anyone remembering to extend this test.
func TestNoN8NDependencyInNodeManifests(t *testing.T) {
	root := repoRoot(t)
	seen := 0
	for _, rel := range trackedFiles(t, root) {
		if filepath.Base(rel) != "package.json" {
			continue
		}
		seen++
		for _, name := range dependencyNames(t, filepath.Join(root, rel)) {
			if forbiddenDependency(name) {
				t.Errorf("%s declares the n8n dependency %q; n8n packages are LicenseRef-n8n-sustainable-use and this repository is Apache-2.0 (see .pine/memory/licensing.md)", rel, name)
			}
		}
	}
	// A discovery bug that finds nothing would pass silently forever.
	if seen == 0 {
		t.Fatal("found no tracked package.json to check; the discovery step is broken")
	}
}

// TestReferenceCheckoutIsNeverABuildInput proves no tracked file that runs at
// build or test time reaches outside the repository for bytes. Prose may name
// the reference checkouts — tickets and memory do, deliberately — so only build
// inputs are scanned.
func TestReferenceCheckoutIsNeverABuildInput(t *testing.T) {
	root := repoRoot(t)
	scanned := 0
	for _, rel := range trackedFiles(t, root) {
		slash := filepath.ToSlash(rel)
		if !isBuildInput(rel) || strings.HasPrefix(slash, guardrailsDir+"/") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(root, rel))
		if err != nil {
			t.Errorf("reading %s: %v", rel, err)
			continue
		}
		body := string(raw)
		scanned++
		for _, marker := range foreignPathMarkers {
			needle := marker.prefix + marker.suffix
			if strings.Contains(body, needle) {
				t.Errorf("%s contains %q: %s (see .pine/memory/licensing.md)", rel, needle, marker.why)
			}
		}
	}
	if scanned == 0 {
		t.Fatal("scanned no build inputs; the discovery step is broken")
	}
}

// TestVendoredThirdPartyCarriesItsLicence proves every vendored third-party
// directory keeps its notice beside the bytes. A vendored MIT file whose notice
// lives one directory away is a licence violation waiting to be found.
func TestVendoredThirdPartyCarriesItsLicence(t *testing.T) {
	root := filepath.Join(repoRoot(t), "third_party")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return // Nothing vendored yet is a valid state.
	}
	if err != nil {
		t.Fatalf("reading third_party: %v", err)
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		for _, required := range []string{"LICENSE", "PROVENANCE.md"} {
			if _, err := os.Stat(filepath.Join(root, entry.Name(), required)); err != nil {
				t.Errorf("third_party/%s has no %s beside its vendored bytes", entry.Name(), required)
			}
		}
	}
}
