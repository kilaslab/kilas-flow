package webhook

import "net/http"

// Webhook responses are served on the instance's own origin — the same one as
// the dashboard, the API and every embed iframe — and their bodies are written
// by tenants: a Respond to Webhook node picks any body, headers and
// Content-Type, and a trigger's responseData acknowledgement is sent as
// text/html. Without a sandbox, a returned page's script ran as the instance,
// with its cookies and a same-origin view of its API (BUG-x2sxzt).
//
// The fix is a CSP sandbox without allow-same-origin: the browser gives the
// document an opaque origin, so to the instance it is no different from a page
// on any other site. The tenant's page itself keeps working — its script runs,
// its forms submit, its links open — just not as the instance.
const (
	// responsePolicy covers everything a workflow wrote. It restricts nothing
	// but the origin: a page an n8n author returns commonly loads a script or
	// a stylesheet from a CDN and posts a form, and all of that still works.
	// Popups escape the sandbox so a "continue to payment" link opens the real
	// destination rather than a sandboxed copy that cannot keep a session;
	// that grants nothing on this origin, because a popup on it is the real
	// application, which any other site can open too.
	responsePolicy = "sandbox allow-downloads allow-forms allow-modals allow-popups allow-popups-to-escape-sandbox allow-scripts"

	// pagePolicy covers the hosted form pages KilasFlow renders itself. They
	// carry no script and load nothing — one inline stylesheet, and a form
	// that posts back to its own address — so the sandbox grants only the
	// form submission and the fetch directives allow only the inline style.
	// Tenant-written labels are escaped before they reach that markup; this is
	// the second wall behind the escaping.
	pagePolicy = "default-src 'none'; style-src 'unsafe-inline'; base-uri 'none'; sandbox allow-forms"
)

// sandboxedWriter forces the sandbox onto whatever the handler writes.
//
// It applies at the moment the status line is written, after every header a
// workflow set has been copied, so no response path can forget it and a
// workflow-set Content-Security-Policy or X-Content-Type-Options cannot weaken
// it: the handler's own value replaces the tenant's rather than joining it.
//
// Every response gets it, JSON and problem documents included, not just the
// ones that look like HTML. The workflow chooses the Content-Type, so "is this
// HTML" is the tenant's answer and not the handler's — and SVG and XML render
// script as well as HTML does. A CSP on a response the browser does not render
// as a document is ignored, so fetch and API callers see no difference. The one
// cost is that a browser will not show a PDF inline under a sandbox; Respond to
// Webhook has no binary response, so nothing KilasFlow can send depends on it.
type sandboxedWriter struct {
	http.ResponseWriter
	written bool
}

func (w *sandboxedWriter) WriteHeader(status int) {
	if !w.written {
		harden(w.Header())
	}
	// An informational status is followed by the real one, which sends the
	// header map again; only a final status closes it.
	if status >= http.StatusOK {
		w.written = true
	}
	w.ResponseWriter.WriteHeader(status)
}

func (w *sandboxedWriter) Write(body []byte) (int, error) {
	if !w.written {
		harden(w.Header())
		w.written = true
	}
	return w.ResponseWriter.Write(body)
}

// Unwrap lets http.ResponseController reach the underlying writer.
func (w *sandboxedWriter) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// harden sets the response's security headers, replacing any a workflow set.
//
// The hosted-page policy is kept when it is the only policy present, because
// writePage sets it for KilasFlow's own pages. A workflow that sets that exact
// value gains nothing by it: the page policy is the stricter of the two.
func harden(header http.Header) {
	if policies := header.Values("Content-Security-Policy"); len(policies) != 1 || policies[0] != pagePolicy {
		header.Set("Content-Security-Policy", responsePolicy)
	}
	// The browser renders what the Content-Type says and nothing else: a body
	// sent with a vague or empty type is not second-guessed into a document
	// or a script.
	header.Set("X-Content-Type-Options", "nosniff")
}
