package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// maxErrorBodyBytes bounds the non-JSON error body the CLI keeps, so a proxy's
// HTML error page cannot flood an agent's context.
const maxErrorBodyBytes = 4096

// skillsUsedHeader is the wire name for the skills a caller consulted before a
// write. It is written out here rather than imported because the CLI depends on
// no internal package: the header is a contract with the server, and the server
// names it on its own side of the same contract.
const skillsUsedHeader = "X-KilasFlow-Skills-Used"

// Client talks to one server. Every verb reaches the API through it, and it is
// the only place a token is attached to a request, so it is also the only
// place that has to be careful about printing one.
type Client struct {
	BaseURL string
	Token   string
	HTTP    *http.Client
	// Verbose receives the request trace when --verbose is passed. It is
	// always stderr: stdout carries the envelope and nothing else.
	Verbose io.Writer
	// Now is injectable so the envelope's duration is testable.
	Now func() time.Time
	// SkillsUsed names the skills an agent consulted to make a write. It is
	// sent on mutating requests only, as X-KilasFlow-Skills-Used, and the
	// server records it on the revision the call creates: it is how an
	// installation learns which skills actually get used.
	SkillsUsed []string
	// operations is the served operation index, read once per client. A client
	// is one invocation, so two invocations never share an index and a server
	// upgraded in between is never described by a stale copy.
	operations map[string]Operation
	// authEnabled is read from the same document as operations, at the same
	// time: whether the OpenAPI document declares a root `security`
	// requirement, which server.go sets only when auth is on. It is valid
	// exactly when operations is non-nil.
	authEnabled bool
}

// Response is the successful half of an HTTP round trip.
type Response struct {
	Status      int
	Header      http.Header
	Body        []byte
	ContentType string
}

// now returns the clock time, defaulting to the real clock.
func (c *Client) now() time.Time {
	if c.Now != nil {
		return c.Now()
	}

	return time.Now()
}

// httpClient returns the configured client, defaulting to the shared one.
func (c *Client) httpClient() *http.Client {
	if c.HTTP != nil {
		return c.HTTP
	}

	return http.DefaultClient
}

// Do performs one request against the server.
//
// path is the server-absolute path including the API prefix, so a caller can
// hand it a template resolved from the OpenAPI document unchanged. A non-2xx
// response is an *ExitError carrying the problem document verbatim; a
// transport failure is an *ExitError with the network_error code.
func (c *Client) Do(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*Response, error) {
	req, target, err := c.buildRequest(ctx, method, path, query, header, body)
	if err != nil {
		return nil, err
	}

	c.trace(c.redactText(fmt.Sprintf("> %s %s", method, target)))
	if body != nil {
		c.trace(c.redactText("> body " + string(truncateBytes(redactJSON(body), maxErrorBodyBytes))))
	}

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, networkError(c.redactText(err.Error()))
	}
	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, networkError(c.redactText(err.Error()))
	}
	c.trace(fmt.Sprintf("< %d %d bytes", resp.StatusCode, len(raw)))

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		return nil, c.errorFor(method, path, resp, raw)
	}

	return &Response{
		Status:      resp.StatusCode,
		Header:      resp.Header,
		Body:        raw,
		ContentType: resp.Header.Get("Content-Type"),
	}, nil
}

// Stream performs one request whose response body the caller reads as it
// arrives.
//
// An event stream has no end the client can wait for, so this returns the live
// response instead of a buffered one: the caller reads the frames and closes
// the body, which is also what tells the server to stop. A non-2xx answer is
// still read in full and turned into the same *ExitError Do produces, because
// a refusal arrives as an ordinary problem document.
func (c *Client) Stream(ctx context.Context, method, path string, query url.Values, header http.Header) (*http.Response, error) {
	req, target, err := c.buildRequest(ctx, method, path, query, header, nil)
	if err != nil {
		return nil, err
	}

	c.trace(c.redactText(fmt.Sprintf("> %s %s", method, target)))

	resp, err := c.httpClient().Do(req)
	if err != nil {
		return nil, networkError(c.redactText(err.Error()))
	}

	if resp.StatusCode >= 200 && resp.StatusCode <= 299 {
		return resp, nil
	}

	defer func() { _ = resp.Body.Close() }()

	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxErrorBodyBytes))
	if err != nil {
		return nil, networkError(c.redactText(err.Error()))
	}
	c.trace(fmt.Sprintf("< %d %d bytes", resp.StatusCode, len(raw)))

	return nil, c.errorFor(method, path, resp, raw)
}

