package metrics

import (
	"context"
	"fmt"
	"net/http"
	"time"
)

// scrapeTimeout bounds one gauge collector, so a scrape cannot hang on a
// database that will not answer. The exposition of what was already observed
// is instant; only the gauges ask the outside world anything.
const scrapeTimeout = 5 * time.Second

// Handler serves the registry in the Prometheus text exposition format.
//
// One endpoint, one method, no parameters and no credentials of its own: the
// listener this handler is mounted on is the security boundary, which is why
// the configuration defaults it to loopback and leaves enabling it to the
// operator.
func Handler(registry *Registry) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if registry == nil {
			// A listener without a registry still answers, with the empty
			// exposition: a scraper sees a dead endpoint rather than a
			// connection refused, and an operator reading the config sees
			// the missing wiring in what comes back — nothing.
			w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
			return
		}
		ctx, cancel := context.WithTimeout(context.Background(), scrapeTimeout)
		defer cancel()
		body := registry.Render(ctx)
		w.Header().Set("Content-Type", "text/plain; version=0.0.4; charset=utf-8")
		fmt.Fprint(w, body)
	})
}
