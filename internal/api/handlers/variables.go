package handlers

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/repository"
)

// VariableResource is one of the tenant's variables, read by every workflow as
// `$vars.KEY`.
type VariableResource struct {
	ID        string    `json:"id"`
	Key       string    `json:"key" doc:"The name a workflow reads, as $vars.KEY."`
	Value     string    `json:"value" doc:"Plain text, stored unencrypted. Never put a secret here: use a credential."`
	CreatedAt time.Time `json:"createdAt"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Variables serves the tenant's variables: n8n's Settings → Variables.
type Variables struct {
	store   repository.VariableRepository
	tenants TenantResolver
}

// NewVariables builds the variables handler. A nil store answers 503.
func NewVariables(store repository.VariableRepository, tenants TenantResolver) *Variables {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Variables{store: store, tenants: tenants}
}

type variableBody struct {
	Key   string `json:"key" minLength:"1" maxLength:"64" pattern:"^[A-Za-z_][A-Za-z0-9_]*$" doc:"Letters, digits and underscores, starting with a letter or underscore, so it reads as $vars.KEY."`
	Value string `json:"value" maxLength:"4096" doc:"Plain text, at most 4096 bytes. Stored unencrypted: a secret belongs in a credential."`
}

type createVariableInput struct{ Body variableBody }

type variablePathInput struct {
	ID string `path:"id" minLength:"1" doc:"Variable identifier"`
}

type updateVariableInput struct {
	ID   string `path:"id" minLength:"1" doc:"Variable identifier"`
	Body variableBody
}

type variableOutput struct{ Body VariableResource }

type createdVariableOutput struct {
	Status   int    `status:"201"`
	Location string `header:"Location"`
	Body     VariableResource
}

type variableListOutput struct{ Body []VariableResource }

type deletedVariableOutput struct {
	Status int `status:"204"`
}

// Register mounts the variable operations.
func (handler *Variables) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "list-variables", Method: http.MethodGet, Path: "/variables",
		Summary: "List variables", Tags: []string{"Variables"},
		Description: "Returns every variable of the tenant, ordered by key. Every workflow of the tenant reads them as `$vars.KEY`. " +
			"The list is not paged: a tenant holds at most 500 variables.",
	}, handler.List)
	huma.Register(api, huma.Operation{
		OperationID: "create-variable", Method: http.MethodPost, Path: "/variables", DefaultStatus: http.StatusCreated,
		Summary: "Create a variable", Tags: []string{"Variables"},
		Description: "Adds a variable. A key the tenant already has answers 409. The next execution that starts reads it.",
	}, handler.Create)
	huma.Register(api, huma.Operation{
		OperationID: "update-variable", Method: http.MethodPut, Path: "/variables/{id}",
		Summary: "Update a variable", Tags: []string{"Variables"},
		Description: "Replaces a variable's key and value. Renaming onto a key the tenant already has answers 409. " +
			"Executions already running keep the value they started with.",
	}, handler.Update)
	huma.Register(api, huma.Operation{
		OperationID: "delete-variable", Method: http.MethodDelete, Path: "/variables/{id}", DefaultStatus: http.StatusNoContent,
		Summary: "Delete a variable", Tags: []string{"Variables"},
		Description: "Removes a variable. A workflow that still reads it gets an empty value from the next execution on.",
	}, handler.Delete)
}

// List answers every variable of the tenant.
func (handler *Variables) List(ctx context.Context, _ *struct{}) (*variableListOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("variables unavailable")
	}
	variables, err := handler.store.ListVariables(ctx, handler.tenants.Resolve(ctx))
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	resources := make([]VariableResource, 0, len(variables))
	for _, variable := range variables {
		resources = append(resources, variableResource(variable))
	}
	return &variableListOutput{Body: resources}, nil
}

// Create adds a variable.
func (handler *Variables) Create(ctx context.Context, input *createVariableInput) (*createdVariableOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("variables unavailable")
	}
	variable, err := handler.store.CreateVariable(ctx, handler.tenants.Resolve(ctx), strings.TrimSpace(input.Body.Key), input.Body.Value)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &createdVariableOutput{
		Status: http.StatusCreated, Location: "/api/v1/variables/" + variable.ID,
		Body: variableResource(variable),
	}, nil
}

// Update replaces a variable's key and value.
func (handler *Variables) Update(ctx context.Context, input *updateVariableInput) (*variableOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("variables unavailable")
	}
	variable, err := handler.store.UpdateVariable(ctx, handler.tenants.Resolve(ctx), input.ID, strings.TrimSpace(input.Body.Key), input.Body.Value)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &variableOutput{Body: variableResource(variable)}, nil
}

// Delete removes a variable.
func (handler *Variables) Delete(ctx context.Context, input *variablePathInput) (*deletedVariableOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("variables unavailable")
	}
	if err := handler.store.DeleteVariable(ctx, handler.tenants.Resolve(ctx), input.ID); err != nil {
		return nil, handler.problem(ctx, err)
	}
	return &deletedVariableOutput{Status: http.StatusNoContent}, nil
}

func (handler *Variables) problem(ctx context.Context, err error) error {
	switch {
	case errors.Is(err, repository.ErrNotFound):
		return huma.Error404NotFound("variable not found")
	case errors.Is(err, repository.ErrAlreadyExists):
		return huma.Error409Conflict("the tenant already has a variable with this key")
	case errors.Is(err, repository.ErrVariableInvalid):
		return huma.Error422UnprocessableEntity(strings.TrimPrefix(err.Error(), repository.ErrVariableInvalid.Error()+": "))
	case internalFailure(err):
		return serverProblem(ctx, "variable operation failed", err)
	default:
		return huma.Error422UnprocessableEntity(err.Error())
	}
}

func variableResource(variable repository.Variable) VariableResource {
	return VariableResource{
		ID: variable.ID, Key: variable.Key, Value: variable.Value,
		CreatedAt: variable.CreatedAt, UpdatedAt: variable.UpdatedAt,
	}
}
