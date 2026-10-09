package api_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// A Sheets credential saved without a scope used to fall through the
// Gmail-or-else-Drive branch and ask Google for Drive access. Connect now asks
// for the scope its own type declares: the least the node needs.
func TestConnectAsksForTheSheetsScopeWhenTheCredentialSavedNone(t *testing.T) {
	for _, test := range []struct {
		typeID string
		want   string
	}{
		{credentials.GoogleSheetsOAuthType, "https://www.googleapis.com/auth/spreadsheets https://www.googleapis.com/auth/drive.metadata.readonly"},
		{credentials.GoogleSheetsTriggerOAuthType, "https://www.googleapis.com/auth/spreadsheets.readonly https://www.googleapis.com/auth/drive.metadata.readonly"},
		{credentials.GoogleDriveOAuthType, "https://www.googleapis.com/auth/drive"},
		{credentials.GmailOAuthType, "https://www.googleapis.com/auth/gmail.modify"},
	} {
		t.Run(test.typeID, func(t *testing.T) {
			fake := newFakeGoogle(t)
			handler, _ := oauthAPI(t, "https://app.test", fake)
			created := storeCredential(t, handler, "Google", test.typeID, map[string]string{
				"clientId": "tenant-cid", "clientSecret": "tenant-csec",
			})
			start := startConnect(t, handler, fake, created.ID)
			if got := start.authorize.Query().Get("scope"); got != test.want {
				t.Errorf("scope = %q, want %q", got, test.want)
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
