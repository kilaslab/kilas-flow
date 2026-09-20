// Package api builds the kilasflow HTTP surface: the REST API, the generated
// OpenAPI document and docs UI, the webhook entrypoint, and the embedded SPA.
//
// Production runs a single origin, so all of these are mounted on one router.
package api

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/datastore"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// Path prefixes for the single-origin layout. Keeping them in one place makes
// the Vite dev proxy easy to keep in step (see web/vite.config.ts).
const (
	APIPrefix     = "/api/v1"
	OpenAPIPath   = "/api/openapi"
	SchemasPath   = "/api/schemas"
	DocsPath      = "/docs"
	WebhookPrefix = "/webhook"
	// ResumePrefix is where an approval page resumes a durable wait by token.
	// It is a path token rather than an API operation because the person
	// clicking it may hold nothing but the link, and it is listed here because
	// the Vite dev proxy mirrors this block.
	ResumePrefix = "/resume"
)

// Deps are the collaborators a Server needs, passed by the caller rather than
// resolved from a global.
type Deps struct {
	Config       config.Config
	Logger       *slog.Logger
	DB           handlers.Pinger
	NodeRegistry *node.Registry
	Workflows    repository.WorkflowRepository
	Executions   repository.ExecutionRepository
	Credentials  repository.CredentialRepository
	// Events is the live execution feed. A nil broker disables streaming
	// without affecting durable execution.
	Events *events.Broker
	// Schedules is the cron surface. A nil store disables the endpoints.
	Schedules repository.ScheduleRepository
	// Datastores is the row store. A nil engine disables the endpoints.
	Datastores *datastore.Engine
	// Webhook serves the reserved /webhook prefix. A nil handler keeps the
	// prefix answering "not implemented" rather than falling through to the SPA.
	Webhook http.Handler
	// EmbedIssuer mints and verifies iframe sessions. A nil issuer disables
	// embedding rather than defaulting it open.
	EmbedIssuer *embed.Issuer
	// ExecutionController owns live worker wakeups and cancellation. It is
	// separate from the repository so HTTP never reaches into ORM state.
	ExecutionController handlers.ExecutionController
	// ResumeService owns the durable-wait surface behind the /resume prefix:
	// wait info for the approval page and token consumes that re-queue their
	// execution. Nil leaves the prefix answering "unavailable" rather than
	// half-working. It is a separate field rather than part of
	// ExecutionController so existing controller fakes keep compiling.
	ResumeService handlers.ResumeService
	Tenants       handlers.TenantResolver
	// AuthStore is the identity boundary: tenants, accounts and API keys. Nil
	// leaves the identity endpoints reporting that authentication is not
	// configured, and — with Config.Auth.Enabled set — leaves every API key
	// refused rather than admitted.
	AuthStore repository.AuthRepository
	// AuthIssuer signs browser sessions and stream tickets. Nil disables both
	// for the same reason: an instance that cannot verify a session must not
	// mint one.
	AuthIssuer *auth.Issuer
	// TriggerCoordinator registers a workflow's webhook triggers with the
	// remote services they depend on, around activation. Nil leaves a workflow
	// activating and routing normally without telling any service where to
	// deliver, which is what every trigger did before it existed.
	TriggerCoordinator handlers.TriggerCoordinator
	// OptionLoader resolves a property's selectable values at edit time. Nil
	// leaves the endpoint answering "unavailable" rather than half-working.
	OptionLoader *loadoptions.Resolver
	// CredentialResolverFor builds a tenant-scoped credential resolver for an
	// option loader, so a request naming another tenant's credential resolves
	// to nothing rather than to a secret.
	CredentialResolverFor func(repository.TenantScope) loadoptions.CredentialResolver
	// HTTPPolicy is the instance egress policy. A credential probe runs under
	// it rather than under a policy of its own, so testing a credential and
	// using it reach the same set of hosts. A zero value falls back to
	// safehttp.DefaultPolicy.
	HTTPPolicy safehttp.Policy
	// NodeAvailability reports which nodes this deployment cannot run, keyed by
	// node type, so the editor can say so before a workflow is saved rather
	// than after it runs. Nil means everything registered can run.
	NodeAvailability func() map[string]string
	// DatabaseGuard is the same guard the database executors receive, so a
	// SQLite credential naming KilasFlow's own database is refused by the test
	// endpoint too rather than only at run time.
	DatabaseGuard sqlnode.Guard
	// SessionMemory is the agent conversation store. Deleting a workflow
	// drops its sessions through it. Nil leaves deletion removing only the
	// workflow, with conversations ageing out under retention instead.
	SessionMemory handlers.SessionForgetter
	Version       string
}

