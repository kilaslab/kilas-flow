package loadoptions

import (
	"context"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// The three loaders the Google Sheets node's pickers are built from. The node
// names them as data, and this package decides what they mean.
const (
	// GoogleSheetsDocumentsLoader lists the spreadsheets the credential can see.
	GoogleSheetsDocumentsLoader = "googleSheets.documents"
	// GoogleSheetsSheetsLoader lists the tabs of one spreadsheet, valued by gid.
	GoogleSheetsSheetsLoader = "googleSheets.sheets"
	// GoogleSheetsColumnsLoader is the schema loader behind the columns mapper.
	GoogleSheetsColumnsLoader = "googleSheets.columns"
)

// The dependency keys these loaders read.
const (
	googleSheetsDocumentDependency  = "documentId"
	googleSheetsSheetDependency     = "sheetName"
	googleSheetsOperationDependency = "operation"
)

// The API roots, variables only so a test can aim them at a stub.
var (
	googleDriveBase  = "https://www.googleapis.com/drive/v3"
	googleSheetsBase = "https://sheets.googleapis.com/v4"
)

var (
	spreadsheetLink = regexp.MustCompile(`/spreadsheets/d/([A-Za-z0-9_-]+)`)
	spreadsheetID   = regexp.MustCompile(`^[A-Za-z0-9_-]+$`)
)

// RegisterGoogleSheets installs the Sheets pickers' loaders.
//
// They read Google on behalf of whoever holds the editor, outside a run, so each
// call carries the checks a run's request would: the egress policy, the
// credential's allowed hosts, the redirect scope and the credential's own
// authentication. An internal loader is given none of that for free.
func RegisterGoogleSheets(resolver *Resolver) error {
	if resolver == nil {
		return fmt.Errorf("an options resolver is required")
	}
	for name, loader := range map[string]InternalLoader{
		GoogleSheetsDocumentsLoader: googleSheetsDocuments(resolver.policy),
		GoogleSheetsSheetsLoader:    googleSheetsSheets(resolver.policy),
	} {
		if err := resolver.RegisterInternal(name, loader); err != nil {
			return err
		}
	}
	return resolver.RegisterSchema(GoogleSheetsColumnsLoader, googleSheetsColumns(resolver.policy))
}

// googleGet reads one Google API URL as the scope's credential. It answers the
// status as well as the body, because a refusal for a missing scope is an
// answer the picker explains, not a failure.
func googleGet(ctx context.Context, policy safehttp.Policy, scope Scope, rawURL string, query url.Values) ([]byte, int, error) {
	if scope.Credential == nil {
		return nil, 0, fmt.Errorf("this field needs a Google Sheets credential before it can be filled in")
	}
	record := scope.Credential.Record
	if !credentials.IsGoogleOAuth(record.Type) {
		return nil, 0, fmt.Errorf("%q is not a Google credential", record.Name)
	}
	target, err := url.Parse(rawURL)
	if err != nil {
		return nil, 0, fmt.Errorf("this field's request is not a valid URL")
	}
	if query != nil {
		target.RawQuery = query.Encode()
	}
	if err := policy.CheckURL(target); err != nil {
		return nil, 0, err
	}
	if !record.AllowsHost(target.Hostname()) {
		return nil, 0, fmt.Errorf("request target is not allowed: this credential cannot reach that host")
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return nil, 0, fmt.Errorf("this field's request could not be built")
	}
	// The scope has to survive a redirect: without it a 30x from Google's host
	// would carry the credential to a host its allowed domains never named.
	request = request.WithContext(safehttp.WithCredentialScope(request.Context(), record.RedirectScope()))
	credentialType, known := credentials.Default().Get(record.Type)
	if !known {
		return nil, 0, fmt.Errorf("this credential's type is not registered on this server")
	}
	if err := credentials.ApplyAuthentication(request, credentialType, scope.Credential.Fields); err != nil {
		return nil, 0, err
	}
	response, err := safehttp.NewClient(policy).Do(request)
	if err != nil {
		return nil, 0, safehttp.RedactError(err)
	}
	defer response.Body.Close()
	body, _, err := policy.ReadBody(response.Body)
	if err != nil {
		return nil, 0, fmt.Errorf("Google's answer could not be read")
	}
	return body, response.StatusCode, nil
}

// refusedForScope is the status Google answers a token that lacks a scope with.
func refusedForScope(status int) bool {
	return status == http.StatusUnauthorized || status == http.StatusForbidden
}

func googleSheetsDocuments(policy safehttp.Policy) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		query := url.Values{}
		query.Set("q", "mimeType='application/vnd.google-apps.spreadsheet' and trashed=false")
		query.Set("fields", "nextPageToken,files(id,name)")
		query.Set("orderBy", "modifiedTime desc")
		query.Set("pageSize", "100")
		body, status, err := googleGet(ctx, policy, scope, googleDriveBase+"/files", query)
		if err != nil {
			return Result{}, err
		}
		if refusedForScope(status) {
			return Result{Options: []Option{}, Reason: "Google did not let this credential list spreadsheets. " +
				"Connect it again with the Drive metadata scope, or choose By URL or By ID."}, nil
		}
		if status >= 400 {
			return Result{}, fmt.Errorf("Google answered %d", status)
		}
		var payload struct {
			NextPageToken string `json:"nextPageToken"`
			Files         []struct {
				ID   string `json:"id"`
				Name string `json:"name"`
			} `json:"files"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return Result{}, fmt.Errorf("Google's list of spreadsheets was not JSON")
		}
		options := make([]Option, 0, len(payload.Files))
		for _, file := range payload.Files {
			options = append(options, Option{Label: file.Name, Value: file.ID})
		}
		result := Result{Options: options}
		if payload.NextPageToken != "" {
			result.Reason = "Showing the 100 most recently changed spreadsheets. For another one, choose By URL or By ID."
		}
		return result, nil
	}
}

// spreadsheetIDOf reads the spreadsheet a document dependency names. The value
// arrives as the locator's value alone, so it is an ID or a link; the mode that
// said which is not sent.
func spreadsheetIDOf(value string) (string, error) {
	value = strings.TrimSpace(value)
	if value == "" {
		return "", nil
	}
	if match := spreadsheetLink.FindStringSubmatch(value); match != nil {
		return match[1], nil
	}
	if !spreadsheetID.MatchString(value) {
		return "", fmt.Errorf("that document is neither a spreadsheet ID nor a spreadsheet link")
	}
	return value, nil
}

type spreadsheetTab struct {
	gid   int64
	title string
}

// spreadsheetTabs reads a spreadsheet's tab list. ok is false, with a reason
// the picker can show, when Google would not say.
func spreadsheetTabs(ctx context.Context, policy safehttp.Policy, scope Scope, documentID string) (tabs []spreadsheetTab, reason string, err error) {
	query := url.Values{"fields": []string{"sheets.properties(sheetId,title)"}}
	body, status, err := googleGet(ctx, policy, scope, googleSheetsBase+"/spreadsheets/"+url.PathEscape(documentID), query)
	if err != nil {
		return nil, "", err
	}
	switch {
	case status == http.StatusNotFound:
		return nil, "Google has no spreadsheet with that ID, or this credential cannot see it.", nil
	case refusedForScope(status):
		return nil, "Google did not let this credential read that spreadsheet.", nil
	case status >= 400:
		return nil, "", fmt.Errorf("Google answered %d", status)
	}
	var payload struct {
		Sheets []struct {
			Properties struct {
				SheetID int64  `json:"sheetId"`
				Title   string `json:"title"`
			} `json:"properties"`
		} `json:"sheets"`
	}
	if err := json.Unmarshal(body, &payload); err != nil {
		return nil, "", fmt.Errorf("Google's list of tabs was not JSON")
	}
	for _, sheet := range payload.Sheets {
		tabs = append(tabs, spreadsheetTab{gid: sheet.Properties.SheetID, title: sheet.Properties.Title})
	}
	return tabs, "", nil
}

func googleSheetsSheets(policy safehttp.Policy) InternalLoader {
	return func(ctx context.Context, scope Scope) (Result, error) {
		documentID, err := spreadsheetIDOf(scope.Dependencies[googleSheetsDocumentDependency])
		if err != nil {
			return Result{}, err
		}
		if documentID == "" {
			return Result{Options: []Option{}, Reason: "choose a document first"}, nil
		}
		tabs, reason, err := spreadsheetTabs(ctx, policy, scope, documentID)
		if err != nil {
			return Result{}, err
		}
		options := make([]Option, 0, len(tabs))
		for _, tab := range tabs {
			options = append(options, Option{Label: tab.title, Value: strconv.FormatInt(tab.gid, 10)})
		}
		return Result{Options: options, Reason: reason}, nil
	}
}

// tabNamed finds the tab a sheet dependency names. The dependency is the
// locator's value alone: a gid from the list mode, which reaches here as text
// and, for a large gid, in exponent form after a float64 round trip; or a
// title. A number that is no tab's gid is tried as a title.
func tabNamed(tabs []spreadsheetTab, value string) (spreadsheetTab, bool) {
	value = strings.TrimSpace(strings.TrimPrefix(strings.TrimSpace(value), "gid="))
	if number, err := strconv.ParseFloat(value, 64); err == nil && number == math.Trunc(number) && number >= 0 && number < 1<<53 {
		for _, tab := range tabs {
			if tab.gid == int64(number) {
				return tab, true
			}
		}
	}
	for _, tab := range tabs {
		if tab.title == value {
			return tab, true
		}
	}
	return spreadsheetTab{}, false
}

// googleSheetsColumns answers the columns mapper: the header row becomes the
// column list, and Update also offers the row number as a column to match on.
func googleSheetsColumns(policy safehttp.Policy) SchemaLoader {
	return func(ctx context.Context, scope Scope) (property.MapperSchema, error) {
		documentID, err := spreadsheetIDOf(scope.Dependencies[googleSheetsDocumentDependency])
		if err != nil {
			return property.MapperSchema{}, err
		}
		sheet := strings.TrimSpace(scope.Dependencies[googleSheetsSheetDependency])
		if documentID == "" || sheet == "" {
			return property.MapperSchema{Reason: "choose a document and a sheet first"}, nil
		}
		tabs, reason, err := spreadsheetTabs(ctx, policy, scope, documentID)
		if err != nil {
			return property.MapperSchema{}, err
		}
		if reason != "" {
			return property.MapperSchema{Reason: reason}, nil
		}
		tab, found := tabNamed(tabs, sheet)
		if !found {
			return property.MapperSchema{Reason: "that sheet is not in the document"}, nil
		}

		query := url.Values{"valueRenderOption": []string{"FORMATTED_VALUE"}}
		rangeA1 := "'" + strings.ReplaceAll(tab.title, "'", "''") + "'!1:1"
		body, status, err := googleGet(ctx, policy, scope,
			googleSheetsBase+"/spreadsheets/"+url.PathEscape(documentID)+"/values/"+url.PathEscape(rangeA1), query)
		if err != nil {
			return property.MapperSchema{}, err
		}
		if refusedForScope(status) {
			return property.MapperSchema{Reason: "Google did not let this credential read that sheet."}, nil
		}
		if status >= 400 {
			return property.MapperSchema{}, fmt.Errorf("Google answered %d", status)
		}
		var payload struct {
			Values [][]any `json:"values"`
		}
		if err := json.Unmarshal(body, &payload); err != nil {
			return property.MapperSchema{}, fmt.Errorf("Google's header row was not JSON")
		}

		schema := property.MapperSchema{}
		seen := map[string]bool{}
		if len(payload.Values) > 0 {
			for _, cell := range payload.Values[0] {
				name := strings.TrimSpace(fmt.Sprint(cell))
				if cell == nil || name == "" || seen[name] {
					continue
				}
				seen[name] = true
				schema.Fields = append(schema.Fields, property.MapperField{
					ID: name, DisplayName: name, Type: "string", CanBeUsedToMatch: true,
				})
			}
		}
		if len(schema.Fields) == 0 {
			schema.Reason = "the first row of this sheet is empty, so there are no column names yet. " +
				"Map the columns automatically and the first rows written will create them."
		}
		if strings.TrimSpace(scope.Dependencies[googleSheetsOperationDependency]) == "update" && !seen["row_number"] {
			schema.Fields = append(schema.Fields, property.MapperField{
				ID: "row_number", DisplayName: "row_number", Type: "number", ReadOnly: true, CanBeUsedToMatch: true,
			})
		}
		return schema, nil
	}
}
