package credentials

import (
	"net/url"
	"strings"
)

// This file decides where a credential may be sent when its author said
// nothing, and whether a credential could be sent anywhere at all.
//
// An empty AllowedDomains list used to mean "every host" for every type. For a
// generic header or bearer credential that is still what an empty list means:
// the type has no home, so the only scope it can have is the one its author
// typed. But an OpenAI key has exactly one legitimate destination, and leaving
// it unrestricted meant that anyone able to edit a workflow — an embedded guest
// editor, a narrowed agent token — could aim it at a host they control and read
// the key off the wire. So a type that knows its service's address declares it,
// and an empty list on that type means that address rather than everywhere.

// EffectiveDomains is the host scope actually enforced for this credential: the
// list its author saved, or, when that is empty, its type's default.
//
// It is computed on every read rather than written into existing rows, so a
// credential stored before its type had a default is confined the moment the
// server upgrades, with no migration to forget. A nil result means the
// credential may be sent to any host.
func (record Record) EffectiveDomains() []string {
	if len(record.AllowedDomains) > 0 {
		return record.AllowedDomains
	}
	return DefaultDomains(record)
}

// DefaultDomains is the scope a credential of this type carries when its author
// saved none: the type's fixed list, or the host of the URL one of its own
// fields names. Nil when the type has neither, or is not registered.
func DefaultDomains(record Record) []string {
	credentialType, found := Default().Get(record.Type)
	if !found {
		return nil
	}
	if len(credentialType.DefaultDomains) > 0 {
		return append([]string(nil), credentialType.DefaultDomains...)
	}
	if credentialType.DefaultDomainsFrom == "" {
		return nil
	}
	raw := strings.TrimSpace(record.Fields[credentialType.DefaultDomainsFrom])
	if raw == "" {
		// A row that never stored the field reads the field's declared
		// default, which is what the node that consumes it falls back to.
		for _, declared := range credentialType.Properties {
			if declared.Key == credentialType.DefaultDomainsFrom {
				raw, _ = declared.Default.(string)
			}
		}
	}
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Hostname() == "" {
		// No host to derive is not a licence to go anywhere: it is simply no
		// default, and the credential is as unscoped as an empty list makes it.
		return nil
	}
	return []string{strings.ToLower(parsed.Hostname())}
}

// ApplyDefaultDomains stores a type's fixed default scope on a credential saved
// with none, so the stored row — and the form that edits it — shows the hosts
// that bound it.
//
// Only a fixed list is materialised. A scope derived from a field, such as the
// host of a Bot API base URL, is left unstored on purpose: a stored copy would
// keep naming the old host after the owner moved the base URL, and the node
// would then be refused the server it is configured to call.
func ApplyDefaultDomains(record *Record) {
	if record == nil || len(record.AllowedDomains) > 0 {
		return
	}
	credentialType, found := Default().Get(record.Type)
	if !found || len(credentialType.DefaultDomains) == 0 {
		return
	}
	record.AllowedDomains = append([]string(nil), credentialType.DefaultDomains...)
}

// Unscoped reports whether this credential could be sent to a host of the
// sender's choosing: its secret travels on an outbound HTTP request whose URL a
// node decides, and neither its author nor its type named where that may be.
//
// It is the question the confinement of an embed session or a scoped API key
// asks before a document may attach the credential. Those callers may edit
// where a request goes, so a credential that would follow the request anywhere
// is, in their hands, a way to read the secret.
//
// A type the server does not know is read as unscoped: nothing says where it
// may go, and a pack credential that was uninstalled can come back.
func (record Record) Unscoped() bool {
	if credentialType, found := Default().Get(record.Type); found && credentialType.NeverSentOverHTTP {
		return false
	}
	return len(record.EffectiveDomains()) == 0
}
