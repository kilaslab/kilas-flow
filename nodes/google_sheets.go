package nodes

import (
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Google Sheets node pair. Operation, value and parameter names are n8n's
// own, so a workflow imported from n8n keeps meaning what it meant.
const (
	GoogleSheetsExecutorID        = "core.googleSheets"
	GoogleSheetsTriggerExecutorID = "core.googleSheetsTrigger"
	GoogleSheetsNodeType          = "kilasflow.googleSheets"
	GoogleSheetsTriggerNodeType   = "kilasflow.googleSheetsTrigger"
	// GoogleSheetsCredentialType and GoogleSheetsTriggerCredentialType are two
	// credential types because n8n has two: a workflow names one or the other,
	// and an import must land each node on the type it named.
	GoogleSheetsCredentialType        = "googleSheetsOAuth2Api"
	GoogleSheetsTriggerCredentialType = "googleSheetsTriggerOAuth2Api"

	googleSheetsAPIBase = "https://sheets.googleapis.com/v4"
)

// sheetsAPIRoot is a variable only so a test can aim it at a stub.
var sheetsAPIRoot = googleSheetsAPIBase

// The loaders behind the node's pickers. They are registered by the option
// resolver (internal/loadoptions), and these names are the contract between the
// two packages.
const (
	googleSheetsDocumentsLoader = "googleSheets.documents"
	googleSheetsSheetsLoader    = "googleSheets.sheets"
	googleSheetsColumnsLoader   = "googleSheets.columns"
)

// Sheet operations, using n8n's value strings.
const (
	SheetsOperationAppend         = "append"
	SheetsOperationAppendOrUpdate = "appendOrUpdate"
	SheetsOperationClear          = "clear"
	SheetsOperationDelete         = "delete"
	SheetsOperationRead           = "read"
	SheetsOperationUpdate         = "update"
)

// Trigger events, using n8n's value strings. The update event is spelt
// rowUpdate, not rowUpdated.
const (
	SheetsEventRowAdded  = "rowAdded"
	SheetsEventRowUpdate = "rowUpdate"
	SheetsEventAnyUpdate = "anyUpdate"
)

// sheetsShownFor is a visibility rule keyed on the operation.
func sheetsShownFor(operations ...string) []node.VisibilityCondition {
	conditions := make([]node.VisibilityCondition, 0, len(operations))
	for _, operation := range operations {
		conditions = append(conditions, node.VisibilityCondition{Key: "operation", Equals: operation})
	}
	return conditions
}

// sheetsDocumentLocator is the "which spreadsheet" picker.
func sheetsDocumentLocator(credentialType string) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "documentId", Label: "Document", Kind: node.PropertyResourceLocator, Required: true,
		Description: "Which spreadsheet this acts on.",
		Modes: []node.PropertyMode{
			{
				Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
				LoadOptions: &node.OptionsLoader{
					Source: property.LoaderInternal, Name: googleSheetsDocumentsLoader, CredentialType: credentialType,
				},
			},
			{
				Name: "url", Label: "By URL", Kind: node.PropertyString,
				Placeholder: "https://docs.google.com/spreadsheets/d/1aBc…/edit",
				Pattern:     `^https://docs\.google\.com/spreadsheets/d/[A-Za-z0-9_-]+`,
				PatternHint: "Use the link of the spreadsheet, https://docs.google.com/spreadsheets/d/…",
			},
			{
				Name: "id", Label: "By ID", Kind: node.PropertyString, Placeholder: "1aBc…",
				Pattern:     `^[A-Za-z0-9_-]+$`,
				PatternHint: "The ID is the long code between /d/ and /edit in the link",
			},
		},
	}
}

