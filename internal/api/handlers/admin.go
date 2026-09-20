package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// TenantResource is one customer as the operator sees it.
//
// The account count is part of the listing rather than a second call, because
// the question an operator opens this page with is "who is here and how big is
// each of them", and a per-tenant follow-up would turn one page into a hundred
// requests.
type TenantResource struct {
	ID        string    `json:"id"`
	Name      string    `json:"name"`
	CreatedAt time.Time `json:"createdAt"`
	UserCount int64     `json:"userCount" doc:"Accounts this tenant holds"`
}

// UserResource is a dashboard account as the operator sees it.
//
// There is no field for a password hash and there never should be: the store's
// reads leave the hash behind (userFromModel only fills it for the one login
// lookup), and this type has nowhere to put one, so a listing cannot leak what
// the struct cannot carry.
type UserResource struct {
	ID       string `json:"id"`
	TenantID string `json:"tenantId"`
	Email    string `json:"email"`
	Name     string `json:"name"`
	// DisabledAt is when the account was offboarded. Absent means the account
	// can sign in.
	DisabledAt *time.Time `json:"disabledAt,omitempty"`
	CreatedAt  time.Time  `json:"createdAt"`
}

// Admin serves the operator-only provisioning surface.
//
// It is what makes a second customer a product action rather than a database
// one. Before it, a tenant existed only if somebody inserted a row, and a user
// only if the one-time bootstrap happened to create them: the multi-tenant
// embedding story the SDK describes could not be provisioned through the API at
// all, so the SDK's own reference host documented raw SQL against the store.
//
// Every operation here is available to the operator's own key and to nobody
// else, including a perfectly valid API key belonging to a customer. The
// surface does not widen what an existing tenant can reach; it is the thing
// that creates tenants for them to be confined to.
type Admin struct {
	store repository.AuthRepository
	// prefix is where these routes are mounted, so a Location header names a
	// URL the caller can follow. It is injected rather than assumed: the
	// handler registers a path relative to a huma group and never sees the
	// group's own prefix, so the alternative would be a second copy of the
	// version string that could drift from the first.
	prefix string
}

// NewAdmin builds the operator handler.
//
// A nil store leaves every operation answering 503, the same as the identity
// endpoints: an install whose composition root passed no identity boundary must
// say so rather than accept a create it cannot persist.
func NewAdmin(store repository.AuthRepository, apiPrefix string) *Admin {
	return &Admin{store: store, prefix: strings.TrimSuffix(strings.TrimSpace(apiPrefix), "/")}
}

type listTenantsOutput struct {
	Body struct {
		Items []TenantResource `json:"items"`
	}
}

type getTenantInput struct {
	ID string `path:"id" doc:"Tenant ID"`
}

type getTenantOutput struct {
	Body TenantResource
}

type createTenantInput struct {
	Body struct {
		ID   string `json:"id" minLength:"1" maxLength:"64" pattern:"^[a-z0-9][a-z0-9_-]*$" doc:"Stable ID; it appears in URLs and on every row scoped to this tenant"`
		Name string `json:"name" maxLength:"255" doc:"Display name. Defaults to the ID"`
	}
}

type createTenantOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location" doc:"Where the created tenant can be read back"`
	Body     TenantResource
}

type listTenantUsersInput struct {
	ID string `path:"id" doc:"Tenant ID"`
}

type listTenantUsersOutput struct {
	Body struct {
		Items []UserResource `json:"items"`
	}
}

