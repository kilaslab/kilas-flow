package api_test

import (
	"encoding/json"
	"net/http"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// taggedDraftBody is a write body that carries tags. The shared draft request
// type predates tags, so the tag tests build the body as a map.
func taggedDraftBody(name string, tags ...string) map[string]any {
	document := validManualWorkflow(name)
	body := map[string]any{
		"schemaVersion": document.SchemaVersion,
		"name":          document.Name,
		"nodes":         document.Nodes,
		"connections":   document.Connections,
		"settings":      document.Settings,
	}
	if tags != nil {
		body["tags"] = tags
	}
	return body
}

type taggedSummary struct {
	ID   string   `json:"id"`
	Name string   `json:"name"`
	Tags []string `json:"tags"`
}

type taggedWorkflow struct {
	ID            string `json:"id"`
	LatestVersion struct {
		Document workflow.Document `json:"document"`
	} `json:"latestVersion"`
}

func TestAWorkflowCreatedWithTagsEchoesThem(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Tagged", "Prod", "billing"), http.StatusCreated)
	if got := strings.Join(created.LatestVersion.Document.Tags, "|"); got != "Prod|billing" {
		t.Errorf("created document tags = %q, want Prod|billing", got)
	}
	got := requestJSON[taggedWorkflow](t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID, nil, http.StatusOK)
	if got := strings.Join(got.LatestVersion.Document.Tags, "|"); got != "Prod|billing" {
		t.Errorf("read-back document tags = %q, want Prod|billing", got)
	}

	// An update replaces the tags with whatever the new body says.
	updated := requestJSON[taggedWorkflow](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, taggedDraftBody("Tagged", "billing", "ops"), http.StatusOK)
	if got := strings.Join(updated.LatestVersion.Document.Tags, "|"); got != "billing|ops" {
		t.Errorf("updated document tags = %q, want billing|ops", got)
	}
}

// Every summary carries a tags array, never null, so a client can read
// summary.tags.length without a guard.
func TestTheWorkflowListShowsTagsAndAnUntaggedWorkflowHasAnEmptyArray(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Tagged", "Prod", "billing"), http.StatusCreated)
	requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Plain"), http.StatusCreated)

	recorder := get(t, handler, "/api/v1/workflows")
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	var raw []map[string]json.RawMessage
	if err := json.Unmarshal(recorder.Body.Bytes(), &raw); err != nil {
		t.Fatalf("decode list = %v", err)
	}
	if len(raw) != 2 {
		t.Fatalf("list length = %d, want 2", len(raw))
	}
	byName := map[string]string{}
	for _, row := range raw {
		var name string
		_ = json.Unmarshal(row["name"], &name)
		byName[name] = string(row["tags"])
	}
	if byName["Tagged"] != `["Prod","billing"]` {
		t.Errorf("tagged summary tags = %s, want [\"Prod\",\"billing\"]", byName["Tagged"])
	}
	if byName["Plain"] != `[]` {
		t.Errorf("untagged summary tags = %s, want [] (not null, not absent)", byName["Plain"])
	}
}

func TestTheWorkflowListFiltersByTag(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("One", "Prod"), http.StatusCreated)
	requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Two", "staging", "PROD"), http.StatusCreated)
	requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Three", "staging"), http.StatusCreated)

	names := func(query string) string {
		rows := requestJSON[[]taggedSummary](t, handler, http.MethodGet, "/api/v1/workflows?"+query, nil, http.StatusOK)
		out := make([]string, 0, len(rows))
		for _, row := range rows {
			out = append(out, row.Name)
		}
		return strings.Join(out, ",")
	}
	if got := names("tag=prod"); got != "Two,One" && got != "One,Two" {
		t.Errorf("tag=prod listed %q, want One and Two", got)
	}
	if got := names("tag=" + url.QueryEscape(" STAGING ")); got != "Three,Two" && got != "Two,Three" {
		t.Errorf("tag=STAGING listed %q, want Two and Three", got)
	}
	if got := names("tag=missing"); got != "" {
		t.Errorf("tag=missing listed %q, want nothing", got)
	}
	if got := names("tag="); !strings.Contains(got, "One") || !strings.Contains(got, "Three") {
		t.Errorf("an empty tag listed %q, want every workflow", got)
	}

	// The filter pages with the ordinary cursor.
	first := get(t, handler, "/api/v1/workflows?tag=staging&limit=1")
	if first.Code != http.StatusOK {
		t.Fatalf("first page status = %d (body: %s)", first.Code, first.Body)
	}
	cursor := first.Header().Get("X-Next-Cursor")
	if cursor == "" {
		t.Fatal("a filtered page that stopped short carried no X-Next-Cursor")
	}
	second := requestJSON[[]taggedSummary](t, handler, http.MethodGet, "/api/v1/workflows?tag=staging&limit=1&cursor="+url.QueryEscape(cursor), nil, http.StatusOK)
	if len(second) != 1 {
		t.Errorf("second filtered page carried %d workflows, want 1", len(second))
	}
}

