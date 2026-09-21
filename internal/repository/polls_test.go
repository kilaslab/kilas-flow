package repository_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func TestClaimDueLeasesADuePollAndSkipsAnOwnedLease(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-poll-lease"}
		catalogue := node.NewRegistry()
		if err := nodes.RegisterAll(catalogue); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		workflows := repository.NewWorkflowStore(db.DB).WithPolls(nodes.ExtractPolls)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_poll",
			Name:          "Gmail poll",
			Nodes: []workflow.Node{{
				ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
				Credentials: map[string]string{nodes.GmailCredentialType: "cred-gmail"},
				Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
			}},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalogue); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		cursors := repository.NewPollCursorStore(db.DB)
		now := time.Now().UTC().Add(time.Minute)
		first, err := cursors.ClaimDue(ctx, now, "worker-a", time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue() error = %v", err)
		}
		if len(first) != 1 || first[0].Cursor.NodeID != "gmail" || first[0].WorkflowVersionID == "" {
			t.Fatalf("first claim = %#v", first)
		}
		second, err := cursors.ClaimDue(ctx, now, "worker-b", time.Minute)
		if err != nil {
			t.Fatalf("second ClaimDue() error = %v", err)
		}
		if len(second) != 0 {
			t.Fatalf("a leased poll was claimed again: %#v", second)
		}
		expired, err := cursors.ClaimDue(ctx, now.Add(2*time.Minute), "worker-b", time.Minute)
		if err != nil {
			t.Fatalf("expired ClaimDue() error = %v", err)
		}
		if len(expired) != 1 || expired[0].Cursor.ID != first[0].Cursor.ID {
			t.Fatalf("expired claim = %#v, want the same cursor after the lease lapsed", expired)
		}
	})
}

func TestActivatePreservesPollCursor(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-poll-preserve"}
		catalogue := node.NewRegistry()
		if err := nodes.RegisterAll(catalogue); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		workflows := repository.NewWorkflowStore(db.DB).WithPolls(nodes.ExtractPolls)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_poll_keep",
			Name:          "Gmail poll",
			Nodes: []workflow.Node{{
				ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
				Credentials: map[string]string{nodes.GmailCredentialType: "cred-gmail"},
				Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
			}},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalogue); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		cursors := repository.NewPollCursorStore(db.DB)
		now := time.Now().UTC().Add(time.Minute)
		first, err := cursors.ClaimDue(ctx, now, "worker-a", time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue() error = %v", err)
		}
		if len(first) != 1 {
			t.Fatalf("first claim = %#v", first)
		}
		if err := cursors.Complete(ctx, first[0].Cursor.ID, `{"historyId":"99"}`, now.Add(time.Minute), "worker-a"); err != nil {
			t.Fatalf("Complete() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalogue); err != nil {
			t.Fatalf("second Activate() error = %v", err)
		}
		again, err := cursors.ClaimDue(ctx, now.Add(2*time.Minute), "worker-b", time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue after reactivate error = %v", err)
		}
		if len(again) != 1 {
			t.Fatalf("claim after reactivate = %#v", again)
		}
		if again[0].Cursor.ID != first[0].Cursor.ID {
			t.Fatalf("poll row was recreated: %s -> %s", first[0].Cursor.ID, again[0].Cursor.ID)
		}
		if again[0].Cursor.Cursor != `{"historyId":"99"}` {
			t.Fatalf("cursor = %q, want the previously stored history id", again[0].Cursor.Cursor)
		}
	})
}

