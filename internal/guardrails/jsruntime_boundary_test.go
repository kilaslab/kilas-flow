package guardrails_test

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// The embedded JavaScript runtime runs code a tenant wrote, inside the server
// process. The sandbox is exactly the set of globals the runtime installs, so
// what keeps a script away from the host is what the Go code behind those
// globals can reach. That reach is decided by imports.
//
// The rules are about imports, not call sites, because an import is where the
// reach begins and it is cheap to see in review. `os.ReadFile` added to a
// shim months from now is one line; the `os` import it needs is what these
// tests refuse. A capability the runtime genuinely needs, like outbound HTTP,
// arrives as a Go function the node package hands in, never as an import here.
//
// The engine itself sits behind one seam (EPIC-tjnr1z, amendment 2), so a
// swap to the fallback engine is one adapter: only `internal/jsrun/engine*.go`
// may import the engine's runtime packages, and only those files plus the
// analyser may import its syntax-only packages.

const (
	jsrunDir         = "internal/jsrun/"
	gojaRuntime      = "github.com/dop251/goja"
	gojaModules      = "github.com/dop251/"
	regexpEngine     = "github.com/dlclark/regexp2"
	moduleImportPath = "github.com/kilaslab/kilas-flow/"
)

// gojaSyntaxPackages are the engine's packages that only parse: they read
// text and build a tree, and run nothing. The analyser uses them to refuse
// constructs before a script runs.
var gojaSyntaxPackages = map[string]bool{
	gojaRuntime + "/ast":       true,
	gojaRuntime + "/parser":    true,
	gojaRuntime + "/file":      true,
	gojaRuntime + "/token":     true,
	gojaRuntime + "/unistring": true,
}

// importRule is one boundary: the files it covers must not import what it
// forbids.
type importRule struct {
	name    string
	covers  func(relative string) bool
	forbids func(importPath string) bool
}

type importViolation struct {
	rule, file, importPath string
}

func (violation importViolation) String() string {
	return fmt.Sprintf("%s imports %q (%s)", violation.file, violation.importPath, violation.rule)
}

// importViolations checks every file against every rule. It is a pure
// function of the parsed files, so the self-test can feed it fixtures.
func importViolations(files []goFile, rules []importRule) []importViolation {
	violations := []importViolation{}
	for _, file := range files {
		for _, spec := range file.file.Imports {
			path, err := strconv.Unquote(spec.Path.Value)
			if err != nil {
				continue
			}
			for _, rule := range rules {
				if rule.covers(file.relative) && rule.forbids(path) {
					violations = append(violations, importViolation{rule: rule.name, file: file.relative, importPath: path})
				}
			}
		}
	}
	sort.Slice(violations, func(i, j int) bool { return violations[i].String() < violations[j].String() })
	return violations
}

func inRuntime(relative string) bool { return strings.HasPrefix(relative, jsrunDir) }

func isCodeNodeFile(relative string) bool {
	return strings.HasPrefix(relative, "nodes/jscode") && strings.HasSuffix(relative, ".go")
}

func isEngineSeam(relative string) bool {
	return inRuntime(relative) && strings.HasPrefix(filepath.Base(relative), "engine")
}

func isAnalyser(relative string) bool {
	return inRuntime(relative) && strings.HasPrefix(filepath.Base(relative), "analyze")
}

func underPath(importPath, root string) bool {
	return importPath == root || strings.HasPrefix(importPath, root+"/")
}

// processAndFiles are the standard-library packages that reach the operating
// system: files, processes, signals and raw memory.
func processAndFiles(importPath string) bool {
	switch importPath {
	case "os", "os/exec", "os/signal", "os/user", "syscall", "unsafe", "plugin", "io/ioutil":
		return true
	}
	return strings.HasPrefix(importPath, "golang.org/x/sys/")
}

// hostSubsystems are the parts of KilasFlow that own shared state or other
// runtimes: storage, the Node.js sidecar, and outbound HTTP. The runtime gets
// what it needs from them as functions, never by importing them.
func hostSubsystems(importPath string) bool {
	for _, root := range []string{"sidecar", "internal/sidecarnode", "internal/repository", "internal/database", "internal/safehttp"} {
		if underPath(importPath, moduleImportPath+root) {
			return true
		}
	}
	return false
}

