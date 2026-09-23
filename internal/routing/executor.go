package routing

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"mime/multipart"
	"net/http"
	"net/url"
	"path"
	"sort"
	"strconv"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/safehttp"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// Catalog supplies the definition whose properties the interpreter walks.
//
// The interpreter needs the ordered property list and each property's
// visibility rule, and those live on the definition rather than being copied
// into routing metadata: a second copy of a visibility rule is a rule that will
// disagree with the editor's copy about whether a field is even shown.
type Catalog interface {
	Get(nodeType string, version workflow.TypeVersion) (node.Definition, bool)
}

// Executor runs any node described by routing metadata, with no node-specific
// Go anywhere in the path.
type Executor struct {
	policy  safehttp.Policy
	client  *http.Client
	routes  *Registry
	catalog Catalog
}

// NewExecutor builds the interpreter.
func NewExecutor(policy safehttp.Policy, routes *Registry, catalog Catalog) *Executor {
	return &Executor{policy: policy, client: safehttp.NewClient(policy), routes: routes, catalog: catalog}
}

// Execute runs the node once per incoming item.
//
// Once per item is the declarative contract, and it is why the page loop below
// lives inside this one: a two-item input must produce two independent reads of
// a paginated list, not one list counted twice.
func (executor *Executor) Execute(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	description, found := executor.routes.Lookup(ir.Type, ir.TypeVersion)
	if !found {
		return nil, fmt.Errorf("node %q: no routing description is registered for %s v%s", ir.Name, ir.Type, ir.TypeVersion)
	}
	definition, found := executor.catalog.Get(ir.Type, ir.TypeVersion)
	if !found {
		return nil, fmt.Errorf("node %q: %s v%s is not in the node catalogue", ir.Name, ir.Type, ir.TypeVersion)
	}

	// The credential is resolved once. It does not vary per item, and resolving
	// it inside the loop would decrypt the same secret once per row.
	resolved, _, hasCredential, err := request.ResolveNodeCredential(ctx, ir)
	if err != nil {
		return nil, err
	}
	public := map[string]string{}
	if hasCredential {
		// Non-secret half only, from the credential type's own field
		// descriptors. A type this build does not know yields nothing rather
		// than everything.
		_, public = credentials.Split(resolved.Type, resolved.Fields)
	}

	items := input["main"]
	if len(items) == 0 {
		// A routed node behind a trigger that produced no items still makes its
		// one call, the same way the HTTP node does.
		items = []workflow.Item{{JSON: map[string]any{}}}
	}

	produced := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		outputs, err := executor.runOne(ctx, ir, definition, description, input, request, item, index, public)
		if err != nil {
			return nil, err
		}
		produced = append(produced, outputs...)
	}
	return workflow.NodeOutput{produced}, nil
}

func (executor *Executor) runOne(
	ctx context.Context,
	ir workflow.IRNode,
	definition node.Definition,
	description *Node,
	input workflow.NodeInput,
	request engine.Request,
	item workflow.Item,
	index int,
	public map[string]string,
) ([]workflow.Item, error) {
	// Parameters resolve on the advertised grammar first, without the routing
	// roots: what a user wrote in the editor is evaluated the same way it would
	// be in any other node. Only the pack's own templates then see `$parameter`.
	base := request.ExpressionContext(item, input, index)
	parameters, err := expression.Resolve(ir.Parameters, base)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	routingContext := base
	routingContext.AllowRouting = true
	routingContext.Parameters = parameters
	routingContext.Credentials = public

	built, err := buildPlan(definition, description, parameters, item, routingContext)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	pageSize, offset := 0, 0
	if built.pagination != nil {
		pageSize = built.pagination.Properties.PageSize
	}

	collected := make([]workflow.Item, 0, 8)
	for page := 0; page < MaxPages; page++ {
		attempt := cloneRequest(built.request)
		if built.pagination != nil {
			applyOffset(&attempt, built.pagination.Properties, offset, pageSize)
		}

		decoded, err := executor.call(ctx, ir, attempt, built.files, request, parameters)
		if err != nil {
			return nil, err
		}

		pageItems, err := postProcess(decoded, built.postReceive, routingContext)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		if err := executor.attachDownloads(ctx, ir, built.postReceive, pageItems, request, routingContext); err != nil {
			return nil, err
		}
		for _, produced := range pageItems {
			// One request can turn one input item into a whole page, so the
			// runner's count-based rule would mark every result "lineage lost".
			// The executor does know better: each of these descends from the
			// item that made the call, and inherits the origin that item
			// already carried.
			if origin := item.Paired; origin != nil && !origin.Lost {
				inherited := *origin
				produced.Paired = &inherited
			}
			collected = append(collected, produced)
		}

		if built.pagination == nil {
			break
		}
		// A short page is the end of the list. Counting the extracted items
		// rather than the decoded body is what makes `rootProperty` and
		// pagination agree about how big a page was.
		if len(pageItems) < pageSize {
			break
		}
		if built.maxResults > 0 && len(collected) >= built.maxResults {
			break
		}
		offset += pageSize
	}

	if built.maxResults > 0 && len(collected) > built.maxResults {
		collected = collected[:built.maxResults]
	}
	return collected, nil
}

