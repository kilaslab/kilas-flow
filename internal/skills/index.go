package skills

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
)

// IndexGenerator is the command the index names as its producer, so a reader
// who finds skills/index.json stale knows what to run without looking it up.
const IndexGenerator = "go run ./scripts/skills-index"

// indexDocument is the JSON a harness reads: the bundle's stamp first, then one
// entry per skill.
type indexDocument struct {
	SkillsVersion int          `json:"skillsVersion"`
	GeneratedBy   string       `json:"generatedBy"`
	Skills        []indexEntry `json:"skills"`
}

// indexEntry is one skill as a harness sees it: what it teaches, and what it
// refuses to claim.
type indexEntry struct {
	Name            string   `json:"name"`
	Description     string   `json:"description"`
	Path            string   `json:"path"`
	Commands        []string `json:"commands"`
	Operations      []string `json:"operations"`
	Nodes           []string `json:"nodes"`
	ExpressionRoots []string `json:"expressionRoots"`
	NotShipped      []string `json:"notShipped"`
	References      []string `json:"references"`
}

// Index renders skills/index.json.
//
// The output is canonical by construction — skills in name order, every list
// sorted and deduplicated, two-space indent, one trailing newline, no timestamp
// and no absolute path — because the freshness test compares bytes. Anything
// unstable here (map iteration, a clock, a machine path) would make that test
// flap rather than catch a stale file.
func Index(skills []Skill) ([]byte, error) {
	sorted := append([]Skill(nil), skills...)
	sort.Slice(sorted, func(left, right int) bool { return sorted[left].Name < sorted[right].Name })

	entries := make([]indexEntry, 0, len(sorted))
	for _, skill := range sorted {
		if skill.SkillsVersion != SkillsVersion {
			return nil, fmt.Errorf("%s declares kilasflow_skills_version %d, want %d", skill.Path, skill.SkillsVersion, SkillsVersion)
		}

		entries = append(entries, indexEntry{
			Name:            skill.Name,
			Description:     skill.Description,
			Path:            skill.Path,
			Commands:        sortedUnique(skill.Commands),
			Operations:      sortedUnique(skill.Operations),
			Nodes:           sortedUnique(skill.Nodes),
			ExpressionRoots: sortedUnique(skill.ExpressionRoots),
			NotShipped:      sortedUnique(skill.NotShipped),
			References:      sortedUnique(skill.References),
		})
	}

	document := indexDocument{
		SkillsVersion: SkillsVersion,
		GeneratedBy:   IndexGenerator,
		Skills:        entries,
	}

	var buffer bytes.Buffer
	encoder := json.NewEncoder(&buffer)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(document); err != nil {
		return nil, fmt.Errorf("render the skills index: %w", err)
	}

	return buffer.Bytes(), nil
}

// sortedUnique returns a sorted, deduplicated copy that renders as [] rather
// than null when there is nothing to list.
func sortedUnique(values []string) []string {
	unique := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		if seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	sort.Strings(unique)

	return unique
}
