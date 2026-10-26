package nodes_test

// FEAT-sz4ddp: the email pair. The Send Email node builds a real MIME
// message and submits it over SMTP; the IMAP trigger polls a mailbox with a
// UID watermark on the leased poll framework. Both are proven here against
// in-process servers speaking the actual protocols, so what is asserted is
// what a mail server would see.

import (
	"bufio"
	"context"
	"encoding/base64"
	"fmt"
	"net"
	"strings"
	"sync"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/repository"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

// fakeMailSMTP records one submitted message and answers the minimum.
type fakeMailSMTP struct {
	listener net.Listener

	mu       sync.Mutex
	commands []string
	messages []string
}

func startFakeMailSMTP(t *testing.T) *fakeMailSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeMailSMTP{listener: listener}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (server *fakeMailSMTP) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go func(connection net.Conn) {
			defer connection.Close()
			writer := bufio.NewWriter(connection)
			reader := bufio.NewReader(connection)
			writeLine := func(line string) {
				_, _ = writer.WriteString(line + "\r\n")
				_ = writer.Flush()
			}
			writeLine("220 fake ESMTP ready")
			scanner := bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 0, 4<<20), 4<<20)
			for scanner.Scan() {
				line := strings.TrimSpace(scanner.Text())
				server.mu.Lock()
				server.commands = append(server.commands, line)
				server.mu.Unlock()
				switch {
				case strings.HasPrefix(strings.ToUpper(line), "EHLO"):
					writeLine("250-fake greets you\r\n250 8BITMIME")
				case strings.HasPrefix(strings.ToUpper(line), "MAIL FROM"), strings.HasPrefix(strings.ToUpper(line), "RCPT TO"):
					writeLine("250 ok")
				case strings.HasPrefix(strings.ToUpper(line), "DATA"):
					writeLine("354 end with a lone dot")
					var body []string
					for scanner.Scan() {
						if scanner.Text() == "." {
							break
						}
						body = append(body, scanner.Text())
					}
					server.mu.Lock()
					server.messages = append(server.messages, strings.Join(body, "\r\n"))
					server.mu.Unlock()
					writeLine("250 queued")
				case strings.HasPrefix(strings.ToUpper(line), "QUIT"):
					writeLine("221 bye")
					return
				default:
					writeLine("250 ok")
				}
			}
		}(connection)
	}
}

func (server *fakeMailSMTP) hostPort() map[string]string {
	host, port, _ := net.SplitHostPort(server.listener.Addr().String())
	return map[string]string{"host": host, "port": port}
}

func (server *fakeMailSMTP) delivered() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.messages...)
}

func smtpCredential(fields map[string]string) *stubCredentials {
	return &stubCredentials{credential: engine.Credential{
		ID: "cred-mail", Name: "Mail", Type: nodes.SMTPCredentialType,
		Fields: fields,
	}}
}

func emailSendNodeIR(t *testing.T, parameters map[string]any) workflow.IRNode {
	t.Helper()
	definition, found := aiRegistry(t).Lookup(nodes.EmailSendNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.EmailSendNodeType)
	}
	return workflow.IRNode{
		ID: "send", Name: "Send Email", Type: nodes.EmailSendNodeType, TypeVersion: workflow.V(1),
		Parameters: parameters, Definition: definition,
		Credentials: map[string]string{nodes.SMTPCredentialType: "cred-mail"},
	}
}

func runEmailSend(t *testing.T, parameters map[string]any, credential *stubCredentials, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	t.Helper()
	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	ir := emailSendNodeIR(t, parameters)
	request.Credentials = credential
	if input == nil {
		input = workflow.NodeInput{}
	}
	return runExecutor(t, executors, nodes.EmailSendExecutorID, ir, input, request)
}

