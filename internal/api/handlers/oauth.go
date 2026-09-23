package handlers

import (
	"context"
	"encoding/json"
	"fmt"
	"html"
	"net/http"
	"net/url"
	"strings"

	"github.com/danielgtaylor/huma/v2"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/embed"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

// OAuthCallbackPath is the public Google redirect URI. It is mounted on the
// mux, not under the API prefix, so a browser popup can finish Connect without
// an API key. Google blocks OAuth inside iframes; the editor opens this in
// window.open and the callback posts a message to the opener.
const OAuthCallbackPath = "/oauth/callback"

type oauthStartInput struct {
	ID      string `path:"id" minLength:"1" doc:"Credential identifier"`
	Origin  string `header:"Origin"`
	Referer string `header:"Referer"`
	Host    string `header:"Host"`
	Proto   string `header:"X-Forwarded-Proto"`
}

type oauthStartOutput struct {
	Body oauthStartResource
}

type oauthStartResource struct {
	AuthorizeURL string `json:"authorizeUrl" doc:"Google authorization URL. Open it with window.open, never in an iframe."`
}

func (handler *Credentials) registerOAuth(api huma.API) {
	huma.Register(api, huma.Operation{
		OperationID: "start-credential-oauth", Method: http.MethodPost, Path: "/credentials/{id}/oauth/start",
		Summary:     "Start Google OAuth",
		Description: "Returns the Google authorization URL for this credential. Open it in a popup (window.open), not an iframe: Google blocks OAuth inside frames.",
		Tags:        []string{"Credentials"},
	}, handler.StartOAuth)
}

// StartOAuth mints a CSRF state and returns the Google authorize URL.
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
	state, err := credentials.SignOAuthState(handler.oauthSigningKey, credentials.OAuthState{
		TenantID: tenant.ID, CredentialID: record.ID, Origin: origin,
	})
	if err != nil {
		return nil, huma.Error503ServiceUnavailable(err.Error())
	}
	return &oauthStartOutput{Body: oauthStartResource{
		AuthorizeURL: credentials.GoogleAuthorizeURL(clientID, redirect, scope, state),
	}}, nil
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
		redirect := handler.oauthRedirectURL(r.Header.Get("X-Forwarded-Proto"), r.Host, r.TLS != nil)
		token, err := credentials.ExchangeGoogleCode(handler.oauthHTTP, handler.oauthTokenURL, clientID, clientSecret, redirect, code)
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
