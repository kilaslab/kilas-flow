package credentials_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// The Sheets types' Test button asks Drive who the signed-in user is, which
// needs a Drive scope. An operator may drop that scope on purpose (the url and
// id pickers do not need it), and Google then answers 403 to a token that is
// perfectly good: that is a valid token without the scope, not a broken
// credential. A 401 is still a failure.
func TestASheetsCredentialTestReadsAForbiddenDriveAnswerAsAValidTokenWithoutTheScope(t *testing.T) {
	t.Parallel()

	for _, typeID := range []string{credentials.GoogleSheetsOAuthType, credentials.GoogleSheetsTriggerOAuthType} {
		definition, found := credentials.Default().Get(typeID)
		if !found || definition.Test == nil || definition.Test.ForbiddenNote == "" {
			t.Fatalf("%s: Test = %#v, want a note for a 403", typeID, definition.Test)
		}
		if !strings.Contains(definition.Test.ForbiddenNote, "token is valid") || !strings.Contains(definition.Test.ForbiddenNote, "Drive") {
			t.Errorf("%s: ForbiddenNote = %q, want it to say the token is valid and the Drive scope is missing", typeID, definition.Test.ForbiddenNote)
		}

		// The same probe, aimed at a stub that answers by the token it is given.
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			switch r.Header.Get("Authorization") {
			case "Bearer good":
				_, _ = w.Write([]byte(`{"user":{}}`))
			case "Bearer narrow":
				w.WriteHeader(http.StatusForbidden)
			default:
				w.WriteHeader(http.StatusUnauthorized)
			}
		}))
		t.Cleanup(server.Close)
		probe := definition
		probe.Test = &credentials.TestRequest{URL: server.URL + "/drive/v3/about", ForbiddenNote: definition.Test.ForbiddenNote}

		run := func(token string) (string, error) {
			fields := map[string]string{"access_token": token}
			record := credentials.Record{Type: typeID, Fields: fields, AllowedDomains: []string{"127.0.0.1"}}
			return credentials.RunTest(context.Background(), probe, record, fields, endpointPolicy(t, server.URL))
		}
		if _, err := run("good"); err != nil {
			t.Errorf("%s: a token Drive accepts failed the test: %v", typeID, err)
		}
		detail, err := run("narrow")
		if err != nil || !strings.Contains(detail, "token is valid") {
			t.Errorf("%s: a 403 gave (%q, %v), want a pass that says the token is valid", typeID, detail, err)
		}
		if _, err := run("bad"); err == nil || !strings.Contains(err.Error(), "401") {
			t.Errorf("%s: a 401 gave %v, want the failure reported", typeID, err)
		}
	}

	// Only the Sheets types carry the note: a 403 from another probe is a failure.
	for _, typeID := range []string{credentials.GoogleDriveOAuthType, credentials.GmailOAuthType, "openAiApi", "telegramApi"} {
		if definition, _ := credentials.Default().Get(typeID); definition.Test != nil && definition.Test.ForbiddenNote != "" {
			t.Errorf("%s carries a note for 403", typeID)
		}
	}
}