func runtimeReachRules() []importRule {
	return []importRule{
		{
			name:   "the JavaScript runtime reaches no process, file, network or host subsystem",
			covers: inRuntime,
			forbids: func(importPath string) bool {
				return processAndFiles(importPath) || underPath(importPath, "net") || hostSubsystems(importPath)
			},
		},
		{
			// The node package hands the runtime its host functions, so it may
			// use net/http through safehttp's client. It may still never open a
			// socket, a process or a file itself, nor reach the sidecar.
			name:   "the Code (JavaScript) node files reach no process, file, raw socket or sidecar",
			covers: isCodeNodeFile,
			forbids: func(importPath string) bool {
				return processAndFiles(importPath) || importPath == "net" ||
					underPath(importPath, moduleImportPath+"sidecar") || underPath(importPath, moduleImportPath+"internal/sidecarnode")
			},
		},
		{
			// Starting worker processes is the worker package's job, so it may
			// use os, os/exec and syscall. A worker runs tenant code, so the
			// package still reaches no network and no host subsystem: a
			// capability a script needs reaches it as a question the server
			// answers over the worker's pipes.
			name:   "the JavaScript worker package reaches no network or host subsystem",
			covers: func(relative string) bool { return strings.HasPrefix(relative, "internal/jsworker/") },
			forbids: func(importPath string) bool {
				return underPath(importPath, "net") || hostSubsystems(importPath)
			},
		},
	}
}

func engineSeamRules() []importRule {
	return []importRule{
		{
			name:   "only internal/jsrun/engine*.go imports the engine runtime",
			covers: func(relative string) bool { return !isEngineSeam(relative) },
			forbids: func(importPath string) bool {
				if gojaSyntaxPackages[importPath] {
					return false
				}
				return underPath(importPath, gojaRuntime) || underPath(importPath, gojaModules+"goja_nodejs") ||
					underPath(importPath, regexpEngine)
			},
		},
		{
			name:    "only the engine seam and the analyser import the engine's parser",
			covers:  func(relative string) bool { return !isEngineSeam(relative) && !isAnalyser(relative) },
			forbids: func(importPath string) bool { return gojaSyntaxPackages[importPath] },
		},
	}
}

func outsideRuntimeRules() []importRule {
	return []importRule{{
		name:   "nothing outside internal/jsrun imports the JavaScript engine",
		covers: func(relative string) bool { return !inRuntime(relative) },
		forbids: func(importPath string) bool {
			return strings.HasPrefix(importPath, gojaModules) || strings.HasPrefix(importPath, regexpEngine)
		},
	}}
}

func reportViolations(t *testing.T, violations []importViolation) {
	t.Helper()
	for _, violation := range violations {
		t.Errorf("%s.\n\tThe runtime runs tenant code; give it a Go function from the node package instead of an import.", violation)
	}
}

func TestTheJavaScriptRuntimeReachesNoProcessNetworkOrFiles(t *testing.T) {
	reportViolations(t, importViolations(parseGoFiles(t, repoRoot(t)), runtimeReachRules()))
}

func TestOnlyTheEngineSeamImportsTheJavaScriptEngine(t *testing.T) {
	reportViolations(t, importViolations(parseGoFiles(t, repoRoot(t)), engineSeamRules()))
}

func TestNothingOutsideTheRuntimeImportsTheJavaScriptEngine(t *testing.T) {
	reportViolations(t, importViolations(parseGoFiles(t, repoRoot(t)), outsideRuntimeRules()))
}