// call makes one request through the policy and the credential path.
func (executor *Executor) call(
	ctx context.Context,
	ir workflow.IRNode,
	attempt Request,
	files map[string]workflow.BinaryRef,
	request engine.Request,
	parameters map[string]any,
) (any, error) {
	target, err := attempt.target()
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	// Checked before the dial as well as at it: relying on the dialer alone
	// means the check runs after DNS, so an unresolvable host fails with a
	// lookup error instead of the policy's own, and a forbidden-but-resolvable
	// host is contacted before it is refused.
	if err := executor.policy.CheckURL(target); err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}

	var body []byte
	contentType := ""
	switch {
	case len(files) > 0:
		// An upload turns the whole request into multipart: the scalar fields
		// travel beside the file rather than as a JSON body, because that is
		// the only shape an API accepting a file part understands.
		body, contentType, err = multipartBody(attempt.Body, files, request)
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
	case len(attempt.Body) > 0:
		body, err = json.Marshal(attempt.Body)
		if err != nil {
			return nil, fmt.Errorf("node %q: encode request body: %w", ir.Name, err)
		}
		contentType = "application/json"
	}
	method := strings.ToUpper(attempt.Method)
	if method == "" {
		method = http.MethodGet
	}

	var reader *bytes.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}
	var httpRequest *http.Request
	if reader != nil {
		httpRequest, err = http.NewRequestWithContext(ctx, method, target.String(), reader)
	} else {
		httpRequest, err = http.NewRequestWithContext(ctx, method, target.String(), nil)
	}
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if contentType != "" {
		httpRequest.Header.Set("Content-Type", contentType)
	}
	for key, value := range attempt.Headers {
		httpRequest.Header.Set(key, stringOf(value))
	}
	if err := request.Authenticate(ctx, ir, httpRequest); err != nil {
		return nil, err
	}

	response, err := executor.client.Do(httpRequest)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	defer response.Body.Close()

	contents, truncated, err := executor.policy.ReadBody(response.Body)
	if err != nil {
		return nil, fmt.Errorf("node %q: read response: %w", ir.Name, err)
	}
	if response.StatusCode >= 400 {
		// The error names what a user can change: which node, which operation,
		// what the server said, and — when the service explains itself — its
		// own words, which are usually the only actionable part.
		//
		// The URL is deliberately not repeated: a path-placed credential puts
		// the token in it, and Redacted() hides userinfo, not a path segment.
		return nil, fmt.Errorf("node %q: %s %s returned %d%s%s",
			ir.Name, method, target.Path, response.StatusCode,
			operationSuffix(parameters), serviceDescription(contents))
	}
	if truncated {
		return nil, fmt.Errorf("node %q: response exceeds the configured size limit", ir.Name)
	}

	decoded, err := decodeResponse(contents)
	if err != nil {
		return nil, fmt.Errorf("node %q: %w", ir.Name, err)
	}
	return decoded, nil
}

// target assembles baseURL and url into one absolute URL.
func (request Request) target() (*url.URL, error) {
	joined := substitutePath(strings.TrimSpace(request.URL), request.Path)
	if base := strings.TrimSpace(request.BaseURL); base != "" {
		if joined == "" {
			joined = base
		} else if strings.HasPrefix(strings.ToLower(joined), "http://") || strings.HasPrefix(strings.ToLower(joined), "https://") {
			// An operation may point at another host entirely; an absolute URL
			// wins over the base rather than being pasted onto it.
		} else {
			joined = strings.TrimSuffix(base, "/") + "/" + strings.TrimPrefix(joined, "/")
		}
	}
	if joined == "" {
		return nil, fmt.Errorf("routing produced no URL: the node's operation declares no request")
	}
	target, err := url.Parse(joined)
	if err != nil {
		return nil, fmt.Errorf("routing produced an unusable URL %q: %w", joined, err)
	}
	if len(request.Query) > 0 {
		query := target.Query()
		for key, value := range request.Query {
			query.Set(key, stringOf(value))
		}
		target.RawQuery = query.Encode()
	}
	return target, nil
}

