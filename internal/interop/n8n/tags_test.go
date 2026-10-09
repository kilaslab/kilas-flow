package n8n_test

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// taggedFixture is a one-node n8n workflow whose "tags" is the given JSON.
func taggedFixture(tags string) string {
	body := `{
	  "name": "Tagged",
	  "nodes": [{"id":"a","name":"Manual","type":"n8n-nodes-base.manualTrigger","typeVersion":1,"position":[0,0],"parameters":{}}],
	  "connections": {}`
	if tags != "" {
		body += `,
	  "tags": ` + tags
	}
	return body + "\n}"
}

func tagIssues(result n8n.ImportResult) []n8n.ImportIssue {
	var issues []n8n.ImportIssue
	for _, issue := range result.Unsupported {
		if issue.Field == "tags" {
			issues = append(issues, issue)
		}
	}
	return issues
}

// exportedTagNames reads the names back out of an export's "tags" the way n8n
// reads them: as the "name" of each object, in order.
func exportedTagNames(t *testing.T, document n8n.Document) []string {
	t.Helper()
	encoded, err := json.Marshal(document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	var shape struct {
		Tags []map[string]any `json:"tags"`
	}
	if err := json.Unmarshal(encoded, &shape); err != nil {
		t.Fatalf("export is not the n8n shape: %v (%s)", err, encoded)
	}
	names := make([]string, 0, len(shape.Tags))
	for _, tag := range shape.Tags {
		if len(tag) != 1 {
			t.Errorf("an exported tag = %v, want only a name: ids are instance-local and are never invented", tag)
		}
		name, _ := tag["name"].(string)
		names = append(names, name)
	}
	return names
}

// An export from n8n writes each tag with an instance-local id and timestamps.
// Names are the identity: the import keeps them, in order, and the export writes
// them back as bare {"name"} objects, which n8n matches by name or creates.
func TestAWorkflowsTagsRoundTrip(t *testing.T) {
	t.Parallel()

	imported := importFixture(t, taggedFixture(`[
	  {"id":"3uJ2tMBeFKZLPOch","name":"Prod","createdAt":"2025-01-02T03:04:05.000Z","updatedAt":"2025-01-02T03:04:05.000Z"},
	  {"id":"zz9","name":"billing","createdAt":"2025-02-01T00:00:00.000Z","updatedAt":"2025-02-01T00:00:00.000Z"}
	]`))
	if got := strings.Join(imported.Document.Tags, "|"); got != "Prod|billing" {
		t.Fatalf("imported tags = %q, want Prod|billing in file order", got)
	}
	if issues := tagIssues(imported); len(issues) != 0 {
		t.Errorf("a file whose tags all carry reported %+v, want nothing about tags", issues)
	}
	if err := workflow.ValidateDraftWithServerID(imported.Document); err != nil {
		t.Errorf("the imported document does not validate: %v", err)
	}

	exported, err := n8n.Export(imported.Document, registry(t))
	if err != nil {
		t.Fatalf("Export() error = %v", err)
	}
	if got := strings.Join(exportedTagNames(t, exported.Document), "|"); got != "Prod|billing" {
		t.Errorf("exported tags = %q, want Prod|billing", got)
	}
	for _, issue := range exported.Lossy {
		if issue.Field == "tags" {
			t.Errorf("the export reported %+v about tags it carried", issue)
		}
	}

	// And back in again: the same names, nothing reported.
	encoded, err := json.Marshal(exported.Document)
	if err != nil {
		t.Fatalf("Marshal() error = %v", err)
	}
	again := importFixture(t, string(encoded))
	if got := strings.Join(again.Document.Tags, "|"); got != "Prod|billing" {
		t.Errorf("tags after import, export and import = %q, want Prod|billing", got)
	}
	if issues := tagIssues(again); len(issues) != 0 {
		t.Errorf("re-importing an export reported %+v about tags", issues)
	}
}

func TestAnUntaggedExportHasNoTagsKey(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]string{
		"no key":      taggedFixture(""),
		"empty array": taggedFixture(`[]`),
		"null":        taggedFixture(`null`),
	} {
		imported := importFixture(t, fixture)
		if len(imported.Document.Tags) != 0 {
			t.Errorf("%s: imported tags = %q, want none", name, imported.Document.Tags)
		}
		if issues := tagIssues(imported); len(issues) != 0 {
			t.Errorf("%s: reported %+v, want nothing about tags", name, issues)
		}
		exported, err := n8n.Export(imported.Document, registry(t))
		if err != nil {
			t.Fatalf("%s: Export() error = %v", name, err)
		}
		encoded, err := json.Marshal(exported.Document)
		if err != nil {
			t.Fatalf("%s: Marshal() error = %v", name, err)
		}
		if strings.Contains(string(encoded), `"tags"`) {
			t.Errorf("%s: the export of an untagged workflow = %s, want no tags key", name, encoded)
		}
	}
}

