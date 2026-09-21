// Command skills-index renders skills/index.json from the bundle's frontmatter,
// so the index a harness reads is generated rather than maintained by hand.
//
// Usage:
//
//	go run ./scripts/skills-index          # regenerate skills/index.json
//	go run ./scripts/skills-index --check  # fail if the committed index is stale
//
// It refuses to write, or to pass, when the bundle breaks one of its own rules:
// a stale index of a bundle that violates its conventions would be a faithful
// copy of something no agent should load.
//
// This command lives in its own directory on purpose. scripts/config-reference.go
// already declares func main in package main, and a second func main in the same
// directory is a redeclaration go vet refuses, so the generator directory — not
// scripts/ — is the unit of packaging.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

func main() {
	check := flag.Bool("check", false, "compare the rendered index against the committed file and fail on any difference")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "skills-index: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	loaded, err := skills.LoadDir(filepath.Join(root, "skills"))
	if err != nil {
		return err
	}
	if findings := skills.Check(loaded); len(findings) > 0 {
		lines := make([]string, 0, len(findings))
		for _, finding := range findings {
			lines = append(lines, fmt.Sprintf("  %s [%s] %s", finding.Skill, finding.Rule, finding.Message))
		}

		return fmt.Errorf("the bundle breaks %d of its own rules, so the index was not written:\n%s",
			len(findings), strings.Join(lines, "\n"))
	}

	rendered, err := skills.Index(loaded)
	if err != nil {
		return err
	}

	path := filepath.Join(root, "skills", "index.json")
	committed, readErr := os.ReadFile(path)

	switch {
	case readErr == nil && string(committed) == string(rendered):
		fmt.Printf("skills/index.json: %d skills, up to date\n", len(loaded))

		return nil
	case readErr == nil && check:
		return fmt.Errorf("skills/index.json is stale: %s\nrun: make generate-skills-index", staleSummary(committed, rendered))
	case readErr == nil:
		// fall through to the write below
	case os.IsNotExist(readErr) && check:
		return fmt.Errorf("skills/index.json does not exist\nrun: make generate-skills-index")
	case os.IsNotExist(readErr):
		// fall through to the write below
	default:
		return fmt.Errorf("read skills/index.json: %w", readErr)
	}

	if err := os.WriteFile(path, rendered, 0o644); err != nil {
		return fmt.Errorf("write skills/index.json: %w", err)
	}
	fmt.Printf("skills/index.json: %d skills, %d bytes written\n", len(loaded), len(rendered))

	return nil
}

// staleSummary is the short diff a stale index deserves: where it first
// diverges and how much of it does, rather than two hundred lines of JSON.
func staleSummary(committed, rendered []byte) string {
	committedLines := strings.Split(string(committed), "\n")
	renderedLines := strings.Split(string(rendered), "\n")

	first := -1
	differing := 0
	for index := 0; index < len(committedLines) || index < len(renderedLines); index++ {
		var have, want string
		if index < len(committedLines) {
			have = committedLines[index]
		}
		if index < len(renderedLines) {
			want = renderedLines[index]
		}
		if have == want {
			continue
		}
		differing++
		if first < 0 {
			first = index + 1
		}
	}

	return fmt.Sprintf("%d line(s) differ, first at line %d: committed %q, rendered %q",
		differing, first, lineAt(committedLines, first), lineAt(renderedLines, first))
}

// lineAt reads a 1-based line, or "" past the end.
func lineAt(lines []string, number int) string {
	if number < 1 || number > len(lines) {
		return ""
	}

	return lines[number-1]
}

// repoRoot is the repository root, from this file's own location: the command
// runs from anywhere, including a Makefile target invoked from a subdirectory.
func repoRoot() (string, error) {
	_, file, _, ok := runtime.Caller(0)
	if !ok {
		return "", fmt.Errorf("cannot locate the generator")
	}
	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("cannot find the repository root from %s: %w", file, err)
	}

	return root, nil
}
