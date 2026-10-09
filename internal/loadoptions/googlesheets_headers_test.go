package loadoptions_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/loadoptions"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

func columnsLoader() property.OptionsLoader {
	return property.OptionsLoader{
		Source: property.LoaderInternal, Name: loadoptions.GoogleSheetsColumnsLoader,
		CredentialType: credentials.GoogleSheetsOAuthType, DependsOn: []string{"documentId", "sheetName", "operation", "options"},
	}
}

func columnsScope(sheet, options string) loadoptions.Scope {
	dependencies := map[string]string{"documentId": "doc-1", "sheetName": sheet, "operation": "append"}
	if options != "" {
		dependencies["options"] = options
	}
	return loadoptions.Scope{TenantID: "t1", Dependencies: dependencies}
}

func lastCall(stub *sheetsStub) string {
	calls := stub.calls()
	return calls[len(calls)-1]
}

// A heading is the column's name exactly as written, trailing space included:
// the node keys a mapped value, a read row and a filter by that same text, so
// the picker must offer it unchanged.
func TestGoogleSheetsColumnsKeepAHeadingAsWritten(t *testing.T) {
	stub := newSheetsStub(t)
	stub.header = []any{"Email ", "name", 3.0, ""}
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

	schema, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope("Sheet1", ""), "cred_sheets", sheetsCredential("127.0.0.1"))
	if err != nil {
		t.Fatalf("LoadSchema() error = %v", err)
	}
	got := []string{}
	for _, field := range schema.Fields {
		got = append(got, field.ID)
		if field.DisplayName != field.ID {
			t.Errorf("field %q is displayed as %q, want the same text", field.ID, field.DisplayName)
		}
	}
	if strings.Join(got, "|") != "Email |name|3" {
		t.Fatalf("columns = %q, want the headings as written and a number as the text a cell shows", got)
	}
}

// The row the column names are on follows the node's own setting: Data Location
// on Sheet for the writing operations, and for the trigger its range or rows.
func TestGoogleSheetsColumnsReadTheHeaderRowTheNodeNames(t *testing.T) {
	for name, test := range map[string]struct {
		options string
		want    string
	}{
		"no setting":                 {"", "!1:1"},
		"an empty collection":        {"{}", "!1:1"},
		"the header row of a write":  {`{"locationDefine":{"values":{"headerRow":3}}}`, "!3:3"},
		"a header row given as text": {`{"locationDefine":{"values":{"headerRow":"4"}}}`, "!4:4"},
		"the trigger's rows":         {`{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRange","headerRow":5,"firstDataRow":6}}}`, "!5:5"},
		"the trigger's A1 range":     {`{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRangeA1","range":"Other!B7:D"}}}`, "!7:7"},
		"a range with a quoted bang": {`{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRangeA1","range":"'a!b'!C8:F"}}}`, "!8:8"},
		"detect automatically":       {`{"dataLocationOnSheet":{"values":{"rangeDefinition":"detectAutomatically"}}}`, "!1:1"},
		"an option set that is junk": {`not json`, "!1:1"},
		"a header row below one":     {`{"locationDefine":{"values":{"headerRow":0}}}`, "!1:1"},
	} {
		t.Run(name, func(t *testing.T) {
			stub := newSheetsStub(t)
			resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
			if _, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope("Sheet1", test.options), "cred_sheets", sheetsCredential("127.0.0.1")); err != nil {
				t.Fatalf("LoadSchema() error = %v", err)
			}
			if call := lastCall(stub); !strings.Contains(call, "values/'Sheet1'"+test.want+"?") {
				t.Errorf("last call = %q, want a read of 'Sheet1'%s", call, test.want)
			}
		})
	}
}

// A header row that is an expression cannot be known while editing, and falling
// back to row 1 would list the wrong columns without saying so.
func TestGoogleSheetsColumnsSayWhenTheHeaderRowIsAnExpression(t *testing.T) {
	expression := `{"mode":"expression","value":"{{ $json.row }}"}`
	for name, options := range map[string]string{
		"the header row of a write":  `{"locationDefine":{"values":{"headerRow":` + expression + `}}}`,
		"the trigger's header row":   `{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRange","headerRow":` + expression + `}}}`,
		"the trigger's A1 range":     `{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRangeA1","range":` + expression + `}}}`,
		"an expression elsewhere ok": ``,
	} {
		t.Run(name, func(t *testing.T) {
			stub := newSheetsStub(t)
			resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))

			schema, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope("Sheet1", options), "cred_sheets", sheetsCredential("127.0.0.1"))
			if err != nil {
				t.Fatalf("LoadSchema() error = %v", err)
			}
			if options == "" {
				if len(schema.Fields) == 0 {
					t.Fatalf("no options setting gave %#v, want the columns of row 1", schema)
				}
				return
			}
			if len(schema.Fields) != 0 || !strings.Contains(schema.Reason, "expression") {
				t.Fatalf("schema = %#v, want no columns and a reason that says the header row depends on an expression", schema)
			}
			if calls := stub.calls(); len(calls) != 0 {
				t.Errorf("calls = %v, want none made for a row that cannot be known", calls)
			}
		})
	}
}

