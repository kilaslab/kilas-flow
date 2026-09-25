package credentials_test

import (
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// A token refresh posts the client secret and the refresh token, and a 307
// re-sends that body wherever it points. The request is held to the token
// endpoint's own host, so a redirect elsewhere never receives the body.
func TestATokenRefreshDoesNotFollowARedirectToAnotherHost(t *testing.T) {
	t.Parallel()

	var leaked string
	elsewhere := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		leaked = string(body)
		_, _ = w.Write([]byte(`{"access_token":"stolen"}`))
	}))
	defer elsewhere.Close()
	tokenEndpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(elsewhere.URL, "127.0.0.1", "localhost", 1)+"/token", http.StatusTemporaryRedirect)
	}))
	defer tokenEndpoint.Close()

	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	_, err := credentials.RefreshGoogleToken(safehttp.NewClient(policy), tokenEndpoint.URL+"/token", "client-id", "CLIENT-SECRET", "REFRESH-TOKEN")
	if err == nil {
		t.Error("a refresh answered only by a redirect succeeded")
	}
	if leaked != "" {
		t.Errorf("the redirect target received the refresh body: %q", leaked)
	}
}
