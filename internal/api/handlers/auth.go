package handlers

import (
	"context"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslabs/kilas-flow/internal/api/middleware"
	"github.com/kilaslabs/kilas-flow/internal/auth"
	"github.com/kilaslabs/kilas-flow/internal/repository"
)

const (
	// maxConcurrentPasswordChecks caps how many password hashes this process
	// computes at once.
	//
	// Signing in is the only unauthenticated request that costs real CPU: one
	// verification is a deliberate 600,000 PBKDF2 iterations, and an unknown
	// address pays the same for its decoy hash. Uncapped, a flood of logins
	// fills every core the API shares, and an authenticated read waits seconds
	// behind work that will almost all be refusals.
	//
	// Four is a quarter of a small server's cores: low enough that the rest of
	// the API keeps running through a flood, high enough that a handful of
	// people signing in at once — an office arriving in the morning — never
	// notices. The number is a ceiling on damage, not a throughput target;
	// raising it trades the API's responsiveness for a slightly shorter queue
	// of guesses.
	maxConcurrentPasswordChecks = 4

	// passwordCheckBudget bounds how long a sign-in waits for a hash slot.
	//
	// Waiting is what turns a flood into an unbounded queue: every refused
	// request still holds a goroutine and a connection. Half a second is long
	// enough that a person on a loaded instance still gets in on the first
	// try, and short enough that a flood is shed rather than absorbed. A caller
	// refused this way is told to retry, which costs the process nothing.
	passwordCheckBudget = 500 * time.Millisecond
)

// PrincipalResource is what the dashboard needs to render "who am I".
//
// It carries no password hash, no key secret, and no other tenant's existence.
type PrincipalResource struct {
	TenantID string `json:"tenantId" doc:"Tenant every request from this caller is scoped to"`
	Kind     string `json:"kind" doc:"api_key or session"`
	UserID   string `json:"userId,omitempty" doc:"Set for a signed-in person"`
	Email    string `json:"email,omitempty"`
	Name     string `json:"name,omitempty"`
	KeyID    string `json:"keyId,omitempty" doc:"Set for a machine caller"`
	Label    string `json:"label,omitempty" doc:"The key's name, for a machine caller"`
}

// APIKeyResource describes a stored key. It can never carry the secret,
// because the struct has nowhere to put one.
type APIKeyResource struct {
	ID         string     `json:"id"`
	Prefix     string     `json:"prefix" doc:"Public handle, enough to recognise a key in a log"`
	Label      string     `json:"label"`
	CreatedAt  time.Time  `json:"createdAt"`
	LastUsedAt *time.Time `json:"lastUsedAt,omitempty" doc:"Accurate to about a minute"`
	RevokedAt  *time.Time `json:"revokedAt,omitempty"`
}

// CreatedAPIKeyResource is the one response that carries a key's secret.
type CreatedAPIKeyResource struct {
	ID        string    `json:"id"`
	Prefix    string    `json:"prefix"`
	Label     string    `json:"label"`
	CreatedAt time.Time `json:"createdAt"`
	// Token is the whole credential. This is the only time it exists anywhere
	// but the caller's hands: the server stores a hash and cannot reproduce it,
	// so a caller who loses it has to mint another.
	Token string `json:"token" doc:"The full key. Shown once and never again."`
}

// StreamTicketResource authorises one execution's event stream.
type StreamTicketResource struct {
	Ticket      string    `json:"ticket" doc:"Spend as the ticket query parameter on the events endpoint"`
	ExecutionID string    `json:"executionId"`
	ExpiresAt   time.Time `json:"expiresAt"`
}

// Auth serves sign-in, sign-out, identity, key management, and stream tickets.
type Auth struct {
	store  repository.AuthRepository
	issuer *auth.Issuer
	// executions confirms that a stream ticket names an execution the caller
	// can already see, so a ticket cannot be minted for somebody else's run.
	executions   repository.ExecutionRepository
	tenants      TenantResolver
	cookieName   string
	secureCookie bool
	// limiter throttles sign-in by client address and by account. It is a
	// pointer to shared state: the buckets have to outlive one request.
	limiter *middleware.LoginLimiter
	// passwords is the slot pool that caps concurrent PBKDF2 work. A buffered
	// channel rather than a semaphore package: acquisition is a select with a
	// timeout, which is exactly what a channel offers.
	passwords chan struct{}
}

