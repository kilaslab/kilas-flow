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
	"time"

	"github.com/kilaslabs/kilas-flow/internal/ai"
	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/binary"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/runcode"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/scheduler"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/nodes"
	"github.com/kilaslabs/kilas-flow/packs/telegram"
	"github.com/kilaslabs/kilas-flow/packs/waha"
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
		ai.NewLoopRuntime(), agentMemory, runcode.NewToolchainCompiler(),
		nodes.WithDatabaseCeiling(databaseCeiling(cfg.SQL))); err != nil {
		return fmt.Errorf("register built-in executors: %w", err)
	}
	// Declarative node packs run on one interpreter rather than shipping Go.
	// The registry is empty until a pack registers into it; the executor is
	// installed regardless so a pack does not also have to install a runtime.
	routes := routing.NewRegistry()
	if err := nodes.RegisterRoutingExecutor(executorRegistry, outboundPolicy(cfg.Outbound), routes, nodeRegistry); err != nil {
		return fmt.Errorf("register the declarative routing executor: %w", err)
	}
	// Edit-time option loading. It reaches a customer's service through the
	// same egress policy an HTTP node uses, so a loader aimed at a disallowed
	// host fails the same way — and a pack registers its own internal loaders
	// into it, which is why it is built here rather than at the API.
	optionLoader := loadoptions.NewResolver(safehttp.DefaultPolicy(), 30*time.Second)
	// Generated node packs. The definitions, their routing and their option
	// loaders are registered together: a pack whose runtime is missing fails
	// here rather than on its first execution.
	// Webhook lifecycle hooks: how a trigger registers itself with the service
	// that will deliver to it.
	webhookLifecycles := webhook.NewLifecycleRegistry()
	// How each trigger type shapes an inbound delivery. KilasFlow's own webhook
	// keeps the envelope it has always produced; a pack-supplied trigger names
	// the shape it wants rather than shipping Go code to build one.
	webhookTriggers := webhook.NewRegistry()
	if err := nodes.RegisterTriggerKinds(webhookTriggers); err != nil {
		return fmt.Errorf("register webhook triggers: %w", err)
	}

	packTriggers := nodepack.NewTriggerRegistry()
	if err := executorRegistry.Register(nodepack.TriggerExecutorID, nodepack.NewTriggerExecutor(packTriggers, outboundPolicy(cfg.Outbound))); err != nil {
		return fmt.Errorf("register the pack trigger executor: %w", err)
	}
	if err := telegram.Register(nodeRegistry, routes, executorRegistry, optionLoader); err != nil {
		return fmt.Errorf("register the Telegram node pack: %w", err)
	}
	if err := waha.Register(waha.Deps{
		Definitions: nodeRegistry, Routes: routes, Triggers: packTriggers,
		Deliveries: webhookTriggers, Lifecycles: webhookLifecycles,
		Executors: executorRegistry, Options: optionLoader,
	}); err != nil {
		return fmt.Errorf("register the WAHA node pack: %w", err)
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
		WithWebhooks(webhook.Extract(nodeRegistry, nodes.WebhookPath)).
		WithSchedules(scheduler.Extract(nodes.ScheduleType), scheduler.Next)
	schedules := repository.NewScheduleStore(db.DB)
	eventBroker := events.NewBroker(events.BrokerOptions{})
	// Binary payloads live on a filesystem root, never in the database. An
	// unset root leaves the store nil, and a node that needs one then fails
	// with a message saying so rather than silently dropping an attachment.
	var binaries binary.Store
	if strings.TrimSpace(cfg.Binary.Root) != "" {
		fileStore, err := binary.NewFileStore(cfg.Binary.Root, cfg.Binary.MaxBytes)
		if err != nil {
			return fmt.Errorf("configure binary storage: %w", err)
		}
		binaries = fileStore
	}
	runtime, err := engine.NewService(engine.ServiceDeps{
		Executions:     executions,
		Binaries:       binaries,
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
	webhookHandler := webhook.NewHandler(workflows, runtime, credentialStore, eventBroker, webhook.Limits{
		MaxBodyBytes:    cfg.Webhook.MaxBodyBytes,
		ResponseTimeout: cfg.Webhook.ResponseTimeout,
		DeliveryWindow:  repository.DefaultDeliveryWindow,
	}).WithTriggers(webhookTriggers).WithLogger(log)

	// Telegram's development delivery mode. The supervisor's context is the
	// server's, not an activation request's: a poller cancelled when its HTTP
	// request finished would stop the moment it started.
	telegramPollers := nodes.NewTelegramPollers(ctx, outboundPolicy(cfg.Outbound), webhookHandler.QueueRunner())
	if err := nodes.RegisterLifecycles(webhookLifecycles, telegramPollers); err != nil {
		return fmt.Errorf("register webhook lifecycles: %w", err)
	}
	// Every trigger's hook binding is verified now that the packs and the
	// built-ins have registered theirs, so a node declaring a hook nobody
	// registered fails here rather than silently never registering at its
	// first activation.
	if err := webhook.VerifyLifecycleBindings(nodeRegistry.LifecycleIDs(), webhookLifecycles); err != nil {
		return fmt.Errorf("verify webhook lifecycles: %w", err)
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
		Webhook:      webhookHandler,
		Credentials:  credentialStore,
		OptionLoader: optionLoader,
		CredentialResolverFor: func(tenant repository.TenantScope) loadoptions.CredentialResolver {
			return credentialLookup{store: credentialStore, tenant: tenant}
		},
		TriggerCoordinator: webhook.NewCoordinator(
			webhookLifecycles, workflows, safehttp.DefaultPolicy(),
			func(tenantID string) engine.CredentialResolver {
				return engine.NewTenantCredentials(credentialStore, repository.TenantScope{ID: tenantID})
			},
			cfg.Server.PublicURL, log,
		),
		Events:              eventBroker,
		EmbedIssuer:         embedIssuer,
		ExecutionController: runtime,
		HTTPPolicy:          outboundPolicy(cfg.Outbound),
		DatabaseGuard:       databaseGuard(cfg.Database),
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

// databaseCeiling is the deployment's bound on what a SQL node's parameters
// may ask for, which a workflow document cannot raise.
func databaseCeiling(cfg config.SQLNodes) sqlnode.Ceiling {
	return sqlnode.Ceiling{MaxRows: cfg.MaxRows, MaxTimeout: cfg.MaxStatementTimeout}
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

// credentialLookup resolves a credential under one tenant, for edit-time option
// loading. It is scoped at construction so a request naming another tenant's
// credential resolves to nothing rather than to a secret.
type credentialLookup struct {
	store  *repository.GORMCredentialStore
	tenant repository.TenantScope
}

func (lookup credentialLookup) Resolve(ctx context.Context, credentialID string) (credentials.Record, map[string]string, error) {
	return lookup.store.Resolve(ctx, lookup.tenant, credentialID)
}
