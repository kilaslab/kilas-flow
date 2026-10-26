package main

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

const (
	testBegin = "<!-- generated:test -->"
	testEnd   = "<!-- /generated:test -->"
)

// page writes a one-fact tree: a page with a stale block between the markers.
func page(t *testing.T, body string) (root string, list []fact) {
	t.Helper()
	root = t.TempDir()
	if err := os.MkdirAll(filepath.Join(root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	text := strings.Join([]string{"# Title", "", testBegin, body, testEnd, "", "prose after"}, "\n")
	if err := os.WriteFile(filepath.Join(root, "docs", "page.md"), []byte(text), 0o644); err != nil {
		t.Fatal(err)
	}

	return root, []fact{{
		name: "test", path: "docs/page.md", begin: testBegin, end: testEnd,
		body: func() (string, error) { return "fresh sentence.", nil },
	}}
}

func read(t *testing.T, root string) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join(root, "docs", "page.md"))
	if err != nil {
		t.Fatal(err)
	}

	return string(raw)
}

func TestApplyWritesTheBodyBetweenTheMarkersAndNothingElse(t *testing.T) {
	root, list := page(t, "stale sentence.")

	if err := apply(root, list, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	want := strings.Join([]string{"# Title", "", testBegin, "fresh sentence.", testEnd, "", "prose after"}, "\n")
	if got := read(t, root); got != want {
		t.Fatalf("page =\n%q\nwant\n%q", got, want)
	}
}

func TestCheckNamesTheFileAndTheFirstDifferingLineAndWritesNothing(t *testing.T) {
	root, list := page(t, "stale sentence.")
	before := read(t, root)

	err := apply(root, list, true, &bytes.Buffer{})
	if err == nil {
		t.Fatal("apply(check) returned no error for a stale page")
	}
	for _, want := range []string{"docs/page.md", "first at line 4", "stale sentence.", "fresh sentence.", "make generate-docs-facts"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("apply(check) error = %q, want it to hold %q", err, want)
		}
	}
	if after := read(t, root); after != before {
		t.Fatalf("apply(check) rewrote the page:\n%q", after)
	}
}

func TestCheckPassesOnceTheFactsAreWritten(t *testing.T) {
	root, list := page(t, "stale sentence.")
	if err := apply(root, list, false, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply() error = %v", err)
	}
	if err := apply(root, list, true, &bytes.Buffer{}); err != nil {
		t.Fatalf("apply(check) after a write = %v, want it clean", err)
	}
}

func TestApplyRefusesAPageThatLostItsMarkers(t *testing.T) {
	root, list := page(t, "stale sentence.")
	list[0].begin = "<!-- generated:gone -->"

	err := apply(root, list, false, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "docs/page.md") || !strings.Contains(err.Error(), "carries no") {
		t.Fatalf("apply() error = %v, want the page named and the missing marker said", err)
	}
}

// Every stale page is reported in one run, so a contributor who regenerates
// does not have to discover them one failure at a time.
func TestCheckReportsEveryStalePage(t *testing.T) {
	root, list := page(t, "stale sentence.")
	second := filepath.Join(root, "docs", "other.md")
	if err := os.WriteFile(second, []byte(testBegin+"\nold\n"+testEnd), 0o644); err != nil {
		t.Fatal(err)
	}
	list = append(list, fact{name: "test", path: "docs/other.md", begin: testBegin, end: testEnd,
		body: func() (string, error) { return "new", nil }})

	err := apply(root, list, true, &bytes.Buffer{})
	if err == nil || !strings.Contains(err.Error(), "docs/page.md") || !strings.Contains(err.Error(), "docs/other.md") {
		t.Fatalf("apply(check) error = %v, want both pages named", err)
	}
}

// The command owns three sections today: the exit-code tables of the CLI
// reference and the catalogue's size on the two pages that state it. A fact that
// quietly left the list would leave its page unguarded.
func TestFactsCoverTheExitCodesAndBothCataloguePages(t *testing.T) {
	list, err := facts()
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}
	have := map[string]string{}
	for _, entry := range list {
		have[entry.path] = entry.name
	}
	for path, name := range map[string]string{
		"docs/src/content/docs/reference/cli.md":           "exit codes",
		"docs/src/content/docs/concepts/node-registry.md":  "catalogue size",
		"docs/src/content/docs/start/what-kilasflow-is.md": "catalogue size",
	} {
		if have[path] != name {
			t.Errorf("facts() has %q for %s, want %q", have[path], path, name)
		}
	}
}

// The credentials page said "Thirteen types ship" over a table of thirteen while
// the registry held fifteen. The count is generated now, and the table has to
// name every type the registry holds, so a type added in one change cannot reach
// the page unlisted.
func TestFactsCoverTheCredentialTypeCountOfTheCredentialsPage(t *testing.T) {
	list, err := facts()
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}
	const path = "docs/src/content/docs/concepts/credentials.md"
	for _, entry := range list {
		if entry.path != path {
			continue
		}
		body, err := entry.body()
		if err != nil {
			t.Fatal(err)
		}
		want := strconv.Itoa(len(credentials.Default().List()))
		if !strings.HasPrefix(body, want+" credential types ship") {
			t.Fatalf("the credentials page's sentence is %q, want it to open with %q", body, want+" credential types ship")
		}

		root, err := splice.RepoRoot()
		if err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(path)))
		if err != nil {
			t.Fatal(err)
		}
		section, ok := builtInCatalogueSection(string(raw))
		if !ok {
			t.Fatalf("%s has no \"## The built-in catalogue\" section", path)
		}
		var ids []string
		for _, credentialType := range credentials.Default().List() {
			ids = append(ids, credentialType.ID)
		}
		for _, id := range missingTableRows(section, ids) {
			t.Errorf("%s has no row for the credential type %s in its built-in catalogue table", path, id)
		}

		return
	}
	t.Fatalf("facts() has no section for %s", path)
}

