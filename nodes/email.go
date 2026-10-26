package nodes

// The email pair n8n's failure-alerting channel runs on: Send Email over
// SMTP, and the IMAP Email Trigger on the leased poll framework (FEAT-sz4ddp).
//
// The sending node speaks through internal/mail, which applies the
// deployment's outbound policy to the dial the way every HTTP request has it
// applied, upgrades with STARTTLS or connects with TLS per the credential,
// and builds the MIME message itself — text and HTML as alternatives when
// both are set, attachments from the item's binary properties. The parameter
// keys are n8n's Send Email v2 own (fromEmail, toEmail, emailFormat, text,
// html, options.fileAttachments), so an imported node's stored parameters
// carry across, and the legacy v1 spellings — ccEmail and bccEmail at the top
// level, attachments as a plain list — are read at run time the way a renamed
// parameter always is: both keys, the new one first.
//
// The trigger polls a mailbox with a UID watermark on the same leased cursor
// framework the Gmail and Google triggers use, so two replicas cannot emit
// one mail twice. It speaks the small IMAP client in internal/mail: select,
// search above the watermark, fetch without marking seen, then mark seen only
// when the node's action says to.
import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"mime"
	"mime/quotedprintable"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/mail"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/poller"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	EmailSendExecutorID     = "core.emailSend"
	EmailReadImapExecutorID = "core.emailReadImap"

	EmailSendNodeType     = "kilasflow.emailSend"
	EmailReadImapNodeType = "kilasflow.emailReadImap"

	// SMTPCredentialType and IMAPCredentialType are n8n's own credential IDs,
	// so an imported workflow that names `smtp` or `imap` finds a type of
	// exactly that name — the same rule every provider credential here
	// follows.
	SMTPCredentialType = "smtp"
	IMAPCredentialType = "imap"
)

// The IMAP trigger's formats, under n8n's values. n8n also offers `resolved`,
// which resolves the full mail into items with attachments as binary; this
// server's poll framework has no binary store, so the importer downgrades it
// to simple and the node offers only the two it runs.
const (
	imapFormatSimple = "simple"
	imapFormatRaw    = "raw"
)

// maxEmailAttachmentBytes bounds one attachment read into memory. A mail with
// a larger part is refused by name rather than buffered whole.
const maxEmailAttachmentBytes = 10 << 20

// imapPollBatch caps how many messages one tick fetches, the same budget
// n8n's trigger works to: a mailbox that received a thousand mails overnight
// drains over several ticks instead of holding the lease for all of them.
const imapPollBatch = 20

func emailSendNode() node.Definition {
	return node.Definition{
		Type:        EmailSendNodeType,
		Version:     workflow.V(1),
		DisplayName: "Send Email",
		Description: "Sends an email over SMTP: text or HTML, with attachments from the workflow's binary data.",
		Category:    "Communication",
		Group:       []node.NodeGroup{node.GroupOutput},
		Icon:        &node.NodeIcon{Light: "emailSend"},
		IconLight:   emailArtwork("send"),
		IconColor:   "#0f766e",
		Subtitle:    "{{ $parameter.emailFormat }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: SMTPCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{
				Key: "fromEmail", Label: "From Email", Kind: node.PropertyString, Required: true,
				Description: "The sender's address, optionally with a name: Ops <ops@example.test>.",
			},
			{
				Key: "toEmail", Label: "To Email", Kind: node.PropertyString, Required: true,
				Description: "The recipient's address. Several can be comma-separated.",
			},
			{Key: "subject", Label: "Subject", Kind: node.PropertyString},
			{
				Key: "emailFormat", Label: "Email Format", Kind: node.PropertyOptions, Default: "html",
				Options: []node.PropertyOption{
					{Label: "Text", Value: "text"},
					{Label: "HTML", Value: "html"},
					{Label: "Both", Value: "both"},
				},
				Description: "Send the message as plain text, as HTML, or as both — the recipient's client picks.",
			},
			{
				Key: "text", Label: "Text", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 5},
				Description: "The plain-text body. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "emailFormat", Equals: "text"}, {Key: "emailFormat", Equals: "both"}},
			},
			{
				Key: "html", Label: "HTML", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Rows: 5},
				Description: "The HTML body. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "emailFormat", Equals: "html"}, {Key: "emailFormat", Equals: "both"}},
			},
			{
				Key: "options", Label: "Options", Kind: node.PropertyCollection,
				Fields: []node.PropertyDefinition{
					{Key: "ccEmail", Label: "CC Email", Kind: node.PropertyString, Description: "Carbon-copy recipients, comma-separated."},
					{Key: "bccEmail", Label: "BCC Email", Kind: node.PropertyString, Description: "Blind carbon-copy recipients, comma-separated. They receive the mail but no header names them."},
					{Key: "replyTo", Label: "Reply To", Kind: node.PropertyString, Description: "The address replies go to."},
					{
						Key: "attachments", Label: "Attachments (Inline)", Kind: node.PropertyString,
						Description: "Binary properties to embed in the body, comma-separated. Reference them in the HTML via cid:propertyName.",
					},
					{
						Key: "fileAttachments", Label: "Attachments (File)", Kind: node.PropertyString,
						Description: "Binary properties to attach as files, comma-separated.",
					},
					{
						Key: "allowUnauthorizedCerts", Label: "Ignore SSL Issues (Insecure)", Kind: node.PropertyBoolean, Default: false,
						Description: "Connect even if the server's certificate cannot be verified.",
					},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     EmailSendExecutorID,
		Validate:       validateEmailSend,
	}
}

