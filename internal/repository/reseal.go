package repository

import (
	"context"
	"fmt"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// resealBatchSize bounds how many rows one Reseal step reads at a time. The
// pass walks every tenant's rows by key, so it never holds the whole table and
// never holds a transaction open while it works.
const resealBatchSize = 200

// ResealReport is what one reseal pass did.
type ResealReport struct {
	// Resealed counts rows written again under the primary key: legacy v0
	// payloads and v1 payloads under a secondary key.
	Resealed int
	// Current counts rows already sealed as v1 under the primary key.
	Current int
	// Skipped counts rows another writer changed between this pass reading
	// them and writing them back. The other writer sealed under the primary
	// key already, so nothing is lost; a second pass reports them current.
	Skipped int
	// Unopenable names rows no configured key opens. They are left exactly
	// as they were. A non-empty list means a key that sealed them is missing
	// from the keyring.
	Unopenable []string
}

// Reseal writes every credential payload that is not v1 under the primary key
// again, across every tenant.
//
// It is the second half of a key rotation (the first is configuring the new
// key as primary and the old one as secondary) and the one-time upgrade that
// binds the stored legacy payloads to their rows. It upgrades what the
// database holds, not a copy taken before it ran: a legacy payload carries no
// AAD, so a copy of one from a dump, a backup or a replica still opens in any
// row while its key can open legacy payloads. security.legacy_seals: refuse,
// or rotating to a new key, retires those copies.
//
// Each write is a compare-and-swap on the payload that was read, so a row an
// ordinary request updates in the meantime keeps the request's value. It does
// not touch updated_at: a reseal changes how a credential is stored, not what
// it is.
func (store *GORMCredentialStore) Reseal(ctx context.Context) (ResealReport, error) {
	if store.cipher == nil {
		return ResealReport{}, fmt.Errorf("credential encryption is not configured")
	}
	report := ResealReport{}
	after := ""
	for {
		var batch []credentialModel
		if err := store.db.WithContext(ctx).Model(&credentialModel{}).
			Select("id", "tenant_id", "type", "payload").
			Where("id > ?", after).Order("id ASC").Limit(resealBatchSize).
			Find(&batch).Error; err != nil {
			return report, fmt.Errorf("read credentials to reseal: %w", err)
		}
		for _, model := range batch {
			after = model.ID
			aad := credentials.CredentialAAD(model.TenantID, model.ID, model.Type)
			fields, stale, err := store.cipher.OpenForReseal(model.Payload, aad)
			if err != nil {
				report.Unopenable = append(report.Unopenable, model.ID)
				continue
			}
			if !stale {
				report.Current++
				continue
			}
			sealed, err := store.cipher.Seal(fields, aad)
			if err != nil {
				return report, fmt.Errorf("reseal credential %s: %w", model.ID, err)
			}
			if store.resealHook != nil {
				store.resealHook(model.ID)
			}
			result := store.db.WithContext(ctx).Model(&credentialModel{}).
				Where("id = ? AND tenant_id = ? AND payload = ?", model.ID, model.TenantID, model.Payload).
				UpdateColumn("payload", sealed)
			if result.Error != nil {
				return report, fmt.Errorf("write resealed credential %s: %w", model.ID, result.Error)
			}
			if result.RowsAffected == 0 {
				report.Skipped++
				continue
			}
			report.Resealed++
		}
		if len(batch) < resealBatchSize {
			break
		}
	}
	return report, nil
}

// ResealLifecycleState is Reseal for what triggers' registrations captured,
// which is sealed with the same key: a rotation that left it behind would
// break every trigger that verifies deliveries with a captured secret the
// moment the old key was removed.
func (store *GORMWorkflowStore) ResealLifecycleState(ctx context.Context) (ResealReport, error) {
	if store.lifecycleCipher == nil {
		return ResealReport{}, fmt.Errorf("lifecycle state is not kept: the credential encryption key is not configured")
	}
	report := ResealReport{}
	var after uint
	for {
		var batch []webhookRouteModel
		if err := store.db.WithContext(ctx).Model(&webhookRouteModel{}).
			Select("id", "tenant_id", "route", "lifecycle_state").
			Where("id > ? AND lifecycle_state IS NOT NULL", after).Order("id ASC").Limit(resealBatchSize).
			Find(&batch).Error; err != nil {
			return report, fmt.Errorf("read lifecycle state to reseal: %w", err)
		}
		for _, model := range batch {
			after = model.ID
			if len(model.LifecycleState) == 0 {
				continue
			}
			aad := credentials.LifecycleAAD(model.TenantID, model.Route)
			values, stale, err := store.lifecycleCipher.OpenForReseal(model.LifecycleState, aad)
			if err != nil {
				report.Unopenable = append(report.Unopenable, "webhook route "+model.Route)
				continue
			}
			if !stale {
				report.Current++
				continue
			}
			sealed, err := store.lifecycleCipher.Seal(values, aad)
			if err != nil {
				return report, fmt.Errorf("reseal lifecycle state of route %s: %w", model.Route, err)
			}
			result := store.db.WithContext(ctx).Model(&webhookRouteModel{}).
				Where("id = ? AND tenant_id = ? AND lifecycle_state = ?", model.ID, model.TenantID, model.LifecycleState).
				UpdateColumn("lifecycle_state", sealed)
			if result.Error != nil {
				return report, fmt.Errorf("write resealed lifecycle state of route %s: %w", model.Route, result.Error)
			}
			if result.RowsAffected == 0 {
				report.Skipped++
				continue
			}
			report.Resealed++
		}
		if len(batch) < resealBatchSize {
			break
		}
	}
	return report, nil
}
