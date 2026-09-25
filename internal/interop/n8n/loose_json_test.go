package n8n_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// BUG-w18vn3: n8n stores a workflow as JSON it never validates against a
// schema, so a typeVersion written as "3.4" loads there and ran — and here the
// whole file was refused with a Go decode error.

func issueFor(issues []n8n.ImportIssue, nodeName, field string) (n8n.ImportIssue, bool) {
	for _, issue := range issues {
		if issue.NodeName == nodeName && issue.Field == field {
			return issue, true
		}
	}
	return n8n.ImportIssue{}, false
}

// rawGoDecodeError reports the fragments encoding/json puts in its messages.
// None of them means anything to a person holding an n8n export.
func rawGoDecodeError(message string) bool {
	for _, fragment := range []string{"cannot unmarshal", "Go struct", "Go value", "json:"} {
		if strings.Contains(message, fragment) {
			return true
		}
	}
	return false
}

func TestImportReadsANumericStringTypeVersionAsThatNumber(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "String versions",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": [0, 0]},
	    {"id": "b", "name": "Edit Fields", "type": "n8n-nodes-base.set", "typeVersion": "3.4", "position": [220, 0],
	     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "status", "type": "string", "value": "ready"}]}}},
	    {"id": "c", "name": "Code", "type": "n8n-nodes-base.code", "typeVersion": "2", "position": [440, 0],
	     "parameters": {"jsCode": "return $input.all();"}}
	  ],
	  "connections": {
	    "Manual": {"main": [[{"node": "Edit Fields", "type": "main", "index": 0}]]},
	    "Edit Fields": {"main": [[{"node": "Code", "type": "main", "index": 0}]]}
	  }
	}`), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v, want a string typeVersion read as its number", err)
	}
	if got := nodeByName(result.Document, "Edit Fields").TypeVersion; got.String() != "3.4" {
		t.Errorf("Edit Fields typeVersion = %s, want 3.4", got)
	}
	if got := nodeByName(result.Document, "Code").TypeVersion; got.Compare(workflow.V(2)) != 0 {
		t.Errorf("Code typeVersion = %s, want 2", got)
	}
	for _, issue := range result.Unsupported {
		if issue.Field == "typeVersion" {
			t.Errorf("unexpected typeVersion issue %#v: a numeric string is the number n8n reads it as", issue)
		}
	}
	document := result.Document
	document.ID = "wf"
	if _, err := workflow.Compile(document, registry(t)); err != nil {
		t.Fatalf("Compile() error = %v, want the imported workflow to activate", err)
	}
}

func TestImportNamesATypeVersionThatIsNotANumber(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Word version",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
	    {"id": "b", "name": "Edit Fields", "type": "n8n-nodes-base.set", "typeVersion": "latest",
	     "parameters": {"assignments": {"assignments": [{"id": "1", "name": "status", "type": "string", "value": "ready"}]}}}
	  ],
	  "connections": {"Manual": {"main": [[{"node": "Edit Fields", "type": "main", "index": 0}]]}}
	}`), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v, want the file imported with the one node named", err)
	}
	issue, found := issueFor(result.Unsupported, "Edit Fields", "typeVersion")
	if !found {
		t.Fatalf("unsupported = %#v, want an issue naming Edit Fields' typeVersion", result.Unsupported)
	}
	if issue.NodeID != "b" || issue.Type != "n8n-nodes-base.set" {
		t.Errorf("issue = %#v, want it to name node b and its n8n type", issue)
	}
	if !strings.Contains(issue.Reason, `"latest"`) || rawGoDecodeError(issue.Reason) {
		t.Errorf("reason = %q, want the value quoted in a plain sentence", issue.Reason)
	}
	// The node still lands on a version, the one the mapping assumes when
	// n8n names none, so the rest of the workflow is usable.
	if got := nodeByName(result.Document, "Edit Fields").TypeVersion; got.IsZero() {
		t.Errorf("Edit Fields typeVersion is empty, want the mapping's default")
	}
}

