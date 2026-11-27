package cli

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// examplesNotOnThePage are the verbs whose example cli.md cannot run: each
// needs a credential, a live server's readiness or an interactive terminal
// that the example smoke does not have. Every other example must appear
// verbatim on the page, where `make smoke-cli-examples` runs it.
var examplesNotOnThePage = map[string]string{
	"context":    "reads the configured server and the local skills; the page describes it in prose",
	"auth login": "the page pipes the token in (`printf '%s' \"$TOKEN\" | kilasflow auth login --token -`)",
	"ready":      "the page documents health and ready together in prose",
	"help":       "prints this help",
}

// Every verb's --help shows one example, and the example is a command the
// reference page runs, so the help cannot drift into a command that fails.
func TestEveryVerbHasATestedExample(t *testing.T) {
	page, err := os.ReadFile(filepath.Join(repoRoot(t), "docs/src/content/docs/reference/cli.md"))
	if err != nil {
		t.Fatalf("read cli.md: %v", err)
	}
	for _, verb := range registry() {
		if verb.Path == "serve" || verb.Path == "api" {
			continue
		}
		if verb.Example == "" {
			t.Errorf("%s has no Example: copy one from docs/reference/cli.md", verb.Path)
			continue
		}
		if !strings.HasPrefix(verb.Example, "kilasflow "+verb.Path) {
			t.Errorf("%s example %q does not invoke %q", verb.Path, verb.Example, verb.Path)
		}
		if _, exempt := examplesNotOnThePage[verb.Path]; exempt {
			continue
		}
		if !strings.Contains(string(page), verb.Example) {
			t.Errorf("%s example %q is not on docs/reference/cli.md, so nothing runs it", verb.Path, verb.Example)
		}
	}
}
