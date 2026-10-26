package mail_test

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"net"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/mail"
)

// fakeIMAP is a one-mailbox IMAP4rev1 server with the exact commands the
// client speaks: greeting, LOGIN, SELECT, UID SEARCH, UID FETCH with a
// literal, UID STORE and LOGOUT. Messages hold their flags, so a STORE is
// observable.
type fakeIMAP struct {
	listener    net.Listener
	certificate tls.Certificate
	user        string
	password    string
	startTLS    bool

	mu        sync.Mutex
	uidNext   uint32
	mailbox   map[uint32]string
	flags     map[uint32][]string
	commands  []string
	seenNotes []uint32
}

func startFakeIMAP(t *testing.T, user, password string, messages map[uint32]string) *fakeIMAP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeIMAP{
		listener: listener, user: user, password: password,
		uidNext: uint32(len(messages)) + 1,
		mailbox: map[uint32]string{}, flags: map[uint32][]string{},
	}
	for uid, body := range messages {
		server.mailbox[uid] = body
	}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

// withStartTLS makes the greeting advertise STARTTLS and the server speak it,
// so the client's opportunistic upgrade has something to upgrade to.
func (server *fakeIMAP) withStartTLS(t *testing.T) *fakeIMAP {
	t.Helper()
	server.certificate = selfSignedCert(t)
	server.startTLS = true
	return server
}

func (server *fakeIMAP) hostPort() (string, string) {
	host, port, _ := net.SplitHostPort(server.listener.Addr().String())
	return host, port
}

func (server *fakeIMAP) record(command string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.commands = append(server.commands, command)
}

func (server *fakeIMAP) recorded() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.commands...)
}

func (server *fakeIMAP) markedSeen() []uint32 {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]uint32(nil), server.seenNotes...)
}

func (server *fakeIMAP) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(connection)
	}
}

func (server *fakeIMAP) handle(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	reader := bufio.NewReader(connection)
	writer := bufio.NewWriter(connection)
	writeLine := func(format string, arguments ...any) {
		_, _ = fmt.Fprintf(writer, format+"\r\n", arguments...)
		_ = writer.Flush()
	}
	greeting := "* OK fake IMAP ready [CAPABILITY IMAP4rev1 AUTH=PLAIN]"
	if server.startTLS {
		greeting = "* OK fake IMAP ready [CAPABILITY IMAP4rev1 STARTTLS AUTH=PLAIN]"
	}
	writeLine(greeting)
	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			return
		}
		line = strings.TrimRight(line, "\r\n")
		server.record(line)
		fields := strings.Fields(line)
		if len(fields) < 2 {
			writeLine("%s BAD malformed", "A?")
			continue
		}
		tag, command := fields[0], strings.ToUpper(strings.Join(fields[1:], " "))
		switch {
		case strings.HasPrefix(command, "STARTTLS"):
			writeLine("%s OK begin TLS", tag)
			connection = tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{server.certificate}})
			reader = bufio.NewReader(connection)
			writer = bufio.NewWriter(connection)
		case strings.HasPrefix(command, "LOGIN"):
			if strings.Contains(line, `"`+server.user+`"`) && strings.Contains(line, `"`+server.password+`"`) {
				writeLine("%s OK LOGIN completed", tag)
			} else {
				writeLine("%s NO [AUTHENTICATIONFAILED] invalid credentials", tag)
			}
		case strings.HasPrefix(command, "SELECT"):
			writeLine("* %d EXISTS", len(server.mailbox))
			writeLine("* OK [UIDVALIDITY 385792458] UIDs valid")
			writeLine("* OK [UIDNEXT %d] predicted next UID", server.uidNext)
			writeLine("%s OK [READ-WRITE] SELECT completed", tag)
		case strings.HasPrefix(command, "UID SEARCH"):
			above := uint32(0)
			if match := lastNumber(command); match > 0 {
				above = match
			}
			server.mu.Lock()
			uids := []string{}
			for uid := range server.mailbox {
				if uid > above {
					uids = append(uids, strconv.FormatUint(uint64(uid), 10))
				}
			}
			server.mu.Unlock()
			// Sorted so the answer is stable regardless of map order.
			for i := 0; i < len(uids); i++ {
				for j := i + 1; j < len(uids); j++ {
					if uids[j] < uids[i] {
						uids[i], uids[j] = uids[j], uids[i]
					}
				}
			}
			if len(uids) == 0 {
				writeLine("* SEARCH")
			} else {
				writeLine("* SEARCH %s", strings.Join(uids, " "))
			}
			writeLine("%s OK SEARCH completed", tag)
		case strings.HasPrefix(command, "UID FETCH"):
			uid := lastNumber(strings.TrimSuffix(strings.TrimPrefix(command, "UID FETCH "), " (FLAGS INTERNALDATE BODY.PEEK[])"))
			server.mu.Lock()
			body, present := server.mailbox[uid]
			flags := server.flags[uid]
			server.mu.Unlock()
			if !present {
				writeLine("%s OK FETCH completed", tag)
				continue
			}
			flagText := strings.Join(flags, " ")
			writeLine("* %d FETCH (UID %d FLAGS (%s) INTERNALDATE \"26-Oct-2026 03:00:00 +0000\" BODY[] {%d}", uid, uid, flagText, len(body))
			_, _ = writer.WriteString(body)
			_, _ = writer.WriteString(")\r\n")
			_ = writer.Flush()
			writeLine("%s OK FETCH completed", tag)
		case strings.HasPrefix(command, "UID STORE"):
			uid := lastNumber(strings.TrimSuffix(strings.TrimPrefix(command, "UID STORE "), " +FLAGS.SILENT (\\Seen)"))
			server.mu.Lock()
			server.flags[uid] = append(server.flags[uid], "\\Seen")
			server.seenNotes = append(server.seenNotes, uid)
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