// NewAuth builds the identity handler.
//
// A nil store or issuer leaves every operation reporting that authentication is
// not configured, rather than half-working: an install with no signing key must
// not be able to mint a session nobody can verify.
func NewAuth(store repository.AuthRepository, issuer *auth.Issuer, executions repository.ExecutionRepository, tenants TenantResolver) *Auth {
	if tenants == nil {
		tenants = defaultTenantResolver{}
	}
	return &Auth{
		store: store, issuer: issuer, executions: executions, tenants: tenants,
		cookieName: auth.SessionCookieName, secureCookie: true,
		limiter:   middleware.NewLoginLimiter(middleware.DefaultLoginAttemptsPerMinute),
		passwords: make(chan struct{}, maxConcurrentPasswordChecks),
	}
}

// WithLoginLimiter replaces the sign-in throttle.
//
// The throttle is built by default rather than left to the caller, because an
// endpoint this expensive must not be unprotected by an omission. This exists
// for a deployment that wants a different allowance, and for a test that wants
// to drive one.
func (handler *Auth) WithLoginLimiter(limiter *middleware.LoginLimiter) *Auth {
	handler.limiter = limiter
	return handler
}

// WithCookie chooses the session cookie's name and whether it is Secure.
//
// An insecure cookie loses the __Host- prefix as well, because a browser
// refuses that prefix without Secure — the two cannot be set independently.
func (handler *Auth) WithCookie(name string, secure bool) *Auth {
	if name != "" {
		handler.cookieName = name
	}
	handler.secureCookie = secure
	return handler
}

type loginInput struct {
	Body struct {
		Email    string `json:"email" minLength:"3" maxLength:"255" doc:"Account email address"`
		Password string `json:"password" minLength:"1" maxLength:"1024" doc:"Account password"`
	}
}

type loginOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      PrincipalResource
}

type logoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type meOutput struct {
	Body PrincipalResource
}

// listAPIKeysInput is one page of a tenant's keys.
//
// The bounds match the repository's own clamp, so an out-of-range value is
// refused at the edge with a schema error rather than silently clamped behind
// the caller's back. An absent limit is not validated at all and reaches the
// repository as zero, which is its documented default.
type listAPIKeysInput struct {
	Limit  int    `query:"limit" minimum:"1" maximum:"500" doc:"Maximum keys to return (default 100)"`
	Cursor string `query:"cursor" doc:"Opaque cursor from the previous page's X-Next-Cursor header"`
}

type listAPIKeysOutput struct {
	// NextCursor is empty on the last page. It travels in a header because the
	// body is the object the dashboard already reads: adding a field to it
	// would be a change to a contract a running client is parsing.
	NextCursor string `header:"X-Next-Cursor" doc:"Cursor for the next page; empty when there is none"`
	Body       struct {
		Items []APIKeyResource `json:"items"`
	}
}

type createAPIKeyInput struct {
	Body struct {
		Label string `json:"label" maxLength:"255" doc:"How this key will be recognised later"`
	}
}

type createAPIKeyOutput struct {
	Status int `status:"201"`
	Body   CreatedAPIKeyResource
}

type revokeAPIKeyInput struct {
	ID string `path:"id"`
}

type revokeAPIKeyOutput struct {
	Body APIKeyResource
}

type createStreamTicketInput struct {
	Body struct {
		ExecutionID string `json:"executionId" minLength:"1" doc:"Execution whose events will be streamed"`
	}
}

type createStreamTicketOutput struct {
	Status int `status:"201"`
	Body   StreamTicketResource
}

// loginAddress is the middleware that hands the login handler its client's
// address.
//
// huma passes a handler a bare context.Context, so a value the handler needs
// has to be put there by the operation's own middleware. Doing it here rather
// than in the router's authentication gate keeps the dependency where it is
// used: the throttle cannot be silently defused by a deployment that mounts
// these operations differently, and the address is read from the connection
// (ctx.RemoteAddr) rather than from any header a caller could set.
var loginAddress = huma.Middlewares{func(ctx huma.Context, next func(huma.Context)) {
	next(huma.WithContext(ctx, middleware.WithClientIP(ctx.Context(), ctx.RemoteAddr())))
}}

