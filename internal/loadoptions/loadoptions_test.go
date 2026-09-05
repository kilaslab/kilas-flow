package loadoptions_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// fakeCredentials resolves one credential and refuses everything else, which is
// what a tenant-scoped store does for another tenant's ID.
type fakeCredentials struct {
	id     string
	record credentials.Record
	fields map[string]string
	calls  int
}

func (fake *fakeCredentials) Resolve(_ context.Context, credentialID string) (credentials.Record, map[string]string, error) {
	fake.calls++
	if credentialID != fake.id {
		return credentials.Record{}, nil, http.ErrNoLocation
	}
	return fake.record, fake.fields, nil
}

// openPolicy allows exactly the test server's host. The allowlist compares
// against the hostname, so the port must not be included.
func openPolicy(serverURL string) safehttp.Policy {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		panic(err)
	}
	return safehttp.Policy{
		AllowPrivateNetworks: true,
		AllowedHosts:         []string{parsed.Hostname()},
		MaxResponseBytes:     1 << 20,
		Timeout:              5 * time.Second,
	}
}

// TestLoaderRendersOptionsFromAService is the capability: a WAHA node offering
// "pick one of your sessions" rather than a free-text field where a typo is
// indistinguishable from a valid value until the run fails.
func TestLoaderRendersOptionsFromAService(t *testing.T) {
	var seenPath, seenKey string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.Path
		seenKey = r.Header.Get("X-Api-Key")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"data": []any{
				map[string]any{"name": "default", "status": "WORKING"},
				map[string]any{"name": "sales", "status": "STOPPED"},
			},
		})
	}))
	defer server.Close()

	resolver := loadoptions.NewResolver(openPolicy(server.URL), time.Minute)
	credential := &fakeCredentials{
		id:     "cred_1",
		record: credentials.Record{ID: "cred_1", Type: "httpHeaderAuth"},
		fields: map[string]string{"name": "X-Api-Key", "value": "k-secret"},
	}

	result, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Method: http.MethodGet,
		Endpoint:       server.URL + "/api/sessions",
		CredentialType: "httpHeaderAuth",
		ItemsPath:      "data",
		LabelTemplate:  "{{ name }} ({{ status }})",
		ValueField:     "name",
	}, loadoptions.Scope{TenantID: "t1"}, "cred_1", credential)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}

	if len(result.Options) != 2 {
		t.Fatalf("options = %#v, want two", result.Options)
	}
	if result.Options[0].Label != "default (WORKING)" || result.Options[0].Value != "default" {
		t.Errorf("first option = %#v, want the template rendered and the value field read", result.Options[0])
	}
	if seenPath != "/api/sessions" {
		t.Errorf("requested %q", seenPath)
	}
	// The credential signed the request through its declared descriptor.
	if seenKey != "k-secret" {
		t.Errorf("X-Api-Key = %q, want the credential applied", seenKey)
	}
}

// TestDependencyValuesAreEscapedNotConcatenated is the trust rule that matters
// most. The parameters come from a browser, so a value that could reshape the
// path would let a caller point the server anywhere the policy allows.
func TestDependencyValuesAreEscapedNotConcatenated(t *testing.T) {
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The escaped form, which is what actually went on the wire. r.URL.Path
		// is decoded, so it shows the dots even when the value never escaped
		// its segment.
		seenPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	resolver := loadoptions.NewResolver(openPolicy(server.URL), time.Minute)
	_, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP,
		// A hostile session value trying to climb out of the path.
		Endpoint:   server.URL + "/api/{{ session }}/chats",
		ValueField: "id",
		DependsOn:  []string{"session"},
	}, loadoptions.Scope{
		TenantID:     "t1",
		Dependencies: map[string]string{"session": "../../admin/secrets"},
	}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if strings.Contains(seenPath, "/admin/secrets") {
		t.Errorf("path = %q; the dependency value escaped its segment", seenPath)
	}
	if !strings.Contains(seenPath, "%2F") {
		t.Errorf("path = %q, want the separators escaped so the value stays one segment", seenPath)
	}
}

