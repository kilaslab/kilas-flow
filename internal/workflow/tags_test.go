package workflow_test

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// taggedDraft is the smallest document ValidateDraft accepts, carrying tags.
func taggedDraft(tags ...string) workflow.Document {
	return workflow.Document{
		SchemaVersion: workflow.CurrentSchemaVersion,
		ID:            "wf_019",
		Name:          "Tagged",
		Nodes:         []workflow.Node{},
		Connections:   []workflow.Connection{},
		Settings:      map[string]any{},
		Tags:          tags,
	}
}

// A workflow that carries no tags must serialise exactly as it did before the
// field existed: stored revisions are compared and restored byte for byte, and
// a "tags":null in every document would be a silent change to all of them.
func TestADocumentWithoutTagsEncodesAsBefore(t *testing.T) {
	for _, tags := range [][]string{nil, {}} {
		encoded, err := json.Marshal(taggedDraft(tags...))
		if err != nil {
			t.Fatalf("Marshal() error = %v", err)
		}
		if strings.Contains(string(encoded), "tags") {
			t.Errorf("Marshal(tags=%#v) = %s, want no tags key", tags, encoded)
		}
	}
}

func TestDecodeDocumentReadsTags(t *testing.T) {
	document, err := workflow.DecodeDocument(bytes.NewBufferString(`{
        "schemaVersion": 1,
        "id": "wf_019",
        "name": "Draft",
        "nodes": [],
        "connections": [],
        "settings": {},
        "tags": ["Prod", "billing"]
    }`))
	if err != nil {
		t.Fatalf("DecodeDocument() error = %v", err)
	}
	if got, want := strings.Join(document.Tags, "|"), "Prod|billing"; got != want {
		t.Errorf("Tags = %q, want %q (authored order and case kept)", got, want)
	}

	for name, raw := range map[string]string{
		"a string":           `"prod"`,
		"an object list":     `[{"name":"prod"}]`,
		"a number in a list": `[1]`,
	} {
		_, err := workflow.DecodeDocument(bytes.NewBufferString(`{"schemaVersion":1,"id":"wf_019","name":"Draft","nodes":[],"connections":[],"settings":{},"tags":` + raw + `}`))
		if err == nil {
			t.Errorf("DecodeDocument(tags=%s) accepted %s", raw, name)
		}
	}
}

func TestValidateDraftRefusesBadTags(t *testing.T) {
	twentyOne := make([]string, 21)
	for index := range twentyOne {
		twentyOne[index] = fmt.Sprintf("tag-%02d", index)
	}
	tests := []struct {
		name string
		tags []string
		want string
	}{
		{"twenty-one tags", twentyOne, "20"},
		{"twenty-five characters", []string{strings.Repeat("a", 25)}, "24"},
		{"an empty tag", []string{""}, "empty"},
		{"a blank tag", []string{"   "}, "whitespace"},
		{"leading space", []string{" prod"}, "whitespace"},
		{"trailing space", []string{"prod "}, "whitespace"},
		{"a comma", []string{"a,b"}, "comma"},
		{"a tab inside", []string{"a\tb"}, "control"},
		{"a newline inside", []string{"a\nb"}, "control"},
		{"a line separator", []string{"a b"}, "control"},
		{"a NUL byte", []string{"a\x00b"}, "control"},
		{"invalid UTF-8", []string{"a\xffb"}, "UTF-8"},
		{"a case-insensitive duplicate", []string{"Prod", "billing", "prod"}, "duplicate"},
		// Thirteen astral characters are 26 UTF-16 code units, and n8n measures
		// its 24-character limit in code units.
		{"thirteen emoji", []string{strings.Repeat("😀", 13)}, "24"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			err := workflow.ValidateDraft(taggedDraft(test.tags...))
			if err == nil {
				t.Fatalf("ValidateDraft() accepted %q", test.tags)
			}
			if !strings.Contains(err.Error(), test.want) {
				t.Errorf("ValidateDraft() error = %q, want it to contain %q", err, test.want)
			}
		})
	}

	// A refusal names the offending tag, so a client can fix the right one.
	err := workflow.ValidateDraft(taggedDraft("ok", "bad,tag"))
	if err == nil || !strings.Contains(err.Error(), `"bad,tag"`) {
		t.Errorf("ValidateDraft() error = %v, want it to quote the tag", err)
	}
}

func TestValidateDraftAcceptsTwentyTagsOfTwentyFourCharacters(t *testing.T) {
	tags := make([]string, 0, workflow.MaxTags)
	for index := 0; index < workflow.MaxTags; index++ {
		// Twenty-two Cyrillic letters and a two-digit counter: 24 characters
		// and 24 UTF-16 code units, each tag different.
		tags = append(tags, strings.Repeat("ж", workflow.MaxTagLength-2)+fmt.Sprintf("%02d", index))
	}
	if err := workflow.ValidateDraft(taggedDraft(tags...)); err != nil {
		t.Fatalf("ValidateDraft() error = %v, want twenty 24-character tags accepted", err)
	}
	for _, tag := range []string{"a", "Prod", "two words", "naïve", "日本語", "😀", "a-b_c.d/e:f"} {
		if err := workflow.ValidateDraft(taggedDraft(tag)); err != nil {
			t.Errorf("ValidateDraft(%q) error = %v, want it accepted", tag, err)
		}
	}
}

func TestTagKeyFoldsCase(t *testing.T) {
	if workflow.TagKey("Prod") != workflow.TagKey("pROD") {
		t.Error("TagKey() must not tell Prod from pROD")
	}
	if workflow.TagKey("Prod") == workflow.TagKey("Prods") {
		t.Error("TagKey() must tell different tags apart")
	}
}

// The published schema is hand-written, so it must keep saying what the engine
// enforces about tags: the same cap on the list and on a tag's length.
func TestCanonicalJSONSchemaDeclaresTags(t *testing.T) {
	_, testFile, _, ok := runtime.Caller(0)
	if !ok {
		t.Fatal("runtime.Caller() did not return this test file")
	}
	contents, err := os.ReadFile(filepath.Join(filepath.Dir(testFile), "..", "..", "schemas", "workflow-v1.schema.json"))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	var schema struct {
		Required   []string `json:"required"`
		Properties struct {
			Tags struct {
				Type     string `json:"type"`
				MaxItems int    `json:"maxItems"`
				Items    struct {
					Type      string `json:"type"`
					MinLength int    `json:"minLength"`
					MaxLength int    `json:"maxLength"`
				} `json:"items"`
			} `json:"tags"`
		} `json:"properties"`
	}
	if err := json.Unmarshal(contents, &schema); err != nil {
		t.Fatalf("schema JSON is invalid: %v", err)
	}
	tags := schema.Properties.Tags
	if tags.Type != "array" || tags.Items.Type != "string" {
		t.Fatalf("schema tags = %#v, want an array of strings", tags)
	}
	if tags.MaxItems != workflow.MaxTags {
		t.Errorf("schema tags maxItems = %d, want workflow.MaxTags = %d", tags.MaxItems, workflow.MaxTags)
	}
	if tags.Items.MinLength != 1 || tags.Items.MaxLength != workflow.MaxTagLength {
		t.Errorf("schema tag length = %d..%d, want 1..%d", tags.Items.MinLength, tags.Items.MaxLength, workflow.MaxTagLength)
	}
	for _, name := range schema.Required {
		if name == "tags" {
			t.Error("schema requires tags; a document without them must stay valid")
		}
	}
}
