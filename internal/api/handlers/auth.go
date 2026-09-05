package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// PrincipalResource is what the dashboard needs to render "who am I".
//
// It carries no password hash, no key secret, and no other tenant's existence.
type PrincipalResource struct {
	TenantID string `json:"tenantId" doc:"Tenant every request from this caller is scoped to"`
	Kind     string `json:"kind" doc:"api_key or session"`
	UserID   string `json:"userId,omitempty" doc:"Set for a signed-in person"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	KeyID    string `json:"keyId,omitempty" doc:"Set for a machine caller"`
	Label    string `json:"label,omitempty" doc:"The key's name, for a machine caller"`
}

// APIKeyResource describes a stored key. It can never carry the secret,
// because the struct has nowhere to put one.
type APIKeyResource struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix" doc:"Public handle, enough to recognise a key in a log"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" doc:"Accurate to about a minute"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// CreatedAPIKeyResource is the one response that carries a key's secret.
type CreatedAPIKeyResource struct {
	ID        string    `json:"id"`
	Prefix    string    `json:"prefix"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
	// Token is the whole credential. This is the only time it exists anywhere
	// but the caller's hands: the server stores a hash and cannot reproduce it,
	// so a caller who loses it has to mint another.
	Token string `json:"token" doc:"The full key. Shown once and never again."`
}

