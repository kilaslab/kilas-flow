package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"log/slog"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/idempotency"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// OAuthCallbackPath is the public Google redirect URI. It is mounted on the
// mux, not under the API prefix, so a browser popup can finish Connect without
// an API key. Google blocks OAuth inside iframes; the editor opens this in
// window.open and the callback posts a message to the opener.
const OAuthCallbackPath = "/oauth/callback"

// oauthNonceCookiePrefix names the cookie that binds one Connect popup to the
// browser that started it. The rest of the name comes from the state, so two
// popups open in one browser each keep their own cookie instead of the second
// silently breaking the first.
const oauthNonceCookiePrefix = "kilasflow_oauth_"

// oauthStateOperation is the one-time-key purpose a used Connect state is
// recorded under.
const oauthStateOperation = "oauth-state"

// OAuthStateLedger records which Connect states have been used, so each one is
// good for a single callback.
//
// It has to be shared by every process that can serve the callback: behind a
// load balancer the replay can land on a different replica from the first
// callback, and a ledger in one process's memory would admit it there.
type OAuthStateLedger interface {
	// ConsumeOnce marks key used until until and reports whether this call was
	// the first to use it.
	ConsumeOnce(ctx context.Context, tenantID, key string, until time.Time) (bool, error)
}

// memoryOAuthLedger is the ledger used when the composition root supplies
// none: correct for one process, and only for one process. The server binary
// always supplies the database-backed one.
type memoryOAuthLedger struct {
	mu   sync.Mutex
	used map[string]time.Time
}

func (ledger *memoryOAuthLedger) ConsumeOnce(_ context.Context, tenantID, key string, until time.Time) (bool, error) {
	ledger.mu.Lock()
	defer ledger.mu.Unlock()
	now := time.Now()
	if !until.After(now) {
		return false, nil
	}
	if ledger.used == nil {
		ledger.used = map[string]time.Time{}
	}
	for entry, expiry := range ledger.used {
		if !expiry.After(now) {
			delete(ledger.used, entry)
		}
	}
	entry := tenantID + "\x00" + key
	if _, seen := ledger.used[entry]; seen {
		return false, nil
	}
	ledger.used[entry] = until
	return true, nil
}

type oauthStartInput struct {
	ID      string `path:"id" minLength:"1" doc:"Credential identifier"`
	Origin  string `header:"Origin"`
	Referer string `header:"Referer"`
	Host    string `header:"Host"`
	Proto   string `header:"X-Forwarded-Proto"`
}

type oauthStartOutput struct {
	// SetCookie carries the nonce that binds this Connect to the browser that
	// asked for it.
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      oauthStartResource
}

type oauthStartResource struct {
	AuthorizeURL string `json:"authorizeUrl" doc:"Google authorization URL. Open it with window.open, never in an iframe."`
}

func (handler *Credentials) registerOAuth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "start-credential-oauth", Method: http.MethodPost, Path: "/credentials/{id}/oauth/start",
		Summary: "Start Google OAuth",
		Description: "Returns the Google authorization URL for this credential. Open it in a popup (window.open), not an iframe: Google blocks OAuth inside frames. " +
			"The response also sets an HttpOnly cookie that binds the sign-in to this browser: the callback completes only in the browser that made this request, and only once. " +
			"The authorization request uses PKCE (S256).",
		Tags: []string{"Credentials"},
	}, handler.StartOAuth)
}