// Server owns the HTTP listener and the route tree.
type Server struct {
	cfg    config.Config
	log    *slog.Logger
	router *chi.Mux
	http   *http.Server
}

// NewServer builds the router and the underlying http.Server.
func NewServer(deps Deps) *Server {
	router := chi.NewMux()

	router.Use(middleware.RequestID)
	router.Use(middleware.Recover(deps.Logger))
	router.Use(middleware.Logger(deps.Logger))
	// Ahead of the authentication gate, and that order is the point: a
	// preflight carries no credential by definition — it is the browser asking
	// whether it may send one — so a gate that answered it would refuse the
	// request that was supposed to authorise the real one. The layer reflects
	// only the configured embed origins and never allows credentials, so a page
	// outside the allowlist still gets nothing.
	router.Use(middleware.CORS(deps.Config.Embed.AllowedOrigins))
	// Ahead of EmbedAuth, and composing with it rather than stacking on top:
	// a request carrying an embed token is passed straight through to the embed
	// layer, so it is confined to one workflow instead of also having to
	// present a key that would only widen it.
	//
	// Scoped to the API prefix, because this same mux carries the public
	// webhook surface and the SPA's own static assets, and neither can present
	// a credential.
	router.Use(middleware.Authenticate(middleware.AuthOptions{
		Enabled:    deps.Config.Auth.Enabled,
		Keys:       deps.AuthStore,
		Sessions:   deps.AuthIssuer,
		Users:      deps.AuthStore,
		CookieName: auth.CookieName(!deps.Config.Auth.CookieInsecure),
		APIPrefix:  APIPrefix,
	}))
	// Mounted for every request, but inert unless a request carries an embed
	// token: the internal dashboard is unaffected, and an embedded editor is
	// confined to its own workflow and scopes.
	router.Use(middleware.EmbedAuth(deps.EmbedIssuer))

	api := humachi.New(router, openAPIConfig(deps))

	registerRoutes(router, api, deps)

	srv := &Server{
		cfg:    deps.Config,
		log:    deps.Logger,
		router: router,
	}

	srv.http = &http.Server{
		Addr:              deps.Config.Server.Addr(),
		Handler:           router,
		ReadHeaderTimeout: deps.Config.Server.ReadHeaderTimeout,
	}

	return srv
}

// openAPIConfig describes the generated document and the docs UI.
func openAPIConfig(deps Deps) huma.Config {
	cfg := huma.DefaultConfig("KilasFlow API", deps.Version)

	cfg.Info.Description = "Embeddable workflow automation engine. " +
		"Every operation available in the editor is available here: the canvas " +
		"is a client of this API, not the owner of workflow state."

	// Served from the same origin as the SPA, so relative paths are correct.
	cfg.OpenAPIPath = OpenAPIPath
	cfg.SchemasPath = SchemasPath

	// Huma's built-in renderers pull their JavaScript from unpkg. kilasflow serves
	// its own page from a vendored bundle instead (see docs.go), so disable
	// Huma's and register ours in registerRoutes.
	cfg.DocsPath = ""

	// The document used to describe no authentication at all while the server
	// enforced it on every operation: /docs offered nowhere to enter a key, a
	// generated client carried no auth typing, and a reader concluded the API was
	// open by design. Both credentials the middleware accepts are declared here.
	if cfg.Components == nil {
		cfg.Components = &huma.Components{}
	}
	cfg.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		apiKeyScheme: {
			Type:   "http",
			Scheme: "bearer",
			Description: "An API key from Settings → API keys. Send it as " +
				"`Authorization: Bearer <key>`.",
		},
		sessionScheme: {
			Type: "apiKey",
			In:   "cookie",
			Name: auth.CookieName(!deps.Config.Auth.CookieInsecure),
			Description: "Browser session cookie issued by POST /api/v1/auth/login. " +
				"Anything that can hold a cookie can use it.",
		},
	}

	// Enforcement is what the document should describe, so the requirement is
	// attached at the root — and only when auth is on. A root requirement is
	// inherited by every operation, which is the right default: an operation
	// added later is described as authenticated without anyone remembering to say
	// so. The handful that are public are marked off explicitly below.
	if deps.Config.Auth.Enabled {
		cfg.Security = []map[string][]string{
			{apiKeyScheme: {}},
			{sessionScheme: {}},
		}
	}

	return cfg
}

