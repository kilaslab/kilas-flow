package nodepack_test

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// acmeCredentialTypeJSON is the declaration the credentials doc shows: a
// readable base URL with a default, a masked-but-readable account ID, and a
// secret API key sent in a header.
const acmeCredentialTypeJSON = `{
	"id": "pack.acme.api", "displayName": "Acme API",
	"fields": [
		{"key": "baseUrl", "label": "Base URL", "kind": "string", "required": true, "default": "https://api.acme.example"},
		{"key": "accountId", "label": "Account ID", "kind": "string", "typeOptions": {"password": true}},
		{"key": "apiKey", "label": "API key", "kind": "string", "required": true, "secret": true}
	],
	"authenticate": {"placement": "header", "name": "X-Api-Key", "value": "{{ apiKey }}"},
	"test": {"method": "GET", "url": "{{ baseUrl }}/v1/me"},
	"defaultDomainsFrom": "baseUrl"
}`

// acmeCredentialType decodes the fixture declaration.
func acmeCredentialType() nodepack.CredentialType {
	var declared nodepack.CredentialType
	if err := json.Unmarshal([]byte(acmeCredentialTypeJSON), &declared); err != nil {
		panic(err)
	}
	return declared
}

// acmePack is an action pack that declares and uses its own credential type.
func acmePack() *nodepack.Pack {
	pack := nodepack.Scaffold(nodepack.ScaffoldOptions{
		Type: "pack.acme", DisplayName: "Acme", CredentialType: "pack.acme.api",
	})
	pack.CredentialTypes = []nodepack.CredentialType{acmeCredentialType()}
	return pack
}

// acmeTriggerManifest is a trigger pack in a different directory that names the
// acme pack's credential type for its lifecycle request without declaring it.
const acmeTriggerManifest = `{
	"type": "pack.acmeTrigger", "version": 1,
	"displayName": "Acme Trigger", "category": "Triggers",
	"credentialType": "pack.acme.api",
	"trigger": {
		"events": ["message"], "catchAll": "other", "eventPath": "event", "shape": "bodyAsItem",
		"webhook": {"name": "default", "staticPath": "acme", "method": "POST"},
		"lifecycle": {"id": "acme.hooks", "set": {"method": "POST", "url": "{{ .baseUrl }}/v1/hooks", "credentialType": "pack.acme.api"}}
	}
}`

// packCredentialFixture is what installPackCredentialFixture registered into
// the process-wide credential registry, kept for the tests that drive it.
var packCredentialFixture dirInstalled

// installPackCredentialFixture loads the acme pack and the trigger pack that
// borrows its type through LoadDir into credentials.Default(), the way the
// server does at boot. It runs from TestMain only: Default is written at
// composition and read freely afterwards.
//
// The trigger's directory sorts first, so its lifecycle names a type declared
// by a pack the loader has not installed yet.
func installPackCredentialFixture() error {
	dir, err := os.MkdirTemp("", "nodepack-credential-types-")
	if err != nil {
		return err
	}
	if err := writeRawPackDir(filepath.Join(dir, "a-acme-trigger"), []byte(acmeTriggerManifest)); err != nil {
		return err
	}
	if err := nodepack.WritePackDir(filepath.Join(dir, "b-acme"), acmePack()); err != nil {
		return err
	}
	set := installDirDeps(safehttp.DefaultPolicy())
	deps := set.deps()
	deps.Credentials = credentials.Default()
	if err := nodepack.LoadDir(deps, dir); err != nil {
		return err
	}
	packCredentialFixture = set
	return os.RemoveAll(dir)
}

// writeRawPackDir writes manifest bytes as written, with their checksum.
func writeRawPackDir(packDir string, manifest []byte) error {
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(packDir, nodepack.ManifestName), manifest, 0o600); err != nil {
		return err
	}
	return nodepack.WriteChecksum(packDir)
}

// acmeStub records every request the acme service receives.
type acmeStub struct {
	mu    sync.Mutex
	calls []string
}

func (stub *acmeStub) record(r *http.Request) {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	stub.calls = append(stub.calls, r.Method+" "+r.URL.Path+" key="+r.Header.Get("X-Api-Key"))
}