// sheetsSheetLocator is the "which tab" picker.
func sheetsSheetLocator(credentialType string) node.PropertyDefinition {
	return node.PropertyDefinition{
		Key: "sheetName", Label: "Sheet", Kind: node.PropertyResourceLocator, Required: true,
		Description: "Which tab of the spreadsheet this acts on.",
		Modes: []node.PropertyMode{
			{
				Name: "list", Label: "From list", Kind: node.PropertyOptions, Placeholder: "Choose…",
				LoadOptions: &node.OptionsLoader{
					Source: property.LoaderInternal, Name: googleSheetsSheetsLoader, CredentialType: credentialType,
					DependsOn: []string{"documentId"},
				},
			},
			{
				Name: "url", Label: "By URL", Kind: node.PropertyString,
				Placeholder: "https://docs.google.com/spreadsheets/d/1aBc…/edit#gid=0",
				Pattern:     `gid=[0-9]+`,
				PatternHint: "The link must end with #gid= and the number of the tab",
			},
			{Name: "id", Label: "By ID", Kind: node.PropertyString, Placeholder: "gid=0 or 0"},
			{Name: "name", Label: "By Name", Kind: node.PropertyString, Placeholder: "Sheet1"},
		},
	}
}

// googleSheetsNode is the action node.
func googleSheetsNode() node.Definition {
	shownFor := sheetsShownFor
	return node.Definition{
		Type:        GoogleSheetsNodeType,
		Version:     workflow.V(1),
		DisplayName: "Google Sheets",
		Description: "Reads, appends, updates, deletes and clears rows of a Google Sheets tab. Spreadsheets are addressed by ID or link; service-account sign-in and creating spreadsheets are not supported.",
		Category:    "Files",
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "builtin:sheet"},
		IconColor:   "#0f9d58",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GoogleSheetsCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "authentication", Label: "Authentication", Kind: node.PropertyOptions, Default: "oAuth2",
				Options: []node.PropertyOption{{Label: "OAuth2 (recommended)", Value: "oAuth2"}},
			},
			{
				Key: "resource", Label: "Resource", Kind: node.PropertyOptions, Default: "sheet",
				Options: []node.PropertyOption{{Label: "Sheet Within Document", Value: "sheet"}},
			},
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Default: SheetsOperationRead,
				Options: []node.PropertyOption{
					{Label: "Append Row", Value: SheetsOperationAppend},
					{Label: "Append or Update Row", Value: SheetsOperationAppendOrUpdate},
					{Label: "Clear", Value: SheetsOperationClear},
					{Label: "Delete Rows or Columns", Value: SheetsOperationDelete},
					{Label: "Get Row(s)", Value: SheetsOperationRead},
					{Label: "Update Row", Value: SheetsOperationUpdate},
				},
			},
			sheetsDocumentLocator(GoogleSheetsCredentialType),
			sheetsSheetLocator(GoogleSheetsCredentialType),
			{
				Key: "columns", Label: "Columns", Kind: node.PropertyResourceMapper,
				Mapper: &node.ResourceMapperDeclaration{
					Schema: &node.OptionsLoader{
						Source: property.LoaderInternal, Name: googleSheetsColumnsLoader,
						CredentialType: GoogleSheetsCredentialType,
						DependsOn:      []string{"documentId", "sheetName", "operation"},
					},
					SupportsAutoMap: true,
					ValuesLabel:     "Values to Send",
				},
				Description: "What goes into each column. Automatic maps incoming fields by name; manual sets each column. " +
					"Update and Append or Update need one column to match rows on; for Update, row_number addresses a row directly.",
				VisibleWhen: shownFor(SheetsOperationAppend, SheetsOperationAppendOrUpdate, SheetsOperationUpdate),
			},
			{
				Key: "filtersUI", Label: "Filters", Kind: node.PropertyFixedCollection,
				TypeOptions: &node.TypeOptions{MultipleValues: true, MultipleValueButtonText: "Add filter"},
				Description: "Rows whose column holds exactly this text. Without a filter every row is returned.",
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Filter",
					Fields: []node.PropertyDefinition{
						{Key: "lookupColumn", Label: "Column", Kind: node.PropertyString, Description: "A column name."},
						{Key: "lookupValue", Label: "Value", Kind: node.PropertyString, Description: "The column must have this value to be matched."},
					},
				}},
				VisibleWhen: shownFor(SheetsOperationRead),
			},
			{
				Key: "combineFilters", Label: "Combine Filters", Kind: node.PropertyOptions, Default: "AND",
				Options: []node.PropertyOption{
					{Label: "AND", Value: "AND"},
					{Label: "OR", Value: "OR"},
				},
				Description: "AND needs every filter to match; OR needs one.",
				VisibleWhen: shownFor(SheetsOperationRead),
			},
			{
				Key: "toDelete", Label: "Delete", Kind: node.PropertyOptions, Default: "rows",
				Options: []node.PropertyOption{
					{Label: "Rows", Value: "rows"},
					{Label: "Columns", Value: "columns"},
				},
				VisibleWhen: shownFor(SheetsOperationDelete),
			},
			{
				Key: "clear", Label: "Clear", Kind: node.PropertyOptions, Default: "wholeSheet",
				Options: []node.PropertyOption{
					{Label: "Whole Sheet", Value: "wholeSheet"},
					{Label: "Specific Rows", Value: "specificRows"},
					{Label: "Specific Columns", Value: "specificColumns"},
					{Label: "Specific Range", Value: "specificRange"},
				},
				VisibleWhen: shownFor(SheetsOperationClear),
			},
			{
				Key: "keepFirstRow", Label: "Keep First Row", Kind: node.PropertyBoolean, Default: false,
				Description: "Leave the header row in place when clearing the whole sheet.",
				DisplayOptions: node.Visibility{Show: []node.Condition{
					{Key: "operation", Values: []any{SheetsOperationClear}},
					{Key: "clear", Values: []any{"wholeSheet"}},
				}},
			},
			{
				Key: "startIndex", Label: "Start", Kind: node.PropertyString,
				Description: "For rows, the 1-based row number to start from (2 for Delete, 1 for Clear when empty). " +
					"For columns, the column letter (A when empty).",
				VisibleWhen: shownFor(SheetsOperationDelete, SheetsOperationClear),
			},
			{
				Key: "numberToDelete", Label: "Number to Delete", Kind: node.PropertyNumber, Default: 1,
				VisibleWhen: shownFor(SheetsOperationDelete),
			},
			{
				Key: "rowsToDelete", Label: "Number of Rows to Clear", Kind: node.PropertyNumber, Default: 1,
				DisplayOptions: node.Visibility{Show: []node.Condition{
					{Key: "operation", Values: []any{SheetsOperationClear}},
					{Key: "clear", Values: []any{"specificRows"}},
				}},
			},
			{
				Key: "columnsToDelete", Label: "Number of Columns to Clear", Kind: node.PropertyNumber, Default: 1,
				DisplayOptions: node.Visibility{Show: []node.Condition{
					{Key: "operation", Values: []any{SheetsOperationClear}},
					{Key: "clear", Values: []any{"specificColumns"}},
				}},
			},
			{
				Key: "range", Label: "Range", Kind: node.PropertyString, Default: "A:F",
				Description: "The range to clear, in A1 notation. A sheet name before ! is ignored: the tab chosen above is used.",
				DisplayOptions: node.Visibility{Show: []node.Condition{
					{Key: "operation", Values: []any{SheetsOperationClear}},
					{Key: "clear", Values: []any{"specificRange"}},
				}},
			},
			sheetsOptionsCollection(),
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GoogleSheetsExecutorID,
		Validate:       validateGoogleSheetsConfiguration,
	}
}

