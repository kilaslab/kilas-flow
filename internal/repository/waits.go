// Durable execution waits: one row per suspension, holding the checkpoint a
// resumed run continues from.
//
// A wait row exists so an execution can stop, give up its worker and its
// lease, survive a process restart, and continue without re-running nodes
// that already completed. The checkpoint column carries the exact upstream
// data the run held at suspension, and it is written verbatim: it must never
// travel through payload(), which would hand the resumed run "[redacted]"
// where its data was. The executions write path redacts by construction and
// stays that way; this table is the one deliberate exception, and the reason
// is this comment.
//
// Lookup is by SHA-256 of the resume token, never the token itself, so a
// database dump, a replica, or a support export discloses no resume
// capability. Refusals mirror engine's in-memory registry exactly — unknown
// (or wrong-tenant, which answers identically so tokens cannot oracle other
// tenants), already-answered, and expired each have their own stable error —
// and the messages are spelled the same on purpose, so the HTTP surface
// reports one vocabulary whichever backing answers.
package repository

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"gorm.io/gorm"
	"gorm.io/gorm/schema"

	"github.com/kilaslab/kilas-flow/internal/execution"
)

var (
	// ErrWaitNotFound reports an unknown token, or a token looked up under
	// the wrong tenant. Deliberately the same error for both: distinguishing
	// them would let a caller probe which executions exist in other tenants.
	// Spelled like engine.ErrWaitNotFound so the refusal reads the same
	// whichever layer answers.
	ErrWaitNotFound = errors.New("approval request was not found")
	// ErrWaitConsumed reports a second call on a single-use token. Spelled
	// like engine.ErrWaitConsumed.
	ErrWaitConsumed = errors.New("approval request was already answered")
	// ErrWaitExpired reports a resume past the ticket's deadline. Spelled
	// like engine.ErrWaitExpired.
	ErrWaitExpired = errors.New("approval request has expired")
)

// Wait outcomes recorded when a wait is consumed.
const (
	// WaitOutcomeResumed marks a wait that continued its execution, by HTTP
	// resume or by the sweeper re-queuing an expired timer.
	WaitOutcomeResumed = "resumed"
	// WaitOutcomeExpired marks a wait the sweeper failed because its deadline
	// passed on a mode with nobody left to answer it.
	WaitOutcomeExpired = "expired"
)

// WaitExpiredCode names the failure a wait past its deadline becomes. A code
// rather than a message so the next node — or the reader of the trace — can
// branch on it instead of parsing prose.
const WaitExpiredCode = "wait.expired"

// executionWaitModel is one suspended execution awaiting resume. The integer
// key is a deliberate break from the text IDs everywhere else: the resume
// path needs the latest consumed wait for an execution, and an
// auto-increment key orders that query deterministically where ordered
// timestamps can tie.
type executionWaitModel struct {
	ID          uint   `gorm:"primaryKey;autoIncrement"`
	TenantID    string `gorm:"not null;size:64;index:idx_execution_waits_execution,priority:1"`
	ExecutionID string `gorm:"not null;size:64;index:idx_execution_waits_execution,priority:2"`
	WorkflowID  string `gorm:"not null;size:64"`
	NodeID      string `gorm:"not null;size:64"`
	Mode        string `gorm:"not null;size:32"`
	// TokenHash is SHA-256 of the resume token, hex-encoded: the indexed
	// lookup key, so a caller holding only the token finds its row.
	TokenHash string `gorm:"not null;size:64;uniqueIndex:uidx_execution_waits_token"`
	// ResumeToken is the bearer token itself, so the approval URL stays
	// retrievable after the suspending worker is long gone: only the hash
	// would make the link unrecoverable. Single-use and expiring, and
	// cleared the moment the wait is consumed, but treat a dump accordingly
	// — whoever holds a live row's token may answer the wait.
	ResumeToken string `gorm:"not null;size:64"`
	// Checkpoint is the runner's exact suspension state as raw JSON, stored
	// verbatim. Never redacted on the way in — redaction would corrupt the
	// resumed run — and never served: the info surface strips it.
	Checkpoint []byte `gorm:"not null"`
	// ResumeOutput is set when the wait is consumed: the suspending node's
	// output (an approval decision, a timer passthrough) that the resumed
	// run starts from. Null until then.
	ResumeOutput []byte
	// RunCount is how many node runs were persisted through this suspension.
	// The resumed segment offsets its sequences past them: without it a
	// second suspension would re-record sequence 1 and collide.
	RunCount  int       `gorm:"not null"`
	ExpiresAt time.Time `gorm:"not null;index:idx_execution_waits_expiry"`
	// No index tag: the expiry and execution indexes cover the sweeper and
	// the resume lookups, and an index declared only in a tag would exist
	// on a developer's machine and on nobody's server.
	ConsumedAt *time.Time
	Outcome    string         `gorm:"not null;size:32"`
	CreatedAt  time.Time      `gorm:"not null"`
	UpdatedAt  time.Time      `gorm:"not null"`
	Execution  executionModel `gorm:"foreignKey:ExecutionID;references:ID;constraint:OnUpdate:CASCADE,OnDelete:RESTRICT"`
}