func (stub *acmeStub) recorded() []string {
	stub.mu.Lock()
	defer stub.mu.Unlock()
	return append([]string(nil), stub.calls...)
}

// acmeCredential resolves the one acme credential the tests store.
type acmeCredential struct{ baseURL string }

func (stub acmeCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-acme", Name: "Acme", Type: "pack.acme.api",
		Fields: map[string]string{"baseUrl": stub.baseURL, "apiKey": "k-acme"},
	}, nil
}

// acmePolicy admits the loopback test server and nothing else private.
func acmePolicy(t *testing.T, serverURL string) safehttp.Policy {
	t.Helper()
	endpoint, err := url.Parse(serverURL)
	if err != nil {
		t.Fatalf("parse server URL: %v", err)
	}
	policy := safehttp.DefaultPolicy()
	policy.AllowedPrivateEndpoints = []string{endpoint.Host}
	return policy
}

// TestAPackCredentialTypeWorksWhereverABuiltInTypeDoes: once LoadDir registers
// a pack's type, every reader of the registry treats it as it would a built-in
// — the API listing, the routing interpreter, a lifecycle request in another
// pack, the credential test and the host scope.
func TestAPackCredentialTypeWorksWhereverABuiltInTypeDoes(t *testing.T) {
	t.Parallel()

	stub := &acmeStub{}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		stub.record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	t.Cleanup(server.Close)
	policy := acmePolicy(t, server.URL)

	t.Run("listed", func(t *testing.T) {
		var found *credentials.Definition
		for _, definition := range credentials.List() {
			if definition.ID == "pack.acme.api" {
				definition := definition
				found = &definition
			}
		}
		if found == nil {
			t.Fatal("credentials.List() does not contain pack.acme.api, so GET /credential-types would not list it")
		}
		secret := map[string]bool{}
		for _, field := range found.Fields {
			secret[field.Key] = field.Secret
		}
		want := map[string]bool{"baseUrl": false, "accountId": false, "apiKey": true}
		if !reflect.DeepEqual(secret, want) || found.DisplayName != "Acme API" || found.DefaultDomainsFrom != "baseUrl" {
			t.Fatalf("listed definition = %#v, want Acme API with secret flags %v", *found, want)
		}
	})

	t.Run("signs a routed request", func(t *testing.T) {
		set := packCredentialFixture
		executor := routing.NewExecutor(policy, set.routes, set.definitions)
		definition, found := set.definitions.Lookup("pack.acme", workflow.V(1))
		if !found {
			t.Fatal("pack.acme did not register")
		}
		before := len(stub.recorded())
		_, err := executor.Execute(context.Background(), workflow.IRNode{
			ID: "acme-1", Name: "Acme", Type: "pack.acme", TypeVersion: workflow.V(1),
			Parameters: map[string]any{
				"resource": "message", "operation": "sendMessage", "chatId": "@channel", "text": "hi",
			},
			Credentials: map[string]string{"pack.acme.api": "cred-acme"},
			Definition:  definition,
		}, workflow.NodeInput{}, engine.Request{Credentials: acmeCredential{baseURL: server.URL}})
		if err != nil {
			t.Fatalf("Execute() error = %v", err)
		}
		if !containsCall(stub.recorded()[before:], "POST /sendMessage key=k-acme") {
			t.Fatalf("calls = %#v, want POST /sendMessage signed with the pack type's header", stub.recorded()[before:])
		}
	})

	t.Run("signs a lifecycle request", func(t *testing.T) {
		lifecycle, found := packCredentialFixture.lifecycles.Lookup("acme.hooks")
		if !found {
			t.Fatal("the trigger pack's lifecycle did not register")
		}
		before := len(stub.recorded())
		err := lifecycle.Create(context.Background(), webhook.LifecycleContext{
			TenantID: "tenant-a", WorkflowID: "wf_1",
			Binding: repository.WebhookBinding{
				NodeID: "trigger", NodeType: "pack.acmeTrigger", Route: "abc123",
				Parameters: map[string]any{"$credentials": map[string]any{"pack.acme.api": "cred-acme"}},
			},
			PublicURL:   "https://flows.example.test/webhook/abc123",
			HTTP:        policy,
			Credentials: acmeCredential{baseURL: server.URL},
		})
		if err != nil {
			t.Fatalf("Create() error = %v", err)
		}
		if !containsCall(stub.recorded()[before:], "POST /v1/hooks key=k-acme") {
			t.Fatalf("calls = %#v, want POST /v1/hooks signed with the other pack's type", stub.recorded()[before:])
		}
	})

	t.Run("runs its test", func(t *testing.T) {
		credentialType, found := credentials.Default().Get("pack.acme.api")
		if !found {
			t.Fatal("pack.acme.api is not registered")
		}
		fields := map[string]string{"baseUrl": server.URL, "apiKey": "k-acme"}
		before := len(stub.recorded())
		if _, err := credentials.RunTest(context.Background(), credentialType,
			credentials.Record{Type: "pack.acme.api", Fields: fields}, fields, policy); err != nil {
			t.Fatalf("RunTest() error = %v", err)
		}
		if !containsCall(stub.recorded()[before:], "GET /v1/me key=k-acme") {
			t.Fatalf("calls = %#v, want the declared probe signed with the header", stub.recorded()[before:])
		}
	})

	t.Run("is scoped by its base URL", func(t *testing.T) {
		record := credentials.Record{Type: "pack.acme.api", Fields: map[string]string{"baseUrl": server.URL}}
		if record.Unscoped() {
			t.Fatal("a pack credential with a base URL reads as unscoped, so an embed would refuse it")
		}
		if got := record.EffectiveDomains(); !reflect.DeepEqual(got, []string{"127.0.0.1"}) {
			t.Fatalf("EffectiveDomains() = %v, want [127.0.0.1]", got)
		}
	})
}

