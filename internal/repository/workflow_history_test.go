package repository_test

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// historyCatalog is the one node type every fixture document uses.
func historyCatalog() workflow.Catalog {
	return activationCatalog{
		"kilasflow.manual": {
			Type: "kilasflow.manual", Version: workflow.V(1),
			Outputs: []workflow.Port{{Name: "main", Kind: workflow.ConnectionMain}},
		},
	}
}

func historyDocument(name string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_history",
		Name:          name,
		Nodes: []workflow.Node{{
			ID: "manual", Name: "Manual Trigger", Type: "kilasflow.manual",
			TypeVersion: workflow.V(1), Position: workflow.Position{},
		}},
		Connections: []workflow.Connection{},
		Settings:    map[string]any{"timezone": "Asia/Jakarta"},
	}
}

func newHistoryDB(t *testing.T) *database.DB {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite",
		DSN:    filepath.Join(t.TempDir(), "kilasflow.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("Migrate() error = %v", err)
	}
	return db
}

// saveRevisions appends n revisions, naming each one after its number.
func saveRevisions(t *testing.T, store *repository.GORMWorkflowStore, tenant repository.TenantScope, n int) []workflow.StoredWorkflow {
	t.Helper()
	var saved []workflow.StoredWorkflow
	for i := 1; i <= n; i++ {
		stored, err := store.SaveDraft(context.Background(), tenant, historyDocument("Revision "+string(rune('0'+i))))
		if err != nil {
			t.Fatalf("SaveDraft(%d) error = %v", i, err)
		}
		saved = append(saved, stored)
	}
	return saved
}

func TestAVersionListingIsNewestFirstAndCarriesNoDocument(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 3)

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := len(page.Versions), 3; got != want {
		t.Fatalf("versions listed = %d, want %d", got, want)
	}
	for position, wantRevision := range []int{3, 2, 1} {
		if got := page.Versions[position].Revision; got != wantRevision {
			t.Errorf("version %d revision = %d, want %d", position, got, wantRevision)
		}
	}
	if got, want := page.Versions[0].ID, saved[2].LatestVersion.ID; got != want {
		t.Errorf("newest version ID = %q, want %q", got, want)
	}
}

func TestAVersionListingStatesWhichRevisionIsDraftAndWhichIsPublished(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 3)

	// Publish revision 2, so that the published version and the draft are
	// different rows — the case a client cannot get right by guessing.
	if _, err := store.PublishVersion(context.Background(), tenant, "wf_history",
		saved[1].LatestVersion.ID, historyCatalog(), "rolling back a bad save"); err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	roles := map[int]workflow.VersionSummary{}
	for _, summary := range page.Versions {
		roles[summary.Revision] = summary
	}
	if !roles[3].Draft || roles[3].Published {
		t.Errorf("revision 3 = %+v, want draft and not published", roles[3])
	}
	if roles[2].Draft || !roles[2].Published {
		t.Errorf("revision 2 = %+v, want published and not draft", roles[2])
	}
	if roles[1].Draft || roles[1].Published {
		t.Errorf("revision 1 = %+v, want neither draft nor published", roles[1])
	}
}

func TestAVersionListingPagesThroughHistoryWithoutRepeatingARevision(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 5)

	seen := map[int]bool{}
	cursor := ""
	for pages := 0; pages < 5; pages++ {
		page, err := store.ListVersions(context.Background(), tenant, "wf_history",
			repository.VersionFilter{Limit: 2, Cursor: cursor})
		if err != nil {
			t.Fatalf("ListVersions() error = %v", err)
		}
		for _, summary := range page.Versions {
			if seen[summary.Revision] {
				t.Fatalf("revision %d appeared on two pages", summary.Revision)
			}
			seen[summary.Revision] = true
		}
		cursor = page.NextCursor
		if cursor == "" {
			break
		}
	}
	if got, want := len(seen), 5; got != want {
		t.Errorf("revisions seen across pages = %d, want %d", got, want)
	}
}

