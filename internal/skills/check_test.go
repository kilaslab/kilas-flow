package skills

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// plantedCase is one fixture bundle under testdata/planted: a bundle that
// violates exactly one rule.
//
// A violation in the frontmatter cannot become a Check finding, because the
// bundle never loads that far — so those cases carry the substring their
// loader error must name, and the content cases carry the Rule their findings
// must raise.
type plantedCase struct {
	dir string
	// parseError is a substring LoadDir's error must carry.
	parseError string
	// rule is the one Rule Check may report for this bundle.
	rule string
}

// The fixtures are one per rule, and every directory under testdata/planted is
// asserted to be listed here, so a fixture cannot be added and silently
// skipped.
var plantedCases = []plantedCase{
	{dir: "unknown-key", parseError: "kilasflow_bogus"},
	{dir: "missing-key", parseError: "kilasflow_not_shipped"},
	{dir: "wrong-version", parseError: "kilasflow_skills_version"},
	{dir: "name-mismatch", parseError: "kilasflow-fixture"},
	{dir: "bad-description", parseError: "Use when"},
	{dir: "bad-description-triggers", parseError: "Triggers on"},
	{dir: "section-order", rule: RuleSectionOrder},
	{dir: "not-shipped-missing-section", rule: RuleNotShipped},
	{dir: "not-shipped-unnamed-entry", rule: RuleNotShipped},
	{dir: "undeclared-reference", rule: RuleReferences},
	{dir: "missing-reference-file", rule: RuleReferences},
	{dir: "declaration-absent-from-body", rule: RuleDeclarations},
	{dir: "literal-token-in-fence", rule: RuleSecretHygiene},
	{dir: "literal-token-in-prose", rule: RuleSecretHygiene},
	{dir: "literal-token-in-reference", rule: RuleSecretHygiene},
	{dir: "secret-rule-missing", rule: RuleSecretHygiene},
	{dir: "unshipped-command-in-fence", rule: RuleNotShippedCommands},
}

// Each fixture must be caught by the rule it was planted for, and by no other:
// a checker that reports everything on every bundle would satisfy "the rule
// fires" and be useless.
func TestCheckRejectsPlantedViolations(t *testing.T) {
	for _, tc := range plantedCases {
		t.Run(tc.dir, func(t *testing.T) {
			bundle := filepath.Join("testdata", "planted", tc.dir, "skills")
			loaded, err := LoadDir(bundle)
			if tc.parseError != "" {
				if err == nil {
					t.Fatalf("LoadDir(%s) accepted a bundle that violates a frontmatter rule", bundle)
				}
				if !strings.Contains(err.Error(), tc.parseError) {
					t.Fatalf("LoadDir(%s) error = %q, want it to name %q", bundle, err, tc.parseError)
				}

				return
			}
			if err != nil {
				t.Fatalf("LoadDir(%s): %v", bundle, err)
			}

			findings := Check(loaded)
			if len(findings) == 0 {
				t.Fatalf("Check(%s) reported nothing for a planted %s violation", bundle, tc.rule)
			}

			rules := map[string][]string{}
			for _, finding := range findings {
				rules[finding.Rule] = append(rules[finding.Rule], finding.Message)
			}
			for rule, messages := range rules {
				if rule != tc.rule {
					t.Errorf("planted %s bundle also raised %s: %v", tc.rule, rule, messages)
				}
			}
			if _, found := rules[tc.rule]; !found {
				t.Errorf("planted %s bundle raised %v instead", tc.rule, rules)
			}
		})
	}
}

// Every fixture directory must be exercised, and every listed case must exist:
// a renamed directory would otherwise turn a rule's only proof into a no-op.
func TestPlantedFixturesAreAllUsed(t *testing.T) {
	entries, err := os.ReadDir(filepath.Join("testdata", "planted"))
	if err != nil {
		t.Fatalf("read fixtures: %v", err)
	}

	listed := map[string]bool{}
	for _, tc := range plantedCases {
		listed[tc.dir] = true
	}

	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		if !listed[entry.Name()] {
			t.Errorf("fixture %s is not in plantedCases, so nothing exercises it", entry.Name())
		}
	}

	for _, tc := range plantedCases {
		if _, err := os.Stat(filepath.Join("testdata", "planted", tc.dir)); err != nil {
			t.Errorf("plantedCases names %s: %v", tc.dir, err)
		}
	}
}

// The clean bundle Check reads in every fixture case is the control: without
// it, a checker that fails every bundle would look correct.
func TestCheckAcceptsACleanBundle(t *testing.T) {
	loaded, err := LoadDir(filepath.Join("testdata", "clean", "skills"))
	if err != nil {
		t.Fatalf("LoadDir(clean): %v", err)
	}
	if findings := Check(loaded); len(findings) != 0 {
		t.Fatalf("Check(clean bundle) = %v, want no findings", findings)
	}
}