// A bad tag is a 422 on create and update that names the tag, and a diagnostic
// on validate, which reports rather than refuses.
func TestABadTagIsRefusedAndNamed(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Good", "ok"), http.StatusCreated)

	tooMany := make([]string, 21)
	for index := range tooMany {
		tooMany[index] = "t" + string(rune('a'+index))
	}
	for _, test := range []struct {
		name string
		tags []string
		want string
	}{
		{"a comma", []string{"fine", "bad,tag"}, "bad,tag"},
		{"too long", []string{strings.Repeat("x", 25)}, "24"},
		{"a duplicate ignoring case", []string{"Prod", "prod"}, "prod"},
		{"an empty tag", []string{""}, "empty"},
		{"twenty-one tags", tooMany, "20"},
	} {
		t.Run(test.name, func(t *testing.T) {
			for _, call := range []struct{ method, path string }{
				{http.MethodPost, "/api/v1/workflows"},
				{http.MethodPut, "/api/v1/workflows/" + created.ID},
			} {
				recorder := doJSON(t, handler, call.method, call.path, taggedDraftBody("Bad", test.tags...))
				if recorder.Code != http.StatusUnprocessableEntity {
					t.Fatalf("%s %s status = %d, want 422 (body: %s)", call.method, call.path, recorder.Code, recorder.Body)
				}
				if !strings.Contains(recorder.Body.String(), test.want) {
					t.Errorf("%s %s body = %s, want it to mention %q", call.method, call.path, recorder.Body, test.want)
				}
			}

			if len(test.tags) > workflow.MaxTags {
				// The request schema's maxItems refuses an over-long list before
				// the dry run starts, as it refuses any schema violation.
				recorder := doJSON(t, handler, http.MethodPost, "/api/v1/workflows/validate", taggedDraftBody("Bad", test.tags...))
				if recorder.Code != http.StatusUnprocessableEntity || !strings.Contains(recorder.Body.String(), "body.tags") {
					t.Errorf("validate status = %d, body = %s; want 422 naming body.tags", recorder.Code, recorder.Body)
				}
				return
			}
			validated := requestJSON[struct {
				Valid       bool `json:"valid"`
				Diagnostics []struct {
					Reason string `json:"reason"`
				} `json:"diagnostics"`
			}](t, handler, http.MethodPost, "/api/v1/workflows/validate", taggedDraftBody("Bad", test.tags...), http.StatusOK)
			if validated.Valid || len(validated.Diagnostics) == 0 || !strings.Contains(validated.Diagnostics[0].Reason, test.want) {
				t.Errorf("validate = %+v, want an invalid result whose diagnostic mentions %q", validated, test.want)
			}
		})
	}

	// None of the refused writes touched the stored workflow.
	stored := requestJSON[taggedWorkflow](t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID, nil, http.StatusOK)
	if strings.Join(stored.LatestVersion.Document.Tags, "|") != "ok" {
		t.Errorf("tags after refused writes = %q, want ok", stored.LatestVersion.Document.Tags)
	}
}

