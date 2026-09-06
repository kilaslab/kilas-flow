// Command kilasflow runs the embeddable workflow automation engine: REST API,
// webhook server, and the editor SPA, from a single binary.
package main

import (
	"context"
	"crypto/rand"
	"encoding/hex"
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
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/binary"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/datastore"
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
	workerIDOverride := flag.String("worker-id", "", "worker identity recorded in lease_owner (default: host-qualified and unique per process)")
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
	if err := database.Migrate(db, log); err != nil {
		return err
	}
	// The row store behind data tables and the datastore node. Built here
	// because this is the only place holding the database handle and the
	// table prefix together; the API, the node executors and the option
	// loaders all receive the same engine. A bad prefix refuses the boot
	// rather than serving half-named tables.
	datastoreEngine, err := datastore.NewEngine(db, cfg.Database.TablePrefix)
	if err != nil {
		return err
	}
	// The operator's datastore bounds ride into the engine here, beside the
	// prefix: Validate already refused anything non-positive, so this cannot
	// fail on a configuration Load accepted.
	if err := datastoreEngine.SetLimits(datastore.Limits{
		MaxDatastoresPerTenant: cfg.Datastore.MaxDatastoresPerTenant,
		MaxColumnsPerDatastore: cfg.Datastore.MaxColumnsPerDatastore,
		MaxRowsPerDatastore:    cfg.Datastore.MaxRowsPerDatastore,
		MaxValueBytes:          cfg.Datastore.MaxValueBytes,
	}); err != nil {
		return err
	}
	nodeRegistry := node.NewRegistry()
	if err := nodes.RegisterAll(nodeRegistry); err != nil {
		return fmt.Errorf("register built-in nodes: %w", err)
	}

	// The vector store lives in the installation's own PostgreSQL: the same
	// database the guard below refuses to workflow credentials. Absence only
	// costs vector runs — every other node validates and executes unchanged —
	// so a missing extension warns rather than refusing to boot.
	vectorReason := nodes.VectorUnavailableReason(cfg.Database.Driver)
	var vectorStore nodes.VectorStore = nodes.NewDisabledVectorStore(vectorReason)
	if cfg.Database.Driver == "postgres" {
		if sqldb, err := db.DB.DB(); err != nil {
			log.Warn("vector store unavailable", "reason", err)
		} else if err := nodes.ProbeVectorExtension(ctx, sqldb); err != nil {
			log.Warn("vector store unavailable", "reason", err)
			vectorReason = err.Error()
			vectorStore = nodes.NewDisabledVectorStore(vectorReason)
		} else {
			vectorReason = ""
			vectorStore = nodes.NewPostgresVectorStore(sqldb, cfg.Database.TablePrefix)
		}
	}
	if err := nodes.RegisterVectorNodes(nodeRegistry, vectorReason); err != nil {
		return fmt.Errorf("register vector nodes: %w", err)
	}
	executorRegistry := engine.NewRegistry()
	// The Go toolchain is not in the distroless image, so this is absent on a
	// default install. That is reported through the node catalogue rather than
	// discovered when a workflow runs — see internal/runcode/doc.go.
	// Built once, before anything that needs it, so a DSN the guard cannot
	// resolve stops the boot rather than reaching three call sites that would
	// each have to decide what to do about it.
	sqlGuard, err := databaseGuard(cfg.Database)
	if err != nil {
		return err
	}
	// One process egress policy for HTTP and SQL alike: the same outbound
	// section that governs workflow HTTP requests governs database targets,
	// so allow_private_networks: true permits a private database and false
	// refuses it.
	sqlGuard.Policy = outboundPolicy(cfg.Outbound)
	codeCompiler := runcode.NewToolchainCompiler()
	// One tenant's chat volume is another tenant's memory pressure, so each
	// tenant keeps a bounded number of conversations and the least-recently-
	// touched session is evicted first. A deployment-level knob for this
	// belongs in internal/config beside the other deployment decisions;
	// until one exists the default below applies.
	agentMemory, err := ai.NewBufferMemory(ai.Retention{}, nil,
		ai.WithPerTenantSessionLimit(defaultAgentMemorySessionsPerTenant))
	if err != nil {
		return fmt.Errorf("configure agent memory: %w", err)
	}
	if err := nodes.RegisterExecutors(executorRegistry, outboundPolicy(cfg.Outbound), sqlGuard,
		ai.NewLoopRuntime(), agentMemory, codeCompiler,
		nodes.WithDatabaseCeiling(databaseCeiling(cfg.SQL)), nodes.WithVectorStore(vectorStore),
		nodes.WithDatastoreEngine(datastoreEngine)); err != nil {
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
	// same egress policy an HTTP node uses — the operator's, not the library
	// default, which is what this line said and did not do: a loader ignored
	// allowed_hosts entirely, so a deployment that restricted egress found the
	// restriction applied at run time and not while editing. It also ignored
	// allow_private_networks, so the reverse held too and a deployment that
	// permitted them found its loaders blocked. A loader aimed at a disallowed
	// host now fails the same way — and a pack registers its own internal loaders
	// into it, which is why it is built here rather than at the API.
	optionLoader := loadoptions.NewResolver(outboundPolicy(cfg.Outbound), 30*time.Second)
	// Database introspection, under the same guard the executors receive: a
	// SQLite credential naming KilasFlow's own database is refused at edit time
	// exactly as it is at run time.
	if err := loadoptions.RegisterSQL(optionLoader, sqlGuard); err != nil {
		return fmt.Errorf("register the database option loaders: %w", err)
	}
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
	// Directory-sourced packs: the no-rebuild install path. Loaded here, at
	// composition before the registry is shared, through the same Decode →
	// Load → Register path as the embedded packs above. An empty or absent
	// directory is silent; any pack failure refuses the boot, naming the pack
	// and the reason rather than serving a half-registered catalogue.
	if err := nodepack.LoadDir(nodepack.DirDeps{
		Definitions: nodeRegistry, Routes: routes, Triggers: packTriggers,
		Deliveries: webhookTriggers, Lifecycles: webhookLifecycles,
		Executors: executorRegistry, Options: optionLoader,
	}, cfg.Packs.Dir); err != nil {
		return fmt.Errorf("load directory node packs: %w", err)
	}

	// Credentials are optional at boot: an install with no key still runs
	// workflows, and only credential operations report that it is unconfigured.
	// Failing startup instead would make the key mandatory for every user.
	// That holds only when no manager is configured: a manager that is named
	// but unreachable refuses startup, because a transient network error
	// silently disabling every credential is worse than not starting.
	var credentialStore *repository.GORMCredentialStore
	key, keyErr := masterKey(ctx, cfg)
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

	// Identity is opt-in like embedding, and for a sharper reason: turning it on
	// against an installation with no accounts and no keys would answer every
	// request with 401, so a missing signing key refuses to start rather than
	// producing a server nobody can reach.
	authStore := repository.NewAuthStore(db.DB)
	var authIssuer *auth.Issuer
	signingKey, signingErr := credentials.KeyFromEnvironment(cfg.Auth.SigningKeyEnv)
	switch {
	case signingErr == nil:
		issuer, issuerErr := auth.NewIssuer(signingKey, cfg.Auth.SessionTTL, nil)
		if issuerErr != nil {
			return fmt.Errorf("configure authentication: %w", issuerErr)
		}
		authIssuer = issuer
	case !errors.Is(signingErr, credentials.ErrNoKey):
		return fmt.Errorf("auth signing key: %w", signingErr)
	case cfg.Auth.Enabled:
		return fmt.Errorf(
			"auth.enabled is set but %s holds no signing key: every request would be refused",
			cfg.Auth.SigningKeyEnv)
	}

	if err := bootstrapIdentity(ctx, cfg.Auth, authStore, log); err != nil {
		return err
	}
	if !cfg.Auth.Enabled {
		log.Warn("the API is unauthenticated: anyone who can reach this port owns the installation",
			"enable_with", "KILASFLOW_AUTH_ENABLED=true")
	}

	executions := repository.NewExecutionStore(db.DB)
	// Injecting the extractor keeps node-type knowledge out of persistence
	// while still letting webhook bindings be synced inside the activation
	// transaction.
	workflows := repository.NewWorkflowStore(db.DB).
		WithWebhooks(webhook.Extract(nodeRegistry, nodes.WebhookPath)).
		WithSchedules(scheduler.Extract(nodes.ScheduleType), scheduler.Next).
		WithRetention(repository.RetentionPolicy{
			MaxAge:      cfg.History.Retention,
			MaxVersions: cfg.History.MaxVersions,
		})

	// The Execute Sub-workflow node's list mode. Registered here because this
	// is the only place that has both the loader registry and the workflow
	// store; the node names the loader and never reaches storage itself.
	if err := optionLoader.RegisterInternal(nodes.WorkflowListLoader, loadoptions.Workflows(
		func(ctx context.Context, tenant repository.TenantScope) ([]loadoptions.WorkflowOption, error) {
			stored, err := workflows.List(ctx, tenant)
			if err != nil {
				return nil, err
			}
			listed := make([]loadoptions.WorkflowOption, 0, len(stored))
			for _, candidate := range stored {
				listed = append(listed, loadoptions.WorkflowOption{
					ID: candidate.ID, Name: candidate.Name, Active: candidate.Active,
				})
			}
			return listed, nil
		})); err != nil {
		return fmt.Errorf("register the workflow option loader: %w", err)
	}
	// The data-table locator's From-list mode and the column mapper's schema.
	// Registered here because this is the only place holding the loader
	// registry and the row store together; the node names the loaders and
	// never reaches storage itself.
	if err := optionLoader.RegisterInternal(loadoptions.DatastoreListLoader, loadoptions.Datastores(
		func(ctx context.Context, tenantID string) ([]loadoptions.DatastoreOption, error) {
			definitions, err := datastoreEngine.ListDatastores(ctx, tenantID)
			if err != nil {
				return nil, err
			}
			listed := make([]loadoptions.DatastoreOption, 0, len(definitions))
			for _, candidate := range definitions {
				listed = append(listed, loadoptions.DatastoreOption{ID: candidate.ID, Name: candidate.Name})
			}
			return listed, nil
		})); err != nil {
		return fmt.Errorf("register the datastore option loader: %w", err)
	}
	if err := optionLoader.RegisterSchema(loadoptions.DatastoreMappingColumnsLoader, loadoptions.DatastoreColumns(
		func(ctx context.Context, tenantID, ref string) ([]loadoptions.DatastoreColumn, error) {
			definition, err := datastoreEngine.GetDatastore(ctx, tenantID, ref)
			if err != nil {
				if !datastore.IsUnknown(err) {
					return nil, err
				}
				definitions, listErr := datastoreEngine.ListDatastores(ctx, tenantID)
				if listErr != nil {
					return nil, listErr
				}
				found := false
				for _, candidate := range definitions {
					if strings.EqualFold(candidate.Name, ref) {
						definition = &candidate
						found = true
						break
					}
				}
				if !found {
					return nil, err
				}
			}
			columns := make([]loadoptions.DatastoreColumn, 0, len(definition.Columns))
			for _, column := range definition.Columns {
				columns = append(columns, loadoptions.DatastoreColumn{Name: column.Name, Type: string(column.Type)})
			}
			return columns, nil
		})); err != nil {
		return fmt.Errorf("register the datastore schema loader: %w", err)
	}
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
		Logger:      log,
		Executions:  executions,
		Binaries:    binaries,
		Events:      eventBroker,
		Catalog:     nodeRegistry,
		Runner:      engine.NewRunner(executorRegistry),
		Credentials: credentialStore,
		// Named so a called workflow starts from its sub-workflow trigger and
		// not from a webhook or schedule it also happens to carry.
		SubworkflowTriggerType: nodes.ExecuteWorkflowTriggerType,
		WorkerID:               resolveWorkerID(*workerIDOverride),
		DefaultTimeout:         cfg.Execution.DefaultTimeout,
		// server.public_url prefixes the resume links handed to waiting
		// executions. Empty renders path-only links for a same-origin setup.
		PublicBaseURL: cfg.Server.PublicURL,
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
	// PostgreSQL wakes idle workers in every process the moment an execution
	// is queued, instead of leaving each process to find it on its next poll
	// tick. SQLite has no LISTEN/NOTIFY and keeps the tick as its wake path.
	if cfg.Database.Driver == "postgres" {
		go func() {
			_ = runtime.WatchQueue(ctx, cfg.Database.DSN, cfg.Database.TablePrefix, func(err error) {
				log.Error("execution wake listener dropped; the poll interval remains the fallback", "error", err)
			})
		}()
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
		Datastores:   datastoreEngine,
		Schedules:    schedules,
		Webhook:      webhookHandler,
		Credentials:  credentialStore,
		OptionLoader: optionLoader,
		CredentialResolverFor: func(tenant repository.TenantScope) loadoptions.CredentialResolver {
			return credentialLookup{store: credentialStore, tenant: tenant}
		},
		TriggerCoordinator: webhook.NewCoordinator(
			webhookLifecycles, workflows, outboundPolicy(cfg.Outbound),
			func(tenantID string) engine.CredentialResolver {
				return engine.NewTenantCredentials(credentialStore, repository.TenantScope{ID: tenantID})
			},
			cfg.Server.PublicURL, log,
		),
		Events:              eventBroker,
		EmbedIssuer:         embedIssuer,
		Tenants:             handlers.NewPrincipalTenants(fallbackTenant(cfg.Auth)),
		AuthStore:           authStore,
		AuthIssuer:          authIssuer,
		ExecutionController: runtime,
		ResumeService:       runtime,
		NodeAvailability:    nodeAvailability(codeCompiler),
		HTTPPolicy:          outboundPolicy(cfg.Outbound),
		DatabaseGuard:       sqlGuard,
		SessionMemory:       agentMemory,
		Version:             version,
	})

	return server.Run(ctx)
}

