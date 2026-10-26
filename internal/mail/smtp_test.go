package mail_test

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/mail"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// loopbackPolicy allows the loopback fakes while keeping every other guard
// the deployment sets.
func loopbackPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// selfSignedCert mints one certificate for the loopback fakes, so the TLS
// paths are exercised exactly as a self-signed relay would see them: accepted
// only when the settings allow unverified certificates.
func selfSignedCert(t *testing.T) tls.Certificate {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("generate key: %v", err)
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1"), net.ParseIP("::1")},
		DNSNames:     []string{"localhost"},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
	}
	der, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		t.Fatalf("create certificate: %v", err)
	}
	return tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
}

// fakeSMTP is a minimal ESMTP server: it records every command, answers the
// minimum the client needs, and can speak STARTTLS and AUTH.
type fakeSMTP struct {
	listener      net.Listener
	certificate   tls.Certificate
	startTLS      bool
	advertiseAuth string

	mu       sync.Mutex
	commands []string
	messages []string
}

func startFakeSMTP(t *testing.T, startTLS bool, advertiseAuth string, certificate tls.Certificate) *fakeSMTP {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTP{listener: listener, certificate: certificate, startTLS: startTLS, advertiseAuth: advertiseAuth}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })
	return server
}

func (server *fakeSMTP) hostPort() (string, string) {
	address := server.listener.Addr().String()
	host, port, _ := net.SplitHostPort(address)
	return host, port
}

func (server *fakeSMTP) record(command string) {
	server.mu.Lock()
	defer server.mu.Unlock()
	server.commands = append(server.commands, command)
}

func (server *fakeSMTP) recorded() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.commands...)
}

func (server *fakeSMTP) delivered() []string {
	server.mu.Lock()
	defer server.mu.Unlock()
	return append([]string(nil), server.messages...)
}

func (server *fakeSMTP) serve() {
	for {
		connection, err := server.listener.Accept()
		if err != nil {
			return
		}
		go server.handle(connection)
	}
}

func (server *fakeSMTP) handle(connection net.Conn) {
	defer connection.Close()
	_ = connection.SetDeadline(time.Now().Add(10 * time.Second))
	writer := bufio.NewWriter(connection)
	reader := bufio.NewReader(connection)
	writeLine := func(line string) {
		_, _ = writer.WriteString(line + "\r\n")
		_ = writer.Flush()
	}
	writeLine("220 fake ESMTP ready")
	scanner := bufio.NewScanner(reader)
	scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
	for scanner.Scan() {
		line := scanner.Text()
		server.record(strings.TrimSpace(line))
		upper := strings.ToUpper(line)
		switch {
		case strings.HasPrefix(upper, "EHLO"), strings.HasPrefix(upper, "HELO"):
			reply := "250-fake greets you"
			if server.startTLS {
				reply += "\r\n250-STARTTLS"
			}
			if server.advertiseAuth != "" {
				reply += "\r\n250-AUTH " + server.advertiseAuth
			}
			writeLine(reply + "\r\n250 8BITMIME")
		case strings.HasPrefix(upper, "STARTTLS"):
			writeLine("220 go ahead")
			connection = tls.Server(connection, &tls.Config{Certificates: []tls.Certificate{server.certificate}})
			reader = bufio.NewReader(connection)
			writer = bufio.NewWriter(connection)
			scanner = bufio.NewScanner(reader)
			scanner.Buffer(make([]byte, 0, 1<<20), 1<<20)
		case strings.HasPrefix(upper, "AUTH PLAIN"):
			writeLine("235 authenticated")
		case strings.HasPrefix(upper, "AUTH CRAM-MD5"):
			writeLine("334 " + "challenge")
			if scanner.Scan() {
				server.record(strings.TrimSpace(scanner.Text()))
			}
			writeLine("235 authenticated")
		case strings.HasPrefix(upper, "AUTH LOGIN"):
			writeLine("334 " + "VXNlcm5hbWU6")
			if scanner.Scan() {
				server.record("username: " + strings.TrimSpace(scanner.Text()))
			}
			writeLine("334 " + "UGFzc3dvcmQ6")
			if scanner.Scan() {
				server.record("password: " + strings.TrimSpace(scanner.Text()))
			}
			writeLine("235 authenticated")
		case strings.HasPrefix(upper, "MAIL FROM"), strings.HasPrefix(upper, "RCPT TO"):
			writeLine("250 ok")
		case strings.HasPrefix(upper, "DATA"):
			writeLine("354 end with a lone dot")
			var body []string
			for scanner.Scan() {
				if scanner.Text() == "." {
					break
				}
				body = append(body, scanner.Text())
			}
			server.mu.Lock()
			server.messages = append(server.messages, strings.Join(body, "\n"))
			server.mu.Unlock()
			writeLine("250 queued")
		case strings.HasPrefix(upper, "QUIT"):
			writeLine("221 bye")
			return
		default:
			writeLine("502 not implemented")
		}
	}
}

func textMessage(recipients ...string) []byte {
	return []byte("From: ops@example.test\r\nTo: " + strings.Join(recipients, ",") +
		"\r\nSubject: nightly\r\n\r\nall systems nominal\r\n")
}

