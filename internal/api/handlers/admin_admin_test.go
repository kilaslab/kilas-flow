package handlers

// Operator-surface HTTP proof: a customer's key, a customer's session, a
// session in the operator tenant, and an embed session all get 403 on every
// admin route without the store being touched; an operator key gets through;
// and the create paths answer conflict/not-found rather than a driver error.

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// adminTestPrefix is where the operator routes are mounted in these tests. It
// is the deployment's own prefix, so the Location headers are checkable rather
// than merely present.
const adminTestPrefix = "/api/v1"

// adminTestStore answers the operator surface without a database, and counts
// every call: a refused request must not have reached persistence at all.
type adminTestStore struct {
	calls int
	// tenants is what GetTenant answers with; ListTenants answers the same set.
	tenants []repository.TenantSummary
	// getTenantErr, when set, is what GetTenant returns.
	getTenantErr error
	// createTenantErr and createUserErr are what the creates return.
	createTenantErr error
	createUserErr   error
	// storedHash records the hash a handler asked to persist, which is how
	// these tests check a password was hashed rather than passed through.
	storedHash string
	user       repository.User
}

func (stub *adminTestStore) EnsureTenant(context.Context, string, string) (repository.Tenant, error) {
	stub.calls++
	return repository.Tenant{}, errors.New("EnsureTenant is not part of this test")
}

func (stub *adminTestStore) CreateTenant(_ context.Context, id, name string) (repository.Tenant, error) {
	stub.calls++
	if stub.createTenantErr != nil {
		return repository.Tenant{}, stub.createTenantErr
	}
	tenant := repository.Tenant{ID: id, Name: name, CreatedAt: time.Now().UTC()}
	stub.tenants = append(stub.tenants, repository.TenantSummary{Tenant: tenant})
	return tenant, nil
}

func (stub *adminTestStore) GetTenant(_ context.Context, id string) (repository.Tenant, error) {
	stub.calls++
	if stub.getTenantErr != nil {
		return repository.Tenant{}, stub.getTenantErr
	}
	for _, summary := range stub.tenants {
		if summary.ID == id {
			return summary.Tenant, nil
		}
	}
	return repository.Tenant{}, repository.ErrNotFound
}

func (stub *adminTestStore) ListTenants(context.Context) ([]repository.TenantSummary, error) {
	stub.calls++
	return stub.tenants, nil
}

func (stub *adminTestStore) CreateUser(_ context.Context, tenant repository.TenantScope, email, name, passwordHash string) (repository.User, error) {
	stub.calls++
	if stub.createUserErr != nil {
		return repository.User{}, stub.createUserErr
	}
	stub.storedHash = passwordHash
	stub.user = repository.User{
		ID: "usr_1", TenantID: tenant.ID, Email: email, Name: name, CreatedAt: time.Now().UTC(),
	}
	return stub.user, nil
}

func (stub *adminTestStore) FindUserForLogin(context.Context, string) (repository.User, error) {
	stub.calls++
	return repository.User{}, errors.New("FindUserForLogin is not part of this test")
}

func (stub *adminTestStore) ListUsers(_ context.Context, tenant repository.TenantScope) ([]repository.User, error) {
	stub.calls++
	if stub.user.TenantID == tenant.ID && stub.user.ID != "" {
		return []repository.User{stub.user}, nil
	}
	return nil, nil
}

func (stub *adminTestStore) SetUserDisabled(_ context.Context, _ repository.TenantScope, _ string, disabled bool) (repository.User, error) {
	stub.calls++
	user := stub.user
	if disabled {
		now := time.Now().UTC()
		user.DisabledAt = &now
	}
	return user, nil
}

func (stub *adminTestStore) SetUserPassword(_ context.Context, _ repository.TenantScope, _, passwordHash string) (repository.User, error) {
	stub.calls++
	stub.storedHash = passwordHash
	return stub.user, nil
}

func (stub *adminTestStore) CountUsers(context.Context) (int64, error) {
	stub.calls++
	return 0, errors.New("CountUsers is not part of this test")
}

func (stub *adminTestStore) CreateAPIKey(_ context.Context, tenant repository.TenantScope, label string) (repository.APIKey, string, error) {
	stub.calls++
	key := repository.APIKey{
		ID: "key_1", TenantID: tenant.ID, Prefix: "00112233aabb",
		Label: label, CreatedAt: time.Now().UTC(),
	}
	return key, "kfa1_" + key.Prefix + "_secret", nil
}