func TestAnUnrecognisedVersionCursorIsRejectedRatherThanIgnored(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 1)

	_, err := store.ListVersions(context.Background(), tenant, "wf_history",
		repository.VersionFilter{Cursor: "not-a-cursor"})
	if !errors.Is(err, repository.ErrInvalidCursor) {
		t.Fatalf("ListVersions(bad cursor) error = %v, want ErrInvalidCursor", err)
	}
}

func TestPublishingAnEarlierRevisionPinsItWithoutTouchingTheDraft(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 3)

	stored, err := store.PublishVersion(context.Background(), tenant, "wf_history",
		saved[0].LatestVersion.ID, historyCatalog(), "the newest save broke production")
	if err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}
	if !stored.Active {
		t.Error("publishing an earlier revision left the workflow inactive")
	}
	if stored.ActiveVersion == nil || stored.ActiveVersion.Revision != 1 {
		t.Fatalf("published version = %+v, want revision 1", stored.ActiveVersion)
	}
	// The draft is untouched: publishing chooses what runs, it does not rewind
	// what an editor is working on.
	if got, want := stored.LatestVersion.Revision, 3; got != want {
		t.Errorf("latest revision after publishing an earlier one = %d, want %d", got, want)
	}
}

func TestPublishingARevisionThatNoLongerCompilesIsRefused(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}

	broken := historyDocument("Uses a node type the catalogue lost")
	broken.Nodes[0].Type = "kilasflow.removed"
	saved, err := store.SaveDraft(context.Background(), tenant, broken)
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	_, err = store.PublishVersion(context.Background(), tenant, "wf_history",
		saved.LatestVersion.ID, historyCatalog(), "")
	var validationErrors *workflow.ValidationErrors
	if !errors.As(err, &validationErrors) {
		t.Fatalf("PublishVersion(uncompilable) error = %v, want ValidationErrors", err)
	}

	stored, err := store.Get(context.Background(), tenant, "wf_history")
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	if stored.Active || stored.ActiveVersion != nil {
		t.Errorf("a refused publish still pinned a version: %+v", stored)
	}
}

func TestPublishingAnEarlierRevisionResyncsTheWebhookPathsItServes(t *testing.T) {
	db := newHistoryDB(t)
	tenant := repository.TenantScope{ID: "tenant-a"}
	// The extractor reads the path out of the document's settings, so each
	// revision claims a different endpoint and the binding can be checked
	// against the revision that is actually pinned.
	store := repository.NewWorkflowStore(db.DB).WithWebhooks(func(document workflow.Document) []repository.WebhookTrigger {
		path, _ := document.Settings["path"].(string)
		if path == "" {
			return nil
		}
		return []repository.WebhookTrigger{{
			NodeID: "manual", NodeType: "kilasflow.manual", Method: "POST", Path: path,
		}}
	})

	first := historyDocument("First")
	first.Settings = map[string]any{"path": "first-path"}
	firstSaved, err := store.SaveDraft(context.Background(), tenant, first)
	if err != nil {
		t.Fatalf("SaveDraft(first) error = %v", err)
	}
	second := historyDocument("Second")
	second.Settings = map[string]any{"path": "second-path"}
	if _, err := store.SaveDraft(context.Background(), tenant, second); err != nil {
		t.Fatalf("SaveDraft(second) error = %v", err)
	}

	if _, err := store.Activate(context.Background(), tenant, "wf_history", historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if got := boundPaths(t, db); len(got) != 1 || got[0] != "second-path" {
		t.Fatalf("bound paths after activating the latest = %v, want [second-path]", got)
	}

	if _, err := store.PublishVersion(context.Background(), tenant, "wf_history",
		firstSaved.LatestVersion.ID, historyCatalog(), "rolling back"); err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}

	// The old path must be gone in the same commit: an active workflow serving
	// a path belonging to a version it no longer runs is the failure this
	// guards.
	got := boundPaths(t, db)
	if len(got) != 1 || got[0] != "first-path" {
		t.Errorf("bound paths after publishing revision 1 = %v, want [first-path]", got)
	}
}