// buildRequest assembles one request and returns the URL it targets, which the
// trace needs and a caller should not have to recompose.
func (c *Client) buildRequest(ctx context.Context, method, path string, query url.Values, header http.Header, body []byte) (*http.Request, string, error) {
	target := c.BaseURL + path
	if len(query) > 0 {
		target += "?" + query.Encode()
	}

	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, target, reader)
	if err != nil {
		return nil, target, networkError(c.redactText(err.Error()))
	}

	// A caller that names its own Accept keeps it: the streaming verbs ask for
	// an event stream, and adding application/json beside it would leave the
	// server two answers to choose between. Content-Type is the same rule for
	// the same reason: the escape hatch carries bodies that are not JSON (the
	// CSV import), and a default added next to the caller's own value would
	// leave a server that reads the first value decoding the body as JSON.
	if header.Get("Accept") == "" {
		req.Header.Set("Accept", "application/json")
	}
	if body != nil && header.Get("Content-Type") == "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for name, values := range header {
		for _, value := range values {
			req.Header.Add(name, value)
		}
	}
	// Only on a write: the header says what informed a change, and a read
	// changes nothing. Sending it on a GET would put a claim in the server's
	// log that nothing recorded.
	if len(c.SkillsUsed) > 0 && header.Get(skillsUsedHeader) == "" && mutating(method) {
		req.Header.Set(skillsUsedHeader, strings.Join(c.SkillsUsed, ","))
	}
	if c.Token != "" {
		req.Header.Set("Authorization", "Bearer "+c.Token)
	}

	return req, target, nil
}

// mutating reports whether a method changes something on the server.
func mutating(method string) bool {
	switch method {
	case http.MethodGet, http.MethodHead, http.MethodOptions:
		return false
	default:
		return true
	}
}

// networkError is the failure a request that never produced a response has.
func networkError(message string) *ExitError {
	return &ExitError{Code: ExitFailure, ErrCode: "network_error", Message: message}
}

// errorFor turns a non-2xx response into an ExitError.
func (c *Client) errorFor(method, path string, resp *http.Response, raw []byte) *ExitError {
	code, errCode := exitForStatus(resp.StatusCode)

	failure := &ExitError{
		Code:    code,
		ErrCode: errCode,
		Status:  resp.StatusCode,
		Message: c.redactText(fmt.Sprintf("%s %s: HTTP %d", method, path, resp.StatusCode)),
	}

	if problem, ok := problemDocument(raw); ok {
		failure.Problem = c.redactProblem(problem)
		if detail := problemMessage(failure.Problem); detail != "" {
			failure.Message = c.redactText(detail)
		}

		return failure
	}

	if len(raw) > 0 {
		failure.Body = c.redactText(truncateBytes(raw, maxErrorBodyBytes))
	}

	return failure
}

// redactProblem removes secrets from a problem document before it is carried.
//
// An error envelope is copied into terminals, CI logs and agent transcripts, so
// whatever the server put in the problem has to be safe there. The current
// server keeps request values out of its validation problems, but one from
// before that fix echoes a refused body back — the whole object for a missing
// or unexpected property, the raw bytes for a body that did not parse — and a
// proxy or a future handler might echo the client's own token.
//
// Every errors[] value at the location "body" is therefore dropped whatever
// its type, because that is where both whole-body echoes land. Any other value
// is kept, because it is what the caller reads next — a compile refusal's
// codes, an idempotency conflict's — and only its secret-named fields are
// redacted. The document is decoded with UseNumber so every value that
// survives is carried exactly as the server wrote it.
func (c *Client) redactProblem(problem json.RawMessage) json.RawMessage {
	decoder := json.NewDecoder(bytes.NewReader(problem))
	decoder.UseNumber()

	var decoded any
	if err := decoder.Decode(&decoded); err != nil {
		return problem
	}

	if document, ok := decoded.(map[string]any); ok {
		if issues, ok := document["errors"].([]any); ok {
			for _, entry := range issues {
				issue, ok := entry.(map[string]any)
				if !ok {
					continue
				}
				value, present := issue["value"]
				if !present {
					continue
				}
				if issue["location"] == "body" {
					delete(issue, "value")
					continue
				}
				issue["value"] = redactKeys(value, errorValueKeys)
			}
		}
	}

	redacted := redactKeys(decoded, problemKeys)
	if c.Token != "" {
		redacted = c.redactToken(redacted)
	}

	encoded, err := json.Marshal(redacted)
	if err != nil {
		return problem
	}

	return encoded
}

// redactToken replaces the credential wherever it appears in a decoded JSON
// value, including inside a longer string such as a URL.
func (c *Client) redactToken(value any) any {
	switch typed := value.(type) {
	case string:
		return strings.ReplaceAll(typed, c.Token, "[redacted]")
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			out[key] = c.redactToken(inner)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = c.redactToken(inner)
		}

		return out
	default:
		return value
	}
}

