package nodes

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"net/url"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/kilaslab/kilas-flow/internal/binary"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/jsrun"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The server's half of a Code node's this.helpers and $getWorkflowStaticData.
//
// A script never reaches the network, a file or the database itself: its
// request arrives here from the runtime (over the worker protocol when a
// worker runs it) and is carried out by the server, bound to the node's own
// request. So HTTP goes through the deployment's egress policy, exactly as
// the HTTP Request node's does; files are read from and stored in the
// execution's own tenant-scoped storage; and the static data is the
// execution's handle on its own workflow's. No credential is reachable, as
// in n8n's Code node.

// codeHTTP sends a Code node's requests under the deployment's policy. It is
// built once per executor, so its clients' connections are shared.
type codeHTTP struct {
	policy safehttp.Policy
	// following follows redirects up to the policy's ceiling; stopping hands
	// the redirect itself back.
	following *http.Client
	stopping  *http.Client
}

func newCodeHTTP(policy safehttp.Policy) *codeHTTP {
	stopping := safehttp.NewClient(policy)
	stopping.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
	return &codeHTTP{policy: policy, following: safehttp.NewClient(policy), stopping: stopping}
}

// client picks the client for a request's redirect setting: the policy's, or
// fewer redirects than it allows, never more.
func (h *codeHTTP) client(redirects *int) *http.Client {
	switch {
	case redirects == nil:
		return h.following
	case *redirects <= 0:
		return h.stopping
	}
	limited := *h.following
	allowed, check := *redirects, h.following.CheckRedirect
	limited.CheckRedirect = func(request *http.Request, via []*http.Request) error {
		if len(via) > allowed {
			return fmt.Errorf("stopped after %d redirects", allowed)
		}
		return check(request, via)
	}
	return &limited
}

// codeHelpers answers one node run's helpers.
type codeHelpers struct {
	ctx     context.Context
	http    *codeHTTP
	request engine.Request
	items   []workflow.Item
	node    string
}

var _ jsrun.Helpers = (*codeHelpers)(nil)

// HTTPRequest sends the request the code described. A target the policy
// refuses, a network failure and a timeout are the code's to catch; a
// response past the size cap stops it.
func (helpers *codeHelpers) HTTPRequest(ctx context.Context, described jsrun.HTTPRequest, body []byte) (jsrun.HTTPResponse, []byte, error) {
	method := strings.ToUpper(strings.TrimSpace(described.Method))
	if !knownHTTPMethod(method) {
		return jsrun.HTTPResponse{}, nil, fmt.Errorf("the method %q is not supported", described.Method)
	}
	target, err := url.Parse(described.URL)
	if err != nil {
		return jsrun.HTTPResponse{}, nil, fmt.Errorf("%q is not a valid URL", described.URL)
	}
	policy := helpers.http.policy
	if err := policy.CheckURL(target); err != nil {
		return jsrun.HTTPResponse{}, nil, err
	}
	timeout := policy.Timeout
	if described.TimeoutMS > 0 {
		// The code may only tighten the deployment's ceiling, never raise it.
		if requested := time.Duration(described.TimeoutMS) * time.Millisecond; timeout <= 0 || requested < timeout {
			timeout = requested
		}
	}
	if timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, timeout)
		defer cancel()
	}
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	outgoing, err := http.NewRequestWithContext(ctx, method, target.String(), reader)
	if err != nil {
		return jsrun.HTTPResponse{}, nil, fmt.Errorf("the request cannot be built: %w", err)
	}
	for _, header := range described.Headers {
		outgoing.Header.Add(header[0], header[1])
	}
	response, err := helpers.http.client(described.Redirects).Do(outgoing)
	if err != nil {
		return jsrun.HTTPResponse{}, nil, err
	}
	defer response.Body.Close()
	limit := policy.MaxResponseBytes
	if limit <= 0 {
		limit = safehttp.DefaultPolicy().MaxResponseBytes
	}
	limit = min(limit, jsrun.MaxFileBytes)
	contents, truncated, err := safehttp.Policy{MaxResponseBytes: limit}.ReadBody(response.Body)
	if err != nil {
		return jsrun.HTTPResponse{}, nil, fmt.Errorf("reading the response: %w", err)
	}
	if truncated {
		return jsrun.HTTPResponse{}, nil, jsrun.ResponseLimitError(limit)
	}
	return jsrun.HTTPResponse{
		StatusCode:    response.StatusCode,
		StatusMessage: strings.TrimPrefix(response.Status, strconv.Itoa(response.StatusCode)+" "),
		Headers:       responseHeaderValues(response.Header),
	}, contents, nil
}

