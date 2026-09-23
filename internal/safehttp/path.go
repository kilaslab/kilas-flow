package safehttp

import "net/url"

// PathSegment writes a value as one segment of a URL path.
//
// Escaping is the point. The value is data — a chat id a workflow author typed,
// a session a tenant named — and the path around it is the pack's. Substituted
// raw, a slash or a question mark in it silently changes which endpoint a
// request reaches, and that request usually carries a credential. Every place
// that writes data into an outbound path goes through here, so they cannot
// disagree about what is escaped.
func PathSegment(value string) string {
	return url.PathEscape(value)
}
