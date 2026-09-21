package skills

import (
	"fmt"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
)

// The rules Check applies. Each has its own name so a finding says which
// convention was broken, and each has a planted fixture proving it fires.
const (
	// RuleSectionOrder is §5.3: the same four sections, in that order, with
	// nothing else at the second level.
	RuleSectionOrder = "section-order"
	// RuleNotShipped is §5.8 made mechanical: a declared capability that is not
	// shipped must be named in the body, or the agent that loads the skill
	// never sees the denial.
	RuleNotShipped = "not-shipped"
	// RuleReferences is the '## Reference files' table against references/.
	RuleReferences = "references"
	// RuleDeclarations is the mechanical half of "declares what it teaches".
	RuleDeclarations = "declarations"
	// RuleSecretHygiene is the one rule with no exceptions, in either
	// direction: no skill may teach a secret into a document, an argument or
	// chat, and the credentials skill must state that itself.
	RuleSecretHygiene = "secret-hygiene"
	// RuleNotShippedCommands refuses a fenced command inside '## Not shipped
	// yet': that section names capabilities that do not exist, so a command
	// there is a claim no drift gate can verify.
	RuleNotShippedCommands = "not-shipped-commands"
)

// credentialsSkill is the skill that owns the secret rule, and
// credentialsRulePhrases are the words it must carry in '## Non-negotiables'.
//
// The positive half is what makes the rule survivable: a bundle that merely
// lacks a violation is not the same as one that states the rule, and deleting
// the sentence has to fail a test rather than quietly reduce the bundle.
const credentialsSkill = "kilasflow-credentials"

var credentialsRulePhrases = []string{
	"referenced by id",
	"never appears in a workflow document",
	"a cli argument",
	"chat",
}

// sectionTitles are the §5.3 sections, in the order every skill uses them.
//
// The order is checked over whichever of them a skill has; presence is checked
// only for the four unconditional ones, because the two conditional sections
// are owned by their own rules — not-shipped owns the '## Not shipped yet'
// section, and the references rule owns the table.
var sectionTitles = []string{
	"Non-negotiables",
	"Strong defaults",
	"Decision tree",
	"Not shipped yet",
	"Anti-patterns",
	"Reference files",
}

// unconditionalSections are the §5.3 sections every skill must carry.
var unconditionalSections = []string{
	"Non-negotiables",
	"Strong defaults",
	"Decision tree",
	"Anti-patterns",
}

// Finding is one rule broken by one skill.
type Finding struct {
	Skill   string
	Rule    string
	Message string
}

// Check applies every rule to the loaded bundle.
//
// It is pure: the bundle arrives parsed, reference text included, so a rule can
// be reasoned about and a fixture exercised without a filesystem.
func Check(skills []Skill) []Finding {
	findings := make([]Finding, 0)
	for _, skill := range skills {
		findings = append(findings, checkSectionOrder(skill)...)
		findings = append(findings, checkNotShipped(skill)...)
		findings = append(findings, checkReferences(skill)...)
		findings = append(findings, checkDeclarations(skill)...)
		findings = append(findings, checkSecretHygiene(skill)...)
		findings = append(findings, checkNotShippedCommands(skill)...)
	}

	sort.SliceStable(findings, func(left, right int) bool {
		if findings[left].Skill != findings[right].Skill {
			return findings[left].Skill < findings[right].Skill
		}
		if findings[left].Rule != findings[right].Rule {
			return findings[left].Rule < findings[right].Rule
		}

		return findings[left].Message < findings[right].Message
	})

	return findings
}

// checkSectionOrder enforces §5.3 over the second-level headings.
//
// Headings are read at line start and outside nothing: a section title inside a
// fenced example would be one, which is the same reading a markdown renderer
// gives it.
func checkSectionOrder(skill Skill) []Finding {
	present := headings(skill.Body)

	// An H2 that is not one of the six sections is a section an agent will skim
	// past looking for the one it wanted.
	seen := make(map[string]bool, len(present))
	for _, heading := range present {
		if !contains(sectionTitles, heading) {
			seen[heading] = true
		}
	}
	unknown := make([]string, 0, len(seen))
	for heading := range seen {
		unknown = append(unknown, heading)
	}
	sort.Strings(unknown)

	findings := make([]Finding, 0, len(unknown)+len(unconditionalSections))
	for _, heading := range unknown {
		findings = append(findings, Finding{
			Skill: skill.Name, Rule: RuleSectionOrder,
			Message: fmt.Sprintf("the body has a '## %s' section; §5.3 allows only %s", heading, strings.Join(sectionTitles, ", ")),
		})
	}

	for _, required := range unconditionalSections {
		if !contains(present, required) {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleSectionOrder,
				Message: fmt.Sprintf("the body has no '## %s' section", required),
			})
		}
	}

	// Every section present must appear in §5.3 order.
	position := 0
	for _, heading := range present {
		index := indexOf(sectionTitles, heading)
		if index < 0 {
			continue
		}
		if index < position {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleSectionOrder,
				Message: fmt.Sprintf("the '## %s' section is out of order; §5.3 fixes the order as %s", heading, strings.Join(sectionTitles, ", ")),
			})

			continue
		}
		position = index
	}

	return findings
}