// resolveWorkerID returns the explicit --worker-id when set, otherwise a
// host-qualified unique default. Two processes must never share an identity:
// the pid alone repeats across hosts (every container starts at 1), so the
// default carries the hostname, the pid, and a random suffix. Colliding IDs
// cannot cross-fence another worker's writes — ClaimNext stamps every claim
// with a fresh lease id — but they make every log line and every future
// "which worker ran this" answer wrong.
func resolveWorkerID(override string) string {
	if trimmed := strings.TrimSpace(override); trimmed != "" {
		return trimmed
	}
	return defaultWorkerID()
}

// defaultWorkerID builds the per-process identity. Randomness is best-effort:
// if it fails the pid plus the process start time still separates this
// process from any other on the same host.
func defaultWorkerID() string {
	host := "unknown"
	if name, err := os.Hostname(); err == nil && strings.TrimSpace(name) != "" {
		host = strings.TrimSpace(name)
	}
	host = strings.ReplaceAll(host, "/", "-")
	host = strings.ReplaceAll(host, " ", "-")
	if len(host) > 48 {
		host = host[:48]
	}
	var suffix [4]byte
	if _, err := rand.Read(suffix[:]); err != nil {
		return fmt.Sprintf("kilasflow-%s-%d-%d", host, os.Getpid(), time.Now().UnixNano())
	}
	return fmt.Sprintf("kilasflow-%s-%d-%s", host, os.Getpid(), hex.EncodeToString(suffix[:]))
}