// substitutePath fills `{name}` placeholders with escaped path segments.
//
// Escaping is the point. A chat id containing a slash, substituted raw, would
// silently change which endpoint is called — and the value comes from a
// workflow author or from item data, so it is not the pack's to trust.
func substitutePath(target string, values map[string]any) string {
	for name, value := range values {
		target = strings.ReplaceAll(target, "{"+name+"}", safehttp.PathSegment(stringOf(value)))
	}
	return target
}

// applyOffset writes the paging parameters into the request for one page.
func applyOffset(attempt *Request, properties OffsetPagination, offset, pageSize int) {
	destination := &attempt.Query
	if properties.Type == "body" {
		destination = &attempt.Body
	}
	if *destination == nil {
		*destination = map[string]any{}
	}
	(*destination)[properties.OffsetParameter] = offset
	if properties.LimitParameter != "" {
		(*destination)[properties.LimitParameter] = pageSize
	}
}

// operationSuffix names the resource and operation in effect, when the node has
// them, so a failure says which of a hundred operations failed.
//
// Read from the resolved parameters rather than from the routing description:
// the two keys are the format's own convention, and a node that carries them
// without routing on them still has a user who needs to know which one broke.
func operationSuffix(parameters map[string]any) string {
	named := make([]string, 0, 2)
	for _, key := range []string{"resource", "operation"} {
		if value, present := parameters[key]; present && value != nil {
			named = append(named, fmt.Sprintf("%s %v", key, value))
		}
	}
	if len(named) == 0 {
		return ""
	}
	return " (" + strings.Join(named, ", ") + ")"
}

// serviceDescription pulls the service's own explanation out of an error body.
//
// Telegram answers `{"ok":false,"description":"Bad Request: chat not found"}`,
// and that sentence is the only part of the failure a user can act on. The
// keys are the ones the services in this product actually use; anything else
// contributes nothing rather than a guess.
func serviceDescription(contents []byte) string {
	var body map[string]any
	if err := json.Unmarshal(bytes.TrimSpace(contents), &body); err != nil {
		return ""
	}
	for _, key := range []string{"description", "error_description", "message", "error"} {
		if text, ok := body[key].(string); ok && strings.TrimSpace(text) != "" {
			return ": " + strings.TrimSpace(text)
		}
	}
	return ""
}

func decodeResponse(contents []byte) (any, error) {
	trimmed := bytes.TrimSpace(contents)
	if len(trimmed) == 0 {
		return map[string]any{}, nil
	}
	var decoded any
	if err := json.Unmarshal(trimmed, &decoded); err != nil {
		// A declarative pack describes a JSON API. A body that is not JSON is a
		// server saying something went wrong in a way the pack did not predict,
		// and guessing at it would produce an item shaped like nothing.
		return nil, fmt.Errorf("response is not JSON: %w", err)
	}
	return decoded, nil
}

func stringOf(value any) string {
	switch typed := value.(type) {
	case nil:
		return ""
	case string:
		return typed
	case bool:
		return strconv.FormatBool(typed)
	case float64:
		return strconv.FormatFloat(typed, 'f', -1, 64)
	case int:
		return strconv.Itoa(typed)
	case json.Number:
		return typed.String()
	default:
		encoded, err := json.Marshal(typed)
		if err != nil {
			return fmt.Sprintf("%v", typed)
		}
		return string(encoded)
	}
}