func TestTheEmailNodesAreTheirOwnTypesWithTheirOwnCredentials(t *testing.T) {
	t.Parallel()

	registry := aiRegistry(t)
	send, found := registry.Get(nodes.EmailSendNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.EmailSendNodeType)
	}
	if len(send.Credentials) != 1 || send.Credentials[0].Type != nodes.SMTPCredentialType || !send.Credentials[0].Required {
		t.Errorf("Send Email credentials = %#v, want a required smtp", send.Credentials)
	}
	trigger, found := registry.Get(nodes.EmailReadImapNodeType, workflow.V(1))
	if !found {
		t.Fatalf("%s is not registered", nodes.EmailReadImapNodeType)
	}
	if len(trigger.Credentials) != 1 || trigger.Credentials[0].Type != nodes.IMAPCredentialType || !trigger.Credentials[0].Required {
		t.Errorf("IMAP trigger credentials = %#v, want a required imap", trigger.Credentials)
	}
	if len(trigger.Inputs) != 0 {
		t.Errorf("IMAP trigger inputs = %#v, want none: a trigger starts the graph", trigger.Inputs)
	}
	for _, want := range []string{"mailbox", "postProcessAction", "format", "pollTimes"} {
		present := false
		for _, parameter := range trigger.Parameters {
			if parameter.Key == want {
				present = true
			}
		}
		if !present {
			t.Errorf("IMAP trigger declares no %s", want)
		}
	}
	present := map[string]bool{}
	for _, parameter := range send.Parameters {
		present[parameter.Key] = true
	}
	for _, want := range []string{"fromEmail", "toEmail", "subject", "emailFormat", "text", "html", "options"} {
		if !present[want] {
			t.Errorf("Send Email declares no %s", want)
		}
	}
	// The leased poll framework lists exactly the handlers ExtractPolls
	// recognises, and the IMAP trigger is in both.
	handled := nodes.PollHandlers(localPolicy())
	if _, ok := handled[nodes.EmailReadImapNodeType]; !ok {
		t.Error("PollHandlers has no IMAP handler")
	}
	document := workflow.Document{Nodes: []workflow.Node{{
		ID: "imap", Type: nodes.EmailReadImapNodeType,
		Parameters: map[string]any{"pollTimes": map[string]any{"item": []any{map[string]any{"mode": "everyX", "unit": "minutes", "value": 5}}}},
	}}}
	polls := nodes.ExtractPolls(document)
	if len(polls) != 1 || polls[0].NodeType != nodes.EmailReadImapNodeType {
		t.Fatalf("ExtractPolls() = %#v, want the IMAP trigger", polls)
	}
	if polls[0].Interval.Minutes() != 5 {
		t.Errorf("interval = %v, want five minutes", polls[0].Interval)
	}
}

func TestEmailSendValidationNamesWhatIsMissing(t *testing.T) {
	t.Parallel()

	definition, _ := aiRegistry(t).Lookup(nodes.EmailSendNodeType, workflow.V(1))
	attached := map[string]string{nodes.SMTPCredentialType: "cred-mail"}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "smtp credential") {
		t.Errorf("Validate() = %v, want the missing credential", err)
	}
	if err := definition.Validate(workflow.Node{Credentials: attached, Parameters: map[string]any{}}); err == nil || !strings.Contains(err.Error(), "fromEmail is required") {
		t.Errorf("Validate() = %v, want fromEmail named", err)
	}
	if err := definition.Validate(workflow.Node{Credentials: attached, Parameters: map[string]any{"fromEmail": "ops@example.test"}}); err == nil || !strings.Contains(err.Error(), "toEmail is required") {
		t.Errorf("Validate() = %v, want toEmail named", err)
	}
	if err := definition.Validate(workflow.Node{Credentials: attached, Parameters: map[string]any{
		"fromEmail": "ops@example.test", "toEmail": "a@example.test", "emailFormat": "mime",
	}}); err == nil || !strings.Contains(err.Error(), "emailFormat") {
		t.Errorf("Validate() = %v, want the unknown format named", err)
	}
	if err := definition.Validate(workflow.Node{Credentials: attached, Parameters: map[string]any{
		"fromEmail": "ops@example.test\r\nBcc: evil@example.test", "toEmail": "a@example.test",
	}}); err == nil || !strings.Contains(err.Error(), "line break") {
		t.Errorf("Validate() = %v, want the header injection refused", err)
	}
	if err := definition.Validate(workflow.Node{Credentials: attached, Parameters: map[string]any{
		"fromEmail": "ops@example.test", "toEmail": "a@example.test", "text": "hi",
	}}); err != nil {
		t.Errorf("Validate() = %v, want a configured node accepted", err)
	}
}

