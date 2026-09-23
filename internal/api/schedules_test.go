package api_test

import (
	"net/http"
	"testing"
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