// builtInCatalogueSection is the text of the credentials page between the
// "The built-in catalogue" heading and the next second-level heading. The page
// names the same ids in other tables (the default allowed domains, say), and a
// row there must not stand in for the catalogue's own.
func builtInCatalogueSection(page string) (string, bool) {
	const heading = "\n## The built-in catalogue\n"
	start := strings.Index(page, heading)
	if start < 0 {
		return "", false
	}
	section := page[start+len(heading):]
	if end := strings.Index(section, "\n## "); end >= 0 {
		section = section[:end]
	}

	return section, true
}

// missingTableRows lists the ids that have no row of their own: a line that
// opens with the id in backticks as the first cell.
func missingTableRows(section string, ids []string) []string {
	var missing []string
	for _, id := range ids {
		if !regexp.MustCompile("(?m)^\\| `" + regexp.QuoteMeta(id) + "` \\|").MatchString(section) {
			missing = append(missing, id)
		}
	}

	return missing
}

// Planted: a removed row has to be reported, and a row in some other table of
// the page, or an id that only appears inside a longer first cell, is not the
// catalogue's row.
func TestMissingTableRowsSeesARemovedRowAndIgnoresOtherTables(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, "docs", "src", "content", "docs", "concepts", "credentials.md"))
	if err != nil {
		t.Fatal(err)
	}
	section, ok := builtInCatalogueSection(string(raw))
	if !ok {
		t.Fatal("the credentials page has no built-in catalogue section")
	}
	if missing := missingTableRows(section, []string{"httpBearerAuth", "openAiApi"}); len(missing) != 0 {
		t.Fatalf("missingTableRows() = %v on the committed section, want none", missing)
	}

	var kept []string
	for _, line := range strings.Split(section, "\n") {
		if !strings.HasPrefix(line, "| `openAiApi` |") {
			kept = append(kept, line)
		}
	}
	if missing := missingTableRows(strings.Join(kept, "\n"), []string{"openAiApi", "httpBearerAuth"}); len(missing) != 1 || missing[0] != "openAiApi" {
		t.Fatalf("missingTableRows() = %v after removing the openAiApi row, want [openAiApi]", missing)
	}

	// The default-domains table names openAiApi too, but it is outside the section.
	if !strings.Contains(string(raw), "| `openAiApi` | the host of") {
		t.Fatal("the page no longer has a second table naming openAiApi; the planted case has lost its point")
	}
	if strings.Contains(section, "the host of the credential's own") {
		t.Fatal("builtInCatalogueSection() reaches into the default-domains table")
	}
	if missing := missingTableRows("| `googleDriveOAuth2Api`, `gmailOAuth2` | the Google API and sign-in hosts |", []string{"gmailOAuth2"}); len(missing) != 1 {
		t.Fatalf("a shared first cell satisfied the check: %v", missing)
	}
}

// The contract page quotes the operation count in prose. The sentence is
// generated now, and it has to agree with the handler registrations the
// OpenAPI document is generated from — the same count the page tells a reader
// to grep for by hand.
func TestFactsCoverTheOperationCountOfTheContractPage(t *testing.T) {
	list, err := facts()
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}
	const path = "docs/src/content/docs/reference/api-contract.md"
	for _, entry := range list {
		if entry.path != path {
			continue
		}
		root, err := splice.RepoRoot()
		if err != nil {
			t.Fatal(err)
		}
		want := "The document a server serves holds " + strconv.Itoa(operationIDCount(t, root)) + " operations"
		body, err := entry.body()
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(body, want) {
			t.Fatalf("the contract page's sentence is %q, want it to open with %q", body, want)
		}

		return
	}
	t.Fatalf("facts() has no section for %s", path)
}

// operationIDCount is the number of distinct operation ids registered in the
// handler source — the same sweep apiOperationsSentence makes, restated here so
// the test compares the sentence against the source rather than against itself.
func operationIDCount(t *testing.T, root string) int {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(root, "internal", "api", "handlers", "*.go"))
	if err != nil {
		t.Fatal(err)
	}
	ids := map[string]bool{}
	for _, file := range files {
		if strings.HasSuffix(file, "_test.go") {
			continue
		}
		raw, err := os.ReadFile(file)
		if err != nil {
			t.Fatal(err)
		}
		for _, match := range operationID.FindAllStringSubmatch(string(raw), -1) {
			ids[match[1]] = true
		}
	}
	if len(ids) == 0 {
		t.Fatal("no OperationID registrations found — the handlers moved?")
	}

	return len(ids)
}

// The committed pages are the real subject: this is the same comparison
// `make generate-docs-facts-check` makes, run by `go test ./...` so a node added
// in one change cannot reach main with the docs' size untouched.
func TestTheCommittedPagesHoldTheGeneratedFacts(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	list, err := facts()
	if err != nil {
		t.Fatalf("facts() error = %v", err)
	}
	if len(list) == 0 {
		t.Fatal("facts() lists nothing")
	}
	if err := apply(root, list, true, &bytes.Buffer{}); err != nil {
		t.Fatal(err)
	}
}