func boundPaths(t *testing.T, db *database.DB) []string {
	t.Helper()
	var paths []string
	if err := db.Raw("SELECT path FROM webhook_bindings ORDER BY path").Scan(&paths).Error; err != nil {
		t.Fatalf("read webhook bindings: %v", err)
	}
	return paths
}

func TestRestoringAVersionAppendsARevisionAndLeavesTheOriginalUntouched(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}

	original := historyDocument("The good one")
	original.Settings = map[string]any{"timezone": "Asia/Jakarta", "errorWorkflow": "wf_alert"}
	first, err := store.SaveDraft(context.Background(), tenant, original)
	if err != nil {
		t.Fatalf("SaveDraft(original) error = %v", err)
	}
	if _, err := store.SaveDraft(context.Background(), tenant, historyDocument("The bad one")); err != nil {
		t.Fatalf("SaveDraft(bad) error = %v", err)
	}

	stored, err := store.RestoreVersion(context.Background(), tenant, "wf_history",
		first.LatestVersion.ID, "undoing the bad save")
	if err != nil {
		t.Fatalf("RestoreVersion() error = %v", err)
	}
	if got, want := stored.LatestVersion.Revision, 3; got != want {
		t.Fatalf("revision after restore = %d, want %d", got, want)
	}
	if got, want := stored.LatestVersion.Document.Name, "The good one"; got != want {
		t.Errorf("restored document name = %q, want %q", got, want)
	}
	// A KilasFlow snapshot is the whole document, so a restore brings settings
	// back too. This is the property that makes it a real restore rather than a
	// graph copy.
	if got, want := stored.LatestVersion.Document.Settings["errorWorkflow"], "wf_alert"; got != want {
		t.Errorf("restored settings errorWorkflow = %v, want %q", got, want)
	}

	// Append-only: the revision restored from is still there, unchanged.
	source, err := store.GetVersionByID(context.Background(), tenant, "wf_history", first.LatestVersion.ID)
	if err != nil {
		t.Fatalf("GetVersionByID(source) error = %v", err)
	}
	if got, want := source.Revision, 1; got != want {
		t.Errorf("restored-from revision = %d, want %d", got, want)
	}
	if got, want := source.Document.Name, "The good one"; got != want {
		t.Errorf("restored-from document name = %q, want %q", got, want)
	}

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := len(page.Versions), 3; got != want {
		t.Errorf("versions after restore = %d, want %d", got, want)
	}
}

func TestEveryPublishUnpublishAndRestoreLeavesAnAuditRow(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	ctx := repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindKey, Label: "ci-agent", KeyID: "key_ci",
	})
	saved := saveRevisions(t, store, tenant, 2)

	if _, err := store.Activate(ctx, tenant, "wf_history", historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := store.Deactivate(ctx, tenant, "wf_history"); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	if _, err := store.RestoreVersion(ctx, tenant, "wf_history",
		saved[0].LatestVersion.ID, "undoing"); err != nil {
		t.Fatalf("RestoreVersion() error = %v", err)
	}

	events, err := store.ListPublishEvents(ctx, tenant, "wf_history")
	if err != nil {
		t.Fatalf("ListPublishEvents() error = %v", err)
	}
	if got, want := len(events), 3; got != want {
		t.Fatalf("audit rows = %d, want %d: %+v", got, want, events)
	}
	// Newest first.
	wantActions := []workflow.PublishAction{
		workflow.PublishActionRestored,
		workflow.PublishActionUnpublished,
		workflow.PublishActionPublished,
	}
	for position, want := range wantActions {
		if got := events[position].Action; got != want {
			t.Errorf("audit row %d action = %q, want %q", position, got, want)
		}
		if got := events[position].Actor; got != "ci-agent" {
			t.Errorf("audit row %d actor = %q, want the request's actor", position, got)
		}
		// The kind and the key are what make the row attributable: a label
		// alone names whoever reused it.
		if got := events[position].ActorKind; got != repository.ActorKindKey {
			t.Errorf("audit row %d actor kind = %q, want %q", position, got, repository.ActorKindKey)
		}
		if got := events[position].ActorLabel; got != "ci-agent" {
			t.Errorf("audit row %d actor label = %q, want the key's name", position, got)
		}
		if got := events[position].ActorKeyID; got != "key_ci" {
			t.Errorf("audit row %d actor key id = %q, want the key that acted", position, got)
		}
	}
	if got, want := events[0].Reason, "undoing"; got != want {
		t.Errorf("restore reason = %q, want %q", got, want)
	}
	// The publish period is reconstructible: the unpublish names the same
	// version the publish did, which is what says when it stopped serving.
	if events[1].VersionID != events[2].VersionID {
		t.Errorf("unpublish named version %q but publish named %q", events[1].VersionID, events[2].VersionID)
	}
	if !events[1].CreatedAt.After(events[2].CreatedAt) && !events[1].CreatedAt.Equal(events[2].CreatedAt) {
		t.Errorf("unpublish at %v precedes its publish at %v", events[1].CreatedAt, events[2].CreatedAt)
	}
}

