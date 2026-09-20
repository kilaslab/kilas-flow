package middleware

// Session-revalidation proof: a signed session is no longer a grant that
// nothing can withdraw. A disabled account, a changed password, an account that
// no longer exists, a session minted for a different account state, and a
// deployment that never wired the lookup are each refused, while a valid
// session and a live API key keep working.

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

// accounts is the account store a test can change between requests.
type accounts struct {
	user  repository.User
	err   error
	calls int
}

func (lookup *accounts) FindUserForLogin(context.Context, string) (repository.User, error) {
	lookup.calls++
	if lookup.err != nil {
		return repository.User{}, lookup.err
	}
	return lookup.user, nil
}

func middlewareTestKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index * 11)
	}
	return key
}

func middlewareIssuer(t *testing.T) *auth.Issuer {
	t.Helper()
	issuer, err := auth.NewIssuer(middlewareTestKey(), time.Hour, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

// account is the stored state the tests vary: the hash and the disabled flag
// are the two things a session is bound to.
func account() repository.User {
	return repository.User{
		ID: "usr-1", TenantID: "tenant-a", Email: "owner@example.test",
		Name: "Owner", PasswordHash: "pbkdf2-sha256$600000$c2FsdA$aGFzaA",
	}
}

// sessionCookie mints the cookie a browser would be holding for this account.
func sessionCookie(t *testing.T, issuer *auth.Issuer, user repository.User) *http.Cookie {
	t.Helper()
	_, token, err := issuer.IssueSession(user.ID, user.TenantID, user.Email,
		auth.UserVersion(user.PasswordHash, user.DisabledAt != nil))
	if err != nil {
		t.Fatalf("IssueSession() error = %v", err)
	}
	return &http.Cookie{Name: auth.SessionCookieName, Value: token}
}

// probe records what the gate let through.
type probe struct {
	principal auth.Principal
	found     bool
}

func gated(opts AuthOptions) (http.Handler, *probe) {
	seen := &probe{}
	handler := Authenticate(opts)(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seen.principal, seen.found = auth.PrincipalFrom(r.Context())
		w.WriteHeader(http.StatusOK)
	}))
	return handler, seen
}

func sessionRequest(handler http.Handler, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/auth/me", nil)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// gateOptions is the wiring a deployment has: an enabled gate over the API
// prefix, with both stores pointed at the same place.
func gateOptions(issuer *auth.Issuer, lookup UserLookup) AuthOptions {
	return AuthOptions{
		Enabled: true, Sessions: issuer, Users: lookup, APIPrefix: "/api/v1",
	}
}

func TestAValidSessionAuthenticates(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	handler, seen := gated(gateOptions(issuer, &accounts{user: user}))

	recorder := sessionRequest(handler, sessionCookie(t, issuer, user))
	if recorder.Code != http.StatusOK {
		t.Fatalf("a valid session = %d, want 200", recorder.Code)
	}
	if !seen.found || seen.principal.TenantID != "tenant-a" || seen.principal.UserID != "usr-1" {
		t.Errorf("principal = %#v, want the signed-in owner", seen.principal)
	}
	if seen.principal.Kind != auth.KindSession {
		t.Errorf("kind = %q, want a session", seen.principal.Kind)
	}
}

// The finding's reproduction: a valid cookie kept full access after the account
// was disabled in the database. The session was minted while the account was
// live, so nothing in the token itself has changed.
func TestASessionForADisabledAccountIsRefused(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)

	disabled := user
	at := time.Now()
	disabled.DisabledAt = &at
	handler, _ := gated(gateOptions(issuer, &accounts{user: disabled}))

	if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a disabled account's session = %d, want 401", recorder.Code)
	}
}

// The other half of the finding: changing the password has to end the sessions
// that password authorised, or a leaked password stays useful after it is
// rotated.
func TestASessionIsRefusedWhenItsPasswordChanged(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)

	rotated := user
	rotated.PasswordHash = "pbkdf2-sha256$600000$c2FsdA$b3RoZXI"
	handler, _ := gated(gateOptions(issuer, &accounts{user: rotated}))

	if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a session whose password changed = %d, want 401", recorder.Code)
	}
}

