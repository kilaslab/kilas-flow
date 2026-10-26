package n8n_test

// FEAT-re138f: the file & format utility family imports. Every node carries
// n8n's own parameter keys, so the translators are thin and these tests pin
// the three things that could drift: the target types, the refusals for the
// operations this server's nodes do not run, and the two legacy nodes —
// Spreadsheet File and Move Binary Data — landing on the new nodes with the
// right shape for each direction.

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/interop/n8n"
)

// tableImportCases covers every mapped node of the family whose parameters
// cross unchanged or nearly so, so a mapping that starts refusing or
// rewriting shows here first.
func TestTheFileFormatFamilyImports(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name         string
		nodeType     string
		typeVersion  float64
		parameters   string
		wantType     string
		wantContains map[string]any
	}{
		{
			name:        "convert to file writes a csv",
			nodeType:    "n8n-nodes-base.convertToFile",
			typeVersion: 1.1,
			parameters:  `{"operation":"csv","mode":"all","options":{"fileName":"rows.csv","headerRow":true}}`,
			wantType:    n8n.ConvertToFileNodeType,
			wantContains: map[string]any{
				"operation": "csv", "mode": "all",
			},
		},
		{
			name:        "convert to file defaults to csv",
			nodeType:    "n8n-nodes-base.convertToFile",
			typeVersion: 1,
			parameters:  `{}`,
			wantType:    n8n.ConvertToFileNodeType,
			wantContains: map[string]any{
				"operation": "csv",
			},
		},
		{
			name:        "compression carries its format",
			nodeType:    "n8n-nodes-base.compression",
			typeVersion: 1.1,
			parameters:  `{"operation":"decompress","compressionFormat":"zip","binaryPropertyName":"file"}`,
			wantType:    n8n.CompressionNodeType,
			wantContains: map[string]any{
				"operation": "decompress", "compressionFormat": "zip", "binaryPropertyName": "file",
			},
		},
		{
			name:        "html extracts by selector",
			nodeType:    "n8n-nodes-base.html",
			typeVersion: 1.1,
			parameters:  `{"operation":"htmlExtractContents","sourceType":"json","html":"<p>hi</p>","cssSelector":"p","returnValue":"text"}`,
			wantType:    n8n.HTMLNodeType,
			wantContains: map[string]any{
				"operation": "htmlExtractContents", "cssSelector": "p", "returnValue": "text",
			},
		},
		{
			name:        "markdown converts to html",
			nodeType:    "n8n-nodes-base.markdown",
			typeVersion: 1,
			parameters:  `{"operation":"markdownToHtml","markdown":"# Title","options":{"breaks":true}}`,
			wantType:    n8n.MarkdownNodeType,
			wantContains: map[string]any{
				"operation": "markdownToHtml",
			},
		},
		{
			name:        "xml converts to json",
			nodeType:    "n8n-nodes-base.xml",
			typeVersion: 1,
			parameters:  `{"mode":"xmlToJson","xml":"<a>1</a>","options":{"explicitArray":true}}`,
			wantType:    n8n.XMLNodeType,
			wantContains: map[string]any{
				"mode": "xmlToJson",
			},
		},
		{
			name:        "crypto hashes",
			nodeType:    "n8n-nodes-base.crypto",
			typeVersion: 1.1,
			parameters:  `{"operation":"hash","type":"SHA256","inputFieldName":"data","outputFieldName":"digest","digestType":"hex"}`,
			wantType:    n8n.CryptoNodeType,
			wantContains: map[string]any{
				"operation": "hash", "type": "SHA256",
			},
		},
		{
			name:        "edit image resizes",
			nodeType:    "n8n-nodes-base.editImage",
			typeVersion: 1,
			parameters:  `{"operation":"resize","width":100,"height":50,"options":{"resizeMode":"keepAspect","format":"jpeg","quality":90}}`,
			wantType:    n8n.EditImageNodeType,
			wantContains: map[string]any{
				"operation": "resize", "resizeMode": "keepAspect",
			},
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result := importFixture(t, legacyNodeFixture(test.nodeType, test.typeVersion, test.parameters))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != test.wantType {
				t.Fatalf("imported as %q, want %q", node.Type, test.wantType)
			}
			for key, want := range test.wantContains {
				if node.Parameters[key] != want {
					t.Errorf("parameter %q = %#v, want %#v", key, node.Parameters[key], want)
				}
			}
			if blocking := blockingFor(result, "Legacy"); len(blocking) != 0 {
				t.Errorf("blocking issues = %#v, want none", blocking)
			}
		})
	}
}

