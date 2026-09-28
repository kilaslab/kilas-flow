package nodes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
)

// TestEveryBuiltinIconNameIsShippedInGlyphs guards the editor's glyph map.
//
// The canvas draws a definition's icon by looking the name up in
// web/src/lib/workflow-editor/glyphs.json, which the frontend's GLYPHS map is
// tested against. A definition that names a builtin glyph the JSON does not
// list would render the fallback box on every canvas — "this editor is older
// than this node" — so a new icon name has to be added there in the same
// change that uses it.
func TestEveryBuiltinIconNameIsShippedInGlyphs(t *testing.T) {
	payload, err := os.ReadFile(filepath.Join("..", "web", "src", "lib", "workflow-editor", "glyphs.json"))
	if err != nil {
		t.Fatalf("read glyphs.json: %v", err)
	}
	var shipped map[string]string
	if err := json.Unmarshal(payload, &shipped); err != nil {
		t.Fatalf("parse glyphs.json: %v", err)
	}

	registry := node.NewRegistry()
	if err := RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll: %v", err)
	}

	for _, definition := range registry.List() {
		if definition.Icon == nil {
			continue
		}
		for _, name := range []string{definition.Icon.Light, definition.Icon.Dark} {
			if !strings.HasPrefix(name, "builtin:") {
				continue
			}
			glyph := strings.TrimPrefix(name, "builtin:")
			if _, ok := shipped[glyph]; !ok {
				t.Errorf("%s names builtin:%s, which glyphs.json does not list — add it (and a GLYPHS entry) or pick a shipped glyph", definition.Type, glyph)
			}
		}
	}
}
