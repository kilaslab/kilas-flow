package credentials

import (
	"crypto/hmac"
	"crypto/sha256"
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
// host check against googleapis.com. It is both Google types' DefaultDomains,
// the mechanism every fixed-host type now shares.
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
		Secrets:        []string{"clientSecret", "access_token", "refresh_token"},
		Authenticate:   &Authentication{Placement: PlacementBearer, Value: "{{ access_token }}"},
		Test:           &TestRequest{URL: "https://www.googleapis.com/oauth2/v3/userinfo"},
		DefaultDomains: GoogleDefaultDomains,
	}
}

// IsGoogleOAuth reports whether a stored credential type talks to Google.
func IsGoogleOAuth(typeID string) bool {
	return typeID == GoogleDriveOAuthType || typeID == GmailOAuthType
}

// OAuthState is the CSRF payload the Connect popup round-trips through Google.
type OAuthState struct {
	TenantID     string `json:"tenantId"`
	CredentialID string `json:"credentialId"`
	Origin       string `json:"origin,omitempty"`
	Expiry       int64  `json:"exp"`
}

func SignOAuthState(secret []byte, state OAuthState) (string, error) {
	if len(secret) == 0 {
		return "", fmt.Errorf("oauth signing key is not configured")
	}
	if state.Expiry == 0 {
		state.Expiry = time.Now().Add(10 * time.Minute).Unix()
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
	return state, nil
}

func GoogleAuthorizeURL(clientID, redirectURI, scope, state string) string {
	values := url.Values{}
	values.Set("client_id", clientID)
	values.Set("redirect_uri", redirectURI)
	values.Set("response_type", "code")
	values.Set("access_type", "offline")
	values.Set("prompt", "consent")
	values.Set("include_granted_scopes", "true")
	values.Set("scope", scope)
	values.Set("state", state)
	return googleAuthURL + "?" + values.Encode()
}

type googleTokenResponse struct {
	AccessToken  string `json:"access_token"`
	RefreshToken string `json:"refresh_token"`
	ExpiresIn    int    `json:"expires_in"`
	TokenType    string `json:"token_type"`
	Scope        string `json:"scope"`
}

func ExchangeGoogleCode(ctxClient *http.Client, tokenURL, clientID, clientSecret, redirectURI, code string) (map[string]string, error) {
	return postGoogleToken(ctxClient, tokenURL, url.Values{
		"grant_type":    {"authorization_code"},
		"code":          {code},
		"client_id":     {clientID},
		"client_secret": {clientSecret},
		"redirect_uri":  {redirectURI},
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
