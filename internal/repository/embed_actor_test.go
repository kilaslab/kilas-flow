package repository_test

import (
	"context"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/execution"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// embedActorContext is a request an embed session made for a named host user,
// as the API boundary tags it.
func embedActorContext() context.Context {
	return repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindEmbed, Label: "Ada Lovelace", ID: "auth0|ada",
	})
}

func TestAnEmbedActorIsRecordedOnRevisionsAndPublishEvents(t *testing.T) {
	db := newHistoryDB(t)
	store := repository.NewWorkflowStore(db.DB)
	tenant := repository.TenantScope{ID: "tenant-a"}
	ctx := embedActorContext()

	if _, err := store.SaveDraft(ctx, tenant, historyDocument("Saved from an embedded editor")); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	page, err := store.ListVersions(ctx, tenant, "wf_history", repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	revision := page.Versions[0]
	if revision.ActorKind != repository.ActorKindEmbed || revision.ActorLabel != "Ada Lovelace" ||
		revision.ActorID != "auth0|ada" || revision.ActorKeyID != "" {
		t.Errorf("revision actor = (%q, %q, %q, key %q), want the embed actor and no key",
			revision.ActorKind, revision.ActorLabel, revision.ActorID, revision.ActorKeyID)
	}
	if revision.CreatedBy != "Ada Lovelace" {
		t.Errorf("revision createdBy = %q, want the display name", revision.CreatedBy)
	}

	if _, err := store.Activate(ctx, tenant, "wf_history", historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	events, err := store.ListPublishEvents(ctx, tenant, "wf_history")
	if err != nil {
		t.Fatalf("ListPublishEvents() error = %v", err)
	}
	if len(events) == 0 {
		t.Fatal("publish events = none, want the publish")
	}
	event := events[0]
	if event.ActorKind != repository.ActorKindEmbed || event.ActorLabel != "Ada Lovelace" ||
		event.ActorID != "auth0|ada" || event.Actor != "Ada Lovelace" {
		t.Errorf("publish event actor = %+v, want the embed actor", event)
	}
}

func TestAManualRunRecordsWhoQueuedIt(t *testing.T) {
	db := newHistoryDB(t)
	tenant := repository.TenantScope{ID: "tenant-a"}
	stored, err := repository.NewWorkflowStore(db.DB).SaveDraft(context.Background(), tenant, historyDocument("Runnable"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)

	queued, err := store.QueueManualLatest(embedActorContext(), tenant, stored.ID, historyCatalog(), "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest() error = %v", err)
	}
	assertExecutionActor(t, queued, repository.ActorKindEmbed, "Ada Lovelace", "", "auth0|ada")
	loaded, err := store.Get(context.Background(), tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertExecutionActor(t, loaded, repository.ActorKindEmbed, "Ada Lovelace", "", "auth0|ada")

	// An untagged context names nobody rather than a guess.
	anonymous, err := store.QueueManualLatest(context.Background(), tenant, stored.ID, historyCatalog(), "", nil)
	if err != nil {
		t.Fatalf("QueueManualLatest(anonymous) error = %v", err)
	}
	loaded, err = store.Get(context.Background(), tenant, anonymous.ID)
	if err != nil {
		t.Fatalf("Get(anonymous) error = %v", err)
	}
	assertExecutionActor(t, loaded, "", "", "", "")

	// A retry records who asked for it, not who queued the original.
	keyCtx := repository.WithActor(context.Background(), repository.Actor{
		Kind: repository.ActorKindKey, Label: "ci-agent", KeyID: "key_ci",
	})
	retried, err := store.QueueRetry(keyCtx, tenant, loaded, historyCatalog())
	if err != nil {
		t.Fatalf("QueueRetry() error = %v", err)
	}
	loaded, err = store.Get(context.Background(), tenant, retried.ID)
	if err != nil {
		t.Fatalf("Get(retry) error = %v", err)
	}
	assertExecutionActor(t, loaded, repository.ActorKindKey, "ci-agent", "key_ci", "")
}

// A trigger-started run is nobody's manual act: even a context that carries
// an actor leaves its execution unattributed.
func TestATriggeredRunRecordsNoActor(t *testing.T) {
	db := newHistoryDB(t)
	tenant := repository.TenantScope{ID: "tenant-a"}
	workflows := repository.NewWorkflowStore(db.DB)
	stored, err := workflows.SaveDraft(context.Background(), tenant, historyDocument("Triggered"))
	if err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	// Triggers fire only on an active workflow, against its pinned revision.
	if _, err := workflows.Activate(context.Background(), tenant, stored.ID, historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	store := repository.NewExecutionStore(db.DB)

	queued, err := store.QueueTriggered(embedActorContext(), tenant, stored.ID, stored.LatestVersion.ID,
		execution.TriggerWebhook, "manual", nil)
	if err != nil {
		t.Fatalf("QueueTriggered() error = %v", err)
	}
	loaded, err := store.Get(context.Background(), tenant, queued.ID)
	if err != nil {
		t.Fatalf("Get() error = %v", err)
	}
	assertExecutionActor(t, loaded, "", "", "", "")
}

func assertExecutionActor(t *testing.T, record execution.Record, kind, label, keyID, id string) {
	t.Helper()
	if record.ActorKind != kind || record.ActorLabel != label || record.ActorKeyID != keyID || record.ActorID != id {
		t.Errorf("execution %s actor = (%q, %q, %q, %q), want (%q, %q, %q, %q)", record.ID,
			record.ActorKind, record.ActorLabel, record.ActorKeyID, record.ActorID, kind, label, keyID, id)
	}
}