func TestClaimDueReturnsOnePollAtATime(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-poll-one"}
		catalogue := node.NewRegistry()
		if err := nodes.RegisterAll(catalogue); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		workflows := repository.NewWorkflowStore(db.DB).WithPolls(nodes.ExtractPolls)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_poll_two",
			Name:          "Two polls",
			Nodes: []workflow.Node{
				{
					ID: "gmail-a", Name: "Gmail A", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
					Credentials: map[string]string{nodes.GmailCredentialType: "cred-gmail"},
					Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
				},
				{
					ID: "gmail-b", Name: "Gmail B", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
					Credentials: map[string]string{nodes.GmailCredentialType: "cred-gmail"},
					Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
				},
			},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalogue); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		cursors := repository.NewPollCursorStore(db.DB)
		now := time.Now().UTC().Add(time.Minute)
		first, err := cursors.ClaimDue(ctx, now, "worker-a", time.Minute)
		if err != nil {
			t.Fatalf("first ClaimDue() error = %v", err)
		}
		if len(first) != 1 {
			t.Fatalf("first claim = %#v, want exactly one row", first)
		}
		second, err := cursors.ClaimDue(ctx, now, "worker-b", time.Minute)
		if err != nil {
			t.Fatalf("second ClaimDue() error = %v", err)
		}
		if len(second) != 1 {
			t.Fatalf("second claim = %#v, want the other row", second)
		}
		if first[0].Cursor.ID == second[0].Cursor.ID {
			t.Fatal("both workers claimed the same poll")
		}
		third, err := cursors.ClaimDue(ctx, now, "worker-c", time.Minute)
		if err != nil {
			t.Fatalf("third ClaimDue() error = %v", err)
		}
		if len(third) != 0 {
			t.Fatalf("third claim = %#v, want nothing left", third)
		}
	})
}

func TestCompleteDoesNotRewindAfterLeaseStolen(t *testing.T) {
	eachDriver(t, func(t *testing.T, db *database.DB) {
		ctx := context.Background()
		tenant := repository.TenantScope{ID: "drv-poll-stale"}
		catalogue := node.NewRegistry()
		if err := nodes.RegisterAll(catalogue); err != nil {
			t.Fatalf("RegisterAll() error = %v", err)
		}
		workflows := repository.NewWorkflowStore(db.DB).WithPolls(nodes.ExtractPolls)
		saved, err := workflows.SaveDraft(ctx, tenant, workflow.Document{
			SchemaVersion: workflow.CurrentSchemaVersion,
			ID:            "wf_poll_stale",
			Name:          "Gmail poll",
			Nodes: []workflow.Node{{
				ID: "gmail", Name: "Gmail Trigger", Type: nodes.GmailTriggerNodeType, TypeVersion: workflow.V(1),
				Credentials: map[string]string{nodes.GmailCredentialType: "cred-gmail"},
				Parameters:  map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyMinute"}}}},
			}},
			Connections: []workflow.Connection{},
			Settings:    map[string]any{},
		})
		if err != nil {
			t.Fatalf("SaveDraft() error = %v", err)
		}
		if _, err := workflows.Activate(ctx, tenant, saved.ID, catalogue); err != nil {
			t.Fatalf("Activate() error = %v", err)
		}

		cursors := repository.NewPollCursorStore(db.DB)
		now := time.Now().UTC().Add(time.Minute)
		first, err := cursors.ClaimDue(ctx, now, "worker-a", time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue() error = %v", err)
		}
		if len(first) != 1 {
			t.Fatalf("first claim = %#v", first)
		}
		stolen, err := cursors.ClaimDue(ctx, now.Add(2*time.Minute), "worker-b", time.Minute)
		if err != nil {
			t.Fatalf("stolen ClaimDue() error = %v", err)
		}
		if len(stolen) != 1 || stolen[0].Cursor.ID != first[0].Cursor.ID {
			t.Fatalf("stolen claim = %#v", stolen)
		}
		err = cursors.Complete(ctx, first[0].Cursor.ID, `{"rewound":true}`, now.Add(time.Hour), "worker-a")
		if !errors.Is(err, repository.ErrNotFound) {
			t.Fatalf("stale Complete() = %v, want not found", err)
		}
		if err := cursors.Complete(ctx, stolen[0].Cursor.ID, `{"advanced":true}`, now.Add(time.Hour), "worker-b"); err != nil {
			t.Fatalf("owner Complete() error = %v", err)
		}
		again, err := cursors.ClaimDue(ctx, now.Add(2*time.Hour), "worker-c", time.Minute)
		if err != nil {
			t.Fatalf("ClaimDue after owner complete error = %v", err)
		}
		if len(again) != 1 {
			t.Fatalf("claim after owner complete = %#v", again)
		}
		if again[0].Cursor.Cursor != `{"advanced":true}` {
			t.Fatalf("cursor = %q, stale complete rewound the watermark", again[0].Cursor.Cursor)
		}
	})
}
