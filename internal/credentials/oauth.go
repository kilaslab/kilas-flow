package credentials

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/property"
)

const (
	GoogleDriveOAuthType = "googleDriveOAuth2Api"
	GmailOAuthType       = "gmailOAuth2"
	googleTokenURL       = "https://oauth2.googleapis.com/token"
	googleAuthURL        = "https://accounts.google.com/o/oauth2/v2/auth"
)

// GoogleDefaultDomains is the host allowlist every Google credential gets when
// the author saved none. A Drive or Gmail call without this fails the credential
// host check against googleapis.com.
var GoogleDefaultDomains = []string{
	"googleapis.com",
	"*.googleapis.com",
	"accounts.google.com",
	"www.google.com",
	"googleusercontent.com",
	"*.googleusercontent.com",
}

func googleOAuthType(id, display, description, scope string) Type {
	return Type{
		ID: id, DisplayName: display, Description: description,
		Properties: []property.PropertyDefinition{
			{Key: "clientId", Label: "Client ID", Kind: property.KindString,
				Description: "From your Google Cloud OAuth client. Leave empty to use the platform client (KILASFLOW_GOOGLE_CLIENT_ID)."},
			{Key: "clientSecret", Label: "Client secret", Kind: property.KindString,
				TypeOptions: &property.TypeOptions{Password: true},
				Description: "From your Google Cloud OAuth client. Leave empty to use the platform client."},
			{Key: "access_token", Label: "Access token", Kind: property.KindString,
				TypeOptions: &property.TypeOptions{Password: true},
				Description: "Filled by Connect. Do not paste tokens."},
			{Key: "refresh_token", Label: "Refresh token", Kind: property.KindString,
				TypeOptions: &property.TypeOptions{Password: true}},
			{Key: "expiry", Label: "Token expiry", Kind: property.KindString,
				Description: "RFC3339 timestamp written by Connect."},
			{Key: "scope", Label: "Scope", Kind: property.KindString, Default: scope},
		},
		Secrets:      []string{"clientSecret", "access_token", "refresh_token"},
		Authenticate: &Authentication{Placement: PlacementBearer, Value: "{{ access_token }}"},
		Test:         &TestRequest{URL: "https://www.googleapis.com/oauth2/v3/userinfo"},
	}
}

// IsGoogleOAuth reports whether a stored credential type talks to Google.
func IsGoogleOAuth(typeID string) bool {
	return typeID == GoogleDriveOAuthType || typeID == GmailOAuthType
}

// ApplyGoogleDefaults fills an empty host allowlist for Google credentials.
func ApplyGoogleDefaults(record *Record) {
	if record == nil || !IsGoogleOAuth(record.Type) {
		return
	}
	if len(record.AllowedDomains) == 0 {
		record.AllowedDomains = append([]string{}, GoogleDefaultDomains...)
	}
}

// OAuthState is the CSRF payload the Connect popup round-trips through Google.
//
// The signature proves the server minted it; it does not prove who is holding
// it. NonceHash is what does: the start response sets the nonce as an HttpOnly
// cookie in the browser that asked, and the callback accepts the state only
// from a browser presenting that nonce. Without it, a state is a bearer token —
// anyone could send their authorize URL to someone else, and the victim's
// Google tokens would be stored in the sender's credential.
//
// The state carries the nonce's hash, not the nonce, because the state travels
// in URLs (Google's, the browser history, a proxy log) and the cookie value
// must not.
type OAuthState struct {
	TenantID     string `json:"tenantId"`
	CredentialID string `json:"credentialId"`
	Origin       string `json:"origin,omitempty"`
	NonceHash    string `json:"nonce,omitempty"`
	Expiry       int64  `json:"exp"`
}

// OAuthStateTTL is how long a Connect popup has to come back.
const OAuthStateTTL = 10 * time.Minute

// NewOAuthNonce returns a fresh random nonce for one Connect popup.
func NewOAuthNonce() (string, error) {
	buf := make([]byte, 32)
	if _, err := rand.Read(buf); err != nil {
		return "", fmt.Errorf("oauth nonce: %w", err)
	}
	return base64.RawURLEncoding.EncodeToString(buf), nil
}

// OAuthNonceHash is what a state carries in place of its nonce.
func OAuthNonceHash(nonce string) string {
	sum := sha256.Sum256([]byte(nonce))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

// MatchesNonce reports whether nonce is the one this state was signed with.
func (state OAuthState) MatchesNonce(nonce string) bool {
	if nonce == "" || state.NonceHash == "" {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(OAuthNonceHash(nonce)), []byte(state.NonceHash)) == 1
}

// PKCEVerifier derives the RFC 7636 code verifier for one Connect popup.
//
// It is derived rather than stored: HMAC of the browser's nonce under the
// server key, so the callback can recompute it from the cookie it is handed,
// nothing has to be kept between start and callback, and neither the nonce
// alone nor anything in a URL is enough to compute it. The label keeps this
// use of the key apart from the state signature.
func PKCEVerifier(secret []byte, nonce string) string {
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte("kilasflow-oauth-pkce\x00" + nonce))
	return base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
}

// PKCEChallenge is the S256 transform of a verifier.
func PKCEChallenge(verifier string) string {
	sum := sha256.Sum256([]byte(verifier))
	return base64.RawURLEncoding.EncodeToString(sum[:])
}

