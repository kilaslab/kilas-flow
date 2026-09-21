package api_test

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/api/handlers"
	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/repository"
)

func TestGoogleConnectPopupExchangesACodeAndNeverListsTheToken(t *testing.T) {
	tokenServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := r.ParseForm(); err != nil {
			t.Errorf("ParseForm: %v", err)
		}
		if r.Form.Get("code") != "auth-code" || r.Form.Get("grant_type") != "authorization_code" {
			t.Errorf("token form = %v", r.Form)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"access_token":"ya29.secret","refresh_token":"1//refresh","expires_in":3600,"token_type":"Bearer"}`))
	}))
	t.Cleanup(tokenServer.Close)

	cfg := config.Default()
	cfg.Server.PublicURL = "https://app.test"
	cfg.Google.ClientID = "platform-client"
	signingKey := make([]byte, 32)
	for index := range signingKey {
		signingKey[index] = byte(index + 3)
	}
	handler, store := credentialAPI(t, api.Deps{
		Config:          cfg,
		OAuthSigningKey: signingKey,
		OAuthTokenURL:   tokenServer.URL,
		OAuthHTTP:       tokenServer.Client(),
	})

	created := storeCredential(t, handler, "Drive", credentials.GoogleDriveOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
	})
	if len(created.AllowedDomains) == 0 {
		t.Fatal("Google credentials must default to googleapis hosts")
	}

	start := httptest.NewRequest(http.MethodPost, "/api/v1/credentials/"+created.ID+"/oauth/start", nil)
	start.Header.Set("Origin", "https://editor.example")
	startRecorder := httptest.NewRecorder()
	handler.ServeHTTP(startRecorder, start)
	if startRecorder.Code != http.StatusOK {
		t.Fatalf("oauth start status = %d, want 200 (body: %s)", startRecorder.Code, startRecorder.Body)
	}
	var started struct {
		AuthorizeURL string `json:"authorizeUrl"`
	}
	if err := jsonDecode(t, startRecorder, &started); err != nil {
		t.Fatal(err)
	}
	parsed, err := url.Parse(started.AuthorizeURL)
	if err != nil {
		t.Fatalf("authorize URL: %v", err)
	}
	if parsed.Query().Get("client_id") != "tenant-cid" {
		t.Fatalf("client_id = %q", parsed.Query().Get("client_id"))
	}
	if parsed.Query().Get("access_type") != "offline" {
		t.Fatal("Connect must request an offline refresh token")
	}
	if got := parsed.Query().Get("redirect_uri"); got != "https://app.test"+handlers.OAuthCallbackPath {
		t.Fatalf("redirect_uri = %q", got)
	}

	callback := httptest.NewRequest(http.MethodGet, handlers.OAuthCallbackPath+"?code=auth-code&state="+url.QueryEscape(parsed.Query().Get("state")), nil)
	callbackRecorder := httptest.NewRecorder()
	handler.ServeHTTP(callbackRecorder, callback)
	if callbackRecorder.Code != http.StatusOK {
		t.Fatalf("oauth callback status = %d, want 200 (body: %s)", callbackRecorder.Code, callbackRecorder.Body)
	}
	body := callbackRecorder.Body.String()
	if !strings.Contains(body, `"source":"kilasflow-oauth"`) || !strings.Contains(body, `"ok":true`) {
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

	_, fields, err := store.Resolve(start.Context(), repository.TenantScope{ID: repository.DefaultTenantID}, created.ID)
	if err != nil {
		t.Fatalf("Resolve() error = %v", err)
	}
	if fields["access_token"] != "ya29.secret" || fields["refresh_token"] != "1//refresh" {
		t.Fatalf("stored fields = %#v", fields)
	}
}

func jsonDecode(t *testing.T, recorder *httptest.ResponseRecorder, dest any) error {
	t.Helper()
	return json.NewDecoder(recorder.Body).Decode(dest)
}
