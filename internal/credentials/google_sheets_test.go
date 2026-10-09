package credentials_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// The two Sheets credential types carry n8n's own type IDs byte for byte, so a
// credential reference on an imported workflow lands on the type it names.
func TestTheSheetsCredentialTypesAreN8NsOwnIDs(t *testing.T) {
	t.Parallel()

	if credentials.GoogleSheetsOAuthType != "googleSheetsOAuth2Api" {
		t.Errorf("GoogleSheetsOAuthType = %q", credentials.GoogleSheetsOAuthType)
	}
	if credentials.GoogleSheetsTriggerOAuthType != "googleSheetsTriggerOAuth2Api" {
		t.Errorf("GoogleSheetsTriggerOAuthType = %q", credentials.GoogleSheetsTriggerOAuthType)
	}
	for _, id := range []string{credentials.GoogleSheetsOAuthType, credentials.GoogleSheetsTriggerOAuthType} {
		if _, found := credentials.Lookup(id); !found {
			t.Errorf("credential type %q is not registered", id)
		}
	}
}

func TestEveryGoogleCredentialTypeIsRecognisedAsGoogleOAuth(t *testing.T) {
	t.Parallel()

	for _, id := range []string{
		credentials.GoogleDriveOAuthType, credentials.GmailOAuthType,
		credentials.GoogleSheetsOAuthType, credentials.GoogleSheetsTriggerOAuthType,
	} {
		if !credentials.IsGoogleOAuth(id) {
			t.Errorf("IsGoogleOAuth(%q) = false, so Connect and the token refresh would skip it", id)
		}
	}
	for _, id := range []string{"openAiApi", "httpBearerAuth", "telegramApi", ""} {
		if credentials.IsGoogleOAuth(id) {
			t.Errorf("IsGoogleOAuth(%q) = true", id)
		}
	}
}

// GoogleDefaultScope is what Connect asks for when the credential saved none.
// Falling back to the Drive scope for a Sheets credential asked the user to
// grant the wrong access.
func TestGoogleDefaultScopeIsThePerTypeScope(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		typeID  string
		must    []string
		mustNot []string
	}{
		{credentials.GoogleDriveOAuthType, []string{"https://www.googleapis.com/auth/drive"}, []string{"spreadsheets"}},
		{credentials.GmailOAuthType, []string{"https://www.googleapis.com/auth/gmail.modify"}, []string{"spreadsheets", "auth/drive"}},
		{credentials.GoogleSheetsOAuthType, []string{
			"https://www.googleapis.com/auth/drive.file",
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive.metadata",
		}, nil},
		{credentials.GoogleSheetsTriggerOAuthType, []string{
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive.metadata",
		}, nil},
	} {
		scope := credentials.GoogleDefaultScope(test.typeID)
		if scope == "" {
			t.Errorf("GoogleDefaultScope(%q) is empty", test.typeID)
			continue
		}
		for _, want := range test.must {
			if !containsScope(scope, want) {
				t.Errorf("GoogleDefaultScope(%q) = %q, want it to hold %q", test.typeID, scope, want)
			}
		}
		for _, bad := range test.mustNot {
			if strings.Contains(scope, bad) {
				t.Errorf("GoogleDefaultScope(%q) = %q, must not hold %q", test.typeID, scope, bad)
			}
		}
	}
	if got := credentials.GoogleDefaultScope("openAiApi"); got != "" {
		t.Errorf("GoogleDefaultScope(openAiApi) = %q, want empty for a non-Google type", got)
	}
}

func containsScope(scope, want string) bool {
	for _, entry := range strings.Fields(scope) {
		if entry == want {
			return true
		}
	}
	return false
}

func TestASheetsCredentialDefaultsToTheGoogleHosts(t *testing.T) {
	t.Parallel()

	record := credentials.Record{Type: credentials.GoogleSheetsOAuthType}
	credentials.ApplyDefaultDomains(&record)
	if !record.AllowsHost("sheets.googleapis.com") {
		t.Fatal("sheets.googleapis.com must be inside the default scope of a Sheets credential")
	}
	if record.AllowsHost("attacker.test") {
		t.Fatal("a host outside Google must stay refused")
	}
}
