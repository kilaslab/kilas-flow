package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// The delivery log answers with the caller's tenant's events only, newest
// first, narrowed by status, and never with another tenant's.
func TestNotificationDeliveriesListOnlyTheCallersTenant(t *testing.T) {
	server, keys := newAuthenticatedAPIWith(t, func(deps *api.Deps) {
		db := deps.DB.(*database.DB)
		deps.NotificationLog = repository.NewOutboxStore(db.DB)
		now := time.Now().UTC()
		for _, row := range []struct {
			id, tenant, status string
			at                 time.Time
		}{
			{"evt_acme_old", "acme", "delivered", now.Add(-time.Hour)},
			{"evt_acme_new", "acme", "pending", now},
			{"evt_globex", "globex", "pending", now},
		} {
			if err := db.Exec(
				"INSERT INTO outbound_events (id, tenant_id, event_type, subject_id, endpoint, payload, status, attempts, last_status_code, created_at, updated_at) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)",
				row.id, row.tenant, "execution.failed", "exec_1", "https://host.example/hooks/T0123/secret-path?token=abc", []byte(`{"id":"`+row.id+`"}`), row.status, 1, 503, row.at, row.at,
			).Error; err != nil {
				t.Fatalf("seed %s: %v", row.id, err)
			}
		}
	}, "acme", "globex")

	recorder := server.call(t, http.MethodGet, "/api/v1/notifications/deliveries", keys["acme"], nil)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d (body: %s)", recorder.Code, recorder.Body)
	}
	var listed []struct {
		ID             string          `json:"id"`
		Type           string          `json:"type"`
		Status         string          `json:"status"`
		Attempts       int             `json:"attempts"`
		LastStatusCode int             `json:"lastStatusCode"`
		Endpoint       string          `json:"endpoint"`
		Payload        json.RawMessage `json:"payload"`
	}
	if err := json.Unmarshal(recorder.Body.Bytes(), &listed); err != nil {
		t.Fatalf("decode: %v (%s)", err, recorder.Body)
	}
	if len(listed) != 2 || listed[0].ID != "evt_acme_new" || listed[1].ID != "evt_acme_old" {
		t.Fatalf("acme's log = %+v, want its two events newest first", listed)
	}
	// The endpoint is the operator's setting; its path and query can be a
	// capability, so a tenant key reads the scheme and host only.
	if listed[0].Endpoint != "https://host.example" {
		t.Fatalf("endpoint = %q, want the scheme and host only", listed[0].Endpoint)
	}
	if listed[0].Type != "execution.failed" || listed[0].Attempts != 1 || listed[0].LastStatusCode != 503 || string(listed[0].Payload) != `{"id":"evt_acme_new"}` {
		t.Fatalf("acme's newest event = %+v", listed[0])
	}

	filtered := server.call(t, http.MethodGet, "/api/v1/notifications/deliveries?status=delivered", keys["acme"], nil)
	listed = nil
	if err := json.Unmarshal(filtered.Body.Bytes(), &listed); err != nil || len(listed) != 1 || listed[0].ID != "evt_acme_old" {
		t.Fatalf("delivered filter = %+v (%v, %s)", listed, err, filtered.Body)
	}
	if invalid := server.call(t, http.MethodGet, "/api/v1/notifications/deliveries?status=lost", keys["acme"], nil); invalid.Code != http.StatusUnprocessableEntity {
		t.Fatalf("an unknown status = %d, want 422", invalid.Code)
	}
	if anonymous := server.call(t, http.MethodGet, "/api/v1/notifications/deliveries", "", nil); anonymous.Code != http.StatusUnauthorized {
		t.Fatalf("no credential = %d, want 401", anonymous.Code)
	}
}