func emailReadImapNode() node.Definition {
	return node.Definition{
		Type:        EmailReadImapNodeType,
		Version:     workflow.V(1),
		DisplayName: "Email Trigger (IMAP)",
		Description: "Starts a workflow when a mailbox receives a message. Polls with a leased cursor and a UID watermark, so two replicas cannot emit one mail twice.",
		Category:    "Triggers",
		Group:       []node.NodeGroup{node.GroupTrigger},
		Icon:        &node.NodeIcon{Light: "emailInbox"},
		IconLight:   emailArtwork("inbox"),
		IconColor:   "#16a34a",
		Outputs:     mainOutput(),
		Credentials: []node.CredentialRequirement{{Type: IMAPCredentialType, Required: true}},
		Parameters: []node.PropertyDefinition{
			{Key: "mailbox", Label: "Mailbox Name", Kind: node.PropertyString, Default: "INBOX"},
			{
				Key: "postProcessAction", Label: "Action", Kind: node.PropertyOptions, Default: "read",
				Options: []node.PropertyOption{
					{Label: "Mark as Read", Value: "read"},
					{Label: "Nothing", Value: "nothing"},
				},
				Description: "What to do after a message is emitted. Nothing means an unread message is emitted again on the next poll.",
			},
			{
				Key: "format", Label: "Format", Kind: node.PropertyOptions, Default: imapFormatSimple,
				Options: []node.PropertyOption{
					{Label: "Simple", Value: imapFormatSimple},
					{Label: "RAW", Value: imapFormatRaw},
				},
				Description: "Simple emits headers, text and HTML; RAW emits the whole message base64url-encoded.",
			},
			{Key: "pollTimes", Label: "Poll Times", Kind: node.PropertyCollection},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     EmailReadImapExecutorID,
	}
}

func validateEmailSend(n workflow.Node) error {
	if strings.TrimSpace(n.Credentials[SMTPCredentialType]) == "" {
		return fmt.Errorf("a %s credential holding the server and login is required", SMTPCredentialType)
	}
	for _, key := range []string{"fromEmail", "toEmail"} {
		if strings.TrimSpace(statementText(n.Parameters, key)) == "" && !expression.IsExpression(n.Parameters[key]) {
			return fmt.Errorf("%s is required", key)
		}
	}
	switch format := statementText(n.Parameters, "emailFormat"); format {
	case "", "text", "html", "both":
	default:
		return fmt.Errorf("emailFormat %q is not supported", format)
	}
	// A line break in a header value would start a header the author never
	// chose — a Bcc to somebody else. The values can come from a webhook.
	for _, key := range []string{"fromEmail", "toEmail", "subject"} {
		if value, isText := n.Parameters[key].(string); isText && !expression.IsExpression(value) &&
			strings.ContainsAny(value, "\r\n") {
			return fmt.Errorf("%s contains a line break, which would start another mail header", key)
		}
	}
	for _, key := range []string{"ccEmail", "bccEmail", "replyTo"} {
		for _, value := range []any{n.Parameters[key], mapValue(n.Parameters["options"])[key]} {
			if text, isText := value.(string); isText && !expression.IsExpression(text) &&
				strings.ContainsAny(text, "\r\n") {
				return fmt.Errorf("%s contains a line break, which would start another mail header", key)
			}
		}
	}
	return nil
}

// EmailSendExecutor sends one message per item through internal/mail.
type EmailSendExecutor struct {
	policy safehttp.Policy
}

// NewEmailSendExecutor builds the node from the deployment's outbound policy.
func NewEmailSendExecutor(policy safehttp.Policy) *EmailSendExecutor {
	return &EmailSendExecutor{policy: policy}
}

func (executor *EmailSendExecutor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	credential, err := resolveMailCredential(ctx, ir, request, SMTPCredentialType)
	if err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{}}
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		resolved, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		produced, err := executor.sendItem(ctx, ir, resolved, item, request, credential)
		if err != nil {
			return nil, err
		}
		output = append(output, produced)
	}
	return workflow.NodeOutput{output}, nil
}