func TestAnUnknownAuthorIsRecordedAsAbsentRatherThanInvented(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 1)

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got := page.Versions[0].CreatedBy; got != "" {
		t.Errorf("author of an unauthenticated save = %q, want empty", got)
	}
	// "Nobody was recorded" is not a kind. A row that named one would claim an
	// attribution the write never had.
	if got := page.Versions[0].ActorKind; got != "" {
		t.Errorf("actor kind of an unauthenticated save = %q, want empty", got)
	}
	if got := page.Versions[0].ActorLabel; got != "" {
		t.Errorf("actor label of an unauthenticated save = %q, want empty", got)
	}
	if got := page.Versions[0].ActorKeyID; got != "" {
		t.Errorf("actor key id of an unauthenticated save = %q, want empty", got)
	}

	authored, err := store.SaveDraft(repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindUser, Label: "grace@example.com",
	}), tenant, historyDocument("Authored"))
	if err != nil {
		t.Fatalf("SaveDraft(authored) error = %v", err)
	}
	page, err = store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := page.Versions[0].ID, authored.LatestVersion.ID; got != want {
		t.Fatalf("newest version = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].CreatedBy, "grace@example.com"; got != want {
		t.Errorf("author of an attributed save = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].ActorKind, repository.ActorKindUser; got != want {
		t.Errorf("actor kind of a session's save = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].ActorLabel, "grace@example.com"; got != want {
		t.Errorf("actor label of a session's save = %q, want %q", got, want)
	}
	// A session presents no key, so the column stays empty rather than naming
	// whatever key happened to be last.
	if got := page.Versions[0].ActorKeyID; got != "" {
		t.Errorf("actor key id of a session's save = %q, want empty", got)
	}
}

// The listing is where the attribution is read, so the four columns have to
// survive the round trip from the principal that wrote the revision to the row
// a reader sees.
func TestARevisionRecordsTheKeyThatWroteItAndTheSkillsItReported(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	ctx := repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindKey, Label: "ci-agent", KeyID: "key_ci",
		Meta: []string{"kilasflow-debugging", "kilasflow-expressions"},
	})

	saved, err := store.SaveDraft(ctx, tenant, historyDocument("Written by an agent"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}

	page, err := store.ListVersions(ctx, tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := page.Versions[0].ID, saved.LatestVersion.ID; got != want {
		t.Fatalf("newest version = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].ActorKind, repository.ActorKindKey; got != want {
		t.Errorf("actor kind of a key's save = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].ActorLabel, "ci-agent"; got != want {
		t.Errorf("actor label of a key's save = %q, want %q", got, want)
	}
	if got, want := page.Versions[0].ActorKeyID, "key_ci"; got != want {
		t.Errorf("actor key id of a key's save = %q, want %q", got, want)
	}
	if got, want := strings.Join(page.Versions[0].ActorMeta, ","), "kilasflow-debugging,kilasflow-expressions"; got != want {
		t.Errorf("actor meta of a reported save = %q, want %q", got, want)
	}

	// A write that reported nothing carries no meta at all, which is a
	// different answer from one that reported an empty list.
	plain, err := store.SaveDraft(repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindUser, Label: "ada@example.com",
	}), tenant, historyDocument("Written by a person"))
	if err != nil {
		t.Fatalf("SaveDraft(plain) error = %v", err)
	}
	page, err = store.ListVersions(ctx, tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := page.Versions[0].ID, plain.LatestVersion.ID; got != want {
		t.Fatalf("newest version = %q, want %q", got, want)
	}
	if got := page.Versions[0].ActorMeta; len(got) != 0 {
		t.Errorf("actor meta of a save that reported nothing = %v, want none", got)
	}
}

func TestTheDefaultRetentionKeepsEveryVersion(t *testing.T) {
	db := newHistoryDB(t)
	// No WithRetention call at all: this is what an installation that never
	// configured history gets, and it must never lose a revision.
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 12)

	page, err := store.ListVersions(context.Background(), tenant, "wf_history",
		repository.VersionFilter{Limit: repository.MaxVersionPageSize})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := len(page.Versions), 12; got != want {
		t.Errorf("versions kept under the default policy = %d, want %d", got, want)
	}
}

func TestACountBoundKeepsOnlyTheNewestRevisions(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxVersions: 3})
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 6)

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := len(page.Versions), 3; got != want {
		t.Fatalf("versions kept under a bound of 3 = %d, want %d", got, want)
	}
	for position, wantRevision := range []int{6, 5, 4} {
		if got := page.Versions[position].Revision; got != wantRevision {
			t.Errorf("kept version %d revision = %d, want %d", position, got, wantRevision)
		}
	}
}