// StartOAuth mints a CSRF state and returns the Google authorize URL.
//
// The state is bound to the browser that asked: a fresh nonce is set as an
// HttpOnly, SameSite=Lax cookie on this response and its hash is signed into
// the state, and the callback accepts the state only alongside that cookie.
// Lax is the strictest mode that still works — the callback is a top-level
// GET navigation arriving from Google, which Lax sends the cookie on and
// Strict does not. The same nonce derives the PKCE verifier, so nothing needs
// storing between here and the callback.
func (handler *Credentials) StartOAuth(ctx context.Context, input *oauthStartInput) (*oauthStartOutput, error) {
	if handler.store == nil {
		return nil, huma.Error503ServiceUnavailable("credential storage unavailable")
	}
	if len(handler.oauthSigningKey) == 0 {
		return nil, huma.Error503ServiceUnavailable("oauth signing key is not configured")
	}
	tenant := handler.tenants.Resolve(ctx)
	record, fields, err := handler.store.Resolve(ctx, tenant, input.ID)
	if err != nil {
		return nil, handler.problem(ctx, err)
	}
	if !credentials.IsGoogleOAuth(record.Type) {
		return nil, huma.Error422UnprocessableEntity("this credential type does not use Google OAuth")
	}

	clientID, _, err := handler.googleClient(record.Type, fields)
	if err != nil {
		return nil, huma.Error422UnprocessableEntity(err.Error())
	}
	scope := strings.TrimSpace(fields["scope"])
	if scope == "" {
		if record.Type == credentials.GmailOAuthType {
			scope = "https://www.googleapis.com/auth/gmail.modify"
		} else {
			scope = "https://www.googleapis.com/auth/drive"
		}
	}
	redirect := handler.oauthRedirectURL(input.Proto, input.Host, false)
	origin := strings.TrimSpace(input.Origin)
	if origin == "" {
		origin = originOf(input.Referer)
	}
	nonce, err := credentials.NewOAuthNonce()
	if err != nil {
		return nil, serverProblem(ctx, "could not start Google sign-in", err)
	}
	nonceHash := credentials.OAuthNonceHash(nonce)
	state, err := credentials.SignOAuthState(handler.oauthSigningKey, credentials.OAuthState{
		TenantID: tenant.ID, CredentialID: record.ID, Origin: origin, NonceHash: nonceHash,
	})
	if err != nil {
		return nil, huma.Error503ServiceUnavailable(err.Error())
	}
	challenge := credentials.PKCEChallenge(credentials.PKCEVerifier(handler.oauthSigningKey, nonce))
	return &oauthStartOutput{
		SetCookie: oauthNonceCookie(redirect, nonceHash, nonce, int(credentials.OAuthStateTTL.Seconds())),
		Body: oauthStartResource{
			AuthorizeURL: credentials.GoogleAuthorizeURL(clientID, redirect, scope, state, challenge),
		},
	}, nil
}

// oauthNonceCookie is the cookie for one Connect popup. It is scoped to the
// callback's own path, so no other request carries it; Secure whenever the
// callback is served over https, which is where the browser will send it back;
// and it lives no longer than the state. A maxAge below zero deletes it.
func oauthNonceCookie(redirect, nonceHash, value string, maxAge int) http.Cookie {
	cookie := http.Cookie{
		Name:     oauthNonceCookieName(nonceHash),
		Value:    value,
		Path:     OAuthCallbackPath,
		MaxAge:   maxAge,
		HttpOnly: true,
		SameSite: http.SameSiteLaxMode,
	}
	if parsed, err := url.Parse(redirect); err == nil {
		if parsed.Path != "" {
			cookie.Path = parsed.Path
		}
		cookie.Secure = parsed.Scheme == "https"
	}
	return cookie
}

// oauthNonceCookieName is the cookie a state's nonce travels in. It is made
// from the nonce's hash, which the state already carries in the clear, so the
// name reveals nothing the URL does not.
func oauthNonceCookieName(nonceHash string) string {
	if len(nonceHash) > 16 {
		nonceHash = nonceHash[:16]
	}
	return oauthNonceCookiePrefix + nonceHash
}

func (handler *Credentials) googleClient(typeID string, fields map[string]string) (clientID, clientSecret string, err error) {
	clientID = strings.TrimSpace(fields["clientId"])
	clientSecret = strings.TrimSpace(fields["clientSecret"])
	if clientID == "" {
		clientID = strings.TrimSpace(handler.googleClientID)
	}
	if clientSecret == "" {
		clientSecret = strings.TrimSpace(handler.googleClientSecret)
	}
	if clientID == "" || clientSecret == "" {
		return "", "", fmt.Errorf("set a Google Cloud client id and secret on this credential, or configure the platform client (KILASFLOW_GOOGLE_CLIENT_ID)")
	}
	_ = typeID
	return clientID, clientSecret, nil
}

// oauthRedirectURL is built on the same answer to "where is this instance?"
// that the embed origin check uses, so the two cannot drift apart. It keeps
// the public URL's path: Google compares the redirect URI byte for byte.
func (handler *Credentials) oauthRedirectURL(proto, host string, tls bool) string {
	return embed.SelfURL(handler.oauthPublicURL, proto, host, tls) + OAuthCallbackPath
}

func originOf(raw string) string {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return ""
	}
	return parsed.Scheme + "://" + parsed.Host
}