func (executor *EmailSendExecutor) sendItem(ctx context.Context, ir workflow.IRNode, resolved map[string]any, item workflow.Item, request engine.Request, credential engine.Credential) (workflow.Item, error) {
	from := strings.TrimSpace(textValue(resolved["fromEmail"], ""))
	options := mapValue(resolved["options"])
	to := emailAddresses(textValue(resolved["toEmail"], ""))
	// n8n's v1 kept CC and BCC at the top level; v2 moved them into options.
	// Both are read, the newer spelling first.
	cc := append(emailAddresses(textValue(resolved["ccEmail"], "")), emailAddresses(textValue(options["ccEmail"], ""))...)
	bcc := append(emailAddresses(textValue(resolved["bccEmail"], "")), emailAddresses(textValue(options["bccEmail"], ""))...)
	replyTo := strings.TrimSpace(textValue(options["replyTo"], ""))
	subject := textValue(resolved["subject"], "")
	textBody := textValue(resolved["text"], "")
	htmlBody := textValue(resolved["html"], "")

	if from == "" {
		return workflow.Item{}, fmt.Errorf("node %q: fromEmail is required", ir.Name)
	}
	if len(to) == 0 {
		return workflow.Item{}, fmt.Errorf("node %q: toEmail is required", ir.Name)
	}
	if strings.TrimSpace(textBody) == "" && strings.TrimSpace(htmlBody) == "" {
		return workflow.Item{}, fmt.Errorf("node %q: the message has no body; set text or html", ir.Name)
	}
	for _, header := range append(
		[]struct{ key, value string }{{"fromEmail", from}, {"subject", subject}, {"replyTo", replyTo}},
		recipientsWithNames(to, cc, bcc)...) {
		if strings.ContainsAny(header.value, "\r\n") {
			return workflow.Item{}, fmt.Errorf("node %q: %s contains a line break, which would start another mail header", ir.Name, header.key)
		}
	}

	attachments, err := emailAttachments(ir, item, request, resolved, options)
	if err != nil {
		return workflow.Item{}, err
	}
	recipients := append(append([]string{}, to...), append(cc, bcc...)...)
	messageID := newMessageID(from)
	message, err := buildEmailMessage(emailMessage{
		from: from, to: to, cc: cc, replyTo: replyTo,
		subject: subject, text: textBody, html: htmlBody,
		messageID: messageID, attachments: attachments,
	})
	if err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if err := mail.Send(ctx, mail.SMTPSettings{
		Host: credential.Fields["host"], Port: credential.Fields["port"],
		Secure: credentialBool(credential.Fields["secure"], true),
		User:   credential.Fields["user"], Password: credential.Fields["password"],
		DisableStartTLS: credentialBool(credential.Fields["disableStartTls"], false),
		InsecureCerts:   credentialBool(textValue(options["allowUnauthorizedCerts"], ""), false),
		From:            envelopeAddress(from), Recipients: recipients,
		Message: message, Policy: executor.policy, AllowedHost: credential.AllowsHost,
	}); err != nil {
		return workflow.Item{}, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return workflow.Item{JSON: map[string]any{
		"accepted":  toAny(recipients),
		"rejected":  []any{},
		"messageId": messageID,
		"envelope":  map[string]any{"from": envelopeAddress(from), "to": toAny(recipients)},
		"response":  "accepted by the server",
	}, Paired: item.Paired}, nil
}

func recipientsWithNames(groups ...[]string) []struct{ key, value string } {
	out := []struct{ key, value string }{}
	for _, group := range groups {
		for _, address := range group {
			out = append(out, struct{ key, value string }{"a recipient", address})
		}
	}
	return out
}

func toAny(values []string) []any {
	out := make([]any, 0, len(values))
	for _, value := range values {
		out = append(out, value)
	}
	return out
}

// resolveMailCredential reads and checks the node's own mail credential: type
// checked, fields read, and never carried anywhere but into the mail client.
func resolveMailCredential(ctx context.Context, ir workflow.IRNode, request engine.Request, credentialType string) (engine.Credential, error) {
	if request.Credentials == nil {
		return engine.Credential{}, fmt.Errorf("node %q: credentials are not available in this runtime", ir.Name)
	}
	resolved, found, err := request.ResolveAttachedCredential(ctx, ir, credentialType)
	if err != nil {
		return engine.Credential{}, err
	}
	if !found {
		return engine.Credential{}, fmt.Errorf("node %q: a %s credential holding the server and login is required", ir.Name, credentialType)
	}
	if resolved.Type != credentialType {
		return engine.Credential{}, fmt.Errorf("node %q: credential %q is a %s credential, which this node cannot authenticate with",
			ir.Name, resolved.Name, resolved.Type)
	}
	if strings.TrimSpace(resolved.Fields["host"]) == "" {
		return engine.Credential{}, fmt.Errorf("node %q: credential %q names no host", ir.Name, resolved.Name)
	}
	return resolved, nil
}

// credentialBool reads a boolean the credential stored as a string, defaulting
// when the field is absent — the SMTP and IMAP credentials keep their TLS
// switches this way.
func credentialBool(value string, fallback bool) bool {
	switch strings.ToLower(strings.TrimSpace(value)) {
	case "true", "1", "yes", "on":
		return true
	case "false", "0", "no", "off":
		return false
	}
	return fallback
}

// emailAddresses splits a comma-separated recipient list, dropping empties.
func emailAddresses(value string) []string {
	addresses := []string{}
	for _, entry := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			addresses = append(addresses, trimmed)
		}
	}
	return addresses
}

