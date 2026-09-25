package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
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

	"github.com/jackc/pgx/v5/pgconn"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/sqlnode"
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
	root := t.TempDir()
	handler, _ := credentialAPI(t, api.Deps{DatabaseGuard: sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}})

	// SQLite creates the file on connect, so a writable directory is a
	// reachable target and a directory that does not exist is not. The path is
	// the tenant's own, under the confined root.
	reachable := storeCredential(t, handler, "Workflow database", "sqlite", map[string]string{
		"path": "workflow.db",
	})
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credentials/"+reachable.ID+"/test", nil, http.StatusOK)
	if !verdict.OK {
		t.Fatalf("verdict = %#v, want a reachable database", verdict)
	}
	if _, err := os.Stat(filepath.Join(root, repository.DefaultTenantID, "workflow.db")); err != nil {
		t.Fatalf("the test did not open the file in the caller's tenant directory: %v", err)
	}

	// All three database types, so none of them can quietly fall through to the
	// HTTP probe — which has no URL to fetch and would report "no test defined"
	// for a credential this server can perfectly well check.
	for credentialType, fields := range map[string]map[string]string{
		"sqlite": {"path": "no/such/directory/workflow.db"},
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
	// Unconfined, the one mode in which a credential can spell that path.
	handler, _ := credentialAPI(t, api.Deps{DatabaseGuard: sqlnode.Guard{
		InternalPaths: []string{internal}, SQLite: sqlnode.SQLiteFiles{Unconfined: true},
	}})

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

// The live findings, through the endpoint: on a confined install a SQLite
// credential cannot name a system file, escape its tenant's directory, or
// create a file anywhere else — and neither the stored nor the unsaved test
// route opens one.
func TestTheTestEndpointConfinesSQLitePathsToTheTenant(t *testing.T) {
	root := t.TempDir()
	handler, _ := credentialAPI(t, api.Deps{DatabaseGuard: sqlnode.Guard{SQLite: sqlnode.SQLiteFiles{Root: root}}})

	outside := filepath.Join(t.TempDir(), "created-by-tenant.db")
	for name, path := range map[string]string{
		"a system file":        "/etc/passwd",
		"a dot-dot escape":     "../../../../etc/hosts",
		"a new file elsewhere": outside,
	} {
		t.Run(name, func(t *testing.T) {
			stored := storeCredential(t, handler, "Escape", "sqlite", map[string]string{"path": path})
			verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
				"/api/v1/credentials/"+stored.ID+"/test", nil, http.StatusOK)
			if verdict.OK || !strings.Contains(verdict.Detail, "not allowed") {
				t.Fatalf("stored test verdict = %#v, want the path refused", verdict)
			}
			unsaved := requestJSON[testCredentialResource](t, handler, http.MethodPost,
				"/api/v1/credential-types/sqlite/test", map[string]any{"fields": map[string]string{"path": path}}, http.StatusOK)
			if unsaved.OK || !strings.Contains(unsaved.Detail, "not allowed") {
				t.Fatalf("unsaved test verdict = %#v, want the path refused", unsaved)
			}
		})
	}
	if _, err := os.Stat(outside); !errors.Is(err, os.ErrNotExist) {
		t.Errorf("a refused path still created %s (stat err = %v)", outside, err)
	}
}

