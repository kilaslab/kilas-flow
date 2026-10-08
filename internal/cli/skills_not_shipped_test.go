package cli

import (
	"regexp"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

// A skill's kilasflow_not_shipped list is what it tells an agent the binary
// cannot do, and it went stale: debug eval and exec retry shipped while two
// skills still denied them, so an agent following the skill took the long way
// round for a verb it had (BUG-x28fsx). The command tree is the authority, so
// an entry that calls one of its verbs "not shipped" fails here.
func TestNoSkillCallsAShippedVerbUnshipped(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	for _, verb := range registry() {
		if verb.Path == "" {
			continue
		}
		claim := regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(verb.Path) + `\b[^.;,]*\b(is not shipped|is not yet shipped|not shipped yet|is unshipped)`)
		for _, skill := range bundle {
			for _, entry := range skill.NotShipped {
				if claim.MatchString(entry) {
					t.Errorf("%s says %q, but `kilasflow %s` ships", skill.Name, entry, verb.Path)
				}
			}
			if section, found := notShippedSection(skill.Body); found && claim.MatchString(section) {
				t.Errorf("%s's '## Not shipped yet' calls `kilasflow %s` unshipped, but it ships", skill.Name, verb.Path)
			}
		}
	}
}

// notShippedSection is the body of a skill's '## Not shipped yet' section.
func notShippedSection(body string) (string, bool) {
	_, after, found := strings.Cut(body, "## Not shipped yet")
	if !found {
		return "", false
	}
	if next := strings.Index(after, "\n## "); next >= 0 {
		after = after[:next]
	}
	return after, true
}