// envelopeAddress extracts the bare address from a value that may carry a
// display name: "Ops <ops@example.test>" → "ops@example.test".
func envelopeAddress(value string) string {
	if open := strings.LastIndex(value, "<"); open >= 0 {
		if close := strings.Index(value[open:], ">"); close > 0 {
			return strings.TrimSpace(value[open+1 : open+close])
		}
	}
	return strings.TrimSpace(value)
}

// newMessageID mints one RFC 5322 message id from random bytes, under the
// sender's own domain — the shape every mail client expects.
func newMessageID(from string) string {
	buffer := make([]byte, 16)
	_, _ = rand.Read(buffer)
	domain := "kilasflow.local"
	if address := envelopeAddress(from); strings.Contains(address, "@") {
		if candidate := strings.ToLower(strings.TrimSpace(address[strings.LastIndex(address, "@")+1:])); candidate != "" {
			domain = candidate
		}
	}
	return "<" + hex.EncodeToString(buffer) + "@" + domain + ">"
}

// emailAttachment is one binary property dressed as a MIME part.
type emailAttachment struct {
	// contentID, when set, makes the part inline and referenceable from the
	// HTML as cid:<contentID>.
	contentID   string
	fileName    string
	mediaType   string
	contents    []byte
	disposition string
}

// emailAttachments reads the binary properties the node names: v2's
// options.fileAttachments (files) and options.attachments (inline), plus the
// v1 top-level `attachments` list, which named files.
func emailAttachments(ir workflow.IRNode, item workflow.Item, request engine.Request, resolved, options map[string]any) ([]emailAttachment, error) {
	fileProperties := append(
		splitBinaryList(textValue(options["fileAttachments"], "")),
		splitBinaryList(textValue(resolved["attachments"], ""))...,
	)
	inlineProperties := splitBinaryList(textValue(options["attachments"], ""))
	if len(fileProperties) == 0 && len(inlineProperties) == 0 {
		return nil, nil
	}
	attachments := []emailAttachment{}
	read := func(property string, contentID string, disposition string) error {
		reference, present := item.Binary[property]
		if !present {
			return fmt.Errorf("node %q: reads binary %q, which the item does not carry", ir.Name, property)
		}
		if request.Binaries == nil {
			return fmt.Errorf("node %q: reads binary %q, but this run has no binary store", ir.Name, property)
		}
		body, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return fmt.Errorf("node %q: read attachment %q: %w", ir.Name, property, err)
		}
		defer body.Close()
		contents, readErr := io.ReadAll(io.LimitReader(body, maxEmailAttachmentBytes+1))
		if readErr != nil {
			return fmt.Errorf("node %q: read attachment %q: %w", ir.Name, property, readErr)
		}
		if len(contents) > maxEmailAttachmentBytes {
			return fmt.Errorf("node %q: reads binary %q, which is larger than the %d MiB one attachment may be",
				ir.Name, property, maxEmailAttachmentBytes>>20)
		}
		name := strings.TrimSpace(reference.FileName)
		if name == "" {
			name = property
		}
		// A name holding a line break or a quote would break the part's own
		// headers; the property key is the honest fallback.
		if strings.ContainsAny(name, "\r\n\"") {
			name = property
		}
		media := reference.MediaType
		if media == "" {
			media = "application/octet-stream"
		}
		attachments = append(attachments, emailAttachment{
			contentID: contentID, fileName: name, mediaType: media,
			contents: contents, disposition: disposition,
		})
		return nil
	}
	for _, property := range fileProperties {
		if err := read(property, "", "attachment"); err != nil {
			return nil, err
		}
	}
	for _, property := range inlineProperties {
		if err := read(property, property, "inline"); err != nil {
			return nil, err
		}
	}
	return attachments, nil
}

