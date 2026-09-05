package api_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/api"
	"github.com/kilaslabs/kilas-flow/internal/config"
	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/database"
	"github.com/kilaslabs/kilas-flow/internal/embed"
	"github.com/kilaslabs/kilas-flow/internal/repository"
	"github.com/kilaslabs/kilas-flow/internal/sqlnode"
)

// testCredentialResource mirrors the endpoint's verdict.
type testCredentialResource struct {
	OK                  bool     `json:"ok"`
	Detail              string   `json:"detail"`
	ResolvedFromStorage []string `json:"resolvedFromStorage"`
	Untestable          bool     `json:"untestable"`
}

// credentialAPI builds the credential surface with the deployment decisions a
// test needs to vary — the guard the executors get and the probe's deadline.
func credentialAPI(t *testing.T, deps api.Deps) (http.Handler, *repository.GORMCredentialStore) {
	t.Helper()
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "credentials.db"),
	}, slog.New(slog.NewTextHandler(io.Discard, nil)))
	if err != nil {
		t.Fatalf("database.Open() error = %v", err)
	}
	t.Cleanup(func() { _ = db.Close() })
	if err := database.Migrate(db, slog.New(slog.NewTextHandler(io.Discard, nil))); err != nil {
		t.Fatalf("database.Migrate() error = %v", err)
	}
	key := make([]byte, credentials.KeySize)
	for index := range key {
		key[index] = byte(index + 1)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	store := repository.NewCredentialStore(db.DB, cipher)
	deps.DB = db
	deps.Credentials = store
	return newTestServer(t, deps), store
}

func storeCredential(t *testing.T, handler http.Handler, name, credentialType string, fields map[string]string) credentialResource {
	t.Helper()
	return requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": name, "type": credentialType, "fields": fields,
	}, http.StatusCreated)
}

func TestADatabaseCredentialIsTestedByOpeningAConnection(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	// SQLite creates the file on connect, so a writable directory is a
	// reachable target and a directory that does not exist is not.
	reachable := storeCredential(t, handler, "Workflow database", "sqlite", map[string]string{
		"path": filepath.Join(t.TempDir(), "workflow.db"),
	})
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credentials/"+reachable.ID+"/test", nil, http.StatusOK)
	if !verdict.OK {
		t.Fatalf("verdict = %#v, want a reachable database", verdict)
	}

	// All three database types, so none of them can quietly fall through to the
	// HTTP probe — which has no URL to fetch and would report "no test defined"
	// for a credential this server can perfectly well check.
	for credentialType, fields := range map[string]map[string]string{
		"sqlite": {"path": filepath.Join(t.TempDir(), "no", "such", "directory", "workflow.db")},
		"postgres": {
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
		"mysql": {
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": "hunter2",
		},
	} {
		t.Run(credentialType, func(t *testing.T) {
			unreachable := storeCredential(t, handler, "Missing "+credentialType, credentialType, fields)
			// An unreachable target is a successful request with a negative
			// verdict: the API did its job and the database did not, which a
			// 502 would confuse.
			verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
				"/api/v1/credentials/"+unreachable.ID+"/test", nil, http.StatusOK)
			if verdict.OK || verdict.Detail == "" {
				t.Fatalf("verdict = %#v, want a reasoned failure", verdict)
			}
			if verdict.Untestable {
				t.Error("a database that would not open was reported as untestable rather than unreachable")
			}
		})
	}
}

func TestAFailingDatabaseTestNeverEchoesTheCredential(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})

	// Port 1 refuses, and both drivers echo their DSN — password included — in
	// the error they return for that.
	stored := storeCredential(t, handler, "Remote", "postgres", map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})
	request := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+stored.ID+"/test", nil)
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	// The whole body, not just the field a decoder would reach: a leak that
	// landed anywhere else in the response would still be a leak.
	if strings.Contains(recorder.Body.String(), "hunter2") {
		t.Fatalf("the verdict leaked the password: %s", recorder.Body)
	}
	var verdict testCredentialResource
	if err := json.Unmarshal(recorder.Body.Bytes(), &verdict); err != nil {
		t.Fatalf("decode verdict = %v", err)
	}
	if verdict.OK || verdict.Detail == "" {
		t.Fatalf("verdict = %#v, want a reasoned failure", verdict)
	}
}

