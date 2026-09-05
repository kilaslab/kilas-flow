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
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/events"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// Path prefixes for the single-origin layout. Keeping them in one place makes
// the Vite dev proxy easy to keep in step (see web/vite.config.ts).
const (
	APIPrefix     = "/api/v1"
	OpenAPIPath   = "/api/openapi"
	SchemasPath   = "/api/schemas"
	DocsPath      = "/docs"
	WebhookPrefix = "/webhook"
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
	// Webhook serves the reserved /webhook prefix. A nil handler keeps the
	// prefix answering "not implemented" rather than falling through to the SPA.
	Webhook http.Handler
	// EmbedIssuer mints and verifies iframe sessions. A nil issuer disables
	// embedding rather than defaulting it open.
	EmbedIssuer *embed.Issuer
	// ExecutionController owns live worker wakeups and cancellation. It is
	// separate from the repository so HTTP never reaches into ORM state.
	ExecutionController handlers.ExecutionController
	Tenants             handlers.TenantResolver
	// TriggerCoordinator registers a workflow's webhook triggers with the
	// remote services they depend on, around activation. Nil leaves a workflow
	// activating and routing normally without telling any service where to
	// deliver, which is what every trigger did before it existed.
	TriggerCoordinator handlers.TriggerCoordinator
	Version            string
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

	return cfg
}

// Handler exposes the route tree for testing.
func (s *Server) Handler() http.Handler {
	return s.router
}

// Run serves until ctx is cancelled, then shuts down gracefully so in-flight
// requests and workflow executions are allowed to finish.
func (s *Server) Run(ctx context.Context) error {
	errCh := make(chan error, 1)

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
		s.log.Info("shutting down")

		shutdownCtx, cancel := context.WithTimeout(
			context.WithoutCancel(ctx), s.cfg.Server.ShutdownTimeout)
		defer cancel()

		if err := s.http.Shutdown(shutdownCtx); err != nil {
			return fmt.Errorf("graceful shutdown: %w", err)
		}

		return nil
	}
}
