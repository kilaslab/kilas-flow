package mail

import (
	"bufio"
	"context"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// IMAPSettings is one mailbox login, mirroring n8n's IMAP credential: Secure
// connects with TLS from the start (port 993), and InsecureCerts accepts a
// self-signed certificate.
type IMAPSettings struct {
	Host          string
	Port          string
	Secure        bool
	User          string
	Password      string
	InsecureCerts bool
	Policy        safehttp.Policy
	AllowedHost   func(host string) bool
}

// IMAPClient is one logged-in IMAP4rev1 session. It speaks exactly the
// commands a polling reader needs — SELECT, UID SEARCH, UID FETCH of one
// message, UID STORE and LOGOUT — so a workflow document can never make it
// run anything else.
type IMAPClient struct {
	connection net.Conn
	reader     *bufio.Reader
	tag        int
}

// IMAPMessage is one fetched message: its UID, its flags, the server's
// internal date in IMAP's own `dd-Mon-yyyy hh:mm:ss +zzzz` spelling, and the
// whole RFC 822 message as bytes.
type IMAPMessage struct {
	UID          uint32
	Flags        []string
	InternalDate string
	Body         []byte
}

// commandTimeout bounds one command and its answer.
const commandTimeout = 30 * time.Second

// DialIMAP connects, greets, and logs in. A non-TLS connection is upgraded
// with STARTTLS when the server offers it — better than n8n's client, which
// simply sends the password in the clear on port 143 — and left plain only
// when the server offers nothing, which is the shape of a local test relay.
func DialIMAP(ctx context.Context, settings IMAPSettings) (*IMAPClient, error) {
	host, port, err := mailTarget(settings.Host, settings.Port, defaultIMAPPort, settings.AllowedHost)
	if err != nil {
		return nil, err
	}
	connection, err := guardedDial(ctx, "tcp", host, port, settings.Policy)
	if err != nil {
		return nil, err
	}
	if settings.Secure {
		connection = tls.Client(connection, tlsConfig(host, settings.InsecureCerts))
	}
	client := &IMAPClient{connection: connection, reader: bufio.NewReaderSize(connection, 16<<10)}
	greeting, err := client.readRecord()
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("the IMAP server did not greet: %w", err)
	}
	if !strings.HasPrefix(greeting.text(), "* OK") {
		_ = connection.Close()
		return nil, fmt.Errorf("the IMAP server refused the connection: %s", strings.TrimSpace(greeting.text()))
	}
	if !settings.Secure && strings.Contains(strings.ToUpper(greeting.text()), "STARTTLS") {
		if err := client.startTLS(host, settings.InsecureCerts); err != nil {
			_ = connection.Close()
			return nil, err
		}
	}
	if err := client.command("LOGIN %s %s", quoteIMAP(settings.User), quoteIMAP(settings.Password)); err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("the IMAP server refused the login: %w", err)
	}
	return client, nil
}

// Close ends the session politely when it can and always closes the socket.
func (client *IMAPClient) Close() {
	if client == nil || client.connection == nil {
		return
	}
	_ = client.connection.SetDeadline(time.Now().Add(commandTimeout))
	_, _ = client.connection.Write([]byte(client.nextTag() + " LOGOUT\r\n"))
	_, _ = client.readRecord()
	_ = client.connection.Close()
	client.connection = nil
}

func (client *IMAPClient) startTLS(host string, insecureCerts bool) error {
	if err := client.command("STARTTLS"); err != nil {
		return fmt.Errorf("the IMAP server refused the STARTTLS upgrade: %w", err)
	}
	client.connection = tls.Client(client.connection, tlsConfig(host, insecureCerts))
	client.reader = bufio.NewReaderSize(client.connection, 16<<10)
	return nil
}

func (client *IMAPClient) nextTag() string {
	client.tag++
	return "A" + strconv.Itoa(client.tag)
}

