package repository_test

import (
	"context"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// longActorLabelLength is the longest label the API lets an actor carry: an
// API key's label and a user's email are both capped at 255 characters
// (handlers/auth.go, handlers/admin.go).
const longActorLabelLength = 255

// longActorDocument is a one-node workflow under its own identifier, so the
// rows it leaves on a shared PostgreSQL server cannot collide with another
// test's.
func longActorDocument(id string) workflow.Document {
	document := historyDocument("Written by a long-named actor")
	document.ID = id
	return document
}

// The legacy actor copies, workflow_versions.created_by and
// workflow_publish_events.actor, were varchar(64) on PostgreSQL while the
// label written into them can be 255 characters long. Every save, publish,
// deactivate and restore by a key with a long label, or by a user with a long
// email, then failed with "value too long" and answered 500 (BUG-ykxfbe).
// SQLite does not enforce varchar lengths, so only the PostgreSQL half of this
// test can catch a regression.
//
// The key label is multibyte on purpose: PostgreSQL measures varchar in
// characters, and the API caps the label in characters, so a 255-character
// label that is 510 bytes long must still fit.
func TestALongActorLabelIsStoredWholeOnEveryDriver(t *testing.T) {
	keyLabel := strings.Repeat("ключ-", longActorLabelLength/5)
	email := strings.Repeat("a", longActorLabelLength-len("@example.com")) + "@example.com"
	for _, check := range []struct {
		name       string
		tenant     string
		workflowID string
		actor      repository.Actor
	}{
		{
			name: "key", tenant: "drv-long-actor-key", workflowID: "drv_long_actor_key",
			actor: repository.Actor{Kind: repository.ActorKindKey, Label: keyLabel, KeyID: "key_long_actor"},
		},
		{
			name: "user", tenant: "drv-long-actor-user", workflowID: "drv_long_actor_user",
			actor: repository.Actor{Kind: repository.ActorKindUser, Label: email},
		},
	} {
		if got := utf8.RuneCountInString(check.actor.Label); got != longActorLabelLength {
			t.Fatalf("%s label is %d characters, want %d", check.name, got, longActorLabelLength)
		}
		t.Run(check.name, func(t *testing.T) {
			eachDriver(t, func(t *testing.T, db *database.DB) {
				assertLongActorStoredWhole(t, db, repository.TenantScope{ID: check.tenant}, check.workflowID, check.actor)
			})
		})
	}
}

func assertLongActorStoredWhole(t *testing.T, db *database.DB, tenant repository.TenantScope, workflowID string, actor repository.Actor) {
	t.Helper()
	ctx := repository.WithActor(context.Background(), actor)
	store := repository.NewWorkflowStore(db.DB)

	if _, err := store.SaveDraft(ctx, tenant, longActorDocument(workflowID)); err != nil {
		t.Fatalf("SaveDraft() error = %v", err)
	}
	if _, err := store.Activate(ctx, tenant, workflowID, historyCatalog()); err != nil {
		t.Fatalf("Activate() error = %v", err)
	}
	if _, err := store.Deactivate(ctx, tenant, workflowID); err != nil {
		t.Fatalf("Deactivate() error = %v", err)
	}
	page, err := store.ListVersions(ctx, tenant, workflowID, repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() error = %v", err)
	}
	if len(page.Versions) == 0 {
		t.Fatal("ListVersions() = none, want the saved revision")
	}
	if _, err := store.RestoreVersion(ctx, tenant, workflowID, page.Versions[0].ID, "long actor restore"); err != nil {
		t.Fatalf("RestoreVersion() error = %v", err)
	}

	page, err = store.ListVersions(ctx, tenant, workflowID, repository.VersionFilter{})
	if err != nil {
		t.Fatalf("ListVersions() after restore error = %v", err)
	}
	if len(page.Versions) < 2 {
		t.Fatalf("revisions = %d, want the saved one and the restore", len(page.Versions))
	}
	for _, revision := range page.Versions {
		if revision.CreatedBy != actor.Label {
			t.Errorf("revision %d createdBy = %q (%d characters), want the whole %d-character label",
				revision.Revision, revision.CreatedBy, utf8.RuneCountInString(revision.CreatedBy), longActorLabelLength)
		}
		if revision.ActorLabel != actor.Label {
			t.Errorf("revision %d actorLabel = %q, want the whole label", revision.Revision, revision.ActorLabel)
		}
	}

	events, err := store.ListPublishEvents(ctx, tenant, workflowID)
	if err != nil {
		t.Fatalf("ListPublishEvents() error = %v", err)
	}
	if len(events) < 2 {
		t.Fatalf("publish events = %d, want at least the publish and the unpublish", len(events))
	}
	for _, event := range events {
		if event.Actor != actor.Label {
			t.Errorf("%s event actor = %q (%d characters), want the whole %d-character label",
				event.Action, event.Actor, utf8.RuneCountInString(event.Actor), longActorLabelLength)
		}
		if event.ActorLabel != actor.Label {
			t.Errorf("%s event actorLabel = %q, want the whole label", event.Action, event.ActorLabel)
		}
	}
}
