package main

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/metrics"
)

// metricsConfig builds one metrics section, enabled unless said otherwise,
// bound to a free loopback port the test owns.
func metricsConfig(t *testing.T, enabled bool, mutate func(*config.Metrics)) config.Config {
	t.Helper()
	cfg := config.Default()
	cfg.Metrics.Enabled = enabled
	if enabled {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatalf("Listen() for a free port: %v", err)
		}
		// Freed the moment the port is read rather than at cleanup: the
		// metrics listener below has to bind it, and a helper still holding
		// it would accept the test's own requests and answer nothing.
		port := listener.Addr().(*net.TCPAddr).Port
		if err := listener.Close(); err != nil {
			t.Fatalf("close the port probe: %v", err)
		}
		cfg.Metrics.Port = port
	}
	if mutate != nil {
		mutate(&cfg.Metrics)
	}
	return cfg
}

// The listener is wired at boot: enabled, it serves the text exposition
// format on its own address, and disabled it binds nothing at all. A dropped
// wiring line used to be the way a switch in the configuration file governed
// nothing, which is why the pair is driven through the one function run()
// calls.
func TestMetricsListenerIsWiredAtBoot(t *testing.T) {
	t.Run("serves the exposition when enabled", func(t *testing.T) {
		cfg := metricsConfig(t, true, nil)
		registry := metrics.NewRegistry(cfg.Metrics.TenantLabel, cfg.Metrics.TopTenants)
		registry.ExecutionStarted("acme", "manual")

		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		logs := &strings.Builder{}
		server := startMetricsListener(ctx, cfg, registry, slog.New(slog.NewTextHandler(logs, nil)))
		if server == nil {
			t.Fatal("startMetricsListener(enabled) = nil, want the server")
		}
		t.Cleanup(func() { _ = server.Close() })

		body := scrape(t, cfg.Metrics)
		if !strings.Contains(body, `kilasflow_executions_started_total{trigger="manual"} 1`) {
			t.Errorf("the endpoint served %q, want the observed series", body)
		}
		if !strings.Contains(body, "# TYPE kilasflow_executions_started_total counter") {
			t.Errorf("the endpoint served %q, want the exposition's TYPE lines", body)
		}
		if !strings.Contains(logs.String(), "metrics endpoint listening") {
			t.Errorf("boot log = %q, want the listener stated", logs.String())
		}

		// The context ending shuts the listener down: the endpoint is not a
		// process-lifetime leak past a graceful stop.
		cancel()
	shutdown:
		for range 50 {
			if err := scrapeError(cfg.Metrics); err != nil {
				break shutdown
			}
			time.Sleep(20 * time.Millisecond)
		}
		if err := scrapeError(cfg.Metrics); err == nil {
			t.Error("the metrics endpoint still answers after the context ended, want the listener shut down")
		}
	})

	t.Run("binds nothing when disabled", func(t *testing.T) {
		cfg := metricsConfig(t, false, nil)
		logs := &strings.Builder{}
		if server := startMetricsListener(context.Background(), cfg, nil, slog.New(slog.NewTextHandler(logs, nil))); server != nil {
			t.Fatal("startMetricsListener(disabled) returned a server, want nil")
		}
		if logs.String() != "" {
			t.Errorf("boot log = %q, want nothing: an off endpoint states nothing", logs.String())
		}
	})

	t.Run("a nil registry still answers, with the empty exposition", func(t *testing.T) {
		cfg := metricsConfig(t, true, nil)
		logs := &strings.Builder{}
		server := startMetricsListener(context.Background(), cfg, nil, slog.New(slog.NewTextHandler(logs, nil)))
		if server == nil {
			t.Fatal("startMetricsListener(nil registry) = nil, want the server")
		}
		t.Cleanup(func() { _ = server.Close() })

		if body := scrape(t, cfg.Metrics); body != "" {
			t.Errorf("the endpoint served %q, want the empty exposition", body)
		}
	})

	t.Run("the registry is built only when the endpoint is enabled", func(t *testing.T) {
		if registry := newMetricsRegistry(metricsConfig(t, false, nil)); registry != nil {
			t.Error("newMetricsRegistry(disabled) = a registry, want nil")
		}
		cfg := metricsConfig(t, true, nil)
		if registry := newMetricsRegistry(cfg); registry == nil {
			t.Error("newMetricsRegistry(enabled) = nil, want the registry")
		}
		// The observer helper keeps a nil registry out of the interface: a
		// typed nil inside a non-nil interface is not nil, and the engine
		// would dereference it on the first report.
		if observer := metricsObserver(nil); observer != nil {
			t.Error("metricsObserver(nil) = non-nil, want the nil interface")
		}
	})
}

// scrape reads the endpoint once, asserting the exposition's content type.
func scrape(t *testing.T, section config.Metrics) string {
	t.Helper()
	response, err := http.Get(fmt.Sprintf("http://%s:%d", section.Host, section.Port))
	if err != nil {
		t.Fatalf("GET the metrics endpoint: %v", err)
	}
	defer response.Body.Close()
	if contentType := response.Header.Get("Content-Type"); !strings.HasPrefix(contentType, "text/plain; version=0.0.4") {
		t.Errorf("Content-Type = %q, want the text exposition format", contentType)
	}
	body, err := io.ReadAll(response.Body)
	if err != nil {
		t.Fatalf("read the exposition: %v", err)
	}
	return string(body)
}

// scrapeError reports the error of one GET, for the test that waits for the
// listener to stop answering.
func scrapeError(section config.Metrics) error {
	response, err := http.Get(fmt.Sprintf("http://%s:%d", section.Host, section.Port))
	if err != nil {
		return err
	}
	defer response.Body.Close()
	_, _ = io.Copy(io.Discard, response.Body)
	return nil
}