// Register wires the identity operations.
func (handler *Auth) Register(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "login", Method: http.MethodPost, Path: "/auth/login",
		Summary: "Sign in",
		Description: "Exchanges an email and password for a session cookie. " +
			"The cookie is HttpOnly, so the browser can never read it back.",
		Tags:        []string{"Auth"},
		Middlewares: loginAddress,
	}, handler.Login)

	huma.Register(api, huma.Operation{
		OperationID: "logout", Method: http.MethodPost, Path: "/auth/logout",
		DefaultStatus: http.StatusNoContent,
		Summary:       "Sign out",
		Description: "Clears the session cookie in this browser. The session token itself is " +
			"stateless and stays valid until it expires, so a copy taken beforehand is not revoked.",
		Tags: []string{"Auth"},
	}, handler.Logout)

	huma.Register(api, huma.Operation{
		OperationID: "get-me", Method: http.MethodGet, Path: "/auth/me",
		Summary:     "Describe the current caller",
		Description: "Reports the tenant and identity this request authenticated as.",
		Tags:        []string{"Auth"},
	}, handler.Me)

	huma.Register(api, huma.Operation{
		OperationID: "list-api-keys", Method: http.MethodGet, Path: "/api-keys",
		Summary:     "List API keys",
		Description: "Lists one page of this tenant's keys, newest first. No secret is ever included. The next page's cursor is in the X-Next-Cursor response header, empty on the last page.",
		Tags:        []string{"Auth"},
	}, handler.ListKeys)

	huma.Register(api, huma.Operation{
		OperationID: "create-api-key", Method: http.MethodPost, Path: "/api-keys",
		DefaultStatus: http.StatusCreated,
		Summary:       "Create an API key",
		Description: "Mints a key scoped to the calling tenant and returns it in full exactly once. " +
			"The server keeps only a hash and cannot show it again.",
		Tags: []string{"Auth"},
	}, handler.CreateKey)

	huma.Register(api, huma.Operation{
		OperationID: "revoke-api-key", Method: http.MethodDelete, Path: "/api-keys/{id}",
		Summary:     "Revoke an API key",
		Description: "Stops a key authenticating, immediately and permanently. The row is kept so an audit still has something to name.",
		Tags:        []string{"Auth"},
	}, handler.RevokeKey)

	huma.Register(api, huma.Operation{
		OperationID: "create-stream-ticket", Method: http.MethodPost, Path: "/stream-tickets",
		DefaultStatus: http.StatusCreated,
		Summary:       "Mint an execution stream ticket",
		Description: "EventSource cannot send an Authorization header, so a caller exchanges its " +
			"credential for a single-use ticket and spends it on the events endpoint. Tickets live " +
			"for seconds and name one execution.",
		Tags: []string{"Auth"},
	}, handler.CreateStreamTicket)
}

// Login exchanges a password for a session cookie.
//
// Every failure answers 401 with one message. An unknown address, a wrong
// password and a disabled account are indistinguishable from outside, so the
// endpoint cannot be used to find out who has an account here. The two refusals
// that are not about the credentials at all — too many attempts, and the
// process already hashing as much as it will — are 429 and 503, and they are
// the same for every address, so they say nothing about who exists either.
//
// The work is bounded on the way in: an attempt is counted against the client's
// address and against the account before anything is hashed, and the hash
// itself runs in one of a small number of slots. An unauthenticated flood
// therefore costs this endpoint a refusal rather than the whole process its
// CPU.
func (handler *Auth) Login(ctx context.Context, input *loginInput) (*loginOutput, error) {
	if handler.store == nil || handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	const refusal = "email address or password is incorrect"

	// Two buckets, because each covers the other's blind spot: one address
	// working through a list of accounts meets the address bucket, and one
	// account guessed from many addresses meets the account bucket. An attempt
	// that cannot say which address it came from shares one bucket, which is
	// the fail-closed reading of not knowing.
	clientIP := middleware.ClientIPFrom(ctx)
	if clientIP == "" {
		clientIP = "unknown"
	}
	account := repository.NormalizeEmail(input.Body.Email)
	accountKey := "account:" + account
	for _, key := range []string{"address:" + clientIP, accountKey} {
		if allowed, retryAfter := handler.limiter.Allow(key); !allowed {
			// Not logged: the access log already carries the 429, and a
			// refusal this cheap must not be usable to fill a log disk.
			return nil, huma.ErrorWithHeaders(
				huma.Error429TooManyRequests("too many sign-in attempts; wait and try again"),
				http.Header{"Retry-After": []string{strconv.Itoa(int(retryAfter / time.Second))}},
			)
		}
	}

	user, err := handler.store.FindUserForLogin(ctx, input.Body.Email)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			// The password is still hashed for an address that does not exist,
			// so the time a refusal takes does not reveal whether it did.
			if _, busy := handler.matchPassword(ctx, input.Body.Password, auth.DecoyPasswordHash()); busy {
				return nil, handler.busyRefusal()
			}
			handler.noteRefusal(ctx, account, clientIP, "no such account")
			handler.limiter.Fail(accountKey)
			return nil, huma.Error401Unauthorized(refusal)
		}
		return nil, serverProblem(ctx, "could not read the account", err)
	}

	matched, busy := handler.matchPassword(ctx, input.Body.Password, user.PasswordHash)
	if busy {
		return nil, handler.busyRefusal()
	}
	if !matched || user.DisabledAt != nil {
		reason := "wrong password"
		if user.DisabledAt != nil {
			reason = "account disabled"
		}
		handler.noteRefusal(ctx, account, clientIP, reason)
		handler.limiter.Fail(accountKey)
		return nil, huma.Error401Unauthorized(refusal)
	}
	// The password was right, so the mistakes made on the way here are
	// forgotten. Only the account's bucket is cleared: forgiving the address
	// bucket would let one working account reset an attacker's allowance for
	// everything else.
	handler.limiter.Succeed(accountKey)

	_, token, err := handler.issuer.IssueSession(user.ID, user.TenantID, user.Email,
		auth.UserVersion(user.PasswordHash, user.DisabledAt != nil))
	if err != nil {
		return nil, serverProblem(ctx, "could not start a session", err)
	}

	return &loginOutput{
		SetCookie: handler.cookie(token, int(handler.issuer.SessionTTL().Seconds())),
		Body: PrincipalResource{
			TenantID: user.TenantID, Kind: string(auth.KindSession),
			UserID: user.ID, Email: user.Email, Name: user.Name,
		},
	}, nil
}

