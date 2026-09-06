package handlers

import (
	"context"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// CredentialTypeResource describes one credential type so the editor can
// render its form without a per-type implementation.
type CredentialTypeResource struct {
	ID          string              `json:"id"`
	DisplayName string              `json:"displayName"`
	Description string              `json:"description,omitempty"`
	Fields      []credentials.Field `json:"fields"`
}

// CredentialResource is one stored credential.
//
// Fields carries non-secret values as stored and a set/unset marker for secret
// ones. A secret is never returned after it is written, not even to the client
// that wrote it.
type CredentialResource struct {
	ID             string            `json:"id"`
	Name           string            `json:"name"`
	Type           string            `json:"type"`
	Fields         map[string]string `json:"fields"`
	AllowedDomains []string          `json:"allowedDomains"`
	CreatedAt      time.Time         `json:"createdAt"`
	UpdatedAt      time.Time         `json:"updatedAt"`
}

// Credentials is the REST surface over encrypted credential storage.
type Credentials struct {
	store   repository.CredentialRepository
	tenants TenantResolver
	// policy is the instance egress policy a credential test runs under. A
	// probe that bypassed it would be a credential-shaped hole into the
	// internal network.
	policy safehttp.Policy
	// guard is the same one the database executors receive, so a SQLite
	// credential naming KilasFlow's own database is refused here too. Testing
	// through a laxer guard than the one that will run the workflow would
	// report reachable for a path a node then refuses.
	guard sqlnode.Guard
	// timeout bounds one test end to end. A target that accepts a connection
	// and never answers otherwise holds the request for the server's window.
	timeout time.Duration
	// inFlight holds the tests currently running, keyed by tenant and subject,
	// so a client cannot fan a loop of concurrent probes out of one instance.
	inFlight sync.Map
}

// WithHTTPPolicy sets the egress policy credential tests run under.
func (handler *Credentials) WithHTTPPolicy(policy safehttp.Policy) *Credentials {
	handler.policy = policy
	return handler
}

// WithDatabaseGuard sets the guard a database credential test opens under.
func (handler *Credentials) WithDatabaseGuard(guard sqlnode.Guard) *Credentials {
	handler.guard = guard
	return handler
}

// WithTestTimeout bounds one credential test.
func (handler *Credentials) WithTestTimeout(timeout time.Duration) *Credentials {
	handler.timeout = timeout
	return handler
}

// NewCredentials constructs the credential handler.
func NewCredentials(store repository.CredentialRepository, tenants TenantResolver) *Credentials {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Credentials{store: store, tenants: tenants}
}

type credentialBody struct {
	Name           string            `json:"name" minLength:"1" doc:"Display name"`
	Type           string            `json:"type,omitempty" doc:"Credential type ID; immutable after creation"`
	Fields         map[string]string `json:"fields" doc:"Field values for the credential type. Send the redaction placeholder to keep a stored secret."`
	AllowedDomains []string          `json:"allowedDomains,omitempty" doc:"Hosts this credential may be sent to. Empty means unrestricted."`
}

type createCredentialInput struct {
	Body credentialBody
}

type credentialPathInput struct {
	ID string `path:"id" minLength:"1" doc:"Credential identifier"`
}

type updateCredentialInput struct {
	ID   string `path:"id" minLength:"1" doc:"Credential identifier"`
	Body credentialBody
}

type credentialOutput struct {
	Body CredentialResource
}

type createdCredentialOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     CredentialResource
}

type credentialListOutput struct {
	Body []CredentialResource
}

type credentialTypeListOutput struct {
	Body []CredentialTypeResource
}

type deletedCredentialOutput struct {
	Status int `status:"204"`
}