// sheetsOptionsCollection is the Options control, in n8n's stored shape: each
// group is a fixed collection under a `values` key.
func sheetsOptionsCollection() node.PropertyDefinition {
	readOnly := node.Visibility{Show: []node.Condition{{Key: "operation", Values: []any{SheetsOperationRead}}}}
	writes := node.Visibility{Show: []node.Condition{{Key: "operation", Values: []any{
		SheetsOperationAppend, SheetsOperationAppendOrUpdate, SheetsOperationUpdate,
	}}}}
	return node.PropertyDefinition{
		Key: "options", Label: "Options", Kind: node.PropertyCollection,
		VisibleWhen: sheetsShownFor(SheetsOperationRead, SheetsOperationAppend, SheetsOperationAppendOrUpdate, SheetsOperationUpdate),
		Fields: []node.PropertyDefinition{
			{
				Key: "dataLocationOnSheet", Label: "Data Location on Sheet", Kind: node.PropertyFixedCollection,
				DisplayOptions: readOnly,
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Values",
					Fields: []node.PropertyDefinition{
						{
							Key: "rangeDefinition", Label: "Range Definition", Kind: node.PropertyOptions, Default: "detectAutomatically",
							Options: []node.PropertyOption{
								{Label: "Detect Automatically", Value: "detectAutomatically"},
								{Label: "Specify Range (A1 Notation)", Value: "specifyRangeA1"},
								{Label: "Specify Range (Rows)", Value: "specifyRange"},
							},
						},
						{
							Key: "readRowsUntil", Label: "Read Rows Until", Kind: node.PropertyOptions, Default: "lastRowInSheet",
							Options: []node.PropertyOption{
								{Label: "First Empty Row", Value: "firstEmptyRow"},
								{Label: "Last Row In Sheet", Value: "lastRowInSheet"},
							},
						},
						{Key: "headerRow", Label: "Header Row", Kind: node.PropertyNumber, Default: 1,
							Description: "The row holding the column names, counted from the first row read."},
						{Key: "firstDataRow", Label: "First Data Row", Kind: node.PropertyNumber, Default: 2,
							Description: "The first row of data, counted from the first row read."},
						{Key: "range", Label: "Range", Kind: node.PropertyString,
							Description: "The table range to read, for example C4:E7."},
					},
				}},
			},
			{
				Key: "outputFormatting", Label: "Output Formatting", Kind: node.PropertyFixedCollection,
				DisplayOptions: readOnly,
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Values",
					Fields: []node.PropertyDefinition{
						{
							Key: "general", Label: "General Formatting", Kind: node.PropertyOptions, Default: "UNFORMATTED_VALUE",
							Options: []node.PropertyOption{
								{Label: "Values (unformatted)", Value: "UNFORMATTED_VALUE"},
								{Label: "Values (formatted)", Value: "FORMATTED_VALUE"},
								{Label: "Formulas", Value: "FORMULA"},
							},
						},
						{
							Key: "date", Label: "Date Formatting", Kind: node.PropertyOptions, Default: "FORMATTED_STRING",
							Options: []node.PropertyOption{
								{Label: "Formatted Text", Value: "FORMATTED_STRING"},
								{Label: "Serial Number", Value: "SERIAL_NUMBER"},
							},
						},
					},
				}},
			},
			{
				Key: "returnFirstMatch", Label: "Return only First Matching Row", Kind: node.PropertyBoolean, Default: false,
				Description:    "With filters, stop at the first matching row instead of returning every match.",
				DisplayOptions: readOnly,
			},
			{
				Key: "cellFormat", Label: "Cell Format", Kind: node.PropertyOptions, Default: "USER_ENTERED",
				Options: []node.PropertyOption{
					{Label: "Let Google Sheets format", Value: "USER_ENTERED"},
					{Label: "Let KilasFlow keep the input types", Value: "RAW"},
				},
				DisplayOptions: writes,
			},
			{
				Key: "locationDefine", Label: "Data Location on Sheet", Kind: node.PropertyFixedCollection,
				DisplayOptions: writes,
				Groups: []node.PropertyGroup{{
					Key: "values", Label: "Values",
					Fields: []node.PropertyDefinition{
						{Key: "headerRow", Label: "Header Row", Kind: node.PropertyNumber, Default: 1,
							Description: "The row holding the column names."},
						{Key: "firstDataRow", Label: "First Data Row", Kind: node.PropertyNumber, Default: 2,
							Description: "The first row of data."},
					},
				}},
			},
			{
				Key: "handlingExtraData", Label: "Handling extra fields in input", Kind: node.PropertyOptions, Default: "insertInNewColumn",
				Options: []node.PropertyOption{
					{Label: "Insert in New Column(s)", Value: "insertInNewColumn"},
					{Label: "Ignore Them", Value: "ignoreIt"},
					{Label: "Error", Value: "error"},
				},
				Description:    "What to do with fields that match no column of the sheet, when the columns are mapped automatically.",
				DisplayOptions: writes,
			},
			{
				Key: "useAppend", Label: "Minimise API Calls", Kind: node.PropertyBoolean, Default: false,
				Description: "Append through Google's append call instead of writing the rows after the last used row. " +
					"Cheaper, but rows can land misaligned if the sheet has gaps between rows or columns.",
				DisplayOptions: node.Visibility{Show: []node.Condition{{Key: "operation", Values: []any{
					SheetsOperationAppend, SheetsOperationAppendOrUpdate,
				}}}},
			},
		},
	}
}