// TestALoaderCannotReachADisallowedHost proves the egress policy applies, and
// that it fails with the error an HTTP node would produce.
func TestALoaderCannotReachADisallowedHost(t *testing.T) {
	resolver := loadoptions.NewResolver(safehttp.Policy{
		AllowedHosts: []string{"allowed.invalid"}, Timeout: time.Second,
	}, time.Minute)

	_, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Endpoint: "https://elsewhere.invalid/list", ValueField: "id",
	}, loadoptions.Scope{TenantID: "t1"}, "", nil)
	if err == nil {
		t.Fatal("a loader reached a host the policy disallows")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want the egress refusal", err)
	}
}

// TestACredentialCannotReachOutsideItsAllowedDomains keeps a scoped credential
// from being used to reach anywhere its workflows could not.
func TestACredentialCannotReachOutsideItsAllowedDomains(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	resolver := loadoptions.NewResolver(openPolicy(server.URL), time.Minute)
	credential := &fakeCredentials{
		id: "cred_1",
		record: credentials.Record{
			ID: "cred_1", Type: "httpHeaderAuth",
			AllowedDomains: []string{"waha.example.test"},
		},
		fields: map[string]string{"name": "X-Api-Key", "value": "k"},
	}

	_, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Endpoint: server.URL + "/api/sessions",
		CredentialType: "httpHeaderAuth", ValueField: "id",
	}, loadoptions.Scope{TenantID: "t1"}, "cred_1", credential)
	if err == nil {
		t.Fatal("a scoped credential reached a host outside its allowed domains")
	}
	if !strings.Contains(err.Error(), "not allowed") {
		t.Errorf("error = %v, want the refusal", err)
	}
}

// TestAnotherTenantsCredentialResolvesToNothing covers the third trust rule.
func TestAnotherTenantsCredentialResolvesToNothing(t *testing.T) {
	resolver := loadoptions.NewResolver(openPolicy("https://example.invalid"), time.Minute)
	credential := &fakeCredentials{id: "cred_mine"}

	result, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, Endpoint: "https://example.invalid/x",
		CredentialType: "httpHeaderAuth", ValueField: "id",
	}, loadoptions.Scope{TenantID: "t1"}, "cred_theirs", credential)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Options) != 0 {
		t.Errorf("options = %#v, want none for a credential the caller cannot read", result.Options)
	}
	if result.Reason == "" {
		t.Error("no reason was given for the empty list")
	}
	// And no secret in the reason.
	if strings.Contains(result.Reason, "cred_theirs") {
		t.Errorf("reason = %q, want it not to echo the requested ID", result.Reason)
	}
}

// TestResultsAreCachedPerDependencyValues keeps a dropdown from firing a
// request at the customer's service on every open, while still refetching when
// a dependency changes.
func TestResultsAreCachedPerDependencyValues(t *testing.T) {
	var calls int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		calls++
		_, _ = w.Write([]byte(`[{"id":"a"}]`))
	}))
	defer server.Close()

	resolver := loadoptions.NewResolver(openPolicy(server.URL), time.Minute)
	loader := property.OptionsLoader{
		Source: property.LoaderHTTP, Endpoint: server.URL + "/api/{{ session }}/chats",
		ValueField: "id", DependsOn: []string{"session"},
	}

	for range 3 {
		if _, err := resolver.Load(context.Background(), loader, loadoptions.Scope{
			TenantID: "t1", Dependencies: map[string]string{"session": "default"},
		}, "", nil); err != nil {
			t.Fatalf("Load() error = %v", err)
		}
	}
	if calls != 1 {
		t.Errorf("the service was called %d times for one dependency value, want 1", calls)
	}

	// A changed dependency is a different list.
	if _, err := resolver.Load(context.Background(), loader, loadoptions.Scope{
		TenantID: "t1", Dependencies: map[string]string{"session": "sales"},
	}, "", nil); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if calls != 2 {
		t.Errorf("the service was called %d times after the dependency changed, want 2", calls)
	}

	// And another tenant does not read this tenant's cached list.
	if _, err := resolver.Load(context.Background(), loader, loadoptions.Scope{
		TenantID: "t2", Dependencies: map[string]string{"session": "default"},
	}, "", nil); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if calls != 3 {
		t.Errorf("a second tenant read the first tenant's cached options")
	}
}