// Names of the security schemes in the generated document.
const (
	apiKeyScheme  = "apiKey"
	sessionScheme = "session"
)

// publicOperationPaths are the operations served without a credential, relative
// to APIPrefix. They mirror middleware's publicOperations map, which is the
// authority on what is actually served without one — this copy exists so the
// document can say the same thing, and TestPublicOperationsNeedNoCredential
// asserts the two agree as behaviour rather than as text.
//
// The webhook and resume prefixes are not listed because they are mounted as
// plain routes with no operation in the document.
var publicOperationPaths = map[string]bool{
	"/health":      true,
	"/ready":       true,
	"/auth/login":  true,
	"/auth/logout": true,
}

// markPublicOperations takes the document-level credential requirement off the
// operations that need none. Without this, /docs shows a lock on login and a
// generated client insists on a key to call health.
func markPublicOperations(api huma.API) {
	for route, item := range api.OpenAPI().Paths {
		if !publicOperationPaths[strings.TrimPrefix(route, APIPrefix)] {
			continue
		}
		for _, operation := range operationsOf(item) {
			if operation == nil {
				continue
			}
			// An empty, non-nil requirement array is how the specification says
			// "nothing is required here": it overrides the document root, whereas
			// leaving the field alone would inherit it.
			operation.Security = []map[string][]string{}
		}
	}
}

// operationsOf lists the operations a path item carries, so callers do not have
// to repeat the eight accessors.
func operationsOf(item *huma.PathItem) []*huma.Operation {
	return []*huma.Operation{
		item.Get, item.Put, item.Post, item.Delete,
		item.Options, item.Head, item.Patch, item.Trace,
	}
}

// Handler exposes the route tree for testing.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Run serves until ctx is cancelled, then shuts down gracefully so in-flight
// requests and workflow executions are allowed to finish.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

	// BaseContext is what makes a graceful shutdown graceful. http.Server's
	// Shutdown stops accepting connections but never cancels an active
	// request's context, and an event stream only returns when its context is
	// done or the execution ends — so one open editor tab used to hold the
	// server for the whole shutdown timeout and then report failure. Deriving
	// every request from a context this method cancels on shutdown means the
	// stream handlers see Done immediately and return.
	serveCtx, stopServing := context.WithCancel(context.WithoutCancel(ctx))
	defer stopServing()
	s.http.BaseContext = func(net.Listener) context.Context { return serveCtx }

	go func() {
		s.log.Info("http server listening",
			"addr", s.cfg.Server.Addr(),
			"docs", DocsPath,
			"openapi", OpenAPIPath+".json",
		)

		if err := s.http.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			errCh <- fmt.Errorf("listen on %s: %w", s.cfg.Server.Addr(), err)
			return
		}

		errCh <- nil
	}()

	select {
	case err := <-errCh:
		return err

	case <-ctx.Done():
		s.log.Info("shutting down", "timeout", s.cfg.Server.ShutdownTimeout)

		// Cancelled before Shutdown so every open request context — including
		// each event stream — is done by the time Shutdown waits on them.
		stopServing()

		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), s.cfg.Server.ShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			// Logged as well as returned: the return value becomes a plain
			// stderr line from main, which a structured log pipeline never
			// sees, and a shutdown that timed out is exactly the line an
			// operator needs to find.
			s.log.Error("graceful shutdown did not finish", "error", err)
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		s.log.Info("shutdown complete")
		return nil
	}
}
