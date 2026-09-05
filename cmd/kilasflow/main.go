// Command kilasflow runs the embeddable workflow automation engine: REST API,
// webhook server, and the editor SPA, from a single binary.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"strings"
	"syscall"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/nodes"
)

// version is overridden at build time with -ldflags "-X main.version=...".
var version = "0.1.0-dev"

func main() {
	if err := run(); err != nil {
		fmt.Fprintf(os.Stderr, "kilasflow: %v\n", err)
		os.Exit(1)
	}
}

// run wires the application from constructors, so every dependency is explicit
// and there is no global service locator to unpick later.
func run() error {
	configPath := flag.String("config", "config.yaml", "path to the configuration file")
	showVersion := flag.Bool("version", false, "print the version and exit")
	flag.Parse()

	if *showVersion {
		fmt.Println(version)
		return nil
	}

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	log := newLogger(cfg.Log)
	log.Info("starting kilasflow", "version", version)

	// Cancelled on SIGINT/SIGTERM, which unwinds the server and the database.
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	db, err := database.Open(ctx, cfg.Database, log)
	if err != nil {
		return err
	}
	defer func() {
		if err := db.Close(); err != nil {
			log.Error("closing database", "error", err)
		}
	}()
	if err := migrate(db); err != nil {
		return err
	}
	nodeRegistry := node.NewRegistry()
	if err := nodes.RegisterAll(nodeRegistry); err != nil {
		return fmt.Errorf("register built-in nodes: %w", err)
	}
	executorRegistry := engine.NewRegistry()
	agentMemory, err := ai.NewBufferMemory(ai.Retention{}, nil)
	if err != nil {
		return fmt.Errorf("configure agent memory: %w", err)
	}
	if err := nodes.RegisterExecutors(executorRegistry, outboundPolicy(cfg.Outbound), databaseGuard(cfg.Database),
		ai.NewLoopRuntime(), agentMemory, runcode.NewToolchainCompiler()); err != nil {
		return fmt.Errorf("register built-in executors: %w", err)
	}

	// Credentials are optional at boot: an install with no key still runs
	// workflows, and only credential operations report that it is unconfigured.
	// Failing startup instead would make the key mandatory for every user.
	var credentialStore *repository.GORMCredentialStore
	key, keyErr := credentials.KeyFromEnvironment(cfg.Security.EncryptionKeyEnv)
	switch {
	case errors.Is(keyErr, credentials.ErrNoKey):
		log.Warn("credential encryption key is not set; credential storage is disabled",
			"variable", cfg.Security.EncryptionKeyEnv)
		credentialStore = repository.NewCredentialStore(db.DB, nil)
	case keyErr != nil:
		return fmt.Errorf("credential encryption key: %w", keyErr)
	default:
		cipher, err := credentials.NewCipher(key)
		if err != nil {
			return err
		}
		credentialStore = repository.NewCredentialStore(db.DB, cipher)
	}

	// Embedding is opt-in: with no signing key the endpoints report that
	// clearly and the middleware refuses every token, rather than the editor
	// silently being frameable.
	var embedIssuer *embed.Issuer
	if embedKey, err := credentials.KeyFromEnvironment(cfg.Embed.SigningKeyEnv); err == nil {
		issuer, issuerErr := embed.NewIssuer(embedKey, cfg.Embed.AllowedOrigins, nil)
		if issuerErr != nil {
			return fmt.Errorf("configure embed sessions: %w", issuerErr)
		}
		embedIssuer = issuer
	} else if !errors.Is(err, credentials.ErrNoKey) {
		return fmt.Errorf("embed signing key: %w", err)
	} else {
		log.Warn("embed signing key is not set; embedded editor sessions are disabled",
			"variable", cfg.Embed.SigningKeyEnv)
	}

	executions := repository.NewExecutionStore(db.DB)
	// Injecting the extractor keeps node-type knowledge out of persistence
	// while still letting webhook bindings be synced inside the activation
	// transaction.
	workflows := repository.NewWorkflowStore(db.DB).
		WithWebhooks(webhook.Extract(nodes.WebhookNodeType, nodes.WebhookPath))
	schedules := repository.NewScheduleStore(db.DB)
	eventBroker := events.NewBroker(events.BrokerOptions{})
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions:     executions,
		Events:         eventBroker,
		Catalog:        nodeRegistry,
		Runner:         engine.NewRunner(executorRegistry),
		Credentials:    credentialStore,
		Environment:    workflowEnvironment(),
		WorkerID:       fmt.Sprintf("kilasflow-%d", os.Getpid()),
		DefaultTimeout: cfg.Execution.DefaultTimeout,
	})
	if err != nil {
		return fmt.Errorf("configure execution runtime: %w", err)
	}
	// How each trigger type shapes an inbound delivery. KilasFlow's own webhook
	// keeps the envelope it has always produced; a pack-supplied trigger names
	// the shape it wants rather than shipping Go code to build one.
	webhookTriggers := webhook.NewRegistry()
	if err := webhookTriggers.Register(nodes.WebhookNodeType, webhook.TriggerKind{Shape: webhook.ShapeEnvelope}); err != nil {
		return fmt.Errorf("register webhook triggers: %w", err)
	}

	if err := runtime.Start(ctx, cfg.Execution.MaxConcurrent); err != nil {
		return fmt.Errorf("start execution runtime: %w", err)
	}

	cronService, err := scheduler.New(scheduler.Options{
		Schedules: schedules,
		Queue: func(ctx context.Context, tenantID, workflowID, versionID, triggerNodeID string, payload json.RawMessage) error {
			_, err := runtime.QueueScheduled(ctx, tenantID, workflowID, versionID, triggerNodeID, payload)
			return err
		},
		Logger: log,
	})
	if err != nil {
		return fmt.Errorf("configure scheduler: %w", err)
	}
	cronService.Start(ctx)

	server := api.NewServer(api.Deps{
		Config:       cfg,
		Logger:       log,
		DB:           handlers.Pinger(db),
		NodeRegistry: nodeRegistry,
		Workflows:    workflows,
		Executions:   executions,
		Schedules:    schedules,
		Webhook: webhook.NewHandler(workflows, runtime, credentialStore, eventBroker, webhook.Limits{
			MaxBodyBytes:    cfg.Webhook.MaxBodyBytes,
			ResponseTimeout: cfg.Webhook.ResponseTimeout,
			DeliveryWindow:  repository.DefaultDeliveryWindow,
		}).WithTriggers(webhookTriggers),
		Credentials:         credentialStore,
		Events:              eventBroker,
		EmbedIssuer:         embedIssuer,
		ExecutionController: runtime,
		Version:             version,
	})

	return server.Run(ctx)
}