// TestAnInternalLoaderIsScopedByWorkflowNotOnlyTenant is the p9 amendment.
//
// The embed middleware allows the whole /node-types/ subtree on read scope with
// no workflow check, so an internal loader that scoped only by tenant would let
// a session bound to one workflow enumerate everything in the tenant.
func TestAnInternalLoaderIsScopedByWorkflowNotOnlyTenant(t *testing.T) {
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	var seen loadoptions.Scope
	if err := resolver.RegisterInternal("datastores", func(_ context.Context, scope loadoptions.Scope) (loadoptions.Result, error) {
		seen = scope
		return loadoptions.Result{Options: []loadoptions.Option{{Label: "One", Value: "1"}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}

	if _, err := resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: "datastores"},
		loadoptions.Scope{TenantID: "t1", WorkflowID: "wf_1"}, "", nil); err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if seen.WorkflowID != "wf_1" {
		t.Errorf("the internal loader was given workflow %q; without it an embed session could enumerate the whole tenant", seen.WorkflowID)
	}
	// An internal loader constructs no request, so no egress policy applies —
	// which is exactly why its scoping has to be explicit instead.
	if seen.TenantID != "t1" {
		t.Errorf("tenant = %q", seen.TenantID)
	}
}

// TestAnUnregisteredInternalLoaderIsRefused keeps the request from naming a
// lookup this server does not have.
func TestAnUnregisteredInternalLoaderIsRefused(t *testing.T) {
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	if _, err := resolver.Load(context.Background(),
		property.OptionsLoader{Source: property.LoaderInternal, Name: "whatever"},
		loadoptions.Scope{TenantID: "t1"}, "", nil); err == nil {
		t.Error("an unregistered internal loader was run")
	}
}

// TestABaseURLIsNotEscapedWhileDependenciesAre pins the distinction.
//
// A dependency is a value going into a path segment and is always escaped; a
// base URL *is* a URL, and escaping it would make https://host/v1 one unusable
// segment. Keeping them separate is what lets "every dependency value is
// escaped" stay true with no exception.
func TestABaseURLIsNotEscapedWhileDependenciesAre(t *testing.T) {
	var seenPath string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenPath = r.URL.EscapedPath()
		_, _ = w.Write([]byte(`{"data":[{"id":"gpt-4o-mini"}]}`))
	}))
	defer server.Close()

	resolver := loadoptions.NewResolver(openPolicy(server.URL), time.Minute)
	result, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, BaseURLParameter: "baseUrl", Endpoint: "/v1/{{ tenant }}/models",
		ItemsPath: "data", ValueField: "id", DependsOn: []string{"baseUrl", "tenant"},
	}, loadoptions.Scope{
		TenantID: "t1",
		Dependencies: map[string]string{
			"baseUrl": server.URL + "/api",
			"tenant":  "a/b",
		},
	}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Options) != 1 || result.Options[0].Value != "gpt-4o-mini" {
		t.Errorf("options = %#v", result.Options)
	}
	// The base URL's own separators survive.
	if !strings.HasPrefix(seenPath, "/api/v1/") {
		t.Errorf("path = %q, want the base URL preserved", seenPath)
	}
	// The dependency's do not.
	if !strings.Contains(seenPath, "a%2Fb") {
		t.Errorf("path = %q, want the dependency escaped into one segment", seenPath)
	}
}

// TestAMissingBaseURLAsksRatherThanGuessing keeps a half-built URL off the wire.
func TestAMissingBaseURLAsksRatherThanGuessing(t *testing.T) {
	resolver := loadoptions.NewResolver(safehttp.DefaultPolicy(), time.Minute)
	_, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderHTTP, BaseURLParameter: "baseUrl", Endpoint: "/models", ValueField: "id",
		DependsOn: []string{"baseUrl"},
	}, loadoptions.Scope{TenantID: "t1"}, "", nil)
	if err == nil {
		t.Fatal("a loader with no base URL built a request anyway")
	}
	if !strings.Contains(err.Error(), "service address") {
		t.Errorf("error = %v, want it to say what is missing", err)
	}
}

