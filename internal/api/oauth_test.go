package api_test

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/database"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// fakeGoogle is a token endpoint that holds the exchange to PKCE: it answers
// only a code_verifier whose S256 transform is a challenge an authorize URL
// carried, the way Google does.
type fakeGoogle struct {
	server     *httptest.Server
	mu         sync.Mutex
	challenges map[string]bool
	exchanges  atomic.Int32
}

func newFakeGoogle(t *testing.T) *fakeGoogle {
	t.Helper()
	fake := &fakeGoogle{challenges: map[string]bool{}}
	fake.server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("code") != "auth-code" || r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("token form = %v", r.Form)
		}
		fake.exchanges.Add(1)
		sum := sha256.Sum256([]byte(r.Form.Get("code_verifier")))
		fake.mu.Lock()
		known := fake.challenges[base64.RawURLEncoding.EncodeToString(sum[:])]
		fake.mu.Unlock()
		if !known {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"error":"invalid_grant","error_description":"Missing code verifier."}`))
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.secret","refresh_token":"1//refresh","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(fake.server.Close)
	return fake
}

func (fake *fakeGoogle) saw(challenge string) {
	fake.mu.Lock()
	defer fake.mu.Unlock()
	fake.challenges[challenge] = true
}

// oauthAPI is the credential surface with Connect configured against the
// fake token endpoint.
func oauthAPI(t *testing.T, publicURL string, fake *fakeGoogle) (http.Handler, *repository.GORMCredentialStore) {
	t.Helper()
	cfg := config.Default()
	cfg.Server.PublicURL = publicURL
	cfg.Google.ClientID = "platform-client"
	signingKey := make([]byte, 32)
	for index := range signingKey {
		signingKey[index] = byte(index + 3)
	}
	return credentialAPI(t, api.Deps{
		Config:          cfg,
		OAuthSigningKey: signingKey,
		OAuthTokenURL:   fake.server.URL,
		OAuthHTTP:       fake.server.Client(),
	})
}

// connectStart is one browser pressing Connect: the authorize URL it is sent
// to and the cookies the start response set in it.
type connectStart struct {
	authorize *url.URL
	cookies   []*http.Cookie
}

func (start connectStart) state() string { return start.authorize.Query().Get("state") }

func startConnect(t *testing.T, handler http.Handler, fake *fakeGoogle, credentialID string) connectStart {
	t.Helper()
	request := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+credentialID+"/oauth/start", nil)
	request.Header.Set("Origin", "https://editor.example")
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := json.NewDecoder(recorder.Body).Decode(&started); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL: %v", err)
	}
	fake.saw(parsed.Query().Get("code_challenge"))
	return connectStart{authorize: parsed, cookies: recorder.Result().Cookies()}
}

// finishConnect is Google redirecting a browser holding cookies back to the
// callback with state.
func finishConnect(t *testing.T, handler http.Handler, state string, cookies []*http.Cookie) *httptest.ResponseRecorder {
	t.Helper()
	request := httptest.NewRequest(http.MethodGet, handlers.OAuthCallbackPath+"?code=auth-code&state="+url.QueryEscape(state), nil)
	for _, cookie := range cookies {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	if recorder.Code != http.StatusOK {
		t.Fatalf("oauth callback status = %d, want 200 (body: %s)", recorder.Code, recorder.Body)
	}
	return recorder
}

func connected(recorder *httptest.ResponseRecorder) bool {
	return strings.Contains(recorder.Body.String(), `"ok":true`)
}

func storedGoogleTokens(t *testing.T, store *repository.GORMCredentialStore, credentialID string) map[string]string {
	t.Helper()
	_, fields, err := store.Resolve(t.Context(), repository.TenantScope{ID: repository.DefaultTenantID}, credentialID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	return fields
}

func TestGoogleConnectPopupExchangesACodeAndNeverListsTheToken(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, store := oauthAPI(t, "https://app.test", fake)

	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	if len(created.AllowedDomains) == 0 {
		t.Fatal("Google credentials must default to googleapis hosts")
	}

	start := startConnect(t, handler, fake, created.ID)
	query := start.authorize.Query()
	if query.Get("client_id") != "tenant-cid" {
		t.Fatalf("client_id = %q", query.Get("client_id"))
	}
	if query.Get("access_type") != "offline" {
		t.Fatal("Connect must request an offline refresh token")
	}
	if got := query.Get("redirect_uri"); got != "https://app.test"+handlers.OAuthCallbackPath {
		t.Fatalf("redirect_uri = %q", got)
	}
	if query.Get("code_challenge") == "" || query.Get("code_challenge_method") != "S256" {
		t.Fatalf("authorize URL = %s, want an S256 PKCE challenge", start.authorize)
	}

	callback := finishConnect(t, handler, start.state(), start.cookies)
	body := callback.Body.String()
	if !strings.Contains(body, `"source":"kilasflow-oauth"`) || !connected(callback) {
		t.Fatalf("callback html = %s", body)
	}
	if !strings.Contains(body, "window.opener.postMessage") {
		t.Fatal("callback must postMessage to the opener, not render tokens")
	}
	if strings.Contains(body, `postMessage(payload, "*")`) {
		t.Fatal("callback must not postMessage to *")
	}
	if !strings.Contains(body, `"https://editor.example"`) {
		t.Fatalf("callback must postMessage to the editor origin, got %s", body)
	}

	listed := requestJSON[credentialResource](t, handler, http.MethodGet, "/api/v1/credentials/"+created.ID, nil, http.StatusOK)
	if listed.Fields["access_token"] != credentials.RedactedValue {
		t.Fatalf("listed access_token = %q, want the redaction marker", listed.Fields["access_token"])
	}
	if strings.Contains(listed.Fields["access_token"], "ya29") || strings.Contains(listed.Fields["refresh_token"], "refresh") {
		t.Fatalf("Get leaked a google token: %#v", listed.Fields)
	}

	fields := storedGoogleTokens(t, store, created.ID)
	if fields["access_token"] != "ya29.secret" || fields["refresh_token"] != "1//refresh" {
		t.Fatalf("stored fields = %#v", fields)
	}
}

// The start response binds the flow to the browser that asked, with a cookie a
// page script cannot read and a cross-site subrequest does not carry.
func TestGoogleConnectSetsAnHttpOnlyLaxNonceCookieScopedToTheCallback(t *testing.T) {
	for name, testCase := range map[string]struct {
		publicURL  string
		wantSecure bool
		wantPath   string
	}{
		"https":            {"https://app.test", true, handlers.OAuthCallbackPath},
		"http":             {"http://localhost:8080", false, handlers.OAuthCallbackPath},
		"https under path": {"https://app.test/flow", true, "/flow" + handlers.OAuthCallbackPath},
	} {
		t.Run(name, func(t *testing.T) {
			fake := newFakeGoogle(t)
			handler, _ := oauthAPI(t, testCase.publicURL, fake)
			created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
				"clientId": "tenant-cid", "clientSecret": "tenant-csec",
			})
			start := startConnect(t, handler, fake, created.ID)
			if len(start.cookies) != 1 {
				t.Fatalf("start set %d cookies, want the one nonce cookie", len(start.cookies))
			}
			cookie := start.cookies[0]
			if !cookie.HttpOnly || cookie.SameSite != http.SameSiteLaxMode {
				t.Errorf("cookie = %+v, want HttpOnly and SameSite=Lax", cookie)
			}
			if cookie.Secure != testCase.wantSecure {
				t.Errorf("cookie Secure = %v, want %v", cookie.Secure, testCase.wantSecure)
			}
			if cookie.Path != testCase.wantPath {
				t.Errorf("cookie Path = %q, want %q", cookie.Path, testCase.wantPath)
			}
			if cookie.MaxAge <= 0 || cookie.MaxAge > int(credentials.OAuthStateTTL.Seconds()) {
				t.Errorf("cookie MaxAge = %d, want it to outlive nothing past the state", cookie.MaxAge)
			}
			if strings.Contains(start.authorize.String(), cookie.Value) {
				t.Error("the nonce itself travels in the authorize URL")
			}
		})
	}
}

// The exploit the binding exists for: someone starts Connect on their own
// credential and sends the authorize URL to a victim. The victim consents, and
// Google redirects the victim's browser — which never pressed Connect and so
// holds no nonce — to the callback.
func TestAGoogleCallbackFromAnotherBrowserIsRefused(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, store := oauthAPI(t, "https://app.test", fake)
	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	attacker := startConnect(t, handler, fake, created.ID)

	// The victim's browser: no cookie at all.
	if callback := finishConnect(t, handler, attacker.state(), nil); connected(callback) {
		t.Fatalf("a callback with no nonce cookie connected: %s", callback.Body)
	}
	// The victim's browser having started a Connect of its own: a nonce, but
	// not the one this state was signed with.
	victim := startConnect(t, handler, fake, created.ID)
	foreign := make([]*http.Cookie, 0, len(victim.cookies))
	for _, cookie := range victim.cookies {
		// Presented under the attacker flow's cookie name, as if a
		// sibling-origin page had tossed it in.
		foreign = append(foreign, &http.Cookie{Name: attacker.cookies[0].Name, Value: cookie.Value})
	}
	if callback := finishConnect(t, handler, attacker.state(), foreign); connected(callback) {
		t.Fatalf("a callback with another flow's nonce connected: %s", callback.Body)
	}

	if got := fake.exchanges.Load(); got != 0 {
		t.Fatalf("the refused callbacks exchanged %d codes", got)
	}
	if fields := storedGoogleTokens(t, store, created.ID); fields["access_token"] != "" || fields["refresh_token"] != "" {
		t.Fatalf("tokens were stored from another browser: %#v", fields)
	}
}

// A state is used once. Replaying the callback URL — from history, a log, a
// Referer — with the same browser's cookie does not run a second exchange.
func TestAGoogleCallbackCannotBeReplayed(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, _ := oauthAPI(t, "https://app.test", fake)
	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	start := startConnect(t, handler, fake, created.ID)

	first := finishConnect(t, handler, start.state(), start.cookies)
	if !connected(first) {
		t.Fatalf("first callback = %s, want connected", first.Body)
	}
	// The callback clears its cookie, so an honest browser would not even
	// send it again; the replay here is one that kept a copy.
	cleared := false
	for _, cookie := range first.Result().Cookies() {
		if cookie.Name == start.cookies[0].Name && cookie.MaxAge < 0 {
			cleared = true
		}
	}
	if !cleared {
		t.Error("the callback did not clear its nonce cookie")
	}
	replay := finishConnect(t, handler, start.state(), start.cookies)
	if connected(replay) {
		t.Fatalf("a replayed callback connected: %s", replay.Body)
	}
	if !strings.Contains(replay.Body.String(), "already") {
		t.Errorf("replay body = %s, want it to say the sign-in was already used", replay.Body)
	}
	if got := fake.exchanges.Load(); got != 1 {
		t.Fatalf("token exchanges = %d, want exactly the first", got)
	}
}

// The token endpoint is held to PKCE, so an exchange without the verifier is
// refused — and the server never makes one, because the verifier comes from
// the browser's nonce, which a callback without the cookie does not have.
func TestAGoogleCodeIsNotExchangedWithoutItsVerifier(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, _ := oauthAPI(t, "https://app.test", fake)
	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	start := startConnect(t, handler, fake, created.ID)

	// Proof the fake enforces PKCE: a code sent with no verifier is refused.
	if _, err := credentials.ExchangeGoogleCode(fake.server.Client(), fake.server.URL, "tenant-cid", "tenant-csec",
		"https://app.test"+handlers.OAuthCallbackPath, "auth-code", "not-the-verifier"); err == nil {
		t.Fatal("the token endpoint accepted a verifier that matches no challenge")
	}
	before := fake.exchanges.Load()
	if callback := finishConnect(t, handler, start.state(), nil); connected(callback) {
		t.Fatalf("a callback with no verifier source connected: %s", callback.Body)
	}
	if got := fake.exchanges.Load(); got != before {
		t.Fatal("the callback sent a code to the token endpoint without its verifier")
	}
	// The same callback with the browser's cookie completes.
	if callback := finishConnect(t, handler, start.state(), start.cookies); !connected(callback) {
		t.Fatalf("callback with the nonce = %s, want connected", callback.Body)
	}
}

// Behind a load balancer the replay can reach a different replica from the
// first callback. The used state is recorded in the shared database, so the
// other replica refuses it too.
func TestAGoogleCallbackReplayedOnAnotherReplicaIsRefused(t *testing.T) {
	fake := newFakeGoogle(t)
	db, err := database.Open(context.Background(), config.Database{
		Driver: "sqlite", DSN: filepath.Join(t.TempDir(), "replicas.db"),
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
		key[index] = byte(index + 3)
	}
	cipher, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	cfg := config.Default()
	cfg.Server.PublicURL = "https://app.test"
	replica := func() http.Handler {
		// Each replica has its own store and its own idempotency service over
		// the one database, as two processes would.
		service, err := idempotency.NewService(repository.NewIdempotencyStore(db.DB), idempotency.Options{Retention: time.Hour})
		if err != nil {
			t.Fatalf("idempotency.NewService() error = %v", err)
		}
		return newTestServer(t, api.Deps{
			Config: cfg, DB: db, Credentials: repository.NewCredentialStore(db.DB, cipher),
			Idempotency: service, OAuthSigningKey: key,
			OAuthTokenURL: fake.server.URL, OAuthHTTP: fake.server.Client(),
		})
	}
	first, second := replica(), replica()

	created := storeCredential(t, first, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	start := startConnect(t, first, fake, created.ID)
	if callback := finishConnect(t, first, start.state(), start.cookies); !connected(callback) {
		t.Fatalf("first callback = %s, want connected", callback.Body)
	}
	if callback := finishConnect(t, second, start.state(), start.cookies); connected(callback) {
		t.Fatalf("the replay on another replica connected: %s", callback.Body)
	}
	if got := fake.exchanges.Load(); got != 1 {
		t.Fatalf("token exchanges = %d, want exactly the first", got)
	}
}