// responseHeaderValues are a response's headers as Node reports them: names
// in lower case, a repeated header joined with commas, and set-cookie always
// a list.
func responseHeaderValues(header http.Header) map[string]any {
	headers := make(map[string]any, len(header))
	for name, values := range header {
		if len(values) == 0 {
			continue
		}
		name = strings.ToLower(name)
		if name == "set-cookie" {
			headers[name] = append([]string(nil), values...)
			continue
		}
		headers[name] = strings.Join(values, ", ")
	}
	return headers
}

// ReadFile reads a file of the node's own input from the execution's
// storage. Nothing else is reachable: the code names an item and a
// property, never a file.
func (helpers *codeHelpers) ReadFile(_ context.Context, itemIndex int, property string) ([]byte, error) {
	if itemIndex < 0 || itemIndex >= len(helpers.items) {
		return nil, fmt.Errorf("item %d is not in this node's input, which has %d items", itemIndex, len(helpers.items))
	}
	ref, ok := helpers.items[itemIndex].Binary[property]
	if !ok {
		return nil, fmt.Errorf("item %d of this node's input has no file %q", itemIndex, property)
	}
	if helpers.request.Binaries == nil {
		return nil, binary.ErrNotConfigured
	}
	if ref.Size > jsrun.MaxFileBytes {
		return nil, jsrun.FileLimitError("a file", ref.Size)
	}
	reader, _, err := helpers.request.Binaries.Get(ref.ID)
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", property, err)
	}
	defer reader.Close()
	data, err := io.ReadAll(io.LimitReader(reader, jsrun.MaxFileBytes+1))
	if err != nil {
		return nil, fmt.Errorf("reading file %q: %w", property, err)
	}
	if size := int64(len(data)); size > jsrun.MaxFileBytes {
		return nil, jsrun.FileLimitError("a file", size)
	}
	return data, nil
}

// WriteFile stores the code's bytes as a file of this execution. Its name
// keeps only the last part of a path; its type, when the code gave none, is
// the one its name says, or what its bytes look like, or plain text, as in
// n8n.
func (helpers *codeHelpers) WriteFile(ctx context.Context, data []byte, fileName, mimeType string) (workflow.BinaryRef, error) {
	if helpers.request.Binaries == nil {
		return workflow.BinaryRef{}, binary.ErrNotConfigured
	}
	name := ""
	if fileName != "" {
		if name = path.Base(strings.ReplaceAll(fileName, "\\", "/")); name == "." || name == "/" {
			name = ""
		}
	}
	media := strings.TrimSpace(mimeType)
	if media == "" && path.Ext(name) != "" {
		media = mediaType(mime.TypeByExtension(path.Ext(name)))
	}
	if media == "" {
		if sniffed := mediaType(http.DetectContentType(data)); sniffed != "application/octet-stream" {
			media = sniffed
		}
	}
	if media == "" {
		media = "text/plain"
	}
	// Checked again as late as it can be: a run that ended while this call
	// was on its way stores nothing.
	if err := ctx.Err(); err != nil {
		return workflow.BinaryRef{}, fmt.Errorf("the run this file belongs to is over: %w", err)
	}
	ref, err := helpers.request.Binaries.Put(name, media, bytes.NewReader(data))
	if err != nil {
		return workflow.BinaryRef{}, fmt.Errorf("storing the file: %w", err)
	}
	return ref, nil
}

// StaticData returns one kind of the workflow's static data: the workflow's
// own for "global", this node's for "node", keyed by its name as n8n keys it.
func (helpers *codeHelpers) StaticData(kind string) (string, error) {
	if helpers.request.StaticData == nil {
		return "{}", nil
	}
	data, err := helpers.request.StaticData.Get(helpers.ctx, staticDataKey(kind, helpers.node))
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func staticDataKey(kind, node string) string {
	if kind == "node" {
		return "node:" + node
	}
	return kind
}

// keepStaticData puts what a node run left in the static data into the
// execution's handle, where the next node run sees it and the service saves
// it. Growing the whole document past its cap fails the run and keeps
// nothing.
func keepStaticData(ctx context.Context, request engine.Request, node string, static map[string]string) error {
	if len(static) == 0 || request.StaticData == nil {
		return nil
	}
	changes := make(map[string]json.RawMessage, len(static))
	for kind, text := range static {
		changes[staticDataKey(kind, node)] = json.RawMessage(text)
	}
	err := request.StaticData.Set(ctx, changes, jsrun.MaxStaticDataBytes)
	if errors.Is(err, engine.ErrStaticDataTooLarge) {
		return jsrun.StaticDataLimitError()
	}
	return err
}