func (executionWaitModel) TableName(namer schema.Namer) string {
	return namer.TableName("execution_waits")
}

// Wait is one durable suspension as the engine sees it.
type Wait struct {
	ID           uint
	TenantID     string
	ExecutionID  string
	WorkflowID   string
	NodeID       string
	Mode         string
	TokenHash    string
	ResumeToken  string
	Checkpoint   json.RawMessage
	ResumeOutput json.RawMessage
	RunCount     int
	ExpiresAt    time.Time
	ConsumedAt   *time.Time
	Outcome      string
	CreatedAt    time.Time
}

func waitFromModel(model executionWaitModel) Wait {
	return Wait{
		ID: model.ID, TenantID: model.TenantID, ExecutionID: model.ExecutionID,
		WorkflowID: model.WorkflowID, NodeID: model.NodeID, Mode: model.Mode,
		TokenHash: model.TokenHash, ResumeToken: model.ResumeToken,
		Checkpoint:   append(json.RawMessage(nil), model.Checkpoint...),
		ResumeOutput: append(json.RawMessage(nil), model.ResumeOutput...),
		RunCount:     model.RunCount,
		ExpiresAt:    model.ExpiresAt, ConsumedAt: model.ConsumedAt,
		Outcome: model.Outcome, CreatedAt: model.CreatedAt,
	}
}

// HashWaitToken renders a resume token as its lookup key. SHA-256, hex, like
// the API-key verifier: the row proves the caller held the token without
// holding anything that resumes on its own.
func HashWaitToken(token string) string {
	sum := sha256.Sum256([]byte(token))
	return hex.EncodeToString(sum[:])
}

// SuspendWaitParams are the fields of one suspension.
type SuspendWaitParams struct {
	ExecutionID string
	// LeaseOwner must match the claim that ran the graph: only the worker
	// holding the lease may suspend it.
	LeaseOwner string
	WorkflowID string
	NodeID     string
	Mode       string
	// TokenHash is HashWaitToken of the bearer token the caller minted. The
	// token itself travels beside it in ResumeToken: the hash is the indexed
	// lookup, the token keeps the approval URL retrievable after suspend.
	TokenHash string
	// ResumeToken is that bearer token. Cleared when the wait is consumed.
	ResumeToken string
	// Checkpoint is the runner's suspension state. Stored verbatim, never
	// through payload().
	Checkpoint []byte
	// RunCount is how many node runs are already persisted for this
	// execution, so the resumed segment offsets its sequences past them.
	RunCount  int
	ExpiresAt time.Time
}