// splitBinaryList splits a comma-separated list of binary property names.
func splitBinaryList(value string) []string {
	properties := []string{}
	for _, entry := range strings.Split(value, ",") {
		if trimmed := strings.TrimSpace(entry); trimmed != "" {
			properties = append(properties, trimmed)
		}
	}
	return properties
}

// emailMessage is everything one outgoing mail carries.
type emailMessage struct {
	from, replyTo, subject string
	to, cc                 []string
	text, html             string
	messageID              string
	attachments            []emailAttachment
}

// buildEmailMessage assembles the RFC 5322 message: headers, then the body as
// alternatives when both text and HTML are set, then attachments — base64
// throughout, so nothing depends on the transport being 8-bit clean.
func buildEmailMessage(message emailMessage) ([]byte, error) {
	var out bytes.Buffer
	write := func(format string, arguments ...any) {
		_, _ = fmt.Fprintf(&out, format+"\r\n", arguments...)
	}
	write("From: %s", message.from)
	write("To: %s", strings.Join(message.to, ", "))
	if len(message.cc) > 0 {
		write("Cc: %s", strings.Join(message.cc, ", "))
	}
	// Bcc recipients are on the envelope only: a header naming them is the
	// leak the field exists to prevent.
	if message.replyTo != "" {
		write("Reply-To: %s", message.replyTo)
	}
	write("Subject: %s", mime.QEncoding.Encode("utf-8", message.subject))
	write("Date: %s", time.Now().Format(time.RFC1123Z))
	write("Message-ID: %s", message.messageID)
	write("MIME-Version: 1.0")

	bodyWriter := func() error {
		switch {
		case strings.TrimSpace(message.text) == "":
			write("Content-Type: text/html; charset=utf-8")
			write("Content-Transfer-Encoding: base64")
			write("")
			return writeBase64(&out, []byte(message.html))
		case strings.TrimSpace(message.html) == "":
			write("Content-Type: text/plain; charset=utf-8")
			write("Content-Transfer-Encoding: base64")
			write("")
			return writeBase64(&out, []byte(message.text))
		default:
			boundary := boundaryOf("alt")
			write("Content-Type: multipart/alternative; boundary=%q", boundary)
			write("")
			write("--%s", boundary)
			write("Content-Type: text/plain; charset=utf-8")
			write("Content-Transfer-Encoding: base64")
			write("")
			if err := writeBase64(&out, []byte(message.text)); err != nil {
				return err
			}
			write("--%s", boundary)
			write("Content-Type: text/html; charset=utf-8")
			write("Content-Transfer-Encoding: base64")
			write("")
			if err := writeBase64(&out, []byte(message.html)); err != nil {
				return err
			}
			write("--%s--", boundary)
			return nil
		}
	}

	if len(message.attachments) == 0 {
		if err := bodyWriter(); err != nil {
			return nil, err
		}
		return out.Bytes(), nil
	}
	boundary := boundaryOf("mixed")
	write("Content-Type: multipart/mixed; boundary=%q", boundary)
	write("")
	write("--%s", boundary)
	if err := bodyWriter(); err != nil {
		return nil, err
	}
	for _, attachment := range message.attachments {
		write("--%s", boundary)
		write("Content-Type: %s; name=%q", attachment.mediaType, attachment.fileName)
		write("Content-Transfer-Encoding: base64")
		if attachment.contentID != "" {
			write("Content-ID: <%s>", attachment.contentID)
		}
		write("Content-Disposition: %s; filename=%q", attachment.disposition, attachment.fileName)
		write("")
		if err := writeBase64(&out, attachment.contents); err != nil {
			return nil, err
		}
	}
	write("--%s--", boundary)
	return out.Bytes(), nil
}

// writeBase64 writes bytes wrapped at 76 characters, the line length every
// mail transport honours.
func writeBase64(out *bytes.Buffer, contents []byte) error {
	encoded := base64.StdEncoding.EncodeToString(contents)
	for len(encoded) > 76 {
		_, err := out.WriteString(encoded[:76] + "\r\n")
		if err != nil {
			return err
		}
		encoded = encoded[76:]
	}
	_, err := out.WriteString(encoded + "\r\n")
	return err
}