// TestTheImportRuleCatchesAForbiddenImport keeps the rules honest: a rule that
// matches nothing passes forever, so it is run against files that break it.
func TestTheImportRuleCatchesAForbiddenImport(t *testing.T) {
	fixtures := map[string]string{
		"internal/jsrun/shim.go":      `package jsrun; import ("os"; "net/http"; "github.com/dop251/goja"; "github.com/kilaslab/kilas-flow/internal/repository")`,
		"internal/jsrun/engine.go":    `package jsrun; import ("github.com/dop251/goja"; "github.com/dop251/goja_nodejs/buffer"; "github.com/dlclark/regexp2/v2"; "github.com/dop251/goja/parser")`,
		"internal/jsrun/analyze.go":   `package jsrun; import ("github.com/dop251/goja/ast"; "github.com/dop251/goja/parser")`,
		"internal/jsrun/crypto.go":    `package jsrun; import "github.com/dop251/goja/ast"`,
		"nodes/jscode_http.go":        `package nodes; import ("net"; "net/http"; "github.com/kilaslab/kilas-flow/sidecar")`,
		"internal/api/handlers/x.go":  `package handlers; import "github.com/dop251/goja/parser"`,
		"internal/expression/eval.go": `package expression; import "os"`,
	}
	files := []goFile{}
	fset := token.NewFileSet()
	for relative, source := range fixtures {
		parsed, err := parser.ParseFile(fset, relative, source, parser.ImportsOnly)
		if err != nil {
			t.Fatalf("fixture %s: %v", relative, err)
		}
		files = append(files, goFile{relative: relative, file: parsed})
	}
	rules := append(append(runtimeReachRules(), engineSeamRules()...), outsideRuntimeRules()...)
	got := []string{}
	for _, violation := range importViolations(files, rules) {
		got = append(got, violation.file+" -> "+violation.importPath)
	}
	want := []string{
		"internal/api/handlers/x.go -> github.com/dop251/goja/parser", // parser outside the analyser
		"internal/api/handlers/x.go -> github.com/dop251/goja/parser", // anything of the engine outside jsrun
		"internal/jsrun/crypto.go -> github.com/dop251/goja/ast",
		"internal/jsrun/shim.go -> github.com/dop251/goja",
		"internal/jsrun/shim.go -> github.com/kilaslab/kilas-flow/internal/repository",
		"internal/jsrun/shim.go -> net/http",
		"internal/jsrun/shim.go -> os",
		"nodes/jscode_http.go -> github.com/kilaslab/kilas-flow/sidecar",
		"nodes/jscode_http.go -> net",
	}
	sort.Strings(got)
	sort.Strings(want)
	if strings.Join(got, "\n") != strings.Join(want, "\n") {
		t.Errorf("violations =\n\t%s\nwant\n\t%s", strings.Join(got, "\n\t"), strings.Join(want, "\n\t"))
	}
}

// provenanceRow matches one row of a PROVENANCE.md files table:
// | `vendored path` | `upstream path` | `sha256` |
var provenanceRow = regexp.MustCompile("^\\|\\s*`([^`]+)`\\s*\\|\\s*`[^`]+`\\s*\\|\\s*`([0-9a-f]{64})`\\s*\\|")

// TestVendoredBundlesMatchTheirProvenanceDigests proves every vendored byte is
// the byte its provenance note says it is. The JavaScript bundles run inside
// the server, so a bundle edited in place would be code nobody reviewed under
// a name everybody trusts. KilasFlow's own files beside the bytes (the note
// itself and a Go embed stub) are the only files a digest does not cover.
func TestVendoredBundlesMatchTheirProvenanceDigests(t *testing.T) {
	root := filepath.Join(repoRoot(t), "third_party")
	entries, err := os.ReadDir(root)
	if os.IsNotExist(err) {
		return
	}
	if err != nil {
		t.Fatalf("reading third_party: %v", err)
	}
	checked := 0
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		dir := filepath.Join(root, entry.Name())
		note, err := os.ReadFile(filepath.Join(dir, "PROVENANCE.md"))
		if err != nil {
			continue // TestVendoredThirdPartyCarriesItsLicence reports the missing note.
		}
		digests := map[string]string{}
		for _, line := range strings.Split(string(note), "\n") {
			if match := provenanceRow.FindStringSubmatch(strings.TrimSpace(line)); match != nil {
				digests[match[1]] = match[2]
			}
		}
		files, err := os.ReadDir(dir)
		if err != nil {
			t.Fatalf("reading %s: %v", dir, err)
		}
		for _, file := range files {
			name := file.Name()
			if file.IsDir() || name == "PROVENANCE.md" || strings.HasSuffix(name, ".go") {
				continue
			}
			want, listed := digests[name]
			if !listed {
				t.Errorf("third_party/%s/%s has no SHA-256 row in PROVENANCE.md", entry.Name(), name)
				continue
			}
			raw, err := os.ReadFile(filepath.Join(dir, name))
			if err != nil {
				t.Fatalf("reading %s: %v", name, err)
			}
			sum := sha256.Sum256(raw)
			if got := hex.EncodeToString(sum[:]); got != want {
				t.Errorf("third_party/%s/%s has SHA-256 %s, but PROVENANCE.md records %s; vendored bytes are never edited in place", entry.Name(), name, got, want)
			}
			checked++
		}
	}
	if checked == 0 {
		t.Fatal("checked no vendored files; the discovery step is broken")
	}
}