// matchPassword verifies one password in one of the process's hash slots.
//
// The second result reports that no slot came free inside passwordCheckBudget,
// which the caller answers by telling the client to retry rather than by
// waiting: the wait is what an attacker would use to hold a goroutine per
// connection.
func (handler *Auth) matchPassword(ctx context.Context, password, stored string) (bool, bool) {
	timer := time.NewTimer(passwordCheckBudget)
	defer timer.Stop()

	select {
	case handler.passwords <- struct{}{}:
		// The deferred release runs after the comparison has produced its
		// result, so the slot is held for exactly the CPU work and not a
		// moment longer.
		defer func() { <-handler.passwords }()
		return auth.MatchPassword(password, stored), false
	case <-timer.C:
		return false, true
	case <-ctx.Done():
		return false, true
	}
}

// busyRefusal answers a sign-in that could not start hashing.
//
// 503 rather than 429 because the limit that was reached is the process's, not
// this caller's: the same answer goes to everyone while the slots are full, so
// it reveals nothing about the address that asked. Retry-After says when the
// client should come back, which for a queue this short is measured in the time
// one hash takes.
//
// It is not logged, for the reason noteRefusal gives: this is a path a flood
// reaches at full rate, and the access log already records the status.
func (handler *Auth) busyRefusal() error {
	return huma.ErrorWithHeaders(
		huma.Error503ServiceUnavailable("sign-in is busy; try again shortly"),
		http.Header{"Retry-After": []string{"1"}},
	)
}

// noteRefusal records a sign-in that was refused on its credentials.
//
// The address and the account are logged and the password never is: a password
// in a log is a credential in a log. Throttled and saturated refusals are not
// logged here at all — they are the cheap paths, and a flood of them would turn
// this line into the log-flood vector the throttle exists to prevent.
func (handler *Auth) noteRefusal(ctx context.Context, account, clientIP, reason string) {
	slog.WarnContext(ctx, "sign-in refused",
		slog.String("email", account),
		slog.String("client_ip", clientIP),
		slog.String("reason", reason),
		slog.String("request_id", middleware.RequestIDFrom(ctx)),
	)
}

// Logout clears the cookie in the browser that asked.
func (handler *Auth) Logout(ctx context.Context, _ *struct{}) (*logoutOutput, error) {
	return &logoutOutput{SetCookie: handler.cookie("", -1)}, nil
}

// Me describes the caller.
func (handler *Auth) Me(ctx context.Context, _ *struct{}) (*meOutput, error) {
	principal, found := auth.PrincipalFrom(ctx)
	if !found {
		return nil, huma.Error401Unauthorized("this request is not authenticated")
	}
	resource := PrincipalResource{
		TenantID: principal.TenantID, Kind: string(principal.Kind),
		UserID: principal.UserID, KeyID: principal.KeyID,
	}
	if principal.Kind == auth.KindSession {
		resource.Email = principal.Label
	} else {
		resource.Label = principal.Label
	}
	return &meOutput{Body: resource}, nil
}

