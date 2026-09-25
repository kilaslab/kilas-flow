package property_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/property"
)

func TestValidateEditorAcceptsACodeEditorOnAStringField(t *testing.T) {
	t.Parallel()

	for _, language := range []property.EditorLanguage{
		property.EditorLanguageJavaScript, property.EditorLanguageGo,
		property.EditorLanguagePython, property.EditorLanguageJSON,
	} {
		options := &property.TypeOptions{Rows: 12, Editor: property.EditorCode, EditorLanguage: language}
		if err := property.ValidateEditor(property.KindString, options); err != nil {
			t.Errorf("language %q: ValidateEditor() error = %v", language, err)
		}
	}
	if err := property.ValidateEditor(property.KindNumber, nil); err != nil {
		t.Errorf("no type options: ValidateEditor() error = %v", err)
	}
	if err := property.ValidateEditor(property.KindNumber, &property.TypeOptions{Rows: 3}); err != nil {
		t.Errorf("no editor: ValidateEditor() error = %v", err)
	}
}

func TestValidateEditorRefusesADeclarationItCannotRender(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		kind    property.Kind
		options property.TypeOptions
		want    string
	}{
		{"language without an editor", property.KindString, property.TypeOptions{EditorLanguage: property.EditorLanguageGo}, "needs editor"},
		{"unknown editor", property.KindString, property.TypeOptions{Editor: "sqlEditor", EditorLanguage: property.EditorLanguageGo}, "unknown editor"},
		{"code editor on a number", property.KindNumber, property.TypeOptions{Editor: property.EditorCode, EditorLanguage: property.EditorLanguageGo}, "only a string field"},
		{"code editor without a language", property.KindString, property.TypeOptions{Editor: property.EditorCode}, "needs an editorLanguage"},
		{"unknown language", property.KindString, property.TypeOptions{Editor: property.EditorCode, EditorLanguage: "cobol"}, "unknown editorLanguage"},
	} {
		options := tc.options
		err := property.ValidateEditor(tc.kind, &options)
		if err == nil || !strings.Contains(err.Error(), tc.want) {
			t.Errorf("%s: ValidateEditor() error = %v, want one containing %q", tc.name, err, tc.want)
		}
	}
}
