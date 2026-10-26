package credentials_test

// FEAT-sz4ddp: the mail credential types exist for the email nodes, and a
// credential that cannot be tested is one nobody can configure with
// confidence. Their probe is a dial test — SMTP and IMAP have no HTTP to
// fetch — so these tests stand a real greeting server up and prove the login
// reaches it, through the same egress policy an HTTP probe honours.

import (
	"bufio"
	"context"
	"net"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// mailGreetingServer answers the first commands of one SMTP conversation and
// records whether the client authenticated. It advertises CRAM-MD5, the one
// mechanism a deliberately plaintext relay can authenticate with.
func mailGreetingServer(t *testing.T, lines map[string]string) (host, port string, saw func(string) bool) {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	seen := map[string]bool{}
	go func() {
		connection, acceptErr := listener.Accept()
		if acceptErr != nil {
			return
		}
		defer connection.Close()
		reader := bufio.NewReader(connection)
		writer := bufio.NewWriter(connection)
		_, _ = writer.WriteString("220 fake ESMTP ready\r\n")
		_ = writer.Flush()
		scanner := bufio.NewScanner(reader)
		awaitingCram := false
		for scanner.Scan() {
			line := strings.TrimSpace(scanner.Text())
			upper := strings.ToUpper(line)
			for prefix := range lines {
				if strings.HasPrefix(upper, prefix) {
					seen[prefix] = true
				}
			}
			switch {
			case awaitingCram:
				awaitingCram = false
				_, _ = writer.WriteString("235 authenticated\r\n")
			case strings.HasPrefix(upper, "EHLO"):
				_, _ = writer.WriteString("250-fake greets you\r\n250-AUTH CRAM-MD5\r\n250 8BITMIME\r\n")
			case strings.HasPrefix(upper, "AUTH CRAM-MD5"):
				awaitingCram = true
				_, _ = writer.WriteString("334 PDkwMTM4NTcyOTYuMTY2NjQ3NTQyNUBmYWtlPg==\r\n")
			case strings.HasPrefix(upper, "QUIT"):
				_, _ = writer.WriteString("221 bye\r\n")
				_ = writer.Flush()
				return
			default:
				if answer, present := lines[firstWord(upper)]; present {
					_, _ = writer.WriteString(answer + "\r\n")
				} else {
					_, _ = writer.WriteString("250 ok\r\n")
				}
			}
			_ = writer.Flush()
		}
	}()
	t.Cleanup(func() { _ = listener.Close() })
	host, port, _ = net.SplitHostPort(listener.Addr().String())
	return host, port, func(prefix string) bool { return seen[prefix] }
}

func firstWord(line string) string {
	if index := strings.IndexByte(line, ' '); index > 0 {
		return line[:index]
	}
	return line
}

func TestTheMailCredentialTypesDeclareTheirFieldsAndTheirDialTests(t *testing.T) {
	t.Parallel()

	for id, want := range map[string][]string{
		"smtp": {"user", "password", "host", "port", "secure", "disableStartTls"},
		"imap": {"user", "password", "host", "port", "secure", "allowUnauthorizedCerts"},
	} {
		definition, found := credentials.Lookup(id)
		if !found {
			t.Errorf("credential type %q is not registered", id)
			continue
		}
		keys := make([]string, 0, len(definition.Fields))
		for _, field := range definition.Fields {
			keys = append(keys, field.Key)
		}
		if strings.Join(keys, ",") != strings.Join(want, ",") {
			t.Errorf("%s fields = %v, want %v", id, keys, want)
		}
	}
	for _, id := range []string{"smtp", "imap"} {
		credentialType, _ := credentials.Default().Get(id)
		if credentialType.Dial == nil {
			t.Errorf("%s declares no dial test; its connection test is the login", id)
			continue
		}
		if credentialType.Dial.Protocol != id {
			t.Errorf("%s dial protocol = %q, want %q", id, credentialType.Dial.Protocol, id)
		}
		if credentialType.Test != nil {
			t.Errorf("%s also declares an HTTP test; a mail server answers no HTTP", id)
		}
		if !credentialType.NeverSentOverHTTP {
			t.Errorf("%s is sent over HTTP nowhere; it must never count as unscoped", id)
		}
	}
}

func TestRunDialTestLogsIntoTheSMTPServer(t *testing.T) {
	t.Parallel()

	host, port, saw := mailGreetingServer(t, map[string]string{"AUTH": "235 authenticated"})
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	credentialType, _ := credentials.Default().Get("smtp")
	detail, err := credentials.RunDialTest(context.Background(), credentialType,
		credentials.Record{}, map[string]string{
			"host": host, "port": port, "secure": "false", "user": "ops", "password": "secret",
		}, policy)
	if err != nil {
		t.Fatalf("RunDialTest() error = %v", err)
	}
	if !strings.Contains(detail, "accepted the login") {
		t.Errorf("detail = %q, want the login verdict", detail)
	}
	if !saw("AUTH") {
		t.Error("the probe never authenticated; it proved nothing about the credential")
	}
}

func TestRunDialTestRefusesAHostThePolicyBlocks(t *testing.T) {
	t.Parallel()

	host, port, _ := mailGreetingServer(t, map[string]string{"AUTH": "235 authenticated"})
	credentialType, _ := credentials.Default().Get("imap")
	_, err := credentials.RunDialTest(context.Background(), credentialType,
		credentials.Record{}, map[string]string{"host": host, "port": port, "user": "ops", "password": "secret"},
		safehttp.DefaultPolicy())
	if err == nil || !strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
		t.Fatalf("RunDialTest() error = %v, want the loopback dial refused", err)
	}
}

func TestRunDialTestNamesATypeWithNoDialDeclared(t *testing.T) {
	t.Parallel()

	credentialType, _ := credentials.Default().Get("openAiApi")
	if _, err := credentials.RunDialTest(context.Background(), credentialType,
		credentials.Record{}, map[string]string{}, safehttp.DefaultPolicy()); err == nil || !strings.Contains(err.Error(), "no dial test") {
		t.Fatalf("RunDialTest() error = %v, want the absence named", err)
	}
}