// The columns mapper says which case an empty first row is, from what the
// loader returns, not from the wording of its reason.
func TestGoogleSheetsColumnsOfAnEmptyHeaderRowOfferTheAutomaticMapping(t *testing.T) {
	stub := newSheetsStub(t)
	stub.header = nil
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	schema, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope("Sheet1", ""), "cred_sheets", sheetsCredential("127.0.0.1"))
	if err != nil || len(schema.Fields) != 0 || !strings.Contains(schema.Reason, "Map the columns automatically") {
		t.Fatalf("LoadSchema(empty header) = %#v, %v, want a reason that offers the automatic mapping", schema, err)
	}

	// A refusal from Google is not an empty row, and does not offer it.
	stub.status = http.StatusForbidden
	refused, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope("Sheet1", ""), "cred_sheets", sheetsCredential("127.0.0.1"))
	if err != nil || strings.Contains(refused.Reason, "Map the columns automatically") || refused.Reason == "" {
		t.Fatalf("LoadSchema(refused) = %#v, %v, want Google's refusal and no suggestion to map automatically", refused, err)
	}
}

// The tab arrives as the locator's value alone, which for a link is the whole
// link: its #gid= names the tab.
func TestGoogleSheetsColumnsFindTheTabByTheGidInALink(t *testing.T) {
	stub := newSheetsStub(t)
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	for _, sheet := range []string{
		"https://docs.google.com/spreadsheets/d/doc-1/edit#gid=1234567890",
		"https://docs.google.com/spreadsheets/d/doc-1/edit?gid=1234567890&x=1",
		"gid=1234567890",
	} {
		schema, err := resolver.LoadSchema(context.Background(), columnsLoader(), columnsScope(sheet, ""), "cred_sheets", sheetsCredential("127.0.0.1"))
		if err != nil || schema.Reason != "" {
			t.Fatalf("LoadSchema(%q) = %#v, %v", sheet, schema, err)
		}
		if call := lastCall(stub); !strings.Contains(call, "values/'Q1 Data'!1:1") {
			t.Errorf("LoadSchema(%q) read %q, want the tab with that gid, Q1 Data", sheet, call)
		}
	}
}

// Columns to Watch lists the same headings, for the trigger's own credential.
func TestGoogleSheetsHeadingsListTheColumnNamesToWatch(t *testing.T) {
	stub := newSheetsStub(t)
	stub.header = []any{"name", "", "Email ", "name"}
	resolver := sheetsResolver(t, grantedPolicy(t, stub.server.URL))
	loader := property.OptionsLoader{
		Source: property.LoaderInternal, Name: loadoptions.GoogleSheetsHeadingsLoader,
		CredentialType: credentials.GoogleSheetsTriggerOAuthType, DependsOn: []string{"documentId", "sheetName", "options"},
	}
	trigger := sheetsCredential("127.0.0.1")
	trigger.record.Type = credentials.GoogleSheetsTriggerOAuthType
	trigger.record.Name = "Trigger"

	result, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1", Dependencies: map[string]string{
		"documentId": "doc-1", "sheetName": "Sheet1",
		"options": `{"dataLocationOnSheet":{"values":{"rangeDefinition":"specifyRange","headerRow":2}}}`,
	}}, "cred_sheets", trigger)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	got := []string{}
	for _, option := range result.Options {
		if option.Label != option.Value {
			t.Errorf("option %#v, want the label to be the value", option)
		}
		got = append(got, option.Value)
	}
	if strings.Join(got, "|") != "name|Email " {
		t.Errorf("options = %q, want each heading once, as written, with the empty one left out", got)
	}
	if call := lastCall(stub); !strings.Contains(call, "values/'Sheet1'!2:2?") {
		t.Errorf("last call = %q, want the header row the trigger names", call)
	}

	none, err := resolver.Load(context.Background(), loader, loadoptions.Scope{TenantID: "t1", Dependencies: map[string]string{}}, "cred_sheets", trigger)
	if err != nil || len(none.Options) != 0 || none.Reason == "" {
		t.Errorf("Load(no document) = %#v, %v, want a reason asking for the document and tab", none, err)
	}
}

// The same bound a run has: the credential is never carried to a host its own
// allowed hosts do not name, even by a redirect from one they do.
func TestGoogleSheetsLoadersDoNotFollowARedirectOffTheCredentialsHosts(t *testing.T) {
	var leaked http.Header
	outsider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		leaked = r.Header.Clone()
		_, _ = w.Write([]byte(`{"files":[{"id":"stolen","name":"Stolen"}]}`))
	}))
	defer outsider.Close()
	insider := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, strings.Replace(outsider.URL, "127.0.0.1", "localhost", 1)+"/drive/v3/files", http.StatusFound)
	}))
	defer insider.Close()
	t.Cleanup(loadoptions.SetGoogleSheetsBasesForTest(insider.URL+"/drive/v3", ""))

	// Both endpoints are granted by the egress policy, so only the credential's
	// own scope can stop the hop: it names 127.0.0.1, and the redirect goes to
	// the same socket under another name.
	policy := safehttp.DefaultPolicy()
	insiderURL, _ := url.Parse(insider.URL)
	outsiderURL, _ := url.Parse(outsider.URL)
	policy.AllowedPrivateEndpoints = []string{insiderURL.Host, "localhost:" + outsiderURL.Port()}
	policy.Timeout = 5 * time.Second
	resolver := sheetsResolver(t, policy)

	result, err := resolver.Load(context.Background(),
		internalLoader(loadoptions.GoogleSheetsDocumentsLoader, credentials.GoogleSheetsOAuthType),
		loadoptions.Scope{TenantID: "t1"}, "cred_sheets", sheetsCredential("127.0.0.1"))
	if leaked != nil {
		t.Fatalf("the loader delivered the credential to a host outside its domains: %v (result %#v, err %v)", leaked, result, err)
	}
	if err == nil && len(result.Options) != 0 {
		t.Errorf("the loader answered %#v from the host the redirect named", result)
	}
}