func TestADuplicatedWorkflowKeepsItsTags(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	source := requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Original", "Prod", "billing"), http.StatusCreated)
	copied := requestJSON[taggedWorkflow](t, handler, http.MethodPost, "/api/v1/workflows/"+source.ID+"/duplicate", nil, http.StatusCreated)
	if copied.ID == source.ID {
		t.Fatal("duplicate returned the source workflow")
	}
	if got := strings.Join(copied.LatestVersion.Document.Tags, "|"); got != "Prod|billing" {
		t.Errorf("duplicate document tags = %q, want Prod|billing", got)
	}
	rows := requestJSON[[]taggedSummary](t, handler, http.MethodGet, "/api/v1/workflows?tag=billing", nil, http.StatusOK)
	if len(rows) != 2 {
		t.Errorf("tag=billing listed %d workflows after a duplicate, want 2", len(rows))
	}
}

// Restoring a revision brings its tags back through the API too.
func TestRestoringARevisionRestoresItsTagsInTheList(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := requestJSON[struct {
		ID            string `json:"id"`
		LatestVersion struct {
			ID string `json:"id"`
		} `json:"latestVersion"`
	}](t, handler, http.MethodPost, "/api/v1/workflows", taggedDraftBody("Restorable", "first"), http.StatusCreated)
	requestJSON[taggedWorkflow](t, handler, http.MethodPut, "/api/v1/workflows/"+created.ID, taggedDraftBody("Restorable", "second"), http.StatusOK)

	restore := doJSON(t, handler, http.MethodPost, "/api/v1/workflows/"+created.ID+"/versions/"+created.LatestVersion.ID+"/restore", nil)
	if restore.Code != http.StatusOK {
		t.Fatalf("restore status = %d, want 200 (body: %s)", restore.Code, restore.Body)
	}
	rows := requestJSON[[]taggedSummary](t, handler, http.MethodGet, "/api/v1/workflows?tag=first", nil, http.StatusOK)
	if len(rows) != 1 || strings.Join(rows[0].Tags, "|") != "first" {
		t.Errorf("after the restore tag=first listed %+v, want the restored workflow carrying only first", rows)
	}
}

func TestTheOpenAPIDocumentsTags(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	recorder := get(t, handler, "/api/openapi.json")
	if recorder.Code != http.StatusOK {
		t.Fatalf("GET OpenAPI status = %d", recorder.Code)
	}
	var document struct {
		Paths map[string]map[string]struct {
			Parameters []struct {
				Name string `json:"name"`
				In   string `json:"in"`
			} `json:"parameters"`
		} `json:"paths"`
		Components struct {
			Schemas map[string]struct {
				Required   []string                   `json:"required"`
				Properties map[string]json.RawMessage `json:"properties"`
			} `json:"schemas"`
		} `json:"components"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &document); err != nil {
		t.Fatalf("decode OpenAPI document = %v", err)
	}

	schemas := document.Components.Schemas
	for _, name := range []string{"WorkflowDocumentInput", "WorkflowSummary", "Document"} {
		if _, found := schemas[name].Properties["tags"]; !found {
			t.Errorf("OpenAPI schema %s has no tags property", name)
		}
	}
	required := false
	for _, field := range schemas["WorkflowSummary"].Required {
		required = required || field == "tags"
	}
	if !required {
		t.Error("WorkflowSummary.tags is optional in the OpenAPI document, want required")
	}
	for _, name := range []string{"WorkflowDocumentInput", "Document"} {
		for _, field := range schemas[name].Required {
			if field == "tags" {
				t.Errorf("%s requires tags; a document without them must stay valid", name)
			}
		}
	}

	found := false
	for _, parameter := range document.Paths["/api/v1/workflows"]["get"].Parameters {
		found = found || (parameter.Name == "tag" && parameter.In == "query")
	}
	if !found {
		t.Errorf("list-workflows has no tag query parameter: %+v", document.Paths["/api/v1/workflows"]["get"].Parameters)
	}
}