// historySweepInterval is how often the age bound on workflow history is
// enforced. History is measured in days at the shortest, so sweeping hourly is
// already far finer than any retention an operator would configure, and a
// coarser sweep keeps a large installation from paying for a full scan often.
const historySweepInterval = time.Hour

// startHistorySweeper enforces the age bound on workflow version history.
//
// The count bound rides on SaveDraft's own transaction, but an age bound has to
// fire for a workflow nobody is saving — which is exactly the workflow whose
// history has gone stale — so it needs a clock of its own.
//
// If V2-p6-7 ever puts more than one process on the same database this needs
// the advisory-lock treatment the scheduler already has; until then a duplicated
// sweep is merely wasteful, because pruning is idempotent.
func startHistorySweeper(ctx context.Context, cfg config.History, store *repository.GORMWorkflowStore, log *slog.Logger) {
	// Nothing to enforce when history is unbounded, and starting a goroutine to
	// discover that every hour would be pure cost.
	if cfg.Retention <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(historySweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruned, err := store.PruneAllVersions(ctx)
				if err != nil {
					// Logged rather than fatal: failing to prune costs disk,
					// while stopping the server over it costs the customer their
					// automation.
					log.Error("pruning workflow history", "error", err)
					continue
				}
				if pruned > 0 {
					log.Info("pruned workflow history", "versions", pruned)
				}
			}
		}
	}()
}