func (stub *adminTestStore) EnsureAPIKey(context.Context, repository.TenantScope, string, string) (repository.APIKey, error) {
	stub.calls++
	return repository.APIKey{}, errors.New("EnsureAPIKey is not part of this test")
}

func (stub *adminTestStore) ListAPIKeys(context.Context, repository.TenantScope) ([]repository.APIKey, error) {
	stub.calls++
	return nil, errors.New("ListAPIKeys is not part of this test")
}

func (stub *adminTestStore) RevokeAPIKey(context.Context, repository.TenantScope, string) (repository.APIKey, error) {
	stub.calls++
	return repository.APIKey{}, errors.New("RevokeAPIKey is not part of this test")
}

func (stub *adminTestStore) AuthenticateAPIKey(context.Context, string) (repository.APIKey, error) {
	stub.calls++
	return repository.APIKey{}, errors.New("AuthenticateAPIKey is not part of this test")
}

// adminOperation is one admin route, wrapped so a test can exercise every one
// of them without an HTTP layer in the way. method, path and body are what the
// route takes over HTTP, so the embed test drives the real paths with requests
// that would otherwise succeed.
type adminOperation struct {
	name   string
	method string
	path   string
	body   string
	call   func(ctx context.Context) error
}

// adminOperations enumerates the whole surface. It is an explicit list rather
// than a reflection walk so that adding an operation without a gate is a
// visible edit here, not a silent gap.
func adminOperations(handler *Admin) []adminOperation {
	return []adminOperation{
		{
			name: "GET /tenants", method: http.MethodGet, path: adminTestPrefix + "/tenants",
			call: func(ctx context.Context) error { _, err := handler.ListTenants(ctx, &struct{}{}); return err },
		},
		{
			name: "POST /tenants", method: http.MethodPost, path: adminTestPrefix + "/tenants",
			body: `{"id":"acme","name":"Acme"}`,
			call: func(ctx context.Context) error {
				_, err := handler.CreateTenant(ctx, &createTenantInput{})
				return err
			},
		},
		{
			name: "GET /tenants/{id}", method: http.MethodGet, path: adminTestPrefix + "/tenants/acme",
			call: func(ctx context.Context) error {
				_, err := handler.GetTenant(ctx, &getTenantInput{ID: "acme"})
				return err
			},
		},
		{
			name: "GET /tenants/{id}/users", method: http.MethodGet,
			path: adminTestPrefix + "/tenants/acme/users",
			call: func(ctx context.Context) error {
				_, err := handler.ListTenantUsers(ctx, &listTenantUsersInput{ID: "acme"})
				return err
			},
		},
		{
			name: "POST /tenants/{id}/users", method: http.MethodPost,
			path: adminTestPrefix + "/tenants/acme/users",
			body: `{"email":"owner@acme.example","name":"Acme Owner","password":"acme-password"}`,
			call: func(ctx context.Context) error {
				_, err := handler.CreateTenantUser(ctx, &createTenantUserInput{ID: "acme"})
				return err
			},
		},
		{
			name: "POST /tenants/{id}/users/{userId}/disable", method: http.MethodPost,
			path: adminTestPrefix + "/tenants/acme/users/usr_1/disable",
			body: `{}`,
			call: func(ctx context.Context) error {
				_, err := handler.DisableTenantUser(ctx, &setUserDisabledInput{ID: "acme", UserID: "usr_1"})
				return err
			},
		},
		{
			name: "POST /tenants/{id}/users/{userId}/enable", method: http.MethodPost,
			path: adminTestPrefix + "/tenants/acme/users/usr_1/enable",
			body: `{}`,
			call: func(ctx context.Context) error {
				_, err := handler.EnableTenantUser(ctx, &setUserDisabledInput{ID: "acme", UserID: "usr_1"})
				return err
			},
		},
		{
			name: "POST /tenants/{id}/users/{userId}/password", method: http.MethodPost,
			path: adminTestPrefix + "/tenants/acme/users/usr_1/password",
			body: `{"password":"reset-password"}`,
			call: func(ctx context.Context) error {
				_, err := handler.SetTenantUserPassword(ctx, &setUserPasswordInput{ID: "acme", UserID: "usr_1"})
				return err
			},
		},
		{
			name: "POST /tenants/{id}/api-keys", method: http.MethodPost,
			path: adminTestPrefix + "/tenants/acme/api-keys",
			body: `{"label":"reference host"}`,
			call: func(ctx context.Context) error {
				_, err := handler.CreateTenantAPIKey(ctx, &createTenantAPIKeyInput{ID: "acme"})
				return err
			},
		},
	}
}

