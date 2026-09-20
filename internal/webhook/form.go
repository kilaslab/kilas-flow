package webhook

import (
	"fmt"
	"html"
	"strings"
	"time"
)

// FormField is one field a hosted form declares.
//
// It is a plain struct rather than a decoded node parameter because the page
// renderer is the only reader: the node writes the declaration, the boundary
// renders it, and nothing in between needs a second interpretation of it.
type FormField struct {
	Label       string
	Kind        string
	Required    bool
	Placeholder string
	Options     []string
}

// Form is the page a form trigger serves.
type Form struct {
	Title       string
	Description string
	Fields      []FormField
	Attribution bool
	// Action is where the page submits to. Empty posts to the page's own
	// address, which is what a form served at its own URL wants.
	Action string
}

// FormFromParameters reads the page a form trigger's node parameters describe.
//
// A binding carries the trigger node's own configuration, so the page is
// rendered from exactly what the author wrote — there is no second copy of the
// form definition to drift from it. The node validates the same reader's
// result, so a form that renders is a form that validates.
func FormFromParameters(parameters map[string]any) Form {
	form := Form{
		Title:       text(parameters["formTitle"]),
		Description: text(parameters["formDescription"]),
		Attribution: true,
	}
	if attribution, ok := parameters["appendAttribution"].(bool); ok {
		form.Attribution = attribution
	}
	if strings.TrimSpace(form.Title) == "" {
		form.Title = "Form"
	}
	collection, _ := parameters["formFields"].(map[string]any)
	entries, _ := collection["values"].([]any)
	for _, entry := range entries {
		row, ok := entry.(map[string]any)
		if !ok {
			continue
		}
		label := strings.TrimSpace(text(row["fieldLabel"]))
		if label == "" {
			continue
		}
		field := FormField{
			Label:       label,
			Kind:        text(row["fieldType"]),
			Placeholder: text(row["placeholder"]),
		}
		if field.Kind == "" {
			field.Kind = "text"
		}
		if required, ok := row["requiredField"].(bool); ok {
			field.Required = required
		}
		// A dropdown's choices arrive either as the node's own
		// `{values: [{option}]}` rows or as the list of strings an import
		// writes, and both are read: the importer reduces n8n's rows to
		// strings, and the node's control stores rows.
		options, _ := row["fieldOptions"].(map[string]any)
		optionRows, _ := options["values"].([]any)
		for _, optionRow := range optionRows {
			switch typed := optionRow.(type) {
			case string:
				if option := strings.TrimSpace(typed); option != "" {
					field.Options = append(field.Options, option)
				}
			case map[string]any:
				if option := strings.TrimSpace(text(typed["option"])); option != "" {
					field.Options = append(field.Options, option)
				}
			}
		}
		form.Fields = append(form.Fields, field)
	}
	return form
}

func text(value any) string {
	typed, _ := value.(string)
	return typed
}

// RenderForm builds the hosted page.
//
// Everything that reaches the markup is escaped, including field labels, option
// values and the title: they come from a workflow document, which one tenant's
// editor writes and another tenant's browser reads, so an unescaped label is a
// stored cross-site scripting hole rather than a formatting bug.
//
// The page carries no JavaScript and posts to its own address with
// `enctype="multipart/form-data"`, so a file field works without any client
// script and the submission arrives at the same URL that rendered the page.
func RenderForm(form Form, action string) []byte {
	if strings.TrimSpace(action) == "" {
		action = ""
	}
	var builder strings.Builder
	builder.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	builder.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	builder.WriteString("<title>" + html.EscapeString(form.Title) + "</title>\n")
	builder.WriteString("<style>\n" + formStyles + "</style>\n</head>\n<body>\n<main>\n")
	builder.WriteString("<h1>" + html.EscapeString(form.Title) + "</h1>\n")
	if strings.TrimSpace(form.Description) != "" {
		builder.WriteString("<p class=\"description\">" + html.EscapeString(form.Description) + "</p>\n")
	}
	builder.WriteString("<form method=\"post\" enctype=\"multipart/form-data\" action=\"" + html.EscapeString(action) + "\">\n")
	for _, field := range form.Fields {
		builder.WriteString(renderField(field))
	}
	builder.WriteString("<button type=\"submit\">Submit</button>\n</form>\n")
	if form.Attribution {
		builder.WriteString("<p class=\"attribution\">This form was created with KilasFlow.</p>\n")
	}
	builder.WriteString("</main>\n</body>\n</html>\n")
	return []byte(builder.String())
}

// RenderFormMessage builds the page a submission is answered with.
//
// A form is filled in by a person in a browser, so an immediate acknowledgement
// is a page and not a JSON body: n8n renders a thank-you page for the same
// reason, and a caller staring at `{"message":"Workflow was started"}` has no
// way to tell whether their submission was received.
func RenderFormMessage(title, message string, attribution bool) []byte {
	var builder strings.Builder
	builder.WriteString("<!doctype html>\n<html lang=\"en\">\n<head>\n<meta charset=\"utf-8\">\n")
	builder.WriteString("<meta name=\"viewport\" content=\"width=device-width, initial-scale=1\">\n")
	builder.WriteString("<title>" + html.EscapeString(title) + "</title>\n")
	builder.WriteString("<style>\n" + formStyles + "</style>\n</head>\n<body>\n<main>\n")
	builder.WriteString("<h1>" + html.EscapeString(title) + "</h1>\n")
	if strings.TrimSpace(message) != "" {
		builder.WriteString("<p class=\"description\">" + html.EscapeString(message) + "</p>\n")
	}
	if attribution {
		builder.WriteString("<p class=\"attribution\">This form was created with KilasFlow.</p>\n")
	}
	builder.WriteString("</main>\n</body>\n</html>\n")
	return []byte(builder.String())
}