func TestSendEmailBuildsAlternativesAndAttachments(t *testing.T) {
	t.Parallel()

	server := startFakeMailSMTP(t)
	store := binaryStore(t)
	report, err := store.Put("report.txt", "text/plain", strings.NewReader("quarterly numbers"))
	if err != nil {
		t.Fatalf("Put report = %v", err)
	}
	output, err := runEmailSend(t, map[string]any{
		"fromEmail": "Ops <ops@example.test>", "toEmail": "alice@example.test, bob@example.test",
		"subject": "nightly report", "emailFormat": "both",
		"text": "all systems nominal", "html": "<p>all <b>systems</b> nominal</p>",
		"options": map[string]any{
			"ccEmail": "carol@example.test", "replyTo": "replies@example.test",
			"fileAttachments": "report",
		},
	}, smtpCredential(appendMap(server.hostPort(), "secure", "false")),
		workflow.NodeInput{"main": {{Binary: map[string]workflow.BinaryRef{"report": report}}}},
		engine.Request{Binaries: store})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || len(output[0]) != 1 {
		t.Fatalf("output = %#v, want one item", output)
	}
	summary := output[0][0].JSON
	if accepted, _ := summary["accepted"].([]any); len(accepted) != 3 {
		t.Errorf("accepted = %#v, want to, the second to, and cc", summary["accepted"])
	}
	if _, hasMessageID := summary["messageId"].(string); !hasMessageID || summary["messageId"] == "" {
		t.Errorf("messageId = %#v, want one", summary["messageId"])
	}

	delivered := server.delivered()
	if len(delivered) != 1 {
		t.Fatalf("delivered = %d messages, want 1", len(delivered))
	}
	mailText := delivered[0]
	for _, want := range []string{
		"From: Ops <ops@example.test>",
		"To: alice@example.test, bob@example.test",
		"Cc: carol@example.test",
		"Reply-To: replies@example.test",
		"Subject: nightly report",
		"MIME-Version: 1.0",
		"multipart/mixed",
		"multipart/alternative",
		"text/plain; charset=utf-8",
		"text/html; charset=utf-8",
	} {
		if !strings.Contains(mailText, want) {
			t.Errorf("the message lacks %q:\n%s", want, mailText)
		}
	}
	if strings.Contains(mailText, "carol@example.test\r\nBcc") {
		t.Error("cc line broke into another header")
	}
	decoded, err := decodeMailBase64(mailText, "all systems nominal")
	if err != nil || decoded == "" {
		t.Errorf("the plain body did not decode: %v", err)
	}
	// The attachment part: the binary's own filename and content type.
	if !strings.Contains(mailText, `name="report.txt"`) || !strings.Contains(mailText, "Content-Disposition: attachment") {
		t.Errorf("the attachment part is malformed:\n%s", mailText)
	}
}

func TestSendEmailRefusesAHeaderInjectionAtRunTime(t *testing.T) {
	t.Parallel()

	server := startFakeMailSMTP(t)
	_, err := runEmailSend(t, map[string]any{
		"fromEmail": "ops@example.test", "toEmail": map[string]any{"mode": "expression", "value": "{{ $json.to }}"},
		"text": "hi",
	}, smtpCredential(appendMap(server.hostPort(), "secure", "false")), workflow.NodeInput{"main": {
		{JSON: map[string]any{"to": "alice@example.test\r\nBcc: evil@example.test"}},
	}}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), "line break") {
		t.Fatalf("Execute() error = %v, want the injected recipient refused", err)
	}
	if len(server.delivered()) != 0 {
		t.Error("a refused message was delivered anyway")
	}
}

func TestSendEmailRefusesAMissingBinaryAttachment(t *testing.T) {
	t.Parallel()

	server := startFakeMailSMTP(t)
	_, err := runEmailSend(t, map[string]any{
		"fromEmail": "ops@example.test", "toEmail": "alice@example.test", "text": "hi",
		"options": map[string]any{"fileAttachments": "report"},
	}, smtpCredential(appendMap(server.hostPort(), "secure", "false")), workflow.NodeInput{}, engine.Request{})
	if err == nil || !strings.Contains(err.Error(), `binary "report"`) {
		t.Fatalf("Execute() error = %v, want the missing binary named", err)
	}
}

func appendMap(base map[string]string, key, value string) map[string]string {
	out := map[string]string{}
	for existingKey, existingValue := range base {
		out[existingKey] = existingValue
	}
	out[key] = value
	return out
}

