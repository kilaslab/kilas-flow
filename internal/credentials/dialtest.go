package credentials

import (
	"context"
	"fmt"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/mail"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// DialTest declares a connection test for a credential whose service is not
// an HTTP endpoint: the test dials the host the credential names, through the
// instance's egress policy, and speaks enough of the wire protocol to prove
// the credential itself — a login for SMTP and IMAP, exactly what a node
// would do.
//
// It exists for the same reason TestRequest does: a probe should be data on
// the type, not a switch on the type ID. The database types reach their
// equivalent through internal/sqlnode's driver table; the mail types have no
// driver, so their probe is declared here and executed by RunDialTest.
type DialTest struct {
	// Protocol is the wire protocol the handshake speaks: "smtp" or "imap".
	Protocol string `json:"protocol"`
}

// RunDialTest probes a credential whose type declares a dial test. The
// credential's own AllowedDomains narrow the policy further, exactly as they
// do for an HTTP probe: a scope the owner saved is honoured even though the
// type itself is never sent over HTTP.
//
// The probe writes nothing: SMTP sends EHLO, AUTH and QUIT — no message is
// submitted; IMAP logs in and logs out — no mailbox is read. Proving the
// credential is the whole of the job, the same line internal/sqlnode.Test
// draws at "opens a connection and closes it again".
func RunDialTest(ctx context.Context, credentialType Type, record Record, fields map[string]string, policy safehttp.Policy) (string, error) {
	if credentialType.Dial == nil {
		return "", fmt.Errorf("this credential type has no dial test defined")
	}
	fields = credentialType.WithDefaults(fields)
	allowed := record.AllowsHost
	switch credentialType.Dial.Protocol {
	case "smtp":
		settings := mail.SMTPSettings{
			Host: strings.TrimSpace(fields["host"]), Port: strings.TrimSpace(fields["port"]),
			Secure: boolField(fields["secure"]), DisableStartTLS: boolField(fields["disableStartTls"]),
			User: fields["user"], Password: fields["password"],
			InsecureCerts: boolField(fields["allowUnauthorizedCerts"]),
			// The probe never submits a message; the recipient list stays
			// empty and Send refuses before the handshake rather than
			// naming one. Reach the handshake through an explicit ping so
			// no envelope is needed.
			Policy: policy, AllowedHost: allowed,
		}
		if err := mail.PingSMTP(ctx, settings); err != nil {
			return "", err
		}
	case "imap":
		client, err := mail.DialIMAP(ctx, mail.IMAPSettings{
			Host: strings.TrimSpace(fields["host"]), Port: strings.TrimSpace(fields["port"]),
			Secure: boolField(fields["secure"]), User: fields["user"], Password: fields["password"],
			InsecureCerts: boolField(fields["allowUnauthorizedCerts"]),
			Policy:        policy, AllowedHost: allowed,
		})
		if err != nil {
			return "", err
		}
		client.Close()
	default:
		return "", fmt.Errorf("credential type %q declares unsupported dial protocol %q", credentialType.ID, credentialType.Dial.Protocol)
	}
	return "the mail server accepted the login", nil
}

// boolField reads a boolean stored as a string, the way every credential
// field is stored.
func boolField(value string) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	}
	return false
}