// adminOperatorContext is the principal an operator key authenticates as.
func adminOperatorContext() context.Context {
	return auth.WithPrincipal(context.Background(), auth.Principal{
		TenantID: repository.OperatorTenantID, KeyID: "key_operator",
		Kind: auth.KindAPIKey, Label: "operator",
	})
}

// statusOf reports the HTTP status a handler error carries.
func statusOf(t *testing.T, err error) int {
	t.Helper()
	var model *huma.ErrorModel
	if !errors.As(err, &model) {
		t.Fatalf("error = %v (%T), want a huma error carrying a status", err, err)
	}
	return model.Status
}

func TestAdminRoutesRefuseANonOperatorPrincipal(t *testing.T) {
	principals := []struct {
		name  string
		ctx   context.Context
		store *adminTestStore
	}{
		{
			name: "a customer's API key",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				TenantID: "acme", KeyID: "key_customer", Kind: auth.KindAPIKey,
			}),
		},
		{
			name: "a customer's signed-in session",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				TenantID: "acme", UserID: "usr_1", Kind: auth.KindSession,
			}),
		},
		{
			// Operator authority comes from the boot path's key and nothing
			// else, so a browser login inside the operator tenant is refused
			// rather than being a second, unadvertised way in.
			name: "a session inside the operator tenant",
			ctx: auth.WithPrincipal(context.Background(), auth.Principal{
				TenantID: repository.OperatorTenantID, UserID: "usr_op", Kind: auth.KindSession,
			}),
		},
		{
			// With authentication off there is no principal at all. Reading
			// that as the operator would open this surface on exactly the
			// installs least prepared for it.
			name: "no identity",
			ctx:  context.Background(),
		},
	}

	for _, principal := range principals {
		principal.store = &adminTestStore{}
		operations := adminOperations(NewAdmin(principal.store, adminTestPrefix))
		if len(operations) == 0 {
			t.Fatal("no operations to exercise")
		}
		for _, operation := range operations {
			err := operation.call(principal.ctx)
			if err == nil {
				t.Errorf("%s as %s succeeded, want 403", operation.name, principal.name)
				continue
			}
			if status := statusOf(t, err); status != http.StatusForbidden {
				t.Errorf("%s as %s = %d, want 403", operation.name, principal.name, status)
			}
			if principal.store.calls != 0 {
				t.Errorf("%s as %s reached the store %d time(s)",
					operation.name, principal.name, principal.store.calls)
			}
		}
	}
}

// adminTestStack mounts the operator routes behind the embed middleware the way
// the request path does, so the refusal is proved where a deployment makes it.
//
// Every request is given an operator principal before the embed layer sees it,
// which is what makes the refusal attributable: with that identity the handler
// would have answered normally, so a 403 that never touched the store can only
// have come from permits().
func adminTestStack(t *testing.T, store repository.AuthRepository) (http.Handler, string) {
	t.Helper()
	issuer, err := embed.NewIssuer([]byte("0123456789abcdef0123456789abcdef"),
		[]string{"https://host.example"}, nil)
	if err != nil {
		t.Fatalf("embed.NewIssuer() error = %v", err)
	}
	_, token, err := issuer.Issue(embed.Request{
		TenantID: "acme", WorkflowID: "wf_1",
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun},
		Origin: "https://host.example",
	})
	if err != nil {
		t.Fatalf("embed Issue() error = %v", err)
	}

	router := chi.NewMux()
	router.Use(func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			next.ServeHTTP(w, r.WithContext(auth.WithPrincipal(r.Context(), auth.Principal{
				TenantID: repository.OperatorTenantID, KeyID: "key_operator",
				Kind: auth.KindAPIKey, Label: "operator",
			})))
		})
	})
	router.Use(middleware.EmbedAuth(issuer))
	api := humachi.New(router, huma.DefaultConfig("KilasFlow API", "0.0.0-test"))
	NewAdmin(store, adminTestPrefix).Register(huma.NewGroup(api, adminTestPrefix))
	return router, token
}

// adminServe issues one request against the mounted stack.
func adminServe(stack http.Handler, operation adminOperation, token string) int {
	request := httptest.NewRequest(operation.method, operation.path, strings.NewReader(operation.body))
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("Origin", "https://host.example")
	if token != "" {
		request.Header.Set("X-KilasFlow-Embed", token)
	}
	recorder := httptest.NewRecorder()
	stack.ServeHTTP(recorder, request)
	return recorder.Code
}