// The revalidation window is the cache lifetime, and it is worth stating: a
// session keeps working for that long after the account changes, which is what
// makes the lookup affordable. Past it, the change takes effect.
func TestADisabledAccountIsNoticedWhenTheCachedAnswerExpires(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)
	lookup := &accounts{user: user}

	opts := gateOptions(issuer, lookup)
	opts.SessionRevalidationTTL = 20 * time.Millisecond
	handler, _ := gated(opts)

	if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusOK {
		t.Fatalf("the first request = %d, want 200", recorder.Code)
	}
	at := time.Now()
	lookup.user = user
	lookup.user.DisabledAt = &at
	if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusOK {
		t.Errorf("a request inside the revocation window = %d, want the cached answer", recorder.Code)
	}

	time.Sleep(40 * time.Millisecond)
	if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a request after the window = %d, want 401", recorder.Code)
	}
}

// A dashboard polls several endpoints per refresh, so the lookup has to be
// cached rather than run per request.
func TestTheAccountLookupIsCachedAcrossRequests(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)
	lookup := &accounts{user: user}

	opts := gateOptions(issuer, lookup)
	opts.SessionRevalidationTTL = time.Hour
	handler, _ := gated(opts)

	for request := 0; request < 5; request++ {
		if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusOK {
			t.Fatalf("request %d = %d, want 200", request, recorder.Code)
		}
	}
	if lookup.calls != 1 {
		t.Errorf("the account was read %d times for five requests, want once", lookup.calls)
	}
}

func TestASessionIsRefusedWhenItsAccountCannotBeRead(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)

	for name, lookup := range map[string]*accounts{
		"the account no longer exists": {err: repository.ErrNotFound},
		"the store is unreachable":     {err: errors.New("connection refused")},
	} {
		handler, _ := gated(gateOptions(issuer, lookup))
		if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s: session = %d, want 401", name, recorder.Code)
		}
	}
}

// A session that names one account while the address now resolves to another
// must not authenticate as either.
func TestASessionIsRefusedWhenItsAccountNoLongerMatches(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)

	reassigned := user
	reassigned.TenantID = "tenant-b"
	moved := user
	moved.ID = "usr-2"

	for name, stored := range map[string]repository.User{
		"the account moved tenant": reassigned,
		"the address was reused":   moved,
	} {
		handler, _ := gated(gateOptions(issuer, &accounts{user: stored}))
		if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s: session = %d, want 401", name, recorder.Code)
		}
	}
}

// A deployment that has not wired the account store refuses sessions rather
// than admitting them unrevalidated, which is the fail-closed half of the same
// decision: it cannot tell a live account from a disabled one.
func TestASessionIsRefusedWhenNoLookupIsWired(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	handler, _ := gated(gateOptions(issuer, nil))

	if recorder := sessionRequest(handler, sessionCookie(t, issuer, user)); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a session with no lookup wired = %d, want 401", recorder.Code)
	}
}

// A cookie is not a credential the gate trusts on its own, so a token that is
// not ours never reaches a handler.
func TestAnUnverifiableSessionIsRefused(t *testing.T) {
	t.Parallel()

	issuer := middlewareIssuer(t)
	handler, _ := gated(gateOptions(issuer, &accounts{user: account()}))

	for name, value := range map[string]string{
		"an empty cookie":     "",
		"a made-up token":     "kfs1.not.a.token",
		"a stream ticket":     streamTicketValue(t, issuer),
		"a truncated session": "kfs1.eyJzdWIiOiJ1c3ItMSJ9.x",
	} {
		cookie := &http.Cookie{Name: auth.SessionCookieName, Value: value}
		if recorder := sessionRequest(handler, cookie); recorder.Code != http.StatusUnauthorized {
			t.Errorf("%s = %d, want 401", name, recorder.Code)
		}
	}
}

func streamTicketValue(t *testing.T, issuer *auth.Issuer) string {
	t.Helper()
	_, token, err := issuer.IssueTicket("tenant-a", "exec-1", 0)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}
	return token
}

// A typed nil satisfies the interface while pointing at nothing, which is the
// shape an install with no store hands over when it passes its zero value
// through unchanged. It must read as "nothing to revalidate against" rather
// than as a store to call.
type nilLookup struct {
	calls int
}

func (lookup *nilLookup) FindUserForLogin(context.Context, string) (repository.User, error) {
	lookup.calls++
	return repository.User{}, nil
}