// executionSweepInterval is how often expired executions are looked for.
//
// More often than the history sweep because an execution is written per run
// rather than per edit, so this is the table that actually grows; still slow
// enough that an installation with retention off pays nothing and one with it
// on pays a bounded index scan four times an hour.
const executionSweepInterval = 15 * time.Minute

// startExecutionPruner enforces the age bound on execution history.
//
// Nothing deleted an execution before this existed: every run stored its input,
// its output and its error, and every node attempt stored three more payloads,
// and a busy tenant's database grew without bound with no knob anywhere to stop
// it.
//
// Several KilasFlow processes may sweep the same database at once. That is safe
// rather than coordinated: the prune re-states its age and status predicates in
// the DELETE, so a batch another process already took removes nothing and the
// loser simply stops early.
func startExecutionPruner(
	ctx context.Context,
	cfg config.Execution,
	store *repository.GORMExecutionStore,
	discard func(tenantID, executionID string) error,
	log *slog.Logger,
) {
	// Retention off is the default, and a goroutine that wakes every quarter of
	// an hour to discover that would be pure cost.
	if cfg.Retention <= 0 {
		return
	}
	go func() {
		ticker := time.NewTicker(executionSweepInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				pruned, err := store.PruneExpired(ctx, repository.ExecutionRetention{MaxAge: cfg.Retention}, discard)
				if err != nil {
					// Logged rather than fatal, for the same reason the history
					// sweep is: failing to prune costs disk, while stopping the
					// server over it costs the customer their automation.
					log.Error("pruning execution history", "error", err)
				}
				if pruned > 0 {
					log.Info("pruned execution history", "executions", pruned)
				}
			}
		}
	}()
}

