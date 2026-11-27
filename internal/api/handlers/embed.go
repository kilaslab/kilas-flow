package handlers

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/datastore"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
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
	// DatastoreID names the one datastore a datastore-scoped session may
	// touch. Empty on every workflow session. A datastore session carries no
	// workflow, so its EmbedURL is empty rather than pointing at /embed/.
	DatastoreID string `json:"datastoreId,omitempty"`
}

// EmbedSessions mints short-lived iframe authorizations.
type EmbedSessions struct {
	issuer      *embed.Issuer
	workflows   repository.WorkflowRepository
	datastores  *datastore.Engine
	credentials repository.CredentialRepository
	tenants     TenantResolver
}

// NewEmbedSessions constructs the embed-session handler.
func NewEmbedSessions(issuer *embed.Issuer, workflows repository.WorkflowRepository, tenants TenantResolver) *EmbedSessions {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &EmbedSessions{issuer: issuer, workflows: workflows, tenants: tenants}
}

// WithDatastores lets a datastore-scoped session prove its subject exists in
// this tenant before a token is minted, the way the workflow check below
// does. Without an engine the check is skipped, exactly as a nil workflow
// repository skips its own.
func (handler *EmbedSessions) WithDatastores(store *datastore.Engine) *EmbedSessions {
	handler.datastores = store
	return handler
}

// WithCredentials lets a declared credential id be checked against the
// tenant before it is minted into a token. Without a store, a mint that
// declares credential ids is refused rather than trusted: the check is the
// whole point of the field.
func (handler *EmbedSessions) WithCredentials(store repository.CredentialRepository) *EmbedSessions {
	handler.credentials = store
	return handler
}

type embedSessionBody struct {
	WorkflowID  string         `json:"workflowId,omitempty" doc:"Workflow this session may open; exactly one of this and datastoreId"`
	DatastoreID string         `json:"datastoreId,omitempty" doc:"Datastore this session may touch; exactly one of this and workflowId"`
	Scopes      []string       `json:"scopes" doc:"workflow:read, workflow:write, workflow:run, workflow:publish, workflow:activate, datastore:read, datastore:write. workflow:publish pins a revision as the version production traffic runs and workflow:activate turns the workflow on and off; neither is implied by write."`
	Origin      string         `json:"origin" minLength:"1" doc:"Exact origin of the page that will frame the editor"`
	TTLSeconds  int            `json:"ttlSeconds,omitempty" doc:"Session lifetime; capped by the server"`
	Branding    embed.Branding `json:"branding,omitempty" doc:"Validated white-label values; never markup"`
	Actor       *embed.Actor   `json:"actor,omitempty" doc:"The host user this session acts for. Signed into the token and recorded as an embed actor on the revisions, publish events, and manual runs the session causes. Host-asserted: KilasFlow does not verify it. Omit it for an unnamed session."`
	Allow       *EmbedAllow    `json:"allow,omitempty" doc:"What the session's document may reference beyond what the workflow's published revision already uses. Workflow sessions only. Each id is checked against this tenant at mint (an unknown or foreign id answers 404); \"tenant\" opens the whole kind and needs a tenant-wide key. Omit it to confine the session to the published revision alone."`
}

// EmbedAllow is the host-declared half of a workflow session's confinement.
//
// The derived half comes from the revision the workflow's owner published, so
// it cannot know about a credential or data table the host provisioned for its
// end user after that — and a blank workflow references nothing at all. The
// host is the one party that knows what its user is entitled to, and it is the
// tenant's own key that mints, so it may name more. The token carries the
// union of the two halves.
type EmbedAllow struct {
	Credentials *EmbedAllowList `json:"credentials,omitempty" doc:"Credential ids the document may attach, or \"tenant\" for every credential in the tenant. The embedded credential picker lists exactly these plus the ones the published revision uses."`
	Datastores  *EmbedAllowList `json:"datastores,omitempty" doc:"Data table ids the document may address (by id, or by the table's name), or \"tenant\" for every data table in the tenant. No session may create, rename, list, clear or drop a table whatever this says."`
	Workflows   *EmbedAllowList `json:"workflows,omitempty" doc:"Workflow ids an Execute Sub-workflow or Workflow Tool node may call, or \"tenant\" for every workflow in the tenant. A called workflow runs with what its own document uses."`
}

// maxDeclaredIDs bounds one kind's id list. The ids travel inside the token,
// which a browser sends on every request in a header; past this many, a host
// should declare the kind "tenant" instead of growing every request.
const maxDeclaredIDs = 50

// tenantAllowKeyword is the one string an allow list accepts in place of ids.
const tenantAllowKeyword = "tenant"

// EmbedAllowList is either a list of ids or the keyword "tenant".
type EmbedAllowList struct {
	// Tenant is true when the host sent "tenant": the whole kind is open.
	Tenant bool
	// IDs is what the host listed when it did not.
	IDs []string
}

