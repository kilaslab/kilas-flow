package workflow

import (
	"fmt"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"
)

// The bounds on a workflow's tags.
//
// MaxTagLength is n8n's tag-name limit (its tag entity stores 24 characters and
// validates a name as 1 to 24), so an export never carries a tag n8n would
// refuse. n8n counts UTF-16 code units, the way JavaScript counts string
// length, and so does CheckTag: for the characters people type this is the
// character count, and for an emoji it is two. JSON Schema counts code points,
// so the published schema's maxLength is a bound a valid tag always meets, not
// one that every tag meeting it satisfies.
//
// MaxTags keeps a list row's chips and a tag filter's options usable. It is
// KilasFlow's own cap; n8n has none.
const (
	MaxTags      = 20
	MaxTagLength = 24
)

// TagKey is the identity of a tag: two tags with the same key are the same tag.
// Tags differ in case only by accident ("Prod" and "prod" are one label to the
// person who typed them), so the key is the lowercased text.
//
// strings.ToLower is simple case mapping, not full case folding, so "ß" and
// "SS" stay different tags. That is the right trade for a label: it is stable,
// it is what a database LOWER() agrees with for the same text, and it never
// merges two tags a person meant to keep apart.
func TagKey(tag string) string {
	return strings.ToLower(tag)
}

// CheckTag reports why one tag cannot be stored, or nil when it can.
//
// A tag is a short label: not empty, no whitespace at either end (so " prod"
// and "prod" cannot both exist), no comma (the dashboard edits tags as a
// comma-separated list) and no control character (a tag is drawn in a chip and
// written into logs and exports, where a newline or a NUL would break the
// line). Its length is at most MaxTagLength UTF-16 code units; see the
// constants above.
func CheckTag(tag string) error {
	if tag == "" {
		return &TagError{Problem: "must not be empty"}
	}
	if !utf8.ValidString(tag) {
		return &TagError{Tag: tag, Problem: "is not valid UTF-8"}
	}
	if strings.TrimSpace(tag) != tag {
		return &TagError{Tag: tag, Problem: "must not start or end with whitespace"}
	}
	for _, r := range tag {
		switch {
		case r == ',':
			return &TagError{Tag: tag, Problem: "must not contain a comma"}
		case unicode.IsControl(r) || r == '\u2028' || r == '\u2029':
			return &TagError{Tag: tag, Problem: "must not contain a control character"}
		}
	}
	if tagLength(tag) > MaxTagLength {
		return &TagError{Tag: tag, Problem: fmt.Sprintf("is longer than %d characters", MaxTagLength)}
	}
	return nil
}

// TagError is why one tag cannot be stored. It keeps the tag apart from the
// problem so that a caller reporting on someone else's data (the n8n importer)
// can quote a shortened tag instead of the whole of a hostile one.
type TagError struct {
	// Tag is the offending tag; empty for the empty tag.
	Tag string
	// Problem completes the sentence "the tag ...", for example "must not
	// contain a comma".
	Problem string
}

func (err *TagError) Error() string {
	if err.Tag == "" {
		return "a tag " + err.Problem
	}
	return fmt.Sprintf("tag %q %s", err.Tag, err.Problem)
}

// tagLength is a tag's length in UTF-16 code units.
func tagLength(tag string) int {
	return len(utf16.Encode([]rune(tag)))
}

// validateTags checks a document's whole tag list: its size, each tag, and that
// no two tags share a key. The first problem found is returned, naming the tag.
func validateTags(tags []string) error {
	if len(tags) > MaxTags {
		return fmt.Errorf("workflow tags must not exceed %d, got %d", MaxTags, len(tags))
	}
	seen := make(map[string]string, len(tags))
	for _, tag := range tags {
		if err := CheckTag(tag); err != nil {
			return fmt.Errorf("workflow tags: %w", err)
		}
		key := TagKey(tag)
		if first, duplicate := seen[key]; duplicate {
			return fmt.Errorf("workflow tags: tag %q is a duplicate of %q (tags are compared ignoring case)", tag, first)
		}
		seen[key] = tag
	}
	return nil
}
