package engine

import "time"

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