// OAuthCallback finishes the Google popup. It is a plain HTTP handler because
// the browser lands here without an API credential.
func (handler *Credentials) OAuthCallback() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet {
			http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
			return
		}
		query := r.URL.Query()
		if errMsg := strings.TrimSpace(query.Get("error")); errMsg != "" {
			writeOAuthResult(w, false, errMsg, "")
			return
		}
		if len(handler.oauthSigningKey) == 0 || handler.store == nil {
			writeOAuthResult(w, false, "oauth is not configured on this instance", "")
			return
		}
		state, err := credentials.ParseOAuthState(handler.oauthSigningKey, query.Get("state"))
		if err != nil {
			writeOAuthResult(w, false, err.Error(), "")
			return
		}
		redirect := handler.oauthRedirectURL(r.Header.Get("X-Forwarded-Proto"), r.Host, r.TLS != nil)

		// The state is only good in the browser that started it. A state
		// arriving without its nonce is somebody else's authorize URL, opened
		// by a person who never pressed Connect — exactly the link an attacker
		// sends to have the victim's Google account stored in the attacker's
		// credential.
		cookieName := oauthNonceCookieName(state.NonceHash)
		nonce := ""
		if cookie, err := r.Cookie(cookieName); err == nil {
			nonce = cookie.Value
		}
		if !state.MatchesNonce(nonce) {
			writeOAuthResult(w, false, "this sign-in was not started in this browser: press Connect again from the credential", state.Origin)
			return
		}
		expired := oauthNonceCookie(redirect, state.NonceHash, "", -1)
		http.SetCookie(w, &expired)

		// Single use, recorded where every replica can see it. Checked before
		// the code is exchanged, so a replayed callback URL never reaches
		// Google, let alone overwrites the credential.
		first, err := handler.oauthLedger().ConsumeOnce(r.Context(), state.TenantID,
			idempotency.OneTimeKey(oauthStateOperation, state.NonceHash), time.Unix(state.Expiry, 0))
		if err != nil {
			slog.ErrorContext(r.Context(), "recording a used Google sign-in state", slog.Any("error", err))
			writeOAuthResult(w, false, "could not record this sign-in: press Connect again", state.Origin)
			return
		}
		if !first {
			writeOAuthResult(w, false, "this sign-in was already used: press Connect again", state.Origin)
			return
		}

		code := strings.TrimSpace(query.Get("code"))
		if code == "" {
			writeOAuthResult(w, false, "google did not return an authorization code", state.Origin)
			return
		}
		tenant := repository.TenantScope{ID: state.TenantID}
		record, fields, err := handler.store.Resolve(r.Context(), tenant, state.CredentialID)
		if err != nil {
			writeOAuthResult(w, false, "credential not found", state.Origin)
			return
		}
		clientID, clientSecret, err := handler.googleClient(record.Type, fields)
		if err != nil {
			writeOAuthResult(w, false, err.Error(), state.Origin)
			return
		}
		verifier := credentials.PKCEVerifier(handler.oauthSigningKey, nonce)
		token, err := credentials.ExchangeGoogleCode(handler.oauthHTTP, handler.oauthTokenURL, clientID, clientSecret, redirect, code, verifier)
		if err != nil {
			writeOAuthResult(w, false, err.Error(), state.Origin)
			return
		}
		merged := credentials.MergeGoogleToken(fields, token)
		if _, err := handler.store.Update(r.Context(), tenant, state.CredentialID, credentials.Record{
			Name: record.Name, Type: record.Type, Fields: merged, AllowedDomains: record.AllowedDomains,
		}); err != nil {
			writeOAuthResult(w, false, "could not store the google token", state.Origin)
			return
		}
		writeOAuthResult(w, true, "", state.Origin)
	})
}

func writeOAuthResult(w http.ResponseWriter, ok bool, detail, origin string) {
	payload, _ := json.Marshal(map[string]any{
		"source": "kilasflow-oauth",
		"ok":     ok,
		"error":  detail,
	})
	script := "window.close();"
	if origin != "" {
		script = `if (window.opener) { window.opener.postMessage(payload, ` + jsonString(origin) + `); }` + script
	}
	body := `<!DOCTYPE html><html><head><meta charset="utf-8"><title>KilasFlow</title></head><body>` +
		`<p>` + html.EscapeString(oauthResultText(ok, detail)) + `</p>` +
		`<script>` +
		`const payload = ` + string(payload) + `;` +
		script +
		`</script></body></html>`
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write([]byte(body))
}

func oauthResultText(ok bool, detail string) string {
	if ok {
		return "Connected. You can close this window."
	}
	if strings.TrimSpace(detail) == "" {
		return "Google authorization failed. You can close this window."
	}
	return "Google authorization failed: " + detail
}

func jsonString(value string) string {
	encoded, err := json.Marshal(value)
	if err != nil {
		return `""`
	}
	return string(encoded)
}

// WithOAuthStateLedger sets where used Connect states are recorded. The server
// binary passes the database-backed one, because any replica may serve a
// callback; without one, states are remembered in this process only.
func (handler *Credentials) WithOAuthStateLedger(ledger OAuthStateLedger) *Credentials {
	handler.oauthStates = ledger
	return handler
}

func (handler *Credentials) oauthLedger() OAuthStateLedger {
	handler.oauthStatesOnce.Do(func() {
		if handler.oauthStates == nil {
			handler.oauthStates = &memoryOAuthLedger{}
		}
	})
	return handler.oauthStates
}
