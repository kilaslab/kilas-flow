package cli

import (
	"strings"
	"testing"
)

// BUG-b4cb1c: the --file flag is shared by every verb that sends a document,
// and its help named a workflow document on `credential create` too. Each verb
// now names the document it sends.
func TestDocumentFlagHelpNamesTheVerbsOwnDocument(t *testing.T) {
	for _, test := range []struct {
		args []string
		want string
	}{
		{[]string{"credential", "create", "-h"}, "credential document to send"},
		{[]string{"credential", "update", "-h"}, "credential document to send"},
		{[]string{"workflow", "create", "-h"}, "workflow document to send"},
	} {
		code, handled, stdout, stderr := runCLI(t, Env{Args: test.args, TTY: true})
		if !handled || code != ExitOK {
			t.Fatalf("%v exit = %d handled = %v (stdout=%q)", test.args, code, handled, stdout)
		}
		if !strings.Contains(stderr, test.want) {
			t.Errorf("%v help does not say %q: %q", test.args, test.want, stderr)
		}
		if test.args[0] == "credential" && strings.Contains(stderr, "workflow document") {
			t.Errorf("%v help still names a workflow document: %q", test.args, stderr)
		}
	}
}
