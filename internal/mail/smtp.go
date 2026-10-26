package mail

import (
	"context"
	"crypto/tls"
	"fmt"
	"net/smtp"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// SMTPSettings is one submission: where to send, how to authenticate, and the
// already-built RFC 5322 message. The fields mirror n8n's SMTP credential so
// an imported credential's meaning survives the mapping: Secure is n8n's
// `secure` (implicit TLS from the start), DisableStartTLS is its
// `disableStartTls`, and a credential with no user and password skips
// authentication the way nodemailer does for an internal relay.
type SMTPSettings struct {
	Host     string
	Port     string
	Secure   bool
	User     string
	Password string
	// DisableStartTLS leaves a non-TLS connection plain rather than upgrading
	// when the server offers STARTTLS. The credential's owner opts in; the
	// upgrade is attempted whenever it is not set.
	DisableStartTLS bool
	// InsecureCerts accepts a self-signed server certificate.
	InsecureCerts bool
	// From is the envelope sender; Recipients are the envelope recipients,
	// already split out of the To, Cc and Bcc fields.
	From       string
	Recipients []string
	// Message is the full RFC 5322 message, headers and bodies.
	Message []byte
	// Policy guards the dial. A zero policy refuses every private address,
	// which is the correct default for a hosted install.
	Policy safehttp.Policy
	// AllowedHost, when set, is the credential's own domain scope.
	AllowedHost func(host string) bool
}

// Send submits one message over SMTP.
//
// The handshake follows what n8n's Send Email node does through nodemailer:
// implicit TLS when the credential says so, an opportunistic STARTTLS upgrade
// on a plain connection when the server offers one and the credential has not
// disabled it, and authentication with PLAIN, CRAM-MD5 or LOGIN — whichever
// the server advertises — when the credential carries a user.
func Send(ctx context.Context, settings SMTPSettings) error {
	client, err := connectSMTP(ctx, settings)
	if err != nil {
		return err
	}
	defer client.Close()
	if len(settings.Recipients) == 0 {
		return fmt.Errorf("the message has no recipients")
	}

	if err := client.Mail(settings.From); err != nil {
		return fmt.Errorf("the SMTP server refused the sender %q: %w", settings.From, err)
	}
	for _, recipient := range settings.Recipients {
		if err := client.Rcpt(recipient); err != nil {
			return fmt.Errorf("the SMTP server refused the recipient %q: %w", recipient, err)
		}
	}
	writer, err := client.Data()
	if err != nil {
		return fmt.Errorf("the SMTP server did not accept the message body: %w", err)
	}
	if _, err := writer.Write(settings.Message); err != nil {
		_ = writer.Close()
		return fmt.Errorf("writing the message failed: %w", err)
	}
	if err := writer.Close(); err != nil {
		return fmt.Errorf("the SMTP server refused the message: %w", err)
	}
	return client.Quit()
}

// PingSMTP proves a credential: the same dial, greeting, upgrade and login a
// send performs, and nothing after it — no envelope is opened and no message
// is submitted. It is the connection test a credential of this kind declares,
// and it draws the same line internal/sqlnode.Test draws at "opens a
// connection and closes it again".
func PingSMTP(ctx context.Context, settings SMTPSettings) error {
	client, err := connectSMTP(ctx, settings)
	if err != nil {
		return err
	}
	return client.Quit()
}

// connectSMTP performs the half of a submission that proves the credential:
// the policy-guarded dial, the greeting, the TLS upgrade the settings choose,
// and the login when the settings carry a user.
func connectSMTP(ctx context.Context, settings SMTPSettings) (*smtp.Client, error) {
	host, port, err := mailTarget(settings.Host, settings.Port, defaultSMTPPort, settings.AllowedHost)
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
	client, err := smtp.NewClient(connection, host)
	if err != nil {
		_ = connection.Close()
		return nil, fmt.Errorf("the SMTP server did not greet: %w", err)
	}

	// EHLO first; a server that only knows HELO gets one.
	if err := client.Hello(localHostname()); err != nil {
		_ = client.Close()
		return nil, fmt.Errorf("the SMTP server refused the greeting: %w", err)
	}
	encrypted := settings.Secure
	if !settings.Secure && !settings.DisableStartTLS {
		if supports, _ := client.Extension("STARTTLS"); supports {
			if err := client.StartTLS(tlsConfig(host, settings.InsecureCerts)); err != nil {
				_ = client.Close()
				return nil, fmt.Errorf("the SMTP server refused the STARTTLS upgrade: %w", err)
			}
			encrypted = true
		}
	}
	if err := smtpAuthenticate(client, host, encrypted, settings); err != nil {
		_ = client.Close()
		return nil, err
	}
	return client, nil
}

// smtpAuthenticate picks a mechanism from the server's EHLO answer. A
// credential with no user does not authenticate at all, which is the normal
// shape of an internal relay. Go's own smtp.PlainAuth refuses to send over a
// connection that is not TLS, so PLAIN is only offered after a handshake; a
// plain connection falls back to CRAM-MD5 (challenge-response) and then LOGIN,
// which is the order nodemailer tries and the only way a deliberately
// unencrypted relay with a user still works.
func smtpAuthenticate(client *smtp.Client, host string, encrypted bool, settings SMTPSettings) error {
	if strings.TrimSpace(settings.User) == "" && strings.TrimSpace(settings.Password) == "" {
		return nil
	}
	_, mechanisms := client.Extension("AUTH")
	offered := map[string]bool{}
	for _, mechanism := range strings.Fields(mechanisms) {
		offered[strings.ToUpper(mechanism)] = true
	}
	switch {
	case offered["PLAIN"] && encrypted:
		if err := client.Auth(smtp.PlainAuth("", settings.User, settings.Password, host)); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	case offered["CRAM-MD5"]:
		if err := client.Auth(smtp.CRAMMD5Auth(settings.User, settings.Password)); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	case offered["LOGIN"]:
		if err := client.Auth(newLoginAuth(settings.User, settings.Password)); err != nil {
			return fmt.Errorf("SMTP authentication failed: %w", err)
		}
	default:
		return fmt.Errorf("the SMTP server offers none of the authentication mechanisms this client speaks (PLAIN, CRAM-MD5, LOGIN)")
	}
	return nil
}

// loginAuth implements the AUTH LOGIN mechanism, which exchanges the user and
// the password as two base64 lines. The standard library ships PLAIN and
// CRAM-MD5 only, and a relay that offers nothing else is common enough that
// n8n's node supports it through nodemailer.
type loginAuth struct {
	user     string
	password string
}

func newLoginAuth(user, password string) smtp.Auth {
	return &loginAuth{user: user, password: password}
}

func (auth *loginAuth) Start(_ *smtp.ServerInfo) (string, []byte, error) {
	return "LOGIN", []byte(auth.user), nil
}

func (auth *loginAuth) Next(fromServer []byte, more bool) ([]byte, error) {
	if !more {
		return nil, nil
	}
	switch strings.ToLower(strings.TrimSpace(string(fromServer))) {
	case "username:":
		return []byte(auth.user), nil
	case "password:":
		return []byte(auth.password), nil
	}
	return nil, fmt.Errorf("the server sent an unexpected AUTH LOGIN challenge %q", string(fromServer))
}

// localHostname is what EHLO says about this client. localhost is what
// nodemailer sends when no name is configured, and a name the server can
// resolve is not something an email node can promise.
func localHostname() string { return "localhost" }
