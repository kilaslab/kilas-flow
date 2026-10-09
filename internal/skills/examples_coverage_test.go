package skills_test

import (
	"sort"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/skills"
)

// Every skill teaches by example: at least one workflow document, and every
// example it carries has the shape the server expects.
func TestEverySkillCarriesAnExample(t *testing.T) {
	all := bundleExamples(t)
	names := make([]string, 0, len(all))
	for name := range all {
		names = append(names, name)
	}
	sort.Strings(names)

	for _, name := range names {
		examples := all[name]
		workflows := 0
		for _, example := range examples {
			if example.Kind == skills.ExampleWorkflow {
				workflows++
			}
			if err := skills.CheckExampleShape(example); err != nil {
				t.Errorf("%s: %v", name, err)
			}
		}
		if workflows == 0 {
			t.Errorf("%s carries no ```json %s example; start from `kilasflow skills show %s --reference EXAMPLE.md`",
				name, skills.ExampleWorkflow, name)
		}
	}
}