// checkNotShipped enforces §5.8: the declaration and the body agree.
func checkNotShipped(skill Skill) []Finding {
	section, found := sectionOf(skill.Body, "Not shipped yet")
	if len(skill.NotShipped) == 0 {
		if found {
			return []Finding{{
				Skill: skill.Name, Rule: RuleNotShipped,
				Message: "the body has a '## Not shipped yet' section but kilasflow_not_shipped is empty",
			}}
		}

		return nil
	}
	if !found {
		return []Finding{{
			Skill: skill.Name, Rule: RuleNotShipped,
			Message: fmt.Sprintf("kilasflow_not_shipped declares %d entries but the body has no '## Not shipped yet' section", len(skill.NotShipped)),
		}}
	}

	findings := make([]Finding, 0, len(skill.NotShipped))
	for _, entry := range skill.NotShipped {
		if !strings.Contains(section, entry) {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleNotShipped,
				Message: fmt.Sprintf("'## Not shipped yet' does not name %q; an entry the agent never reads denies nothing", entry),
			})
		}
	}

	return findings
}

// checkReferences compares the '## Reference files' table with references/.
//
// Both directions matter: a table row with no file is a reference the agent
// cannot read, and a file with no row is depth nobody knows to open.
func checkReferences(skill Skill) []Finding {
	declared := make(map[string]bool, len(skill.References))
	for _, reference := range skill.References {
		declared[referenceBase(reference)] = true
	}

	listed, found := sectionOf(skill.Body, "Reference files")
	rows := make(map[string]bool)
	if found {
		for _, row := range referenceTableRows(listed) {
			rows[referenceBase(row)] = true
		}
	}

	findings := make([]Finding, 0)
	if len(skill.References) > 0 && !found {
		findings = append(findings, Finding{
			Skill: skill.Name, Rule: RuleReferences,
			Message: fmt.Sprintf("the skill has %d reference files but no '## Reference files' table", len(skill.References)),
		})
	}

	for _, reference := range skill.References {
		if !rows[referenceBase(reference)] {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleReferences,
				Message: fmt.Sprintf("references/%s is not listed in the '## Reference files' table", referenceBase(reference)),
			})
		}
	}

	names := make([]string, 0, len(rows))
	for name := range rows {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		if !declared[name] {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleReferences,
				Message: fmt.Sprintf("the '## Reference files' table names %s, which is not in references/", name),
			})
		}
	}

	return findings
}

// checkDeclarations asserts every declared command, operation, node and root is
// actually named in the body.
//
// It is the mechanical half of "declares what it teaches". The other half — a
// command leading and its operation id following — is prose, and stays a review
// item.
func checkDeclarations(skill Skill) []Finding {
	findings := make([]Finding, 0)
	for _, group := range []struct {
		kind    string
		entries []string
	}{
		{"kilasflow_commands", skill.Commands},
		{"kilasflow_operations", skill.Operations},
		{"kilasflow_nodes", skill.Nodes},
		{"kilasflow_expression_roots", skill.ExpressionRoots},
	} {
		for _, entry := range group.entries {
			if !strings.Contains(skill.Body, entry) {
				findings = append(findings, Finding{
					Skill: skill.Name, Rule: RuleDeclarations,
					Message: fmt.Sprintf("%s declares %q, which the body never names", group.kind, entry),
				})
			}
		}
	}

	return findings
}

