// Package skills reads the agent skills bundle at skills/ in the repository
// root: the frontmatter contract of design §5.2, the body conventions of §5.3,
// and the generated index a harness reads without parsing markdown.
//
// The bundle is prose, so what can be checked is checked: Parse refuses a
// frontmatter that is unknown, incomplete or misspelled, Check walks the rules
// the design states about the body, and Index renders skills/index.json so a
// harness reads one file instead of every SKILL.md.
//
// Nothing here resolves a CLI verb, an operation id, a node type or an
// expression root: those are product facts, and they are gated against the
// product by the drift gates rather than restated in this package.
package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/knadh/koanf/parsers/yaml"
)

// SkillsVersion is the stamp every SKILL.md declares as
// kilasflow_skills_version.
//
// A harness compares what it installed against this number, so it has exactly
// one definition and this is it: a second copy inside an install or a check
// verb would let the two drift apart, and drift reporting would stop working
// without anything failing.
const SkillsVersion = 1

// frontmatterKeys is every key the frontmatter contract allows, in the order
// design §5.2 lists them.
//
// Parse decodes against this list and nothing else. An unknown key is a typo
// that a lenient parser would silently ignore — leaving the skill declaring
// less than its author meant — and every key here is required, because a
// missing declaration is a capability nobody can check.
var frontmatterKeys = []string{
	"name",
	"description",
	"kilasflow_skills_version",
	"kilasflow_commands",
	"kilasflow_operations",
	"kilasflow_nodes",
	"kilasflow_expression_roots",
	"kilasflow_not_shipped",
}

// Skill is one parsed SKILL.md and the reference files beside it.
type Skill struct {
	Name        string
	Description string
	// SkillsVersion is the value the file declares; Parse refuses anything but
	// SkillsVersion, so a loaded bundle always carries the stamp.
	SkillsVersion int
	// Path is repository-relative: skills/<name>/SKILL.md.
	Path string
	// Commands, Operations, Nodes and ExpressionRoots are what the skill
	// declares it teaches. The product is the authority on whether each one
	// exists; the declarations rule is the authority on whether the body
	// actually teaches it.
	Commands        []string
	Operations      []string
	Nodes           []string
	ExpressionRoots []string
	// NotShipped is what the skill must deny; §5.8 makes it a body section.
	NotShipped []string
	// References are repository-relative: references/FILTERS.md, in name order,
	// taken from the directory listing rather than from the frontmatter, so a
	// file that exists but is undeclared is still visible to the checker.
	References []string
	// Body is everything after the closing frontmatter delimiter.
	Body string
	// ReferenceText is each declared reference's text, keyed by the same
	// repository-relative path References holds.
	//
	// It is here rather than loaded by the checker so Check stays pure: a
	// secret-hygiene violation in a reference file is exactly as wrong as one
	// in a body, and the only way to see it without giving Check a filesystem
	// is to carry the text through the load.
	ReferenceText map[string]string
}

