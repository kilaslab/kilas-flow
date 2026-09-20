package webhook_test

import (
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/webhook"
)

// formParameters is a form trigger's own configuration, as a workflow stores
// it: n8n's field names, which is what an import writes and what the node's own
// controls save.
func formParameters() map[string]any {
	return map[string]any{
		"path":              "signup",
		"formTitle":         "Join the list",
		"formDescription":   "Tell us who you are.",
		"appendAttribution": true,
		"formFields": map[string]any{"values": []any{
			map[string]any{"fieldLabel": "Name", "fieldType": "text", "requiredField": true, "placeholder": "Ada"},
			map[string]any{"fieldLabel": "Email", "fieldType": "email"},
			map[string]any{"fieldLabel": "Plan", "fieldType": "dropdown", "fieldOptions": map[string]any{
				"values": []any{map[string]any{"option": "free"}, map[string]any{"option": "pro"}},
			}},
			map[string]any{"fieldLabel": "Notes", "fieldType": "textarea"},
			map[string]any{"fieldLabel": "Attachment", "fieldType": "file"},
			map[string]any{"fieldLabel": "", "fieldType": "text"},
		}},
	}
}

func TestFormFromParametersReadsTheDeclaration(t *testing.T) {
	t.Parallel()

	form := webhook.FormFromParameters(formParameters())
	if form.Title != "Join the list" || form.Description != "Tell us who you are." {
		t.Errorf("form = %#v, want the title and description", form)
	}
	if !form.Attribution {
		t.Error("attribution = false, want the configured footer")
	}
	labels := make([]string, 0, len(form.Fields))
	for _, field := range form.Fields {
		labels = append(labels, field.Label)
	}
	// The nameless row is dropped rather than rendered as a control nobody can
	// key a value under.
	if strings.Join(labels, ",") != "Name,Email,Plan,Notes,Attachment" {
		t.Fatalf("fields = %v, want the declared labels in order", labels)
	}
	if !form.Fields[0].Required || form.Fields[0].Placeholder != "Ada" {
		t.Errorf("first field = %#v, want required with its placeholder", form.Fields[0])
	}
	if len(form.Fields[2].Options) != 2 || form.Fields[2].Options[1] != "pro" {
		t.Errorf("dropdown options = %#v, want the declared options", form.Fields[2].Options)
	}
}

// Every value that reaches the page comes from a workflow document one tenant
// edits and another tenant's browser reads, so an unescaped label is a stored
// cross-site scripting hole.
func TestRenderedFormEscapesEverythingFromTheDocument(t *testing.T) {
	t.Parallel()

	form := webhook.Form{
		Title:       `"><script>alert(1)</script>`,
		Description: `<img src=x onerror=alert(2)>`,
		Fields: []webhook.FormField{{
			Label:       `x" onfocus="alert(3)`,
			Kind:        "dropdown",
			Placeholder: `"><script>alert(4)</script>`,
			Options:     []string{`<script>alert(5)</script>`},
		}},
	}
	page := string(webhook.RenderForm(form, ""))
	// The property is that no *tag or attribute delimiter* survives: a label
	// whose text happens to read `onerror=` is inert once its angle brackets and
	// quotes are entities, and asserting on the bare substring would fail on
	// correctly escaped markup.
	for _, injected := range []string{"<script>", "<img", `onfocus="alert`} {
		if strings.Contains(page, injected) {
			t.Errorf("rendered page contains %q as live markup:\n%s", injected, page)
		}
	}
	for _, escaped := range []string{"&lt;script&gt;", "&lt;img", "&#34;"} {
		if !strings.Contains(page, escaped) {
			t.Errorf("rendered page is missing the escaped form %q:\n%s", escaped, page)
		}
	}
}

// The page has to work with no client script and no second request: it posts to
// its own address, so a file field and an ordinary text field both arrive.
func TestRenderedFormCarriesEachFieldKindAndPostsToItself(t *testing.T) {
	t.Parallel()

	form := webhook.FormFromParameters(formParameters())
	page := string(webhook.RenderForm(form, "https://flows.example.test/webhook/abc"))

	if !strings.Contains(page, `method="post"`) || !strings.Contains(page, `enctype="multipart/form-data"`) {
		t.Error("the form does not post a multipart body to itself")
	}
	if !strings.Contains(page, `action="https://flows.example.test/webhook/abc"`) {
		t.Error("the form does not post to the address it was served from")
	}
	for _, expected := range []string{
		`type="text" id="Name" name="Name" required`,
		`type="email" id="Email" name="Email"`,
		`<select id="Plan" name="Plan"`,
		`<option value="pro">pro</option>`,
		`<textarea id="Notes" name="Notes"`,
		`type="file" id="Attachment" name="Attachment"`,
		`placeholder="Ada"`,
	} {
		if !strings.Contains(page, expected) {
			t.Errorf("rendered page is missing %q:\n%s", expected, page)
		}
	}
}

// A browser that disabled its own validation must not start a run with an empty
// required field: the workflow would then have to defend itself against a
// submission it never expected.
func TestMissingRequiredFieldsRefusesAnIncompleteSubmission(t *testing.T) {
	t.Parallel()

	form := webhook.FormFromParameters(formParameters())
	missing := webhook.MissingRequiredFields(form, map[string]any{"Email": "ada@example.test"})
	if len(missing) != 1 || missing[0] != "Name" {
		t.Fatalf("missing = %v, want the required field that was omitted", missing)
	}
	if missing := webhook.MissingRequiredFields(form, map[string]any{"Name": "Ada"}); len(missing) != 0 {
		t.Errorf("missing = %v, want none", missing)
	}
	if missing := webhook.MissingRequiredFields(form, map[string]any{"Name": "   "}); len(missing) != 1 {
		t.Errorf("missing = %v, want a whitespace-only value treated as omitted", missing)
	}
	// A submission that is not an object at all cannot satisfy a required
	// field, and must not panic on the way to saying so.
	if missing := webhook.MissingRequiredFields(form, "not an object"); len(missing) != 1 {
		t.Errorf("missing = %v, want the required field reported", missing)
	}
}

// n8n's form item carries the submitted fields plus `submittedAt` and
// `formMode`, and an expression reading them is written against those names.
func TestSubmissionFieldsStampsTheTimeAndMode(t *testing.T) {
	t.Parallel()

	at := time.Date(2026, 9, 20, 10, 30, 0, 0, time.UTC)
	fields := webhook.SubmissionFields(map[string]any{"Name": "Ada", "Plan": "pro"}, at)

	if fields["Name"] != "Ada" || fields["Plan"] != "pro" {
		t.Errorf("fields = %#v, want the submitted values", fields)
	}
	if fields["submittedAt"] != "2026-09-20T10:30:00Z" {
		t.Errorf("submittedAt = %#v, want the RFC3339 stamp", fields["submittedAt"])
	}
	if fields["formMode"] != "production" {
		t.Errorf("formMode = %#v, want production", fields["formMode"])
	}
	// A body that is not an object cannot be spread, so it is carried rather
	// than dropped.
	raw := webhook.SubmissionFields("plain text", at)
	if raw["body"] != "plain text" {
		t.Errorf("body = %#v, want the unreadable body kept", raw["body"])
	}
}
