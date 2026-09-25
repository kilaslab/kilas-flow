package api_test

import (
	"bytes"
	"encoding/json"
	"net"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
)

// countingListener accepts connections, holds them without a word, and counts
// them: a probe that should have been refused before dialling shows up here.
func countingListener(t *testing.T) (string, *atomic.Int64) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	var accepted atomic.Int64
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			t.Cleanup(func() { _ = connection.Close() })
		}
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	return port, &accepted
}

func jsonRequest(t *testing.T, method, path string, body any) *http.Request {
	t.Helper()
	encoded, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("marshal request body = %v", err)
	}
	request := httptest.NewRequest(method, path, bytes.NewReader(encoded))
	request.Header.Set("Content-Type", "application/json")
	return request
}

// probeAPI is the credential surface with loopback reachable on exactly one
// port, so a probe that gets past the checks under test actually dials.
func probeAPI(t *testing.T, port string) http.Handler {
	t.Helper()
	handler, _ := credentialAPI(t, api.Deps{
		Config: withCredentialTestTimeout(300 * time.Millisecond),
		DatabaseGuard: sqlnode.Guard{Policy: safehttp.Policy{
			AllowedPrivateEndpoints: []string{"127.0.0.1:" + port, "127.0.0.2:" + port},
		}},
	})
	return handler
}

// The exploit: aim an edit at a host of one's own choosing, leave the password
// as the placeholder, and name the stored credential. The stored password
// would travel to the new host. A placeholder stands for a secret bound to the
// stored target, so a changed target refuses the merge.
func TestAPlaceholderIsNotMergedIntoAnEditThatMovesTheTarget(t *testing.T) {
	port, accepted := countingListener(t)
	handler := probeAPI(t, port)
	stored := storeCredential(t, handler, "Corp", "postgres", map[string]string{
		"host": "127.0.0.1", "port": port, "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})

	for name, moved := range map[string]map[string]string{
		"host": {"host": "127.0.0.2", "port": port},
		"port": {"host": "127.0.0.1", "port": "1"},
	} {
		t.Run(name, func(t *testing.T) {
			fields := map[string]string{
				"database": "app", "user": "ada", "password": credentials.RedactedValue, "sslMode": "disable",
			}
			for key, value := range moved {
				fields[key] = value
			}
			requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
				"credentialId": stored.ID, "fields": fields,
			}, http.StatusUnprocessableEntity)
		})
	}
	if got := accepted.Load(); got != 0 {
		t.Fatalf("the refused probes dialled %d times", got)
	}

	// The same edit with the password typed in is the caller's own secret,
	// and is tested like any unsaved credential.
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": stored.ID,
		"fields": map[string]string{
			"host": "127.0.0.2", "port": port, "database": "app",
			"user": "ada", "password": "typed-again", "sslMode": "disable",
		},
	}, http.StatusOK)
	if len(verdict.ResolvedFromStorage) != 0 {
		t.Errorf("resolvedFromStorage = %#v, want nothing taken from storage", verdict.ResolvedFromStorage)
	}
}

// The stored scope is where the stored secret may go. A test that merges that
// secret is held to it, whatever scope — or none — the body sends.
func TestAPayloadTestThatMergesAStoredSecretIsHeldToTheStoredScope(t *testing.T) {
	port, accepted := countingListener(t)
	handler := probeAPI(t, port)
	// A credential whose own host is outside its scope is exactly the one a
	// probe must refuse: the scope is what the operator promised.
	stored := requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Corp", "type": "postgres",
		"fields": map[string]string{
			"host": "127.0.0.1", "port": port, "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
		"allowedDomains": []string{"db.corp.test"},
	}, http.StatusCreated)
	fields := map[string]string{
		"host": "127.0.0.1", "port": port, "database": "other",
		"user": "ada", "password": credentials.RedactedValue, "sslMode": "disable",
	}

	// No scope in the body used to mean unrestricted.
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": stored.ID, "fields": fields,
	}, http.StatusOK)
	if verdict.OK {
		t.Fatalf("verdict = %#v, want the stored scope to refuse 127.0.0.1", verdict)
	}

	// A body scope naming the target does not widen the stored one: the two
	// share no host, and an empty intersection is not "unrestricted".
	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": stored.ID, "fields": fields, "allowedDomains": []string{"127.0.0.1"},
	}, http.StatusUnprocessableEntity)

	if got := accepted.Load(); got != 0 {
		t.Fatalf("a probe outside the stored scope dialled %d times", got)
	}
}

// A credentialId is checked before it is used as the claim key. Unchecked, a
// random id per request was a fresh key per request, and the one-at-a-time
// limit held nothing back.
func TestAPayloadTestNamingAnUnknownCredentialIsRefusedBeforeItRuns(t *testing.T) {
	port, accepted := countingListener(t)
	handler := probeAPI(t, port)
	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": "cred_does_not_exist",
		"fields": map[string]string{
			"host": "127.0.0.1", "port": port, "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
	}, http.StatusNotFound)

	other := storeCredential(t, handler, "Token", "httpBearerAuth", map[string]string{"token": "keep-me"})
	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": other.ID,
		"fields": map[string]string{
			"host": "127.0.0.1", "port": port, "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
	}, http.StatusUnprocessableEntity)
	if got := accepted.Load(); got != 0 {
		t.Fatalf("a refused probe dialled %d times", got)
	}
}

// Different credentials may be tested side by side, but not without bound:
// one tenant cannot hold more than a handful of probes open at once, whatever
// it names them.
func TestATenantCannotRunUnboundedConcurrentTests(t *testing.T) {
	port, _ := countingListener(t)
	handler, _ := credentialAPI(t, api.Deps{
		Config: withCredentialTestTimeout(2 * time.Second),
		DatabaseGuard: sqlnode.Guard{Policy: safehttp.Policy{
			AllowedPrivateEndpoints: []string{"127.0.0.1:" + port},
		}},
	})
	const limit = 4
	ids := make([]string, 0, limit+1)
	for index := range limit + 1 {
		stored := storeCredential(t, handler, "Silent "+string(rune('a'+index)), "postgres", map[string]string{
			"host": "127.0.0.1", "port": port, "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		})
		ids = append(ids, stored.ID)
	}

	var running sync.WaitGroup
	for _, id := range ids[:limit] {
		running.Add(1)
		go func() {
			defer running.Done()
			handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+id+"/test", nil))
		}()
	}
	// Every one of them is now hanging on a target that never answers.
	time.Sleep(300 * time.Millisecond)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+ids[limit]+"/test", nil))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("test past the tenant's limit: status = %d, want 429 (body: %s)", recorder.Code, recorder.Body)
	}
	// The unsaved route counts against the same limit.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, jsonRequest(t, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"fields": map[string]string{
			"host": "127.0.0.1", "port": port, "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
	}))
	if recorder.Code != http.StatusTooManyRequests {
		t.Fatalf("payload test past the tenant's limit: status = %d, want 429 (body: %s)", recorder.Code, recorder.Body)
	}

	running.Wait()
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+ids[limit]+"/test", nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status once the others finished = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
}