func containsCall(calls []string, want string) bool {
	for _, call := range calls {
		if call == want {
			return true
		}
	}
	return false
}

// newBuiltinCredentialRegistry is a private registry holding only the built-in
// types, so a test can register into it without touching Default.
func newBuiltinCredentialRegistry(t *testing.T) *credentials.Registry {
	t.Helper()
	registry := credentials.NewRegistry()
	if err := credentials.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	return registry
}

// loadAcmePacks writes each pack under its directory name and loads them into
// registry with LoadDir.
func loadAcmePacks(t *testing.T, registry *credentials.Registry, packs map[string]*nodepack.Pack) error {
	t.Helper()
	dir := t.TempDir()
	for name, pack := range packs {
		if err := nodepack.WritePackDir(filepath.Join(dir, name), pack); err != nil {
			t.Fatalf("WritePackDir(%s) error = %v", name, err)
		}
	}
	deps := installDirDeps(safehttp.DefaultPolicy()).deps()
	deps.Credentials = registry
	return nodepack.LoadDir(deps, dir)
}

// TestLoadDirRegistersAPacksCredentialTypes: every part of the declaration
// lands in the registered type, with secret implying masked and masked alone
// staying readable.
func TestLoadDirRegistersAPacksCredentialTypes(t *testing.T) {
	t.Parallel()

	registry := newBuiltinCredentialRegistry(t)
	if err := loadAcmePacks(t, registry, map[string]*nodepack.Pack{"acme": acmePack()}); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	got, found := registry.Get("pack.acme.api")
	if !found {
		t.Fatal("LoadDir did not register pack.acme.api")
	}
	if got.DisplayName != "Acme API" || got.DefaultDomainsFrom != "baseUrl" || len(got.DefaultDomains) != 0 {
		t.Fatalf("type = %#v, want Acme API scoped from baseUrl", got)
	}
	if !reflect.DeepEqual(got.Secrets, []string{"apiKey"}) {
		t.Fatalf("Secrets = %v, want [apiKey]", got.Secrets)
	}
	want := credentials.Authentication{Placement: credentials.PlacementHeader, Name: "X-Api-Key", Value: "{{ apiKey }}"}
	if got.Authenticate == nil || *got.Authenticate != want {
		t.Fatalf("Authenticate = %#v, want %#v", got.Authenticate, want)
	}
	if got.Test == nil || got.Test.Method != "GET" || got.Test.URL != "{{ baseUrl }}/v1/me" {
		t.Fatalf("Test = %#v, want GET {{ baseUrl }}/v1/me", got.Test)
	}
	if got.NeverSentOverHTTP || got.Dial != nil {
		t.Fatalf("type = %#v, a pack may declare neither neverSentOverHttp nor dial", got)
	}
	if len(got.Properties) != 3 {
		t.Fatalf("Properties = %#v, want three fields", got.Properties)
	}
	masked := func(index int) bool {
		return got.Properties[index].TypeOptions != nil && got.Properties[index].TypeOptions.Password
	}
	baseURL, accountID, apiKey := got.Properties[0], got.Properties[1], got.Properties[2]
	if baseURL.Key != "baseUrl" || !baseURL.Required || baseURL.Default != "https://api.acme.example" || masked(0) {
		t.Fatalf("baseUrl = %#v, want a required, readable field with its default", baseURL)
	}
	if accountID.Key != "accountId" || accountID.Required || !masked(1) {
		t.Fatalf("accountId = %#v, want an optional masked field", accountID)
	}
	if apiKey.Key != "apiKey" || !apiKey.Required || !masked(2) {
		t.Fatalf("apiKey = %#v, want a required field masked because it is secret", apiKey)
	}
}