func TestAdminRoutesRefuseEmbedSessionsBeforeTheHandlerRuns(t *testing.T) {
	store := &adminTestStore{}
	stack, token := adminTestStack(t, store)

	for _, operation := range adminOperations(NewAdmin(store, adminTestPrefix)) {
		// Control first: without the embed token the same request reaches the
		// handler and touches the store, so the 403 below is the embed layer's
		// rather than a missing route, a validation failure, or this package's
		// own gate.
		before := store.calls
		if code := adminServe(stack, operation, ""); code == http.StatusForbidden {
			t.Errorf("%s without an embed token = %d, want it to reach the handler",
				operation.name, code)
		}
		if store.calls == before {
			t.Errorf("%s without an embed token never reached the store", operation.name)
		}

		// The embed session holds every workflow scope, so the only thing that
		// can refuse it is the default deny in permits(): these paths name no
		// workflow, no datastore and no execution, so no scope reaches them.
		before = store.calls
		if code := adminServe(stack, operation, token); code != http.StatusForbidden {
			t.Errorf("%s with an embed session = %d, want 403", operation.name, code)
		}
		if store.calls != before {
			t.Errorf("%s with an embed session reached the store", operation.name)
		}
	}
}

func TestAdminOperatorProvisionsATenantUserAndKey(t *testing.T) {
	store := &adminTestStore{}
	handler := NewAdmin(store, adminTestPrefix)
	ctx := adminOperatorContext()

	tenantInput := &createTenantInput{}
	tenantInput.Body.ID = "acme"
	tenantInput.Body.Name = "Acme"
	created, err := handler.CreateTenant(ctx, tenantInput)
	if err != nil {
		t.Fatalf("CreateTenant() error = %v", err)
	}
	if created.Status != http.StatusCreated {
		t.Errorf("CreateTenant() status = %d, want 201", created.Status)
	}
	if created.Body.ID != "acme" || created.Body.UserCount != 0 {
		t.Errorf("CreateTenant() = %#v, want the new tenant with no accounts", created.Body)
	}
	// The Location has to name the route the tenant can be read back from,
	// which is why the handler is told the API prefix rather than guessing it.
	if created.Location != adminTestPrefix+"/tenants/acme" {
		t.Errorf("Location = %q, want %q", created.Location, adminTestPrefix+"/tenants/acme")
	}

	userInput := &createTenantUserInput{ID: "acme"}
	userInput.Body.Email = "owner@acme.example"
	userInput.Body.Name = "Acme Owner"
	userInput.Body.Password = "acme-password"
	user, err := handler.CreateTenantUser(ctx, userInput)
	if err != nil {
		t.Fatalf("CreateTenantUser() error = %v", err)
	}
	if user.Status != http.StatusCreated {
		t.Errorf("CreateTenantUser() status = %d, want 201", user.Status)
	}
	if user.Body.Email != "owner@acme.example" || user.Body.TenantID != "acme" {
		t.Errorf("CreateTenantUser() = %#v, want the account in acme", user.Body)
	}
	if user.Location != adminTestPrefix+"/tenants/acme/users" {
		t.Errorf("Location = %q, want the account listing", user.Location)
	}
	// What reached persistence is a hash of the password the caller sent, not
	// the password: this is the half of the login path the handler owns.
	if store.storedHash == "" || store.storedHash == "acme-password" {
		t.Fatalf("stored password value = %q, want a hash", store.storedHash)
	}
	if !auth.MatchPassword("acme-password", store.storedHash) {
		t.Error("the stored hash does not verify the password the account was created with")
	}

	keyInput := &createTenantAPIKeyInput{ID: "acme"}
	keyInput.Body.Label = "reference host"
	key, err := handler.CreateTenantAPIKey(ctx, keyInput)
	if err != nil {
		t.Fatalf("CreateTenantAPIKey() error = %v", err)
	}
	if key.Status != http.StatusCreated {
		t.Errorf("CreateTenantAPIKey() status = %d, want 201", key.Status)
	}
	// The secret exists in this one response and nowhere else.
	if !strings.HasPrefix(key.Body.Token, "kfa1_") {
		t.Errorf("minted token = %q, want a full API key", key.Body.Token)
	}
}