// boundaryOf mints a MIME boundary no body is going to contain.
func boundaryOf(kind string) string {
	buffer := make([]byte, 12)
	_, _ = rand.Read(buffer)
	return "----kilasflow-" + kind + "-" + hex.EncodeToString(buffer)
}

// EmailReadImapTriggerExecutor emits the polled message on a manual run.
type EmailReadImapTriggerExecutor struct{}

func (EmailReadImapTriggerExecutor) Execute(_ context.Context, _ workflow.IRNode, _ workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	item := request.Input
	if item.JSON == nil {
		item.JSON = map[string]any{}
	}
	return workflow.NodeOutput{{item}}, nil
}

// EmailImapPoller fetches new messages above the UID watermark.
type EmailImapPoller struct {
	policy safehttp.Policy
}

// NewEmailImapPoller builds the IMAP trigger's poll handler.
func NewEmailImapPoller(policy safehttp.Policy) *EmailImapPoller {
	return &EmailImapPoller{policy: policy}
}

// imapPollCursor is the leased watermark: the mailbox's UIDVALIDITY and the
// highest UID already emitted. A mailbox recreated behind the same name gets
// a new validity, which resets the watermark rather than comparing UIDs from
// two different generations.
type imapPollCursor struct {
	UIDValidity uint32 `json:"uidValidity"`
	UID         uint32 `json:"uid"`
}

func (pollerHandler *EmailImapPoller) Poll(ctx context.Context, claim poller.Claim) ([]workflow.Item, string, error) {
	ir := irFromNode(claim.Node)
	credential, err := resolveMailCredential(ctx, ir, claim.Request, IMAPCredentialType)
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	mailbox := strings.TrimSpace(textValue(claim.Node.Parameters["mailbox"], "INBOX"))
	if mailbox == "" {
		mailbox = "INBOX"
	}
	action := strings.ToLower(strings.TrimSpace(textValue(claim.Node.Parameters["postProcessAction"], "read")))
	format := strings.ToLower(strings.TrimSpace(textValue(claim.Node.Parameters["format"], imapFormatSimple)))
	if format == "" {
		format = imapFormatSimple
	}

	client, err := mail.DialIMAP(ctx, mail.IMAPSettings{
		Host: credential.Fields["host"], Port: credential.Fields["port"],
		Secure: credentialBool(credential.Fields["secure"], true),
		User:   credential.Fields["user"], Password: credential.Fields["password"],
		InsecureCerts: credentialBool(credential.Fields["allowUnauthorizedCerts"], false),
		Policy:        pollerHandler.policy, AllowedHost: credential.AllowsHost,
	})
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	defer client.Close()

	info, err := client.Select(mailbox)
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	var cursor imapPollCursor
	if strings.TrimSpace(claim.Cursor.Cursor) != "" {
		_ = json.Unmarshal([]byte(claim.Cursor.Cursor), &cursor)
	}
	// First poll, or a mailbox that was recreated: everything that exists now
	// is the baseline, and the next poll starts from its top.
	if cursor.UIDValidity == 0 || info.UIDValidity != cursor.UIDValidity {
		encoded, _ := json.Marshal(imapPollCursor{UIDValidity: info.UIDValidity, UID: highestBaseline(info)})
		return nil, string(encoded), nil
	}

	uids, err := client.SearchUIDsAfter(cursor.UID)
	if err != nil {
		return nil, claim.Cursor.Cursor, err
	}
	if len(uids) > imapPollBatch {
		uids = uids[:imapPollBatch]
	}
	items := make([]workflow.Item, 0, len(uids))
	highest := cursor.UID
	for _, uid := range uids {
		message, err := client.FetchMessage(uid)
		if err != nil {
			return nil, claim.Cursor.Cursor, err
		}
		if uid > highest {
			highest = uid
		}
		items = append(items, imapMessageItem(message, format))
		if action == "read" {
			if err := client.MarkSeen(uid); err != nil {
				return nil, claim.Cursor.Cursor, err
			}
		}
	}
	// A tick that drained everything the search found may still sit below the
	// mailbox's top — messages deleted before this poll, say — so the
	// watermark moves to UIDNEXT minus one and the next poll reads only what
	// arrives after now. A capped tick keeps the UID it reached, so the rest
	// are fetched next time.
	if len(uids) < imapPollBatch && info.UIDNext > 1 && info.UIDNext-1 > highest {
		highest = info.UIDNext - 1
	}
	encoded, _ := json.Marshal(imapPollCursor{UIDValidity: cursor.UIDValidity, UID: highest})
	return items, string(encoded), nil
}

