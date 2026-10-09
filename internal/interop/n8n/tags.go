package n8n

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// How n8n files carry a workflow's tags, as far as a second implementation has
// to match it:
//
//   - A workflow's "tags" is a list of objects. What n8n writes on export is
//     {"id", "name", "createdAt", "updatedAt"} for each; the id and the
//     timestamps belong to the instance the file came from and mean nothing
//     anywhere else.
//   - A tag is identified by its name: names are unique within an instance and
//     at most 24 characters. When a file is imported, n8n matches each entry to
//     an existing tag by name and creates the ones it does not have, so an entry
//     with only a "name" imports cleanly (n8n generates the id).
//
// So the importer reads names and ignores the rest, and the exporter writes
// {"name"} objects and invents no ids.
//
// The "tags" value is decoded as raw JSON, the way loose.go reads a node's
// numbers, because n8n never checks an export against a schema: a value that is
// not a list, or an entry that is not an object, must cost that tag, not the
// whole file.

// exportedTag is one entry of an exported workflow's "tags".
type exportedTag struct {
	Name string `json:"name"`
}

// exportTags writes a document's tags in n8n's shape, in order. It returns nil
// for none, so the key is left out of the file.
func exportTags(tags []string) json.RawMessage {
	if len(tags) == 0 {
		return nil
	}
	entries := make([]exportedTag, 0, len(tags))
	for _, tag := range tags {
		entries = append(entries, exportedTag{Name: tag})
	}
	encoded, err := json.Marshal(entries)
	if err != nil {
		// A slice of strings always marshals; keep the key out rather than
		// write a half-built one if that ever stops being true.
		return nil
	}
	return encoded
}

// importTags reads the names of a workflow's tags, in file order, keeping only
// what a KilasFlow document may carry, so that saving the result can never be
// refused for its tags.
//
// Everything it does not keep is reported, and none of it refuses the file. A
// tag it could not keep at all is "dropped"; one it kept in a slightly
// different form (trimmed of surrounding whitespace) is "lossy".
func importTags(raw json.RawMessage) ([]string, []ImportIssue) {
	if absent(raw) {
		return nil, nil
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, []ImportIssue{droppedTag("n8n's tags are not a list (%s), so no tag was carried", preview(raw))}
	}

	var (
		kept   []string
		issues []ImportIssue
		first  = map[string]string{}
	)
	for index, entry := range entries {
		if len(kept) == workflow.MaxTags {
			// One line for the rest, however many there are.
			issues = append(issues, droppedTag(
				"%d more tags were not carried: KilasFlow keeps at most %d tags on a workflow, in the order the file lists them",
				len(entries)-index, workflow.MaxTags))
			break
		}

		name, issue := tagName(entry)
		if issue != nil {
			issues = append(issues, *issue)
			continue
		}
		trimmed := strings.TrimSpace(name)
		if trimmed == "" {
			issues = append(issues, droppedTag("a tag with an empty name was not carried"))
			continue
		}
		if err := workflow.CheckTag(trimmed); err != nil {
			var refused *workflow.TagError
			if errors.As(err, &refused) {
				// The reason quotes the tag shortened, never whole.
				issues = append(issues, droppedTag("the tag %s %s, so it was not carried", quoteShort(refused.Tag), refused.Problem))
			} else {
				issues = append(issues, droppedTag("the tag %s cannot be carried: %s", quoteShort(trimmed), err.Error()))
			}
			continue
		}
		key := workflow.TagKey(trimmed)
		if earlier, duplicate := first[key]; duplicate {
			issues = append(issues, droppedTag(
				"the tag %s was not carried: it repeats %s, and KilasFlow compares tags ignoring case, so it is a duplicate", quoteShort(trimmed), quoteShort(earlier)))
			continue
		}
		// Only now is the tag kept, so only now can it be said to have been
		// carried differently: a padded name that was refused above is one
		// dropped issue, not a lossy one and a dropped one.
		if trimmed != name {
			issues = append(issues, ImportIssue{
				Severity: SeverityLossy, Field: "tags",
				Reason: fmt.Sprintf("the tag %s was carried as %s: whitespace around a tag is trimmed", quoteShort(name), quoteShort(trimmed)),
			})
		}
		first[key] = trimmed
		kept = append(kept, trimmed)
	}
	return kept, issues
}

// quoteShort quotes a tag for a reason, cut to 40 characters so that a hostile
// name cannot make one report line enormous.
func quoteShort(text string) string {
	const limit = 40
	if runes := []rune(text); len(runes) > limit {
		return strconv.Quote(string(runes[:limit]) + "…")
	}
	return strconv.Quote(text)
}

// tagName reads one entry's name, or says why the entry has none to read.
func tagName(entry json.RawMessage) (string, *ImportIssue) {
	trimmed := bytes.TrimSpace(entry)
	switch {
	case len(trimmed) > 0 && trimmed[0] == '"':
		// A bare string only appears in n8n's own editor traffic, where it is
		// a tag's id, so it says nothing about the name.
		issue := droppedTag("the tag entry %s is a bare string; a file names a tag with an object's \"name\", and a string here is an instance-local id, so its name is unknown and it was not carried", preview(trimmed))
		return "", &issue
	case len(trimmed) > 0 && trimmed[0] == '{':
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(trimmed, &fields); err == nil {
			var name string
			if json.Unmarshal(fields["name"], &name) == nil && !absent(fields["name"]) {
				return name, nil
			}
		}
		issue := droppedTag("the tag entry %s has no readable \"name\" (it must be text), so it was not carried", preview(trimmed))
		return "", &issue
	}
	issue := droppedTag("the tag entry %s is not an object with a \"name\", so it was not carried", preview(trimmed))
	return "", &issue
}

func droppedTag(format string, args ...any) ImportIssue {
	return ImportIssue{Severity: SeverityDropped, Field: "tags", Reason: fmt.Sprintf(format, args...)}
}

// preview quotes a raw JSON value for a reason, shortened so a hostile entry
// cannot make the report enormous.
func preview(raw json.RawMessage) string {
	const limit = 60
	text := string(bytes.TrimSpace(raw))
	if len(text) > limit {
		cut := limit
		for cut > 0 && !isRuneStart(text[cut]) {
			cut--
		}
		text = text[:cut] + "…"
	}
	return text
}

func isRuneStart(b byte) bool { return b&0xC0 != 0x80 }