// ListKeys returns this tenant's keys, secrets excluded by construction.
func (handler *Auth) ListKeys(ctx context.Context, input *listAPIKeysInput) (*listAPIKeysOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	page, err := handler.store.ListAPIKeysPage(ctx, tenant, repository.APIKeyFilter{
		Limit: input.Limit, Cursor: input.Cursor,
	})
	// A cursor the client did not receive from this API is a bad request, not a
	// server fault, so it must not be reported as a 500.
	if errors.Is(err, repository.ErrInvalidCursor) {
		return nil, huma.Error400BadRequest("api key cursor is invalid")
	}
	if err != nil {
		return nil, serverProblem(ctx, "could not list API keys", err)
	}
	out := &listAPIKeysOutput{NextCursor: page.NextCursor}
	out.Body.Items = make([]APIKeyResource, 0, len(page.Keys))
	for _, key := range page.Keys {
		out.Body.Items = append(out.Body.Items, apiKeyResource(key))
	}
	return out, nil
}

// CreateKey mints a key for the calling tenant.
func (handler *Auth) CreateKey(ctx context.Context, input *createAPIKeyInput) (*createAPIKeyOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	key, token, err := handler.store.CreateAPIKey(ctx, tenant, input.Body.Label)
	if err != nil {
		return nil, serverProblem(ctx, "could not create the API key", err)
	}
	return &createAPIKeyOutput{
		Status: http.StatusCreated,
		Body: CreatedAPIKeyResource{
			ID: key.ID, Prefix: key.Prefix, Label: key.Label,
			CreatedAt: key.CreatedAt, Token: token,
		},
	}, nil
}

// RevokeKey withdraws a key belonging to the calling tenant.
func (handler *Auth) RevokeKey(ctx context.Context, input *revokeAPIKeyInput) (*revokeAPIKeyOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	key, err := handler.store.RevokeAPIKey(ctx, tenant, input.ID)
	if err != nil {
		if errors.Is(err, repository.ErrNotFound) {
			return nil, huma.Error404NotFound("API key not found")
		}
		return nil, serverProblem(ctx, "could not revoke the API key", err)
	}
	return &revokeAPIKeyOutput{Body: apiKeyResource(key)}, nil
}

// CreateStreamTicket exchanges the caller's credential for a stream ticket.
func (handler *Auth) CreateStreamTicket(ctx context.Context, input *createStreamTicketInput) (*createStreamTicketOutput, error) {
	if handler.issuer == nil {
		return nil, huma.Error503ServiceUnavailable("authentication is not configured on this instance")
	}
	tenant := handler.tenants.Resolve(ctx)
	if tenant.ID == "" {
		return nil, huma.Error401Unauthorized("this request is not authenticated")
	}
	// The execution is confirmed inside the caller's own tenant first, so a
	// ticket can never be minted for a run the caller could not already read.
	if handler.executions != nil {
		if _, err := handler.executions.Get(ctx, tenant, input.Body.ExecutionID); err != nil {
			return nil, huma.Error404NotFound("execution not found")
		}
	}
	ticket, token, err := handler.issuer.IssueTicket(tenant.ID, input.Body.ExecutionID, 0)
	if err != nil {
		return nil, serverProblem(ctx, "could not mint a stream ticket", err)
	}
	return &createStreamTicketOutput{
		Status: http.StatusCreated,
		Body: StreamTicketResource{
			Ticket: token, ExecutionID: ticket.ExecutionID, ExpiresAt: ticket.ExpiresAt,
		},
	}, nil
}

// cookie builds the session cookie.
//
// SameSite=Lax is the only cross-site request forgery defence here: there is no
// CSRF token, so a state-changing request is protected because the browser
// declines to attach this cookie to one arriving from another site. An embedded
// editor does not rely on it — it carries an embed token instead — which is why
// Lax is affordable.
func (handler *Auth) cookie(value string, maxAge int) http.Cookie {
	return http.Cookie{
		Name:     handler.cookieName,
		Value:    value,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   handler.secureCookie,
		SameSite: http.SameSiteLaxMode,
	}
}

func apiKeyResource(key repository.APIKey) APIKeyResource {
	return APIKeyResource{
		ID: key.ID, Prefix: key.Prefix, Label: key.Label,
		CreatedAt: key.CreatedAt, LastUsedAt: key.LastUsedAt, RevokedAt: key.RevokedAt,
	}
}