// The edit-image resize mode rides under options in n8n and at the top level
// here; the translator lifts it so the imported node's form shows it.
func TestEditImageLiftsTheResizeModeOutOfOptions(t *testing.T) {
	t.Parallel()

	result := importFixture(t, legacyNodeFixture("n8n-nodes-base.editImage", 1,
		`{"operation":"resize","width":640,"options":{"resizeMode":"exact"}}`))
	node := nodeByName(result.Document, "Legacy")
	if node.Parameters["resizeMode"] != "exact" {
		t.Fatalf("resizeMode = %#v, want exact lifted to the top level", node.Parameters["resizeMode"])
	}
	if options, _ := node.Parameters["options"].(map[string]any); options != nil {
		if _, present := options["resizeMode"]; present {
			t.Errorf("options = %#v, want the resize mode lifted out", options)
		}
	}
}

// The operations this server's nodes do not run are refused into the
// placeholder, each naming the work to rebuild around.
func TestTheFileFormatFamilyRefusesOperationsItDoesNotRun(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name       string
		nodeType   string
		params     string
		wantNeedle string
	}{
		{
			name:       "convert to file ical",
			nodeType:   "n8n-nodes-base.convertToFile",
			params:     `{"operation":"iCal"}`,
			wantNeedle: "rebuild the step around",
		},
		{
			name:       "crypto verify",
			nodeType:   "n8n-nodes-base.crypto",
			params:     `{"operation":"verify"}`,
			wantNeedle: "It hashes, computes an HMAC",
		},
		{
			name:       "edit image composite",
			nodeType:   "n8n-nodes-base.editImage",
			params:     `{"operation":"composite"}`,
			wantNeedle: "It resizes, crops and adds text",
		},
		{
			name:       "edit image webp",
			nodeType:   "n8n-nodes-base.editImage",
			params:     `{"operation":"resize","options":{"format":"webp"}}`,
			wantNeedle: "PNG and JPEG only",
		},
		{
			name:       "spreadsheet file ods",
			nodeType:   "n8n-nodes-base.spreadsheetFile",
			params:     `{"operation":"read","fileType":"ods"}`,
			wantNeedle: "no reader or writer",
		},
		{
			name:       "move binary data json path",
			nodeType:   "n8n-nodes-base.moveBinaryData",
			params:     `{"operation":"moveToBinaryField","mode":"jsonPath"}`,
			wantNeedle: "base64 string into binary",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()

			result := importFixture(t, legacyNodeFixture(test.nodeType, 1, test.params))
			node := nodeByName(result.Document, "Legacy")
			if node.Type != n8n.UnsupportedNodeType {
				t.Fatalf("imported as %q, want the unsupported placeholder", node.Type)
			}
			blocking := blockingFor(result, "Legacy")
			if len(blocking) != 1 || !strings.Contains(blocking[0].Reason, test.wantNeedle) {
				t.Fatalf("blocking issues = %#v, want the reason naming %q", blocking, test.wantNeedle)
			}
		})
	}
}

