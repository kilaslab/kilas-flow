package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/docexamples"
	"github.com/kilaslab/kilas-flow/internal/shippedcatalogue"
	"github.com/kilaslab/kilas-flow/scripts/internal/splice"
)

func TestVariablesUsedFindsEveryShellVariableOfAnExample(t *testing.T) {
	got := variablesUsed(`kilasflow debug eval '$json.n' --execution "$EXECUTION_ID" --node ${NODE_ID} --token $TOKEN "$wf"`)
	want := []string{"EXECUTION_ID", "NODE_ID", "TOKEN"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("variablesUsed() = %q, want %q (an expression's $json and a lowercase loop variable are not seeded)", got, want)
	}
}

func TestNeedsOfSaysWhatABlockHasToHaveBeforeItRuns(t *testing.T) {
	needed, unknown := needsOf([]string{
		`kilasflow run "$WORKFLOW_ID" --revision "$VERSION_ID"`,
		`kilasflow exec get "$EXECUTION_ID"`,
		`kilasflow tenant get acme`,
		`kilasflow health --token "$TOKEN"`,
	})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %q, want none", unknown)
	}
	for name, got := range map[string]bool{
		"workflow": needed.workflow, "version": needed.version, "execution": needed.execution, "tenant": needed.tenant,
	} {
		if !got {
			t.Errorf("needs.%s = false, want true", name)
		}
	}
	if needed.credential || needed.datastore {
		t.Errorf("needs = %+v, want no credential or datastore", needed)
	}
}

// A credential a workflow attaches is its own seed: deleting it is refused,
// which is the example, and the block's plain $CREDENTIAL_ID stays unattached.
func TestAnInUseCredentialIsSeededApartFromAPlainOne(t *testing.T) {
	needed, unknown := needsOf([]string{`kilasflow credential delete "$IN_USE_CREDENTIAL_ID" --yes`})
	if len(unknown) != 0 {
		t.Fatalf("unknown = %q, want none", unknown)
	}
	if !needed.inUseCredential || needed.credential || needed.workflow {
		t.Fatalf("needs = %+v, want only the in-use credential", needed)
	}
	document, err := inUseWorkflowDocument([]byte(`{"nodes":[{"id":"http_request","type":"kilasflow.httpRequest"},{"id":"set","type":"kilasflow.set"}]}`), "cred_1")
	if err != nil {
		t.Fatalf("inUseWorkflowDocument() error = %v", err)
	}
	var decoded struct {
		Nodes []struct {
			ID          string            `json:"id"`
			Credentials map[string]string `json:"credentials"`
		} `json:"nodes"`
	}
	if err := json.Unmarshal(document, &decoded); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded.Nodes[0].Credentials["httpHeaderAuth"] != "cred_1" || len(decoded.Nodes[1].Credentials) != 0 {
		t.Fatalf("document = %s, want the HTTP node alone to attach the credential", document)
	}
}

func TestNeedsOfRefusesAVariableNothingSeeds(t *testing.T) {
	_, unknown := needsOf([]string{`kilasflow workflow get "$MYSTERY_ID"`, `kilasflow workflow get "$WORKFLOW_ID"`})
	if want := []string{"MYSTERY_ID"}; !reflect.DeepEqual(unknown, want) {
		t.Fatalf("unknown = %q, want %q", unknown, want)
	}
}

func TestAnExecutionIsRunFromAWorkflowAndAVersionIsReadFromOne(t *testing.T) {
	needed, _ := needsOf([]string{`kilasflow exec get "$EXECUTION_ID"`})
	if !needed.workflow || !needed.execution {
		t.Errorf("an execution needs a workflow to run: %+v", needed)
	}
	needed, _ = needsOf([]string{`kilasflow workflow get-version "$WORKFLOW_ID" "$VERSION_ID"`})
	if !needed.workflow || !needed.version {
		t.Errorf("a version belongs to a workflow: %+v", needed)
	}
}

// The cursor an example passes has to come from a page of the same listing, so
// the probe is the example's own command asking for one item.
func TestProbeCommandAsksTheSameListingForOnePage(t *testing.T) {
	got, err := probeCommand([]string{"kilasflow", "workflow", "versions", "$WORKFLOW_ID", "--limit", "50", "--cursor", "$CURSOR"})
	if err != nil {
		t.Fatalf("probeCommand() error = %v", err)
	}
	want := `kilasflow workflow versions "$WORKFLOW_ID" --limit 1 --json`
	if got != want {
		t.Fatalf("probeCommand() = %q, want %q", got, want)
	}
}

