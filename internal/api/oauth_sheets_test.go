package api_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// A Sheets credential saved without a scope used to fall through the
// Gmail-or-else-Drive branch and ask Google for Drive access. Connect now
// asks for the scope its own type declares.
func TestConnectAsksForTheSheetsScopeWhenTheCredentialSavedNone(t *testing.T) {
	for _, test := range []struct {
		typeID string
		want   string
		not    string
	}{
		{credentials.GoogleSheetsOAuthType, "https://www.googleapis.com/auth/spreadsheets", "https://www.googleapis.com/auth/drive "},
		{credentials.GoogleSheetsTriggerOAuthType, "https://www.googleapis.com/auth/spreadsheets", ""},
		{credentials.GoogleDriveOAuthType, "https://www.googleapis.com/auth/drive", "spreadsheets"},
		{credentials.GmailOAuthType, "https://www.googleapis.com/auth/gmail.modify", "spreadsheets"},
	} {
		t.Run(test.typeID, func(t *testing.T) {
			fake := newFakeGoogle(t)
			handler, _ := oauthAPI(t, "https://app.test", fake)
			created := storeCredential(t, handler, "Google", test.typeID, map[string]string{
				"clientId": "tenant-cid", "clientSecret": "tenant-csec",
			})
			start := startConnect(t, handler, fake, created.ID)
			scope := start.authorize.Query().Get("scope")
			if !strings.Contains(scope, test.want) {
				t.Errorf("scope = %q, want it to hold %q", scope, test.want)
			}
			if test.not != "" && strings.Contains(scope, test.not) {
				t.Errorf("scope = %q, must not hold %q", scope, test.not)
			}
		})
	}
}

// A scope the credential saved still wins over the type's default.
func TestConnectKeepsAScopeTheCredentialSaved(t *testing.T) {
	fake := newFakeGoogle(t)
	handler, _ := oauthAPI(t, "https://app.test", fake)
	created := storeCredential(t, handler, "Sheets", credentials.GoogleSheetsOAuthType, map[string]string{
		"clientId": "tenant-cid", "clientSecret": "tenant-csec",
		"scope": "https://www.googleapis.com/auth/spreadsheets.readonly",
	})
	start := startConnect(t, handler, fake, created.ID)
	if got := start.authorize.Query().Get("scope"); got != "https://www.googleapis.com/auth/spreadsheets.readonly" {
		t.Errorf("scope = %q, want the saved read-only scope", got)
	}
}