func TestAnAgeBoundDropsOnlyVersionsOlderThanTheWindow(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxAge: time.Hour})
	tenant := repository.TenantScope{ID: "tenant-a"}
	saveRevisions(t, store, tenant, 4)

	// Age the two oldest revisions past the window. Rewriting created_at is the
	// only way to test an age bound without sleeping for it.
	if err := db.Exec(
		"UPDATE workflow_versions SET created_at = ? WHERE revision IN (1,2)",
		time.Now().UTC().Add(-48*time.Hour),
	).Error; err != nil {
		t.Fatalf("age the oldest revisions: %v", err)
	}

	pruned, err := store.PruneAllVersions(context.Background())
	if err != nil {
		t.Fatalf("PruneAllVersions() error = %v", err)
	}
	if got, want := pruned, 2; got != want {
		t.Errorf("pruned = %d, want %d", got, want)
	}

	page, err := store.ListVersions(context.Background(), tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if got, want := len(page.Versions), 2; got != want {
		t.Fatalf("versions kept = %d, want %d", got, want)
	}
	for position, wantRevision := range []int{4, 3} {
		if got := page.Versions[position].Revision; got != wantRevision {
			t.Errorf("kept version %d revision = %d, want %d", position, got, wantRevision)
		}
	}
}

// Each exclusion is asserted on its own, because a prune that kept the right
// number of rows for the wrong reason would pass a count-only assertion.
func TestPruningNeverRemovesThePublishedVersion(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxAge: time.Hour})
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 4)

	published := saved[0].LatestVersion.ID
	if _, err := store.PublishVersion(context.Background(), tenant, "wf_history",
		published, historyCatalog(), ""); err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}
	ageEverything(t, db)

	if _, err := store.PruneAllVersions(context.Background()); err != nil {
		t.Fatalf("PruneAllVersions() error = %v", err)
	}

	if _, err := store.GetVersionByID(context.Background(), tenant, "wf_history", published); err != nil {
		t.Errorf("the published version was pruned: %v", err)
	}
}

