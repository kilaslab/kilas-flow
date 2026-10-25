package nodepack_test

import (
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
)

// A transcription of a community package records the npm identity it was
// transcribed from — the package half of FEAT-mq412g: the importer resolves
// the source type onto the converted pack through it. n8n's own nodes record
// nothing, because those map by name in the adapter's table.
func TestACommunityTranscriptionRecordsItsN8NIdentity(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name        string
		sourceType  string
		wantPackage string
		wantNode    string
	}{
		{
			name:        "a scoped community package",
			sourceType:  "@apify/n8n-nodes-apify.apify",
			wantPackage: "@apify/n8n-nodes-apify",
			wantNode:    "apify",
		},
		{
			name:        "an unscoped community package",
			sourceType:  "n8n-nodes-acme.AcmeMail",
			wantPackage: "n8n-nodes-acme",
			wantNode:    "AcmeMail",
		},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			t.Parallel()
			transcription := strings.Replace(acmeTranscription, `"n8n-nodes-acme.AcmeMail"`,
				`"`+test.sourceType+`"`, 1)
			pack, _, err := nodepack.ConvertDocument([]byte(transcription), nodepack.ConvertOptions{PackType: "pack.acmemail"})
			if err != nil {
				t.Fatalf("ConvertDocument() error = %v", err)
			}
			if pack.SourcePackage != test.wantPackage || pack.SourceNode != test.wantNode {
				t.Fatalf("pack source = %q/%q, want %q/%q",
					pack.SourcePackage, pack.SourceNode, test.wantPackage, test.wantNode)
			}
			// The definition the registry holds carries the same identity,
			// which is the half the importer reads.
			definition, _, err := nodepack.Load(pack)
			if err != nil {
				t.Fatalf("Load() error = %v", err)
			}
			if definition.N8NSource == nil ||
				definition.N8NSource.Package != test.wantPackage || definition.N8NSource.Node != test.wantNode {
				t.Fatalf("definition N8NSource = %#v, want the recorded identity", definition.N8NSource)
			}
		})
	}

	t.Run("one of n8n's own nodes records nothing", func(t *testing.T) {
		t.Parallel()
		transcription := strings.Replace(acmeTranscription, `"n8n-nodes-acme.AcmeMail"`,
			`"n8n-nodes-base.sendEmail"`, 1)
		pack, _, err := nodepack.ConvertDocument([]byte(transcription), nodepack.ConvertOptions{PackType: "pack.acmemail"})
		if err != nil {
			t.Fatalf("ConvertDocument() error = %v", err)
		}
		if pack.SourcePackage != "" || pack.SourceNode != "" {
			t.Fatalf("pack source = %q/%q, want none recorded", pack.SourcePackage, pack.SourceNode)
		}
	})
}

// A pack file that declares its source package loads with the identity on the
// definition, action node and trigger each under their own node name.
func TestAPackFileDeclaringItsSourceLoadsItOntoTheDefinition(t *testing.T) {
	t.Parallel()

	manifest := `{
	  "type": "pack.demo", "version": 1,
	  "displayName": "Demo", "category": "Web",
	  "sourcePackage": "@example/n8n-nodes-demo", "sourceNode": "demo",
	  "generator": {"tool": "test", "source": "hand"},
	  "requestDefaults": {"method": "GET", "baseURL": "https://example.test"},
	  "resources": [
	    {"name": "thing", "operations": [
	      {"name": "get", "method": "GET", "url": "/thing"}
	    ]}
	  ],
	  "parameters": []
	}`
	pack, err := nodepack.Decode([]byte(manifest))
	if err != nil {
		t.Fatalf("Decode() error = %v", err)
	}
	definition, _, err := nodepack.Load(pack)
	if err != nil {
		t.Fatalf("Load() error = %v", err)
	}
	if definition.N8NSource == nil || definition.N8NSource.Package != "@example/n8n-nodes-demo" ||
		definition.N8NSource.Node != "demo" {
		t.Fatalf("N8NSource = %#v, want the declared identity", definition.N8NSource)
	}
}