func TestImportReadsNumericStringsInTheOtherNumericNodeFields(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Loose numbers",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1, "position": ["0", "0"]},
	    {"id": "b", "name": "Fetch", "type": "n8n-nodes-base.httpRequest", "typeVersion": 4.2, "position": ["220", "40.5"],
	     "retryOnFail": true, "maxTries": "4", "waitBetweenTries": "2000",
	     "parameters": {"url": "https://example.com"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node": "Fetch", "type": "main", "index": "0"}]]}}
	}`), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v, want numeric strings read as numbers", err)
	}
	fetch := nodeByName(result.Document, "Fetch")
	if fetch.Position.X != 220 || fetch.Position.Y != 40.5 {
		t.Errorf("position = %#v, want 220, 40.5", fetch.Position)
	}
	if got := fetch.Settings["maxTries"]; got != float64(4) {
		t.Errorf("maxTries = %#v, want 4", got)
	}
	if got := fetch.Settings["waitBetweenTries"]; got != float64(2000) {
		t.Errorf("waitBetweenTries = %#v, want 2000", got)
	}
	if len(result.Document.Connections) != 1 {
		t.Errorf("connections = %#v, want the edge whose index was written as \"0\"", result.Document.Connections)
	}
}

func TestImportNamesANonNumericRetryBudget(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Word retries",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
	    {"id": "b", "name": "Fetch", "type": "n8n-nodes-base.httpRequest", "typeVersion": 4.2,
	     "retryOnFail": true, "maxTries": "several", "parameters": {"url": "https://example.com"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node": "Fetch", "type": "main", "index": 0}]]}}
	}`), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v, want the file imported with the field named", err)
	}
	issue, found := issueFor(result.Unsupported, "Fetch", "maxTries")
	if !found {
		t.Fatalf("unsupported = %#v, want an issue naming Fetch's maxTries", result.Unsupported)
	}
	if issue.Severity != n8n.SeverityDropped || !strings.Contains(issue.Reason, `"several"`) {
		t.Errorf("issue = %#v, want a dropped maxTries quoting the value", issue)
	}
	if _, carried := nodeByName(result.Document, "Fetch").Settings["maxTries"]; carried {
		t.Errorf("maxTries was carried, want it left to the default")
	}
}

// n8n's engine only honours these flags when they are literally true, so a
// string "true" is off there — and must be off here, or an import would switch
// on behaviour the workflow never had.
func TestImportReadsNonBooleanFlagsAsN8NDoes(t *testing.T) {
	t.Parallel()

	result, err := n8n.Import([]byte(`{
	  "name": "Loose flags",
	  "nodes": [
	    {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
	    {"id": "b", "name": "Fetch", "type": "n8n-nodes-base.httpRequest", "typeVersion": 4.2,
	     "disabled": "true", "retryOnFail": 1, "executeOnce": "yes", "alwaysOutputData": null,
	     "parameters": {"url": "https://example.com"}}
	  ],
	  "connections": {"Manual": {"main": [[{"node": "Fetch", "type": "main", "index": 0}]]}}
	}`), registry(t))
	if err != nil {
		t.Fatalf("Import() error = %v, want non-boolean flags read as n8n reads them", err)
	}
	fetch := nodeByName(result.Document, "Fetch")
	if fetch.Disabled {
		t.Errorf("Fetch is disabled, want the string \"true\" read as off, as n8n's engine does")
	}
	for _, key := range []string{"retryOnFail", "executeOnce", "alwaysOutputData"} {
		if _, set := fetch.Settings[key]; set {
			t.Errorf("setting %s = %#v, want it off", key, fetch.Settings[key])
		}
	}
}

// What n8n itself cannot read is still refused, but in words that name the
// node and the field rather than the Go struct the adapter decodes into.
func TestImportRefusesAMistypedFieldInPlainWords(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name, payload string
		want          []string
	}{
		{
			name: "node field",
			payload: `{"name": "W", "nodes": [
			  {"id": "a", "name": "Edit Fields", "type": "n8n-nodes-base.set", "typeVersion": 3.4, "parameters": ["not", "an", "object"]}
			], "connections": {}}`,
			want: []string{`"Edit Fields"`, "parameters"},
		},
		{
			name:    "workflow field",
			payload: `{"name": "W", "nodes": {"id": "a"}, "connections": {}}`,
			want:    []string{"nodes"},
		},
		{
			name: "connection index",
			payload: `{"name": "W", "nodes": [
			  {"id": "a", "name": "Manual", "type": "n8n-nodes-base.manualTrigger", "typeVersion": 1},
			  {"id": "b", "name": "Edit Fields", "type": "n8n-nodes-base.set", "typeVersion": 3.4}
			], "connections": {"Manual": {"main": [[{"node": "Edit Fields", "type": "main", "index": "first"}]]}}}`,
			want: []string{`"Edit Fields"`, "index", `"first"`},
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			_, err := n8n.Import([]byte(tc.payload), registry(t))
			if err == nil {
				t.Fatalf("Import() succeeded, want a refusal")
			}
			if rawGoDecodeError(err.Error()) {
				t.Errorf("error = %q, want no Go decoding vocabulary", err)
			}
			for _, fragment := range tc.want {
				if !strings.Contains(err.Error(), fragment) {
					t.Errorf("error = %q, want it to mention %s", err, fragment)
				}
			}
		})
	}
}
