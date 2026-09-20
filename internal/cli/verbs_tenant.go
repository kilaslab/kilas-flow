package cli

import (
	"net/http"
	"net/url"
)

// tenantVerbs are the operator's read-only view of the deployment's tenants.
//
// Every one of them needs the operator credential — an API key scoped to the
// operator tenant — and the server is the thing that enforces it: the verb sends
// whatever token it was configured with and carries the 403 back as exit 3 with
// `scope_denied`. A customer's key gets the refusal, not a filtered listing.
//
// Deliberately absent: `tenant create` (a write, and phase 2's), and `tenant
// api-keys`. The design's tree lists an api-keys verb, but the only key
// operation under a tenant is `create-tenant-api-key`, which mints a credential
// for someone else — a write, and not something an agent should reach through an
// ergonomic verb. Listing the caller's own keys is a different operation
// (`list-api-keys`, GET /api-keys), and `kilasflow api list-api-keys` reaches it
// today, as does `api create-tenant-api-key` for the mint.
func tenantVerbs() []Verb {
	return []Verb{
		{
			Path:      "tenant list",
			Operation: "list-tenants",
			Summary:   "list every tenant in this deployment (operator credential)",
			Run:       runTenantList,
			Human:     humanResourceList("id", "name", "userCount", "createdAt"),
		},
		{
			Path:      "tenant get",
			Operation: "get-tenant",
			Summary:   "read one tenant (operator credential)",
			Run:       runTenantGet,
			Human:     humanResourceResource,
		},
		{
			Path:      "tenant users",
			Operation: "list-tenant-users",
			Summary:   "list a tenant's accounts, without any password hash (operator credential)",
			Run:       runTenantUsers,
			Human:     humanResourceList("id", "email", "name", "disabledAt"),
		},
	}
}

// runTenantList reads every tenant.
func runTenantList(ctx *Context, args []string) error {
	if err := refusePositional(args, "tenant list"); err != nil {
		return err
	}

	// list-tenants takes no parameters, so the verb takes none.
	list, err := ctx.listPage("/tenants", nil)
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}

// runTenantGet reads one tenant.
func runTenantGet(ctx *Context, args []string) error {
	id, err := requireOneID(args, "tenant id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodGet, "/tenants/"+url.PathEscape(id), nil, id)
}

// runTenantUsers reads one tenant's accounts.
func runTenantUsers(ctx *Context, args []string) error {
	id, err := requireOneID(args, "tenant id")
	if err != nil {
		return err
	}

	list, err := ctx.listPage("/tenants/"+url.PathEscape(id)+"/users", nil)
	if err != nil {
		return err
	}

	return listPageResult(ctx, list)
}