func SignOAuthState(secret []byte, state OAuthState) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("oauth signing key is not configured")
	}
	if state.Expiry == 0 {
		state.Expiry = time.Now().Add(OAuthStateTTL).Unix()
	}
	payload, err := json.Marshal(state)
	if err != nil {
		return "", err
	}
	encoded := base64.RawURLEncoding.EncodeToString(payload)
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encoded))
	return encoded + "." + base64.RawURLEncoding.EncodeToString(mac.Sum(nil)), nil
}

func ParseOAuthState(secret []byte, raw string) (OAuthState, error) {
	encoded, sig, ok := strings.Cut(raw, ".")
	if !ok {
		return OAuthState{}, fmt.Errorf("oauth state is malformed")
	}
	mac := hmac.New(sha256.New, secret)
	_, _ = mac.Write([]byte(encoded))
	want := mac.Sum(nil)
	got, err := base64.RawURLEncoding.DecodeString(sig)
	if err != nil || !hmac.Equal(want, got) {
		return OAuthState{}, fmt.Errorf("oauth state is not valid")
	}
	payload, err := base64.RawURLEncoding.DecodeString(encoded)
	if err != nil {
		return OAuthState{}, fmt.Errorf("oauth state is malformed")
	}
	var state OAuthState
	if err := json.Unmarshal(payload, &state); err != nil {
		return OAuthState{}, fmt.Errorf("oauth state is malformed")
	}
	if time.Now().Unix() > state.Expiry {
		return OAuthState{}, fmt.Errorf("oauth state has expired")
	}
	if state.NonceHash == "" {
		// Minted before the browser binding existed, or by something that is
		// not this server's start: either way there is nothing to bind it to.
		return OAuthState{}, fmt.Errorf("oauth state is not bound to a browser: start Connect again")
	}
	return state, nil
}

// GoogleAuthorizeURL builds the consent URL. codeChallenge is the S256 PKCE
// challenge; Google then refuses to exchange the code without its verifier, so
// a code lifted from the redirect is useless to anyone but this server.
func GoogleAuthorizeURL(clientID, redirectURI, scope, state, codeChallenge string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("response_type", "code")
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	values.Set("include_granted_scopes", "true")
	values.Set("scope", scope)
	values.Set("state", state)
	values.Set("code_challenge", codeChallenge)
	values.Set("code_challenge_method", "S256")
	return googleAuthURL + "?" + values.Encode()
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

// ExchangeGoogleCode trades an authorization code for tokens. codeVerifier is
// the PKCE verifier the authorize URL's challenge was made from, and it is
// required: every code this server asks for was requested with a challenge.
func ExchangeGoogleCode(ctxClient *http.Client, tokenURL, clientID, clientSecret, redirectURI, code, codeVerifier string) (map[string]string, error) {
	if strings.TrimSpace(codeVerifier) == "" {
		return nil, fmt.Errorf("google code exchange needs the PKCE code verifier")
	}
	return postGoogleToken(ctxClient, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
		"code_verifier": {codeVerifier},
	})
}

func RefreshGoogleToken(ctxClient *http.Client, tokenURL, clientID, clientSecret, refreshToken string) (map[string]string, error) {
	return postGoogleToken(ctxClient, tokenURL, url.Values{
		"grant_type":    {"refresh_token"},
		"refresh_token": {refreshToken},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
	})
}

func TokenNeedsRefresh(fields map[string]string, now time.Time) bool {
	if strings.TrimSpace(fields["refresh_token"]) == "" {
		return false
	}
	raw := strings.TrimSpace(fields["expiry"])
	if raw == "" {
		return strings.TrimSpace(fields["access_token"]) == ""
	}
	expiry, err := time.Parse(time.RFC3339, raw)
	if err != nil {
		return true
	}
	return !expiry.After(now.Add(2 * time.Minute))
}

func MergeGoogleToken(fields, token map[string]string) map[string]string {
	merged := map[string]string{}
	for key, value := range fields {
		merged[key] = value
	}
	for key, value := range token {
		if strings.TrimSpace(value) != "" {
			merged[key] = value
		}
	}
	return merged
}

func postGoogleToken(client *http.Client, tokenURL string, values url.Values) (map[string]string, error) {
	if client == nil {
		return nil, fmt.Errorf("google token http client is not configured")
	}
	if strings.TrimSpace(tokenURL) == "" {
		tokenURL = googleTokenURL
	}
	response, err := client.PostForm(tokenURL, values)
	if err != nil {
		return nil, fmt.Errorf("google token request: %w", err)
	}
	defer response.Body.Close()
	body, err := io.ReadAll(io.LimitReader(response.Body, 1<<20))
	if err != nil {
		return nil, fmt.Errorf("google token response: %w", err)
	}
	if response.StatusCode >= 300 {
		return nil, fmt.Errorf("google token request failed with status %d: %.200s", response.StatusCode, strings.TrimSpace(string(body)))
	}
	var decoded googleTokenResponse
	if err := json.Unmarshal(body, &decoded); err != nil {
		return nil, fmt.Errorf("google token response is not JSON: %w", err)
	}
	if strings.TrimSpace(decoded.AccessToken) == "" {
		return nil, fmt.Errorf("google did not return an access token")
	}
	fields := map[string]string{
		"access_token": decoded.AccessToken,
	}
	if decoded.RefreshToken != "" {
		fields["refresh_token"] = decoded.RefreshToken
	}
	if decoded.ExpiresIn > 0 {
		fields["expiry"] = time.Now().Add(time.Duration(decoded.ExpiresIn) * time.Second).UTC().Format(time.RFC3339)
	}
	if decoded.Scope != "" {
		fields["scope"] = decoded.Scope
	}
	return fields, nil
}