// fallbackTenant is the tenant a request with no principal resolves to.
//
// With authentication on it is empty, so a request that somehow reached a
// handler without one scopes to a tenant owning nothing rather than to the
// tenant owning everything. With it off, every caller is by definition the
// operator of a standalone install, and the default tenant is what all their
// existing data is already under.
func fallbackTenant(cfg config.Auth) string {
	if cfg.Enabled {
		return ""
	}
	return repository.DefaultTenantID
}

// bootstrapIdentity gives an installation its first tenant and, once, an owner.
//
// The tenant is ensured on every boot because the rest of the system writes
// rows against it whether or not authentication is on, and a foreign key needs
// it to exist. The account is created only when there are no accounts at all: a
// deployment that already has users must not gain another owner because an
// environment variable outlived the first boot.
func bootstrapIdentity(ctx context.Context, cfg config.Auth, store *repository.GORMAuthStore, log *slog.Logger) error {
	tenantID := cfg.BootstrapTenant
	if tenantID == "" {
		tenantID = repository.DefaultTenantID
	}
	if _, err := store.EnsureTenant(ctx, tenantID, tenantID); err != nil {
		return fmt.Errorf("bootstrap the first tenant: %w", err)
	}

	email := strings.TrimSpace(cfg.BootstrapEmail)
	if email == "" {
		return nil
	}
	existing, err := store.CountUsers(ctx)
	if err != nil {
		return fmt.Errorf("bootstrap the first account: %w", err)
	}
	if existing > 0 {
		return nil
	}

	password := os.Getenv(cfg.BootstrapPasswordEnv)
	if password == "" {
		log.Warn("auth.bootstrap_email is set but the password variable is empty; no account was created",
			"variable", cfg.BootstrapPasswordEnv)
		return nil
	}
	hash, err := auth.HashPassword(password)
	if err != nil {
		return fmt.Errorf("bootstrap the first account: %w", err)
	}
	user, err := store.CreateUser(ctx, repository.TenantScope{ID: tenantID}, email, "", hash)
	if err != nil {
		return fmt.Errorf("bootstrap the first account: %w", err)
	}
	log.Info("created the first account", "email", user.Email, "tenant", tenantID)

	return nil
}

