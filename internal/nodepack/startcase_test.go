package nodepack_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/nodepack"
)

// This one function decides whether every imported WAHA workflow matches or
// silently selects nothing, so it is tested against the shapes real documents
// contain rather than against a couple of happy cases.
func TestStartCaseReproducesLodashSemantics(t *testing.T) {
	t.Parallel()

	for input, want := range map[string]string{
		"sendText":               "Send Text",
		"send_text":              "Send Text",
		"send-text":              "Send Text",
		"send text":              "Send Text",
		"SendText":               "Send Text",
		"APIKey":                 "API Key",
		"API":                    "API",
		"getURLFor":              "Get URL For",
		"utf8":                   "Utf 8",
		"sendText2":              "Send Text 2",
		"v2Endpoint":             "V 2 Endpoint",
		"DEPRECATED_checkNumber": "DEPRECATED Check Number",
		"--leading":              "Leading",
		"trailing--":             "Trailing",
		"a.b.c":                  "A B C",
		"":                       "",
		"___":                    "",
		"already Start Case":     "Already Start Case",
		"XMLHttpRequest":         "XML Http Request",
		"getMessages":            "Get Messages",
		"sendContactVcard":       "Send Contact Vcard",
		"setReaction":            "Set Reaction",
		"chatsGetChatPicture":    "Chats Get Chat Picture",
		"9lives":                 "9 Lives",
		"lives9":                 "Lives 9",
	} {
		if got := nodepack.StartCase(input); got != want {
			t.Errorf("StartCase(%q) = %q, want %q", input, got, want)
		}
	}
}

// Real tags carry decoration. An emoji is a word to lodash, so it is stripped
// before start-casing rather than surviving into the value an imported workflow
// has to match.
func TestResourceNameStripsTagDecoration(t *testing.T) {
	t.Parallel()

	for tag, want := range map[string]string{
		"📤 Chatting":      "Chatting",
		"🖥️ Sessions":     "Sessions",
		"✅ Presence":      "Presence",
		"🆔 Profile":       "Profile",
		"🔍 Observability": "Observability",
		"Chat Management": "Chat Management",
		"api-keys":        "Api Keys",
	} {
		if got := nodepack.ResourceName(tag); got != want {
			t.Errorf("ResourceName(%q) = %q, want %q", tag, got, want)
		}
	}
}

func TestOperationNameDropsTheControllerSegment(t *testing.T) {
	t.Parallel()

	for id, want := range map[string]string{
		"ChattingController_sendText":                     "Send Text",
		"ChattingController_DEPRECATED_checkNumberStatus": "DEPRECATED Check Number Status",
		"SessionsController_list":                         "List",
		"noUnderscore":                                    "No Underscore",
	} {
		if got := nodepack.OperationName(id); got != want {
			t.Errorf("OperationName(%q) = %q, want %q", id, got, want)
		}
	}
}
