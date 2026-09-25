package nodes

import (
	"context"
	"crypto/tls"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/ai"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// These tests reach into the model and embeddings clients to trust an
// httptest TLS server's certificate. That is the only thing they change: the
// clients are otherwise the ones the executors build, redirect check included.

// fixedModelCredential resolves one API-key credential for any ID.
type fixedModelCredential struct{ credential engine.Credential }

func (resolver fixedModelCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return resolver.credential, nil
}

// A generic bearer credential rather than an OpenAI one: an OpenAI key with no
// domains is held to api.openai.com by its type's default, so it could never
// reach these loopback servers, and the "no domains named" redirect rule these
// tests pin applies only to a credential whose type has no default either.
func modelKeyCredential(domains ...string) engine.Request {
	return engine.Request{Credentials: fixedModelCredential{credential: engine.Credential{
		ID: "cred-1", Name: "Model key", Type: BearerCredentialType, AllowedDomains: domains,
		Fields: map[string]string{"token": "MODEL-KEY-SECRET"},
	}}}
}

// keyCatcher is a plain-http server that records every Authorization header it
// is sent. The redirect target in each case: whatever reaches it is a leak.
type keyCatcher struct {
	mu     sync.Mutex
	seen   []string
	server *httptest.Server
}

func newKeyCatcher(t *testing.T) *keyCatcher {
	t.Helper()
	catcher := &keyCatcher{}
	catcher.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		catcher.mu.Lock()
		catcher.seen = append(catcher.seen, r.Header.Get("Authorization"))
		catcher.mu.Unlock()
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"stolen"}}],"data":[{"index":0,"embedding":[1]}]}`))
	}))
	t.Cleanup(catcher.server.Close)
	return catcher
}

// leaked takes what reached the catcher since it was last asked, so each case
// is judged on its own requests. Anything at all is a leak: across a host
// change Go drops the key itself, but the prompt or the texts still went
// somewhere the credential never named.
func (catcher *keyCatcher) leaked() []string {
	catcher.mu.Lock()
	defer catcher.mu.Unlock()
	taken := catcher.seen
	catcher.seen = nil
	return taken
}

// redirectingOrigin answers every request with a 307 to target, which keeps
// the method, the body and — on a same-host hop — the Authorization header.
func redirectingOrigin(t *testing.T, secure bool, target string) *httptest.Server {
	t.Helper()
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target+r.URL.Path, http.StatusTemporaryRedirect)
	})
	var origin *httptest.Server
	if secure {
		origin = httptest.NewTLSServer(handler)
	} else {
		origin = httptest.NewServer(handler)
	}
	t.Cleanup(origin.Close)
	return origin
}

// trustOrigin makes client trust origin's certificate, if it has one.
func trustOrigin(client *http.Client, origin *httptest.Server) {
	if origin.TLS == nil {
		return
	}
	transport := client.Transport.(*http.Transport)
	transport.TLSClientConfig = origin.Client().Transport.(*http.Transport).TLSClientConfig.Clone()
	transport.TLSClientConfig.MinVersion = tls.VersionTLS12
}

func loopbackModelPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// redirectCases are the two hops BUG-0bzsa1 closes, from a model or
// embeddings base URL: https down to plain http on the same host, where Go
// keeps the Authorization header and the key would travel in the clear; and a
// credential naming no domains redirected to another host.
func redirectCases(t *testing.T, catcher *keyCatcher) map[string]*httptest.Server {
	return map[string]*httptest.Server{
		"https to http on the same host": redirectingOrigin(t, true, catcher.server.URL),
		"another host, no domains named": redirectingOrigin(t, false, strings.Replace(catcher.server.URL, "127.0.0.1", "localhost", 1)),
	}
}

func TestAChatModelKeyDoesNotFollowADowngradeOrACrossHostRedirect(t *testing.T) {
	t.Parallel()

	catcher := newKeyCatcher(t)
	for name, origin := range redirectCases(t, catcher) {
		backend := newModelBackend(loopbackModelPolicy())
		trustOrigin(backend.client, origin)
		resolved, err := backend.resolveModel(context.Background(), "Chat Model", map[string]any{
			"credentialId": "cred-1", "baseUrl": origin.URL + "/v1", "model": "gpt-test",
		}, modelKeyCredential())
		if err != nil {
			t.Fatalf("%s: resolveModel() error = %v", name, err)
		}
		_, _ = resolved.model.Complete(context.Background(), ai.ModelRequest{
			Model: "gpt-test", Messages: []ai.Message{{Role: "user", Content: "hi"}},
		})
		if leaked := catcher.leaked(); len(leaked) != 0 {
			t.Errorf("%s: the redirect target received the model call: %q", name, leaked)
		}
	}
}

func TestAnEmbeddingsKeyDoesNotFollowADowngradeOrACrossHostRedirect(t *testing.T) {
	t.Parallel()

	catcher := newKeyCatcher(t)
	for name, origin := range redirectCases(t, catcher) {
		executor := NewEmbeddingsExecutor(loopbackModelPolicy(), nil)
		trustOrigin(executor.client, origin)
		_, _ = executor.EmbedFromDescriptor(context.Background(), "Embeddings", map[string]any{
			"credentialId": "cred-1", "baseUrl": origin.URL + "/v1", "model": "text-embedding-test",
		}, modelKeyCredential(), []string{"hello"})
		if leaked := catcher.leaked(); len(leaked) != 0 {
			t.Errorf("%s: the redirect target received the embeddings call: %q", name, leaked)
		}
	}
}

// The scope is a bound, not a ban: a model endpoint that redirects within its
// own host over https still answers.
func TestAChatModelStillFollowsASameHostHTTPSRedirect(t *testing.T) {
	t.Parallel()

	var seen string
	server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/old/chat/completions" {
			http.Redirect(w, r, "/v1/chat/completions", http.StatusTemporaryRedirect)
			return
		}
		seen = r.Header.Get("Authorization")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"role":"assistant","content":"hello"}}]}`))
	}))
	defer server.Close()

	backend := newModelBackend(loopbackModelPolicy())
	trustOrigin(backend.client, server)
	resolved, err := backend.resolveModel(context.Background(), "Chat Model", map[string]any{
		"credentialId": "cred-1", "baseUrl": server.URL + "/old", "model": "gpt-test",
	}, modelKeyCredential())
	if err != nil {
		t.Fatalf("resolveModel() error = %v", err)
	}
	response, err := resolved.model.Complete(context.Background(), ai.ModelRequest{
		Model: "gpt-test", Messages: []ai.Message{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("Complete() error = %v", err)
	}
	if response.Message.Content != "hello" || seen != "Bearer MODEL-KEY-SECRET" {
		t.Errorf("response %#v, key %q; want the same-host https redirect followed", response, seen)
	}
}