// SuspendExecution parks a running execution as waiting: the wait row is
// inserted and the execution releases its lease in one transaction, so a
// crash between the two cannot leave a wait nobody owns or an execution that
// looks crashed while its checkpoint sits elsewhere.
func (store *GORMExecutionStore) SuspendExecution(ctx context.Context, tenant TenantScope, params SuspendWaitParams) (Wait, execution.Record, error) {
	if err := tenant.validate(); err != nil {
		return Wait{}, execution.Record{}, err
	}
	if params.ExecutionID == "" || params.LeaseOwner == "" {
		return Wait{}, execution.Record{}, fmt.Errorf("execution ID and lease owner are required")
	}
	if params.NodeID == "" || params.Mode == "" {
		return Wait{}, execution.Record{}, fmt.Errorf("suspending node and wait mode are required")
	}
	if params.TokenHash == "" || params.ResumeToken == "" {
		return Wait{}, execution.Record{}, fmt.Errorf("resume token and its hash are required")
	}
	if len(params.Checkpoint) == 0 {
		return Wait{}, execution.Record{}, fmt.Errorf("wait checkpoint is required")
	}
	if params.ExpiresAt.IsZero() {
		return Wait{}, execution.Record{}, fmt.Errorf("wait deadline is required")
	}
	var wait executionWaitModel
	var suspended executionModel
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		var model executionModel
		if err := tx.Where("tenant_id = ? AND id = ?", tenant.ID, params.ExecutionID).First(&model).Error; err != nil {
			return mapNotFound(err, "execution")
		}
		if execution.Status(model.Status) != execution.StatusRunning {
			return fmt.Errorf("cannot suspend execution %q in status %q", params.ExecutionID, model.Status)
		}
		if model.LeaseOwner == "" || model.LeaseOwner != params.LeaseOwner {
			return fmt.Errorf("cannot suspend execution %q without its lease", params.ExecutionID)
		}
		now := time.Now().UTC()
		wait = executionWaitModel{
			TenantID: tenant.ID, ExecutionID: params.ExecutionID, WorkflowID: model.WorkflowID,
			NodeID: params.NodeID, Mode: params.Mode, TokenHash: params.TokenHash,
			ResumeToken: params.ResumeToken,
			Checkpoint:  append([]byte(nil), params.Checkpoint...),
			RunCount:    params.RunCount,
			ExpiresAt:   params.ExpiresAt.UTC(),
			CreatedAt:   now, UpdatedAt: now,
		}
		if err := tx.Create(&wait).Error; err != nil {
			return fmt.Errorf("persist suspended wait: %w", err)
		}
		if err := tx.Model(&executionModel{}).
			Where("tenant_id = ? AND id = ? AND lease_owner = ? AND status = ?",
				tenant.ID, params.ExecutionID, params.LeaseOwner, string(execution.StatusRunning)).
			Updates(map[string]any{
				"status":           string(execution.StatusWaiting),
				"lease_owner":      "",
				"lease_expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("park execution as waiting: %w", err)
		}
		model.Status = string(execution.StatusWaiting)
		model.LeaseOwner = ""
		model.LeaseExpiresAt = nil
		suspended = model
		return nil
	})
	if err != nil {
		return Wait{}, execution.Record{}, err
	}
	return waitFromModel(wait), executionFromModel(suspended), nil
}

// FindWaitByToken loads a wait by its token hash for the info surface. The
// caller strips the checkpoint before responding: it is run data, and the
// info call is answered to whoever holds the token link.
func (store *GORMExecutionStore) FindWaitByToken(ctx context.Context, tokenHash string) (Wait, error) {
	if tokenHash == "" {
		return Wait{}, ErrWaitNotFound
	}
	var model executionWaitModel
	if err := store.db.WithContext(ctx).Where("token_hash = ?", tokenHash).First(&model).Error; err != nil {
		if errors.Is(err, gorm.ErrRecordNotFound) {
			return Wait{}, ErrWaitNotFound
		}
		return Wait{}, fmt.Errorf("lookup suspended wait: %w", err)
	}
	return waitFromModel(model), nil
}

// FindActiveWait returns the unconsumed wait holding an execution, if any.
// The read surface uses it to enrich a waiting execution with its resume
// link; the checkpoint travels only on the resume path, never here.
func (store *GORMExecutionStore) FindActiveWait(ctx context.Context, tenant TenantScope, executionID string) (Wait, error) {
	if err := tenant.validate(); err != nil {
		return Wait{}, err
	}
	if executionID == "" {
		return Wait{}, fmt.Errorf("execution ID is required")
	}
	var model executionWaitModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND execution_id = ? AND consumed_at IS NULL", tenant.ID, executionID).
		Order("id DESC").First(&model).Error; err != nil {
		return Wait{}, mapNotFound(err, "suspended wait")
	}
	return waitFromModel(model), nil
}

