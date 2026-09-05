package handlers

import (
	"context"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// EmbedSessionResource is what a host integration needs and nothing more.
//
// It carries no workflow content, no credential, and no tenant detail: the
// host's browser receives only a token, where to load, and how long it lasts.
type EmbedSessionResource struct {
	Token     string    `json:"token"`
	EmbedURL  string    `json:"embedUrl"`
	ExpiresAt time.Time `json:"expiresAt"`
	Scopes    []string  `json:"scopes"`
	// Origin echoes the one origin this token may be used from, so a host can
	// assert the postMessage target rather than guessing it.
	Origin   string         `json:"origin"`
	Branding embed.Branding `json:"branding,omitempty"`
}

// EmbedSessions mints short-lived iframe authorizations.
type EmbedSessions struct {
	issuer    *embed.Issuer
	workflows repository.WorkflowRepository
	tenants   TenantResolver
}

// NewEmbedSessions constructs the embed-session handler.
func NewEmbedSessions(issuer *embed.Issuer, workflows repository.WorkflowRepository, tenants TenantResolver) *EmbedSessions {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &EmbedSessions{issuer: issuer, workflows: workflows, tenants: tenants}
}

type embedSessionBody struct {
	WorkflowID string         `json:"workflowId" minLength:"1" doc:"Workflow this session may open"`
	Scopes     []string       `json:"scopes" doc:"workflow:read, workflow:write, workflow:run"`
	Origin     string         `json:"origin" minLength:"1" doc:"Exact origin of the page that will frame the editor"`
	TTLSeconds int            `json:"ttlSeconds,omitempty" doc:"Session lifetime; capped by the server"`
	Branding   embed.Branding `json:"branding,omitempty" doc:"Validated white-label values; never markup"`
}

type createEmbedSessionInput struct {
	Body embedSessionBody
}

type embedSessionOutput struct {
	Status int `status:"201"`
	Body   EmbedSessionResource
}

// Register wires embed-session creation.
func (handler *EmbedSessions) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "create-embed-session", Method: http.MethodPost, Path: "/embed-sessions",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create an embed session",
		Description: "Mints a short-lived, workflow-scoped token for one host origin. " +
			"The host passes it to the iframe over postMessage.",
		Tags: []string{"Embed"},
	}, handler.Create)
}

// Create mints a session after checking the workflow exists in this tenant.
func (handler *EmbedSessions) Create(ctx context.Context, input *createEmbedSessionInput) (*embedSessionOutput, error) {
	if handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("embed sessions are not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)

	// The workflow is confirmed before a token is minted, so a session can
	// never point at something that does not exist or belongs elsewhere.
	if handler.workflows != nil {
		if _, err := handler.workflows.Get(ctx, tenant, input.Body.WorkflowID); err != nil {
			return nil, huma.Error404NotFound("workflow not found")
		}
	}

	scopes := make([]embed.Scope, 0, len(input.Body.Scopes))
	for _, scope := range input.Body.Scopes {
		scopes = append(scopes, embed.Scope(scope))
	}

	session, token, err := handler.issuer.Issue(embed.Request{
		TenantID: tenant.ID, WorkflowID: input.Body.WorkflowID,
		Scopes: scopes, Origin: input.Body.Origin,
		Lifetime: time.Duration(input.Body.TTLSeconds) * time.Second,
		Branding: input.Body.Branding,
	})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	granted := make([]string, 0, len(session.Scopes))
	for _, scope := range session.Scopes {
		granted = append(granted, string(scope))
	}
	return &embedSessionOutput{
		Status: http.StatusCreated,
		Body: EmbedSessionResource{
			Token:     token,
			EmbedURL:  "/embed/" + session.WorkflowID,
			ExpiresAt: session.ExpiresAt, Scopes: granted,
			Origin: session.Origin, Branding: session.Branding,
		},
	}, nil
}