// The secret-hygiene denylist. Each pattern is a way a literal credential
// reaches a place it must not be: an argument left in a shell history, a header
// pasted into a chat, an environment assignment in a script, a document field
// an agent copies into a workflow, or an instruction to do one of those.
//
// The allowed forms are what the patterns spare: "-" (stdin), a "<placeholder>"
// and a "$VARIABLE" are all the skill showing the reader *where* a secret goes
// without printing one. The five patterns are deliberately about shape rather
// than about looking like a key, because a plausible-looking example key is
// exactly the string an agent will paste into a real request.
var (
	// credentialFlag: --token, --password and --api-key are the CLI's own
	// secret-carrying flags; the API-key verb is `--token`, and the rejections
	// in this rule are why.
	credentialFlag = regexp.MustCompile(`--(?:token|password|api-key)\s+(\S+)`)
	// credentialHeader: an Authorization or X-Api-Key header.
	credentialHeader = regexp.MustCompile(`(?i)(?:authorization:\s*bearer|x-api-key:)\s+(\S+)`)
	// credentialEnv: the CLI reads its token from KILASFLOW_TOKEN, so an
	// assignment with a literal on the right is a token in a script forever.
	credentialEnv = regexp.MustCompile(`KILASFLOW_(?:TOKEN|API_KEY)=(\S+)`)
	// credentialField: a credential field inside a fenced document, which is
	// how a secret ends up in a workflow or an example an agent copies.
	credentialField = regexp.MustCompile(`"(?:token|apiKey|password|secret)"\s*:\s*"([^"]*)"`)
)

// credentialPhrases are instructions to move a secret somewhere it will be
// kept. They name no key, which is exactly why the patterns above cannot catch
// them.
var credentialPhrases = []string{"put the token", "paste the secret", "hard-code the key"}

// checkSecretHygiene scans the body and every reference file.
func checkSecretHygiene(skill Skill) []Finding {
	findings := make([]Finding, 0)

	documents := make([]proseDocument, 0, len(skill.ReferenceText)+1)
	documents = append(documents, proseDocument{name: skill.Path, text: skill.Body})
	for _, reference := range skill.References {
		text, found := skill.ReferenceText[reference]
		if !found {
			continue
		}
		documents = append(documents, proseDocument{name: reference, text: text})
	}

	for _, document := range documents {
		findings = append(findings, checkSecretsInDocument(skill.Name, document)...)
	}

	// The positive half: the credentials skill states the rule in the section an
	// agent reads first.
	if skill.Name == credentialsSkill {
		section, found := sectionOf(skill.Body, "Non-negotiables")
		if !found {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleSecretHygiene,
				Message: "the credentials skill has no '## Non-negotiables' section to state the secret rule in",
			})
		} else {
			lower := strings.ToLower(section)
			for _, phrase := range credentialsRulePhrases {
				if !strings.Contains(lower, phrase) {
					findings = append(findings, Finding{
						Skill: skill.Name, Rule: RuleSecretHygiene,
						Message: fmt.Sprintf("'## Non-negotiables' does not state that a credential is %q", phrase),
					})
				}
			}
		}
	}

	return findings
}

// checkNotShippedCommands refuses a fenced `kilasflow …` line inside the '##
// Not shipped yet' section.
//
// That section exists to name capabilities that do not exist yet, so a command
// written there is either a verb that does not resolve or a fence a drift gate
// cannot verify — the two failure modes §5.8 was written after.
func checkNotShippedCommands(skill Skill) []Finding {
	section, found := sectionOf(skill.Body, "Not shipped yet")
	if !found {
		return nil
	}

	findings := make([]Finding, 0)
	fenced := false
	for _, line := range strings.Split(section, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced

			continue
		}
		if fenced && strings.HasPrefix(strings.TrimSpace(line), "kilasflow ") {
			findings = append(findings, Finding{
				Skill: skill.Name, Rule: RuleNotShippedCommands,
				Message: fmt.Sprintf("'## Not shipped yet' carries the fenced command %q; name the missing capability in prose instead", strings.TrimSpace(line)),
			})
		}
	}

	return findings
}

// proseDocument is one document the secret-hygiene rule reads: a skill body or
// one of its reference files.
type proseDocument struct {
	name string
	text string
}