func migrate(db *database.DB) error {
	return database.Migrate(db, repository.Models()...)
}

// databaseGuard names the files a SQLite workflow credential must never open.
//
// A workflow that could open KilasFlow's own database would be able to read
// every credential, workflow, and execution in the installation, so the path
// is passed explicitly rather than inferred inside the node.
func databaseGuard(cfg config.Database) sqlnode.Guard {
	if cfg.Driver != "sqlite" || cfg.DSN == "" {
		return sqlnode.Guard{}
	}
	return sqlnode.Guard{InternalPaths: []string{cfg.DSN}}
}

func outboundPolicy(cfg config.OutboundHTTP) safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = cfg.AllowPrivateNetworks
	policy.AllowedHosts = append([]string(nil), cfg.AllowedHosts...)
	if cfg.MaxRedirects > 0 {
		policy.MaxRedirects = cfg.MaxRedirects
	}
	if cfg.MaxResponseBytes > 0 {
		policy.MaxResponseBytes = cfg.MaxResponseBytes
	}
	if cfg.Timeout > 0 {
		policy.Timeout = cfg.Timeout
	}
	return policy
}

// workflowEnvironment is the allowlist behind the `$env` expression root.
//
// Only variables under KILASFLOW_WORKFLOW_ENV_ are exposed, so a workflow can
// never read the database DSN or the credential master key out of the process
// environment.
func workflowEnvironment() map[string]string {
	const prefix = "KILASFLOW_WORKFLOW_ENV_"
	exposed := map[string]string{}
	for _, entry := range os.Environ() {
		name, value, found := strings.Cut(entry, "=")
		if !found {
			continue
		}
		if key, ok := strings.CutPrefix(name, prefix); ok && key != "" {
			exposed[key] = value
		}
	}
	return exposed
}

// newLogger builds the structured logger described by the configuration.
func newLogger(cfg config.Log) *slog.Logger {
	opts := &slog.HandlerOptions{Level: parseLevel(cfg.Level)}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

func parseLevel(level string) slog.Level {
	var parsed slog.Level
	if err := parsed.UnmarshalText([]byte(level)); err != nil {
		return slog.LevelInfo
	}

	return parsed
}