// What the importer cannot keep is named, and never refuses the file: the
// workflow arrives with the tags it could keep.
func TestImportedTagsStayWithinWhatAWorkflowMayCarry(t *testing.T) {
	t.Parallel()

	twentyOne := make([]string, 21)
	for index := range twentyOne {
		twentyOne[index] = fmt.Sprintf(`{"name":"tag-%02d"}`, index)
	}
	tests := []struct {
		name     string
		tags     string
		want     string
		severity n8n.IssueSeverity
		reasons  []string
	}{
		{"a bare string is an id, not a name", `["abc123",{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"abc123"}},
		{"an entry with no name", `[{"id":"x"},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"name"}},
		{"an empty name", `[{"name":""},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"empty"}},
		{"a blank name", `[{"name":"   "},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"empty"}},
		{"a name that is not text", `[{"name":7},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"name"}},
		{"an entry that is not an object", `[7,null,[],{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"7", "null", "[]"}},
		{"a name over the limit", `[{"name":"` + strings.Repeat("x", 25) + `"},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"24"}},
		{"a comma", `[{"name":"a,b"},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"comma"}},
		{"a control character", `[{"name":"a\u0007b"},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"control"}},
		{"a case-insensitive duplicate", `[{"name":"Prod"},{"name":"prod"},{"name":"billing"}]`, "Prod|billing", n8n.SeverityDropped, []string{"prod", "duplicate"}},
		{"a padded name that is then refused is dropped, not lossy", `[{"name":"  a,b "},{"name":"Prod"}]`, "Prod", n8n.SeverityDropped, []string{"comma"}},
		{"a name padded with spaces is trimmed", `[{"name":"  Prod "}]`, "Prod", n8n.SeverityLossy, []string{"trimmed"}},
		{"more than twenty tags", "[" + strings.Join(twentyOne, ",") + "]", strings.Join(tagNames(twentyOne[:20]), "|"), n8n.SeverityDropped, []string{"20"}},
		{"tags that is an object", `{"name":"Prod"}`, "", n8n.SeverityDropped, []string{"list"}},
		{"tags that is a string", `"Prod"`, "", n8n.SeverityDropped, []string{"list"}},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result, err := n8n.Import([]byte(taggedFixture(test.tags)), registry(t))
			if err != nil {
				t.Fatalf("Import() error = %v, want the file imported without these tags", err)
			}
			if got := strings.Join(result.Document.Tags, "|"); got != test.want {
				t.Errorf("kept tags = %q, want %q", got, test.want)
			}
			if err := workflow.ValidateDraftWithServerID(result.Document); err != nil {
				t.Errorf("what was kept does not validate: %v", err)
			}
			issues := tagIssues(result)
			if len(issues) == 0 {
				t.Fatal("nothing was reported about the tags that could not be kept")
			}
			joined := ""
			for _, issue := range issues {
				joined += issue.Reason + "\n"
				if issue.NodeID != "" || issue.NodeName != "" {
					t.Errorf("a workflow-level tag issue names node %q/%q", issue.NodeID, issue.NodeName)
				}
			}
			if issues[0].Severity != test.severity {
				t.Errorf("severity = %q, want %q", issues[0].Severity, test.severity)
			}
			for _, fragment := range test.reasons {
				if !strings.Contains(joined, fragment) {
					t.Errorf("reasons = %q, want them to mention %q", joined, fragment)
				}
			}
		})
	}
}

func tagNames(objects []string) []string {
	names := make([]string, 0, len(objects))
	for _, object := range objects {
		var parsed struct {
			Name string `json:"name"`
		}
		_ = json.Unmarshal([]byte(object), &parsed)
		names = append(names, parsed.Name)
	}
	return names
}

// A lot of rejected tags is one more line, not a wall of them.
func TestManyRefusedTagsAreReportedInBoundedSpace(t *testing.T) {
	t.Parallel()

	entries := make([]string, 300)
	for index := range entries {
		entries[index] = fmt.Sprintf(`{"name":"tag-%03d"}`, index)
	}
	result := importFixture(t, taggedFixture("["+strings.Join(entries, ",")+"]"))
	if len(result.Document.Tags) != workflow.MaxTags {
		t.Errorf("kept %d tags, want %d", len(result.Document.Tags), workflow.MaxTags)
	}
	if issues := tagIssues(result); len(issues) != 1 {
		t.Errorf("reported %d tag issues for an over-long list, want one: %+v", len(issues), issues)
	}
}

// A name that is padded and then refused (or repeats an earlier tag) was never
// carried, so it is one dropped issue, not a "carried differently" line and a
// "not carried" line about the same tag.
func TestAPaddedTagThatIsRefusedIsReportedOnce(t *testing.T) {
	t.Parallel()

	for name, fixture := range map[string]struct {
		tags string
		want string
	}{
		"padded with a comma":     {`[{"name":"  a,b "},{"name":"Prod"}]`, "Prod"},
		"padded and too long":     {`[{"name":"  ` + strings.Repeat("x", 25) + ` "},{"name":"Prod"}]`, "Prod"},
		"padded duplicate":        {`[{"name":"Prod"},{"name":" prod "}]`, "Prod"},
		"padded and then refused": {`[{"name":" a\u0007b "},{"name":"Prod"}]`, "Prod"},
	} {
		result := importFixture(t, taggedFixture(fixture.tags))
		if got := strings.Join(result.Document.Tags, "|"); got != fixture.want {
			t.Errorf("%s: kept %q, want %q", name, got, fixture.want)
		}
		issues := tagIssues(result)
		if len(issues) != 1 || issues[0].Severity != n8n.SeverityDropped {
			t.Errorf("%s: reported %+v, want one dropped issue", name, issues)
		}
	}

	padded := importFixture(t, taggedFixture(`[{"name":" Prod "}]`))
	if issues := tagIssues(padded); len(issues) != 1 || issues[0].Severity != n8n.SeverityLossy || strings.Join(padded.Document.Tags, "|") != "Prod" {
		t.Errorf("a padded name that is kept: tags %q, issues %+v; want Prod and one lossy issue", padded.Document.Tags, issues)
	}
}

// What the report quotes of a hostile name is shortened, so no single tag can
// make a report enormous.
func TestAReasonQuotesAtMostAShortenedTag(t *testing.T) {
	t.Parallel()

	huge := strings.Repeat("é", 5000)
	for name, tags := range map[string]string{
		"too long":           `[{"name":"` + huge + `"}]`,
		"with a comma":       `[{"name":"` + huge + `,"}]`,
		"with a control":     `[{"name":"` + huge + `\u0007"}]`,
		"padded and kept":    `[{"name":"  ` + strings.Repeat("a", 20) + `  "}]`,
		"padded and refused": `[{"name":"  ` + huge + `  "}]`,
		"a long bare entry":  `["` + huge + `"]`,
		"an object entry":    `[{"id":"` + huge + `"}]`,
	} {
		issues := tagIssues(importFixture(t, taggedFixture(tags)))
		if len(issues) == 0 {
			t.Errorf("%s: nothing was reported", name)
		}
		for _, issue := range issues {
			if len(issue.Reason) > 600 {
				t.Errorf("%s: a reason is %d bytes long: %.80s…", name, len(issue.Reason), issue.Reason)
			}
		}
	}
}

// Pasting nodes copied from n8n brings the clipboard's workflow metadata with
// it; the workflow they land in keeps its own tags, so nothing is said about
// them. The same goes for a whole export run through convert.
func TestAPastedExportReportsNothingAboutTags(t *testing.T) {
	t.Parallel()

	for _, tags := range []string{
		`[{"id":"1","name":"Prod"}]`,
		`[{"name":"a,b"},"x"]`,
		`{"unreadable":true}`,
	} {
		result, err := n8n.ImportFragment([]byte(taggedFixture(tags)), registry(t))
		if err != nil {
			t.Fatalf("ImportFragment() error = %v", err)
		}
		if issues := tagIssues(result); len(issues) != 0 {
			t.Errorf("a pasted fragment with tags %s reported %+v, want nothing", tags, issues)
		}
	}
}
