package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/web"
)

// registerRoutes mounts every route kilasflow serves.
//
// Ordering matters: the SPA claims "/*" and must be registered last, or it
// would shadow the API. New milestones add their operations to the v1 group.
func registerRoutes(router *chi.Mux, api huma.API, deps Deps) {
	// Version the operations without versioning the spec endpoints, so the
	// document stays at a stable /api/openapi.json across API versions.
	v1 := huma.NewGroup(api, APIPrefix)

	handlers.NewSystem(deps.Version, deps.DB).Register(v1)
	secureCookie := !deps.Config.Auth.CookieInsecure
	handlers.NewAuth(deps.AuthStore, deps.AuthIssuer, deps.Executions, deps.Tenants).
		WithCookie(auth.CookieName(secureCookie), secureCookie).Register(v1)
	handlers.NewNodeTypes(deps.NodeRegistry).
		WithOptionLoading(deps.Tenants, deps.OptionLoader, deps.CredentialResolverFor).
		WithAvailability(deps.NodeAvailability).
		WithWorkflowCredentials(workflowCredentials(deps)).Register(v1)
	handlers.NewWorkflows(deps.Workflows, deps.Executions, deps.NodeRegistry, deps.Tenants, deps.ExecutionController).
		WithTriggers(deps.TriggerCoordinator).WithSessionMemory(deps.SessionMemory).Register(v1)
	handlers.NewExecutions(deps.ExecutionController, deps.Executions, deps.Events, deps.Tenants).Register(v1)
	handlers.NewCredentials(deps.Credentials, deps.Tenants).
		WithHTTPPolicy(credentialTestPolicy(deps)).
		WithDatabaseGuard(deps.DatabaseGuard).
		WithTestTimeout(deps.Config.Credential.TestTimeout).Register(v1)
	handlers.NewSchedules(deps.Schedules, deps.Tenants).Register(v1)
	handlers.NewDatastores(deps.Datastores, deps.Tenants).Register(v1)
	handlers.NewEmbedSessions(deps.EmbedIssuer, deps.Workflows, deps.Tenants).Register(v1)
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

	router.Handle("/*", web.Handler())
}

// workflowCredentials lists the credential IDs one workflow's nodes reference.
//
// Built here rather than injected, because the workflow repository is already a
// dependency and a second seam for "read a workflow" would be one more thing to
// keep pointed at the same store.
func workflowCredentials(deps Deps) func(context.Context, string, string) ([]string, error) {
	if deps.Workflows == nil {
		return nil
	}
	return func(ctx context.Context, tenantID, workflowID string) ([]string, error) {
		stored, err := deps.Workflows.Get(ctx, repository.TenantScope{ID: tenantID}, workflowID)
		if err != nil {
			return nil, err
		}
		referenced := make([]string, 0, 2)
		for _, node := range stored.LatestVersion.Document.Nodes {
			for _, id := range node.Credentials {
				referenced = append(referenced, id)
			}
		}
		return referenced, nil
	}
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
