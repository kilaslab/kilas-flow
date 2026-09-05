package nodepack

import (
	"strings"
	"unicode"
)

// StartCase reproduces lodash's `startCase`, which is what decides whether an
// imported workflow matches a generated pack.
//
// The n8n package a WAHA workflow was authored against names its `resource` and
// `operation` values with `_.startCase`, and KilasFlow matches an imported node
// against its own parameter options by literal string. One character out and
// every imported WAHA workflow selects nothing — silently, because an option
// that does not match is indistinguishable from one nobody chose. That is why
// this is a faithful reimplementation with its own table test rather than
// `strings.Title` with a comment.
//
// Words break on case boundaries, on every non-alphanumeric character, and
// between a letter run and a digit run. Each word's first character is upper
// cased and the rest is left alone, so an acronym survives:
//
//	sendText   send_text   send-text   →  Send Text
//	APIKey                             →  API Key
//	utf8                               →  Utf 8
//	DEPRECATED_checkNumber             →  DEPRECATED Check Number
func StartCase(value string) string {
	words := Words(value)
	for index, word := range words {
		words[index] = upperFirst(word)
	}
	return strings.Join(words, " ")
}

// Words splits an identifier the way lodash's `words` does.
func Words(value string) []string {
	runes := []rune(value)
	words := make([]string, 0, 8)
	for index := 0; index < len(runes); {
		char := runes[index]
		switch {
		case !isAlphanumeric(char):
			index++

		case unicode.IsDigit(char):
			// A digit run is its own word, which is why `utf8` is two words and
			// a naive title-caser gets it wrong.
			end := index
			for end < len(runes) && unicode.IsDigit(runes[end]) {
				end++
			}
			words = append(words, string(runes[index:end]))
			index = end

		case unicode.IsUpper(char):
			end := index + 1
			if end < len(runes) && unicode.IsLower(runes[end]) {
				// One capital then lower case: an ordinary capitalised word.
				for end < len(runes) && unicode.IsLower(runes[end]) {
					end++
				}
				words = append(words, string(runes[index:end]))
				index = end
				continue
			}
			// A run of capitals is one word, except that a capital followed by
			// lower case starts the next word: `APIKey` is `API` then `Key`.
			for end < len(runes) && unicode.IsUpper(runes[end]) {
				end++
			}
			if end < len(runes) && unicode.IsLower(runes[end]) && end-index > 1 {
				end--
			}
			words = append(words, string(runes[index:end]))
			index = end

		default:
			end := index
			for end < len(runes) && unicode.IsLower(runes[end]) {
				end++
			}
			if end == index {
				// A letter that is neither upper nor lower — a script with no
				// case. It is one word on its own rather than being dropped.
				end++
			}
			words = append(words, string(runes[index:end]))
			index = end
		}
	}
	return words
}

// ResourceName is the `resource` value for an OpenAPI tag.
//
// The tag is stripped of everything that is not a letter, a digit or a space
// before it is start-cased, because real tags carry decoration: WAHA's are
// `📤 Chatting` and `🖥️ Sessions`, and an emoji is a word to lodash. Stripping
// to a space rather than to nothing keeps the boundary in a tag whose words are
// separated only by punctuation; start-casing then re-splits on case anyway, so
// the two readings differ only for an all-lowercase spaced tag, where keeping
// the boundary is the answer a human would expect.
func ResourceName(tag string) string {
	cleaned := strings.Map(func(char rune) rune {
		if isAlphanumeric(char) || char == ' ' {
			return char
		}
		return ' '
	}, tag)
	return StartCase(cleaned)
}

// OperationName is the `operation` value for an OpenAPI operation id.
//
// The first `_`-separated segment is dropped: the generators that produce these
// documents name operations `ControllerName_methodName`, and the controller is
// the resource, already carried by the tag.
func OperationName(operationID string) string {
	if _, rest, found := strings.Cut(operationID, "_"); found {
		return StartCase(rest)
	}
	return StartCase(operationID)
}

func isAlphanumeric(char rune) bool {
	return unicode.IsLetter(char) || unicode.IsDigit(char)
}

func upperFirst(word string) string {
	runes := []rune(word)
	if len(runes) == 0 {
		return word
	}
	runes[0] = unicode.ToUpper(runes[0])
	return string(runes)
}