// problemDocument recognises an RFC 9457 problem document.
//
// The media type is not consulted: huma answers with application/json on some
// error paths and a proxy may rewrite the header, so the shape (a JSON object
// carrying title or status) is what identifies one.
func problemDocument(body []byte) (json.RawMessage, bool) {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 || trimmed[0] != '{' {
		return nil, false
	}

	var probe map[string]json.RawMessage
	if err := json.Unmarshal(trimmed, &probe); err != nil {
		return nil, false
	}
	_, hasTitle := probe["title"]
	_, hasStatus := probe["status"]
	if !hasTitle && !hasStatus {
		return nil, false
	}

	return json.RawMessage(trimmed), true
}

// problemMessage is the one-line reason a problem document carries, if any.
func problemMessage(problem json.RawMessage) string {
	var doc struct {
		Detail string `json:"detail"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(problem, &doc); err != nil {
		return ""
	}
	if strings.TrimSpace(doc.Detail) != "" {
		return doc.Detail
	}

	return doc.Title
}

// trace writes one request-trace line when --verbose is on.
func (c *Client) trace(line string) {
	if c.Verbose == nil {
		return
	}
	fmt.Fprintln(c.Verbose, line)
}

// credentialKeys are the JSON field names whose values are never printed. The
// comparison normalises case and drops underscores, so api_key and apiKey both
// match.
var credentialKeys = map[string]bool{
	"token":         true,
	"apikey":        true,
	"password":      true,
	"secret":        true,
	"authorization": true,
	"privatekey":    true,
	"credential":    true,
}

// redactJSON replaces credential values in a JSON document. A body that is not
// JSON is returned unchanged, because there is nothing to redact it by.
func redactJSON(body []byte) []byte {
	trimmed := bytes.TrimSpace(body)
	if len(trimmed) == 0 {
		return body
	}

	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		return body
	}

	encoded, err := json.Marshal(redactValue(decoded))
	if err != nil {
		return body
	}

	return encoded
}

// problemKeys are the field names redacted anywhere in a problem document:
// every credential key, and fields, which is the object a credential's secrets
// are sent in.
var problemKeys = withKeys(credentialKeys, "fields")

// errorValueKeys are the field names redacted inside one errors[] value. It
// adds value, which is where a header credential keeps its secret. It is not
// in problemKeys because every errors[] entry has a value key of its own, and
// matching that one would redact the compile codes a caller needs whole.
var errorValueKeys = withKeys(problemKeys, "value")

// withKeys is base plus extra, with base left as it was.
func withKeys(base map[string]bool, extra ...string) map[string]bool {
	keys := make(map[string]bool, len(base)+len(extra))
	for key := range base {
		keys[key] = true
	}
	for _, key := range extra {
		keys[normalizeKey(key)] = true
	}

	return keys
}

// redactValue walks a decoded JSON value, replacing credential values.
func redactValue(value any) any {
	return redactKeys(value, credentialKeys)
}

// redactKeys walks a decoded JSON value, replacing the value of every field
// whose normalised name is in keys, at any depth.
func redactKeys(value any, keys map[string]bool) any {
	switch typed := value.(type) {
	case map[string]any:
		out := make(map[string]any, len(typed))
		for key, inner := range typed {
			if keys[normalizeKey(key)] {
				out[key] = "[redacted]"
				continue
			}
			out[key] = redactKeys(inner, keys)
		}

		return out
	case []any:
		out := make([]any, len(typed))
		for i, inner := range typed {
			out[i] = redactKeys(inner, keys)
		}

		return out
	default:
		return value
	}
}

// normalizeKey lowercases a JSON field name and drops underscores.
func normalizeKey(key string) string {
	return strings.ReplaceAll(strings.ToLower(key), "_", "")
}

// sensitiveQuery matches a credential in a query string, so a token that
// reached a URL is redacted even when the CLI did not put it there.
var sensitiveQuery = regexp.MustCompile(`(?i)([?&](?:token|api_key|apikey|access_token|key|secret|password|authorization)=)[^&\s"']*`)

// redactText removes the client's own token and any credential a URL in the
// text carries. Every string that can reach a stream goes through it.
func (c *Client) redactText(text string) string {
	if c.Token != "" {
		text = strings.ReplaceAll(text, c.Token, "[redacted]")
	}

	return sensitiveQuery.ReplaceAllString(text, "${1}[redacted]")
}

// truncateBytes returns at most limit bytes of body as text.
func truncateBytes(body []byte, limit int) string {
	if len(body) <= limit {
		return string(body)
	}

	return string(body[:limit])
}