// UnmarshalJSON accepts an array of ids or the string "tenant", nothing else.
func (list *EmbedAllowList) UnmarshalJSON(data []byte) error {
	var keyword string
	if err := json.Unmarshal(data, &keyword); err == nil {
		if keyword != tenantAllowKeyword {
			return fmt.Errorf("an allow list is an array of ids or the string %q", tenantAllowKeyword)
		}
		*list = EmbedAllowList{Tenant: true}
		return nil
	}
	var ids []string
	if err := json.Unmarshal(data, &ids); err != nil {
		return fmt.Errorf("an allow list is an array of ids or the string %q", tenantAllowKeyword)
	}
	*list = EmbedAllowList{IDs: ids}
	return nil
}

// MarshalJSON writes the list back in the shape it was accepted in.
func (list EmbedAllowList) MarshalJSON() ([]byte, error) {
	if list.Tenant {
		return json.Marshal(tenantAllowKeyword)
	}
	if list.IDs == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(list.IDs)
}

// Schema publishes the union to OpenAPI as what it is on the wire, so a
// generated client types it `string[] | "tenant"` rather than as an object.
func (EmbedAllowList) Schema(huma.Registry) *huma.Schema {
	minLength, maxLength, maxItems := 1, 128, maxDeclaredIDs
	return &huma.Schema{
		OneOf: []*huma.Schema{
			{
				Type:     "array",
				MaxItems: &maxItems,
				Items:    &huma.Schema{Type: "string", MinLength: &minLength, MaxLength: &maxLength},
			},
			{Type: "string", Enum: []any{tenantAllowKeyword}},
		},
	}
}

var _ huma.SchemaProvider = EmbedAllowList{}

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
		Description: "Mints a short-lived token for one host origin, scoped to one workflow or one datastore. " +
			"The host passes it to the iframe over postMessage.",
		Tags: []string{"Embed"},
	}, handler.Create)
}

// Create mints a session after checking its subject exists in this tenant.
func (handler *EmbedSessions) Create(ctx context.Context, input *createEmbedSessionInput) (*embedSessionOutput, error) {
	if handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("embed sessions are not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)

	// The subject is confirmed before a token is minted, so a session can
	// never point at something that does not exist or belongs elsewhere. A
	// datastore the tenant does not own reads as unknown, never as another
	// tenant's table.
	if input.Body.WorkflowID != "" && input.Body.DatastoreID != "" {
		return nil, huma.Error422UnprocessableEntity("an embed session is scoped to one workflow or one datastore, not both")
	}
	// A workflow session's confinement is derived here, from the revision its
	// owner published, and travels inside the token. Deriving it per request
	// instead would let a poisoned revision authorise itself, and the whole
	// point of the confinement is that the document is not its own authority.
	confinement := embed.Confinement{}
	if input.Body.WorkflowID != "" && handler.workflows != nil {
		stored, err := handler.workflows.Get(ctx, tenant, input.Body.WorkflowID)
		if err != nil {
			return nil, huma.Error404NotFound("workflow not found")
		}
		confinement = embedConfinementOf(stored)
	}
	if input.Body.DatastoreID != "" && handler.datastores != nil {
		if _, err := handler.datastores.GetDatastore(ctx, tenant.ID, input.Body.DatastoreID); err != nil {
			return nil, huma.Error404NotFound("datastore not found")
		}
	}
	// The host's declared half is added to the derived one, never the other
	// way round: what the owner's revision uses stays granted, and the host
	// may only add what this tenant owns.
	declared, err := handler.declaredConfinement(ctx, tenant, input.Body)
	if err != nil {
		return nil, err
	}
	confinement = confinement.Union(declared)

	scopes := make([]embed.Scope, 0, len(input.Body.Scopes))
	for _, scope := range input.Body.Scopes {
		scopes = append(scopes, embed.Scope(scope))
	}

	session, token, err := handler.issuer.Issue(embed.Request{
		TenantID: tenant.ID, WorkflowID: input.Body.WorkflowID, DatastoreID: input.Body.DatastoreID,
		Scopes: scopes, Origin: input.Body.Origin,
		Lifetime:    time.Duration(input.Body.TTLSeconds) * time.Second,
		Branding:    input.Body.Branding,
		Confinement: confinement,
		Actor:       input.Body.Actor,
	})
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}

	granted := make([]string, 0, len(session.Scopes))
	for _, scope := range session.Scopes {
		granted = append(granted, string(scope))
	}
	// A datastore session names no workflow, so it carries no editor URL: a
	// fabricated /embed/ would send the host's iframe at a workflow route
	// this very token is refused on.
	embedURL := ""
	if session.WorkflowID != "" {
		embedURL = "/embed/" + session.WorkflowID
	}
	return &embedSessionOutput{
		Status: http.StatusCreated,
		Body: EmbedSessionResource{
			Token:     token,
			EmbedURL:  embedURL,
			ExpiresAt: session.ExpiresAt, Scopes: granted,
			Origin: session.Origin, Branding: session.Branding,
			DatastoreID: session.DatastoreID,
		},
	}, nil
}