// renderField writes one control, chosen by the field's own kind.
func renderField(field FormField) string {
	name := html.EscapeString(field.Label)
	required := ""
	if field.Required {
		required = " required"
	}
	placeholder := ""
	if strings.TrimSpace(field.Placeholder) != "" {
		placeholder = " placeholder=\"" + html.EscapeString(field.Placeholder) + "\""
	}
	label := "<label for=\"" + name + "\">" + name + "</label>\n"
	switch field.Kind {
	case "textarea":
		return "<div class=\"field\">\n" + label + "<textarea id=\"" + name + "\" name=\"" + name + "\"" + required + placeholder + "></textarea>\n</div>\n"
	case "dropdown":
		var options strings.Builder
		options.WriteString("<div class=\"field\">\n" + label + "<select id=\"" + name + "\" name=\"" + name + "\"" + required + ">\n")
		options.WriteString("<option value=\"\"></option>\n")
		for _, option := range field.Options {
			options.WriteString("<option value=\"" + html.EscapeString(option) + "\">" + html.EscapeString(option) + "</option>\n")
		}
		options.WriteString("</select>\n</div>\n")
		return options.String()
	case "file":
		// A required file input cannot be satisfied by a browser that has no
		// file to attach, which is the author's intent when they mark it
		// required; the boundary refuses a submission that omits it.
		return "<div class=\"field\">\n" + label + "<input type=\"file\" id=\"" + name + "\" name=\"" + name + "\"" + required + ">\n</div>\n"
	default:
		kind := field.Kind
		switch kind {
		case "email", "number", "date", "text":
		default:
			kind = "text"
		}
		return "<div class=\"field\">\n" + label + "<input type=\"" + kind + "\" id=\"" + name + "\" name=\"" + name + "\"" + required + placeholder + ">\n</div>\n"
	}
}

// SubmissionFields adds the two keys n8n's own form trigger puts beside the
// submitted values.
//
// `submittedAt` is stamped here rather than in a node, because the boundary is
// the only place that knows when the submission arrived; `formMode` says
// whether the submission came from a live endpoint, which is the same
// distinction n8n draws between its production and test modes.
func SubmissionFields(body any, at time.Time) map[string]any {
	fields := map[string]any{}
	if decoded, ok := body.(map[string]any); ok {
		for key, value := range decoded {
			fields[key] = value
		}
	} else if body != nil {
		fields["body"] = body
	}
	fields["submittedAt"] = at.UTC().Format(time.RFC3339)
	fields["formMode"] = ExecutionModeProduction
	return fields
}

// MissingRequiredFields reports the required fields a submission omitted.
//
// It is checked at the boundary so a browser that disabled its own validation
// cannot start a run with an empty required field — the workflow would then have
// to defend itself against a submission it never expected.
func MissingRequiredFields(form Form, body any) []string {
	fields, ok := body.(map[string]any)
	if !ok {
		fields = map[string]any{}
	}
	missing := make([]string, 0, 1)
	for _, field := range form.Fields {
		if !field.Required {
			continue
		}
		value, present := fields[field.Label]
		if !present || strings.TrimSpace(fmt.Sprint(value)) == "" {
			missing = append(missing, field.Label)
		}
	}
	return missing
}

// formStyles is inline because the page has to work on a server that serves no
// other asset: a form trigger's route is the only URL it owns.
const formStyles = `:root { color-scheme: light dark; }
body { margin: 0; font-family: system-ui, -apple-system, "Segoe UI", sans-serif; background: #f6f7f9; color: #111827; }
main { max-width: 34rem; margin: 3rem auto; padding: 2rem; background: #ffffff; border-radius: 0.75rem; box-shadow: 0 1px 3px rgba(0,0,0,0.12); }
h1 { margin-top: 0; font-size: 1.5rem; }
.description { color: #4b5563; }
.field { margin-bottom: 1rem; display: flex; flex-direction: column; gap: 0.35rem; }
label { font-weight: 600; font-size: 0.9rem; }
input, textarea, select { padding: 0.55rem 0.65rem; border: 1px solid #d1d5db; border-radius: 0.5rem; font: inherit; }
textarea { min-height: 6rem; }
button { margin-top: 0.5rem; padding: 0.6rem 1.1rem; border: 0; border-radius: 0.5rem; background: #7c3aed; color: #ffffff; font: inherit; cursor: pointer; }
.attribution { margin-top: 1.5rem; color: #6b7280; font-size: 0.8rem; }
@media (prefers-color-scheme: dark) {
  body { background: #0b1020; color: #e5e7eb; }
  main { background: #151a2d; box-shadow: none; }
  .description, .attribution { color: #9ca3af; }
  input, textarea, select { background: #0b1020; color: #e5e7eb; border-color: #374151; }
}`