func TestPruningNeverRemovesTheLatestRevision(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxAge: time.Hour})
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 4)
	latest := saved[3].LatestVersion.ID
	ageEverything(t, db)

	if _, err := store.PruneAllVersions(context.Background()); err != nil {
		t.Fatalf("PruneAllVersions() error = %v", err)
	}

	if _, err := store.GetVersionByID(context.Background(), tenant, "wf_history", latest); err != nil {
		t.Errorf("the latest revision was pruned: %v", err)
	}
}

func TestPruningNeverRemovesAVersionASurvivingExecutionReplays(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxAge: time.Hour})
	tenant := repository.TenantScope{ID: "tenant-a"}
	saved := saveRevisions(t, store, tenant, 4)

	pinned := saved[1].LatestVersion.ID
	if _, err := repository.NewExecutionStore(db.DB).Create(context.Background(), tenant, execution.Record{
		WorkflowID:        "wf_history",
		WorkflowVersionID: pinned,
		Status:            execution.StatusSucceeded,
		Trigger:           execution.TriggerManual,
		Input:             json.RawMessage(`{}`),
	}); err != nil {
		t.Fatalf("Create(execution) error = %v", err)
	}
	ageEverything(t, db)

	if _, err := store.PruneAllVersions(context.Background()); err != nil {
		t.Fatalf("PruneAllVersions() error = %v", err)
	}

	// The execution detail view fetches its pinned version by ID to draw the
	// graph that ran; a missing row turns a finished execution into an error
	// page, so this read is the acceptance criterion itself.
	if _, err := store.GetVersionByID(context.Background(), tenant, "wf_history", pinned); err != nil {
		t.Errorf("a version a surviving execution replays was pruned: %v", err)
	}
}

func TestPruningNeverRemovesAVersionAWebhookBindingPointsAt(t *testing.T) {
	db := newHistoryDB(t)
	tenant := repository.TenantScope{ID: "tenant-a"}
	store := repository.NewWorkflowStore(db.DB).
		WithRetention(repository.RetentionPolicy{MaxAge: time.Hour}).
		WithWebhooks(func(workflow.Document) []repository.WebhookTrigger {
			return []repository.WebhookTrigger{{
				NodeID: "manual", NodeType: "kilasflow.manual", Method: "POST", Path: "hook",
			}}
		})
	saved := saveRevisions(t, store, tenant, 4)

	bound := saved[1].LatestVersion.ID
	if _, err := store.PublishVersion(context.Background(), tenant, "wf_history",
		bound, historyCatalog(), ""); err != nil {
		t.Fatalf("PublishVersion() error = %v", err)
	}
	// Clear the published pin so the binding is the only thing protecting this
	// version: otherwise the test would pass on the published exclusion alone.
	if err := db.Exec("UPDATE workflows SET active_version_id = NULL WHERE id = ?", "wf_history").Error; err != nil {
		t.Fatalf("clear the published pin: %v", err)
	}
	ageEverything(t, db)

	if _, err := store.PruneAllVersions(context.Background()); err != nil {
		t.Fatalf("PruneAllVersions() error = %v", err)
	}

	// webhook_bindings.workflow_version_id has no foreign key, so nothing but
	// the prune's own exclusion protects it.
	if _, err := store.GetVersionByID(context.Background(), tenant, "wf_history", bound); err != nil {
		t.Errorf("a version a webhook binding points at was pruned: %v", err)
	}
}

// ageEverything pushes every stored revision well outside any test's window.
func ageEverything(t *testing.T, db *database.DB) {
	t.Helper()
	if err := db.Exec(
		"UPDATE workflow_versions SET created_at = ?", time.Now().UTC().Add(-72*time.Hour),
	).Error; err != nil {
		t.Fatalf("age every revision: %v", err)
	}
}
