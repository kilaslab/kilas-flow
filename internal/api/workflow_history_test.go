package api_test

import (
	"net/http"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

type workflowVersionSummaryResource struct {
	ID        string `json:"id"`
	Revision  int    `json:"revision"`
	Label     string `json:"label"`
	CreatedBy string `json:"createdBy"`
	Draft     bool   `json:"draft"`
	Published bool   `json:"published"`
	// Document is not part of the resource. It is declared here so the test can
	// prove the field never arrives rather than merely not reading it.
	Document *workflow.Document `json:"document"`
}

type workflowVersionListResource struct {
	Items      []workflowVersionSummaryResource `json:"items"`
	NextCursor string                           `json:"nextCursor"`
}

type workflowPublishEventResource struct {
	VersionID string `json:"versionId"`
	Action    string `json:"action"`
	Reason    string `json:"reason"`
}

// saveRevisionsOverAPI appends revisions through the public save endpoint, so
// the history under test is the one a real editor would produce.
func saveRevisionsOverAPI(t *testing.T, handler http.Handler, id string, names ...string) []workflowResource {
	t.Helper()
	var saved []workflowResource
	for _, name := range names {
		document := validManualWorkflow(name)
		saved = append(saved, requestJSON[workflowResource](
			t, handler, http.MethodPut, "/api/v1/workflows/"+id, workflowDraft(document), http.StatusOK))
	}
	return saved
}

func TestTheVersionsEndpointListsHistoryNewestFirstWithoutTheDocument(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))
	saveRevisionsOverAPI(t, handler, created.ID, "Second", "Third")

	page := requestJSON[workflowVersionListResource](
		t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID+"/versions", nil, http.StatusOK)

	if got, want := len(page.Items), 3; got != want {
		t.Fatalf("versions listed = %d, want %d", got, want)
	}
	for position, wantRevision := range []int{3, 2, 1} {
		if got := page.Items[position].Revision; got != wantRevision {
			t.Errorf("item %d revision = %d, want %d", position, got, wantRevision)
		}
	}
	for _, item := range page.Items {
		if item.Document != nil {
			t.Errorf("revision %d carried a document in a listing", item.Revision)
		}
	}
}

func TestTheVersionsEndpointNamesTheDraftAndThePublishedRevision(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))
	saved := saveRevisionsOverAPI(t, handler, created.ID, "Second", "Third")
	target := saved[0].LatestVersion.ID // revision 2

	requestJSON[workflowResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/versions/"+target+"/publish",
		map[string]string{"reason": "rolling back"}, http.StatusOK)

	page := requestJSON[workflowVersionListResource](
		t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID+"/versions", nil, http.StatusOK)

	byRevision := map[int]workflowVersionSummaryResource{}
	for _, item := range page.Items {
		byRevision[item.Revision] = item
	}
	if !byRevision[3].Draft || byRevision[3].Published {
		t.Errorf("revision 3 = %+v, want the draft", byRevision[3])
	}
	if !byRevision[2].Published || byRevision[2].Draft {
		t.Errorf("revision 2 = %+v, want published", byRevision[2])
	}
	if byRevision[1].Draft || byRevision[1].Published {
		t.Errorf("revision 1 = %+v, want neither", byRevision[1])
	}
}

func TestPublishingAnEarlierRevisionThroughTheAPIPinsIt(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))
	saveRevisionsOverAPI(t, handler, created.ID, "Second")

	published := requestJSON[workflowResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/versions/"+created.LatestVersion.ID+"/publish",
		nil, http.StatusOK)

	if !published.Active {
		t.Error("publishing left the workflow inactive")
	}
	if published.ActiveVersion == nil || published.ActiveVersion.Revision != 1 {
		t.Fatalf("published version = %+v, want revision 1", published.ActiveVersion)
	}
	if got, want := published.LatestVersion.Revision, 2; got != want {
		t.Errorf("latest revision = %d, want %d — publishing must not rewind the draft", got, want)
	}
}

func TestPublishingAnUnknownVersionIsNotFound(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))

	requestJSON[map[string]any](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/versions/wfv_nonexistent/publish", nil, http.StatusNotFound)
}

func TestRestoringAnEarlierRevisionThroughTheAPIAppendsIt(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("The good one"))
	saveRevisionsOverAPI(t, handler, created.ID, "The bad one")

	restored := requestJSON[workflowResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/versions/"+created.LatestVersion.ID+"/restore",
		map[string]string{"reason": "undoing the bad save"}, http.StatusOK)

	if got, want := restored.LatestVersion.Revision, 3; got != want {
		t.Fatalf("revision after restore = %d, want %d", got, want)
	}
	if got, want := restored.LatestVersion.Document.Name, "The good one"; got != want {
		t.Errorf("restored document name = %q, want %q", got, want)
	}

	// Append-only: three revisions exist and the restored-from one is intact.
	page := requestJSON[workflowVersionListResource](
		t, handler, http.MethodGet, "/api/v1/workflows/"+created.ID+"/versions", nil, http.StatusOK)
	if got, want := len(page.Items), 3; got != want {
		t.Errorf("versions after restore = %d, want %d", got, want)
	}
	original := requestJSON[workflowVersionResource](t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/versions/"+created.LatestVersion.ID, nil, http.StatusOK)
	if got, want := original.Document.Name, "The good one"; got != want {
		t.Errorf("restored-from revision now reads %q, want %q", got, want)
	}
}

func TestThePublishEventsEndpointReportsWhatHappenedToEachVersion(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))

	requestJSON[workflowResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/activate", nil, http.StatusOK)
	requestJSON[workflowResource](t, handler, http.MethodPost,
		"/api/v1/workflows/"+created.ID+"/deactivate", nil, http.StatusOK)

	events := requestJSON[[]workflowPublishEventResource](t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/publish-events", nil, http.StatusOK)

	if got, want := len(events), 2; got != want {
		t.Fatalf("publish events = %d, want %d: %+v", got, want, events)
	}
	if got, want := events[0].Action, "unpublished"; got != want {
		t.Errorf("newest event action = %q, want %q", got, want)
	}
	if got, want := events[1].Action, "published"; got != want {
		t.Errorf("oldest event action = %q, want %q", got, want)
	}
	// Both name the same version, which is what makes the publish period
	// reconstructible from the audit rows alone.
	if events[0].VersionID != events[1].VersionID {
		t.Errorf("unpublish named %q but publish named %q", events[0].VersionID, events[1].VersionID)
	}
}

func TestAVersionCursorTheAPINeverIssuedIsABadRequest(t *testing.T) {
	handler, _, _ := newWorkflowAPI(t)
	created := createWorkflow(t, handler, validManualWorkflow("First"))

	requestJSON[map[string]any](t, handler, http.MethodGet,
		"/api/v1/workflows/"+created.ID+"/versions?cursor=nonsense", nil, http.StatusBadRequest)
}