// declaredConfinement turns a mint body's allow set into the confinement it
// grants, after checking every id belongs to the minting tenant.
//
// An id this tenant does not own answers 404 whether it is unknown or another
// tenant's, so the answer cannot be used to probe for ids elsewhere. The first
// refusal stops the mint: a token is never issued with part of what the host
// asked for silently dropped.
func (handler *EmbedSessions) declaredConfinement(ctx context.Context, tenant repository.TenantScope, body embedSessionBody) (embed.Confinement, error) {
	allow := body.Allow
	if allow == nil {
		return embed.Confinement{}, nil
	}
	if body.WorkflowID == "" {
		return embed.Confinement{}, huma.Error422UnprocessableEntity(
			"allow applies to a workflow session only: a datastore session has no document to confine")
	}
	declared := embed.Confinement{}
	kinds := []struct {
		field string
		list  *EmbedAllowList
		open  *bool
	}{
		{"allow.credentials", allow.Credentials, &declared.TenantCredentials},
		{"allow.datastores", allow.Datastores, &declared.TenantDatastores},
		{"allow.workflows", allow.Workflows, &declared.TenantWorkflows},
	}
	for _, kind := range kinds {
		if kind.list == nil {
			continue
		}
		if kind.list.Tenant {
			if !mintsWithTenantAuthority(ctx) {
				return embed.Confinement{}, huma.Error403Forbidden(fmt.Sprintf(
					"%s: %q needs a tenant-wide key; a caller confined to part of the tenant must list ids", kind.field, tenantAllowKeyword))
			}
			*kind.open = true
			continue
		}
		if len(kind.list.IDs) > maxDeclaredIDs {
			return embed.Confinement{}, huma.Error422UnprocessableEntity(fmt.Sprintf(
				"%s lists %d ids, more than %d: declare it %q instead", kind.field, len(kind.list.IDs), maxDeclaredIDs, tenantAllowKeyword))
		}
		for _, raw := range kind.list.IDs {
			if strings.TrimSpace(raw) == "" {
				return embed.Confinement{}, huma.Error422UnprocessableEntity(kind.field + " contains a blank id")
			}
		}
	}

	if allow.Credentials != nil {
		for _, id := range allow.Credentials.IDs {
			id = strings.TrimSpace(id)
			if handler.credentials == nil {
				return embed.Confinement{}, huma.Error503ServiceUnavailable(
					"credential storage unavailable: allow.credentials cannot be checked")
			}
			if _, err := handler.credentials.Get(ctx, tenant, id); err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return embed.Confinement{}, huma.Error404NotFound(fmt.Sprintf("allow.credentials: credential %s not found", id))
				}
				return embed.Confinement{}, serverProblem(ctx, "declared credential check failed", err)
			}
			declared.Credentials = append(declared.Credentials, id)
		}
	}
	if allow.Datastores != nil {
		for _, id := range allow.Datastores.IDs {
			id = strings.TrimSpace(id)
			if handler.datastores == nil {
				return embed.Confinement{}, huma.Error503ServiceUnavailable(
					"datastores unavailable: allow.datastores cannot be checked")
			}
			found, err := handler.datastores.GetDatastore(ctx, tenant.ID, id)
			if err != nil || found == nil {
				return embed.Confinement{}, huma.Error404NotFound(fmt.Sprintf("allow.datastores: data table %s not found", id))
			}
			// The name is granted beside the id because an editor may address
			// the table By Name, and that locator is checked by its name. It
			// is the name the table has now, read from this tenant's catalogue.
			declared.Datastores = append(declared.Datastores, embed.DatastoreRef{ID: found.ID, Name: found.Name})
		}
	}
	if allow.Workflows != nil {
		for _, id := range allow.Workflows.IDs {
			id = strings.TrimSpace(id)
			if handler.workflows == nil {
				return embed.Confinement{}, huma.Error503ServiceUnavailable(
					"workflow storage unavailable: allow.workflows cannot be checked")
			}
			if _, err := handler.workflows.Get(ctx, tenant, id); err != nil {
				if errors.Is(err, repository.ErrNotFound) {
					return embed.Confinement{}, huma.Error404NotFound(fmt.Sprintf("allow.workflows: workflow %s not found", id))
				}
				return embed.Confinement{}, serverProblem(ctx, "declared workflow check failed", err)
			}
			declared.Workflows = append(declared.Workflows, id)
		}
	}
	return declared, nil
}

// mintsWithTenantAuthority reports whether the caller minting a session holds
// the whole tenant's authority, which is what opening a kind to "tenant" hands
// on to the session.
//
// A tenant-wide API key and a dashboard user do. A scoped key, a key bound to
// one workflow, and an embed session do not: each is confined to part of the
// tenant, and a session it minted with "tenant" would reach further than its
// minter. The routing middleware already refuses the embed-session endpoint to
// all three; this is the handler's own answer, so the rule does not rest on a
// route table alone. With authentication off there is no principal, and the
// caller is the installation's operator.
func mintsWithTenantAuthority(ctx context.Context) bool {
	if _, embedded := middleware.EmbedSessionFrom(ctx); embedded {
		return false
	}
	principal, found := auth.PrincipalFrom(ctx)
	if !found {
		return true
	}
	return !principal.Scoped() && principal.WorkflowID == ""
}