// ResumeWait consumes one token and re-queues its execution in a single
// transaction. The checks refuse without consuming — an embed-denied,
// already-answered, or expired call never marks anything — so each refusal
// is stable: asking again gets the same answer, not the next error in the
// list. CallerTenant is the caller's tenant when one is known; empty is the
// public bearer link, which carries no identity beyond the token itself.
func (store *GORMExecutionStore) ResumeWait(ctx context.Context, callerTenant, tokenHash string, resumeOutput json.RawMessage, now time.Time) (Wait, execution.Record, error) {
	if tokenHash == "" {
		return Wait{}, execution.Record{}, ErrWaitNotFound
	}
	var wait executionWaitModel
	var queued executionModel
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("token_hash = ?", tokenHash).First(&wait).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitNotFound
			}
			return fmt.Errorf("lookup suspended wait: %w", err)
		}
		if callerTenant != "" && wait.TenantID != callerTenant {
			return ErrWaitNotFound
		}
		if wait.ConsumedAt != nil {
			return ErrWaitConsumed
		}
		if !now.Before(wait.ExpiresAt) {
			return ErrWaitExpired
		}
		var model executionModel
		if err := tx.Where("tenant_id = ? AND id = ?", wait.TenantID, wait.ExecutionID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitConsumed
			}
			return fmt.Errorf("lookup waiting execution: %w", err)
		}
		if execution.Status(model.Status) != execution.StatusWaiting {
			return ErrWaitConsumed
		}
		consumed := now.UTC()
		output := append([]byte(nil), resumeOutput...)
		if err := tx.Model(&executionWaitModel{}).Where("id = ? AND consumed_at IS NULL", wait.ID).
			Updates(map[string]any{
				"consumed_at": &consumed, "outcome": WaitOutcomeResumed,
				"resume_output": output, "resume_token": "", "updated_at": consumed,
			}).Error; err != nil {
			return fmt.Errorf("consume suspended wait: %w", err)
		}
		if err := tx.Model(&executionModel{}).
			Where("tenant_id = ? AND id = ? AND status = ?", wait.TenantID, wait.ExecutionID, string(execution.StatusWaiting)).
			Updates(map[string]any{
				"status":           string(execution.StatusQueued),
				"lease_owner":      "",
				"lease_expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("re-queue waiting execution: %w", err)
		}
		wait.ConsumedAt = &consumed
		wait.Outcome = WaitOutcomeResumed
		wait.ResumeOutput = output
		wait.ResumeToken = ""
		model.Status = string(execution.StatusQueued)
		model.LeaseOwner = ""
		model.LeaseExpiresAt = nil
		queued = model
		return nil
	})
	if err != nil {
		return Wait{}, execution.Record{}, err
	}
	return waitFromModel(wait), executionFromModel(queued), nil
}

// ExpiredResolution tells the sweeper what a wait past its deadline becomes:
// timer waits resume, everything else fails by name.
type ExpiredResolution struct {
	// Requeue continues the execution instead of failing it. Timer modes use
	// this: their deadline is the resume, not the end.
	Requeue bool
	// ResumeOutput continues a re-queued wait. The service synthesizes it
	// (a timer passthrough); ignored unless Requeue is set.
	ResumeOutput json.RawMessage
	// FailCode names the failure when the wait does not re-queue. Empty
	// keeps WaitExpiredCode.
	FailCode string
}

