package handlers

// Sign-in hardening proof: the endpoint throttles by client address and by
// account, it says how long to wait, the address it throttles is the connected
// one rather than anything a caller can set, and the password work is capped so
// a flood cannot starve the rest of the API.

import (
	"context"
	"crypto/pbkdf2"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"

	"github.com/kilaslab/kilas-flow/internal/api/middleware"
	"github.com/kilaslab/kilas-flow/internal/auth"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// loginStore answers the two questions these tests ask of storage: who signs in
// as an address, and one page of a tenant's keys.
//
// Every other method is unreachable from these tests, so the embedded nil
// interface is the honest way to say so.
type loginStore struct {
	repository.AuthRepository
	user     repository.User
	err      error
	page     repository.APIKeyPage
	pageErr  error
	received repository.APIKeyFilter
}

func (store *loginStore) FindUserForLogin(context.Context, string) (repository.User, error) {
	if store.err != nil {
		return repository.User{}, store.err
	}
	return store.user, nil
}

func (store *loginStore) ListAPIKeysPage(_ context.Context, _ repository.TenantScope, filter repository.APIKeyFilter) (repository.APIKeyPage, error) {
	store.received = filter
	return store.page, store.pageErr
}

func loginTestKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index * 7)
	}
	return key
}

// loginTestHandler builds a handler whose sign-in throttle runs on a stopped
// clock.
//
// The throttle refills with wall time, so a test that spends an allowance and
// then looks for a refusal is asserting on how long its own attempts take
// unless it says what "now" is. On the loaded machine the landing gates run on,
// one wrong-password attempt can cost longer than the six seconds a token takes
// to come back, and the bucket refilled underneath the loop — that is the flake
// this file used to fail with. Held still, the clock lets the refusal be
// asserted on the attempt count, which is the limiter's actual contract, and
// leaves every attempt's real cost out of the result.
func loginTestHandler(t *testing.T, store repository.AuthRepository) *Auth {
	t.Helper()
	issuer, err := auth.NewIssuer(loginTestKey(), time.Hour, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	stopped := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	limiter := middleware.NewLoginLimiter(middleware.DefaultLoginAttemptsPerMinute).
		WithClock(func() time.Time { return stopped })
	return NewAuth(store, issuer, nil, nil).WithLoginLimiter(limiter)
}

// cheapPasswordHash stores a password at one PBKDF2 iteration.
//
// The stored form records its own work factor, so a real hash here would make
// these tests pay six hundred thousand iterations per attempt — a minute of
// hashing to prove a counter. What is under test is the throttle, and the hash
// still has to be one MatchPassword accepts.
func cheapPasswordHash(t *testing.T, password, salt string) string {
	t.Helper()
	derived, err := pbkdf2.Key(sha256.New, password, []byte(salt), 1, sha256.Size)
	if err != nil {
		t.Fatalf("pbkdf2.Key() error = %v", err)
	}
	return "pbkdf2-sha256$1$" +
		base64.RawStdEncoding.EncodeToString([]byte(salt)) + "$" +
		base64.RawStdEncoding.EncodeToString(derived)
}

func loginAttempt(t *testing.T, handler *Auth, address, email, password string) error {
	t.Helper()
	ctx := middleware.WithClientIP(context.Background(), address)
	input := &loginInput{}
	input.Body.Email = email
	input.Body.Password = password
	_, err := handler.Login(ctx, input)
	return err
}

func loginStatus(t *testing.T, err error) int {
	t.Helper()
	if err == nil {
		return http.StatusOK
	}
	var status huma.StatusError
	if !errors.As(err, &status) {
		t.Fatalf("login error = %v, want a status error", err)
	}
	return status.GetStatus()
}

// retryAfter reads the header the client needs in order to back off, which the
// status code alone cannot express.
func retryAfter(t *testing.T, err error) int {
	t.Helper()
	var withHeaders huma.HeadersError
	if !errors.As(err, &withHeaders) {
		t.Fatalf("login error = %v, want a 429 carrying Retry-After", err)
	}
	raw := withHeaders.GetHeaders().Get("Retry-After")
	seconds, parseErr := strconv.Atoi(raw)
	if parseErr != nil || seconds < 1 {
		t.Fatalf("Retry-After = %q, want a positive number of seconds", raw)
	}
	return seconds
}

// waitForRefusal spends an allowance until the handler refuses an attempt.
//
// The handler's limiter runs on a clock this file holds still, so nothing about
// refill can intrude and the refusal is asserted on the attempt count rather
// than on how long the attempts take. One allowance is the bound: a spray from
// one address empties the address bucket on its tenth attempt and is refused on
// the eleventh, and a run at a single account empties the account's bucket
// faster still, because a wrong password costs it two tokens.
func waitForRefusal(t *testing.T, attempt func(i int) error) error {
	t.Helper()
	bound := middleware.DefaultLoginAttemptsPerMinute + 1
	for i := range bound {
		err := attempt(i)
		switch status := loginStatus(t, err); status {
		case http.StatusTooManyRequests:
			return err
		case http.StatusUnauthorized:
		default:
			t.Fatalf("attempt %d = %d, want 401 until the bucket is empty", i, status)
		}
	}
	t.Fatalf("no refusal within %d attempts", bound)
	return nil
}

// One address spraying many accounts is the shape of the flood the finding
// measured: nothing about a single account looks wrong, and the work is spread
// across addresses that do not exist.
func TestLoginRefusesASprayFromOneAddress(t *testing.T) {
	handler := loginTestHandler(t, &loginStore{err: repository.ErrNotFound})
	const attacker = "203.0.113.7:40001"

	refusal := waitForRefusal(t, func(i int) error {
		return loginAttempt(t, handler, attacker, "nobody"+strconv.Itoa(i)+"@example.test", "guess")
	})
	if retryAfter(t, refusal) < 1 {
		t.Error("the refusal did not say how long to wait")
	}

	// The throttle is per address, so a second client is unaffected by the
	// first one's spending. Anything else would let one attacker lock out the
	// whole installation.
	other := loginAttempt(t, handler, "203.0.113.8:40002", "someone@example.test", "guess")
	if status := loginStatus(t, other); status != http.StatusUnauthorized {
		t.Errorf("a different address = %d, want the ordinary 401", status)
	}
}

// NewAuth builds the sign-in throttle itself rather than leaving it to the
// caller, because an endpoint this expensive must not be unprotected by an
// omission. Every other test here replaces that limiter through WithLoginLimiter
// to drive the bucket it wants, so this is the only one that proves the default
// the server actually ships: it builds the handler the way the server does and
// holds that limiter's own clock still, so the refusal is asserted on the
// attempt count rather than on how long the attempts take.
func TestLoginRefusesASprayWithTheDefaultLimiter(t *testing.T) {
	issuer, err := auth.NewIssuer(loginTestKey(), time.Hour, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	// No WithLoginLimiter: whatever NewAuth constructs is what is under test.
	handler := NewAuth(&loginStore{err: repository.ErrNotFound}, issuer, nil, nil)
	if handler.limiter == nil {
		t.Fatal("NewAuth() built no sign-in throttle; a nil one admits every attempt")
	}
	stopped := time.Date(2026, 9, 20, 9, 0, 0, 0, time.UTC)
	handler.limiter.WithClock(func() time.Time { return stopped })

	const attacker = "203.0.113.70:40001"
	refusal := waitForRefusal(t, func(i int) error {
		return loginAttempt(t, handler, attacker, "nobody"+strconv.Itoa(i)+"@example.test", "guess")
	})
	if retryAfter(t, refusal) < 1 {
		t.Error("the refusal did not say how long to wait")
	}
}

// Guessing one account from many addresses is the other half: a credential
// stuffing run that rotates its source address still meets the account's own
// bucket.
func TestLoginRefusesRepeatedGuessesAtOneAccount(t *testing.T) {
	handler := loginTestHandler(t, &loginStore{err: repository.ErrNotFound})
	const victim = "owner@example.test"

	// A wrong password costs two tokens — one for the attempt, one for the
	// mistake — so the account's bucket empties in half the addresses an
	// address-only throttle would need.
	refusal := waitForRefusal(t, func(i int) error {
		return loginAttempt(t, handler, "198.51.100."+strconv.Itoa(i)+":40000", victim, "guess")
	})
	if retryAfter(t, refusal) < 1 {
		t.Error("the refusal did not say how long to wait")
	}

	// The throttle is the account's, not the whole installation's: another
	// account from a fresh address is still served.
	if status := loginStatus(t, loginAttempt(t, handler, "198.51.100.201:40000", "someone-else@example.test", "guess")); status != http.StatusUnauthorized {
		t.Errorf("a different account = %d, want the ordinary 401", status)
	}
}

// A person who mistypes a password and then gets it right has proved they are
// not the guesser the counter was tracking, so their mistakes are forgotten.
// Held against them, every typo would leave the account one step closer to
// being locked out.
func TestLoginForgetsMistakesAfterASuccessfulSignIn(t *testing.T) {
	hash := cheapPasswordHash(t, "correct horse", "salt-a")
	store := &loginStore{user: repository.User{
		ID: "usr-1", TenantID: "tenant-a", Email: "owner@example.test",
		Name: "Owner", PasswordHash: hash,
	}}
	handler := loginTestHandler(t, store)

	for attempt := 0; attempt < 3; attempt++ {
		address := "192.0.2." + strconv.Itoa(attempt) + ":40000"
		if status := loginStatus(t, loginAttempt(t, handler, address, "owner@example.test", "wrong")); status != http.StatusUnauthorized {
			t.Fatalf("mistake %d = %d, want 401", attempt, status)
		}
	}
	// The correct password from a fresh address: a success resets the account's
	// bucket, and the address bucket has not been touched by the mistakes.
	if err := loginAttempt(t, handler, "192.0.2.50:40000", "owner@example.test", "correct horse"); err != nil {
		t.Fatalf("sign-in with the right password error = %v", err)
	}

	// Five more mistakes from addresses of their own. Without the reset the
	// account bucket would already hold three failures plus three penalties
	// plus the sign-in, so these would run out after one.
	for attempt := 0; attempt < 5; attempt++ {
		address := "192.0.2." + strconv.Itoa(60+attempt) + ":40000"
		if status := loginStatus(t, loginAttempt(t, handler, address, "owner@example.test", "wrong")); status != http.StatusUnauthorized {
			t.Errorf("mistake %d after a successful sign-in = %d, want 401", attempt, status)
		}
	}
}

// Signing in is the most expensive thing an unauthenticated caller can ask for,
// so the number of hashes in flight is capped: past the cap the endpoint refuses
// instead of queueing, and an authenticated read is not made to wait behind a
// flood of passwords.
func TestLoginRefusesWhileEveryPasswordSlotIsBusy(t *testing.T) {
	hash := cheapPasswordHash(t, "correct horse", "salt-b")
	handler := loginTestHandler(t, &loginStore{user: repository.User{
		ID: "usr-1", TenantID: "tenant-a", Email: "owner@example.test", PasswordHash: hash,
	}})

	// Every slot taken, as it would be under a flood.
	for slot := 0; slot < maxConcurrentPasswordChecks; slot++ {
		handler.passwords <- struct{}{}
	}
	defer func() {
		for slot := 0; slot < maxConcurrentPasswordChecks; slot++ {
			<-handler.passwords
		}
	}()

	started := time.Now()
	err := loginAttempt(t, handler, "203.0.113.30:40000", "owner@example.test", "correct horse")
	if status := loginStatus(t, err); status != http.StatusServiceUnavailable {
		t.Fatalf("login with every slot busy = %d, want 503", status)
	}
	// Refused rather than queued: the wait is bounded, so a caller cannot hold
	// a goroutine behind the flood indefinitely.
	if elapsed := time.Since(started); elapsed > 5*time.Second {
		t.Errorf("the refusal took %s, want it bounded", elapsed)
	}

	// An address with no account still costs a decoy hash, and that hash is
	// capped by the same slots: refusing it here is what proves the decoy work
	// is still done rather than skipped once the throttle was added.
	unknown := loginTestHandler(t, &loginStore{err: repository.ErrNotFound})
	for slot := 0; slot < maxConcurrentPasswordChecks; slot++ {
		unknown.passwords <- struct{}{}
	}
	defer func() {
		for slot := 0; slot < maxConcurrentPasswordChecks; slot++ {
			<-unknown.passwords
		}
	}()
	decoy := loginAttempt(t, unknown, "203.0.113.31:40000", "nobody@example.test", "guess")
	if status := loginStatus(t, decoy); status != http.StatusServiceUnavailable {
		t.Errorf("an unknown address with every slot busy = %d, want 503", status)
	}
}

// The address has to come from the connection. A caller who could name it in a
// header would rotate it per request and never meet a bucket.
func TestLoginThrottlesByTheConnectedAddress(t *testing.T) {
	handler := loginTestHandler(t, &loginStore{user: repository.User{
		ID: "usr-1", TenantID: "tenant-a", Email: "owner@example.test",
		PasswordHash: cheapPasswordHash(t, "correct horse", "salt-c"),
	}})

	router := chi.NewMux()
	// Mounted the way the server mounts it: a version group over the API,
	// because a middleware attached to an operation has to survive that.
	handler.Register(huma.NewGroup(humachi.New(router, huma.DefaultConfig("test", "0")), "/api/v1"))

	attempt := func(address, forwarded, email string) *httptest.ResponseRecorder {
		request := httptest.NewRequest(http.MethodPost, "/api/v1/auth/login",
			strings.NewReader(`{"email":"`+email+`","password":"wrong"}`))
		request.Header.Set("Content-Type", "application/json")
		if forwarded != "" {
			request.Header.Set("X-Forwarded-For", forwarded)
		}
		request.RemoteAddr = address
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, request)
		return recorder
	}

	for attemptIndex := 0; attemptIndex < middleware.DefaultLoginAttemptsPerMinute; attemptIndex++ {
		// A different claimed address and a different account on every attempt,
		// so only the connection can be what runs out: if the header were
		// honoured, each attempt would land in a bucket of its own.
		response := attempt("198.51.100.20:50000", "10.0.0."+strconv.Itoa(attemptIndex),
			"owner"+strconv.Itoa(attemptIndex)+"@example.test")
		if response.Code != http.StatusUnauthorized {
			t.Fatalf("attempt %d = %d, want 401 (body: %s)", attemptIndex, response.Code, response.Body)
		}
	}

	refused := attempt("198.51.100.20:50000", "10.0.0.99", "owner-again@example.test")
	if refused.Code != http.StatusTooManyRequests {
		t.Fatalf("the connected address was not throttled: got %d (body: %s)", refused.Code, refused.Body)
	}
	if refused.Header().Get("Retry-After") == "" {
		t.Error("the refusal carried no Retry-After header")
	}

	// And it is the connection that is counted, not the whole process: a second
	// client gets its own allowance.
	if other := attempt("198.51.100.21:50001", "", "owner-elsewhere@example.test"); other.Code != http.StatusUnauthorized {
		t.Errorf("a second client = %d, want the ordinary 401 (body: %s)", other.Code, other.Body)
	}
}

// Key listing pages through the X-Next-Cursor header rather than a field in the
// body, because the dashboard already reads {"items": [...]} and a running
// client must not have to change to keep working.
func TestListAPIKeysPagesThroughTheHeader(t *testing.T) {
	store := &loginStore{page: repository.APIKeyPage{
		Keys: []repository.APIKey{
			{ID: "key-1", TenantID: "tenant-a", Prefix: "aa", Label: "CI"},
			{ID: "key-2", TenantID: "tenant-a", Prefix: "bb", Label: "Deploy"},
		},
		NextCursor: "c2Vjb25k",
	}}
	handler := loginTestHandler(t, store)

	out, err := handler.ListKeys(context.Background(), &listAPIKeysInput{Limit: 2, Cursor: "Zmlyc3Q"})
	if err != nil {
		t.Fatalf("ListKeys() error = %v", err)
	}
	if len(out.Body.Items) != 2 || out.Body.Items[0].ID != "key-1" {
		t.Errorf("items = %#v, want the page the store returned", out.Body.Items)
	}
	if out.NextCursor != "c2Vjb25k" {
		t.Errorf("NextCursor = %q, want the store's cursor", out.NextCursor)
	}
	// The page request reaches the repository unchanged: the handler is a
	// translation layer, not a second place that decides what a page is.
	if store.received.Limit != 2 || store.received.Cursor != "Zmlyc3Q" {
		t.Errorf("repository filter = %#v, want the request's limit and cursor", store.received)
	}
	// No secret can appear, because the resource has nowhere to put one.
	if out.Body.Items[0].Prefix != "aa" || out.Body.Items[0].Label != "CI" {
		t.Errorf("first item = %#v, want the key's public handle and label", out.Body.Items[0])
	}
}

// A cursor the client did not receive from this API is the client's mistake, so
// it is a 400 rather than the 500 a raw repository error would produce.
func TestListAPIKeysRefusesACursorItDidNotIssue(t *testing.T) {
	handler := loginTestHandler(t, &loginStore{pageErr: repository.ErrInvalidCursor})

	_, err := handler.ListKeys(context.Background(), &listAPIKeysInput{Cursor: "not-a-cursor"})
	if status := loginStatus(t, err); status != http.StatusBadRequest {
		t.Errorf("a bad cursor = %d, want 400", status)
	}
}