// MailboxInfo is what SELECT answers: the mailbox's UIDVALIDITY, which says
// whether yesterday's UIDs still mean anything, and its UIDNEXT, which is a
// baseline for a first poll.
type MailboxInfo struct {
	UIDValidity uint32
	UIDNext     uint32
}

var (
	uidValidityPattern = regexp.MustCompile(`UIDVALIDITY (\d+)`)
	uidNextPattern     = regexp.MustCompile(`UIDNEXT (\d+)`)
)

// Select opens one mailbox read-write, which flag stores require.
func (client *IMAPClient) Select(mailbox string) (MailboxInfo, error) {
	records, err := client.exchange("SELECT %s", quoteIMAP(mailbox))
	if err != nil {
		return MailboxInfo{}, fmt.Errorf("the IMAP server refused to open the mailbox %q: %w", mailbox, err)
	}
	info := MailboxInfo{}
	for _, record := range records {
		text := strings.ToUpper(record.text())
		if match := uidValidityPattern.FindStringSubmatch(text); match != nil {
			validity, _ := strconv.ParseUint(match[1], 10, 32)
			info.UIDValidity = uint32(validity)
		}
		if match := uidNextPattern.FindStringSubmatch(text); match != nil {
			next, _ := strconv.ParseUint(match[1], 10, 32)
			info.UIDNext = uint32(next)
		}
	}
	return info, nil
}

// SearchUIDsAfter lists the UIDs strictly above the watermark. IMAP's
// `UID n:*` always includes the highest existing message even when its UID is
// below n, so the comparison is made again here rather than trusted to the
// server.
func (client *IMAPClient) SearchUIDsAfter(watermark uint32) ([]uint32, error) {
	records, err := client.exchange("UID SEARCH UID %d:*", watermark+1)
	if err != nil {
		return nil, fmt.Errorf("the IMAP search failed: %w", err)
	}
	uids := []uint32{}
	for _, record := range records {
		fields := strings.Fields(record.text())
		if len(fields) < 2 || fields[0] != "*" || fields[1] != "SEARCH" {
			continue
		}
		for _, field := range fields[2:] {
			value, err := strconv.ParseUint(field, 10, 32)
			if err != nil || value <= uint64(watermark) {
				continue
			}
			uids = append(uids, uint32(value))
		}
	}
	return uids, nil
}

// FetchMessage reads one message without marking it seen: BODY.PEEK[] is the
// server's own spelling of "read but do not set \Seen".
func (client *IMAPClient) FetchMessage(uid uint32) (IMAPMessage, error) {
	records, err := client.exchange("UID FETCH %d (FLAGS INTERNALDATE BODY.PEEK[])", uid)
	if err != nil {
		return IMAPMessage{}, fmt.Errorf("the IMAP fetch of message %d failed: %w", uid, err)
	}
	for _, record := range records {
		if !strings.HasPrefix(record.text(), "* ") || !strings.Contains(record.text(), "FETCH") {
			continue
		}
		message := IMAPMessage{UID: uid}
		text := record.text()
		if match := uidPattern.FindStringSubmatch(text); match != nil {
			if parsed, _ := strconv.ParseUint(match[1], 10, 32); uint32(parsed) == uid {
				message.UID = uint32(parsed)
			}
		}
		if match := flagsPattern.FindStringSubmatch(text); match != nil {
			message.Flags = strings.Fields(match[1])
		}
		if match := internalDatePattern.FindStringSubmatch(text); match != nil {
			message.InternalDate = match[1]
		}
		if literal, ok := record.firstLiteral(); ok {
			message.Body = literal
		}
		if len(message.Body) == 0 {
			return IMAPMessage{}, fmt.Errorf("message %d came back with no body", uid)
		}
		return message, nil
	}
	return IMAPMessage{}, fmt.Errorf("message %d was not in the mailbox", uid)
}