// highestBaseline is the UID a first poll watermarks: UIDNEXT minus one, the
// newest message the mailbox already holds, so activation emits only mail
// that arrives afterwards.
func highestBaseline(info mail.MailboxInfo) uint32 {
	if info.UIDNext > 1 {
		return info.UIDNext - 1
	}
	return 0
}

// imapMessageItem shapes one message the way n8n's trigger does: the simple
// format keeps the mail's own headers lowercased under metadata with
// subject/from/to/cc/date promoted to the top level, plus the decoded text
// and HTML bodies and the UID; the raw format base64url-encodes the whole
// message.
func imapMessageItem(message mail.IMAPMessage, format string) workflow.Item {
	if format == imapFormatRaw {
		return workflow.Item{JSON: map[string]any{
			"raw":        base64.URLEncoding.EncodeToString(message.Body),
			"attributes": map[string]any{"uid": float64(message.UID)},
		}}
	}
	parsed := parseEmailMessage(message.Body)
	json := map[string]any{
		"textPlain":  parsed.textPlain,
		"textHtml":   parsed.textHtml,
		"metadata":   parsed.metadata,
		"attributes": map[string]any{"uid": float64(message.UID)},
	}
	for _, key := range []string{"subject", "from", "to", "cc", "date"} {
		if value, present := parsed.metadata[key]; present {
			json[key] = value
		}
	}
	return workflow.Item{JSON: json}
}

// parsedEmailMessage is a decoded RFC 822 message: its headers (lowercased,
// first value, encoded words decoded) and its text and HTML bodies.
type parsedEmailMessage struct {
	metadata  map[string]any
	textPlain string
	textHtml  string
}

// parseEmailMessage walks one message's MIME structure far enough to read its
// bodies. Attachments are not extracted: the poll framework has no binary
// store to put them in, and the ticket's slice is the trigger's text output.
func parseEmailMessage(raw []byte) parsedEmailMessage {
	headerBlock, body := splitHeaders(raw)
	headers := readEmailHeaders(headerBlock)
	parsed := parsedEmailMessage{metadata: headers}
	contentType, parameters, _ := mime.ParseMediaType(headerValue(headerBlock, "content-type"))
	collectBodies(body, contentType, parameters, headerValue(headerBlock, "content-transfer-encoding"), &parsed)
	if parsed.textPlain == "" {
		parsed.textPlain = strings.TrimSpace(parsed.textHtml)
	}
	return parsed
}

// emailHeader is one unfolded header line: a key and its value.
type emailHeader struct {
	key   string
	value string
}

// splitHeaders divides a message at its blank line, unfolding continuation
// lines — a header wrapped over several lines is one header, and reading it
// as five would both mis-key and mis-value the message.
func splitHeaders(raw []byte) ([]emailHeader, []byte) {
	text := string(raw)
	if index := strings.Index(text, "\r\n\r\n"); index >= 0 {
		return parseEmailHeaders(text[:index]), []byte(text[index+4:])
	}
	if index := strings.Index(text, "\n\n"); index >= 0 {
		return parseEmailHeaders(text[:index]), []byte(text[index+2:])
	}
	return parseEmailHeaders(text), nil
}

func parseEmailHeaders(block string) []emailHeader {
	headers := []emailHeader{}
	for _, line := range strings.Split(block, "\n") {
		line = strings.TrimRight(line, "\r")
		if line == "" {
			continue
		}
		if line[0] == ' ' || line[0] == '\t' {
			if len(headers) > 0 {
				headers[len(headers)-1].value += " " + strings.TrimSpace(line)
			}
			continue
		}
		key, value, found := strings.Cut(line, ":")
		if !found {
			continue
		}
		headers = append(headers, emailHeader{key: strings.TrimSpace(key), value: strings.TrimSpace(value)})
	}
	return headers
}

// readEmailHeaders renders the header block the way n8n's trigger reports
// it: keys lowercased, first value, RFC 2047 encoded words decoded.
func readEmailHeaders(headers []emailHeader) map[string]any {
	values := map[string]any{}
	for _, header := range headers {
		key := strings.ToLower(header.key)
		if _, seen := values[key]; seen {
			continue
		}
		values[key] = decodeEmailHeader(header.value)
	}
	return values
}

// headerValue finds one header's value by name, case-insensitively.
func headerValue(headers []emailHeader, name string) string {
	for _, header := range headers {
		if strings.EqualFold(header.key, name) {
			return header.value
		}
	}
	return ""
}