// googleSheetsTrigger is the polling trigger.
func googleSheetsTrigger() node.Definition {
	return node.Definition{
		Type:        GoogleSheetsTriggerNodeType,
		Version:     workflow.V(1),
		DisplayName: "Google Sheets Trigger",
		Description: "Starts a workflow when a row is added or changed in a Google Sheets tab. Polls with a leased cursor and compares the sheet's values; the first poll only records what is already there.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "builtin:sheet"},
		IconColor:   "#0f9d58",
		Subtitle:    "{{ $parameter.event }}",
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: GoogleSheetsTriggerCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "authentication", Label: "Authentication", Kind: node.PropertyOptions, Default: "triggerOAuth2",
				Options: []node.PropertyOption{{Label: "OAuth2 (recommended)", Value: "triggerOAuth2"}},
			},
			sheetsDocumentLocator(GoogleSheetsTriggerCredentialType),
			sheetsSheetLocator(GoogleSheetsTriggerCredentialType),
			{
				Key: "event", Label: "Trigger On", Kind: node.PropertyOptions, Default: SheetsEventAnyUpdate,
				Options: []node.PropertyOption{
					{Label: "Row Added", Value: SheetsEventRowAdded},
					{Label: "Row Updated", Value: SheetsEventRowUpdate},
					{Label: "Row Added or Updated", Value: SheetsEventAnyUpdate},
				},
			},
			{
				Key: "includeInOutput", Label: "Include in Output", Kind: node.PropertyOptions, Default: "new",
				Options: []node.PropertyOption{
					{Label: "New Version", Value: "new"},
					{Label: "Old Version", Value: "old"},
					{Label: "Both Versions", Value: "both"},
				},
				Description: "Only the new version is produced: the old and both settings are kept when a workflow is imported, " +
					"and the trigger still emits the new version.",
				DisplayOptions: node.Visibility{Show: []node.Condition{{Key: "event", Values: []any{SheetsEventRowUpdate, SheetsEventAnyUpdate}}}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{
						Key: "columnsToWatch", Label: "Columns to Watch", Kind: node.PropertyMultiOptions,
						Description: "Only a change in one of these columns counts as an update. Leave empty to watch every column.",
					},
					{
						Key: "dataLocationOnSheet", Label: "Data Location on Sheet", Kind: node.PropertyFixedCollection,
						Groups: []node.PropertyGroup{{
							Key: "values", Label: "Values",
							Fields: []node.PropertyDefinition{
								{
									Key: "rangeDefinition", Label: "Range Definition", Kind: node.PropertyOptions, Default: "specifyRangeA1",
									Options: []node.PropertyOption{
										{Label: "Specify Range (A1 Notation)", Value: "specifyRangeA1"},
										{Label: "Specify Range (Rows)", Value: "specifyRange"},
									},
								},
								{Key: "range", Label: "Range", Kind: node.PropertyString, Description: "The table range to watch, for example A:F."},
								{Key: "headerRow", Label: "Header Row", Kind: node.PropertyNumber, Default: 1},
								{Key: "firstDataRow", Label: "First Data Row", Kind: node.PropertyNumber, Default: 2},
							},
						}},
					},
					{
						Key: "valueRender", Label: "Value Render", Kind: node.PropertyOptions, Default: "UNFORMATTED_VALUE",
						Options: []node.PropertyOption{
							{Label: "Values (unformatted)", Value: "UNFORMATTED_VALUE"},
							{Label: "Values (formatted)", Value: "FORMATTED_VALUE"},
							{Label: "Formulas", Value: "FORMULA"},
						},
					},
					{
						Key: "dateTimeRenderOption", Label: "Date Render", Kind: node.PropertyOptions, Default: "FORMATTED_STRING",
						Options: []node.PropertyOption{
							{Label: "Formatted Text", Value: "FORMATTED_STRING"},
							{Label: "Serial Number", Value: "SERIAL_NUMBER"},
						},
					},
				},
			},
			{Key: "pollTimes", Label: "Poll Times", Kind: node.PropertyCollection},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     GoogleSheetsTriggerExecutorID,
		Validate:       validateGoogleSheetsTriggerConfiguration,
	}
}