// Parse reads one SKILL.md.
//
// name is the directory the file lives in: the frontmatter's name must equal
// it, which is what keeps `skills/<name>/` and the index entry from drifting.
func Parse(name string, data []byte) (Skill, error) {
	path := bundlePath(name)

	frontmatter, body, err := SplitFrontmatter(data)
	if err != nil {
		return Skill{}, fmt.Errorf("%s: %w", path, err)
	}

	decoded, err := yaml.Parser().Unmarshal(frontmatter)
	if err != nil {
		return Skill{}, fmt.Errorf("%s: parse the frontmatter: %w", path, err)
	}

	known := make(map[string]bool, len(frontmatterKeys))
	for _, key := range frontmatterKeys {
		known[key] = true
	}
	for key := range decoded {
		if !known[key] {
			return Skill{}, fmt.Errorf("%s: frontmatter declares an unknown key %q", path, key)
		}
	}
	for _, key := range frontmatterKeys {
		if _, found := decoded[key]; !found {
			return Skill{}, fmt.Errorf("%s: frontmatter is missing %q", path, key)
		}
	}

	declaredName, err := stringField(path, decoded, "name")
	if err != nil {
		return Skill{}, err
	}
	if declaredName != name {
		return Skill{}, fmt.Errorf("%s: frontmatter name %q does not match its directory %q", path, declaredName, name)
	}

	description, err := stringField(path, decoded, "description")
	if err != nil {
		return Skill{}, err
	}
	if err := validateDescription(path, description); err != nil {
		return Skill{}, err
	}

	version, err := intField(path, decoded, "kilasflow_skills_version")
	if err != nil {
		return Skill{}, err
	}
	if version != SkillsVersion {
		return Skill{}, fmt.Errorf("%s: kilasflow_skills_version is %d, want %d", path, version, SkillsVersion)
	}

	commands, err := stringListField(path, decoded, "kilasflow_commands")
	if err != nil {
		return Skill{}, err
	}
	operations, err := stringListField(path, decoded, "kilasflow_operations")
	if err != nil {
		return Skill{}, err
	}
	nodes, err := stringListField(path, decoded, "kilasflow_nodes")
	if err != nil {
		return Skill{}, err
	}
	expressionRoots, err := stringListField(path, decoded, "kilasflow_expression_roots")
	if err != nil {
		return Skill{}, err
	}
	notShipped, err := stringListField(path, decoded, "kilasflow_not_shipped")
	if err != nil {
		return Skill{}, err
	}

	return Skill{
		Name:            declaredName,
		Description:     description,
		SkillsVersion:   version,
		Path:            path,
		Commands:        commands,
		Operations:      operations,
		Nodes:           nodes,
		ExpressionRoots: expressionRoots,
		NotShipped:      notShipped,
		Body:            body,
	}, nil
}

// LoadDir reads every skill under root — the bundle directory, skills/ — in
// name order.
//
// Every subdirectory must hold a SKILL.md: a directory without one is a skill
// that silently does not exist, and the index would be missing it without
// anything failing.
func LoadDir(root string) ([]Skill, error) {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil, fmt.Errorf("read the bundle directory %s: %w", root, err)
	}

	loaded := make([]Skill, 0, len(entries))
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}

		name := entry.Name()
		data, err := os.ReadFile(filepath.Join(root, name, "SKILL.md"))
		if err != nil {
			return nil, fmt.Errorf("%s: %w", bundlePath(name), err)
		}
		skill, err := Parse(name, data)
		if err != nil {
			return nil, err
		}

		references, text, err := loadReferences(filepath.Join(root, name, "references"))
		if err != nil {
			return nil, err
		}
		skill.References = references
		skill.ReferenceText = text

		loaded = append(loaded, skill)
	}
	if len(loaded) == 0 {
		return nil, fmt.Errorf("no skill directories under %s", root)
	}

	sort.Slice(loaded, func(left, right int) bool { return loaded[left].Name < loaded[right].Name })

	return loaded, nil
}

// SplitFrontmatter separates the leading YAML block from the body.
//
// The block is delimited by a leading "---\n" and a closing "\n---\n", so a
// file whose first line is prose is reported as a missing block rather than
// parsed as an empty frontmatter and accepted with every key missing.
func SplitFrontmatter(data []byte) ([]byte, string, error) {
	const opening = "---\n"
	const closing = "\n---\n"

	if !strings.HasPrefix(string(data), opening) {
		return nil, "", fmt.Errorf("frontmatter block is missing: the file must start with %q", "---")
	}
	rest := string(data[len(opening):])
	end := strings.Index(rest, closing)
	if end < 0 {
		return nil, "", fmt.Errorf("frontmatter block is not closed with %q", "---")
	}

	return []byte(rest[:end]), rest[end+len(closing):], nil
}

// bundlePath is a skill's path inside the repository, where the index and the
// errors name it.
func bundlePath(name string) string {
	return filepath.ToSlash(filepath.Join("skills", name, "SKILL.md"))
}

