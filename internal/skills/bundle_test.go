package skills

import (
	"os"
	"path/filepath"
	"runtime"
	"sort"
	"strings"
	"testing"
)

// testRepoRoot locates the repository root from this file, the same way
// scripts/config-reference_test.go does: the package is internal/skills, so the
// root is two directories up.
func testRepoRoot(t *testing.T) string {
	t.Helper()

	_, file, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("cannot locate the test file")
	}

	return filepath.Dir(filepath.Dir(filepath.Dir(file)))
}

// testBundle loads skills/ from the repository root.
func testBundle(t *testing.T) []Skill {
	t.Helper()

	loaded, err := LoadDir(filepath.Join(testRepoRoot(t), "skills"))
	if err != nil {
		t.Fatalf("load the bundle: %v", err)
	}

	return loaded
}

// The frontmatter contract of design §5.2 is exactly these eight keys. The
// literal set is asserted against the parser's own key list so a key cannot be
// dropped from the parser without this failing.
func TestFrontmatterKeysAreTheDesignSet(t *testing.T) {
	want := []string{
		"name",
		"description",
		"kilasflow_skills_version",
		"kilasflow_commands",
		"kilasflow_operations",
		"kilasflow_nodes",
		"kilasflow_expression_roots",
		"kilasflow_not_shipped",
	}

	got := append([]string(nil), frontmatterKeys...)
	sort.Strings(got)
	sorted := append([]string(nil), want...)
	sort.Strings(sorted)

	if strings.Join(got, ",") != strings.Join(sorted, ",") {
		t.Fatalf("frontmatterKeys = %v, want %v", got, sorted)
	}

	// Every key is required: the parser must refuse a document that omits one,
	// and one it does not know.
	for _, key := range want {
		document := validFrontmatter(t)
		document = removeKey(document, key)
		if _, err := Parse("kilasflow-fixture", []byte(document)); err == nil {
			t.Errorf("Parse accepted frontmatter without %s", key)
		} else if !strings.Contains(err.Error(), key) {
			t.Errorf("Parse error for a missing %s = %q, want it to name the key", key, err)
		}
	}

	unknown := strings.Replace(validFrontmatter(t), "\n---\n", "\nkilasflow_unknown: []\n---\n", 1)
	if _, err := Parse("kilasflow-fixture", []byte(unknown)); err == nil {
		t.Fatal("Parse accepted an unknown frontmatter key")
	} else if !strings.Contains(err.Error(), "kilasflow_unknown") {
		t.Errorf("unknown-key error = %q, want it to name the key", err)
	}
}

// Every skill in the bundle loads, is named after its directory, carries the
// bundle's stamp, and describes itself the way the router's index relies on.
func TestFrontmatterContract(t *testing.T) {
	loaded := testBundle(t)
	if len(loaded) == 0 {
		t.Fatal("the bundle holds no skills")
	}

	for _, skill := range loaded {
		if skill.Name == "" {
			t.Errorf("%s has no name", skill.Path)
		}
		if want := filepath.Base(filepath.Dir(skill.Path)); skill.Name != want {
			t.Errorf("skill %q lives in %s, want the directory %q", skill.Name, skill.Path, want)
		}
		if skill.SkillsVersion != SkillsVersion {
			t.Errorf("%s declares kilasflow_skills_version %d, want %d", skill.Path, skill.SkillsVersion, SkillsVersion)
		}

		lower := strings.ToLower(skill.Description)
		if !strings.HasPrefix(lower, "use when") {
			t.Errorf("%s description does not start with 'Use when': %q", skill.Path, skill.Description)
		}
		if !strings.Contains(lower, "triggers on") {
			t.Errorf("%s description has no 'Triggers on' trigger list: %q", skill.Path, skill.Description)
		}
		if skill.Body == "" {
			t.Errorf("%s has an empty body", skill.Path)
		}
	}

	// References come from the directory listing, sorted, and every declared
	// file exists — the half the checker cannot state about the disk.
	for _, skill := range loaded {
		if !sort.StringsAreSorted(skill.References) {
			t.Errorf("%s references are not sorted: %v", skill.Path, skill.References)
		}
		for _, reference := range skill.References {
			path := filepath.Join(filepath.Dir(skill.Path), reference)
			if _, err := os.Stat(filepath.Join(testRepoRoot(t), path)); err != nil {
				t.Errorf("%s declares %s: %v", skill.Path, reference, err)
			}
		}
	}
}

