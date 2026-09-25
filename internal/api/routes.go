package api

import (
	"net/http"
	"os"
	"strings"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/web"
)

// registerRoutes mounts every route kilasflow serves.
//
// Ordering matters: the SPA claims "/*" and must be registered last, or it
// would shadow the API. New milestones add their operations to the v1 group.
func registerRoutes(router *chi.Mux, api huma.API, deps Deps) {
	// Version the operations without versioning the spec endpoints, so the
	// document stays at a stable /api/openapi.json across API versions.
	v1 := huma.NewGroup(api, APIPrefix)

	system := handlers.NewSystem(deps.Version, deps.DB)
	// The guard is the point: a nil *datastore.Engine stored in the reporter
	// interface would be a non-nil interface, so an unconditional WithFleet
	// would call FleetStatus through a nil pointer and answer 503 on every
	// instance that has no row store.
	if deps.Datastores != nil {
		system = system.WithFleet(deps.Datastores)
	}
	system.Register(v1)
	secureCookie := !deps.Config.Auth.CookieInsecure
	handlers.NewAuth(deps.AuthStore, deps.AuthIssuer, deps.Executions, deps.Tenants).
		WithCookie(auth.CookieName(secureCookie), secureCookie).Register(v1)
	// The operator surface is registered beside the identity endpoints because
	// it is the same boundary: it mints the tenants and accounts every other
	// route is then scoped to. It is not a tenant-scoped handler — it takes the
	// API prefix instead of a TenantResolver, because a resolver would answer
	// "which tenant is this request for" when the whole point of these
	// operations is that the operator names a different one.
	handlers.NewAdmin(deps.AuthStore, APIPrefix).WithTenantPurger(deps.TenantPurger).Register(v1)
	handlers.NewNodeTypes(deps.NodeRegistry).
		WithOptionLoading(deps.Tenants, deps.OptionLoader, deps.CredentialResolverFor).
		WithAvailability(deps.NodeAvailability).Register(v1)
	handlers.NewWorkflows(deps.Workflows, deps.Executions, deps.NodeRegistry, deps.Tenants, deps.ExecutionController).
		WithTriggers(deps.TriggerCoordinator).WithSessionMemory(deps.SessionMemory).WithIdempotency(deps.Idempotency).
		WithCredentials(deps.Credentials).Register(v1)
	handlers.NewExecutions(deps.ExecutionController, deps.Executions, deps.Events, deps.Tenants, deps.NodeRegistry).
		WithWorkflowVersions(deps.Workflows).WithCredentials(deps.Credentials).Register(v1)
	credentials := handlers.NewCredentials(deps.Credentials, deps.Tenants).
		WithHTTPPolicy(credentialTestPolicy(deps)).
		WithDatabaseGuard(deps.DatabaseGuard).
		WithTestTimeout(deps.Config.Credential.TestTimeout)
	googleSecret := ""
	if env := strings.TrimSpace(deps.Config.Google.ClientSecretEnv); env != "" {
		googleSecret = strings.TrimSpace(os.Getenv(env))
	}
	credentials.WithOAuth(
		deps.OAuthSigningKey,
		deps.Config.Server.PublicURL,
		deps.Config.Google.ClientID,
		googleSecret,
		deps.OAuthTokenURL,
		deps.OAuthHTTP,
	)
	credentials.Register(v1)
	handlers.NewSchedules(deps.Schedules, deps.Tenants).Register(v1)
	handlers.NewDatastores(deps.Datastores, deps.Tenants).WithIdempotency(deps.Idempotency).Register(v1)
	handlers.NewEmbedSessions(deps.EmbedIssuer, deps.Workflows, deps.Tenants).WithDatastores(deps.Datastores).Register(v1)
	handlers.NewInterop(deps.Workflows, deps.NodeRegistry, deps.Tenants).Register(v1)

	// Self-hosted API reference. Huma's own docs endpoint is disabled in
	// openAPIConfig because it loads Scalar from a CDN.
	router.Handle(DocsPath, docsHandler("KilasFlow API"))

	// The webhook prefix is answered explicitly rather than falling through to
	// the SPA, which would hand an API client an HTML page.
	webhookHandler := deps.Webhook
	if webhookHandler == nil {
		webhookHandler = notImplemented("Webhook triggers are not configured on this instance.")
	}
	router.Handle(WebhookPrefix, webhookHandler)
	router.Handle(WebhookPrefix+"/*", webhookHandler)

	// The resume prefix answers the same way: per-execution resume URLs keyed
	// by an opaque single-use token, never modelled as webhook bindings.
	resumeHandler := handlers.NewResume(deps.ResumeService, deps.Tenants).Handler()
	router.Handle(handlers.ResumePrefix, resumeHandler)
	router.Handle(handlers.ResumePrefix+"/*", resumeHandler)

	router.Handle(handlers.OAuthCallbackPath, credentials.OAuthCallback())

	// Last, because it reads the document every registration above wrote into:
	// the public operations have to be marked after they exist, and the SPA route
	// above is the one that must stay last among the handlers.
	markPublicOperations(api)

	router.Handle("/*", web.Handler(web.WithFrameAncestors(deps.Config.Embed.AllowedOrigins)))
}

// credentialTestPolicy is the egress policy a credential probe runs under.
//
// It is the instance's own policy, not a fresh default: a probe held to
// different rules than the HTTP node would either refuse a target the workflow
// can legitimately reach, or reach one the deployment has ruled out. A zero
// policy means the composition root passed none, and the conservative default
// is the only safe reading of that.
func credentialTestPolicy(deps Deps) safehttp.Policy {
	if deps.HTTPPolicy.Timeout == 0 && deps.HTTPPolicy.MaxResponseBytes == 0 {
		return safehttp.DefaultPolicy()
	}
	return deps.HTTPPolicy
}

// notImplemented answers a reserved route with a problem document rather than
// a 404, so the path is visibly claimed but not yet functional.
func notImplemented(detail string) http.Handler {
	body := `{"title":"Not Implemented","status":501,"detail":"` + detail + `"}`

	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/problem+json")
		w.WriteHeader(http.StatusNotImplemented)
		_, _ = w.Write([]byte(body))
	})
}