func TestAdminCreateRefusalsAreStatusesNotDriverErrors(t *testing.T) {
	ctx := adminOperatorContext()
	tenantInput := &createTenantInput{}
	tenantInput.Body.ID = "acme"
	tenantInput.Body.Name = "Acme"
	userInput := &createTenantUserInput{ID: "acme"}
	userInput.Body.Email = "owner@acme.example"
	userInput.Body.Password = "acme-password"
	keyInput := &createTenantAPIKeyInput{ID: "acme"}

	duplicate := &adminTestStore{createTenantErr: fmt.Errorf("%w: tenant acme", repository.ErrAlreadyExists)}
	if _, err := NewAdmin(duplicate, adminTestPrefix).CreateTenant(ctx, tenantInput); err == nil {
		t.Error("CreateTenant() accepted a tenant that already exists")
	} else if status := statusOf(t, err); status != http.StatusConflict {
		t.Errorf("CreateTenant() duplicate = %d, want 409", status)
	}

	duplicateUser := &adminTestStore{
		createUserErr: fmt.Errorf("%w: account owner@acme.example", repository.ErrAlreadyExists),
	}
	duplicateUser.tenants = []repository.TenantSummary{{Tenant: repository.Tenant{ID: "acme", Name: "Acme"}}}
	handler := NewAdmin(duplicateUser, adminTestPrefix)
	if _, err := handler.CreateTenantUser(ctx, userInput); err == nil {
		t.Error("CreateTenantUser() accepted an address that already has an account")
	} else if status := statusOf(t, err); status != http.StatusConflict {
		t.Errorf("CreateTenantUser() duplicate = %d, want 409", status)
	}

	// A tenant that does not exist is a missing collection, not an internal
	// failure: the foreign key would refuse the write, but a driver error is
	// not something an operator can act on.
	missing := &adminTestStore{getTenantErr: repository.ErrNotFound}
	missingHandler := NewAdmin(missing, adminTestPrefix)
	if _, err := missingHandler.CreateTenantUser(ctx, userInput); err == nil {
		t.Error("CreateTenantUser() succeeded under a tenant that does not exist")
	} else if status := statusOf(t, err); status != http.StatusNotFound {
		t.Errorf("CreateTenantUser() in a missing tenant = %d, want 404", status)
	}
	if _, err := missingHandler.CreateTenantAPIKey(ctx, keyInput); err == nil {
		t.Error("CreateTenantAPIKey() succeeded for a tenant that does not exist")
	} else if status := statusOf(t, err); status != http.StatusNotFound {
		t.Errorf("CreateTenantAPIKey() in a missing tenant = %d, want 404", status)
	}
	if _, err := missingHandler.GetTenant(ctx, &getTenantInput{ID: "acme"}); err == nil {
		t.Error("GetTenant() found a tenant that does not exist")
	} else if status := statusOf(t, err); status != http.StatusNotFound {
		t.Errorf("GetTenant() missing = %d, want 404", status)
	}
}

func TestAdminDisablingAndReEnablingAUser(t *testing.T) {
	store := &adminTestStore{user: repository.User{
		ID: "usr_1", TenantID: "acme", Email: "owner@acme.example",
		CreatedAt: time.Now().UTC(),
	}}
	handler := NewAdmin(store, adminTestPrefix)
	ctx := adminOperatorContext()

	disabled, err := handler.DisableTenantUser(ctx, &setUserDisabledInput{ID: "acme", UserID: "usr_1"})
	if err != nil {
		t.Fatalf("DisableTenantUser() error = %v", err)
	}
	if disabled.Body.DisabledAt == nil {
		t.Error("DisableTenantUser() reported the account as enabled")
	}

	enabled, err := handler.EnableTenantUser(ctx, &setUserDisabledInput{ID: "acme", UserID: "usr_1"})
	if err != nil {
		t.Fatalf("EnableTenantUser() error = %v", err)
	}
	if enabled.Body.DisabledAt != nil {
		t.Errorf("EnableTenantUser() left DisabledAt = %v", enabled.Body.DisabledAt)
	}
}

func TestAdminReplacingAPasswordStoresAHash(t *testing.T) {
	store := &adminTestStore{}
	handler := NewAdmin(store, adminTestPrefix)

	input := &setUserPasswordInput{ID: "acme", UserID: "usr_1"}
	input.Body.Password = "reset-password"
	if _, err := handler.SetTenantUserPassword(adminOperatorContext(), input); err != nil {
		t.Fatalf("SetTenantUserPassword() error = %v", err)
	}
	if store.storedHash == "reset-password" || store.storedHash == "" {
		t.Fatalf("stored password value = %q, want a hash", store.storedHash)
	}
	if !auth.MatchPassword("reset-password", store.storedHash) {
		t.Error("the reset did not store a hash of the new password")
	}
}