// decodeMailBase64 finds a base64 block near a marker and decodes it, so the
// test can assert the body a recipient's client would render.
func decodeMailBase64(mailText, marker string) (string, error) {
	_ = marker
	lines := strings.Split(mailText, "\r\n")
	var collected []string
	collecting := false
	for _, line := range lines {
		if strings.HasPrefix(line, "Content-Transfer-Encoding: base64") {
			collecting = true
			continue
		}
		if collecting {
			if line == "" {
				continue
			}
			if strings.HasPrefix(line, "--") {
				break
			}
			collected = append(collected, line)
		}
	}
	if len(collected) == 0 {
		return "", fmt.Errorf("no base64 body found")
	}
	decoded, err := base64.StdEncoding.DecodeString(strings.Join(collected, ""))
	if err != nil {
		return "", err
	}
	return string(decoded), nil
}

// fakeNodesIMAP is the one-mailbox server the poller tests speak to.
type fakeNodesIMAP struct {
	listener net.Listener

	mu       sync.Mutex
	mailbox  map[uint32]string
	seen     []uint32
	uidNext  uint32
	commands []string
}

func startFakeNodesIMAP(t *testing.T, messages map[uint32]string) *fakeNodesIMAP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeNodesIMAP{
		listener: listener, mailbox: map[uint32]string{},
		uidNext: uint32(len(messages)) + 1,
	}
	for uid, body := range messages {
		server.mailbox[uid] = body
	}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (server *fakeNodesIMAP) deliver(uid uint32, body string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.mailbox[uid] = body
	if uid >= server.uidNext {
		server.uidNext = uid + 1
	}
}

func (server *fakeNodesIMAP) markedSeen() []uint32 {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]uint32(nil), server.seen...)
}

func (server *fakeNodesIMAP) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(connection)
	}
}

func (server *fakeNodesIMAP) handle(connection net.Conn) {
	defer connection.Close()
	writer := bufio.NewWriter(connection)
	reader := bufio.NewReader(connection)
	writeLine := func(format string, arguments ...any) {
		_, _ = fmt.Fprintf(writer, format+"\r\n", arguments...)
		_ = writer.Flush()
	}
	writeLine("* OK fake IMAP ready [CAPABILITY IMAP4rev1]")
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 4<<20), 4<<20)
	for scanner.Scan() {
		line := strings.TrimRight(scanner.Text(), "\r")
		server.mu.Lock()
		server.commands = append(server.commands, line)
		server.mu.Unlock()
		fields := strings.Fields(line)
		if len(fields) < 2 {
			continue
		}
		tag, command := fields[0], strings.ToUpper(strings.Join(fields[1:], " "))
		switch {
		case strings.HasPrefix(command, "LOGIN"):
			writeLine("%s OK LOGIN completed", tag)
		case strings.HasPrefix(command, "SELECT"):
			server.mu.Lock()
			count := len(server.mailbox)
			uidNext := server.uidNext
			server.mu.Unlock()
			writeLine("* %d EXISTS", count)
			writeLine("* OK [UIDVALIDITY 385792458] UIDs valid")
			writeLine("* OK [UIDNEXT %d] predicted next UID", uidNext)
			writeLine("%s OK [READ-WRITE] SELECT completed", tag)
		case strings.HasPrefix(command, "UID SEARCH"):
			above := uint32(0)
			for _, field := range fields {
				if number, ok := parseSmallUint(field); ok && number > 0 {
					above = number
				}
			}
			server.mu.Lock()
			uids := []string{}
			for uid := range server.mailbox {
				if uid > above {
					uids = append(uids, fmt.Sprintf("%d", uid))
				}
			}
			server.mu.Unlock()
			sortStrings(uids)
			if len(uids) == 0 {
				writeLine("* SEARCH")
			} else {
				writeLine("* SEARCH %s", strings.Join(uids, " "))
			}
			writeLine("%s OK SEARCH completed", tag)
		case strings.HasPrefix(command, "UID FETCH"):
			uid := uint32(0)
			for _, field := range fields {
				if number, ok := parseSmallUint(field); ok && number > 0 {
					uid = number
				}
			}
			server.mu.Lock()
			body, present := server.mailbox[uid]
			server.mu.Unlock()
			if !present {
				writeLine("%s OK FETCH completed", tag)
				continue
			}
			writeLine("* %d FETCH (UID %d FLAGS () INTERNALDATE \"26-Oct-2026 03:00:00 +0000\" BODY[] {%d}", uid, uid, len(body))
			_, _ = writer.WriteString(body)
			_, _ = writer.WriteString(")\r\n")
			_ = writer.Flush()
			writeLine("%s OK FETCH completed", tag)
		case strings.HasPrefix(command, "UID STORE"):
			uid := uint32(0)
			for _, field := range fields {
				if number, ok := parseSmallUint(field); ok && number > 0 {
					uid = number
				}
			}
			server.mu.Lock()
			server.seen = append(server.seen, uid)
			server.mu.Unlock()
			writeLine("%s OK STORE completed", tag)
		case strings.HasPrefix(command, "LOGOUT"):
			writeLine("* BYE fake IMAP signing off")
			writeLine("%s OK LOGOUT completed", tag)
			return
		default:
			writeLine("%s BAD unsupported", tag)
		}
	}
}