func TestTheTestEndpointRefusesWhatTheExecutorsWouldRefuse(t *testing.T) {
	directory := t.TempDir()
	internal := filepath.Join(directory, "kilasflow.db")
	if err := os.WriteFile(internal, []byte("internal"), 0o600); err != nil {
		t.Fatalf("write internal database: %v", err)
	}
	// The same guard the database executors receive. A probe held to a laxer
	// one would report reachable for a path a node then refuses — and this
	// particular path is every credential in the installation.
	handler, _ := credentialAPI(t, api.Deps{DatabaseGuard: sqlnode.Guard{InternalPaths: []string{internal}}})

	stored := storeCredential(t, handler, "Sneaky", "sqlite", map[string]string{"path": internal})
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credentials/"+stored.ID+"/test", nil, http.StatusOK)
	if verdict.OK {
		t.Fatal("the test endpoint opened KilasFlow's own database")
	}
	if !strings.Contains(verdict.Detail, "KilasFlow's own database") {
		t.Errorf("detail = %q, want the guard's explanation", verdict.Detail)
	}
}

func TestAnUnsavedEditIsTestedAgainstItsStoredSecrets(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	stored := storeCredential(t, handler, "Remote", "postgres", map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})

	// The editor only ever saw the placeholder, so an edit that changes the
	// host sends it straight back. Passing it through would authenticate with
	// eight bullet characters and blame the password.
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credential-types/postgres/test", map[string]any{
			"credentialId": stored.ID,
			"fields": map[string]string{
				"host": "127.0.0.2", "port": "1", "database": "app",
				"user": "ada", "password": credentials.RedactedValue, "sslMode": "disable",
			},
		}, http.StatusOK)
	// Which fields were not the caller's own is the part that makes "it works"
	// mean anything.
	if len(verdict.ResolvedFromStorage) != 1 || verdict.ResolvedFromStorage[0] != "password" {
		t.Errorf("resolvedFromStorage = %#v, want the password named", verdict.ResolvedFromStorage)
	}

	// Without a credential to resolve against, a placeholder is refused rather
	// than sent as a password.
	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"fields": map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": credentials.RedactedValue, "sslMode": "disable",
		},
	}, http.StatusUnprocessableEntity)

	// And a credential of another type is not a source of this one's secrets.
	other := storeCredential(t, handler, "Token", "httpBearerAuth", map[string]string{"token": "keep-me"})
	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/postgres/test", map[string]any{
		"credentialId": other.ID,
		"fields": map[string]string{
			"host": "127.0.0.1", "port": "1", "database": "app",
			"user": "ada", "password": credentials.RedactedValue, "sslMode": "disable",
		},
	}, http.StatusUnprocessableEntity)

	requestProblem(t, handler, http.MethodPost, "/api/v1/credential-types/nope/test",
		map[string]any{"fields": map[string]string{}}, http.StatusUnprocessableEntity)
}

func TestACredentialTypeWithNoProbeSaysSoRatherThanFailing(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	stored := storeCredential(t, handler, "Token", "httpBearerAuth", map[string]string{"token": "keep-me"})

	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credentials/"+stored.ID+"/test", nil, http.StatusOK)
	// "We cannot check this" and "we checked and it is broken" are different
	// answers, and a client drawing a red cross for both would be lying about
	// one of them.
	if verdict.OK || !verdict.Untestable {
		t.Fatalf("verdict = %#v, want an untestable answer rather than a failure", verdict)
	}
	if verdict.Detail == "" {
		t.Error("an untestable verdict said nothing about why")
	}
}

