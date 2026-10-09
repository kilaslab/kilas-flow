package repository

import (
	"context"
	"fmt"
	"strings"
	"time"
)

// What a credential event records. They are the operations a person or a key
// performs on a credential; a token refresh is the system acting on its own
// and records nothing.
const (
	CredentialEventCreated = "created"
	CredentialEventUpdated = "updated"
	CredentialEventDeleted = "deleted"
	// CredentialEventTested is a test that spent the stored secret, whether
	// through the credential's own test or an unsaved-edit test that named it.
	// Detail is ok, failed or untestable.
	CredentialEventTested = "tested"
	// CredentialEventConnectStarted is a Google Connect popup being opened.
	// Detail is the state's nonce hash, which the state already carries in
	// its URL, so the callback can find who started it.
	CredentialEventConnectStarted = "connect-started"
	// CredentialEventConnected is Google's callback storing the tokens. It
	// runs without a principal; its actor is the one that started the flow.
	CredentialEventConnected = "connected"
)

// CredentialEvent is one entry in a credential's audit trail.
type CredentialEvent struct {
	CredentialID string
	// CredentialName and CredentialType are what the credential was called
	// and was when the event happened, kept because the row may be gone.
	CredentialName string
	CredentialType string
	Action         string
	// Actor is who did it. Only Kind, Label and KeyID are kept; an empty
	// actor is recorded as nobody rather than guessed.
	Actor     Actor
	Detail    string
	CreatedAt time.Time
}

// RecordCredentialEvent appends one entry to a credential's audit trail.
//
// It needs no cipher: a deployment with credential storage disabled has no
// credentials to audit, but the trail itself never holds a secret.
func (store *GORMCredentialStore) RecordCredentialEvent(ctx context.Context, tenant TenantScope, event CredentialEvent) error {
	if err := tenant.validate(); err != nil {
		return err
	}
	if strings.TrimSpace(event.CredentialID) == "" || strings.TrimSpace(event.Action) == "" {
		return fmt.Errorf("a credential event needs a credential and an action")
	}
	model := credentialEventModel{
		TenantID: tenant.ID, CredentialID: event.CredentialID,
		CredentialName: truncateEventText(event.CredentialName, 255),
		CredentialType: truncateEventText(event.CredentialType, 64),
		Action:         event.Action,
		ActorKind:      truncateEventText(event.Actor.Kind, 16),
		ActorLabel:     truncateEventText(event.Actor.Label, 255),
		ActorKeyID:     truncateEventText(event.Actor.KeyID, 64),
		Detail:         truncateEventText(event.Detail, 255),
		CreatedAt:      time.Now().UTC(),
	}
	if err := store.db.WithContext(ctx).Create(&model).Error; err != nil {
		return fmt.Errorf("record credential event: %w", err)
	}
	return nil
}

// CredentialEvents returns one credential's audit trail, newest first. A
// credential that no longer exists still has one.
func (store *GORMCredentialStore) CredentialEvents(ctx context.Context, tenant TenantScope, credentialID string) ([]CredentialEvent, error) {
	if err := tenant.validate(); err != nil {
		return nil, err
	}
	var models []credentialEventModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND credential_id = ?", tenant.ID, credentialID).
		Order("created_at DESC, id DESC").Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list credential events: %w", err)
	}
	events := make([]CredentialEvent, 0, len(models))
	for _, model := range models {
		events = append(events, CredentialEvent{
			CredentialID: model.CredentialID, CredentialName: model.CredentialName,
			CredentialType: model.CredentialType, Action: model.Action,
			Actor:  Actor{Kind: model.ActorKind, Label: model.ActorLabel, KeyID: model.ActorKeyID},
			Detail: model.Detail, CreatedAt: model.CreatedAt,
		})
	}
	return events, nil
}

// truncateEventText keeps a value inside its column. PostgreSQL refuses an
// over-long varchar outright, and an audit row lost to a long name is worse
// than a shortened name.
func truncateEventText(value string, limit int) string {
	if len(value) <= limit {
		return value
	}
	// A UTF-8 sequence split by the cut is invalid and dropped whole.
	return strings.ToValidUTF8(value[:limit], "")
}
