package nodes

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/packs/telegram"
	"github.com/kilaslab/kilas-flow/packs/waha"
)

// glyphVariants names the node types allowed to share a glyph with another:
// a trigger or tool that is the same service seen from the other side. Each
// entry maps the variant to the type whose glyph it borrows, so the exception
// is spelled out rather than inferred from a naming pattern.
var glyphVariants = map[string]string{
	"kilasflow.calculatorTool":         "kilasflow.calculator",
	"kilasflow.datastoreTool":          "kilasflow.datastore",
	"kilasflow.httpTool":               "kilasflow.httpRequest",
	"kilasflow.gmailTrigger":           "kilasflow.gmail",
	"kilasflow.googleDriveTrigger":     "kilasflow.googleDrive",
	"kilasflow.googleSheetsTrigger":    "kilasflow.googleSheets",
	"kilasflow.telegramTrigger":        "pack.telegram",
	"kilasflow.executeWorkflowTrigger": "kilasflow.executeWorkflow",
	"kilasflow.workflowTool":           "kilasflow.executeWorkflow",
	"pack.wahaTrigger":                 "pack.waha",
	"kilasflow.rssFeedReadTrigger":     "kilasflow.rssFeedRead",
	"kilasflow.rssFeedReadTool":        "kilasflow.rssFeedRead",
	// Tool forms (tool_form.go) draw their base node's glyph.
	"kilasflow.gmailTool":        "kilasflow.gmail",
	"kilasflow.postgresTool":     "kilasflow.postgres",
	"kilasflow.googleSheetsTool": "kilasflow.googleSheets",
	"pack.telegramTool":          "pack.telegram",
	"kilasflow.vectorStoreTool":  "kilasflow.vectorStorePGVector",
}

// TestEveryNodeTypeHasAGlyphOfItsOwn refuses two catalogue types that draw
// the same picture.
//
// Compared by the lucide component glyphs.json resolves a name to, not by the
// name: two names can alias one icon ("form" and "clipboard-list"), and two
// tiles that look the same on the canvas are the defect whatever they are
// called. Versions of one type share a glyph by design, and so do the variants
// listed in glyphVariants.
func TestEveryNodeTypeHasAGlyphOfItsOwn(t *testing.T) {
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
	if err := RegisterVectorNodes(registry, ""); err != nil {
		t.Fatalf("RegisterVectorNodes: %v", err)
	}

	icons := map[string]string{} // type -> icon name
	for _, definition := range registry.List() {
		if definition.Icon != nil {
			icons[definition.Type] = definition.Icon.Light
		}
	}
	// The packs the server registers at boot are catalogue types too.
	telegramPack, err := telegram.Pack()
	if err != nil {
		t.Fatalf("telegram pack: %v", err)
	}
	wahaPacks, err := waha.Packs()
	if err != nil {
		t.Fatalf("WAHA packs: %v", err)
	}
	for _, pack := range append([]*nodepack.Pack{telegramPack}, wahaPacks...) {
		if pack.Icon != "" {
			icons[pack.Type] = pack.Icon
		}
	}

	owner := map[string]string{} // lucide component -> first type drawing it
	for _, nodeType := range sortedKeys(icons) {
		name := icons[nodeType]
		if !strings.HasPrefix(name, "builtin:") {
			continue // served artwork is the node's own by construction
		}
		component, ok := shipped[strings.TrimPrefix(name, "builtin:")]
		if !ok {
			continue // TestEveryBuiltinIconNameIsShippedInGlyphs reports it
		}
		if base, variant := glyphVariants[nodeType]; variant {
			if icons[base] != name {
				t.Errorf("%s is listed as a variant of %s but draws %s, not %s", nodeType, base, name, icons[base])
			}
			continue
		}
		if other, taken := owner[component]; taken {
			t.Errorf("%s and %s both draw the %s glyph (%s) — give one of them a glyph of its own", other, nodeType, component, name)
			continue
		}
		owner[component] = nodeType
	}
}

func sortedKeys(values map[string]string) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

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