// databaseGuard names the files a SQLite workflow credential must never open.
//
// A workflow that could open KilasFlow's own database would be able to read
// every credential, workflow, and execution in the installation, so the path
// is passed explicitly rather than inferred inside the node.
func databaseGuard(cfg config.Database) (sqlnode.Guard, error) {
	if cfg.DSN == "" {
		return sqlnode.Guard{}, nil
	}
	if cfg.Driver == "postgres" {
		target, err := sqlnode.ParseInternalTarget(sqlnode.DriverPostgres, cfg.DSN)
		if err != nil {
			// Fatal for the same reason as the SQLite path below: a guard
			// that cannot resolve its own identity still returns "allowed"
			// for every credential, and the install would look protected
			// and not be.
			return sqlnode.Guard{}, fmt.Errorf("the database guard could not resolve the configured DSN, "+
				"so a workflow credential naming KilasFlow's own database could not be refused: %w", err)
		}
		return sqlnode.Guard{Internal: target}, nil
	}
	// Resolved through the same function that opens the file, so the guarded
	// path and the opened path can never be derived differently. They were:
	// the DSN went in raw, and `file:./data/kilasflow.db` resolved to a path
	// no credential could ever match, which turned the guard into a silent
	// no-op protecting nothing.
	path, err := database.SQLitePath(cfg.DSN)
	if err != nil {
		// Fatal rather than an empty guard. A guard that cannot resolve its
		// own path still returns "allowed" for every credential, and nothing
		// anywhere would say so — the install would look protected and not be.
		// Refusing to boot is the only failure mode an operator can see.
		return sqlnode.Guard{}, fmt.Errorf("the database guard could not resolve the configured DSN, "+
			"so a workflow credential naming KilasFlow's own database could not be refused: %w", err)
	}
	if path == "" {
		// An in-memory database has no file for a credential to reach.
		return sqlnode.Guard{}, nil
	}
	return sqlnode.Guard{InternalPaths: []string{path}}, nil
}

// nodeAvailability reports the nodes this deployment cannot run.
//
// Only the Code node, for now, and only because compiling Go needs a toolchain
// the distroless image does not carry. Evaluated per request rather than once
// at startup, so a compiler that becomes reachable is picked up without a
// restart — and, more importantly, so one that goes away is too.
func nodeAvailability(compiler runcode.Compiler) func() map[string]string {
	return func() map[string]string {
		if compiler != nil && compiler.Available() {
			return nil
		}
		return map[string]string{
			nodes.CodeNodeType: "This deployment has no Go compiler, so Code nodes cannot be built. " +
				"Use the native nodes instead, or run an image that carries the Go toolchain.",
		}
	}
}

// defaultAgentMemorySessionsPerTenant bounds how many agent conversations one
// tenant may retain in the in-process memory store. It is a constant rather
// than a config value until internal/config gains an AI section; a workflow
// document cannot raise it either way.
const defaultAgentMemorySessionsPerTenant = 1000

// databaseCeiling is the deployment's bound on what a SQL node's parameters
// may ask for, which a workflow document cannot raise.
func databaseCeiling(cfg config.SQLNodes) sqlnode.Ceiling {
	return sqlnode.Ceiling{MaxRows: cfg.MaxRows, MaxTimeout: cfg.MaxStatementTimeout}
}

func outboundPolicy(cfg config.OutboundHTTP) safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = cfg.AllowPrivateNetworks
	policy.AllowedHosts = append([]string(nil), cfg.AllowedHosts...)
	policy.AllowedPrivateEndpoints = append([]string(nil), cfg.AllowedPrivateEndpoints...)
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

// masterKey resolves the credential master key: from the external secrets
// manager when the secrets section names one, from the environment
// otherwise. The two absences stay distinguishable: nothing configured is
// ErrNoKey (boot continues with credential storage disabled), while a named
// but unreachable manager is ErrManagerUnreachable (boot refuses). A
// transient network error at boot silently disabling every credential would
// be worse than not starting, so the manager path never degrades into the
// no-key one.
func masterKey(ctx context.Context, cfg config.Config) ([]byte, error) {
	if strings.TrimSpace(cfg.Secrets.ManagerAddr) == "" {
		return credentials.KeyFromEnvironment(cfg.Security.EncryptionKeyEnv)
	}
	policy := outboundPolicy(cfg.Outbound)
	provider, err := credentials.NewVaultProvider(
		cfg.Secrets.ManagerAddr, os.Getenv(cfg.Secrets.ManagerTokenEnv), policy)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", credentials.ErrManagerUnreachable, err)
	}
	if err := provider.Health(ctx); err != nil {
		return nil, fmt.Errorf("%w: %v", credentials.ErrManagerUnreachable, err)
	}
	key, err := credentials.KeyFromManager(ctx, provider, cfg.Secrets.MasterKey)
	if err != nil {
		// Partial configuration is rejected by config.Validate, so a NoKey
		// here means the reference itself is empty: refusing beats running
		// an install whose operator believes the manager is in charge.
		if errors.Is(err, credentials.ErrNoKey) {
			return nil, fmt.Errorf("secrets.master_key is required when secrets.manager_addr is set: %w", err)
		}
		return nil, err
	}
	return key, nil
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
