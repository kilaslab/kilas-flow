package handlers

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/repository"
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

// Create stores a new encrypted credential.
func (handler *Credentials) Create(ctx context.Context, input *createCredentialInput) (*createdCredentialOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
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