// TestLoadDirCredentialTypeCollisions is the boot-refusal matrix.
func TestLoadDirCredentialTypeCollisions(t *testing.T) {
	t.Parallel()

	secondVersion := func(change func(*nodepack.CredentialType)) *nodepack.Pack {
		pack := acmePack()
		pack.Version = workflow.V(2)
		if change != nil {
			change(&pack.CredentialTypes[0])
		}
		return pack
	}

	t.Run("two versions declaring it identically load it once", func(t *testing.T) {
		t.Parallel()
		registry := newBuiltinCredentialRegistry(t)
		before := len(registry.List())
		if err := loadAcmePacks(t, registry, map[string]*nodepack.Pack{"acme-v1": acmePack(), "acme-v2": secondVersion(nil)}); err != nil {
			t.Fatalf("LoadDir() error = %v", err)
		}
		if after := len(registry.List()); after != before+1 {
			t.Fatalf("registry grew by %d, want 1", after-before)
		}
	})

	t.Run("a changed declaration under the same id is refused", func(t *testing.T) {
		t.Parallel()
		err := loadAcmePacks(t, newBuiltinCredentialRegistry(t), map[string]*nodepack.Pack{
			"acme-v1": acmePack(),
			"acme-v2": secondVersion(func(declared *nodepack.CredentialType) { declared.Fields[0].Label = "Acme base URL" }),
		})
		if err == nil || !strings.Contains(err.Error(), `"acme-v1"`) || !strings.Contains(err.Error(), `"acme-v2"`) ||
			!strings.Contains(err.Error(), "immutable") {
			t.Fatalf("LoadDir() error = %v, want an immutability refusal naming both packs", err)
		}
	})

	t.Run("an id the deployment already registered is refused", func(t *testing.T) {
		t.Parallel()
		registry := newBuiltinCredentialRegistry(t)
		if err := registry.Register(credentials.Type{ID: "pack.acme.api", DisplayName: "Existing Acme"}); err != nil {
			t.Fatalf("Register() error = %v", err)
		}
		err := loadAcmePacks(t, registry, map[string]*nodepack.Pack{"acme": acmePack()})
		if err == nil || !strings.Contains(err.Error(), "already registered by this deployment") || !strings.Contains(err.Error(), "Existing Acme") {
			t.Fatalf("LoadDir() error = %v, want the existing registration named", err)
		}
	})

	t.Run("a server with no credential registry refuses the pack", func(t *testing.T) {
		t.Parallel()
		err := loadAcmePacks(t, nil, map[string]*nodepack.Pack{"acme": acmePack()})
		if err == nil || !strings.Contains(err.Error(), `load node pack "acme"`) || !strings.Contains(err.Error(), "no credential registry") {
			t.Fatalf("LoadDir() error = %v, want a refusal naming the pack", err)
		}
	})

	t.Run("an invalid declaration refuses the boot before anything installs", func(t *testing.T) {
		t.Parallel()
		pack := acmePack()
		pack.CredentialTypes[0].Authenticate.Value = "{{ token }}"
		registry := newBuiltinCredentialRegistry(t)
		err := loadAcmePacks(t, registry, map[string]*nodepack.Pack{"acme": pack})
		if err == nil || !strings.Contains(err.Error(), "$.credentialTypes[0].authenticate.value") {
			t.Fatalf("LoadDir() error = %v, want the declaration's problem at its path", err)
		}
		if _, found := registry.Get("pack.acme.api"); found {
			t.Fatal("an invalid declaration was registered")
		}
	})
}

