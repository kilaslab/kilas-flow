package tenantpurge_test

// The operator-facing documentation names the steps of a deletion in order, and
// an operator reading that page while a half-finished deletion is stuck needs
// the names to be the ones the service reports. The page is the only place a
// step name reaches a person, so this test compares the two rather than trusting
// that a step rename was also made in prose.

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// documentedPage is the page this test reads, relative to this package.
const documentedPage = "../../docs/src/content/docs/operate/tenant-deletion.md"

func TestDocumentedOrderMatchesTheOrchestrator(t *testing.T) {
	page := documentedPageText(t)

	service := fakes(&recorder{}, stubRows{}).service(t)
	steps := service.Steps()
	if len(steps) == 0 {
		t.Fatal("Steps() is empty")
	}

	// Every step name is written in backticks on the page, so the names a table
	// row claims are the names a log line carries. Their first appearances have
	// to be in the order the steps run: a page that lists them out of order is
	// wrong in the one way that matters to somebody following it.
	previous, previousName := -1, ""
	for _, step := range steps {
		at := strings.Index(page, "`"+step.Name+"`")
		if at < 0 {
			t.Errorf("the page never names the %q step in backticks; every step is documented", step.Name)
			continue
		}
		if at < previous {
			t.Errorf("the page names %q before %q, but Steps() runs them the other way round",
				step.Name, previousName)
		}
		previous, previousName = at, step.Name
	}

	// The tables matter as much as the order: the page tells an operator what
	// was removed, so a table a step newly covers has to be named there rather
	// than only in the counts of a response.
	for _, table := range service.Tables() {
		if !strings.Contains(page, "`"+table+"`") {
			t.Errorf("the page never names the %q table, which the purge removes", table)
		}
	}
}

// TestDocumentedResponseExampleNamesEveryTableTheServiceReports pins the key set
// of the page's response example to the key set the endpoint returns. The
// service seeds Result.Removed with every name Tables() returns before any step
// runs, so the example is the contract an operator compares their own answer
// against, and it is wrong the moment a step gains or loses a table.
//
// The four vector_documents_* widths are the case that catches people: every
// dialect reports them at zero, including a deployment that has no such table.
func TestDocumentedResponseExampleNamesEveryTableTheServiceReports(t *testing.T) {
	reported := documentedExampleRemoved(t, documentedPageText(t))
	covered := map[string]struct{}{}
	for _, table := range fakes(&recorder{}, stubRows{}).service(t).Tables() {
		covered[table] = struct{}{}
		if _, ok := reported[table]; !ok {
			t.Errorf("the response example is missing %q, which every purge reports", table)
		}
	}
	for table := range reported {
		if _, ok := covered[table]; !ok {
			t.Errorf("the response example reports %q, which no step removes", table)
		}
	}
}

// documentedPageText reads the page these tests assert against.
func documentedPageText(t *testing.T) string {
	t.Helper()
	contents, err := os.ReadFile(filepath.Clean(documentedPage))
	if err != nil {
		t.Fatalf("read %s: %v", documentedPage, err)
	}
	return string(contents)
}

// documentedExampleRemoved reads the removed map out of the page's JSON response
// example.
func documentedExampleRemoved(t *testing.T, page string) map[string]int64 {
	t.Helper()
	const fence = "```json"
	open := strings.Index(page, fence)
	if open < 0 {
		t.Fatalf("the page has no %s response example", fence)
	}
	body := page[open+len(fence):]
	end := strings.Index(body, "```")
	if end < 0 {
		t.Fatalf("the page's %s example is not closed", fence)
	}
	var example struct {
		Removed map[string]int64 `json:"removed"`
	}
	if err := json.Unmarshal([]byte(body[:end]), &example); err != nil {
		t.Fatalf("the page's response example is not JSON: %v", err)
	}
	if len(example.Removed) == 0 {
		t.Fatal("the page's response example has no removed object")
	}
	return example.Removed
}
