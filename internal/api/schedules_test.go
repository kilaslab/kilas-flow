package api_test

import (
	"encoding/json"
	"net/http"
	"testing"
	"time"
)

// TestCreatingAScheduleWithAnImpossibleCronIsRefused is BUG-g7ffj1's API-level
// regression test. "0 0 31 2 *" parses as an ordinary five-field expression —
// February 31st reads like a date — but never occurs on any calendar, and
// robfig/cron's answer for that is the zero time, not an error. Before the
// fix that zero time was accepted and stored as nextRunAt, and the schedule
// then fired on every tick. Both an active and an inactive schedule must be
// refused: an inactive one storing the zero time would fire the instant it
// was switched on.
func TestCreatingAScheduleWithAnImpossibleCronIsRefused(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Impossible schedule")

	for _, active := range []bool{true, false} {
		recorder := server.call(t, http.MethodPost, "/api/v1/schedules", keys["acme"], map[string]any{
			"workflowId": workflowID, "nodeId": "manual", "cron": "0 0 31 2 *", "active": active,
		})
		if recorder.Code != http.StatusUnprocessableEntity {
			t.Errorf("active=%v: status = %d, want 422 (body: %s)", active, recorder.Code, recorder.Body)
		}
	}
}

// The cron field's documentation says an expression is read in UTC unless it
// starts with a TZ= prefix, and that is the only zone the REST surface takes:
// the resource carries no timezone field, because a schedule a workflow's
// Schedule Trigger registers takes its zone from the workflow's settings
// instead. A reference that said "evaluated in UTC" without the prefix sent a
// reader in Jakarta to write 02:00 for nine in the morning.
func TestAScheduleCronReadsInUTCUnlessItCarriesATimezonePrefix(t *testing.T) {
	server, keys := newAuthenticatedAPI(t, "acme")
	workflowID := server.createWorkflowAs(t, keys["acme"], "Zoned schedule")

	nextRun := func(cron string) time.Time {
		t.Helper()
		recorder := server.call(t, http.MethodPost, "/api/v1/schedules", keys["acme"], map[string]any{
			"workflowId": workflowID, "nodeId": "manual-" + cron, "cron": cron, "active": true,
		})
		if recorder.Code != http.StatusCreated {
			t.Fatalf("create %q: status = %d, want 201 (body: %s)", cron, recorder.Code, recorder.Body)
		}
		var created struct {
			NextRunAt time.Time `json:"nextRunAt"`
		}
		if err := json.Unmarshal(recorder.Body.Bytes(), &created); err != nil {
			t.Fatalf("decode %q: %v", cron, err)
		}
		return created.NextRunAt.UTC()
	}

	if got := nextRun("0 9 * * *"); got.Hour() != 9 || got.Minute() != 0 {
		t.Errorf("a bare cron fires at %s, want 09:00 UTC", got.Format(time.RFC3339))
	}
	// Jakarta is UTC+7 all year, so nine there is two in the morning in UTC.
	if got := nextRun("TZ=Asia/Jakarta 0 9 * * *"); got.Hour() != 2 || got.Minute() != 0 {
		t.Errorf("a TZ=Asia/Jakarta cron fires at %s, want 02:00 UTC", got.Format(time.RFC3339))
	}
}