// lastNumber picks the last integer in a command, which for the commands this
// server answers is the UID the client named.
func lastNumber(command string) uint32 {
	fields := strings.FieldsFunc(command, func(r rune) bool {
		return r == ' ' || r == '(' || r == ')' || r == '[' || r == ']'
	})
	for index := len(fields) - 1; index >= 0; index-- {
		if number, err := strconv.ParseUint(fields[index], 10, 32); err == nil {
			return uint32(number)
		}
	}
	return 0
}

func sampleMessage(uid uint32) string {
	return fmt.Sprintf("Return-Path: <sender@example.test>\r\nMessage-ID: <%d@example.test>\r\nFrom: Sender <sender@example.test>\r\nTo: ops@example.test\r\nSubject: message %d\r\nDate: Mon, 26 Oct 2026 03:00:00 +0000\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nHello from message %d.\r\n", uid, uid, uid)
}

func TestIMAPSelectSearchFetchAndMarkSeen(t *testing.T) {
	t.Parallel()

	server := startFakeIMAP(t, "ops", "secret", map[uint32]string{
		1: sampleMessage(1), 2: sampleMessage(2), 3: sampleMessage(3),
	})
	host, port := server.hostPort()
	client, err := mail.DialIMAP(context.Background(), mail.IMAPSettings{
		Host: host, Port: port, User: "ops", Password: "secret", Policy: loopbackPolicy(),
	})
	if err != nil {
		t.Fatalf("DialIMAP() error = %v", err)
	}
	defer client.Close()

	info, err := client.Select("INBOX")
	if err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	if info.UIDValidity == 0 || info.UIDNext != 4 {
		t.Errorf("Select() = %#v, want the fake's UIDVALIDITY and UIDNEXT", info)
	}

	uids, err := client.SearchUIDsAfter(1)
	if err != nil {
		t.Fatalf("SearchUIDsAfter(1) error = %v", err)
	}
	if len(uids) != 2 || uids[0] != 2 || uids[1] != 3 {
		t.Fatalf("SearchUIDsAfter(1) = %v, want [2 3]", uids)
	}

	message, err := client.FetchMessage(3)
	if err != nil {
		t.Fatalf("FetchMessage(3) error = %v", err)
	}
	if message.UID != 3 || !strings.Contains(string(message.Body), "Subject: message 3") {
		t.Errorf("message = %#v, want UID 3 and its body", message)
	}
	if message.InternalDate == "" {
		t.Errorf("message = %#v, want the internal date", message)
	}

	if err := client.MarkSeen(3); err != nil {
		t.Fatalf("MarkSeen(3) error = %v", err)
	}
	if seen := server.markedSeen(); len(seen) != 1 || seen[0] != 3 {
		t.Errorf("markedSeen() = %v, want [3]", seen)
	}
}

func TestIMAPRefusesAWrongPassword(t *testing.T) {
	t.Parallel()

	server := startFakeIMAP(t, "ops", "secret", map[uint32]string{1: sampleMessage(1)})
	host, port := server.hostPort()
	_, err := mail.DialIMAP(context.Background(), mail.IMAPSettings{
		Host: host, Port: port, User: "ops", Password: "wrong", Policy: loopbackPolicy(),
	})
	if err == nil || !strings.Contains(err.Error(), "refused the login") {
		t.Fatalf("DialIMAP() error = %v, want the login refused", err)
	}
}

func TestIMAPSearchFiltersTheStarsEdgeTheProtocolAllows(t *testing.T) {
	t.Parallel()

	// One message: UID SEARCH UID 5:* must answer that message even though
	// its UID is below 5, because the protocol defines `n:*` to include the
	// last message unconditionally — the client, not the server, filters.
	server := startFakeIMAP(t, "ops", "secret", map[uint32]string{1: sampleMessage(1)})
	host, port := server.hostPort()
	client, err := mail.DialIMAP(context.Background(), mail.IMAPSettings{
		Host: host, Port: port, User: "ops", Password: "secret", Policy: loopbackPolicy(),
	})
	if err != nil {
		t.Fatalf("DialIMAP() error = %v", err)
	}
	defer client.Close()
	if _, err := client.Select("INBOX"); err != nil {
		t.Fatalf("Select() error = %v", err)
	}
	uids, err := client.SearchUIDsAfter(4)
	if err != nil {
		t.Fatalf("SearchUIDsAfter(4) error = %v", err)
	}
	if len(uids) != 0 {
		t.Errorf("SearchUIDsAfter(4) = %v, want none: the protocol's star edge was not filtered", uids)
	}
}
