// Command skills-command-reference renders the router skill's compact command
// reference from the binary's own command tree, so the surface an agent reads in
// turn one cannot describe a verb the binary does not implement.
//
// Usage:
//
//	go run ./scripts/skills-command-reference          # regenerate the section
//	go run ./scripts/skills-command-reference --check  # fail if it is stale
//
// The tree comes from `kilasflow help --json` — the document the binary hands an
// agent — because the command registry is unexported and a second copy of it
// here would be exactly the drift this section exists to prevent. The section is
// delimited by two markers in SKILL.md, and the generator refuses to run when
// they are absent or ambiguous rather than guessing where the section ends.
//
// This command lives in its own directory for the same reason scripts/skills-index
// does: scripts/config-reference.go already declares func main in package main,
// and a second one in the same directory is a redeclaration go vet refuses.
package main

import (
	"bytes"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"sort"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

const (
	// skillName is the router's directory, and skillPath the file the generated
	// section lives in.
	skillName = "using-kilasflow-skills"
	skillPath = "skills/" + skillName + "/SKILL.md"
	// beginMarker and endMarker delimit the generated section. They are HTML
	// comments so a markdown renderer drops them and a reader of the source sees
	// what to run.
	beginMarker = "<!-- generated:command-reference -->"
	endMarker   = "<!-- /generated:command-reference -->"
	// generator names the command in the generated header, so a reader who finds
	// the section stale knows what to run without looking it up.
	generator = "scripts/skills-command-reference"
)

func main() {
	check := flag.Bool("check", false, "compare the rendered section against the committed file and fail on any difference")
	flag.Parse()

	if err := run(*check); err != nil {
		fmt.Fprintf(os.Stderr, "skills-command-reference: %v\n", err)
		os.Exit(1)
	}
}

func run(check bool) error {
	root, err := repoRoot()
	if err != nil {
		return err
	}

	tree, err := commandTree(root)
	if err != nil {
		return err
	}

	path := filepath.Join(root, filepath.FromSlash(skillPath))
	committed, err := os.ReadFile(path)
	if err != nil {
		return fmt.Errorf("read %s: %w", skillPath, err)
	}

	updated, err := splice(string(committed), render(tree))
	if err != nil {
		return fmt.Errorf("%s: %w", skillPath, err)
	}

	// The rendered document is checked before it is written or compared: a
	// section the bundle's own rules refuse is a bug in the generator or in the
	// tree, and writing it would move the failure into someone else's build.
	if err := validate(updated, tree); err != nil {
		return err
	}

	switch {
	case string(committed) == updated:
		fmt.Printf("%s: %d verbs, up to date\n", skillPath, len(tree))

		return nil
	case check:
		return fmt.Errorf("%s is stale: %s\nrun: make generate-skills-command-reference",
			skillPath, staleSummary(string(committed), updated))
	}
	if err := os.WriteFile(path, []byte(updated), 0o644); err != nil {
		return fmt.Errorf("write %s: %w", skillPath, err)
	}
	fmt.Printf("%s: %d verbs, section written\n", skillPath, len(tree))

	return nil
}

// verb is one command in the tree, as `kilasflow help --json` reports it.
type verb struct {
	Path      string `json:"path"`
	Summary   string `json:"summary"`
	Operation string `json:"operation"`
	Guarded   bool   `json:"guarded"`
}

// treeEnvelope is the JSON envelope the CLI prints, decoded for one field: a
// failing help is reported with the CLI's own message rather than as a decode
// error about an empty document.
type treeEnvelope struct {
	OK    bool   `json:"ok"`
	Data  []verb `json:"data"`
	Error struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// commandTree reads the command tree from the binary built out of this tree.
//
// The flags follow the verb (`help --json`) because that is where the CLI's
// flag parser looks; stdout is a pipe, not a terminal, so the envelope is JSON
// either way, and --json makes it JSON when a human runs the generator by hand.
func commandTree(root string) ([]verb, error) {
	command := exec.Command("go", "run", "./cmd/kilasflow", "help", "--json")
	command.Dir = root

	var stdout, stderr bytes.Buffer
	command.Stdout = &stdout
	command.Stderr = &stderr
	if err := command.Run(); err != nil {
		return nil, fmt.Errorf("go run ./cmd/kilasflow help --json: %w: %s", err, strings.TrimSpace(stderr.String()))
	}

	var decoded treeEnvelope
	if err := json.Unmarshal(stdout.Bytes(), &decoded); err != nil {
		return nil, fmt.Errorf("decode the command tree: %w", err)
	}
	if !decoded.OK || len(decoded.Data) == 0 {
		return nil, fmt.Errorf("the command tree is empty (%s): %s", decoded.Error.Code, decoded.Error.Message)
	}

	return decoded.Data, nil
}

// render draws the generated section: one line per verb, the command first, the
// operation it drives second, and the verb's own summary third. Nothing here is
// typed — a summary is the tree's own sentence, and a verb with no operation
// says local because that is what the tree says.
func render(tree []verb) string {
	commands := make([]string, len(tree))
	operations := make([]string, len(tree))
	summaries := make([]string, len(tree))
	commandWidth, operationWidth := 0, 0
	for index, entry := range tree {
		commands[index] = "kilasflow " + entry.Path
		operations[index] = entry.Operation
		if operations[index] == "" {
			operations[index] = "local"
		}
		if entry.Guarded {
			operations[index] += " [guarded]"
		}
		// A summary may carry markdown of its own (backticked flags); inside a
		// fence that is punctuation, so it is stripped rather than rendered.
		summaries[index] = strings.ReplaceAll(entry.Summary, "`", "")
		commandWidth = max(commandWidth, len(commands[index]))
		operationWidth = max(operationWidth, len(operations[index]))
	}

	lines := make([]string, 0, len(tree)+5)
	lines = append(lines,
		beginMarker,
		"```text",
		"# every verb this binary implements, and the operation it drives.",
		"# generated from `kilasflow help --json` by "+generator+";",
		"# run `make generate-skills-command-reference` after the command tree changes.",
	)
	for index := range tree {
		lines = append(lines, fmt.Sprintf("%-*s  %-*s  %s",
			commandWidth, commands[index], operationWidth, operations[index], summaries[index]))
	}
	lines = append(lines, "```", endMarker)

	return strings.Join(lines, "\n")
}

// splice replaces the lines between the two markers, markers included.
//
// A missing marker is an error rather than an append: a document whose section
// silently moved to the end is a document nobody re-reads, and the second copy
// of a marker makes "which section" ambiguous.
func splice(document, section string) (string, error) {
	lines := strings.Split(document, "\n")
	begin, end := -1, -1
	for index, line := range lines {
		switch strings.TrimSpace(line) {
		case beginMarker:
			if begin >= 0 {
				return "", fmt.Errorf("the document carries %s twice", beginMarker)
			}
			begin = index
		case endMarker:
			if end >= 0 {
				return "", fmt.Errorf("the document carries %s twice", endMarker)
			}
			end = index
		}
	}
	switch {
	case begin < 0:
		return "", fmt.Errorf("the document carries no %s marker", beginMarker)
	case end < 0:
		return "", fmt.Errorf("the document carries no %s marker", endMarker)
	case end < begin:
		return "", fmt.Errorf("%s precedes %s", endMarker, beginMarker)
	}

	spliced := make([]string, 0, len(lines))
	spliced = append(spliced, lines[:begin]...)
	spliced = append(spliced, strings.Split(section, "\n")...)
	spliced = append(spliced, lines[end+1:]...)

	// A blank line separates the markers from the surrounding prose, so the
	// fenced block is not read as part of the paragraph above it.
	spliced = collapseBlankLines(spliced)

	return strings.Join(spliced, "\n"), nil
}

// collapseBlankLines collapses runs of blank lines into one, which is what the
// splice introduces when the section it replaces already ended in one.
func collapseBlankLines(lines []string) []string {
	expanded := make([]string, 0, len(lines))
	for index, line := range lines {
		if index > 0 && strings.TrimSpace(line) == "" && strings.TrimSpace(lines[index-1]) == "" {
			continue
		}
		expanded = append(expanded, line)
	}

	return expanded
}

// validate refuses a rendered document the bundle's own rules would refuse, and
// a document that declares a command the binary does not implement.
//
// It is the reason the generated half can be trusted: the rules are re-applied
// after the section is replaced, so a body the splice broke fails here, and the
// frontmatter's commands are resolved against the same tree the section was
// rendered from, so a verb that leaves the tree fails here instead of writing a
// reference and a declaration that teach it anyway. The operations are not
// resolved here: an operation id is a product fact the spec owns, and the drift
// gate (design §5.7 G2) is the surface that owns comparing them with
// internal/api/handlers.
func validate(document string, tree []verb) error {
	parsed, err := skills.Parse(skillName, []byte(document))
	if err != nil {
		return fmt.Errorf("the rendered %s does not load: %w", skillName, err)
	}

	findings := skills.Check([]skills.Skill{parsed})
	if len(findings) > 0 {
		lines := make([]string, 0, len(findings))
		for _, finding := range findings {
			lines = append(lines, fmt.Sprintf("  %s [%s] %s", finding.Skill, finding.Rule, finding.Message))
		}
		sort.Strings(lines)

		return fmt.Errorf("the rendered section breaks %d of the bundle's rules:\n%s", len(findings), strings.Join(lines, "\n"))
	}

	return resolveDeclarations(parsed, tree)
}

// resolveDeclarations refuses a command the frontmatter declares and the binary
// does not implement.
//
// A declaration is what the index advertises to a harness, so it is the claim
// that has to be true; the tree is the authority, and the wording of the verb's
// own summary is not this command's business.
func resolveDeclarations(declared skills.Skill, tree []verb) error {
	unresolved := make([]string, 0)
	for _, command := range declared.Commands {
		path := strings.TrimPrefix(command, "kilasflow ")
		if !resolves(tree, path) {
			unresolved = append(unresolved, command)
		}
	}
	if len(unresolved) == 0 {
		return nil
	}
	sort.Strings(unresolved)

	return fmt.Errorf("%s declares %d command(s) the binary does not implement: %s",
		skillName, len(unresolved), strings.Join(unresolved, ", "))
}

// resolves reports whether a declaration names a verb the binary implements.
//
// The match is exact because a declaration is an invocation with no arguments: a
// declaration may name `workflow get`, and it may not name `workflow`, which is
// a prefix the CLI itself would refuse as an unknown command. Placeholders and
// arguments belong in the prose that teaches the verb, not in the declaration
// the index advertises.
func resolves(tree []verb, path string) bool {
	for _, entry := range tree {
		if entry.Path == path {
			return true
		}
	}

	return false
}

// staleSummary is the short diff a stale section deserves: where it first
// diverges and how many lines do, rather than the whole tree twice.
func staleSummary(committed, rendered string) string {
	committedLines := strings.Split(committed, "\n")
	renderedLines := strings.Split(rendered, "\n")

	first, differing := -1, 0
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
		return "", fmt.Errorf("locate %s", generator)
	}

	root := filepath.Dir(filepath.Dir(filepath.Dir(file)))
	if _, err := os.Stat(filepath.Join(root, "go.mod")); err != nil {
		return "", fmt.Errorf("locate the repository root above %s: %w", generator, err)
	}

	return root, nil
}
