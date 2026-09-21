package idempotency

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"time"
)

// Sweeper deletes expired idempotency keys in the background.
//
// Expiry is enforced by the claim as well, so a late sweep costs correctness
// nothing; it costs disk, and on an installation whose keys are all retried
// once and never again it is the only thing that ever deletes a row.
type Sweeper struct {
	cancel context.CancelFunc
	done   chan struct{}
}

// StartSweeper runs one sweep every interval until ctx ends or Stop is called.
//
// A nil service returns a Sweeper that does nothing and can still be stopped,
// so a composition root that has no idempotency store — there is one key, and
// it is not worth a conditional at every call site — does not have to special
// case the shutdown path.
func StartSweeper(ctx context.Context, service *Service, interval time.Duration, log *slog.Logger) *Sweeper {
	if service == nil {
		return &Sweeper{}
	}
	if interval <= 0 {
		interval = DefaultSweepInterval
	}
	if log == nil {
		log = slog.New(slog.NewTextHandler(io.Discard, nil))
	}

	sweepCtx, cancel := context.WithCancel(ctx)
	sweeper := &Sweeper{cancel: cancel, done: make(chan struct{})}
	go func() {
		defer close(sweeper.done)
		log.Info("idempotency sweeper started", "interval", interval)
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		for {
			select {
			case <-sweepCtx.Done():
			case <-ticker.C:
				// A tick that queued while the last sweep was running is not
				// an instruction to sweep again once shutdown has been asked
				// for: that sweep would open a query against a database the
				// composition root is about to close.
				if sweepCtx.Err() == nil {
					sweep(sweepCtx, service, log)
					continue
				}
			}
			log.Info("idempotency sweeper stopped")
			return
		}
	}()
	return sweeper
}

// sweep runs one pass and reports what it found.
func sweep(ctx context.Context, service *Service, log *slog.Logger) {
	swept, err := service.Sweep(ctx)
	if err != nil {
		// A cancelled sweep is the shutdown this goroutine is already
		// handling, not a failure worth reporting.
		if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
			return
		}
		log.Error("sweeping expired idempotency keys", "error", err)
		return
	}
	if swept > 0 {
		log.Info("swept expired idempotency keys", "keys", swept)
	}
}

// Stop ends the sweeps and waits for the one in flight.
//
// It waits because the alternative is a delete still running against a
// database the composition root is about to close. Calling it twice is safe,
// and so is calling it on a nil Sweeper: it is deferred on paths that may not
// have started one.
func (s *Sweeper) Stop() {
	if s == nil || s.cancel == nil {
		return
	}
	s.cancel()
	<-s.done
}