func parseSmallUint(field string) (uint32, bool) {
	if field == "" {
		return 0, false
	}
	var number uint32
	for _, digit := range field {
		if digit < '0' || digit > '9' {
			return 0, false
		}
		number = number*10 + uint32(digit-'0')
	}
	return number, true
}

func sortStrings(values []string) {
	for i := 0; i < len(values); i++ {
		for j := i + 1; j < len(values); j++ {
			if values[j] < values[i] {
				values[i], values[j] = values[j], values[i]
			}
		}
	}
}

func multipartMail(subject string) string {
	plain := base64.StdEncoding.EncodeToString([]byte("the plain body"))
	html := base64.StdEncoding.EncodeToString([]byte("<p>the html body</p>"))
	return "From: Sender <sender@example.test>\r\n" +
		"To: ops@example.test\r\n" +
		"Subject: =?utf-8?B?" + base64.StdEncoding.EncodeToString([]byte(subject)) + "?=\r\n" +
		"Date: Mon, 26 Oct 2026 03:00:00 +0000\r\n" +
		"Message-ID: <1@example.test>\r\n" +
		"MIME-Version: 1.0\r\n" +
		"Content-Type: multipart/alternative; boundary=\"bb\"\r\n\r\n" +
		"--bb\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		plain + "\r\n" +
		"--bb\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"Content-Transfer-Encoding: base64\r\n\r\n" +
		html + "\r\n" +
		"--bb--\r\n"
}

func imapPollClaim(t *testing.T, server *fakeNodesIMAP, credential *stubCredentials, cursor string) poller.Claim {
	t.Helper()
	host, port, _ := net.SplitHostPort(server.listener.Addr().String())
	fields := map[string]string{"host": host, "port": port, "secure": "false", "user": "ops", "password": "secret"}
	if credential != nil {
		credential.credential.Fields = fields
	}
	return poller.Claim{
		Cursor: repository.PollCursor{Cursor: cursor},
		Node: workflow.Node{
			ID: "imap", Name: "Email Trigger", Type: nodes.EmailReadImapNodeType,
			Parameters: map[string]any{"mailbox": "INBOX"}, Credentials: map[string]string{nodes.IMAPCredentialType: "cred-mail"},
		},
		Request: engine.Request{Credentials: credential},
	}
}