type createTenantUserInput struct {
	ID   string `path:"id" doc:"Tenant the account belongs to"`
	Body struct {
		Email    string `json:"email" minLength:"3" maxLength:"255" doc:"Sign-in address. Unique across the deployment, because login presents an address and nothing else"`
		Name     string `json:"name" maxLength:"255" doc:"Display name"`
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"Hashed with PBKDF2 before it reaches the database"`
	}
}

type createTenantUserOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location" doc:"Where the created account can be listed from"`
	Body     UserResource
}

type setUserDisabledInput struct {
	ID     string `path:"id" doc:"Tenant the account belongs to"`
	UserID string `path:"userId" doc:"Account to disable or re-enable"`
}

type setUserDisabledOutput struct {
	Body UserResource
}

type setUserPasswordInput struct {
	ID     string `path:"id"`
	UserID string `path:"userId"`
	Body   struct {
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"Replacement password. The old one stops working immediately"`
	}
}

type setUserPasswordOutput struct {
	Body UserResource
}

type createTenantAPIKeyInput struct {
	ID   string `path:"id" doc:"Tenant the key will belong to"`
	Body struct {
		Label string `json:"label" maxLength:"255" doc:"How the key will be recognised later"`
	}
}

type createTenantAPIKeyOutput struct {
	Status int `status:"201"`
	// No Location header, unlike the tenant and account creates: an operator
	// has no read-back path for another tenant's keys — the listing endpoint is
	// scoped to the caller's own tenant — so a Location here would name a URL
	// that does not exist.
	Body CreatedAPIKeyResource
}

// operatorOnly refuses every caller that is not the operator's.
//
// The decision is made here rather than in the middleware because it is about
// this surface alone: authentication has already answered *who* the caller is,
// and the only question left is whether that principal holds the deployment's
// provisioning authority.
//
// Only an API key is accepted, not a session, even one whose tenant is the
// operator tenant. The boot path hands this authority out as a key and nothing
// else, so a session would be a second way to acquire it — create an account in
// the operator tenant — that nothing bootstraps, nothing documents, and no
// check besides this one would notice.
//
// An unauthenticated request is refused exactly like a customer's key. That is
// deliberate: with authentication disabled there is no principal at all, and
// treating "no identity" as "the operator" would hand this surface to anyone
// who can reach the port on exactly the installs least prepared for it.
//
// One message covers every refusal. Distinguishing a missing credential from a
// tenant key, or naming the privileged tenant, would tell a caller which
// credentials are worth stealing and what to look for once they had one.
func operatorOnly(ctx context.Context) error {
	if principal, found := auth.PrincipalFrom(ctx); found &&
		principal.Kind == auth.KindAPIKey && principal.TenantID == repository.OperatorTenantID {
		return nil
	}
	return huma.Error403Forbidden("this endpoint is reserved for the operator")
}

// Register wires the operator operations.
func (handler *Admin) Register(api huma.API) {
	const operatorNote = "Requires the operator credential: an API key scoped to the " +
		"operator tenant. Any other principal, a customer's key or any session, is refused."

	huma.Register(api, huma.Operation{
		OperationID: "list-tenants", Method: http.MethodGet, Path: "/tenants",
		Summary: "List tenants",
		Description: "Every tenant in this deployment with its account count, oldest first. " +
			operatorNote,
		Tags: []string{"Admin"},
	}, handler.ListTenants)

	huma.Register(api, huma.Operation{
		OperationID: "create-tenant", Method: http.MethodPost, Path: "/tenants",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create a tenant",
		Description: "Adds a customer. Answers 409 if the ID is taken, so a mistyped create is " +
			"never mistaken for a successful one. " + operatorNote,
		Tags: []string{"Admin"},
	}, handler.CreateTenant)

	huma.Register(api, huma.Operation{
		OperationID: "get-tenant", Method: http.MethodGet, Path: "/tenants/{id}",
		Summary:     "Read one tenant",
		Description: "The resource the create response points at. " + operatorNote,
		Tags:        []string{"Admin"},
	}, handler.GetTenant)

	huma.Register(api, huma.Operation{
		OperationID: "list-tenant-users", Method: http.MethodGet, Path: "/tenants/{id}/users",
		Summary:     "List a tenant's accounts",
		Description: "Reads one tenant's accounts. No password hash is ever included. " + operatorNote,
		Tags:        []string{"Admin"},
	}, handler.ListTenantUsers)

	huma.Register(api, huma.Operation{
		OperationID: "create-tenant-user", Method: http.MethodPost, Path: "/tenants/{id}/users",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create an account in a tenant",
		Description: "Creates a dashboard account that can sign in immediately. This is the path " +
			"that used to be reachable only through the one-time bootstrap or a raw insert. " +
			operatorNote,
		Tags: []string{"Admin"},
	}, handler.CreateTenantUser)

	huma.Register(api, huma.Operation{
		OperationID: "disable-tenant-user", Method: http.MethodPost,
		Path:    "/tenants/{id}/users/{userId}/disable",
		Summary: "Disable an account",
		Description: "Stops the account signing in from the next request onwards. The row is kept " +
			"so the workflows and executions it authored still have a name. " + operatorNote,
		Tags: []string{"Admin"},
	}, handler.DisableTenantUser)

	huma.Register(api, huma.Operation{
		OperationID: "enable-tenant-user", Method: http.MethodPost,
		Path:    "/tenants/{id}/users/{userId}/enable",
		Summary: "Re-enable an account",
		Description: "Clears the offboarding marker, so the account signs in again with its " +
			"existing password. " + operatorNote,
		Tags: []string{"Admin"},
	}, handler.EnableTenantUser)

	huma.Register(api, huma.Operation{
		OperationID: "set-tenant-user-password", Method: http.MethodPost,
		Path:    "/tenants/{id}/users/{userId}/password",
		Summary: "Replace an account's password",
		Description: "Sets a new password. This is the operator's reset: the old password stops " +
			"working at once, and sessions minted under it are cut loose by the account's " +
			"password version. " + operatorNote,
		Tags: []string{"Admin"},
	}, handler.SetTenantUserPassword)

	huma.Register(api, huma.Operation{
		OperationID: "create-tenant-api-key", Method: http.MethodPost, Path: "/tenants/{id}/api-keys",
		DefaultStatus: http.StatusCreated,
		Summary:       "Mint an API key for a tenant",
		Description: "Mints a key on another tenant's behalf. The existing /api-keys endpoint can " +
			"only mint for the caller, so without this a new tenant could be created and then " +
			"never used. The token is returned exactly once and is never listed. " + operatorNote,
		Tags: []string{"Admin"},
	}, handler.CreateTenantAPIKey)
}

// ListTenants returns every tenant with its account count.
func (handler *Admin) ListTenants(ctx context.Context, _ *struct{}) (*listTenantsOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenants, err := handler.store.ListTenants(ctx)
	if err != nil {
		return nil, serverProblem(ctx, "could not list tenants", err)
	}
	out := &listTenantsOutput{}
	out.Body.Items = make([]TenantResource, 0, len(tenants))
	for _, tenant := range tenants {
		out.Body.Items = append(out.Body.Items, tenantResource(tenant))
	}
	return out, nil
}

// CreateTenant adds a customer tenant.
func (handler *Admin) CreateTenant(ctx context.Context, input *createTenantInput) (*createTenantOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant, err := handler.store.CreateTenant(ctx, input.Body.ID, input.Body.Name)
	if err != nil {
		if errors.Is(err, repository.ErrAlreadyExists) {
			return nil, huma.Error409Conflict("a tenant with that ID already exists")
		}
		return nil, serverProblem(ctx, "could not create the tenant", err)
	}
	// The new tenant has no accounts yet, so the count is known without asking
	// the store for it.
	return &createTenantOutput{
		Status:   http.StatusCreated,
		Location: handler.tenantPath(tenant.ID),
		Body:     tenantResource(repository.TenantSummary{Tenant: tenant}),
	}, nil
}

// GetTenant reads one tenant.
func (handler *Admin) GetTenant(ctx context.Context, input *getTenantInput) (*getTenantOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	summary, err := handler.tenantSummary(ctx, input.ID)
	if err != nil {
		return nil, err
	}
	return &getTenantOutput{Body: tenantResource(summary)}, nil
}

// ListTenantUsers reads one tenant's accounts.
func (handler *Admin) ListTenantUsers(ctx context.Context, input *listTenantUsersInput) (*listTenantUsersOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	users, err := handler.store.ListUsers(ctx, repository.TenantScope{ID: input.ID})
	if err != nil {
		return nil, serverProblem(ctx, "could not list the tenant's accounts", err)
	}
	out := &listTenantUsersOutput{}
	out.Body.Items = make([]UserResource, 0, len(users))
	for _, user := range users {
		out.Body.Items = append(out.Body.Items, userResource(user))
	}
	return out, nil
}

// CreateTenantUser adds an account that can sign in immediately.
//
// The password is hashed here, at the one boundary that sees it in the clear:
// the store takes a hash and has no parameter that would accept anything else,
// so a password can never be handed to persistence by mistake.
func (handler *Admin) CreateTenantUser(ctx context.Context, input *createTenantUserInput) (*createTenantUserOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	// Confirmed before the insert so a typo in the tenant ID reads as a missing
	// collection rather than as an internal failure: the users table's foreign
	// key would refuse the write, but a driver error is not something an
	// operator can act on.
	if _, err := handler.store.GetTenant(ctx, input.ID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("tenant not found")
		}
		return nil, serverProblem(ctx, "could not read the tenant", err)
	}
	hash, err := auth.HashPassword(input.Body.Password)
	if err != nil {
		return nil, serverProblem(ctx, "could not hash the password", err)
	}
	user, err := handler.store.CreateUser(ctx, repository.TenantScope{ID: input.ID},
		input.Body.Email, input.Body.Name, hash)
	if err != nil {
		// Addresses are unique across the deployment, not per tenant, so the
		// duplicate is reported even when it lives elsewhere: the caller would
		// otherwise create nothing and be told it worked.
		if errors.Is(err, repository.ErrAlreadyExists) {
			return nil, huma.Error409Conflict("an account with that email address already exists")
		}
		return nil, serverProblem(ctx, "could not create the account", err)
	}
	return &createTenantUserOutput{
		Status:   http.StatusCreated,
		Location: handler.tenantUsersPath(user.TenantID),
		Body:     userResource(user),
	}, nil
}

// DisableTenantUser offboards an account without deleting it.
func (handler *Admin) DisableTenantUser(ctx context.Context, input *setUserDisabledInput) (*setUserDisabledOutput, error) {
	return handler.setDisabled(ctx, input, true)
}

// EnableTenantUser restores an offboarded account.
func (handler *Admin) EnableTenantUser(ctx context.Context, input *setUserDisabledInput) (*setUserDisabledOutput, error) {
	return handler.setDisabled(ctx, input, false)
}

// SetTenantUserPassword replaces an account's password.
func (handler *Admin) SetTenantUserPassword(ctx context.Context, input *setUserPasswordInput) (*setUserPasswordOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	hash, err := auth.HashPassword(input.Body.Password)
	if err != nil {
		return nil, serverProblem(ctx, "could not hash the password", err)
	}
	user, err := handler.store.SetUserPassword(ctx,
		repository.TenantScope{ID: input.ID}, input.UserID, hash)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("account not found")
		}
		return nil, serverProblem(ctx, "could not replace the password", err)
	}
	return &setUserPasswordOutput{Body: userResource(user)}, nil
}

// CreateTenantAPIKey mints a key on another tenant's behalf.
func (handler *Admin) CreateTenantAPIKey(ctx context.Context, input *createTenantAPIKeyInput) (*createTenantAPIKeyOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	if _, err := handler.store.GetTenant(ctx, input.ID); err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("tenant not found")
		}
		return nil, serverProblem(ctx, "could not read the tenant", err)
	}
	key, token, err := handler.store.CreateAPIKey(ctx,
		repository.TenantScope{ID: input.ID}, input.Body.Label)
	if err != nil {
		return nil, serverProblem(ctx, "could not create the API key", err)
	}
	return &createTenantAPIKeyOutput{
		Status: http.StatusCreated,
		Body: CreatedAPIKeyResource{
			ID: key.ID, Prefix: key.Prefix, Label: key.Label,
			CreatedAt: key.CreatedAt, Token: token,
		},
	}, nil
}

// setDisabled applies both halves of the offboarding pair.
//
// One implementation for two operations because the only difference is the
// boolean: two near-identical handlers would be two places for the tenant check
// and the refusal to drift apart.
func (handler *Admin) setDisabled(ctx context.Context, input *setUserDisabledInput, disabled bool) (*setUserDisabledOutput, error) {
	if err := operatorOnly(ctx); err != nil {
		return nil, err
	}
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	user, err := handler.store.SetUserDisabled(ctx,
		repository.TenantScope{ID: input.ID}, input.UserID, disabled)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("account not found")
		}
		return nil, serverProblem(ctx, "could not update the account", err)
	}
	return &setUserDisabledOutput{Body: userResource(user)}, nil
}

// tenantSummary reads one tenant together with its account count.
//
// It goes through the listing rather than a GetTenant plus a count of its own:
// the count behind the listing is one grouped query for every tenant, where a
// per-tenant count would either load every account row or want a second store
// method doing the same aggregation for one ID.
func (handler *Admin) tenantSummary(ctx context.Context, id string) (repository.TenantSummary, error) {
	summaries, err := handler.store.ListTenants(ctx)
	if err != nil {
		return repository.TenantSummary{}, serverProblem(ctx, "could not read the tenant", err)
	}
	for _, summary := range summaries {
		if summary.ID == id {
			return summary, nil
		}
	}
	return repository.TenantSummary{}, huma.Error404NotFound("tenant not found")
}

// tenantPath is where a tenant can be read back.
func (handler *Admin) tenantPath(id string) string { return handler.prefix + "/tenants/" + id }

// tenantUsersPath is where a tenant's accounts can be listed.
func (handler *Admin) tenantUsersPath(id string) string { return handler.tenantPath(id) + "/users" }

func tenantResource(summary repository.TenantSummary) TenantResource {
	return TenantResource{
		ID: summary.ID, Name: summary.Name,
		CreatedAt: summary.CreatedAt, UserCount: summary.UserCount,
	}
}

func userResource(user repository.User) UserResource {
	return UserResource{
		ID: user.ID, TenantID: user.TenantID, Email: user.Email, Name: user.Name,
		DisabledAt: user.DisabledAt, CreatedAt: user.CreatedAt,
	}
}
