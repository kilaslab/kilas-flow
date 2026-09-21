package middleware

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/embed"
)

func datastoreSession(scopes ...embed.Scope) embed.Session {
	return embed.Session{
		ID: "es_1", TenantID: "tenant-a", DatastoreID: "datastore_1",
		Scopes: scopes, Origin: "https://host.example",
	}
}

func workflowSession(scopes ...embed.Scope) embed.Session {
	return embed.Session{
		ID: "es_2", TenantID: "tenant-a", WorkflowID: "wf_1",
		Scopes: scopes, Origin: "https://host.example",
	}
}

func TestPermitsAdmitsDatastoreRoutesOnlyWithTheMatchingScope(t *testing.T) {
	t.Parallel()

	read := datastoreSession(embed.ScopeDatastoreRead)
	write := datastoreSession(embed.ScopeDatastoreWrite)

	for _, route := range []struct {
		name    string
		method  string
		path    string
		session embed.Session
		want    bool
	}{
		// Reads admit the definition, the row listing, the single row,
		// and the CSV export — one family, one verb.
		{"read owns the definition", http.MethodGet, "/api/v1/datastores/datastore_1", read, true},
		{"read lists rows", http.MethodGet, "/api/v1/datastores/datastore_1/rows", read, true},
		{"read gets a row", http.MethodGet, "/api/v1/datastores/datastore_1/rows/7", read, true},
		{"read exports csv", http.MethodGet, "/api/v1/datastores/datastore_1/rows/export", read, true},
		// Identity is the handler's question, not this gate's: a session
		// naming another datastore still passes here and reads it as
		// unknown (404) downstream.
		{"read passes another datastore to the handler", http.MethodGet, "/api/v1/datastores/datastore_2/rows", read, true},
		// Writes need the write scope, and write implies read.
		{"read cannot insert", http.MethodPost, "/api/v1/datastores/datastore_1/rows", read, false},
		{"read cannot update", http.MethodPut, "/api/v1/datastores/datastore_1/rows", read, false},
		{"read cannot delete", http.MethodDelete, "/api/v1/datastores/datastore_1/rows", read, false},
		{"read cannot increment", http.MethodPost, "/api/v1/datastores/datastore_1/rows/increment", read, false},
		{"read cannot import", http.MethodPost, "/api/v1/datastores/datastore_1/rows/import", read, false},
		{"write inserts", http.MethodPost, "/api/v1/datastores/datastore_1/rows", write, true},
		{"write updates", http.MethodPut, "/api/v1/datastores/datastore_1/rows", write, true},
		{"write deletes", http.MethodDelete, "/api/v1/datastores/datastore_1/rows", write, true},
		{"write upserts", http.MethodPost, "/api/v1/datastores/datastore_1/rows/upsert", write, true},
		{"write increments", http.MethodPost, "/api/v1/datastores/datastore_1/rows/increment", write, true},
		{"write imports", http.MethodPost, "/api/v1/datastores/datastore_1/rows/import", write, true},
		{"write still reads", http.MethodGet, "/api/v1/datastores/datastore_1/rows", write, true},
		// Schema work is never embed work, on either scope: creating,
		// renaming, dropping, clearing, and editing columns stay with the
		// backend key, the way workflow import and activation already do.
		{"read cannot list tables", http.MethodGet, "/api/v1/datastores", read, false},
		{"write cannot list tables", http.MethodGet, "/api/v1/datastores", write, false},
		{"write cannot create a table", http.MethodPost, "/api/v1/datastores", write, false},
		{"write cannot rename", http.MethodPut, "/api/v1/datastores/datastore_1", write, false},
		{"write cannot drop", http.MethodDelete, "/api/v1/datastores/datastore_1", write, false},
		{"write cannot clear", http.MethodPost, "/api/v1/datastores/datastore_1/clear", write, false},
		{"write cannot add a column", http.MethodPost, "/api/v1/datastores/datastore_1/columns", write, false},
		{"write cannot rename a column", http.MethodPut, "/api/v1/datastores/datastore_1/columns/email", write, false},
		{"write cannot drop a column", http.MethodDelete, "/api/v1/datastores/datastore_1/columns/email", write, false},
		// Anything unrecognised stays refused by default.
		{"unknown datastore subpath is refused", http.MethodGet, "/api/v1/datastores/datastore_1/share", read, false},
		{"unknown method on rows is refused", "PATCH", "/api/v1/datastores/datastore_1/rows", write, false},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(route.method, route.path, nil)
			if allowed, _ := permits(route.session, request); allowed != route.want {
				t.Errorf("%s %s allowed = %v, want %v", route.method, route.path, allowed, route.want)
			}
		})
	}
}

func TestPermitsKeepsTheTwoFamiliesApart(t *testing.T) {
	t.Parallel()

	fullWorkflow := workflowSession(embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun)
	readDatastore := datastoreSession(embed.ScopeDatastoreRead)

	for _, route := range []struct {
		name    string
		method  string
		path    string
		session embed.Session
		want    bool
	}{
		// A fully-scoped workflow session reaches no datastore route.
		{"workflow cannot read the definition", http.MethodGet, "/api/v1/datastores/datastore_1", fullWorkflow, false},
		{"workflow cannot list rows", http.MethodGet, "/api/v1/datastores/datastore_1/rows", fullWorkflow, false},
		{"workflow cannot insert", http.MethodPost, "/api/v1/datastores/datastore_1/rows", fullWorkflow, false},
		{"workflow cannot export", http.MethodGet, "/api/v1/datastores/datastore_1/rows/export", fullWorkflow, false},
		// A datastore session reaches no workflow route.
		{"datastore cannot read the workflow", http.MethodGet, "/api/v1/workflows/wf_1", readDatastore, false},
		{"datastore cannot list executions", http.MethodGet, "/api/v1/executions?workflowId=wf_1", readDatastore, false},
		{"datastore cannot read credentials", http.MethodGet, "/api/v1/credentials", readDatastore, false},
		{"datastore cannot read node types", http.MethodGet, "/api/v1/node-types", readDatastore, false},
		// And the default arm still refuses what nothing names.
		{"datastore refused elsewhere", http.MethodGet, "/api/v1/schedules", readDatastore, false},
		{"workflow refused elsewhere", http.MethodGet, "/api/v1/schedules", fullWorkflow, false},
	} {
		t.Run(route.name, func(t *testing.T) {
			t.Parallel()
			request := httptest.NewRequest(route.method, route.path, nil)
			if allowed, _ := permits(route.session, request); allowed != route.want {
				t.Errorf("%s %s allowed = %v, want %v", route.method, route.path, allowed, route.want)
			}
		})
	}
}