// Register wires credential types and CRUD.
func (handler *Credentials) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-credential-types", Method: http.MethodGet, Path: "/credential-types",
		Summary: "List credential types", Description: "Returns the server-defined credential types and their editor field metadata.", Tags: []string{"Credentials"},
	}, handler.ListTypes)
	huma.Register(api, huma.Operation{
		OperationID: "list-credentials", Method: http.MethodGet, Path: "/credentials",
		Summary: "List credentials", Description: "Returns stored credentials without any secret value.", Tags: []string{"Credentials"},
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "create-credential", Method: http.MethodPost, Path: "/credentials", DefaultStatus: http.StatusCreated,
		Summary: "Create a credential", Description: "Stores an encrypted credential payload.", Tags: []string{"Credentials"},
	}, handler.Create)
	huma.Register(api, huma.Operation{
		OperationID: "get-credential", Method: http.MethodGet, Path: "/credentials/{id}",
		Summary: "Get a credential", Description: "Returns one credential without any secret value.", Tags: []string{"Credentials"},
	}, handler.Get)
	huma.Register(api, huma.Operation{
		OperationID: "update-credential", Method: http.MethodPut, Path: "/credentials/{id}",
		Summary: "Update a credential", Description: "Replaces name, scope, and any field sent with a new value.", Tags: []string{"Credentials"},
	}, handler.Update)
	huma.Register(api, huma.Operation{
		OperationID: "test-credential", Method: http.MethodPost, Path: "/credentials/{id}/test",
		Summary:     "Test a credential",
		Description: "Runs the credential type's declared probe and reports pass or fail. No secret and no remote response body is returned.",
		Tags:        []string{"Credentials"},
	}, handler.Test)
	huma.Register(api, huma.Operation{
		OperationID: "test-credential-payload", Method: http.MethodPost, Path: "/credential-types/{type}/test",
		Summary: "Test an unsaved credential",
		Description: "Runs a credential type's probe against a payload that has not been saved. " +
			"Send credentialId alongside the redaction placeholder to test an edit against stored secrets.",
		Tags: []string{"Credentials"},
	}, handler.TestPayload)
	huma.Register(api, huma.Operation{
		OperationID: "delete-credential", Method: http.MethodDelete, Path: "/credentials/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a credential", Description: "Permanently removes a stored credential.", Tags: []string{"Credentials"},
	}, handler.Delete)
}

// ListTypes returns the credential catalogue.
func (handler *Credentials) ListTypes(context.Context, *struct{}) (*credentialTypeListOutput, error) {
	definitions := credentials.List()
	resources := make([]CredentialTypeResource, 0, len(definitions))
	for _, definition := range definitions {
		resources = append(resources, CredentialTypeResource{
			ID: definition.ID, DisplayName: definition.DisplayName,
			Description: definition.Description, Fields: definition.Fields,
		})
	}
	return &credentialTypeListOutput{Body: resources}, nil
}

// List returns every credential in the tenant.
func (handler *Credentials) List(ctx context.Context, _ *struct{}) (*credentialListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	records, err := handler.store.List(ctx, handler.tenants.Resolve(ctx))
	if err != nil {
		return nil, handler.problem(err)
	}
	resources := make([]CredentialResource, 0, len(records))
	for _, record := range records {
		resources = append(resources, credentialResource(record))
	}
	return &credentialListOutput{Body: resources}, nil
}

// rejectSQLiteScope refuses a host scope on a credential that names a file.
// A SQLite credential has no host for AllowedDomains to match, so a stored
// scope would be a promise nothing keeps: sqlnode refuses the connection at
// open, and saving it at all is the defect. Blank entries are ignored the
// way the store's own normalization ignores them.
func rejectSQLiteScope(credentialType string, domains []string) error {
	driver, isDatabase := sqlnode.DriverForCredential(credentialType)
	if !isDatabase || driver != sqlnode.DriverSQLite {
		return nil
	}
	for _, domain := range domains {
		if strings.TrimSpace(domain) != "" {
			return huma.Error422UnprocessableEntity("a sqlite credential names a file, not a host: remove allowed domains to save it")
		}
	}
	return nil
}