// The bundle must pass its own checker: whitespace-tolerant fixtures prove the
// rules fire, and this proves the shipped content is not a violation.
func TestBundleChecksPass(t *testing.T) {
	if findings := Check(testBundle(t)); len(findings) != 0 {
		for _, finding := range findings {
			t.Errorf("%s [%s] %s", finding.Skill, finding.Rule, finding.Message)
		}
	}
}

// §5.8 made mechanical: a skill declaring a capability as not shipped must say
// so in the body, naming the entry verbatim, or the claim is invisible to the
// agent that loads it.
func TestNotShippedSectionsNameEveryEntry(t *testing.T) {
	for _, skill := range testBundle(t) {
		section, found := sectionOf(skill.Body, "Not shipped yet")
		if len(skill.NotShipped) == 0 {
			if found {
				t.Errorf("%s has a '## Not shipped yet' section with nothing to deny", skill.Path)
			}

			continue
		}
		if !found {
			t.Errorf("%s declares %d not-shipped entries with no '## Not shipped yet' section", skill.Path, len(skill.NotShipped))

			continue
		}
		for _, entry := range skill.NotShipped {
			if !strings.Contains(section, entry) {
				t.Errorf("%s does not name %q inside '## Not shipped yet'", skill.Path, entry)
			}
		}
	}
}

// No skill may teach a secret into a document, an argument or chat, and the
// credentials skill must carry the rule itself — deleting the sentence has to
// fail a test, not just drop a sentence.
func TestNoSecretInstructions(t *testing.T) {
	for _, skill := range testBundle(t) {
		if skill.Name != credentialsSkill {
			continue
		}

		nonNegotiables, found := sectionOf(skill.Body, "Non-negotiables")
		if !found {
			t.Fatalf("%s has no '## Non-negotiables' section", skill.Path)
		}
		for _, phrase := range credentialsRulePhrases {
			if !strings.Contains(strings.ToLower(nonNegotiables), phrase) {
				t.Errorf("%s does not state %q in '## Non-negotiables'", skill.Path, phrase)
			}
		}
	}
}

// The committed index is the renderer's output, byte for byte: hand-editing it
// fails here rather than reaching a harness.
func TestIndexIsFresh(t *testing.T) {
	loaded := testBundle(t)
	rendered, err := Index(loaded)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}

	committed, err := os.ReadFile(filepath.Join(testRepoRoot(t), "skills", "index.json"))
	if err != nil {
		t.Fatalf("read skills/index.json: %v", err)
	}
	if string(committed) != string(rendered) {
		t.Fatalf("skills/index.json is stale: regenerate it with `make generate-skills-index`\n--- committed\n%s\n--- rendered\n%s", committed, rendered)
	}
}

// The freshness test above is only meaningful if the renderer is stable: map
// iteration order or a timestamp inside the document would make it flap.
func TestIndexIsDeterministic(t *testing.T) {
	loaded := testBundle(t)
	first, err := Index(loaded)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	second, err := Index(loaded)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if string(first) != string(second) {
		t.Fatalf("Index rendered differently twice:\n%s\n---\n%s", first, second)
	}

	// The order of the input must not matter either.
	reversed := make([]Skill, len(loaded))
	for index, skill := range loaded {
		reversed[len(loaded)-1-index] = skill
	}
	third, err := Index(reversed)
	if err != nil {
		t.Fatalf("Index: %v", err)
	}
	if string(first) != string(third) {
		t.Fatalf("Index depends on the input order:\n%s\n---\n%s", first, third)
	}
}

// validFrontmatter is a document satisfying all eight keys, for tests that
// perturb one of them.
func validFrontmatter(t *testing.T) string {
	t.Helper()

	return "---\n" +
		"name: kilasflow-fixture\n" +
		"description: Use when a fixture is exercised. Triggers on \"fixture\".\n" +
		"kilasflow_skills_version: 1\n" +
		"kilasflow_commands:\n  - kilasflow run\n" +
		"kilasflow_operations:\n  - run-workflow\n" +
		"kilasflow_nodes: []\n" +
		"kilasflow_expression_roots: []\n" +
		"kilasflow_not_shipped: []\n" +
		"---\n" +
		"## Non-negotiables\n"
}

// removeKey drops one scalar or block key from a frontmatter document.
func removeKey(document, key string) string {
	lines := strings.Split(document, "\n")
	kept := make([]string, 0, len(lines))
	dropping := false
	for _, line := range lines {
		if strings.HasPrefix(line, key+":") {
			dropping = true

			continue
		}
		if dropping {
			// Keep dropping the block-style continuation of the key.
			if strings.HasPrefix(line, "  - ") || strings.HasPrefix(line, "  ") {
				continue
			}
			dropping = false
		}
		kept = append(kept, line)
	}

	return strings.Join(kept, "\n")
}