func TestSendDeliversAMessageOverPlaintext(t *testing.T) {
	t.Parallel()

	server := startFakeSMTP(t, false, "", tls.Certificate{})
	host, port := server.hostPort()
	err := mail.Send(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, From: "ops@example.test",
		Recipients: []string{"alice@example.test", "bob@example.test"},
		Message:    textMessage("alice@example.test", "bob@example.test"),
		Policy:     loopbackPolicy(),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	commands := strings.Join(server.recorded(), "\n")
	if !strings.Contains(commands, "MAIL FROM:<ops@example.test>") {
		t.Errorf("commands = %q, want the envelope sender", commands)
	}
	if strings.Count(commands, "RCPT TO:") != 2 {
		t.Errorf("commands = %q, want both recipients", commands)
	}
	if delivered := server.delivered(); len(delivered) != 1 || !strings.Contains(delivered[0], "all systems nominal") {
		t.Errorf("delivered = %#v, want the message body", delivered)
	}
}

func TestSendUpgradesWithStartTLSAndAuthenticates(t *testing.T) {
	t.Parallel()

	certificate := selfSignedCert(t)
	server := startFakeSMTP(t, true, "PLAIN", certificate)
	host, port := server.hostPort()
	err := mail.Send(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, From: "ops@example.test", User: "ops", Password: "secret",
		Recipients: []string{"alice@example.test"}, Message: textMessage("alice@example.test"),
		InsecureCerts: true, Policy: loopbackPolicy(),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	commands := server.recorded()
	if len(commands) < 4 || !strings.HasPrefix(commands[1], "STARTTLS") {
		t.Errorf("commands = %#v, want STARTTLS second, before AUTH", commands)
	}
	sawTLS := false
	for _, command := range commands {
		if strings.HasPrefix(strings.ToUpper(command), "STARTTLS") {
			sawTLS = true
		}
		if strings.HasPrefix(strings.ToUpper(command), "AUTH") && !sawTLS {
			t.Fatalf("AUTH ran before STARTTLS: %#v", commands)
		}
	}
	if !strings.Contains(strings.Join(commands, "\n"), "AUTH PLAIN") {
		t.Errorf("commands = %#v, want AUTH PLAIN after the upgrade", commands)
	}
}

func TestSendUsesImplicitTLSFromTheStart(t *testing.T) {
	t.Parallel()

	listener, err := tls.Listen("tcp", "127.0.0.1:0", &tls.Config{Certificates: []tls.Certificate{selfSignedCert(t)}})
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	server := &fakeSMTP{listener: listener, advertiseAuth: "PLAIN"}
	go server.serve()
	t.Cleanup(func() { _ = listener.Close() })

	host, port, _ := net.SplitHostPort(listener.Addr().String())
	err = mail.Send(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, Secure: true, From: "ops@example.test", User: "ops", Password: "secret",
		Recipients: []string{"alice@example.test"}, Message: textMessage("alice@example.test"),
		InsecureCerts: true, Policy: loopbackPolicy(),
	})
	if err != nil {
		t.Fatalf("Send() error = %v", err)
	}
	if delivered := server.delivered(); len(delivered) != 1 {
		t.Errorf("delivered = %#v, want the message over implicit TLS", delivered)
	}
}

func TestSendRefusesAPrivateHostThePolicyBlocks(t *testing.T) {
	t.Parallel()

	server := startFakeSMTP(t, false, "", tls.Certificate{})
	host, port := server.hostPort()
	err := mail.Send(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, From: "ops@example.test",
		Recipients: []string{"alice@example.test"}, Message: textMessage("alice@example.test"),
		Policy: safehttp.DefaultPolicy(),
	})
	if err == nil || !strings.Contains(err.Error(), safehttp.ErrBlocked.Error()) {
		t.Fatalf("Send() error = %v, want the loopback dial refused by the default policy", err)
	}
}

func TestSendHonoursTheCredentialScope(t *testing.T) {
	t.Parallel()

	server := startFakeSMTP(t, false, "", tls.Certificate{})
	host, port := server.hostPort()
	err := mail.Send(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, From: "ops@example.test",
		Recipients: []string{"alice@example.test"}, Message: textMessage("alice@example.test"),
		Policy:      loopbackPolicy(),
		AllowedHost: func(string) bool { return false },
	})
	if err == nil || !strings.Contains(err.Error(), "not allowed to reach") {
		t.Fatalf("Send() error = %v, want the host refused by the credential scope", err)
	}
}

func TestPingSMTPAuthenticatesWithoutSubmitting(t *testing.T) {
	t.Parallel()

	server := startFakeSMTP(t, false, "LOGIN", tls.Certificate{})
	host, port := server.hostPort()
	if err := mail.PingSMTP(context.Background(), mail.SMTPSettings{
		Host: host, Port: port, User: "ops", Password: "secret", Policy: loopbackPolicy(),
	}); err != nil {
		t.Fatalf("PingSMTP() error = %v", err)
	}
	commands := strings.Join(server.recorded(), "\n")
	if !strings.Contains(commands, "AUTH LOGIN") {
		t.Errorf("commands = %q, want the login", commands)
	}
	for _, banned := range []string{"MAIL FROM", "RCPT TO", "DATA"} {
		if strings.Contains(commands, banned) {
			t.Errorf("commands = %q, a ping must not contain %q", commands, banned)
		}
	}
	if len(server.delivered()) != 0 {
		t.Errorf("delivered = %#v, a ping submits nothing", server.delivered())
	}
}