// SettleExpiredWait consumes one expired wait the sweeper reaped, either
// re-queuing its execution or failing it by name, in one transaction. A wait
// another call already consumed reports ErrWaitConsumed so the sweeper moves
// on rather than failing an execution twice.
func (store *GORMExecutionStore) SettleExpiredWait(ctx context.Context, waitID uint, resolution ExpiredResolution, now time.Time) (Wait, execution.Record, error) {
	if waitID == 0 {
		return Wait{}, execution.Record{}, fmt.Errorf("wait ID is required")
	}
	var wait executionWaitModel
	var settled executionModel
	err := store.db.WithContext(ctx).Transaction(func(tx *gorm.DB) error {
		if err := tx.Where("id = ?", waitID).First(&wait).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitNotFound
			}
			return fmt.Errorf("lookup suspended wait: %w", err)
		}
		if wait.ConsumedAt != nil {
			return ErrWaitConsumed
		}
		if now.Before(wait.ExpiresAt) {
			return fmt.Errorf("wait %d has not expired", waitID)
		}
		var model executionModel
		if err := tx.Where("tenant_id = ? AND id = ?", wait.TenantID, wait.ExecutionID).First(&model).Error; err != nil {
			if errors.Is(err, gorm.ErrRecordNotFound) {
				return ErrWaitConsumed
			}
			return fmt.Errorf("lookup waiting execution: %w", err)
		}
		if execution.Status(model.Status) != execution.StatusWaiting {
			return ErrWaitConsumed
		}
		consumed := now.UTC()
		if resolution.Requeue {
			output := append([]byte(nil), resolution.ResumeOutput...)
			if err := tx.Model(&executionWaitModel{}).Where("id = ? AND consumed_at IS NULL", wait.ID).
				Updates(map[string]any{
					"consumed_at": &consumed, "outcome": WaitOutcomeResumed,
					"resume_output": output, "resume_token": "", "updated_at": consumed,
				}).Error; err != nil {
				return fmt.Errorf("consume expired timer wait: %w", err)
			}
			if err := tx.Model(&executionModel{}).
				Where("tenant_id = ? AND id = ? AND status = ?", wait.TenantID, wait.ExecutionID, string(execution.StatusWaiting)).
				Updates(map[string]any{
					"status":           string(execution.StatusQueued),
					"lease_owner":      "",
					"lease_expires_at": nil,
				}).Error; err != nil {
				return fmt.Errorf("re-queue expired timer wait: %w", err)
			}
			wait.ConsumedAt = &consumed
			wait.Outcome = WaitOutcomeResumed
			wait.ResumeOutput = output
			wait.ResumeToken = ""
			model.Status = string(execution.StatusQueued)
			model.LeaseOwner = ""
			model.LeaseExpiresAt = nil
			settled = model
			return nil
		}
		code := resolution.FailCode
		if code == "" {
			code = WaitExpiredCode
		}
		failure, err := payload(json.RawMessage(fmt.Sprintf(
			`{"code":%q,"message":%q}`,
			code, fmt.Sprintf("node %q was still waiting when its deadline passed", wait.NodeID))))
		if err != nil {
			return fmt.Errorf("wait expiry error: %w", err)
		}
		finished := now.UTC()
		if err := tx.Model(&executionWaitModel{}).Where("id = ? AND consumed_at IS NULL", wait.ID).
			Updates(map[string]any{
				"consumed_at": &consumed, "outcome": WaitOutcomeExpired,
				"resume_token": "", "updated_at": consumed,
			}).Error; err != nil {
			return fmt.Errorf("consume expired wait: %w", err)
		}
		if err := tx.Model(&executionModel{}).
			Where("tenant_id = ? AND id = ? AND status = ?", wait.TenantID, wait.ExecutionID, string(execution.StatusWaiting)).
			Updates(map[string]any{
				"status": string(execution.StatusFailed), "error": failure,
				"finished_at": &finished, "lease_owner": "", "lease_expires_at": nil,
			}).Error; err != nil {
			return fmt.Errorf("fail expired wait: %w", err)
		}
		wait.ConsumedAt = &consumed
		wait.Outcome = WaitOutcomeExpired
		wait.ResumeToken = ""
		wait.UpdatedAt = consumed
		model.Status = string(execution.StatusFailed)
		model.Error = failure
		model.FinishedAt = &finished
		model.LeaseOwner = ""
		model.LeaseExpiresAt = nil
		settled = model
		return nil
	})
	if err != nil {
		return Wait{}, execution.Record{}, err
	}
	return waitFromModel(wait), executionFromModel(settled), nil
}

// LoadResumeState returns the latest consumed wait that continued an
// execution, which is the checkpoint a claimed re-queued run resumes from.
// A fresh execution has no wait rows and reports ErrNotFound: the claim path
// runs this once per claim, and not-found simply means a fresh run.
func (store *GORMExecutionStore) LoadResumeState(ctx context.Context, tenant TenantScope, executionID string) (Wait, error) {
	if err := tenant.validate(); err != nil {
		return Wait{}, err
	}
	if executionID == "" {
		return Wait{}, fmt.Errorf("execution ID is required")
	}
	var model executionWaitModel
	if err := store.db.WithContext(ctx).
		Where("tenant_id = ? AND execution_id = ? AND consumed_at IS NOT NULL AND outcome = ?",
			tenant.ID, executionID, WaitOutcomeResumed).
		Order("id DESC").First(&model).Error; err != nil {
		return Wait{}, mapNotFound(err, "suspended wait")
	}
	return waitFromModel(model), nil
}

// defaultExpiredWaitPage bounds one sweep so a fleet of forgotten approvals
// cannot hold the sweeper past its tick.
const defaultExpiredWaitPage = 100

// ListExpiredWaits returns unconsumed waits past their deadline, oldest
// deadline first, for the sweeper to settle. Internal: no tenant scope, the
// sweeper settles every tenant's expired waits on its period.
func (store *GORMExecutionStore) ListExpiredWaits(ctx context.Context, now time.Time, limit int) ([]Wait, error) {
	if limit <= 0 {
		limit = defaultExpiredWaitPage
	}
	var models []executionWaitModel
	if err := store.db.WithContext(ctx).
		Where("consumed_at IS NULL AND expires_at <= ?", now.UTC()).
		Order("expires_at ASC, id ASC").Limit(limit).Find(&models).Error; err != nil {
		return nil, fmt.Errorf("list expired waits: %w", err)
	}
	waits := make([]Wait, 0, len(models))
	for _, model := range models {
		waits = append(waits, waitFromModel(model))
	}
	return waits, nil
}
