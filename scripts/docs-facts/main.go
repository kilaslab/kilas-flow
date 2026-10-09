// Command docs-facts writes the facts a docs page states about the product from
// the product, so a count or a table the prose quotes cannot drift from the
// binary it describes.
//
// Usage:
//
//	go run ./scripts/docs-facts          # regenerate the marked sections
//	go run ./scripts/docs-facts --check  # fail if one is stale
//
// A fact lives between two HTML-comment markers on a page that is otherwise
// hand-written:
//
//	<!-- generated:catalogue-size -->
//	...written by this command...
//	<!-- /generated:catalogue-size -->
//
// Everything outside the markers is left exactly as it is, and the command
// refuses a page whose markers are absent or ambiguous rather than guessing
// where a section ends. The facts come from the Go packages that own them
// (internal/shippedcatalogue for the node catalogue, internal/cli for the exit
// codes), not from a copy of them here.
//
// This command lives in its own directory for the same reason
// scripts/skills-index does: scripts/config-reference.go already declares
// func main in package main, and a second one in the same directory is a
// redeclaration go vet refuses.
package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

const (
	catalogueBegin = "<!-- generated:catalogue-size -->"
	catalogueEnd   = "<!-- /generated:catalogue-size -->"

	// regenerate is what a stale page is told to run.
	regenerate = "make generate-docs-facts"
)

// fact is one generated section of one page.
type fact struct {
	// name says what the section states, for messages.
	name string
	// path is the page, slash-separated from the repository root.
	path string
	// begin and end are the marker lines that delimit the section.
	begin, end string
	// body renders what lies between the markers.
	body func() (string, error)
}

func main() {
	check := flag.Bool("check", false, "compare the rendered facts against the committed pages and fail on any difference")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "docs-facts: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := splice.RepoRoot()
	if err != nil {
		return err
	}
	list, err := facts()
	if err != nil {
		return err
	}

	return apply(root, list, check, os.Stdout)
}

// facts is every section this command owns.
func facts() ([]fact, error) {
	registry := node.NewRegistry()
	if err := shippedcatalogue.Register(registry); err != nil {
		return nil, fmt.Errorf("assemble the shipped catalogue: %w", err)
	}
	sentence := shippedcatalogue.RenderSentence(shippedcatalogue.Count(registry))
	size := func() (string, error) { return sentence, nil }

	return []fact{
		{name: "catalogue size", path: "docs/src/content/docs/concepts/node-registry.md",
			begin: catalogueBegin, end: catalogueEnd, body: size},
		{name: "catalogue size", path: "docs/src/content/docs/start/what-kilasflow-is.md",
			begin: catalogueBegin, end: catalogueEnd, body: size},
	}, nil
}

// apply renders every fact into its page under root. With check it writes
// nothing and fails, naming each stale page and where it first differs; without
// it, it rewrites the pages that differ.
//
// Every page is visited before any failure is returned, so one run reports all
// of the drift rather than the first page's.
func apply(root string, list []fact, check bool, out io.Writer) error {
	// A page can hold several facts, so they are applied in sequence to one
	// text and written once.
	order := make([]string, 0, len(list))
	texts := make(map[string]string, len(list))
	committed := make(map[string]string, len(list))
	problems := make([]string, 0)

	for _, entry := range list {
		current, loaded := texts[entry.path]
		if !loaded {
			raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(entry.path)))
			if err != nil {
				return fmt.Errorf("read %s: %w", entry.path, err)
			}
			current = string(raw)
			committed[entry.path] = current
			order = append(order, entry.path)
		}
		body, err := entry.body()
		if err != nil {
			return fmt.Errorf("render the %s for %s: %w", entry.name, entry.path, err)
		}
		updated, err := splice.Fill(current, entry.begin, entry.end, body)
		if err != nil {
			return fmt.Errorf("%s: %w", entry.path, err)
		}
		texts[entry.path] = updated
	}

	for _, path := range order {
		if texts[path] == committed[path] {
			fmt.Fprintf(out, "%s: up to date\n", path)

			continue
		}
		if check {
			problems = append(problems, fmt.Sprintf("%s is stale: %s", path, splice.StaleSummary(committed[path], texts[path])))

			continue
		}
		if err := os.WriteFile(filepath.Join(root, filepath.FromSlash(path)), []byte(texts[path]), 0o644); err != nil {
			return fmt.Errorf("write %s: %w", path, err)
		}
		fmt.Fprintf(out, "%s: written\n", path)
	}
	if len(problems) > 0 {
		return fmt.Errorf("%s\nrun: %s", strings.Join(problems, "\n"), regenerate)
	}

	return nil
}