// decodeEmailHeader decodes RFC 2047 encoded words on a best-effort basis: an
// undecodable value crosses as itself rather than costing the message.
func decodeEmailHeader(value string) string {
	decoded, err := new(mime.WordDecoder).DecodeHeader(value)
	if err != nil {
		return value
	}
	return decoded
}

// collectBodies walks a MIME part: a multipart part recurses into its
// children, a text part decodes into the body its subtype names, and anything
// else is an attachment this slice leaves alone.
func collectBodies(body []byte, contentType string, parameters map[string]string, transferEncoding string, parsed *parsedEmailMessage) {
	if contentType == "" {
		contentType = "text/plain"
	}
	if media, _, isMultipart := strings.Cut(contentType, "/"); isMultipart && media == "multipart" {
		boundary := parameters["boundary"]
		if boundary == "" {
			return
		}
		for _, part := range splitMultipart(body, boundary) {
			partHeaders, partBody := splitHeaders(part)
			partType, partParameters, _ := mime.ParseMediaType(headerValue(partHeaders, "content-type"))
			if partType == "" {
				partType = "text/plain"
			}
			if nestedMedia, _, isNested := strings.Cut(partType, "/"); isNested && nestedMedia == "multipart" {
				collectBodies(partBody, partType, partParameters, headerValue(partHeaders, "content-transfer-encoding"), parsed)
				continue
			}
			if partMedia, _, isText := strings.Cut(partType, "/"); isText && partMedia == "text" {
				decoded := decodeTransfer(partBody, headerValue(partHeaders, "content-transfer-encoding"))
				switch strings.ToLower(partType) {
				case "text/plain":
					if parsed.textPlain == "" {
						parsed.textPlain = decoded
					}
				case "text/html":
					if parsed.textHtml == "" {
						parsed.textHtml = decoded
					}
				}
			}
		}
		return
	}
	if media, _, isText := strings.Cut(contentType, "/"); isText && media == "text" {
		decoded := decodeTransfer(body, transferEncoding)
		switch strings.ToLower(contentType) {
		case "text/plain":
			parsed.textPlain = decoded
		case "text/html":
			parsed.textHtml = decoded
		}
	}
}

// splitMultipart divides a multipart body on its boundary, dropping the
// preamble and the epilogue.
func splitMultipart(body []byte, boundary string) [][]byte {
	delimiter := []byte("--" + boundary)
	segments := [][]byte{}
	split := bytes.Split(body, delimiter)
	for index, segment := range split {
		if index == 0 {
			continue
		}
		segment = bytes.TrimPrefix(segment, []byte("\r\n"))
		if bytes.HasPrefix(segment, []byte("--")) {
			break
		}
		segment = bytes.TrimSuffix(segment, []byte("\r\n"))
		segments = append(segments, segment)
	}
	return segments
}

// decodeTransfer decodes one part's body by its transfer encoding, falling
// back to the bytes as written for anything it cannot decode.
func decodeTransfer(body []byte, encoding string) string {
	switch strings.ToLower(strings.TrimSpace(encoding)) {
	case "base64":
		decoded, err := base64.StdEncoding.DecodeString(strings.Map(func(r rune) rune {
			if r == '\r' || r == '\n' || r == ' ' || r == '\t' {
				return -1
			}
			return r
		}, string(body)))
		if err != nil {
			return string(body)
		}
		return string(decoded)
	case "quoted-printable":
		decoded, err := io.ReadAll(quotedprintable.NewReader(bytes.NewReader(body)))
		if err != nil {
			return string(body)
		}
		return string(decoded)
	default:
		return string(body)
	}
}

// emailArtwork is the small monochrome mark each email node ships as its own
// artwork, in the stroke idiom the editor's builtin glyphs draw in. No
// shipped glyph is free — mail belongs to Gmail, send to Telegram — so each
// email node draws its own, served by the icon route without an editor
// change.
func emailArtwork(key string) *node.IconAsset {
	marks := map[string]string{
		// An envelope leaving: the flap closed, an arrow out.
		"send": `<rect x="2" y="4" width="20" height="16" rx="2"/><path d="m2 7 10 6 10-6"/><path d="M18 15h4"/><path d="m20 13 2 2-2 2"/>`,
		// An envelope arriving: the flap open, a signal above it.
		"inbox": `<path d="m2 13 5-8h10l5 8"/><path d="M2 13h5l2 4h6l2-4h5"/><path d="M12 2v3"/><path d="m10.5 4.5 1.5-1.5 1.5 1.5"/>`,
	}
	body, drawn := marks[key]
	if !drawn {
		return nil
	}
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes:     []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` + body + `</svg>`),
	}
}
