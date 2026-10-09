package loadoptions_test

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// sheetsStub answers the two Google APIs the Sheets pickers read.
type sheetsStub struct {
	server *httptest.Server
	mu     sync.Mutex
	paths  []string
	auth   string
	// status, when set, answers every call with it.
	status int
	header []any
}

func newSheetsStub(t *testing.T) *sheetsStub {
	t.Helper()
	stub := &sheetsStub{header: []any{"name", "", "age", "name"}}
	stub.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.mu.Lock()
		stub.paths = append(stub.paths, r.URL.Path+"?"+r.URL.RawQuery)
		stub.auth = r.Header.Get("Authorization")
		status := stub.status
		stub.mu.Unlock()
		if status != 0 {
			w.WriteHeader(status)
			_, _ = w.Write([]byte(`{"error":{"message":"SECRET-DETAIL"}}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch {
		case r.URL.Path == "/drive/v3/files":
			_, _ = w.Write([]byte(`{"files":[{"id":"doc-1","name":"Budget"},{"id":"doc-2","name":"Plan"}]}`))
		case r.URL.Path == "/v4/spreadsheets/doc-1":
			_, _ = w.Write([]byte(`{"sheets":[{"properties":{"sheetId":0,"title":"Sheet1"}},{"properties":{"sheetId":1234567890,"title":"Q1 Data"}}]}`))
		case strings.HasPrefix(r.URL.Path, "/v4/spreadsheets/doc-1/values/"):
			values := []any{stub.header}
			_ = json.NewEncoder(w).Encode(map[string]any{"values": values})
		default:
			http.NotFound(w, r)
		}
	}))
	t.Cleanup(stub.server.Close)
	t.Cleanup(loadoptions.SetGoogleSheetsBasesForTest(stub.server.URL+"/drive/v3", stub.server.URL+"/v4"))
	return stub
}

func (stub *sheetsStub) calls() []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]string(nil), stub.paths...)
}

// grantedPolicy is the default egress policy with the stub's one endpoint
// granted, never allow_private_networks.
func grantedPolicy(t *testing.T, serverURL string) safehttp.Policy {
	t.Helper()
	parsed, err := url.Parse(serverURL)
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := net.SplitHostPort(parsed.Host); err != nil {
		t.Fatal(err)
	}
	policy := safehttp.DefaultPolicy()
	if policy.AllowPrivateNetworks {
		t.Fatal("the default policy allows private networks")
	}
	policy.AllowedPrivateEndpoints = []string{parsed.Host}
	policy.Timeout = 5 * time.Second
	return policy
}

func sheetsResolver(t *testing.T, policy safehttp.Policy) *loadoptions.Resolver {
	t.Helper()
	resolver := loadoptions.NewResolver(policy, time.Minute)
	if err := loadoptions.RegisterGoogleSheets(resolver); err != nil {
		t.Fatalf("RegisterGoogleSheets() error = %v", err)
	}
	return resolver
}

func sheetsCredential(domains ...string) *fakeCredentials {
	return &fakeCredentials{
		id: "cred_sheets",
		record: credentials.Record{
			ID: "cred_sheets", Name: "Sheets", Type: credentials.GoogleSheetsOAuthType, AllowedDomains: domains,
		},
		fields: map[string]string{"access_token": "ya29.test"},
	}
}

func internalLoader(name string, credentialType string, dependsOn ...string) property.OptionsLoader {
	return property.OptionsLoader{Source: property.LoaderInternal, Name: name, CredentialType: credentialType, DependsOn: dependsOn}
}

func TestGoogleSheetsLoadersAreRegisteredOnce(t *testing.T) {
	t.Parallel()
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if err := loadoptions.RegisterGoogleSheets(resolver); err != nil {
		t.Fatalf("RegisterGoogleSheets() error = %v", err)
	}
	if err := loadoptions.RegisterGoogleSheets(resolver); err == nil {
		t.Error("registering twice succeeded, want the duplicate refused")
	}
	if err := loadoptions.RegisterGoogleSheets(nil); err == nil {
		t.Error("RegisterGoogleSheets(nil) succeeded")
	}
}

func TestGoogleSheetsDocumentsListSpreadsheetsWithTheCredential(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

	result, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType),
		loadoptions.Scope{TenantID: "t1"}, "cred_sheets", sheetsCredential("127.0.0.1"))
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Options) != 2 || result.Options[0].Label != "Budget" || result.Options[0].Value != "doc-1" {
		t.Fatalf("options = %#v, want the two spreadsheets by name and ID", result.Options)
	}
	if stub.auth != "Bearer ya29.test" {
		t.Errorf("Authorization = %q, want the credential's token", stub.auth)
	}
	query := stub.calls()[0]
	if !strings.Contains(query, "spreadsheet") || !strings.Contains(query, "trashed") {
		t.Errorf("query = %q, want spreadsheets that are not trashed", query)
	}
}

func TestGoogleSheetsDocumentsExplainAMissingScope(t *testing.T) {
	stub := newSheetsStub(t)
	stub.status = http.StatusForbidden
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

	result, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType),
		loadoptions.Scope{TenantID: "t1"}, "cred_sheets", sheetsCredential("127.0.0.1"))
	if err != nil {
		t.Fatalf("Load() error = %v, want an explanation the user can act on rather than a failure", err)
	}
	if len(result.Options) != 0 || !strings.Contains(result.Reason, "By URL") {
		t.Fatalf("result = %#v, want an empty list that points to By URL and By ID", result)
	}
	if strings.Contains(result.Reason, "SECRET-DETAIL") {
		t.Error("the reason echoes Google's response body")
	}
}

func TestGoogleSheetsSheetsListTabsByGid(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	load := func(documentID string) (loadoptions.Result, error) {
		return resolver.Load(context.Background(),
			internalLoader(loadoptions.GoogleSheetsSheetsLoader, credentials.GoogleSheetsOAuthType, "documentId"),
			loadoptions.Scope{TenantID: "t1", Dependencies: map[string]string{"documentId": documentID}}, "cred_sheets", sheetsCredential("127.0.0.1"))
	}

	// The document arrives as the locator's value, so it may be an ID or a link.
	for _, documentID := range []string{"doc-1", "https://docs.google.com/spreadsheets/d/doc-1/edit#gid=0"} {
		result, err := load(documentID)
		if err != nil {
			t.Fatalf("Load(%q) error = %v", documentID, err)
		}
		if len(result.Options) != 2 || result.Options[0].Value != "0" || result.Options[1].Label != "Q1 Data" || result.Options[1].Value != "1234567890" {
			t.Fatalf("Load(%q) options = %#v, want each tab labelled by title and valued by gid", documentID, result.Options)
		}
	}
	if result, err := load(""); err != nil || len(result.Options) != 0 || result.Reason == "" {
		t.Errorf("Load(empty) = %#v, %v, want a reason asking for the document first", result, err)
	}
	if _, err := load("../../etc/passwd"); err == nil {
		t.Error("a document value that is a path was accepted")
	}
}

func TestGoogleSheetsColumnsAreTheHeaderRow(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	loader := property.OptionsLoader{
		Source: property.LoaderInternal, Name: loadoptions.GoogleSheetsColumnsLoader,
		CredentialType: credentials.GoogleSheetsOAuthType, DependsOn: []string{"documentId", "sheetName", "operation"},
	}
	load := func(sheet, operation string) (property.MapperSchema, error) {
		return resolver.LoadSchema(context.Background(), loader, loadoptions.Scope{
			TenantID:     "t1",
			Dependencies: map[string]string{"documentId": "doc-1", "sheetName": sheet, "operation": operation},
		}, "cred_sheets", sheetsCredential("127.0.0.1"))
	}

	schema, err := load("Sheet1", "append")
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	// An empty heading is skipped and a repeated one is kept once.
	if len(schema.Fields) != 2 || schema.Fields[0].ID != "name" || schema.Fields[1].ID != "age" {
		t.Fatalf("fields = %#v, want name and age", schema.Fields)
	}
	for _, field := range schema.Fields {
		if !field.CanBeUsedToMatch || field.ReadOnly || field.Type != "string" {
			t.Errorf("field %#v, want a writable string column that can be matched on", field)
		}
	}

	// Update can also address a row by its number.
	updating, err := load("Sheet1", "update")
	if err != nil {
		t.Fatalf("LoadSchema(update) error = %v", err)
	}
	last := updating.Fields[len(updating.Fields)-1]
	if last.ID != "row_number" || !last.ReadOnly || !last.CanBeUsedToMatch {
		t.Errorf("update fields = %#v, want row_number as a read-only column to match on", updating.Fields)
	}

	// The tab arrives as the locator's value: a gid, which a float64 gid turns
	// into exponent form on its way here, or a title.
	for _, sheet := range []string{"0", "1.23456789e+09", "Q1 Data"} {
		if _, err := load(sheet, "append"); err != nil {
			t.Errorf("LoadSchema(%q) error = %v", sheet, err)
		}
	}
	missing, err := load("", "append")
	if err != nil || len(missing.Fields) != 0 || missing.Reason == "" {
		t.Errorf("LoadSchema(no sheet) = %#v, %v, want a reason", missing, err)
	}

	stub.header = nil
	empty, err := load("Sheet1", "append")
	if err != nil || len(empty.Fields) != 0 || !strings.Contains(empty.Reason, "first row") {
		t.Errorf("LoadSchema(empty header) = %#v, %v, want a reason that names the first row", empty, err)
	}
}

func TestGoogleSheetsLoadersNeedAGoogleCredential(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	loader := internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType)

	if _, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1"}, "", nil); err == nil ||
		!strings.Contains(err.Error(), credentials.GoogleSheetsOAuthType) {
		t.Errorf("Load(no credential) error = %v, want the missing credential type named", err)
	}
	if _, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1"}, "other", sheetsCredential("127.0.0.1")); err == nil {
		t.Error("Load(another tenant's credential) succeeded")
	}
	if len(stub.calls()) != 0 {
		t.Errorf("calls = %v, want none made without a usable credential", stub.calls())
	}
}

// The credential's own allowed hosts bound the loader, as they bound a run: a
// credential that does not name the host never gets sent to it.
func TestGoogleSheetsLoadersHonourTheCredentialsAllowedHosts(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

	_, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType),
		loadoptions.Scope{TenantID: "t1"}, "cred_sheets", sheetsCredential("googleapis.com"))
	if err == nil || !strings.Contains(err.Error(), "not allowed") {
		t.Fatalf("Load() error = %v, want the credential's host scope to refuse the stub", err)
	}
	if len(stub.calls()) != 0 || stub.auth != "" {
		t.Errorf("the stub was contacted (%v, auth %q) despite the refusal", stub.calls(), stub.auth)
	}
}

// And the egress policy still grants one endpoint only.
func TestGoogleSheetsLoadersRefuseALoopbackEndpointThePolicyDidNotGrant(t *testing.T) {
	stub := newSheetsStub(t)
	other := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("the refused endpoint was contacted: %s", r.URL)
	}))
	t.Cleanup(other.Close)
	t.Cleanup(loadoptions.SetGoogleSheetsBasesForTest(other.URL+"/drive/v3", other.URL+"/v4"))
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

	_, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType),
		loadoptions.Scope{TenantID: "t1"}, "cred_sheets", sheetsCredential("127.0.0.1"))
	if !errors.Is(err, safehttp.ErrBlocked) {
		t.Fatalf("Load() error = %v, want safehttp.ErrBlocked", err)
	}
}

func TestGoogleSheetsLoadersReportAGoogleErrorWithoutItsBody(t *testing.T) {
	stub := newSheetsStub(t)
	stub.status = http.StatusInternalServerError
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	_, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsSheetsLoader, credentials.GoogleSheetsOAuthType, "documentId"),
		loadoptions.Scope{TenantID: "t1", Dependencies: map[string]string{"documentId": "doc-1"}}, "cred_sheets", sheetsCredential("127.0.0.1"))
	if err == nil || !strings.Contains(err.Error(), "500") || strings.Contains(err.Error(), "SECRET-DETAIL") {
		t.Fatalf("error = %v, want the status and not the body", err)
	}
}
