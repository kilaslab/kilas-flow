package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// Every write a tool makes is RAW here, where n8n's default is USER_ENTERED, so
// a model's value that starts with "=" stays text. An import of a writing tool
// says so; a reading tool writes nothing and has nothing to say.
func TestAGoogleSheetsWriteToolImportsWithANoteThatItWritesRaw(t *testing.T) {
	t.Parallel()

	result := importFixture(t, agentWithTool(sheetsToolNode))
	issue, found := issueOn(result, "cellFormat")
	if !found || issue.Severity != n8n.SeverityLossy || !strings.Contains(issue.Reason, "RAW") {
		t.Fatalf("cellFormat issue = %+v (found %v), want a lossy note naming RAW", issue, found)
	}

	result = importFixture(t, agentWithTool(`{"id":"d","name":"Tool","type":"n8n-nodes-base.googleSheetsTool","typeVersion":4.7,"position":[400,200],
	  "parameters":{"operation":"read","documentId":"1abc","sheetName":"gid=0","descriptionType":"manual","toolDescription":"Reads"}}`))
	if _, found := issueOn(result, "cellFormat"); found {
		t.Error("a reading tool reported that it writes RAW")
	}
}

// A model-chosen start column can reach the column names, so a tool deletes
// rows only.
func TestAGoogleSheetsToolThatDeletesColumnsIsBlocked(t *testing.T) {
	t.Parallel()

	result := importFixture(t, agentWithTool(`{"id":"d","name":"Tool","type":"n8n-nodes-base.googleSheetsTool","typeVersion":4.7,"position":[400,200],
	  "parameters":{"operation":"delete","toDelete":"columns","startIndex":"={{ $fromAI('Column', '', 'string') }}",
	    "documentId":"1abc","sheetName":"gid=0","descriptionType":"manual","toolDescription":"Deletes a column"}}`))
	issue, found := issueOn(result, "toDelete")
	if !found || issue.Severity != n8n.SeverityBlocking {
		t.Fatalf("toDelete issue = %+v (found %v), want a blocking issue", issue, found)
	}
}