func TestAnInternalLoaderNeverConstructsAnOutboundRequest(t *testing.T) {
	t.Parallel()

	// The distinction is load-bearing rather than cosmetic. An outbound loader
	// is governed by the egress policy and the credential's allowed domains; an
	// internal one constructs no request at all, so those defences have nothing
	// to say about it — and this test is what makes "constructs no request"
	// true rather than intended.
	reached := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		reached = true
		w.WriteHeader(http.StatusOK)
	}))
	t.Cleanup(server.Close)

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	resolver := loadoptions.NewResolver(policy, time.Minute)
	if err := resolver.RegisterInternal("test.internal", func(context.Context, loadoptions.Scope) (loadoptions.Result, error) {
		return loadoptions.Result{Options: []loadoptions.Option{{Label: "public", Value: "public"}}}, nil
	}); err != nil {
		t.Fatalf("RegisterInternal() error = %v", err)
	}

	result, err := resolver.Load(context.Background(), property.OptionsLoader{
		Source: property.LoaderInternal, Name: "test.internal",
		// Deliberately also carrying an endpoint. A loader that fell back to
		// the HTTP path when it did not recognise the source would reach it,
		// and the whole point is that the source decides, not the fields.
		Endpoint: server.URL, ValueField: "id",
	}, loadoptions.Scope{TenantID: "tenant-a"}, "", nil)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if len(result.Options) != 1 || result.Options[0].Value != "public" {
		t.Errorf("options = %#v, want the internal loader's own list", result.Options)
	}
	if reached {
		t.Fatal("an internal loader made an outbound request")
	}
}

func TestTheWorkflowLoaderIsBoundToAnEmbedSessionsOwnWorkflow(t *testing.T) {
	t.Parallel()

	listed := []loadoptions.WorkflowOption{
		{ID: "wf_1", Name: "Enrichment", Active: true},
		{ID: "wf_2", Name: "Draft", Active: false},
		{ID: "wf_3", Name: "Someone else's", Active: true},
	}
	loader := loadoptions.Workflows(func(_ context.Context, tenant repository.TenantScope) ([]loadoptions.WorkflowOption, error) {
		if tenant.ID != "tenant-a" {
			t.Errorf("the loader was called for tenant %q", tenant.ID)
		}
		return listed, nil
	})

	// An unrestricted caller — the internal dashboard — sees the tenant's
	// workflows, with the inactive ones labelled rather than hidden: a picker
	// that hid them would leave the author hunting for a workflow they know
	// exists.
	all, err := loader(context.Background(), loadoptions.Scope{TenantID: "tenant-a"})
	if err != nil {
		t.Fatalf("loader error = %v", err)
	}
	if len(all.Options) != 3 {
		t.Fatalf("options = %#v, want every workflow in the tenant", all.Options)
	}
	if all.Options[1].Label != "Draft (inactive)" {
		t.Errorf("label = %q, want the inactive one said so", all.Options[1].Label)
	}

	// An embed session sees exactly its own. Nothing else stands between a
	// browser and every workflow in the installation on this path: an internal
	// loader has no egress policy to hide behind.
	bounded, err := loader(context.Background(), loadoptions.Scope{TenantID: "tenant-a", WorkflowID: "wf_1"})
	if err != nil {
		t.Fatalf("loader error = %v", err)
	}
	if len(bounded.Options) != 1 || bounded.Options[0].Value != "wf_1" {
		t.Fatalf("options = %#v, want only the session's own workflow", bounded.Options)
	}

	// And a session whose workflow is not in the list gets a reason rather than
	// a silently empty picker.
	empty, err := loader(context.Background(), loadoptions.Scope{TenantID: "tenant-a", WorkflowID: "wf_missing"})
	if err != nil {
		t.Fatalf("loader error = %v", err)
	}
	if len(empty.Options) != 0 || empty.Reason == "" {
		t.Errorf("result = %#v, want an empty list with a reason", empty)
	}

	if _, err := loader(context.Background(), loadoptions.Scope{}); err == nil {
		t.Error("the loader answered with no tenant")
	}
}