// TestPackCredentialTypeRefusals: each rule is reported at its own JSON path.
func TestPackCredentialTypeRefusals(t *testing.T) {
	t.Parallel()

	if issues := nodepack.Validate(acmePack(), nodepack.ManifestName); len(issues) != 0 {
		t.Fatalf("Validate(acmePack) = %v, want clean", issues)
	}
	clean, err := json.Marshal(acmePack())
	if err != nil {
		t.Fatal(err)
	}
	if issues := nodepack.ValidateBytes(clean, nodepack.ManifestName); len(issues) != 0 {
		t.Fatalf("ValidateBytes(acmePack) = %v, want clean", issues)
	}

	first := func(pack *nodepack.Pack) *nodepack.CredentialType { return &pack.CredentialTypes[0] }
	for name, testCase := range map[string]struct {
		mutate func(*nodepack.Pack)
		path   string
		want   string
	}{
		"empty id":             {func(p *nodepack.Pack) { first(p).ID = "" }, "$.credentialTypes[0].id", "needs an id"},
		"id outside namespace": {func(p *nodepack.Pack) { first(p).ID = "acmeApi" }, "$.credentialTypes[0].id", `name it "pack.acme." followed by one word`},
		"id with two words":    {func(p *nodepack.Pack) { first(p).ID = "pack.acme.api.v2" }, "$.credentialTypes[0].id", "outside this pack's namespace"},
		"id too long":          {func(p *nodepack.Pack) { first(p).ID = "pack.acme." + strings.Repeat("a", 60) }, "$.credentialTypes[0].id", "at most 64"},
		"id declared twice":    {func(p *nodepack.Pack) { p.CredentialTypes = append(p.CredentialTypes, acmeCredentialType()) }, "$.credentialTypes[1].id", "twice"},
		"no display name":      {func(p *nodepack.Pack) { first(p).DisplayName = "" }, "$.credentialTypes[0].displayName", "display name"},
		"no fields": {func(p *nodepack.Pack) {
			first(p).Fields = nil
			first(p).Authenticate = nil
			first(p).Test = nil
			first(p).DefaultDomainsFrom = ""
		}, "$.credentialTypes[0].fields", "no fields"},
		"duplicate field":      {func(p *nodepack.Pack) { first(p).Fields[1].Key = "baseUrl" }, "$.credentialTypes[0].fields[1].key", "twice"},
		"bookkeeping key":      {func(p *nodepack.Pack) { first(p).Fields[1].Key = "$meta" }, "$.credentialTypes[0].fields[1].key", "may not start with $"},
		"no label":             {func(p *nodepack.Pack) { first(p).Fields[1].Label = "" }, "$.credentialTypes[0].fields[1].label", "needs a label"},
		"unsupported kind":     {func(p *nodepack.Pack) { first(p).Fields[1].Kind = "json" }, "$.credentialTypes[0].fields[1].kind", "string, number, boolean, options"},
		"options without list": {func(p *nodepack.Pack) { first(p).Fields[1].Kind = "options" }, "$.credentialTypes[0].fields[1].options", "declares no options"},
		"non-string default":   {func(p *nodepack.Pack) { first(p).Fields[1].Default = 5.0 }, "$.credentialTypes[0].fields[1].default", "not a string"},
		"unknown placement":    {func(p *nodepack.Pack) { first(p).Authenticate.Placement = "cookie" }, "$.credentialTypes[0].authenticate.placement", "custom"},
		"header without name":  {func(p *nodepack.Pack) { first(p).Authenticate.Name = "" }, "$.credentialTypes[0].authenticate.name", "needs a name"},
		"undeclared marker":    {func(p *nodepack.Pack) { first(p).Authenticate.Value = "{{ token }}" }, "$.credentialTypes[0].authenticate.value", "{{ token }} names no field"},
		"basic user undeclared": {func(p *nodepack.Pack) {
			*first(p).Authenticate = credentials.Authentication{Placement: "basicAuth", User: "user", Password: "apiKey"}
		}, "$.credentialTypes[0].authenticate.user", `"user" does not`},
		"test without url":   {func(p *nodepack.Pack) { first(p).Test.URL = "" }, "$.credentialTypes[0].test.url", "needs a url"},
		"test marker":        {func(p *nodepack.Pack) { first(p).Test.URL = "{{ host }}/me" }, "$.credentialTypes[0].test.url", "{{ host }} names no field"},
		"test status":        {func(p *nodepack.Pack) { first(p).Test.ExpectStatusBelow = 700 }, "$.credentialTypes[0].test.expectStatusBelow", "between 0 and 599"},
		"scope from secret":  {func(p *nodepack.Pack) { first(p).DefaultDomainsFrom = "apiKey" }, "$.credentialTypes[0].defaultDomainsFrom", "secret"},
		"scope from nothing": {func(p *nodepack.Pack) { first(p).DefaultDomainsFrom = "host" }, "$.credentialTypes[0].defaultDomainsFrom", "not a field"},
		"both scopes":        {func(p *nodepack.Pack) { first(p).DefaultDomains = []string{"api.acme.example"} }, "$.credentialTypes[0].defaultDomains", "not both"},
		"domain with a path": {func(p *nodepack.Pack) {
			first(p).DefaultDomainsFrom = ""
			first(p).DefaultDomains = []string{"api.acme.example/v1"}
		}, "$.credentialTypes[0].defaultDomains[0]", "host name"},
		"own type undeclared":   {func(p *nodepack.Pack) { p.CredentialType = "pack.acme.other" }, "$.credentialType", "must declare it under credentialTypes"},
		"readable header value": {func(p *nodepack.Pack) { first(p).Authenticate.Value = "{{ accountId }}" }, "$.credentialTypes[0].authenticate.value", `field "accountId" signs with its value, so it must be declared secret`},
		"readable bearer value": {func(p *nodepack.Pack) {
			*first(p).Authenticate = credentials.Authentication{Placement: "bearer", Value: "{{ baseUrl }}"}
		}, "$.credentialTypes[0].authenticate.value", `field "baseUrl" signs with its value`},
		"readable basic password": {func(p *nodepack.Pack) {
			*first(p).Authenticate = credentials.Authentication{Placement: "basicAuth", User: "accountId", Password: "baseUrl"}
		}, "$.credentialTypes[0].authenticate.password", `field "baseUrl" signs with its value`},
		"custom value not JSON": {func(p *nodepack.Pack) {
			*first(p).Authenticate = credentials.Authentication{Placement: "custom", Value: "{{ apiKey }}"}
		}, "$.credentialTypes[0].authenticate.value", "must be a JSON object"},
		"custom extra key": {func(p *nodepack.Pack) {
			*first(p).Authenticate = credentials.Authentication{Placement: "custom", Value: `{"headers": {"X-Key": "{{ apiKey }}"}, "body": "x"}`}
		}, "$.credentialTypes[0].authenticate.value", `only headers and qs, not "body"`},
		"test method":         {func(p *nodepack.Pack) { first(p).Test.Method = "DELETE" }, "$.credentialTypes[0].test.method", "GET, HEAD or POST"},
		"wildcard over a TLD": {func(p *nodepack.Pack) { first(p).DefaultDomainsFrom = ""; first(p).DefaultDomains = []string{"*.com"} }, "$.credentialTypes[0].defaultDomains[0]", "whole top-level domain"},
		"domain with a port": {func(p *nodepack.Pack) {
			first(p).DefaultDomainsFrom = ""
			first(p).DefaultDomains = []string{"api.acme.example:443"}
		}, "$.credentialTypes[0].defaultDomains[0]", "port"},
		"domain with scheme": {func(p *nodepack.Pack) {
			first(p).DefaultDomainsFrom = ""
			first(p).DefaultDomains = []string{"https://api.acme.example"}
		}, "$.credentialTypes[0].defaultDomains[0]", "scheme"},
		"domain with space": {func(p *nodepack.Pack) {
			first(p).DefaultDomainsFrom = ""
			first(p).DefaultDomains = []string{"api acme.example"}
		}, "$.credentialTypes[0].defaultDomains[0]", "space"},
		"wildcard over an IP": {func(p *nodepack.Pack) {
			first(p).DefaultDomainsFrom = ""
			first(p).DefaultDomains = []string{"*.10.0.0.1"}
		}, "$.credentialTypes[0].defaultDomains[0]", "IP address"},
		"pack without a type":   {func(p *nodepack.Pack) { p.Type = "" }, "$.credentialTypes", "needs a type"},
		"pack type with spaces": {func(p *nodepack.Pack) { p.Type = " pack.acme" }, "$.credentialTypes", "needs a type without surrounding spaces"},
		"reserved pack type":    {func(p *nodepack.Pack) { p.Type = node.BuiltinPrefix + "acme" }, "$.credentialTypes", "reserved"},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pack := acmePack()
			testCase.mutate(pack)
			assertPackIssue(t, nodepack.Validate(pack, nodepack.ManifestName), testCase.path, testCase.want)
		})
	}

	for name, testCase := range map[string]struct {
		mutate func(declared map[string]any)
		path   string
		want   string
	}{
		"type-level secrets list":    {func(d map[string]any) { d["secrets"] = []any{"apiKey"} }, "$.credentialTypes[0]", `unknown field "secrets"`},
		"masked flag on a field":     {func(d map[string]any) { d["fields"].([]any)[1].(map[string]any)["masked"] = true }, "$.credentialTypes[0].fields[1]", `unknown field "masked"`},
		"header key in authenticate": {func(d map[string]any) { d["authenticate"].(map[string]any)["header"] = "X-Api-Key" }, "$.credentialTypes[0].authenticate", `unknown field "header"`},
		"unknown type option": {func(d map[string]any) {
			d["fields"].([]any)[1].(map[string]any)["typeOptions"] = map[string]any{"hidden": true}
		}, "$.credentialTypes[0].fields[1].typeOptions", "hidden"},
		"dial probe": {func(d map[string]any) { d["dial"] = map[string]any{"protocol": "smtp"} }, "$.credentialTypes[0]", `unknown field "dial"`},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			var manifest map[string]any
			if err := json.Unmarshal(clean, &manifest); err != nil {
				t.Fatal(err)
			}
			testCase.mutate(manifest["credentialTypes"].([]any)[0].(map[string]any))
			raw, err := json.Marshal(manifest)
			if err != nil {
				t.Fatal(err)
			}
			assertPackIssue(t, nodepack.ValidateBytes(raw, nodepack.ManifestName), testCase.path, testCase.want)
		})
	}
}