// multipartBody encodes scalar fields alongside the item's attachments.
//
// The payload is read from the binary store here rather than being carried on
// the item, which is the whole point of the store: a fifty-megabyte video is
// streamed into one request and never enters an execution record.
func multipartBody(fields map[string]any, files map[string]workflow.BinaryRef, request engine.Request) ([]byte, string, error) {
	if request.Binaries == nil {
		return nil, "", fmt.Errorf("binary storage is not configured on this server")
	}
	buffer := &bytes.Buffer{}
	writer := multipart.NewWriter(buffer)

	// Sorted, so the same request encodes the same way twice — a body that
	// differs run to run is a body nobody can diff in a bug report.
	for _, key := range sortedKeys(fields) {
		if err := writer.WriteField(key, stringOf(fields[key])); err != nil {
			return nil, "", err
		}
	}
	for _, key := range sortedFileKeys(files) {
		reference := files[key]
		payload, _, err := request.Binaries.Get(reference.ID)
		if err != nil {
			return nil, "", fmt.Errorf("read the attachment for %q: %w", key, err)
		}
		name := reference.FileName
		if name == "" {
			name = key
		}
		part, err := writer.CreateFormFile(key, name)
		if err != nil {
			payload.Close()
			return nil, "", err
		}
		_, copyErr := io.Copy(part, payload)
		payload.Close()
		if copyErr != nil {
			return nil, "", fmt.Errorf("read the attachment for %q: %w", key, copyErr)
		}
	}
	if err := writer.Close(); err != nil {
		return nil, "", err
	}
	return buffer.Bytes(), writer.FormDataContentType(), nil
}

// attachDownloads runs the binaryData post-receive action over produced items.
//
// It happens after the response is shaped rather than inside postProcess,
// because it makes a *second network call* — through the same policy and the
// same credential — and postProcess is otherwise pure.
func (executor *Executor) attachDownloads(
	ctx context.Context,
	ir workflow.IRNode,
	actions []PostReceive,
	items []workflow.Item,
	request engine.Request,
	base expression.Context,
) error {
	for _, action := range actions {
		if action.Type != PostReceiveBinaryData {
			continue
		}
		template, _ := action.Properties["url"].(string)
		property, _ := action.Properties["property"].(string)
		if property == "" {
			property = "data"
		}
		for index := range items {
			// `$json` is the item being downloaded for, so a template reads the
			// path the API just returned.
			itemContext := base
			itemContext.JSON = items[index].JSON
			resolved, err := expression.Evaluate(template, itemContext)
			if err != nil {
				return fmt.Errorf("node %q: postReceive %s url: %w", ir.Name, PostReceiveBinaryData, err)
			}
			link := stringOf(resolved)
			if strings.TrimSpace(link) == "" {
				continue
			}
			if err := executor.download(ctx, ir, link, property, &items[index], request); err != nil {
				return err
			}
		}
	}
	return nil
}

func (executor *Executor) download(
	ctx context.Context,
	ir workflow.IRNode,
	link, property string,
	item *workflow.Item,
	request engine.Request,
) error {
	if request.Binaries == nil {
		return fmt.Errorf("node %q: binary storage is not configured on this server", ir.Name)
	}
	target, err := url.Parse(link)
	if err != nil {
		return fmt.Errorf("node %q: the download URL is unusable: %w", ir.Name, err)
	}
	if err := executor.policy.CheckURL(target); err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	httpRequest, err := http.NewRequestWithContext(ctx, http.MethodGet, target.String(), nil)
	if err != nil {
		return fmt.Errorf("node %q: %w", ir.Name, err)
	}
	if err := request.Authenticate(ctx, ir, httpRequest); err != nil {
		return err
	}
	response, err := executor.client.Do(httpRequest)
	if err != nil {
		return fmt.Errorf("node %q: download: %w", ir.Name, err)
	}
	defer response.Body.Close()
	if response.StatusCode >= 400 {
		return fmt.Errorf("node %q: downloading %s answered %d", ir.Name, target.Redacted(), response.StatusCode)
	}
	contents, truncated, err := executor.policy.ReadBody(response.Body)
	if err != nil {
		return fmt.Errorf("node %q: download: %w", ir.Name, err)
	}
	if truncated {
		return fmt.Errorf("node %q: the download exceeds the configured size limit", ir.Name)
	}
	reference, err := request.Binaries.Put(path.Base(target.Path), response.Header.Get("Content-Type"), bytes.NewReader(contents))
	if err != nil {
		return fmt.Errorf("node %q: store the download: %w", ir.Name, err)
	}
	if item.Binary == nil {
		item.Binary = map[string]workflow.BinaryRef{}
	}
	item.Binary[property] = reference
	return nil
}

func sortedKeys(values map[string]any) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}

func sortedFileKeys(values map[string]workflow.BinaryRef) []string {
	keys := make([]string, 0, len(values))
	for key := range values {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
