package credentials_test

import (
	"net/url"
	"reflect"
	"slices"
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
//
// The two Sheets types ask for the least that works, because an imported
// workflow never carries a credential, so nobody is relying on n8n's broader
// list: the action node reads and writes cells (spreadsheets); the trigger only
// reads (spreadsheets.readonly); both list spreadsheets for the picker
// (drive.metadata.readonly).
func TestGoogleDefaultScopeIsThePerTypeScope(t *testing.T) {
	t.Parallel()

	for _, test := range []struct {
		typeID string
		want   []string
	}{
		{credentials.GoogleDriveOAuthType, []string{"https://www.googleapis.com/auth/drive"}},
		{credentials.GmailOAuthType, []string{"https://www.googleapis.com/auth/gmail.modify"}},
		{credentials.GoogleSheetsOAuthType, []string{
			"https://www.googleapis.com/auth/spreadsheets",
			"https://www.googleapis.com/auth/drive.metadata.readonly",
		}},
		{credentials.GoogleSheetsTriggerOAuthType, []string{
			"https://www.googleapis.com/auth/spreadsheets.readonly",
			"https://www.googleapis.com/auth/drive.metadata.readonly",
		}},
	} {
		got := strings.Fields(credentials.GoogleDefaultScope(test.typeID))
		if !reflect.DeepEqual(got, test.want) {
			t.Errorf("GoogleDefaultScope(%q) = %q, want exactly %q", test.typeID, got, test.want)
		}
	}
	if got := credentials.GoogleDefaultScope("openAiApi"); got != "" {
		t.Errorf("GoogleDefaultScope(openAiApi) = %q, want empty for a non-Google type", got)
	}
}

// Every request the Sheets nodes and their pickers make has to be answered under
// the default scope of the credential that signs it. The accepted scopes below
// are the ones Google's reference lists for each method, read on 2026-10-09.
func TestEveryCallTheSheetsNodesMakeIsCoveredByTheirDefaultScope(t *testing.T) {
	t.Parallel()

	const prefix = "https://www.googleapis.com/auth/"
	accepted := func(names ...string) []string {
		scopes := make([]string, 0, len(names))
		for _, name := range names {
			scopes = append(scopes, prefix+name)
		}
		return scopes
	}
	readCells := accepted("drive", "drive.readonly", "drive.file", "spreadsheets", "spreadsheets.readonly")
	writeCells := accepted("drive", "drive.file", "spreadsheets")
	listFiles := accepted("drive", "drive.appdata", "drive.file", "drive.metadata", "drive.metadata.readonly", "drive.photos.readonly", "drive.readonly")

	// What the action node does: read the tab list and cells, write cells, grow
	// or shrink the grid, clear.
	node := map[string][]string{
		"spreadsheets.get":                readCells,
		"spreadsheets.values.get":         readCells,
		"spreadsheets.values.update":      writeCells,
		"spreadsheets.values.append":      writeCells,
		"spreadsheets.values.batchUpdate": writeCells,
		"spreadsheets.values.clear":       writeCells,
		"spreadsheets.batchUpdate":        writeCells,
		"drive.files.list (picker)":       listFiles,
		"drive.about.get (Test button)":   listFiles,
	}
	// What the trigger does: only reads, plus the same picker and Test button.
	trigger := map[string][]string{
		"spreadsheets.get":              readCells,
		"spreadsheets.values.get":       readCells,
		"drive.files.list (picker)":     listFiles,
		"drive.about.get (Test button)": listFiles,
	}
	for typeID, calls := range map[string]map[string][]string{
		credentials.GoogleSheetsOAuthType:        node,
		credentials.GoogleSheetsTriggerOAuthType: trigger,
	} {
		granted := strings.Fields(credentials.GoogleDefaultScope(typeID))
		for call, wantOne := range calls {
			covered := false
			for _, scope := range granted {
				if slices.Contains(wantOne, scope) {
					covered = true
				}
			}
			if !covered {
				t.Errorf("%s: %s needs one of %q, and the default scope is %q", typeID, call, wantOne, granted)
			}
		}
	}

	// The Test button calls the Drive "about" endpoint, which the default scope
	// covers; the generic userinfo probe needs a profile scope these types do not
	// ask for.
	for _, typeID := range []string{credentials.GoogleSheetsOAuthType, credentials.GoogleSheetsTriggerOAuthType} {
		definition, found := credentials.Default().Get(typeID)
		if !found || definition.Test == nil {
			t.Fatalf("%s has no test probe", typeID)
		}
		parsed, err := url.Parse(definition.Test.URL)
		if err != nil || parsed.Host != "www.googleapis.com" || parsed.Path != "/drive/v3/about" {
			t.Errorf("%s probes %q, want the Drive about endpoint", typeID, definition.Test.URL)
		}
	}
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