// TestPackCredentialTypeShapesThatAreAccepted: the rules refuse what is unsafe
// and nothing else.
func TestPackCredentialTypeShapesThatAreAccepted(t *testing.T) {
	t.Parallel()

	for name, mutate := range map[string]func(*nodepack.Pack){
		"a subdomain wildcard": func(p *nodepack.Pack) {
			p.CredentialTypes[0].DefaultDomainsFrom = ""
			p.CredentialTypes[0].DefaultDomains = []string{"*.acme.example", "api.acme.example"}
		},
		"a custom template of headers and qs": func(p *nodepack.Pack) {
			*p.CredentialTypes[0].Authenticate = credentials.Authentication{Placement: "custom", Value: `{"headers": {"X-Key": "{{ apiKey }}"}, "qs": {"key": "{{ apiKey }}"}}`}
		},
		"basic auth with a readable user": func(p *nodepack.Pack) {
			*p.CredentialTypes[0].Authenticate = credentials.Authentication{Placement: "basicAuth", User: "accountId", Password: "apiKey"}
		},
		"a header name templated over a readable field": func(p *nodepack.Pack) {
			p.CredentialTypes[0].Authenticate.Name = "X-{{ accountId }}"
		},
		"a HEAD probe": func(p *nodepack.Pack) { p.CredentialTypes[0].Test.Method = "head" },
		// A pack whose type is a prefix of a longer name only owns one word after
		// its own type, so `pack` may name `pack.acme.api` without declaring it.
		"a deeper name outside the one-word namespace": func(p *nodepack.Pack) {
			p.Type = "pack"
			p.CredentialType = "pack.acme.api"
			p.CredentialTypes = nil
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			pack := acmePack()
			mutate(pack)
			if issues := nodepack.Validate(pack, nodepack.ManifestName); len(issues) != 0 {
				t.Fatalf("Validate() = %v, want clean", issues)
			}
		})
	}
}