// literal reads a fixed string parameter. An expression marker reads as empty,
// because it cannot be judged before there is an item to evaluate it against.
func sheetsLiteral(parameters map[string]any, key string) string {
	value, _ := parameters[key].(string)
	return strings.TrimSpace(value)
}

func validateGoogleSheetsConfiguration(n workflow.Node) error {
	if authentication := sheetsLiteral(n.Parameters, "authentication"); authentication != "" && authentication != "oAuth2" {
		return fmt.Errorf("authentication %q is not supported: Google Sheets signs in with an OAuth2 credential, "+
			"so connect one and set Authentication to OAuth2", authentication)
	}
	if resource := sheetsLiteral(n.Parameters, "resource"); resource != "" && resource != "sheet" {
		return fmt.Errorf("resource %q is not supported: this node works on the rows of a sheet, not on spreadsheets themselves", resource)
	}
	operation := sheetsLiteral(n.Parameters, "operation")
	switch operation {
	case "", SheetsOperationRead, SheetsOperationAppend, SheetsOperationAppendOrUpdate,
		SheetsOperationUpdate, SheetsOperationDelete, SheetsOperationClear:
	default:
		if _, isExpression := n.Parameters["operation"].(map[string]any); !isExpression {
			return fmt.Errorf("operation %q is not supported: use read, append, appendOrUpdate, update, delete or clear", operation)
		}
	}
	if !property.LocatorIsSet(n.Parameters["documentId"]) {
		return fmt.Errorf("documentId is not set: choose the spreadsheet, or give its ID or link")
	}
	if !property.LocatorIsSet(n.Parameters["sheetName"]) {
		return fmt.Errorf("sheetName is not set: choose the tab, or give its name or gid")
	}
	if operation == SheetsOperationUpdate || operation == SheetsOperationAppendOrUpdate {
		mapping, _ := property.ReadMapping(n.Parameters["columns"])
		switch len(mapping.MatchingColumns) {
		case 0:
			return fmt.Errorf("%s needs a matching column to find the row: set one in Columns", operation)
		case 1:
		default:
			return fmt.Errorf("%s matches rows on exactly one matching column, and %d are set", operation, len(mapping.MatchingColumns))
		}
	}
	return nil
}

func validateGoogleSheetsTriggerConfiguration(n workflow.Node) error {
	if authentication := sheetsLiteral(n.Parameters, "authentication"); authentication != "" && authentication != "triggerOAuth2" {
		return fmt.Errorf("authentication %q is not supported: the Google Sheets Trigger signs in with an OAuth2 credential", authentication)
	}
	switch event := sheetsLiteral(n.Parameters, "event"); event {
	case "", SheetsEventRowAdded, SheetsEventRowUpdate, SheetsEventAnyUpdate:
	default:
		return fmt.Errorf("event %q is not supported: use rowAdded, rowUpdate or anyUpdate", event)
	}
	if !property.LocatorIsSet(n.Parameters["documentId"]) {
		return fmt.Errorf("documentId is not set: choose the spreadsheet, or give its ID or link")
	}
	if !property.LocatorIsSet(n.Parameters["sheetName"]) {
		return fmt.Errorf("sheetName is not set: choose the tab, or give its name or gid")
	}
	return nil
}