// The legacy Spreadsheet File node lands on Extract From File for a read and
// Convert to File for a write, with the file type becoming the operation.
func TestTheLegacySpreadsheetFileMapsOntoTheFamilyNodes(t *testing.T) {
	t.Parallel()

	read := importFixture(t, legacyNodeFixture("n8n-nodes-base.spreadsheetFile", 1,
		`{"operation":"read","fileType":"xlsx","binaryPropertyName":"sheet"}`))
	readNode := nodeByName(read.Document, "Legacy")
	if readNode.Type != n8n.ExtractFromFileNodeType {
		t.Fatalf("read imported as %q, want %q", readNode.Type, n8n.ExtractFromFileNodeType)
	}
	if readNode.Parameters["operation"] != "xlsx" {
		t.Errorf("read operation = %#v, want xlsx", readNode.Parameters["operation"])
	}
	if readNode.Parameters["binaryPropertyName"] != "sheet" {
		t.Errorf("binaryPropertyName = %#v, want it carried", readNode.Parameters["binaryPropertyName"])
	}

	write := importFixture(t, legacyNodeFixture("n8n-nodes-base.spreadsheetFile", 1,
		`{"operation":"write","fileType":"csv","fileName":"rows.csv","options":{"headerRow":false}}`))
	writeNode := nodeByName(write.Document, "Legacy")
	if writeNode.Type != n8n.ConvertToFileNodeType {
		t.Fatalf("write imported as %q, want %q", writeNode.Type, n8n.ConvertToFileNodeType)
	}
	if writeNode.Parameters["operation"] != "csv" {
		t.Errorf("write operation = %#v, want csv", writeNode.Parameters["operation"])
	}
	options, _ := writeNode.Parameters["options"].(map[string]any)
	if options["fileName"] != "rows.csv" || options["headerRow"] != false {
		t.Errorf("options = %#v, want the file name and header row carried", options)
	}
}

// The legacy Move Binary Data node lands on Convert to File's base64
// operation in one direction and Extract From File's binaryToProperty in the
// other, with the direction's loss named rather than hidden.
func TestTheLegacyMoveBinaryDataMapsOntoTheFamilyNodes(t *testing.T) {
	t.Parallel()

	toBinary := importFixture(t, legacyNodeFixture("n8n-nodes-base.moveBinaryData", 1,
		`{"operation":"moveToBinaryField","mode":"base64","sourceProperty":"payload","targetProperty":"file"}`))
	toBinaryNode := nodeByName(toBinary.Document, "Legacy")
	if toBinaryNode.Type != n8n.ConvertToFileNodeType {
		t.Fatalf("moveToBinaryField imported as %q, want %q", toBinaryNode.Type, n8n.ConvertToFileNodeType)
	}
	if toBinaryNode.Parameters["operation"] != "base64" {
		t.Errorf("operation = %#v, want base64", toBinaryNode.Parameters["operation"])
	}
	if toBinaryNode.Parameters["propertyName"] != "payload" {
		t.Errorf("propertyName = %#v, want the source property carried", toBinaryNode.Parameters["propertyName"])
	}
	if toBinaryNode.Parameters["binaryPropertyName"] != "file" {
		t.Errorf("binaryPropertyName = %#v, want the target property carried", toBinaryNode.Parameters["binaryPropertyName"])
	}

	toProperty := importFixture(t, legacyNodeFixture("n8n-nodes-base.moveBinaryData", 1,
		`{"operation":"moveToBase64","encoding":"base64","sourceProperty":"file","targetProperty":"payload"}`))
	toPropertyNode := nodeByName(toProperty.Document, "Legacy")
	if toPropertyNode.Type != n8n.ExtractFromFileNodeType {
		t.Fatalf("moveToBase64 imported as %q, want %q", toPropertyNode.Type, n8n.ExtractFromFileNodeType)
	}
	if toPropertyNode.Parameters["operation"] != "binaryToProperty" {
		t.Errorf("operation = %#v, want binaryToProperty", toPropertyNode.Parameters["operation"])
	}
	if !hasReason(toProperty.Unsupported, "dataBase64") {
		t.Errorf("issues = %#v, want the base64 shape difference named", toProperty.Unsupported)
	}
}
