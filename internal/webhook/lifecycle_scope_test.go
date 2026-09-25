package webhook_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// scopedLifecycleCredential resolves one fixed credential, so a test can set
// its type and its allowed domains.
type scopedLifecycleCredential struct{ credential engine.Credential }

func (resolver scopedLifecycleCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return resolver.credential, nil
}

// scopedLifecycleContext is a trigger whose lifecycle registers with the
// given credential, under a policy that admits loopback, so
// nothing but the credential's own checks can stop a request.
func scopedLifecycleContext(credential engine.Credential) webhook.LifecycleContext {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return webhook.LifecycleContext{
		TenantID: "tenant-a", WorkflowID: "wf_1",
		Binding: repository.WebhookBinding{
			NodeID: "trigger", NodeType: "pack.stubTrigger", Route: "abc123",
			Parameters: map[string]any{"$credentials": map[string]any{"wahaApi": "cred-1"}},
		},
		PublicURL:   "https://flows.example.test/webhook/abc123",
		HTTP:        policy,
		Credentials: scopedLifecycleCredential{credential: credential},
	}
}

func scopedLifecycle() webhook.RequestLifecycle {
	return webhook.RequestLifecycle{Set: &webhook.RequestDescriptor{
		Method: "POST", URL: "{{ .baseUrl }}/api/webhooks", Body: `{"url": "{{ .PublicURL }}"}`, CredentialType: "wahaApi",
	}}
}

// BUG-0bzsa1: a lifecycle call applied the credential with no host check, no
// redirect scope and no type check — the three things the engine checks before
// a node's request carries a secret. It now goes through the same path.
func TestALifecycleCallAppliesTheCredentialThroughTheEnginesChecks(t *testing.T) {
	t.Parallel()

	var mu sync.Mutex
	var calls []http.Header
	record := func(r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		calls = append(calls, r.Header.Clone())
	}
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		_, _ = w.Write([]byte(`{}`))
	}))
	defer destination.Close()
	stub := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{}`))
	}))
	defer stub.Close()
	redirector := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		record(r)
		http.Redirect(w, r, strings.Replace(destination.URL, "127.0.0.1", "localhost", 1)+"/stolen", http.StatusTemporaryRedirect)
	}))
	defer redirector.Close()
	taken := func() []http.Header {
		mu.Lock()
		defer mu.Unlock()
		out := calls
		calls = nil
		return out
	}

	waha := func(baseURL string, domains ...string) engine.Credential {
		return engine.Credential{
			ID: "cred-1", Name: "WAHA", Type: "wahaApi", AllowedDomains: domains,
			Fields: map[string]string{"baseUrl": baseURL, "apiKey": "LIFECYCLE-SECRET"},
		}
	}

	// The host check: a credential scoped elsewhere is never sent here.
	err := scopedLifecycle().Create(context.Background(), scopedLifecycleContext(waha(stub.URL, "waha.example.test")))
	if err == nil || !strings.Contains(err.Error(), "not allowed for host") {
		t.Errorf("Create() with an out-of-scope credential = %v, want a host refusal", err)
	}
	if got := taken(); len(got) != 0 {
		t.Errorf("the service was called %d times despite the refusal", len(got))
	}

	// The type check: a credential of another type is refused before it is
	// read into a template or placed on a request.
	wrong := waha(stub.URL)
	wrong.Type = "httpHeaderAuth"
	err = scopedLifecycle().Create(context.Background(), scopedLifecycleContext(wrong))
	if err == nil || !strings.Contains(err.Error(), "not wahaApi") {
		t.Errorf("Create() with a credential of another type = %v, want a type refusal", err)
	}
	if got := taken(); len(got) != 0 {
		t.Errorf("the service was called %d times despite the refusal", len(got))
	}

	// The redirect scope: a credential naming no domains stays on the host it
	// was sent to, so a redirect to another host is not followed with the key.
	err = scopedLifecycle().Create(context.Background(), scopedLifecycleContext(waha(redirector.URL)))
	got := taken()
	if len(got) != 1 || got[0].Get("X-Api-Key") != "LIFECYCLE-SECRET" {
		t.Fatalf("calls = %v (err %v), want exactly the first hop, signed", got, err)
	}

	// And a credential in scope still registers.
	if err := scopedLifecycle().Create(context.Background(), scopedLifecycleContext(waha(stub.URL, "127.0.0.1"))); err != nil {
		t.Errorf("Create() with an in-scope credential error = %v", err)
	}
	if got := taken(); len(got) != 1 || got[0].Get("X-Api-Key") != "LIFECYCLE-SECRET" {
		t.Errorf("calls = %v, want one signed registration", got)
	}
}
