package routing

import (
	"bytes"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"sync"

	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

// ExecutorID is the server-owned binding for the declarative interpreter. A
// pack's node definitions name it instead of shipping Go.
const ExecutorID = "core.routing"

// Node is one node type's whole routing description.
//
// Plain data with JSON tags throughout: a generated pack is data on disk, and
// the same types have to survive a round trip through it.
type Node struct {
	Type    string               `json:"type"`
	Version workflow.TypeVersion `json:"version"`
	// Defaults are the node-level `requestDefaults`.
	Defaults Request `json:"requestDefaults,omitempty"`
	// Properties is per-property routing, keyed by property key.
	Properties map[string]Routing `json:"properties,omitempty"`
	// Options is per-option routing, keyed by property key then by the option's
	// value. This is where a declarative node keeps most of itself: an
	// `operation` property whose every option carries the request it makes is
	// how one node becomes a hundred operations.
	Options map[string]map[string]Routing `json:"options,omitempty"`
	// Cascade is routing selected by two properties at once.
	//
	// It exists because the format's own shape cannot be expressed by Options
	// alone: operation names repeat across resources — WAHA has four distinct
	// `Get` operations — so an `operation` → routing map would collapse them
	// onto whichever request happened to be written last. n8n avoids the
	// collision by keeping one `operation` property per resource, each gated on
	// the resource; a KilasFlow definition holds one property per key, so the
	// disambiguation lives here instead.
	Cascade *Cascade `json:"cascade,omitempty"`
}

// Cascade routes on a resource and an operation together.
type Cascade struct {
	// ResourceKey and OperationKey name the two properties. Empty means
	// "resource" and "operation", which is what the format calls them.
	ResourceKey  string `json:"resourceKey,omitempty"`
	OperationKey string `json:"operationKey,omitempty"`
	// Routes is keyed by resource value, then operation value.
	Routes map[string]map[string]Routing `json:"routes"`
}

// Keys returns the two property names, filling in the format's defaults.
func (cascade *Cascade) Keys() (resource, operation string) {
	resource, operation = "resource", "operation"
	if cascade == nil {
		return resource, operation
	}
	if cascade.ResourceKey != "" {
		resource = cascade.ResourceKey
	}
	if cascade.OperationKey != "" {
		operation = cascade.OperationKey
	}
	return resource, operation
}

// Routing is what a property or an option contributes to the request.
type Routing struct {
	Request *Request `json:"request,omitempty"`
	// Send places the property this routing is attached to. It is the format's
	// own per-property form.
	Send *Send `json:"send,omitempty"`
	// Sends places named parameters, and is how a generated pack expresses
	// placement that varies by operation.
	//
	// n8n keeps one property per operation and lets several share a name,
	// disambiguated by displayOptions. A KilasFlow definition holds one
	// property per key — the registry refuses duplicates, deliberately — so a
	// generated pack cannot say "chatId goes in the body for Send Text and in
	// the query for Get Messages" on the property. It says it on the operation.
	Sends      []Send      `json:"sends,omitempty"`
	Output     *Output     `json:"output,omitempty"`
	Operations *Operations `json:"operations,omitempty"`
}

// Request is the request shape, in the shared vocabulary `requestDefaults` and
// `routing.request` both speak. Every string may carry `{{ }}` templates.
type Request struct {
	Method string `json:"method,omitempty"`
	// BaseURL is prefixed to URL. Only the defaults normally set it, but an
	// option may override it for an operation that lives on another host.
	BaseURL string         `json:"baseURL,omitempty"`
	URL     string         `json:"url,omitempty"`
	Headers map[string]any `json:"headers,omitempty"`
	Query   map[string]any `json:"qs,omitempty"`
	Body    map[string]any `json:"body,omitempty"`
	// Path fills `{name}` placeholders in URL, one whole path segment at a
	// time and percent-escaped.
	//
	// A path parameter is deliberately not a template. A chat id containing a
	// slash written as `{{ $parameter.chatId }}` would silently change which
	// endpoint is called; substituted here it can only ever be one segment.
	Path map[string]any `json:"path,omitempty"`
}

// Send places one property's value into the request.
type Send struct {
	// From names the parameter to read. Empty means the property this routing
	// is attached to, which is the per-property form.
	From string `json:"from,omitempty"`
	// Type is where the value goes: "body", "query", "path" or "binary".
	//
	// "binary" reads the parameter as the *name of a binary property on the
	// item* and sends that attachment as a file part, which turns the whole
	// request into multipart. It is a placement rather than a request-level
	// flag because whether an upload happens depends on which parameter is
	// visible: a Telegram photo is either an uploaded file or a file_id, and
	// the same operation does both.
	Type string `json:"type,omitempty"`
	// Property is the destination path. Dot notation unless disabled, so
	// `message.text` nests rather than making a key with a dot in it.
	Property string `json:"property,omitempty"`
	// PropertyInDotNotation defaults to true, matching the format.
	PropertyInDotNotation *bool `json:"propertyInDotNotation,omitempty"`
	// Value overrides what is sent. Usually an expression over `$value`, which
	// is this property's own value.
	Value string `json:"value,omitempty"`
	// PreSend names JavaScript hooks. Any entry is a registration error: the
	// closure it names does not exist here, and a request that quietly skips
	// the step that was going to sign it is worse than a pack that will not
	// load.
	PreSend []string `json:"preSend,omitempty"`
}

// Output is what happens to the response.
type Output struct {
	MaxResults  int           `json:"maxResults,omitempty"`
	PostReceive []PostReceive `json:"postReceive,omitempty"`
}

// PostReceive is one response transformation.
type PostReceive struct {
	Type       string         `json:"type"`
	Properties map[string]any `json:"properties,omitempty"`
}

// The post-receive actions this interpreter implements.
const (
	PostReceiveRootProperty = "rootProperty"
	PostReceiveSetKeyValue  = "setKeyValue"
	PostReceiveLimit        = "limit"
	// PostReceiveBinaryData downloads a URL built from the response and
	// attaches it to the item as a binary reference.
	//
	// It exists because the shape it serves is common and two-step: an API
	// answers with a path or a link, and the bytes are behind a second call
	// that needs the same credential. Telegram's getFile is exactly that, and
	// leaving it to the caller would mean every such node grew its own Go.
	PostReceiveBinaryData = "binaryData"
)

// Operations carries request-level behaviour that is not one request.
type Operations struct {
	Pagination *Pagination `json:"pagination,omitempty"`
}

// Pagination is the paging strategy. Only "offset" is implemented.
type Pagination struct {
	Type       string           `json:"type"`
	Properties OffsetPagination `json:"properties"`
}

// PaginationOffset is the one implemented pagination type.
const PaginationOffset = "offset"

// OffsetPagination walks pages by an offset and a page size.
type OffsetPagination struct {
	LimitParameter  string `json:"limitParameter"`
	OffsetParameter string `json:"offsetParameter"`
	PageSize        int    `json:"pageSize"`
	// RootProperty is where the page's items are, when the response wraps them.
	RootProperty string `json:"rootProperty,omitempty"`
	// Type is where the two parameters go: "body" or "query".
	Type string `json:"type"`
}

// MaxPages bounds a paginated read no matter what the pack asks for.
//
// A server that answers every page with a full page — a bug, or a hostile
// endpoint — would otherwise page until the process dies. The bound is here
// rather than in configuration because it is a safety limit, not a preference.
const MaxPages = 1000

// Registry holds the routing description for every registered node type.
type Registry struct {
	mu    sync.RWMutex
	nodes map[registryKey]*Node
}

type registryKey struct {
	nodeType string
	version  workflow.TypeVersion
}

// NewRegistry returns an empty registry.
func NewRegistry() *Registry { return &Registry{nodes: map[registryKey]*Node{}} }

// Register validates a routing description and stores it.
//
// Validation is the whole point of this being a registration step: an
// unimplemented post-receive action or pagination type fails here, at startup,
// where it names the pack. The alternative — noticing at run time — turns a
// packaging mistake into a wrong HTTP request.
func (registry *Registry) Register(description *Node) error {
	if description == nil {
		return fmt.Errorf("a routing description is required")
	}
	if strings.TrimSpace(description.Type) == "" {
		return fmt.Errorf("a routing description needs a node type")
	}
	if err := description.validate(); err != nil {
		return fmt.Errorf("routing for %s: %w", description.Type, err)
	}

	registry.mu.Lock()
	defer registry.mu.Unlock()
	if registry.nodes == nil {
		registry.nodes = map[registryKey]*Node{}
	}
	key := registryKey{nodeType: description.Type, version: description.Version}
	if _, exists := registry.nodes[key]; exists {
		return fmt.Errorf("routing for %s v%s is already registered", description.Type, description.Version)
	}
	stored := *description
	registry.nodes[key] = &stored
	return nil
}

// Lookup returns one node type's routing description.
func (registry *Registry) Lookup(nodeType string, version workflow.TypeVersion) (*Node, bool) {
	if registry == nil {
		return nil, false
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	description, found := registry.nodes[registryKey{nodeType: nodeType, version: version}]
	return description, found
}

// Registered lists what is registered, in a stable order, for diagnostics.
func (registry *Registry) Registered() []string {
	if registry == nil {
		return nil
	}
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	listed := make([]string, 0, len(registry.nodes))
	for key := range registry.nodes {
		listed = append(listed, fmt.Sprintf("%s@%s", key.nodeType, key.version))
	}
	sort.Strings(listed)
	return listed
}

// Decode reads a routing description from a pack file.
//
// Unknown fields are an error. A pack written against a routing feature this
// build does not implement must fail loudly at load: silently dropping the
// field would produce a request that looks like it worked.
func Decode(data []byte) (*Node, error) {
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	var description Node
	if err := decoder.Decode(&description); err != nil {
		return nil, fmt.Errorf("decode routing description: %w", err)
	}
	if err := description.validate(); err != nil {
		return nil, fmt.Errorf("routing for %s: %w", description.Type, err)
	}
	return &description, nil
}

func (description *Node) validate() error {
	for key, routing := range description.Properties {
		if err := routing.validate(); err != nil {
			return fmt.Errorf("property %q: %w", key, err)
		}
	}
	for key, options := range description.Options {
		for value, routing := range options {
			if err := routing.validate(); err != nil {
				return fmt.Errorf("property %q option %q: %w", key, value, err)
			}
		}
	}
	if description.Cascade != nil {
		for resource, operations := range description.Cascade.Routes {
			for operation, routing := range operations {
				if err := routing.validate(); err != nil {
					return fmt.Errorf("resource %q operation %q: %w", resource, operation, err)
				}
			}
		}
	}
	return nil
}

func (send Send) validate() error {
	if len(send.PreSend) > 0 {
		return fmt.Errorf("preSend hooks are JavaScript and are not supported; remove %s", strings.Join(send.PreSend, ", "))
	}
	switch send.Type {
	case "", "body", "query", "path", "binary":
	default:
		return fmt.Errorf("send type %q is not supported; use body, query, path or binary", send.Type)
	}
	if strings.TrimSpace(send.Property) == "" {
		return fmt.Errorf("send needs a destination property")
	}
	return nil
}

func (routing Routing) validate() error {
	if routing.Send != nil {
		if err := routing.Send.validate(); err != nil {
			return err
		}
	}
	for _, send := range routing.Sends {
		if err := send.validate(); err != nil {
			return err
		}
		if strings.TrimSpace(send.From) == "" {
			return fmt.Errorf("a sends entry needs the parameter it reads")
		}
	}
	if routing.Output != nil {
		for _, action := range routing.Output.PostReceive {
			switch action.Type {
			case PostReceiveRootProperty, PostReceiveSetKeyValue, PostReceiveLimit:
			case PostReceiveBinaryData:
				if template, _ := action.Properties["url"].(string); strings.TrimSpace(template) == "" {
					return fmt.Errorf("postReceive %s needs a url template", PostReceiveBinaryData)
				}
			default:
				return fmt.Errorf("postReceive action %q is not supported; use %s, %s, %s or %s",
					action.Type, PostReceiveRootProperty, PostReceiveSetKeyValue, PostReceiveLimit, PostReceiveBinaryData)
			}
		}
	}
	if routing.Operations != nil && routing.Operations.Pagination != nil {
		pagination := routing.Operations.Pagination
		if pagination.Type != PaginationOffset {
			return fmt.Errorf("pagination type %q is not supported; use %s", pagination.Type, PaginationOffset)
		}
		if pagination.Properties.PageSize <= 0 {
			return fmt.Errorf("offset pagination needs a positive pageSize")
		}
		if strings.TrimSpace(pagination.Properties.OffsetParameter) == "" {
			return fmt.Errorf("offset pagination needs an offsetParameter")
		}
		switch pagination.Properties.Type {
		case "body", "query":
		default:
			return fmt.Errorf("offset pagination type %q is not supported; use body or query", pagination.Properties.Type)
		}
	}
	return nil
}
