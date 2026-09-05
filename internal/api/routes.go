package api

import (
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslabs/kilas-flow/internal/api/handlers"
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
	handlers.NewNodeTypes(deps.NodeRegistry).Register(v1)
	handlers.NewWorkflows(deps.Workflows, deps.Executions, deps.NodeRegistry, deps.Tenants, deps.ExecutionController).Register(v1)
	handlers.NewExecutions(deps.ExecutionController, deps.Executions, deps.Tenants).Register(v1)
	handlers.NewCredentials(deps.Credentials, deps.Tenants).Register(v1)

	// Self-hosted API reference. Huma's own docs endpoint is disabled in
	// openAPIConfig because it loads Scalar from a CDN.
	router.Handle(DocsPath, docsHandler("KilasFlow API"))

	// Reserved for the webhook trigger. Answering explicitly beats letting
	// these fall through to the SPA and handing an API client an HTML page.
	router.Handle(WebhookPrefix, notImplemented("Webhook triggers arrive in a later milestone."))
	router.Handle(WebhookPrefix+"/*", notImplemented("Webhook triggers arrive in a later milestone."))

	router.Handle("/*", web.Handler())
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
