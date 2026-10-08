package cli

import (
	"os"
	"regexp"
	"strings"
	"testing"
)

// cli.md's examples are what an agent copies, and they drifted from the binary:
// `node describe httpRequest` exited 4 and the `--url ”` claim was wrong
// (BUG-b4cb1c). Running every example needs a server; this part needs none:
// every `kilasflow <verb>` a code block in cli.md runs is a verb of the live
// command tree, matched on up to three words (`datastore columns add`).
func TestEveryVerbCliMdRunsExists(t *testing.T) {
	source, err := os.ReadFile("../../docs/src/content/docs/reference/cli.md")
	if err != nil {
		t.Fatalf("read cli.md: %v", err)
	}
	paths := map[string]bool{}
	for _, verb := range registry() {
		paths[verb.Path] = true
	}
	blocks := regexp.MustCompile("(?s)```(?:bash|sh|console)?\n(.*?)```").FindAllStringSubmatch(string(source), -1)
	if len(blocks) == 0 {
		t.Fatal("cli.md has no code blocks; the test is reading the wrong file")
	}
	checked := 0
	for _, block := range blocks {
		for _, line := range strings.Split(block[1], "\n") {
			words := strings.Fields(strings.TrimPrefix(strings.TrimSpace(line), "$ "))
			// A bare `kilasflow` serves, and a flag or comment after it names no verb.
			if len(words) < 2 || words[0] != "kilasflow" || strings.HasPrefix(words[1], "-") || strings.HasPrefix(words[1], "#") {
				continue
			}
			checked++
			known := false
			for size := 1; size <= 3 && size < len(words); size++ {
				if paths[strings.Join(words[1:1+size], " ")] {
					known = true
					break
				}
			}
			if !known {
				t.Errorf("cli.md runs %q, which names no verb this binary has", strings.TrimSpace(line))
			}
		}
	}
	if checked == 0 {
		t.Fatal("no kilasflow command found in cli.md's code blocks")
	}
}