func TestATargetThatNeverAnswersStillYieldsAVerdict(t *testing.T) {
	// A listener that completes the TCP handshake and then says nothing. This
	// is the shape that holds a request open: the connection is up, so nothing
	// fails, and the driver waits for a greeting that never comes.
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			// Held, never answered, never closed.
			t.Cleanup(func() { _ = connection.Close() })
		}
	}()

	handler, _ := credentialAPI(t, api.Deps{
		Config: withCredentialTestTimeout(300 * time.Millisecond),
	})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	stored := storeCredential(t, handler, "Silent", "postgres", map[string]string{
		"host": "127.0.0.1", "port": port, "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})

	done := make(chan testCredentialResource, 1)
	go func() {
		done <- requestJSON[testCredentialResource](t, handler, http.MethodPost,
			"/api/v1/credentials/"+stored.ID+"/test", nil, http.StatusOK)
	}()
	select {
	case verdict := <-done:
		if verdict.OK {
			t.Fatalf("verdict = %#v, want a failure against a target that never answered", verdict)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("the test endpoint held the request past its own deadline")
	}
}

func withCredentialTestTimeout(timeout time.Duration) config.Config {
	cfg := config.Default()
	cfg.Credential.TestTimeout = timeout
	return cfg
}

func TestAnEmbedSessionCannotTestACredential(t *testing.T) {
	issuer := embedIssuer(t)
	handler, _ := credentialAPI(t, api.Deps{EmbedIssuer: issuer})
	stored := storeCredential(t, handler, "Remote", "postgres", map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})

	// An embed session is confined to one workflow. A probe it could aim is a
	// connect-anywhere primitive handed to whoever the host embedded the editor
	// for, so both routes are refused — and asserted here so a later change to
	// permits cannot open them by accident.
	_, token, err := issuer.Issue(embed.Request{
		TenantID: repository.DefaultTenantID, WorkflowID: "wf_embedded",
		Scopes: []embed.Scope{embed.ScopeRead, embed.ScopeWrite, embed.ScopeRun}, Origin: hostOrigin,
	})
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	for _, path := range []string{
		"/api/v1/credentials/" + stored.ID + "/test",
		"/api/v1/credential-types/postgres/test",
	} {
		if got := embedRequest(t, handler, token, http.MethodPost, path, map[string]any{"fields": map[string]string{}}); got.Code != http.StatusForbidden {
			t.Errorf("%s status = %d, want 403 (body: %s)", path, got.Code, got.Body)
		}
	}
}

func TestOnlyOneTestOfACredentialRunsAtATime(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	t.Cleanup(func() { _ = listener.Close() })
	go func() {
		for {
			connection, err := listener.Accept()
			if err != nil {
				return
			}
			t.Cleanup(func() { _ = connection.Close() })
		}
	}()

	handler, _ := credentialAPI(t, api.Deps{Config: withCredentialTestTimeout(3 * time.Second)})
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	stored := storeCredential(t, handler, "Silent", "postgres", map[string]string{
		"host": "127.0.0.1", "port": port, "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})
	path := "/api/v1/credentials/" + stored.ID + "/test"

	first := make(chan struct{})
	go func() {
		defer close(first)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	}()
	// The first probe is now hanging on a target that never answers. Not a
	// rate limit — sequential tests are how a credential gets fixed — but
	// without this, a few hundred concurrent requests turn one stored
	// credential into a port scanner with the verdict as its oracle.
	time.Sleep(100 * time.Millisecond)

	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	if recorder.Code != http.StatusConflict {
		t.Fatalf("concurrent test status = %d, want 409 (body: %s)", recorder.Code, recorder.Body)
	}

	<-first
	// The claim is released, so the next attempt is ordinary again.
	recorder = httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest(http.MethodPost, path, nil))
	if recorder.Code != http.StatusOK {
		t.Fatalf("status after the first finished = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
}