// loadReferences lists references/*.md beside one skill, in name order, and
// reads them.
//
// A file in references/ that is not markdown is refused rather than ignored:
// the references rule compares the body's table against what is on disk, and a
// file the loader skips would make that comparison quietly wrong.
func loadReferences(dir string) ([]string, map[string]string, error) {
	entries, err := os.ReadDir(dir)
	if os.IsNotExist(err) {
		return nil, nil, nil
	}
	if err != nil {
		return nil, nil, fmt.Errorf("read %s: %w", dir, err)
	}

	references := make([]string, 0, len(entries))
	text := make(map[string]string, len(entries))
	for _, entry := range entries {
		if entry.IsDir() {
			return nil, nil, fmt.Errorf("%s is a directory; reference files are markdown files beside SKILL.md", filepath.Join(dir, entry.Name()))
		}
		if !strings.HasSuffix(entry.Name(), ".md") {
			return nil, nil, fmt.Errorf("%s is not a markdown reference file; reference files end in .md", filepath.Join(dir, entry.Name()))
		}

		name := filepath.ToSlash(filepath.Join("references", entry.Name()))
		contents, err := os.ReadFile(filepath.Join(dir, entry.Name()))
		if err != nil {
			return nil, nil, fmt.Errorf("read %s: %w", name, err)
		}
		references = append(references, name)
		text[name] = string(contents)
	}

	sort.Strings(references)

	return references, text, nil
}

// stringField reads a required string.
func stringField(path string, decoded map[string]any, key string) (string, error) {
	raw, found := decoded[key]
	if !found {
		return "", fmt.Errorf("%s: frontmatter is missing %q", path, key)
	}

	value, ok := raw.(string)
	if !ok {
		return "", fmt.Errorf("%s: %s must be a string, got %T", path, key, raw)
	}
	if strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("%s: %s must not be empty", path, key)
	}

	return value, nil
}

// intField reads a required integer.
func intField(path string, decoded map[string]any, key string) (int, error) {
	raw, found := decoded[key]
	if !found {
		return 0, fmt.Errorf("%s: frontmatter is missing %q", path, key)
	}

	value, ok := raw.(int)
	if !ok {
		return 0, fmt.Errorf("%s: %s must be an integer, got %T", path, key, raw)
	}

	return value, nil
}

// stringListField reads a required list of non-empty strings.
//
// An empty list must be written as [] rather than left out: the key is part of
// the contract whether or not the skill declares anything, so the declaration
// is a decision someone made rather than a key nobody wrote.
func stringListField(path string, decoded map[string]any, key string) ([]string, error) {
	raw, found := decoded[key]
	if !found {
		return nil, fmt.Errorf("%s: frontmatter is missing %q", path, key)
	}

	items, ok := raw.([]any)
	if !ok {
		return nil, fmt.Errorf("%s: %s must be a list, got %T", path, key, raw)
	}

	values := make([]string, 0, len(items))
	for index, item := range items {
		value, ok := item.(string)
		if !ok {
			return nil, fmt.Errorf("%s: %s[%d] must be a string, got %T (quote an entry that contains ': ')", path, key, index, item)
		}
		if strings.TrimSpace(value) == "" {
			return nil, fmt.Errorf("%s: %s[%d] must not be empty", path, key, index)
		}

		values = append(values, value)
	}

	return values, nil
}

// validateDescription enforces the §5.2 description convention: it begins with
// the situation the skill is for and names the words that trigger it, which is
// what lets a router pick a skill from the index alone.
func validateDescription(path, description string) error {
	lower := strings.ToLower(description)
	if !strings.HasPrefix(lower, "use when") {
		return fmt.Errorf("%s: description must start with \"Use when\", got %q", path, description)
	}
	if !strings.Contains(lower, "triggers on") {
		return fmt.Errorf("%s: description must name its triggers with \"Triggers on\", got %q", path, description)
	}

	return nil
}