func TestAnUnsavedEditIsTestedAgainstItsStoredSecrets(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	stored := storeCredential(t, handler, "Remote", "postgres", map[string]string{
		"host": "127.0.0.1", "port": "1", "database": "app",
		"user": "ada", "password": "hunter2", "sslMode": "disable",
	})

	// The editor only ever saw the placeholder, so an edit that changes the
	// database sends it straight back. Passing it through would authenticate
	// with eight bullet characters and blame the password. (An edit that moves
	// the host is refused instead: see credentials_probe_scope_test.go.)
	verdict := requestJSON[testCredentialResource](t, handler, http.MethodPost,
		"/api/v1/credential-types/postgres/test", map[string]any{
			"credentialId": stored.ID,
			"fields": map[string]string{
				"host": "127.0.0.1", "port": "1", "database": "reporting",
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

// A SQLite credential names a file, not a host, so a scope saved on one is a
// promise nothing will keep. It is rejected at save time rather than silently
// stored and ignored.
func TestASQLiteCredentialCannotBeSavedWithAllowedDomains(t *testing.T) {
	handler, _ := credentialAPI(t, api.Deps{})
	scoped := map[string]any{
		"name": "Scoped file", "type": "sqlite",
		"fields":         map[string]string{"path": filepath.Join(t.TempDir(), "workflow.db")},
		"allowedDomains": []string{"db.partner.test"},
	}
	requestProblem(t, handler, http.MethodPost, "/api/v1/credentials", scoped, http.StatusUnprocessableEntity)

	// The same file without a scope saves, and gains none on an update that
	// omits the type — the stored type decides, since the type is immutable.
	stored := storeCredential(t, handler, "Workflow database", "sqlite", map[string]string{
		"path": filepath.Join(t.TempDir(), "workflow.db"),
	})
	requestProblem(t, handler, http.MethodPut, "/api/v1/credentials/"+stored.ID, map[string]any{
		"name":           "Workflow database",
		"fields":         map[string]string{"path": filepath.Join(t.TempDir(), "workflow.db")},
		"allowedDomains": []string{"db.partner.test"},
	}, http.StatusUnprocessableEntity)

	// A network database may be scoped: that is what the field is for.
	requestJSON[credentialResource](t, handler, http.MethodPost, "/api/v1/credentials", map[string]any{
		"name": "Partner", "type": "postgres",
		"fields": map[string]string{
			"host": "db.partner.test", "port": "5432", "database": "app",
			"user": "ada", "password": "hunter2", "sslMode": "disable",
		},
		"allowedDomains": []string{"db.partner.test"},
	}, http.StatusCreated)
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

	_, port, _ := net.SplitHostPort(listener.Addr().String())
	// The probe must reach the silent listener and hang there: under a
	// default-deny guard the loopback dial is refused pre-flight and the
	// first probe returns before the second arrives, which proves nothing
	// about the claim.
	handler, _ := credentialAPI(t, api.Deps{
		Config: withCredentialTestTimeout(3 * time.Second),
		DatabaseGuard: sqlnode.Guard{Policy: safehttp.Policy{
			AllowedPrivateEndpoints: []string{"127.0.0.1:" + port},
		}},
	})
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

// A failure underneath the store is a server fault, and it says so: it is
// logged with its cause and answered generically.
//
// The review found the opposite in credentials, schedules, and datastores —
// every error that was not "not found" became 422 carrying err.Error(), so a
// driver message (table names, column names, the host) reached the caller and a
// database outage read as a mistake the integrator had made.
//
// The store is faked rather than broken because what is under test is the
// mapping, and a driver error is what the mapping has to recognise: the
// Postgres error below is the shape a real deployment produces.
func TestAStoreFailureIsLoggedAndNotAnsweredAsTheCallersMistake(t *testing.T) {
	driver := fmt.Errorf("update credential: %w", &pgconn.PgError{
		Severity: "ERROR", Code: "42P01", Message: `relation "credentials" does not exist`,
	})

	// serverProblem writes to the default logger, which main.go points at the
	// configured handler; a test captures it there rather than through Deps.
	var logged bytes.Buffer
	previous := slog.Default()
	slog.SetDefault(slog.New(slog.NewTextHandler(&logged, nil)))
	t.Cleanup(func() { slog.SetDefault(previous) })

	for _, testCase := range []struct {
		name    string
		deps    api.Deps
		method  string
		path    string
		handler func(http.Handler) *httptest.ResponseRecorder
	}{
		{
			name:   "credentials",
			deps:   api.Deps{DB: stubPinger{}, Credentials: failingCredentialStore{err: driver}},
			method: http.MethodGet, path: "/api/v1/credentials",
		},
		{
			name:   "schedules",
			deps:   api.Deps{DB: stubPinger{}, Schedules: failingScheduleStore{err: driver}},
			method: http.MethodGet, path: "/api/v1/schedules",
		},
	} {
		t.Run(testCase.name, func(t *testing.T) {
			logged.Reset()
			handler := newTestServer(t, testCase.deps)
			recorder := httptest.NewRecorder()
			handler.ServeHTTP(recorder, httptest.NewRequest(testCase.method, testCase.path, nil))

			if recorder.Code != http.StatusInternalServerError {
				t.Fatalf("status = %d, want 500 (body: %s)", recorder.Code, recorder.Body)
			}
			// The driver's own text names the table; the caller gets the
			// handler's sentence and nothing else.
			for _, leak := range []string{"credentials", "relation", "42P01", "does not exist"} {
				if strings.Contains(recorder.Body.String(), leak) {
					t.Errorf("the 500 disclosed %q: %s", leak, recorder.Body)
				}
			}
			// The cause is not discarded: it is in the log, beside the request
			// id that ties it to the access line.
			if !strings.Contains(logged.String(), "does not exist") {
				t.Errorf("the cause is not in the log: %s", logged.String())
			}
		})
	}
}

// failingCredentialStore answers every read with the supplied failure.
type failingCredentialStore struct {
	repository.CredentialRepository
	err error
}

func (store failingCredentialStore) List(context.Context, repository.TenantScope) ([]credentials.Record, error) {
	return nil, store.err
}

func (store failingCredentialStore) ListPage(context.Context, repository.TenantScope, repository.CredentialFilter) (repository.CredentialPage, error) {
	return repository.CredentialPage{}, store.err
}

// failingScheduleStore answers every read with the supplied failure.
type failingScheduleStore struct {
	repository.ScheduleRepository
	err error
}

func (store failingScheduleStore) List(context.Context, repository.TenantScope) ([]repository.Schedule, error) {
	return nil, store.err
}

func (store failingScheduleStore) ListPage(context.Context, repository.TenantScope, repository.ScheduleFilter) (repository.SchedulePage, error) {
	return repository.SchedulePage{}, store.err
}