// checkSecretsInDocument applies the denylist to one document.
func checkSecretsInDocument(skill string, document proseDocument) []Finding {
	findings := make([]Finding, 0)

	for _, pattern := range []*regexp.Regexp{credentialFlag, credentialHeader, credentialEnv} {
		for _, match := range pattern.FindAllStringSubmatch(document.text, -1) {
			if allowedSecretValue(match[1]) {
				continue
			}
			findings = append(findings, Finding{
				Skill: skill, Rule: RuleSecretHygiene,
				Message: fmt.Sprintf("%s carries a literal secret in %q", document.name, match[0]),
			})
		}
	}

	for _, match := range credentialField.FindAllStringSubmatch(fencedText(document.text), -1) {
		if allowedSecretValue(match[1]) {
			continue
		}
		findings = append(findings, Finding{
			Skill: skill, Rule: RuleSecretHygiene,
			Message: fmt.Sprintf("%s carries a credential field with a literal value: %q", document.name, match[0]),
		})
	}

	lower := strings.ToLower(document.text)
	for _, phrase := range credentialPhrases {
		if strings.Contains(lower, phrase) {
			findings = append(findings, Finding{
				Skill: skill, Rule: RuleSecretHygiene,
				Message: fmt.Sprintf("%s instructs the reader to %q", document.name, phrase),
			})
		}
	}

	return findings
}

// allowedSecretValue reports the forms that show where a secret goes without
// printing one: stdin, a placeholder and an environment reference.
func allowedSecretValue(value string) bool {
	switch {
	case value == "-":
		return true
	case strings.HasPrefix(value, "<"):
		return true
	case strings.HasPrefix(value, "$"):
		return true
	default:
		return false
	}
}

// headings lists the second-level headings of a body, in order.
func headings(body string) []string {
	found := make([]string, 0)
	for _, line := range strings.Split(body, "\n") {
		if !strings.HasPrefix(line, "## ") {
			continue
		}
		found = append(found, strings.TrimSpace(strings.TrimPrefix(line, "## ")))
	}

	return found
}

// sectionOf returns one section's text, from just after its heading to the next
// second-level heading or the end of the body.
func sectionOf(body, title string) (string, bool) {
	lines := strings.Split(body, "\n")
	start := -1
	for index, line := range lines {
		if strings.TrimSpace(line) != "## "+title {
			continue
		}
		start = index + 1

		break
	}
	if start < 0 {
		return "", false
	}

	for index := start; index < len(lines); index++ {
		if strings.HasPrefix(lines[index], "## ") {
			return strings.Join(lines[start:index], "\n"), true
		}
	}

	return strings.Join(lines[start:], "\n"), true
}

// referenceTableRows reads the first column of the '## Reference files' table.
//
// The header row and the delimiter row are skipped; a link in the cell is
// reduced to its text, and backticks are tolerated, so the same file may be
// written plainly or as a link.
func referenceTableRows(section string) []string {
	rows := make([]string, 0)
	header := false
	for _, line := range strings.Split(section, "\n") {
		trimmed := strings.TrimSpace(line)
		if !strings.HasPrefix(trimmed, "|") {
			continue
		}

		cells := strings.Split(strings.Trim(trimmed, "|"), "|")
		if len(cells) == 0 {
			continue
		}
		first := strings.TrimSpace(cells[0])
		if first == "" || isDelimiterRow(cells) {
			continue
		}
		if !header {
			header = true

			continue
		}

		rows = append(rows, first)
	}

	return rows
}

// isDelimiterRow reports the |---|---| row under a table header.
func isDelimiterRow(cells []string) bool {
	for _, cell := range cells {
		trimmed := strings.TrimSpace(cell)
		if trimmed == "" {
			continue
		}
		if strings.Trim(trimmed, "-:") != "" {
			return false
		}
	}

	return true
}

// referenceBase reduces a table cell or a reference path to the file name the
// two are compared by.
func referenceBase(value string) string {
	trimmed := strings.TrimSpace(value)
	trimmed = strings.Trim(trimmed, "`")
	if open := strings.Index(trimmed, "]("); open > 0 && strings.HasPrefix(trimmed, "[") {
		trimmed = trimmed[1:open]
	}
	trimmed = strings.Trim(strings.TrimSpace(trimmed), "`")

	return filepath.Base(trimmed)
}

// fencedText is the concatenation of every fenced block in a document.
func fencedText(document string) string {
	var (
		included []string
		fenced   bool
	)
	for _, line := range strings.Split(document, "\n") {
		if strings.HasPrefix(strings.TrimSpace(line), "```") {
			fenced = !fenced

			continue
		}
		if fenced {
			included = append(included, line)
		}
	}

	return strings.Join(included, "\n")
}

// contains reports membership in a small fixed list.
func contains(values []string, want string) bool {
	return indexOf(values, want) >= 0
}

// indexOf is the position of a value in a small fixed list, or -1.
func indexOf(values []string, want string) int {
	for index, value := range values {
		if value == want {
			return index
		}
	}

	return -1
}