func TestProbeCommandRefusesAnExampleWithNoCursor(t *testing.T) {
	if _, err := probeCommand([]string{"kilasflow", "workflow", "list"}); err == nil {
		t.Fatal("probeCommand() accepted a command that does not take a cursor")
	}
}

func TestShellQuoteKeepsVariablesExpandableAndEverythingElseLiteral(t *testing.T) {
	for word, want := range map[string]string{
		"plain":      `"plain"`,
		"$WORKFLOW":  `"$WORKFLOW"`,
		`a"b`:        `"a\"b"`,
		"back`tick":  "\"back\\`tick\"",
		`back\slash`: `"back\\slash"`,
		"with space": `"with space"`,
		"":           `""`,
	} {
		if got := shellQuote(word); got != want {
			t.Errorf("shellQuote(%q) = %s, want %s", word, got, want)
		}
	}
}

func TestCountServedSplitsTheServedCatalogueBySourceAndCountsTypesOnce(t *testing.T) {
	got := countServed([]servedNode{
		{Type: "kilasflow.set", Source: "builtin"},
		{Type: "kilasflow.set", Source: "builtin"},
		{Type: "kilasflow.http", Source: "builtin"},
		{Type: "telegram.send", Source: "pack"},
		{Type: "operator.thing", Source: "sidecar"},
	})
	want := shippedcatalogue.Counts{
		Compiled: shippedcatalogue.Size{Types: 2, Pairs: 3},
		Packs:    shippedcatalogue.Size{Types: 1, Pairs: 1},
	}
	if got != want {
		t.Fatalf("countServed() = %+v, want %+v", got, want)
	}
}

func TestGeneratedBlockReadsTheTextBetweenTheMarkers(t *testing.T) {
	page := "a\n<!-- generated:x -->\n  the sentence.  \n<!-- /generated:x -->\nb"
	if got, ok := generatedBlock(page, "<!-- generated:x -->", "<!-- /generated:x -->"); !ok || got != "the sentence." {
		t.Fatalf("generatedBlock() = %q, %v", got, ok)
	}
	if _, ok := generatedBlock("no markers", "<!-- generated:x -->", "<!-- /generated:x -->"); ok {
		t.Fatal("generatedBlock() found a block in a page with no markers")
	}
}

func TestEveryFixtureIsJSONOnceTheServerAddressIsFilledIn(t *testing.T) {
	entries, err := fixtures.ReadDir("testdata")
	if err != nil {
		t.Fatalf("read the embedded fixtures: %v", err)
	}
	if len(entries) < 4 {
		t.Fatalf("found %d fixtures, want at least the workflow, the draft, the payload and the credential", len(entries))
	}
	for _, entry := range entries {
		raw, err := fixtures.ReadFile("testdata/" + entry.Name())
		if err != nil {
			t.Fatal(err)
		}
		rendered := strings.ReplaceAll(string(raw), serverURLPlaceholder, "http://127.0.0.1:1")
		var decoded any
		if err := json.Unmarshal([]byte(rendered), &decoded); err != nil {
			t.Errorf("%s is not JSON: %v", entry.Name(), err)
		}
	}
}

// What the page runs is read from the page itself, so this is the guard that a
// new example does not slip in a variable the runner cannot seed, or a file the
// runner does not provide, before CI is the one to find out.
func TestEveryVariableAndFileCliMdUsesIsProvidedByTheRunner(t *testing.T) {
	root, err := splice.RepoRoot()
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(root, filepath.FromSlash(defaultPage)))
	if err != nil {
		t.Fatalf("read cli.md: %v", err)
	}

	provided := map[string]bool{"packs": true}
	entries, _ := fixtures.ReadDir("testdata")
	for _, entry := range entries {
		provided[entry.Name()] = true
	}

	examples := docexamples.Extract(string(raw))
	runnable := 0
	for _, example := range examples {
		if example.ServerPath {
			continue
		}
		runnable++
		if _, unknown := needsOf([]string{example.Text}); len(unknown) > 0 {
			t.Errorf("cli.md:%d %q uses %v, which the runner cannot seed", example.Line, example.Text, unknown)
		}
		// Input files named on the command line must exist in the scratch directory.
		for _, word := range example.Words {
			if strings.HasSuffix(word, ".json") && !strings.ContainsAny(word, "$ ") && !provided[word] &&
				!strings.HasPrefix(word, "./packs/") {
				t.Errorf("cli.md:%d %q reads %s, which scripts/cli-examples/testdata does not provide", example.Line, example.Text, word)
			}
		}
	}
	if runnable < floor {
		t.Errorf("cli.md has %d runnable examples; the runner insists on at least %d, so a page that lost its examples fails here first", runnable, floor)
	}
}
