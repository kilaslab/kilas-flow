package cli

import (
	"encoding/json"
	"errors"
	"net"
	"strings"
	"syscall"
	"testing"
)

// decodeEnvelope reads the one JSON envelope a non-TTY invocation prints.
func decodeEnvelope(t *testing.T, stdout string) Envelope {
	t.Helper()
	var envelope Envelope
	if err := json.Unmarshal([]byte(stdout), &envelope); err != nil {
		t.Fatalf("stdout is not one envelope: %v\n%s", err, stdout)
	}

	return envelope
}

// A noun on its own, or with a help word, lists that noun's verbs and only
// those, as an envelope off a terminal and as the help table on one.
func TestANounListsItsOwnVerbs(t *testing.T) {
	for _, args := range [][]string{{"workflow"}, {"workflow", "--help"}, {"workflow", "-h"}, {"workflow", "help"}} {
		t.Run(strings.Join(args, " "), func(t *testing.T) {
			code, handled, stdout, _ := runCLI(t, Env{Args: args})
			if !handled || code != ExitOK {
				t.Fatalf("code = %d, handled = %v, want 0 and handled", code, handled)
			}
			envelope := decodeEnvelope(t, stdout)
			rows, ok := envelope.Data.([]any)
			if !envelope.OK || !ok || len(rows) == 0 {
				t.Fatalf("envelope = %+v, want ok with the noun's verbs", envelope)
			}
			for _, row := range rows {
				path, _ := row.(map[string]any)["path"].(string)
				if !strings.HasPrefix(path, "workflow ") {
					t.Errorf("the workflow help lists %q, which is not a workflow verb", path)
				}
			}
		})
	}

	_, _, stdout, _ := runCLI(t, Env{Args: []string{"workflow"}, TTY: true})
	if !strings.Contains(stdout, "kilasflow workflow list") || strings.Contains(stdout, "kilasflow credential") {
		t.Fatalf("the terminal help = %q, want the workflow verbs only", stdout)
	}
}

// A noun followed by a verb it does not have points at the noun's own help.
func TestAnUnknownVerbOfAKnownNounNamesTheNounsHelp(t *testing.T) {
	code, _, stdout, _ := runCLI(t, Env{Args: []string{"workflow", "frob"}})
	if code != ExitUsage {
		t.Fatalf("code = %d, want the usage exit %d", code, ExitUsage)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || !strings.Contains(envelope.Error.Message, "kilasflow workflow --help") {
		t.Fatalf("envelope = %+v, want a usage error naming `kilasflow workflow --help`", envelope)
	}
}

// An unknown flag in JSON mode is a usage envelope on stdout, not an empty
// stdout and a usage text on stderr.
func TestAnUnknownFlagIsAUsageEnvelopeInJSONMode(t *testing.T) {
	code, _, stdout, _ := runCLI(t, Env{Args: []string{"workflow", "list", "--bogus"}})
	if code != ExitUsage {
		t.Fatalf("code = %d, want the usage exit %d", code, ExitUsage)
	}
	envelope := decodeEnvelope(t, stdout)
	if envelope.OK || envelope.Error == nil || envelope.Error.Code != "usage" {
		t.Fatalf("envelope = %+v, want a usage error", envelope)
	}
	if !strings.Contains(envelope.Error.Message, "-bogus") || !strings.Contains(envelope.Error.Message, "workflow list --help") {
		t.Fatalf("message = %q, want the flag and the verb's help named", envelope.Error.Message)
	}

	// On a terminal the flag package's own text stays the answer.
	code, _, stdout, stderr := runCLI(t, Env{Args: []string{"workflow", "list", "--bogus"}, TTY: true})
	if code != ExitUsage || stdout != "" || !strings.Contains(stderr, "usage: kilasflow workflow list") {
		t.Fatalf("terminal: code = %d, stdout = %q, stderr = %q", code, stdout, stderr)
	}
}

// The usage line names a verb's positional arguments the way its own usage
// errors do.
func TestTheUsageLineNamesPositionalArguments(t *testing.T) {
	_, _, _, stderr := runCLI(t, Env{Args: []string{"workflow", "get", "-h"}})
	if !strings.Contains(stderr, "usage: kilasflow workflow get <workflow id> [flags]") {
		t.Fatalf("stderr = %q, want the named-argument usage line", stderr)
	}
	if got := usageLine(Verb{Path: "api", Args: []Arg{optionalArg("operation id")}}); got != "kilasflow api [<operation id>] [flags]" {
		t.Fatalf("usageLine(optional) = %q", got)
	}
}

// The two transport failures a caller can fix from the command line name the
// fix; any other keeps the transport's own text.
func TestNetworkErrorsNameTheNextStep(t *testing.T) {
	refused := networkError("dial tcp 127.0.0.1:8080: connect: connection refused",
		&net.OpError{Op: "dial", Err: syscall.ECONNREFUSED})
	if !strings.Contains(refused.Message, "kilasflow serve") || !strings.Contains(refused.Message, envURLVar) {
		t.Errorf("refused message = %q, want it to name `kilasflow serve` and %s", refused.Message, envURLVar)
	}

	unresolved := networkError("lookup nowhere.invalid: no such host", &net.DNSError{Name: "nowhere.invalid", Err: "no such host"})
	if !strings.Contains(unresolved.Message, "does not resolve") {
		t.Errorf("DNS message = %q, want it to say the host does not resolve", unresolved.Message)
	}

	other := networkError("tls: handshake failure", errors.New("tls: handshake failure"))
	if other.Message != "tls: handshake failure" {
		t.Errorf("other message = %q, want the transport text unchanged", other.Message)
	}
}
