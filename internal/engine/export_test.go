package engine

import "time"

// NewEvaluationServiceForTest builds a service holding only what the read-only
// evaluator reads: the run budget, the instance's clock zone and the
// allowlisted environment.
//
// The evaluator touches no store, no runner and no catalogue — its input is the
// record and the document the caller already holds — so a full service would
// only be a test that stubs four collaborators the code never calls.
func NewEvaluationServiceForTest(defaultTimeout time.Duration, timezone string, environment map[string]string) *Service {
	exposed := make(map[string]string, len(environment))
	for key, value := range environment {
		exposed[key] = value
	}
	return &Service{defaultTimeout: defaultTimeout, defaultTimezone: timezone, environment: exposed}
}

// SetClockForTest redirects the service's clock to now. The wait deadline is
// validated, swept and armed against it, so a test moves time instead of
// racing the wall clock and its deadlines stop flaking under load. Production
// leaves the field nil and reads time.Now through Service.clock.
func (service *Service) SetClockForTest(now func() time.Time) {
	service.now = now
}

// SetTimerForTest replaces the scheduler wait suspensions arm their exact
// deadline wake-up with. The test then fires that wake-up itself, so the
// property "the deadline requeues the execution" is proven without a
// wall-clock latency margin. Production leaves the field nil for
// time.AfterFunc.
func (service *Service) SetTimerForTest(afterFunc func(time.Duration, func()) *time.Timer) {
	service.waitTimers.afterFunc = afterFunc
}
