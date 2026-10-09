package docexamples_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/docexamples"
)

func fence(lang string, lines ...string) string {
	return "```" + lang + "\n" + strings.Join(lines, "\n") + "\n```\n"
}

// only returns the single example a one-line page yields.
func only(t *testing.T, page string) docexamples.Example {
	t.Helper()
	got := docexamples.Extract(page)
	if len(got) != 1 {
		t.Fatalf("Extract(%q) = %d examples, want 1: %+v", page, len(got), got)
	}

	return got[0]
}

func TestExtractReadsShellFencesAndNothingElse(t *testing.T) {
	page := "prose\n\n" +
		fence("bash", "kilasflow health") +
		fence("json", `{"kilasflow": "not a command"}`) +
		fence("sh", "kilasflow ready") +
		fence("console", "$ kilasflow version") +
		fence("", "kilasflow help") +
		fence("yaml", "kilasflow: also not a command")

	var texts []string
	for _, example := range docexamples.Extract(page) {
		texts = append(texts, example.Text)
	}
	want := []string{"kilasflow health", "kilasflow ready", "kilasflow version"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("Extract() texts = %q, want %q", texts, want)
	}
}

func TestExtractReportsTheLineOfEachCommand(t *testing.T) {
	page := "# Title\n\n```bash\nkilasflow health\n\n# a comment line\nkilasflow ready\n```\n"
	got := docexamples.Extract(page)
	if len(got) != 2 || got[0].Line != 4 || got[1].Line != 7 {
		t.Fatalf("Extract() lines = %+v, want commands at lines 4 and 7", got)
	}
}

func TestExtractNumbersTheShellFencesSoARunnerCanSeedEachOnce(t *testing.T) {
	page := fence("bash", "kilasflow health", "kilasflow ready") +
		fence("json", "{}") +
		fence("bash", "kilasflow version")
	got := docexamples.Extract(page)
	if len(got) != 3 || got[0].Block != 0 || got[1].Block != 0 || got[2].Block != 1 {
		t.Fatalf("Extract() blocks = %+v, want 0, 0, 1 (a non-shell fence does not count)", got)
	}
}

func TestExtractSplitsWordsTheWayAShellDoes(t *testing.T) {
	example := only(t, fence("bash",
		`kilasflow debug eval '$("HTTP Request").first().json.id' --execution "$EXECUTION_ID" --node set --quiet`))
	want := []string{"kilasflow", "debug", "eval", `$("HTTP Request").first().json.id`, "--execution", "$EXECUTION_ID", "--node", "set", "--quiet"}
	if !reflect.DeepEqual(example.Words, want) {
		t.Fatalf("Words = %q, want %q", example.Words, want)
	}
}

func TestExtractKeepsAnEmptyQuotedWord(t *testing.T) {
	example := only(t, fence("bash", `kilasflow workflow list --url ''`))
	want := []string{"kilasflow", "workflow", "list", "--url", ""}
	if !reflect.DeepEqual(example.Words, want) {
		t.Fatalf("Words = %q, want %q", example.Words, want)
	}
}

func TestExtractTakesAnnotationsFromATrailingComment(t *testing.T) {
	for line, want := range map[string]struct {
		comment string
		exit    int
	}{
		`kilasflow health`:                                             {"", 0},
		`kilasflow health   # the readiness probe`:                     {"the readiness probe", 0},
		`kilasflow exec cancel "$EXECUTION_ID"  # exit 5 (conflict)`:   {"exit 5 (conflict)", 5},
		`kilasflow workflow list --url ''  # exit 2: no server URL`:    {"exit 2: no server URL", 2},
		`kilasflow health # we exit 4 times out of 10`:                 {"we exit 4 times out of 10", 4},
		`kilasflow health # prefix exit 12 is not one digit`:           {"prefix exit 12 is not one digit", 0},
		`kilasflow health # exits 3 is not the annotation`:             {"exits 3 is not the annotation", 0},
		`kilasflow x 'not # a comment' "nor # this" # exit 6 for real`: {"exit 6 for real", 6},
		`kilasflow x#y`: {"", 0},
	} {
		example := only(t, fence("bash", line))
		if example.Comment != want.comment || example.ExpectedExit != want.exit {
			t.Errorf("%s: Comment = %q, ExpectedExit = %d; want %q, %d", line, example.Comment, example.ExpectedExit, want.comment, want.exit)
		}
	}
}

func TestExtractDoesNotMistakeTheHashOfAWordForAComment(t *testing.T) {
	example := only(t, fence("bash", `kilasflow x#y '#z'`))
	if want := []string{"kilasflow", "x#y", "#z"}; !reflect.DeepEqual(example.Words, want) {
		t.Fatalf("Words = %q, want %q", example.Words, want)
	}
}