func TestASessionIsRefusedWhenTheLookupIsATypedNil(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	var store *nilLookup
	handler, _ := gated(gateOptions(issuer, store))

	if recorder := sessionRequest(handler, sessionCookie(t, issuer, user)); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a session with a typed nil lookup = %d, want 401", recorder.Code)
	}
	if store != nil {
		t.Error("the test's own nil store was assigned to")
	}
}

// An install with authentication turned off admits every request under the
// fallback tenant. The gate has to keep doing exactly that — including when it
// was handed no account store at all, which is what such an install passes.
func TestADisabledGateAdmitsRequestsWithoutReadingAnyAccount(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	lookup := &accounts{user: user}

	for name, opts := range map[string]AuthOptions{
		"with an account store": {Enabled: false, APIPrefix: "/api/v1", Users: lookup},
		"with no account store": {Enabled: false, APIPrefix: "/api/v1"},
	} {
		handler, seen := gated(opts)
		recorder := sessionRequest(handler, sessionCookie(t, issuer, user))
		if recorder.Code != http.StatusOK {
			t.Errorf("%s: a disabled gate answered %d, want 200", name, recorder.Code)
		}
		// Disabled means "no identity at all": storing one would scope the
		// request to a tenant the operator never asked for.
		if seen.found {
			t.Errorf("%s: a disabled gate stored the principal %#v", name, seen.principal)
		}
	}
	if lookup.calls != 0 {
		t.Errorf("the account store was read %d times with the gate off, want 0", lookup.calls)
	}
}

// keys answers the API-key question without a database.
type keys struct {
	key repository.APIKey
	err error
}

func (lookup *keys) AuthenticateAPIKey(context.Context, string) (repository.APIKey, error) {
	if lookup.err != nil {
		return repository.APIKey{}, lookup.err
	}
	return lookup.key, nil
}

func keyRequest(handler http.Handler, token string, cookie *http.Cookie) *httptest.ResponseRecorder {
	request := httptest.NewRequest(http.MethodGet, "/api/v1/workflows", nil)
	request.Header.Set("Authorization", "Bearer "+token)
	if cookie != nil {
		request.AddCookie(cookie)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, request)
	return recorder
}

// A key is revalidated on every request by construction: the store looks the
// row up and refuses a revoked one, so revocation is immediate rather than
// waiting for something to expire.
func TestARevokedAPIKeyIsRefused(t *testing.T) {
	t.Parallel()

	opts := gateOptions(middlewareIssuer(t), &accounts{user: account()})
	opts.Keys = &keys{err: auth.ErrUnauthenticated}
	handler, _ := gated(opts)

	if recorder := keyRequest(handler, "kfa1_aa_bb", nil); recorder.Code != http.StatusUnauthorized {
		t.Errorf("a revoked key = %d, want 401", recorder.Code)
	}
}

// The API-key path is a machine credential: it must not mint a session, and it
// must not borrow the authority of one that happens to be in the same request.
func TestAnAPIKeyNeverWidensIntoASession(t *testing.T) {
	t.Parallel()

	user := account()
	issuer := middlewareIssuer(t)
	cookie := sessionCookie(t, issuer, user)
	lookup := &accounts{user: user}

	opts := gateOptions(issuer, lookup)
	opts.Keys = &keys{key: repository.APIKey{ID: "key-1", TenantID: "tenant-machine", Label: "CI"}}
	handler, seen := gated(opts)

	// A machine caller that also carries a browser's cookie is still a machine
	// caller: the key is the credential it presented.
	recorder := keyRequest(handler, "kfa1_aa_bb", cookie)
	if recorder.Code != http.StatusOK {
		t.Fatalf("a request with a key and a cookie = %d, want 200", recorder.Code)
	}
	if seen.principal.Kind != auth.KindAPIKey || seen.principal.UserID != "" {
		t.Errorf("principal = %#v, want the key's tenant and no user", seen.principal)
	}
	if seen.principal.TenantID != "tenant-machine" {
		t.Errorf("tenant = %q, want the key's own tenant", seen.principal.TenantID)
	}
	// And the key path asked storage nothing about accounts: a machine caller
	// never touches the user lookup, so a key cannot be used to probe it.
	if lookup.calls != 0 {
		t.Errorf("the user lookup was called %d times for an API-key request, want 0", lookup.calls)
	}
}