// Create stores a new encrypted credential.
func (handler *Credentials) Create(ctx context.Context, input *createCredentialInput) (*createdCredentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	if err := rejectSQLiteScope(input.Body.Type, input.Body.AllowedDomains); err != nil {
		return nil, err
	}
	record, err := handler.store.Create(ctx, handler.tenants.Resolve(ctx), credentials.Record{
		Name: input.Body.Name, Type: input.Body.Type,
		Fields: input.Body.Fields, AllowedDomains: input.Body.AllowedDomains,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	return &createdCredentialOutput{
		Status: http.StatusCreated, Location: "/api/v1/credentials/" + record.ID,
		Body: credentialResource(record),
	}, nil
}

// Get returns one credential's metadata.
func (handler *Credentials) Get(ctx context.Context, input *credentialPathInput) (*credentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	record, err := handler.store.Get(ctx, handler.tenants.Resolve(ctx), input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}
	return &credentialOutput{Body: credentialResource(record)}, nil
}

// Update replaces a credential's name, scope, and supplied field values.
func (handler *Credentials) Update(ctx context.Context, input *updateCredentialInput) (*credentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	credentialType := input.Body.Type
	if credentialType == "" && len(input.Body.AllowedDomains) > 0 {
		// The type is immutable after creation, so an update may omit it.
		// The stored type decides: a scope added to a SQLite credential
		// through a typeless update is the same defect as one set at create.
		stored, err := handler.store.Get(ctx, handler.tenants.Resolve(ctx), input.ID)
		if err != nil {
			return nil, handler.problem(err)
		}
		credentialType = stored.Type
	}
	if err := rejectSQLiteScope(credentialType, input.Body.AllowedDomains); err != nil {
		return nil, err
	}
	record, err := handler.store.Update(ctx, handler.tenants.Resolve(ctx), input.ID, credentials.Record{
		Name: input.Body.Name, Type: input.Body.Type,
		Fields: input.Body.Fields, AllowedDomains: input.Body.AllowedDomains,
	})
	if err != nil {
		return nil, handler.problem(err)
	}
	return &credentialOutput{Body: credentialResource(record)}, nil
}

// Delete removes a credential.
func (handler *Credentials) Delete(ctx context.Context, input *credentialPathInput) (*deletedCredentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	if err := handler.store.Delete(ctx, handler.tenants.Resolve(ctx), input.ID); err != nil {
		return nil, handler.problem(err)
	}
	return &deletedCredentialOutput{Status: http.StatusNoContent}, nil
}

func (handler *Credentials) problem(err error) error {
	if errors.Is(err, repository.ErrNotFound) {
		return huma.Error404NotFound("credential not found")
	}
	// Validation failures here are about the submitted payload — an unknown
	// type, a missing required field — so they belong to the client.
	return huma.Error422UnprocessableEntity(err.Error())
}

func credentialResource(record credentials.Record) CredentialResource {
	domains := record.AllowedDomains
	if domains == nil {
		domains = []string{}
	}
	return CredentialResource{
		ID: record.ID, Name: record.Name, Type: record.Type,
		Fields:         credentials.Redacted(record.Type, record.Fields),
		AllowedDomains: domains,
		CreatedAt:      record.CreatedAt, UpdatedAt: record.UpdatedAt,
	}
}

// testCredentialInput identifies the credential to probe.
type testCredentialInput struct {
	ID string `path:"id"`
}

// testPayloadInput carries an unsaved credential to probe.
type testPayloadInput struct {
	Type string `path:"type" minLength:"1" doc:"Credential type ID"`
	Body testPayloadBody
}

type testPayloadBody struct {
	Fields map[string]string `json:"fields" doc:"Field values to test. Send the redaction placeholder to use a stored secret."`
	// CredentialID names the stored credential a redacted field is taken from.
	// Without it a placeholder has nothing to resolve against, and the test
	// would authenticate with eight bullet characters.
	CredentialID   string   `json:"credentialId,omitempty" doc:"Stored credential the redaction placeholder resolves against"`
	AllowedDomains []string `json:"allowedDomains,omitempty" doc:"Hosts this credential may be sent to. Empty means unrestricted."`
}

// TestCredentialResource reports whether a credential actually works.
//
// It carries no secret and no remote body: a probe that echoed the response
// would be a way to read whatever the credential can reach.
type TestCredentialResource struct {
	OK bool `json:"ok"`
	// Detail explains a failure in terms the user can act on.
	Detail string `json:"detail,omitempty"`
	// ResolvedFromStorage names the fields whose value came from the stored
	// credential rather than the submitted payload.
	//
	// Without it, "it works" is ambiguous in the one case that matters: an edit
	// that changed the host and left the password as the placeholder was tested
	// against a password the user cannot see and did not send.
	ResolvedFromStorage []string `json:"resolvedFromStorage,omitempty"`
	// Untestable marks a type this server has no probe for, as against a probe
	// that ran and failed. The two are different answers and a client showing
	// a red cross for both would be lying about one of them.
	Untestable bool `json:"untestable,omitempty"`
}

type testCredentialOutput struct {
	Body TestCredentialResource
}

// Test runs the credential type's declared probe.
//
// It goes through internal/safehttp under the credential's own AllowedDomains,
// exactly as an HTTP node would. A probe that bypassed the egress policy would
// be a credential-shaped hole straight into the internal network: anyone able
// to store a credential could point its probe at a metadata endpoint and read
// the result through the pass/fail signal.
func (handler *Credentials) Test(ctx context.Context, input *testCredentialInput) (*testCredentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	tenant := handler.tenants.Resolve(ctx)
	record, fields, err := handler.store.Resolve(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(err)
	}

	release, err := handler.claim(tenant, input.ID)
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := handler.bounded(ctx)
	defer cancel()
	return handler.probe(ctx, record, fields, nil), nil
}

// TestPayload probes a credential that has not been saved.
//
// Split from Test because it takes a different input and a different authority:
// one names something already stored, the other carries a body whose secrets
// may not exist yet. A single endpoint doing both would have to guess which it
// was given.
func (handler *Credentials) TestPayload(ctx context.Context, input *testPayloadInput) (*testCredentialOutput, error) {
	if _, known := credentials.Default().Get(input.Type); !known {
		// 422 rather than the untestable verdict Test returns for the same
		// thing: there, the type came from a stored record and the client is
		// being told about its data; here it came from the URL, and an unknown
		// type in the request is the request being wrong.
		return nil, huma.Error422UnprocessableEntity("credential type " + input.Type + " is not registered on this server")
	}

	tenant := handler.tenants.Resolve(ctx)
	fields, fromStorage, err := handler.mergeStoredSecrets(ctx, tenant, input)
	if err != nil {
		return nil, err
	}
	if err := credentials.Validate(input.Type, fields); err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	subject := input.CredentialIDOrType()
	release, err := handler.claim(tenant, subject)
	if err != nil {
		return nil, err
	}
	defer release()

	ctx, cancel := handler.bounded(ctx)
	defer cancel()
	record := credentials.Record{
		ID: input.Body.CredentialID, Type: input.Type,
		Fields: fields, AllowedDomains: input.Body.AllowedDomains,
	}
	return handler.probe(ctx, record, fields, fromStorage), nil
}

// CredentialIDOrType names what a payload test is a test of, for the in-flight
// claim.
//
// An edit of a stored credential is that credential; a brand new one has no
// identity yet and is only ever its type, so two people in one tenant creating
// two different PostgreSQL credentials at the same moment will make each other
// wait. That is the deliberate trade: the unsaved route is the one where the
// caller supplies the host, which makes it the more useful of the two to fan
// out, and a spurious wait is a smaller cost than an unbounded probe.
func (input *testPayloadInput) CredentialIDOrType() string {
	if input.Body.CredentialID != "" {
		return input.Body.CredentialID
	}
	return "type:" + input.Type
}

// mergeStoredSecrets replaces every redaction placeholder with its stored value.
//
// Field by field, the way Update already merges them. Passing the submitted
// body straight through would authenticate with eight bullet characters and
// report a password failure for a credential that is perfectly good; falling
// back to the whole stored record whenever anything is redacted is worse, and
// reports success for an edit that was never tested.
func (handler *Credentials) mergeStoredSecrets(ctx context.Context, tenant repository.TenantScope, input *testPayloadInput) (map[string]string, []string, error) {
	fields := make(map[string]string, len(input.Body.Fields))
	redacted := make([]string, 0, len(input.Body.Fields))
	for key, value := range input.Body.Fields {
		if value == credentials.RedactedValue {
			redacted = append(redacted, key)
			continue
		}
		fields[key] = value
	}
	if len(redacted) == 0 {
		return fields, nil, nil
	}
	if input.Body.CredentialID == "" {
		return nil, nil, huma.Error422UnprocessableEntity(
			"a redacted field needs credentialId, naming the stored credential its value comes from")
	}
	if handler.store == nil {
		return nil, nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	stored, storedFields, err := handler.store.Resolve(ctx, tenant, input.Body.CredentialID)
	if err != nil {
		return nil, nil, handler.problem(err)
	}
	if stored.Type != input.Type {
		return nil, nil, huma.Error422UnprocessableEntity(
			"credential " + input.Body.CredentialID + " is a " + stored.Type + " credential, not " + input.Type)
	}
	sort.Strings(redacted)
	resolved := make([]string, 0, len(redacted))
	for _, key := range redacted {
		value, found := storedFields[key]
		if !found {
			// Nothing stored under that name: leaving it absent lets Validate
			// report a missing required field, which is the true diagnosis.
			continue
		}
		fields[key] = value
		resolved = append(resolved, key)
	}
	return fields, resolved, nil
}

// probe runs whichever kind of test the credential type declares.
func (handler *Credentials) probe(ctx context.Context, record credentials.Record, fields map[string]string, fromStorage []string) *testCredentialOutput {
	answer := func(resource TestCredentialResource) *testCredentialOutput {
		resource.ResolvedFromStorage = fromStorage
		return &testCredentialOutput{Body: resource}
	}

	credentialType, known := credentials.Default().Get(record.Type)
	if !known {
		return answer(TestCredentialResource{
			Untestable: true,
			Detail:     "this credential's type is not registered on this server",
		})
	}

	// A database credential is tested by connecting, not by an HTTP request:
	// there is no URL to fetch, and the driver's own handshake is what a node
	// would do. sqlnode owns the mapping so this package never assembles a DSN.
	if driver, isDatabase := sqlnode.DriverForCredential(record.Type); isDatabase {
		// The same guard the executors receive, narrowed to this credential's
		// own scope: a probe held to a laxer guard would report reachable
		// for a target a node then refuses.
		guard := handler.guard
		guard.AllowedDomains = record.AllowedDomains
		if err := sqlnode.Test(ctx, driver, fields, guard); err != nil {
			return answer(TestCredentialResource{Detail: err.Error()})
		}
		return answer(TestCredentialResource{OK: true, Detail: "the database accepted the connection"})
	}

	if credentialType.Test == nil {
		return answer(TestCredentialResource{
			Untestable: true,
			Detail:     "this credential type has no test defined",
		})
	}
	detail, err := credentials.RunTest(ctx, credentialType, record, fields, handler.policy)
	if err != nil {
		// The message is the probe's own diagnosis, never the remote body.
		return answer(TestCredentialResource{Detail: err.Error()})
	}
	return answer(TestCredentialResource{OK: true, Detail: detail})
}

// bounded gives one test its own deadline.
//
// The server's window is far longer, and a target that accepts a connection and
// then says nothing would hold the request — and the worker behind it — for all
// of it. A person is waiting on this answer.
func (handler *Credentials) bounded(ctx context.Context) (context.Context, context.CancelFunc) {
	timeout := handler.timeout
	if timeout <= 0 {
		timeout = defaultCredentialTestTimeout
	}
	return context.WithTimeout(ctx, timeout)
}

// claim admits one test at a time per credential.
//
// Not a rate limit: sequential tests are the normal way to fix a credential.
// What this stops is fan-out — without it, anyone who can reach the API can aim
// a few hundred concurrent probes at a network from a single stored credential
// and read the results off the timing.
func (handler *Credentials) claim(tenant repository.TenantScope, subject string) (func(), error) {
	key := tenant.ID + "\x00" + subject
	if _, running := handler.inFlight.LoadOrStore(key, struct{}{}); running {
		return nil, huma.Error409Conflict("a test of this credential is already running")
	}
	return func() { handler.inFlight.Delete(key) }, nil
}

// defaultCredentialTestTimeout bounds a test when nothing is configured.
const defaultCredentialTestTimeout = 10 * time.Second
