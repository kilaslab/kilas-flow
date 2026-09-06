// Cross-process fan-out: live execution events and cancellation interrupts
// delivered over PostgreSQL LISTEN/NOTIFY.
//
// The durable queue (ClaimNext) and the transactional schedule claim
// (ClaimDue) already make a second process safe; what they cannot do is make
// it immediate or visible. Three things ride a notification channel instead:
//
//   - execution events: a worker publishes node progress into its own broker,
//     and every API process republishes the notice into its local broker so a
//     browser connected there sees the run live;
//   - cancellation: Cancel persists cancelling in the row (the floor is a
//     poll in runOnce plus a ctx check between nodes in the runner), and the
//     notice interrupts the process actually holding the lease without
//     waiting out the poll.
//
// Payloads carry identifiers only, never the row. PostgreSQL caps a NOTIFY
// payload at roughly 8000 bytes and an event's Data holds a node's whole
// output, which exceeds that on any real workflow; a woken process re-reads
// the durable record it names. Delivery is best effort by design: a dropped
// wake or cancel costs latency (the poll tick stays the fallback), and a
// dropped event costs a live update (the GET surface still has the trace).
//
// Channels are derived from the table prefix for the same reason the wake
// channel is: advisory behaviour is per-database, not per-schema, so two
// prefixed installs sharing one database must not interrupt or render each
// other's runs.
package engine

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	"github.com/jackc/pgx/v5"

	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/execution"
)

// executionEventsBaseName is the unprefixed LISTEN/NOTIFY channel live
// execution events fan out on. The channel on the wire always carries the
// table prefix (see ExecutionEventsChannel); the base name never does.
const executionEventsBaseName = "execution_events"

// executionCancelBaseName is the unprefixed LISTEN/NOTIFY channel
// cancellation interrupts ride on. Prefixing works exactly as for events.
const executionCancelBaseName = "execution_cancel"

// ExecutionEventsChannel derives the PostgreSQL LISTEN/NOTIFY channel for
// live execution events from the configured table prefix, so two prefixed
// installs sharing one database do not render each other's runs.
func ExecutionEventsChannel(tablePrefix string) string {
	return tablePrefix + executionEventsBaseName
}

// ExecutionCancelChannel derives the PostgreSQL LISTEN/NOTIFY channel for
// cancellation interrupts from the configured table prefix, for the same
// reason.
func ExecutionCancelChannel(tablePrefix string) string {
	return tablePrefix + executionCancelBaseName
}

// eventNotice is one cross-process execution event: identifiers only, never
// the node's output. A typical encoding is tens of bytes, far inside the
// ~8000-byte NOTIFY cap.
type eventNotice struct {
	TenantID    string           `json:"tenant_id"`
	ExecutionID string           `json:"execution_id"`
	WorkflowID  string           `json:"workflow_id,omitempty"`
	NodeID      string           `json:"node_id,omitempty"`
	Type        events.Type      `json:"type"`
	Status      execution.Status `json:"status,omitempty"`
	Sequence    int              `json:"sequence,omitempty"`
	Origin      string           `json:"origin,omitempty"`
}

// cancelNotice asks the process holding a lease to stop. Identifiers only,
// for the same payload-cap reason as events.
type cancelNotice struct {
	TenantID    string `json:"tenant_id"`
	ExecutionID string `json:"execution_id"`
	Origin      string `json:"origin,omitempty"`
}

