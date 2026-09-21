package cli

import (
	"encoding/json"
	"io"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
)

// tenantVerbs are the operator's view of the deployment's tenants.
//
// Every one of them needs the operator credential — an API key scoped to the
// operator tenant — and the server is the thing that enforces it: the verb sends
// whatever token it was configured with and carries the 403 back as exit 3 with
// `scope_denied`. A customer's key gets the refusal, not a filtered listing.
//
// `tenant delete` is guarded on top of that: it is irreversible, so it refuses
// without --yes, and the authority gate refuses a scoped agent token before the
// deletion is attempted.
//
// Deliberately absent: `tenant create` (a write with no guarded reason to have
// a verb yet) and `tenant api-keys`. The design's tree lists an api-keys verb,
// but the only key operation under a tenant is `create-tenant-api-key`, which
// mints a credential for someone else — a write, and not something an agent
// should reach through an ergonomic verb. Listing the caller's own keys is a
// different operation (`list-api-keys`, GET /api-keys), and `kilasflow api
// list-api-keys` reaches it today, as does `api create-tenant-api-key` for the
// mint.
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
			Args:      []Arg{arg("tenant id")},
			Run:       runTenantGet,
			Human:     humanResourceResource,
		},
		{
			Path:      "tenant users",
			Operation: "list-tenant-users",
			Summary:   "list a tenant's accounts, without any password hash (operator credential)",
			Args:      []Arg{arg("tenant id")},
			Run:       runTenantUsers,
			Human:     humanResourceList("id", "email", "name", "disabledAt"),
		},
		{
			Path:      "tenant delete",
			Operation: "delete-tenant",
			Summary:   "delete a tenant and everything it owns (operator credential, guarded)",
			Args:      []Arg{arg("tenant id")},
			Guarded:   true,
			Refusal:   "deletes a tenant and everything it owns",
			Run:       runTenantDelete,
			Human:     humanTenantDeletion,
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

// runTenantDelete removes a tenant and everything keyed to it.
//
// The operation is idempotent and answers with what it removed rather than with
// 204: repeating the call until every count is zero and tenantRemoved is false
// is how a client that lost the first answer confirms a deletion finished, and
// the verb carries that evidence through unchanged.
func runTenantDelete(ctx *Context, args []string) error {
	id, err := requireOneID(args, "tenant id")
	if err != nil {
		return err
	}

	return ctx.readResource(http.MethodDelete, "/tenants/"+url.PathEscape(id), nil, id)
}

// humanTenantDeletion prints what a deletion removed, table by table.
func humanTenantDeletion(w io.Writer, data any) {
	raw, ok := data.(json.RawMessage)
	if !ok {
		printJSONValue(w, data)

		return
	}

	var deletion struct {
		TenantID        string           `json:"tenantId"`
		TenantRemoved   bool             `json:"tenantRemoved"`
		Removed         map[string]int64 `json:"removed"`
		DatastoreTables int              `json:"datastoreTables"`
		Binaries        struct {
			Files int   `json:"files"`
			Bytes int64 `json:"bytes"`
		} `json:"binaries"`
	}
	if err := json.Unmarshal(raw, &deletion); err != nil {
		printJSONValue(w, data)

		return
	}

	var rows int64
	names := make([]string, 0, len(deletion.Removed))
	for name := range deletion.Removed {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		rows += deletion.Removed[name]
	}
	perTable := make([]string, 0, len(names))
	for _, name := range names {
		perTable = append(perTable, name+"="+strconv.FormatInt(deletion.Removed[name], 10))
	}

	printKV(w, [][2]string{
		{"tenantId", deletion.TenantID},
		{"tenantRemoved", strconv.FormatBool(deletion.TenantRemoved)},
		{"rows", strconv.FormatInt(rows, 10)},
		{"byTable", strings.Join(perTable, " ")},
		{"datastoreTables", strconv.Itoa(deletion.DatastoreTables)},
		{"binaries", strconv.Itoa(deletion.Binaries.Files) + " files, " + strconv.FormatInt(deletion.Binaries.Bytes, 10) + " bytes"},
	})
}