func TestExtractSkipsTheOutputOfAPromptedCommand(t *testing.T) {
	page := fence("bash",
		"$ kilasflow health --quiet",
		"ok",
		"",
		"$ kilasflow workflow create --file wf.json --quiet   # the new workflow id",
		"wf_01J8ZP…",
		"$ kilasflow ready",
		`{"ok":true}`,
	)
	var texts []string
	for _, example := range docexamples.Extract(page) {
		texts = append(texts, example.Text)
	}
	want := []string{"kilasflow health --quiet", "kilasflow workflow create --file wf.json --quiet", "kilasflow ready"}
	if !reflect.DeepEqual(texts, want) {
		t.Fatalf("Extract() texts = %q, want %q", texts, want)
	}
}

// A fence whose commands carry no prompt has no output lines: everything in it
// that is not blank or a comment is a command.
func TestExtractTreatsUnpromptedLinesAsCommands(t *testing.T) {
	got := docexamples.Extract(fence("bash", "kilasflow health", "kilasflow ready"))
	if len(got) != 2 {
		t.Fatalf("Extract() = %+v, want two commands", got)
	}
}

func TestExtractFollowsBackslashContinuations(t *testing.T) {
	example := only(t, fence("bash", `kilasflow run "$WORKFLOW_ID" \`, `  --input '{"n":1}' \`, `  --wait   # exit 1`))
	want := []string{"kilasflow", "run", "$WORKFLOW_ID", "--input", `{"n":1}`, "--wait"}
	if !reflect.DeepEqual(example.Words, want) || example.ExpectedExit != 1 {
		t.Fatalf("Words = %q, ExpectedExit = %d; want %q, 1", example.Words, example.ExpectedExit, want)
	}
	if example.Line != 2 {
		t.Errorf("Line = %d, want 2, the line the command starts on", example.Line)
	}
}

func TestExtractStopsTheKilasflowWordsAtPipesAndRedirects(t *testing.T) {
	for line, want := range map[string][]string{
		`printf '%s' "$TOKEN" | kilasflow auth login --token -`:       {"kilasflow", "auth", "login", "--token", "-"},
		`kilasflow workflow validate --file - --quiet < patch.json`:   {"kilasflow", "workflow", "validate", "--file", "-", "--quiet"},
		`kilasflow datastore export "$DATASTORE_ID" > rows.csv`:       {"kilasflow", "datastore", "export", "$DATASTORE_ID"},
		`kilasflow workflow list --quiet | while read wf; do :; done`: {"kilasflow", "workflow", "list", "--quiet"},
		`kilasflow health && kilasflow ready`:                         {"kilasflow", "health"},
		`kilasflow node describe "$(kilasflow node list --quiet)"`:    {"kilasflow", "node", "describe", `$(kilasflow node list --quiet)`},
	} {
		example := only(t, fence("bash", line))
		if !reflect.DeepEqual(example.Words, want) {
			t.Errorf("%s: Words = %q, want %q", line, example.Words, want)
		}
		if example.Text != line {
			t.Errorf("%s: Text = %q, want the whole command so a runner can run it as written", line, example.Text)
		}
	}
}

func TestExtractSkipsAnEnvironmentPrefixAndAFileDescriptorRedirect(t *testing.T) {
	example := only(t, fence("bash", `KILASFLOW_URL="http://127.0.0.1:1" kilasflow health --quiet 2>&1`))
	if want := []string{"kilasflow", "health", "--quiet"}; !reflect.DeepEqual(example.Words, want) {
		t.Fatalf("Words = %q, want %q", example.Words, want)
	}
}

func TestExtractIgnoresCommandsThatAreNotKilasflow(t *testing.T) {
	got := docexamples.Extract(fence("bash", "export KILASFLOW_URL=http://127.0.0.1:8080", "cd ./project", "kilasflow health", "curl -s localhost"))
	if len(got) != 1 || got[0].Text != "kilasflow health" {
		t.Fatalf("Extract() = %+v, want only the kilasflow command", got)
	}
}

func TestExtractMarksTheServerPath(t *testing.T) {
	for line, server := range map[string]bool{
		`kilasflow`:                          true,
		`kilasflow serve`:                    true,
		`kilasflow -config config.yaml`:      true,
		`kilasflow    # serves`:              true,
		`kilasflow version`:                  false,
		`kilasflow workflow list`:            false,
		`kilasflow mcp serve`:                false,
		`kilasflow serve -config x.yaml`:     true,
		`kilasflow --version`:                true,
		`kilasflow -h`:                       true,
		`kilasflow help`:                     false,
		`kilasflow servers`:                  false,
		`kilasflow workflow get serve-thing`: false,
	} {
		example := only(t, fence("bash", line))
		if example.ServerPath != server {
			t.Errorf("%s: ServerPath = %v, want %v", line, example.ServerPath, server)
		}
	}
}

func TestExtractReturnsNothingForAPageWithNoShellFence(t *testing.T) {
	if got := docexamples.Extract("just prose\n\n" + fence("json", "{}")); len(got) != 0 {
		t.Fatalf("Extract() = %+v, want nothing", got)
	}
}