// TestATriggerSecretFromAPackTypeMustBeSecret: the HMAC signing secret is read
// out of a credential field, and a readable field would hand it to anyone who
// can read the credential.
func TestATriggerSecretFromAPackTypeMustBeSecret(t *testing.T) {
	t.Parallel()

	manifest := func(field string) []byte {
		return []byte(`{
			"type": "pack.acmeSigned", "version": 1, "displayName": "Acme Signed", "category": "Triggers",
			"credentialType": "pack.acmeSigned.api",
			"credentialTypes": [{"id": "pack.acmeSigned.api", "displayName": "Acme signing", "fields": [
				{"key": "accountId", "label": "Account ID", "kind": "string"},
				{"key": "signingSecret", "label": "Signing secret", "kind": "string", "secret": true}
			]}],
			"trigger": {
				"events": ["message"], "catchAll": "other", "eventPath": "event", "shape": "bodyAsItem",
				"webhook": {"name": "default", "staticPath": "acme-signed", "method": "POST"},
				"hmac": {"header": "X-Signature", "algorithm": "sha256", "secretCredential": {"type": "pack.acmeSigned.api", "field": "` + field + `"}}
			}
		}`)
	}
	if issues := nodepack.ValidateBytes(manifest("signingSecret"), nodepack.ManifestName); len(issues) != 0 {
		t.Fatalf("ValidateBytes(secret field) = %v, want clean", issues)
	}
	assertPackIssue(t, nodepack.ValidateBytes(manifest("accountId"), nodepack.ManifestName),
		"$.trigger.hmac.secretCredential.field", `field "accountId" signs with its value`)
	assertPackIssue(t, nodepack.ValidateBytes(manifest("missing"), nodepack.ManifestName),
		"$.trigger.hmac.secretCredential.field", "is not declared")
}

func assertPackIssue(t *testing.T, issues []nodepack.Issue, path, want string) {
	t.Helper()
	for _, issue := range issues {
		if issue.Path == path && strings.Contains(issue.Message, want) {
			return
		}
	}
	t.Fatalf("issues = %v, want one at %s containing %q", issues, path, want)
}

// TestNoBuiltinCredentialTypeIDCanCollideWithAPackNamespace: a pack's ID is
// always `<pack type>.<word>` and so always holds a dot. A built-in ID with a
// dot could one day equal a pack's, and the namespace would stop being a
// guarantee.
func TestNoBuiltinCredentialTypeIDCanCollideWithAPackNamespace(t *testing.T) {
	t.Parallel()

	for _, credentialType := range newBuiltinCredentialRegistry(t).List() {
		if strings.Contains(credentialType.ID, ".") {
			t.Errorf("built-in credential type %q contains a dot, so a node pack's namespaced type could collide with it", credentialType.ID)
		}
	}
	if strings.HasPrefix("pack.acme.api", node.BuiltinPrefix) {
		t.Fatalf("the fixture id sits in the reserved %q namespace", node.BuiltinPrefix)
	}
}