func encodeEventNotice(origin string, event events.Event) (string, error) {
	out, err := json.Marshal(eventNotice{
		TenantID: event.TenantID, ExecutionID: event.ExecutionID,
		WorkflowID: event.WorkflowID, NodeID: event.NodeID,
		Type: event.Type, Status: event.Status, Sequence: event.Sequence,
		Origin: origin,
	})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func decodeEventNotice(payload string) (eventNotice, error) {
	var notice eventNotice
	if err := json.Unmarshal([]byte(payload), &notice); err != nil {
		return eventNotice{}, err
	}
	if notice.TenantID == "" || notice.ExecutionID == "" || notice.Type == "" {
		return eventNotice{}, fmt.Errorf("execution event notice names no tenant, execution, or type")
	}
	return notice, nil
}

func encodeCancelNotice(origin, tenantID, executionID string) (string, error) {
	out, err := json.Marshal(cancelNotice{TenantID: tenantID, ExecutionID: executionID, Origin: origin})
	if err != nil {
		return "", err
	}
	return string(out), nil
}

func decodeCancelNotice(payload string) (cancelNotice, error) {
	var notice cancelNotice
	if err := json.Unmarshal([]byte(payload), &notice); err != nil {
		return cancelNotice{}, err
	}
	if notice.TenantID == "" || notice.ExecutionID == "" {
		return cancelNotice{}, fmt.Errorf("execution cancel notice names no tenant or execution")
	}
	return notice, nil
}

// notifyEvent relays one published event to every listening process. It is
// best effort: the local broker already has the event, so a relay failure
// costs a remote live update, never correctness.
func (service *Service) notifyEvent(event events.Event) {
	if service.relaySend == nil {
		return
	}
	payload, err := encodeEventNotice(service.workerID, event)
	if err != nil {
		service.log.Error("encode cross-process execution event", "error", err)
		return
	}
	if err := service.relaySend(ExecutionEventsChannel(service.relayPrefix), payload); err != nil {
		service.log.Error("relay cross-process execution event", "error", err)
	}
}

// notifyCancel relays one cancellation request to the process holding the
// lease. Best effort like events: the row already says cancelling, so a
// dropped notice costs latency (the holder's poll still finds it), never a
// stuck run.
func (service *Service) notifyCancel(tenantID, executionID string) {
	if service.relaySend == nil {
		return
	}
	payload, err := encodeCancelNotice(service.workerID, tenantID, executionID)
	if err != nil {
		service.log.Error("encode cross-process execution cancel", "error", err)
		return
	}
	if err := service.relaySend(ExecutionCancelChannel(service.relayPrefix), payload); err != nil {
		service.log.Error("relay cross-process execution cancel", "error", err)
	}
}

// WatchRemoteEvents LISTENs for execution events published by worker
// processes and republishes them into this process's broker, so a browser
// connected here sees runs executing elsewhere live.
//
// Run it on processes serving the API, not (only) on workers: a worker-only
// process has no browser audience, and a process that both publishes and
// watches skips its own origin rather than rendering every event twice. The
// 100 ms worker poll has no equivalent here — there is nothing to fall back
// to — which is acceptable because events are live updates only: the durable
// trace behind GET stays complete whether or not a notice lands.
func (service *Service) WatchRemoteEvents(ctx context.Context, dsn, tablePrefix string, onError func(error)) error {
	return watchNotifyChannel(ctx, dsn, ExecutionEventsChannel(tablePrefix), onError, func(payload string) {
		notice, err := decodeEventNotice(payload)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		if notice.Origin != "" && notice.Origin == service.workerID {
			return
		}
		if service.events == nil {
			return
		}
		service.events.Publish(events.Event{
			TenantID: notice.TenantID, ExecutionID: notice.ExecutionID,
			WorkflowID: notice.WorkflowID, NodeID: notice.NodeID,
			Type: notice.Type, Status: notice.Status, Sequence: notice.Sequence,
			At: time.Now().UTC(),
		})
	})
}

// WatchCancellations LISTENs for cancellation interrupts and stops the local
// run holding the named lease, if any. A notice for an execution this process
// does not hold is simply ignored: the holder (if any) got its own copy.
func (service *Service) WatchCancellations(ctx context.Context, dsn, tablePrefix string, onError func(error)) error {
	return watchNotifyChannel(ctx, dsn, ExecutionCancelChannel(tablePrefix), onError, func(payload string) {
		notice, err := decodeCancelNotice(payload)
		if err != nil {
			if onError != nil {
				onError(err)
			}
			return
		}
		service.activeMu.Lock()
		cancel := service.active[notice.ExecutionID]
		service.activeMu.Unlock()
		if cancel != nil {
			cancel()
		}
	})
}

// watchNotifyChannel holds one LISTEN connection until it drops, reconnecting
// with backoff. It mirrors repository.WatchExecutions: a dropped listener
// reports to onError and retries, a malformed payload reports and drops the
// notification rather than the listener, and stopping the context stops
// quietly rather than as an error.
func watchNotifyChannel(ctx context.Context, dsn, channel string, onError func(error), onNotice func(string)) error {
	// Validated by config, but a listener built by hand should not turn an
	// odd prefix into SQL text: pgx quotes the identifier.
	listen := "LISTEN " + pgx.Identifier{channel}.Sanitize()
	backoff := time.Second
	const maxBackoff = 30 * time.Second
	for {
		if err := watchNotifyOnce(ctx, dsn, listen, channel, onError, onNotice); err == nil {
			return nil
		} else if ctx.Err() != nil {
			return nil
		} else {
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(backoff):
			}
			backoff *= 2
			if backoff > maxBackoff {
				backoff = maxBackoff
			}
		}
	}
}

func watchNotifyOnce(ctx context.Context, dsn, listen, channel string, onError func(error), onNotice func(string)) error {
	report := func(err error) {
		if onError != nil {
			onError(err)
		}
	}
	conn, err := pgx.Connect(ctx, dsn)
	if err != nil {
		if ctx.Err() != nil {
			return nil
		}
		report(fmt.Errorf("execution notify listener connect: %w", err))
		return err
	}
	defer conn.Close(context.Background())
	if _, err := conn.Exec(ctx, listen); err != nil {
		if ctx.Err() != nil {
			return nil
		}
		report(fmt.Errorf("execution notify listener listen: %w", err))
		return err
	}
	for {
		notification, err := conn.WaitForNotification(ctx)
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			report(fmt.Errorf("execution notify listener wait: %w", err))
			return err
		}
		if notification.Channel != channel {
			continue
		}
		onNotice(notification.Payload)
	}
}