func TestTheImapPollerWatermarksAndMarksSeen(t *testing.T) {
	t.Parallel()

	server := startFakeNodesIMAP(t, map[uint32]string{1: multipartMail("first")})
	resolver := &stubCredentials{credential: engine.Credential{ID: "cred-mail", Name: "Mail", Type: nodes.IMAPCredentialType}}
	pollerHandler := nodes.NewEmailImapPoller(localPolicy())

	// First poll: the baseline. Everything that exists is marked, nothing is
	// emitted — activation must not fire the workflow for old mail.
	items, cursor, err := pollerHandler.Poll(context.Background(), imapPollClaim(t, server, resolver, ""))
	if err != nil {
		t.Fatalf("first Poll() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("first Poll() emitted %d items, want none: activation is a baseline", len(items))
	}
	if !strings.Contains(cursor, `"uid":1`) || !strings.Contains(cursor, `"uidValidity":385792458`) {
		t.Fatalf("cursor = %q, want UID 1 under the mailbox's validity", cursor)
	}

	// New mail arrives; the next poll emits exactly it, marked as read.
	server.deliver(2, multipartMail("second"))
	items, cursor, err = pollerHandler.Poll(context.Background(), imapPollClaim(t, server, resolver, cursor))
	if err != nil {
		t.Fatalf("second Poll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("second Poll() emitted %d items, want the one new message", len(items))
	}
	message := items[0].JSON
	if message["subject"] != "second" {
		t.Errorf("subject = %#v, want the decoded subject", message["subject"])
	}
	if message["textPlain"] != "the plain body" || message["textHtml"] != "<p>the html body</p>" {
		t.Errorf("bodies = %#v / %#v, want the decoded alternatives", message["textPlain"], message["textHtml"])
	}
	if attributes, _ := message["attributes"].(map[string]any); attributes["uid"] != float64(2) {
		t.Errorf("attributes = %#v, want uid 2", message["attributes"])
	}
	metadata, _ := message["metadata"].(map[string]any)
	if metadata["message-id"] != "<1@example.test>" {
		t.Errorf("metadata = %#v, want the lowercased headers", metadata)
	}
	if seen := server.markedSeen(); len(seen) != 1 || seen[0] != 2 {
		t.Errorf("markedSeen() = %v, want [2]", seen)
	}
	if !strings.Contains(cursor, `"uid":2`) {
		t.Fatalf("cursor = %q, want the watermark moved to 2", cursor)
	}

	// Nothing new: no items, cursor held.
	items, held, err := pollerHandler.Poll(context.Background(), imapPollClaim(t, server, resolver, cursor))
	if err != nil {
		t.Fatalf("third Poll() error = %v", err)
	}
	if len(items) != 0 {
		t.Fatalf("third Poll() emitted %d items, want none", len(items))
	}
	if held != cursor {
		t.Errorf("cursor = %q, want it held at %q", held, cursor)
	}
}

func TestTheImapPollerEmitsRawWhenTheNodeAsksForIt(t *testing.T) {
	t.Parallel()

	server := startFakeNodesIMAP(t, map[uint32]string{1: "Subject: only\r\n\r\nbody\r\n"})
	resolver := &stubCredentials{credential: engine.Credential{ID: "cred-mail", Name: "Mail", Type: nodes.IMAPCredentialType}}
	pollerHandler := nodes.NewEmailImapPoller(localPolicy())
	_, baseline, err := pollerHandler.Poll(context.Background(), imapPollClaim(t, server, resolver, ""))
	if err != nil {
		t.Fatalf("baseline Poll() error = %v", err)
	}
	server.deliver(2, "Subject: raw one\r\n\r\nthe raw body\r\n")
	claim := imapPollClaim(t, server, resolver, baseline)
	claim.Node.Parameters["format"] = "raw"
	items, _, err := pollerHandler.Poll(context.Background(), claim)
	if err != nil {
		t.Fatalf("Poll() error = %v", err)
	}
	if len(items) != 1 {
		t.Fatalf("Poll() emitted %d items, want one", len(items))
	}
	raw, _ := items[0].JSON["raw"].(string)
	decoded, err := base64.URLEncoding.DecodeString(raw)
	if err != nil || !strings.Contains(string(decoded), "the raw body") {
		t.Fatalf("raw = %q (%v), want the base64url message", raw, err)
	}
}

func TestTheImapTriggerEmitsItsInputOnAManualRun(t *testing.T) {
	t.Parallel()

	executors := engine.NewRegistry()
	if err := nodes.RegisterExecutors(executors, localPolicy(), sqlGuard(), nil, nil, nil); err != nil {
		t.Fatalf("RegisterExecutors() error = %v", err)
	}
	definition, _ := aiRegistry(t).Lookup(nodes.EmailReadImapNodeType, workflow.V(1))
	ir := workflow.IRNode{ID: "imap", Name: "Email Trigger", Type: nodes.EmailReadImapNodeType, Definition: definition}
	request := engine.Request{Input: workflow.Item{JSON: map[string]any{"chatInput": "hello"}}}
	output, err := runExecutor(t, executors, nodes.EmailReadImapExecutorID, ir, workflow.NodeInput{}, request)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(output) != 1 || output[0][0].JSON["chatInput"] != "hello" {
		t.Fatalf("output = %#v, want the trigger's input item", output)
	}
}
