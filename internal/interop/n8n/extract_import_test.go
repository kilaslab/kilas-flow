package n8n_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// The extractFromFile importer used to know only json/extractfromjson, so a
// template whose node says `fromJson` — n8n's own spelling, used by template
// 3647 — was rejected, while a node with no operation at all silently took
// this server's pdf default instead of n8n's csv one.
func TestImportExtractFromFileOperations(t *testing.T) {
	t.Parallel()

	fixture := func(operation string) string {
		value := ""
		if operation != "" {
			value = `"operation": "` + operation + `"`
		}
		return `{
			"name": "probe",
			"nodes": [
				{ "id": "e1", "name": "Extract", "type": "n8n-nodes-base.extractFromFile", "typeVersion": 1,
				  "position": [0, 0], "parameters": {` + value + `} }
			],
			"connections": {}
		}`
	}

	cases := map[string]string{
		"fromJson":         "json",
		"json":             "json",
		"extractFromJson":  "json",
		"":                 "csv",
		"csv":              "csv",
		"xlsx":             "xlsx",
		"binaryToPropery":  "binarytoproperty",
		"binaryToProperty": "binarytoproperty",
		"pdf":              "pdf",
		"text":             "text",
	}
	for given, want := range cases {
		result, err := n8n.Import([]byte(fixture(given)), registry(t))
		if err != nil {
			t.Fatalf("Import(%q) error = %v", given, err)
		}
		if len(result.Unsupported) != 0 {
			t.Fatalf("Import(%q) unsupported = %#v, want none", given, result.Unsupported)
		}
		node := nodeByName(result.Document, "Extract")
		if node.Parameters["operation"] != want {
			t.Errorf("Import(%q).operation = %#v, want %q", given, node.Parameters["operation"], want)
		}
	}

	// An operation with no reader is a blocking import issue, never a silent
	// run-time failure: 34 of the 94 sampled instances named an operation the
	// node could not run, and the import reported none of them.
	for _, given := range []string{"ods", "fromIcs", "rtf", "xml", "html", "xls"} {
		result, err := n8n.Import([]byte(fixture(given)), registry(t))
		if err != nil {
			t.Fatalf("Import(%q) error = %v", given, err)
		}
		found := false
		for _, issue := range result.Unsupported {
			if issue.Field == "operation" && issue.Severity == n8n.SeverityBlocking {
				found = true
			}
		}
		if !found {
			t.Errorf("Import(%q) = %#v, want a blocking operation issue", given, result.Unsupported)
		}
	}
}