// StreamTicketResource authorises one execution's event stream.
type StreamTicketResource struct {
	Ticket      string    `json:"ticket" doc:"Spend as the ticket query parameter on the events endpoint"`
	ExecutionID string    `json:"executionId"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// Auth serves sign-in, sign-out, identity, key management, and stream tickets.
type Auth struct {
	store  repository.AuthRepository
	issuer *auth.Issuer
	// executions confirms that a stream ticket names an execution the caller
	// can already see, so a ticket cannot be minted for somebody else's run.
	executions   repository.ExecutionRepository
	tenants      TenantResolver
	cookieName   string
	secureCookie bool
}

// NewAuth builds the identity handler.
//
// A nil store or issuer leaves every operation reporting that authentication is
// not configured, rather than half-working: an install with no signing key must
// not be able to mint a session nobody can verify.
func NewAuth(store repository.AuthRepository, issuer *auth.Issuer, executions repository.ExecutionRepository, tenants TenantResolver) *Auth {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Auth{
		store: store, issuer: issuer, executions: executions, tenants: tenants,
		cookieName: auth.SessionCookieName, secureCookie: true,
	}
}

// WithCookie chooses the session cookie's name and whether it is Secure.
//
// An insecure cookie loses the __Host- prefix as well, because a browser
// refuses that prefix without Secure — the two cannot be set independently.
func (handler *Auth) WithCookie(name string, secure bool) *Auth {
	if name != "" {
		handler.cookieName = name
	}
	handler.secureCookie = secure
	return handler
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" minLength:"3" maxLength:"255" doc:"Account email address"`
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"Account password"`
	}
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      PrincipalResource
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meOutput struct {
	Body PrincipalResource
}

type listAPIKeysOutput struct {
	Body struct {
		Items []APIKeyResource `json:"items"`
	}
}

type createAPIKeyInput struct {
	Body struct {
		Label string `json:"label" maxLength:"255" doc:"How this key will be recognised later"`
	}
}

type createAPIKeyOutput struct {
	Status int `status:"201"`
	Body   CreatedAPIKeyResource
}

type revokeAPIKeyInput struct {
	ID string `path:"id"`
}

type revokeAPIKeyOutput struct {
	Body APIKeyResource
}

type createStreamTicketInput struct {
	Body struct {
		ExecutionID string `json:"executionId" minLength:"1" doc:"Execution whose events will be streamed"`
	}
}

type createStreamTicketOutput struct {
	Status int `status:"201"`
	Body   StreamTicketResource
}

// Register wires the identity operations.
func (handler *Auth) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/auth/login",
		Summary: "Sign in",
		Description: "Exchanges an email and password for a session cookie. " +
			"The cookie is HttpOnly, so the browser can never read it back.",
		Tags: []string{"Auth"},
	}, handler.Login)

	huma.Register(api, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Sign out",
		Description: "Clears the session cookie in this browser. The session token itself is " +
			"stateless and stays valid until it expires, so a copy taken beforehand is not revoked.",
		Tags: []string{"Auth"},
	}, handler.Logout)

	huma.Register(api, huma.Operation{
		OperationID: "get-me", Method: http.MethodGet, Path: "/auth/me",
		Summary:     "Describe the current caller",
		Description: "Reports the tenant and identity this request authenticated as.",
		Tags:        []string{"Auth"},
	}, handler.Me)

	huma.Register(api, huma.Operation{
		OperationID: "list-api-keys", Method: http.MethodGet, Path: "/api-keys",
		Summary:     "List API keys",
		Description: "Lists this tenant's keys. No secret is ever included.",
		Tags:        []string{"Auth"},
	}, handler.ListKeys)

	huma.Register(api, huma.Operation{
		OperationID: "create-api-key", Method: http.MethodPost, Path: "/api-keys",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create an API key",
		Description: "Mints a key scoped to the calling tenant and returns it in full exactly once. " +
			"The server keeps only a hash and cannot show it again.",
		Tags: []string{"Auth"},
	}, handler.CreateKey)

	huma.Register(api, huma.Operation{
		OperationID: "revoke-api-key", Method: http.MethodDelete, Path: "/api-keys/{id}",
		Summary:     "Revoke an API key",
		Description: "Stops a key authenticating, immediately and permanently. The row is kept so an audit still has something to name.",
		Tags:        []string{"Auth"},
	}, handler.RevokeKey)

	huma.Register(api, huma.Operation{
		OperationID: "create-stream-ticket", Method: http.MethodPost, Path: "/stream-tickets",
		DefaultStatus: http.StatusCreated,
		Summary:       "Mint an execution stream ticket",
		Description: "EventSource cannot send an Authorization header, so a caller exchanges its " +
			"credential for a single-use ticket and spends it on the events endpoint. Tickets live " +
			"for seconds and name one execution.",
		Tags: []string{"Auth"},
	}, handler.CreateStreamTicket)
}

// Login exchanges a password for a session cookie.
//
// Every failure answers 401 with one message. An unknown address, a wrong
// password and a disabled account are indistinguishable from outside, so the
// endpoint cannot be used to find out who has an account here.
func (handler *Auth) Login(ctx context.Context, input *loginInput) (*loginOutput, error) {
	if handler.store == nil || handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	const refusal = "email address or password is incorrect"

	user, err := handler.store.FindUserForLogin(ctx, input.Body.Email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// The password is still hashed for an address that does not exist,
			// so the time a refusal takes does not reveal whether it did.
			auth.MatchPassword(input.Body.Password, auth.DecoyPasswordHash())
			return nil, huma.Error401Unauthorized(refusal)
		}
		return nil, huma.Error500InternalServerError("could not read the account")
	}
	if !auth.MatchPassword(input.Body.Password, user.PasswordHash) || user.DisabledAt != nil {
		return nil, huma.Error401Unauthorized(refusal)
	}

	_, token, err := handler.issuer.IssueSession(user.ID, user.TenantID, user.Email)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not start a session")
	}

	return &loginOutput{
		SetCookie: handler.cookie(token, int(handler.issuer.SessionTTL().Seconds())),
		Body: PrincipalResource{
			TenantID: user.TenantID, Kind: string(auth.KindSession),
			UserID: user.ID, Email: user.Email, Name: user.Name,
		},
	}, nil
}

// Logout clears the cookie in the browser that asked.
func (handler *Auth) Logout(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
	return &logoutOutput{SetCookie: handler.cookie("", -1)}, nil
}

// Me describes the caller.
func (handler *Auth) Me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	principal, found := auth.PrincipalFrom(ctx)
	if !found {
		return nil, huma.Error401Unauthorized("this request is not authenticated")
	}
	resource := PrincipalResource{
		TenantID: principal.TenantID, Kind: string(principal.Kind),
		UserID: principal.UserID, KeyID: principal.KeyID,
	}
	if principal.Kind == auth.KindSession {
		resource.Email = principal.Label
	} else {
		resource.Label = principal.Label
	}
	return &meOutput{Body: resource}, nil
}

// ListKeys returns this tenant's keys, secrets excluded by construction.
func (handler *Auth) ListKeys(ctx context.Context, _ *struct{}) (*listAPIKeysOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	keys, err := handler.store.ListAPIKeys(ctx, tenant)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not list API keys")
	}
	out := &listAPIKeysOutput{}
	out.Body.Items = make([]APIKeyResource, 0, len(keys))
	for _, key := range keys {
		out.Body.Items = append(out.Body.Items, apiKeyResource(key))
	}
	return out, nil
}

// CreateKey mints a key for the calling tenant.
func (handler *Auth) CreateKey(ctx context.Context, input *createAPIKeyInput) (*createAPIKeyOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	key, token, err := handler.store.CreateAPIKey(ctx, tenant, input.Body.Label)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not create the API key")
	}
	return &createAPIKeyOutput{
		Status: http.StatusCreated,
		Body: CreatedAPIKeyResource{
			ID: key.ID, Prefix: key.Prefix, Label: key.Label,
			CreatedAt: key.CreatedAt, Token: token,
		},
	}, nil
}

// RevokeKey withdraws a key belonging to the calling tenant.
func (handler *Auth) RevokeKey(ctx context.Context, input *revokeAPIKeyInput) (*revokeAPIKeyOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	key, err := handler.store.RevokeAPIKey(ctx, tenant, input.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("API key not found")
		}
		return nil, huma.Error500InternalServerError("could not revoke the API key")
	}
	return &revokeAPIKeyOutput{Body: apiKeyResource(key)}, nil
}

// CreateStreamTicket exchanges the caller's credential for a stream ticket.
func (handler *Auth) CreateStreamTicket(ctx context.Context, input *createStreamTicketInput) (*createStreamTicketOutput, error) {
	if handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	if tenant.ID == "" {
		return nil, huma.Error401Unauthorized("this request is not authenticated")
	}
	// The execution is confirmed inside the caller's own tenant first, so a
	// ticket can never be minted for a run the caller could not already read.
	if handler.executions != nil {
		if _, err := handler.executions.Get(ctx, tenant, input.Body.ExecutionID); err != nil {
			return nil, huma.Error404NotFound("execution not found")
		}
	}
	ticket, token, err := handler.issuer.IssueTicket(tenant.ID, input.Body.ExecutionID, 0)
	if err != nil {
		return nil, huma.Error500InternalServerError("could not mint a stream ticket")
	}
	return &createStreamTicketOutput{
		Status: http.StatusCreated,
		Body: StreamTicketResource{
			Ticket: token, ExecutionID: ticket.ExecutionID, ExpiresAt: ticket.ExpiresAt,
		},
	}, nil
}

// cookie builds the session cookie.
//
// SameSite=Lax is the only cross-site request forgery defence here: there is no
// CSRF token, so a state-changing request is protected because the browser
// declines to attach this cookie to one arriving from another site. An embedded
// editor does not rely on it — it carries an embed token instead — which is why
// Lax is affordable.
func (handler *Auth) cookie(value string, maxAge int) http.Cookie {
	return http.Cookie{
		Name:     handler.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   handler.secureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func apiKeyResource(key repository.APIKey) APIKeyResource {
	return APIKeyResource{
		ID: key.ID, Prefix: key.Prefix, Label: key.Label,
		CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt,
	}
}
