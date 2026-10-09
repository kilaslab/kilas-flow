package cli

import (
	"flag"
	"io"
	"io/fs"
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

// The other half of the drift: a skill that tells an agent to run a verb the
// binary does not have sends it into a usage error. Every `kilasflow <verb>`
// a skill's prose or reference files quote has to be a path of the live
// command tree (BUG-x28fsx).
func TestEveryVerbASkillQuotesExists(t *testing.T) {
	paths := map[string]bool{}
	for _, verb := range registry() {
		paths[verb.Path] = true
	}
	quoted := regexp.MustCompile("`kilasflow ([a-z][a-z-]*)(?: ([a-z][a-z-]*))?(?: ([a-z][a-z-]*))?")
	err := fs.WalkDir(skills.BundleFS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := fs.ReadFile(skills.BundleFS(), path)
		if err != nil {
			return err
		}
		for _, match := range quoted.FindAllStringSubmatch(string(data), -1) {
			first, second, third := match[1], match[2], match[3]
			if paths[first] || (second != "" && paths[first+" "+second]) ||
				(third != "" && paths[first+" "+second+" "+third]) {
				continue
			}
			t.Errorf("%s quotes `kilasflow %s %s`, which is not a verb this binary has", path, first, second)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the skills bundle: %v", err)
	}
}

// The list form of the same drift: "credential create, update and delete …
// have no verbs yet" denied three guarded verbs the binary ships, and named
// none of them as "<path> is not shipped". A verb path written before "have
// no verb(s)" in a not-shipped entry is a claim that it does not exist.
func TestNoSkillListsAShippedVerbAsMissing(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	for _, skill := range bundle {
		for _, entry := range skill.NotShipped {
			claim, _, found := strings.Cut(entry, " have no verb")
			if !found {
				claim, _, found = strings.Cut(entry, " has no verb")
			}
			if !found {
				continue
			}
			for _, verb := range registry() {
				if strings.Contains(verb.Path, " ") && strings.Contains(claim, verb.Path) {
					t.Errorf("%s says %q has no verb, but `kilasflow %s` ships", skill.Name, verb.Path, verb.Path)
				}
			}
		}
	}
}

// A "not shipped" claim is a sentence, and the three tests above only catch the
// sentences that happen to use their exact wording. The gate below reads every
// sentence of a not_shipped entry and of the '## Not shipped yet' section and
// refuses the claim in whichever form it is written: a shipped verb path next to
// a negation, a "No --flag" entry for a flag the binary registers, an operation
// id a verb drives described as reached "through the escape hatch", and a
// "No <family> verbs" head whose leaf verbs the family has (BUG-x28fsx).

// notShippedPieces is every place a skill denies a capability: each
// kilasflow_not_shipped entry and the body of its '## Not shipped yet' section.
func notShippedPieces(skill skills.Skill) []string {
	pieces := append([]string{}, skill.NotShipped...)
	if section, found := notShippedSection(skill.Body); found {
		pieces = append(pieces, section)
	}

	return pieces
}

// sentenceBreak ends a sentence at a full stop followed by a space, a
// semicolon, a newline or a leading bullet. A colon does not end one, because
// "No X verb: exec retry reruns …" is a single claim and the gate has to be
// able to see both halves of it.
var sentenceBreak = regexp.MustCompile(`\.\s|;|\n|^\s*[-*]\s`)

func claimSentences(piece string) []string {
	return sentenceBreak.Split(piece, -1)
}

// negation is what turns a mention of a verb into a claim about it.
var negation = regexp.MustCompile(`(?i)\b(no|not|never|nor|without|cannot|isn't|aren't|nothing)\b`)

// verbPattern matches a verb's words with any run of spaces between them.
func verbPattern(path string) string {
	return strings.Join(strings.Fields(path), `\s+`)
}

// deniesVerb reports whether a sentence denies a verb that ships.
//
// A multi-word path (`workflow activate`, `skills list`) is specific enough
// that writing it next to a negation is the denial, bare or after `kilasflow`,
// backticked or not. A one-word verb (`run`, `api`, `help`) is also a common
// English word and a sentence about its flags legitimately says "no", so only
// the denial's own forms count: "no `run` verb", "`run` does not exist".
func deniesVerb(sentence, path string) bool {
	joined := verbPattern(path)
	if strings.Contains(path, " ") {
		return negation.MatchString(sentence) &&
			regexp.MustCompile(`(?i)\b`+joined+`\b`).MatchString(sentence)
	}

	mention := "`?(?:kilasflow\\s+)?" + joined + "`?"
	missing := "(?i)\\b(?:no|without)\\s+" + mention + "\\s+(?:verb|command)\\b"
	absent := "(?i)" + mention + "\\s+(?:is|are|does|do)\\s+not\\s+(?:exist|ship)"

	return regexp.MustCompile(missing).MatchString(sentence) ||
		regexp.MustCompile(absent).MatchString(sentence)
}

func TestNoNotShippedSentenceNegatesAShippedVerb(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	for _, skill := range bundle {
		for _, piece := range notShippedPieces(skill) {
			for _, sentence := range claimSentences(piece) {
				for _, verb := range registry() {
					if verb.Path != "" && deniesVerb(sentence, verb.Path) {
						t.Errorf("%s: %q writes a negation next to `kilasflow %s`, which ships; say what is missing without naming a verb that exists",
							skill.Name, strings.TrimSpace(sentence), verb.Path)
					}
				}
			}
		}
	}
}

// verbFlags is every flag each verb registers, the globals every verb takes
// and its own, keyed by the verb's path.
func verbFlags() map[string]map[string]bool {
	flags := map[string]map[string]bool{}
	for _, verb := range registry() {
		set := flag.NewFlagSet(verb.Path, flag.ContinueOnError)
		set.SetOutput(io.Discard)
		registerGlobalFlags(set)
		if verb.Flags != nil {
			verb.Flags(set)
		}
		names := map[string]bool{}
		set.VisitAll(func(registered *flag.Flag) { names[registered.Name] = true })
		flags[verb.Path] = names
	}

	return flags
}

// A "No --skills-used flag" entry outlived the flag, and "`run --revision` does
// not exist" outlived it too. Any entry of the form "No --x flag" has to name a
// flag the binary registers on no verb, and a verb's own flag written next to a
// negation is a denial of a flag that ships.
func TestNoSkillDeniesAFlagTheBinaryRegisters(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	denial := regexp.MustCompile("(?i)\\bno\\s+`?--([a-z][a-z0-9-]*)`?\\s+flag")
	flags := verbFlags()
	registered := map[string]bool{}
	for _, names := range flags {
		for name := range names {
			registered[name] = true
		}
	}
	for _, skill := range bundle {
		for _, piece := range notShippedPieces(skill) {
			for _, match := range denial.FindAllStringSubmatch(piece, -1) {
				if registered[match[1]] {
					t.Errorf("%s says %q, but --%s is a flag this binary registers", skill.Name, match[0], match[1])
				}
			}
			for _, sentence := range claimSentences(piece) {
				if !negation.MatchString(sentence) {
					continue
				}
				for path, names := range flags {
					for name := range names {
						mention := "(?i)" + verbPattern(path) + "`?\\s+--" + regexp.QuoteMeta(name) + "\\b"
						if regexp.MustCompile(mention).MatchString(sentence) {
							t.Errorf("%s: %q writes a negation next to `%s --%s`, which ships", skill.Name, strings.TrimSpace(sentence), path, name)
						}
					}
				}
			}
		}
	}
}

// operationsOfShippedVerbs maps each operation id a verb drives to that verb.
func operationsOfShippedVerbs() map[string]string {
	operations := map[string]string{}
	for _, verb := range registry() {
		if verb.Operation != "" && verb.Path != "" {
			if _, taken := operations[verb.Operation]; !taken {
				operations[verb.Operation] = verb.Path
			}
		}
	}

	return operations
}

// "activate-workflow and deactivate-workflow are reached through the escape
// hatch" names no verb path, so the negation gate cannot see that
// `workflow activate` ships. An operation id a verb drives, described as
// reached through the escape hatch, is the same claim.
func TestNoSkillSendsAShippedVerbsOperationThroughTheEscapeHatch(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	escape := regexp.MustCompile("(?i)escape hatch")
	for _, skill := range bundle {
		for _, piece := range notShippedPieces(skill) {
			for _, sentence := range claimSentences(piece) {
				if !escape.MatchString(sentence) {
					continue
				}
				for operation, path := range operationsOfShippedVerbs() {
					if regexp.MustCompile(`\b` + regexp.QuoteMeta(operation) + `\b`).MatchString(sentence) {
						t.Errorf("%s: %q sends `%s` through the escape hatch, but `kilasflow %s` drives it",
							skill.Name, strings.TrimSpace(sentence), operation, path)
					}
				}
			}
		}
	}
}

// "No skills verbs: list, show, install, check and export do not exist yet"
// names no path either. A "No <family> verbs" head followed by two or more of
// the family's real leaf verbs is the same denial.
func TestNoSkillDeniesAWholeVerbFamily(t *testing.T) {
	bundle, err := skills.LoadBundle()
	if err != nil {
		t.Fatalf("LoadBundle() error = %v", err)
	}
	leaves := map[string][]string{}
	for _, verb := range registry() {
		words := strings.Fields(verb.Path)
		if len(words) >= 2 {
			leaves[words[0]] = append(leaves[words[0]], words[len(words)-1])
		}
	}
	head := regexp.MustCompile(`(?i)\bno\s+([a-z]+)\s+verbs?\b`)
	for _, skill := range bundle {
		for _, piece := range notShippedPieces(skill) {
			for _, sentence := range claimSentences(piece) {
				match := head.FindStringSubmatch(sentence)
				if match == nil {
					continue
				}
				named := 0
				for _, leaf := range leaves[strings.ToLower(match[1])] {
					if regexp.MustCompile(`(?i)\b` + regexp.QuoteMeta(leaf) + `\b`).MatchString(sentence) {
						named++
					}
				}
				if named >= 2 {
					t.Errorf("%s: %q denies the %s verbs, but %d of them ship", skill.Name, strings.TrimSpace(sentence), match[1], named)
				}
			}
		}
	}
}

// The gates above read the denial sections. "There is no debug eval" sat in a
// non-negotiable of kilasflow-expressions for a release after the verb shipped,
// so the plain forms of a denial are also refused anywhere in the bundle's
// prose: "no <verb>" and "<verb> does not exist". The forms are tight on
// purpose (a negation directly before the path, or an existence verb after it)
// because an anti-pattern may say "not" about anything.
func TestNoSkillProseDeniesAShippedVerb(t *testing.T) {
	err := fs.WalkDir(skills.BundleFS(), ".", func(path string, entry fs.DirEntry, err error) error {
		if err != nil || entry.IsDir() || !strings.HasSuffix(path, ".md") {
			return err
		}
		data, err := fs.ReadFile(skills.BundleFS(), path)
		if err != nil {
			return err
		}
		text := string(data)
		if from := strings.Index(text, "<!-- generated:command-reference -->"); from >= 0 {
			if to := strings.Index(text, "<!-- /generated:command-reference -->"); to > from {
				text = text[:from] + text[to:]
			}
		}
		for _, verb := range registry() {
			if verb.Path == "" || !strings.Contains(verb.Path, " ") {
				continue
			}
			joined := verbPattern(verb.Path)
			before := regexp.MustCompile("(?i)\\b(?:no|without|nor)\\s+`?(?:kilasflow\\s+)?" + joined + "\\b")
			after := regexp.MustCompile("(?i)\\b" + joined + "\\b[^.;\\n]{0,60}?\\b(?:do|does|is|are)\\s+not\\s+(?:yet\\s+)?(?:exist|ship|shipped)\\b")
			for _, pattern := range []*regexp.Regexp{before, after} {
				for _, match := range pattern.FindAllString(text, -1) {
					t.Errorf("%s says %q, but `kilasflow %s` ships", path, match, verb.Path)
				}
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("walk the skills bundle: %v", err)
	}
}