var (
	uidPattern          = regexp.MustCompile(`UID (\d+)`)
	flagsPattern        = regexp.MustCompile(`FLAGS \(([^)]*)\)`)
	internalDatePattern = regexp.MustCompile(`INTERNALDATE "([^"]*)"`)
)

// MarkSeen sets \Seen on one message, which is n8n's "Mark as Read" action.
func (client *IMAPClient) MarkSeen(uid uint32) error {
	if _, err := client.exchange("UID STORE %d +FLAGS.SILENT (\\Seen)", uid); err != nil {
		return fmt.Errorf("marking message %d as read failed: %w", uid, err)
	}
	return nil
}

// quoteIMAP writes one quoted string, escaping the two characters RFC 3501
// forbids inside quotes.
func quoteIMAP(value string) string {
	return `"` + strings.NewReplacer(`\`, `\\`, `"`, `\"`).Replace(value) + `"`
}

// command sends one command and expects nothing but its tagged OK.
func (client *IMAPClient) command(format string, arguments ...any) error {
	_, err := client.exchange(format, arguments...)
	return err
}

// exchange sends one command and collects every untagged record before the
// tagged completion. A NO or BAD completion is an error carrying the server's
// own sentence, trimmed of the tag.
func (client *IMAPClient) exchange(format string, arguments ...any) ([]imapRecord, error) {
	if client.connection == nil {
		return nil, fmt.Errorf("the IMAP session is closed")
	}
	tag := client.nextTag()
	_ = client.connection.SetDeadline(time.Now().Add(commandTimeout))
	if _, err := fmt.Fprintf(client.connection, tag+" "+format+"\r\n", arguments...); err != nil {
		return nil, err
	}
	records := []imapRecord{}
	for {
		record, err := client.readRecord()
		if err != nil {
			return nil, err
		}
		text := record.text()
		switch {
		case strings.HasPrefix(text, tag+" "):
			answer := strings.TrimSpace(strings.TrimPrefix(text, tag+" "))
			if strings.HasPrefix(strings.ToUpper(answer), "OK") {
				return records, nil
			}
			return records, fmt.Errorf("%s", answer)
		case text == "":
			continue
		default:
			records = append(records, record)
		}
	}
}

// literalAnnouncement is the `{123}` at the end of a line that says the next
// 123 bytes are a literal, not line content.
var literalAnnouncement = regexp.MustCompile(`\{(\d+)\}$`)

// imapRecord is one server record: text segments and literals in the order
// the server wrote them. A FETCH body arrives as a literal in the middle of
// its parenthesized list, so a record is a sequence rather than one string.
type imapRecord struct {
	segments []imapSegment
}

type imapSegment struct {
	text    string
	literal []byte
	isBytes bool
}

func (record imapRecord) text() string {
	parts := make([]string, 0, len(record.segments))
	for _, segment := range record.segments {
		if !segment.isBytes {
			parts = append(parts, segment.text)
		}
	}
	return strings.Join(parts, "")
}

func (record imapRecord) firstLiteral() ([]byte, bool) {
	for _, segment := range record.segments {
		if segment.isBytes {
			return segment.literal, true
		}
	}
	return nil, false
}

// readRecord reads one record: line segments and literals up to the line
// ending that no literal announcement precedes.
func (client *IMAPClient) readRecord() (imapRecord, error) {
	record := imapRecord{}
	for {
		line, err := client.reader.ReadString('\n')
		if err != nil {
			return record, err
		}
		line = strings.TrimRight(line, "\r\n")
		match := literalAnnouncement.FindStringSubmatch(line)
		if match == nil {
			record.segments = append(record.segments, imapSegment{text: line})
			return record, nil
		}
		record.segments = append(record.segments, imapSegment{text: line})
		size, err := strconv.Atoi(match[1])
		if err != nil {
			return record, err
		}
		literal := make([]byte, size)
		if _, err := io.ReadFull(client.reader, literal); err != nil {
			return record, err
		}
		record.segments = append(record.segments, imapSegment{literal: literal, isBytes: true})
	}
}
