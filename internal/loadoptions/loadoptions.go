// Package loadoptions resolves a property's selectable values at edit time.
//
// The execution half lives here rather than in the HTTP handler because the
// declarative routing interpreter needs exactly the same shape — build a
// request from a descriptor, sign it with a credential, walk a path into the
// response — and two copies of that would drift.
package loadoptions

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// Option is one selectable value.
type Option struct {
	Label string `json:"label"`
	Value string `json:"value"`
}

// Result is a resolved list plus the reason it may be empty.
type Result struct {
	Options []Option `json:"options"`
	// Reason explains an empty list the user can do something about, rather
	// than leaving an empty dropdown that reads as "this service has nothing".
	Reason string `json:"reason,omitempty"`
}

// CredentialResolver resolves a credential by ID under a tenant scope.
//
// Taken as an interface so this package cannot reach the store directly, and so
// the caller is the one that decides which tenant the lookup happens under.
type CredentialResolver interface {
	Resolve(ctx context.Context, credentialID string) (credentials.Record, map[string]string, error)
}

// InternalLoader answers a loader that reads from this process.
//
// Registered by name so the request never names a function. Its results are
// scoped by the caller, not by this package: an internal lookup has no egress
// policy to hide behind, so the scoping has to be explicit at the point that
// knows what the caller is allowed to see.
type InternalLoader func(ctx context.Context, scope Scope) (Result, error)

// Scope is who is asking and what they may see.
type Scope struct {
	TenantID string
	// WorkflowID bounds an embed session to its own workflow.
	//
	// Empty means an unrestricted caller. This is not a nicety: the embed
	// middleware allows the whole /node-types/ subtree on read scope with no
	// workflow check, so without this an internal loader would let a session
	// scoped to one workflow enumerate every datastore in the tenant and every
	// table reachable by any credential in it.
	WorkflowID string
	// Dependencies are the resolved values of the parameters the loader reads.
	Dependencies map[string]string
}

// Resolver runs loaders.
type Resolver struct {
	policy    safehttp.Policy
	internals map[string]InternalLoader
	cache     *cache
}

// NewResolver builds a resolver with a bounded result cache.
func NewResolver(policy safehttp.Policy, ttl time.Duration) *Resolver {
	if ttl <= 0 {
		ttl = 30 * time.Second
	}
	return &Resolver{policy: policy, internals: map[string]InternalLoader{}, cache: newCache(ttl)}
}

// RegisterInternal binds an internal loader by name.
func (resolver *Resolver) RegisterInternal(name string, loader InternalLoader) error {
	if resolver == nil || name == "" || loader == nil {
		return fmt.Errorf("internal options loader name and implementation are required")
	}
	if _, exists := resolver.internals[name]; exists {
		return fmt.Errorf("internal options loader %q is already registered", name)
	}
	resolver.internals[name] = loader
	return nil
}

// Load resolves one property's options.
//
// The loader comes from the *registered definition*, never from the request:
// the body carries an unsaved, partially configured node straight from a
// browser, so the node type, the property and every parameter value are
// attacker-controlled, and the server then makes an outbound request shaped by
// them.
func (resolver *Resolver) Load(
	ctx context.Context,
	loader property.OptionsLoader,
	scope Scope,
	credentialID string,
	resolve CredentialResolver,
) (Result, error) {
	key := cacheKey(loader, scope, credentialID)
	if cached, found := resolver.cache.get(key); found {
		return cached, nil
	}

	var result Result
	var err error
	switch loader.Source {
	case property.LoaderInternal:
		internal, registered := resolver.internals[loader.Name]
		if !registered {
			return Result{}, fmt.Errorf("this field's option source is not available on this server")
		}
		result, err = internal(ctx, scope)
	case property.LoaderHTTP:
		result, err = resolver.loadHTTP(ctx, loader, scope, credentialID, resolve)
	default:
		return Result{}, fmt.Errorf("this field's option source is not supported")
	}
	if err != nil {
		return Result{}, err
	}
	resolver.cache.put(key, result)
	return result, nil
}

func (resolver *Resolver) loadHTTP(
	ctx context.Context,
	loader property.OptionsLoader,
	scope Scope,
	credentialID string,
	resolve CredentialResolver,
) (Result, error) {
	endpoint, err := buildEndpoint(loader, scope.Dependencies)
	if err != nil {
		return Result{}, err
	}

	// The same pre-flight gate the HTTP node applies. Dialling alone would
	// have refused a disallowed host only once DNS resolved it, so an
	// unresolvable one failed with a lookup error rather than the policy
	// refusal — and a host the policy forbids but DNS answers would have been
	// contacted before being refused.
	target, err := url.Parse(endpoint)
	if err != nil {
		return Result{}, fmt.Errorf("this field's option source is not a valid URL")
	}
	if err := resolver.policy.CheckURL(target); err != nil {
		return Result{}, err
	}

	method := strings.ToUpper(loader.Method)
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, endpoint, nil)
	if err != nil {
		return Result{}, fmt.Errorf("this field's option request could not be built")
	}

	policy := resolver.policy
	if loader.CredentialType != "" {
		if credentialID == "" || resolve == nil {
			return Result{Reason: "attach a credential to load this field's options"}, nil
		}
		record, fields, err := resolve.Resolve(ctx, credentialID)
		if err != nil {
			// Resolved under the caller's tenant, so naming another tenant's
			// credential resolves to nothing rather than to a secret.
			return Result{Reason: "this credential could not be read"}, nil
		}
		if record.Type != loader.CredentialType {
			return Result{Reason: fmt.Sprintf("this field needs a %s credential", loader.CredentialType)}, nil
		}
		if !record.AllowsHost(target.Hostname()) {
			return Result{}, fmt.Errorf("request target is not allowed: this credential cannot reach that host")
		}
		credentialType, known := credentials.Default().Get(record.Type)
		if !known {
			return Result{Reason: "this credential's type is not registered on this server"}, nil
		}
		if err := credentials.ApplyAuthentication(request, credentialType, fields); err != nil {
			return Result{}, err
		}
	}

	response, err := safehttp.NewClient(policy).Do(request)
	if err != nil {
		// safehttp's own refusal text is passed through, so a loader aimed at a
		// disallowed host fails with the error an HTTP node would produce.
		return Result{}, err
	}
	defer response.Body.Close()
	body, _, err := policy.ReadBody(response.Body)
	if err != nil {
		return Result{}, fmt.Errorf("this field's option list could not be read")
	}
	if response.StatusCode >= 400 {
		// The remote body is never echoed: it may carry whatever the credential
		// just authenticated against.
		return Result{}, fmt.Errorf("the service answered %d", response.StatusCode)
	}

	var decoded any
	if err := json.Unmarshal(body, &decoded); err != nil {
		return Result{}, fmt.Errorf("this field's option list was not JSON")
	}
	return Result{Options: renderOptions(decoded, loader)}, nil
}

// buildEndpoint substitutes dependency values with URL escaping.
//
// Every value is escaped rather than concatenated: the parameters come from a
// browser, and a value that could reshape the path would let a caller point the
// server anywhere the policy happens to allow.
func buildEndpoint(loader property.OptionsLoader, dependencies map[string]string) (string, error) {
	result := loader.Endpoint
	if loader.BaseURLParameter != "" {
		base := strings.TrimRight(dependencies[loader.BaseURLParameter], "/")
		if base == "" {
			return "", fmt.Errorf("this field's options need the service address to be filled in first")
		}
		// Validated as a URL rather than escaped: it *is* a URL, and escaping
		// would make it one unusable segment.
		parsedBase, err := url.Parse(base)
		if err != nil || parsedBase.Scheme == "" || parsedBase.Host == "" {
			return "", fmt.Errorf("this field's service address is not a valid URL")
		}
		result = base + result
	}
	for key, value := range dependencies {
		if key == loader.BaseURLParameter {
			continue
		}
		result = strings.ReplaceAll(result, "{{ "+key+" }}", url.PathEscape(value))
		result = strings.ReplaceAll(result, "{{"+key+"}}", url.PathEscape(value))
	}
	if strings.Contains(result, "{{") {
		return "", fmt.Errorf("this field's options need another field to be filled in first")
	}
	parsed, err := url.Parse(result)
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("this field's option source is not a valid URL")
	}
	return result, nil
}

func renderOptions(decoded any, loader property.OptionsLoader) []Option {
	items := walk(decoded, loader.ItemsPath)
	list, isList := items.([]any)
	if !isList {
		return []Option{}
	}
	options := make([]Option, 0, len(list))
	for _, entry := range list {
		fields, isObject := entry.(map[string]any)
		if !isObject {
			// A bare list of strings is a perfectly ordinary shape.
			options = append(options, Option{Label: fmt.Sprint(entry), Value: fmt.Sprint(entry)})
			continue
		}
		value := fmt.Sprint(fields[loader.ValueField])
		label := value
		if loader.LabelTemplate != "" {
			label = renderTemplate(loader.LabelTemplate, fields)
		}
		options = append(options, Option{Label: label, Value: value})
	}
	return options
}

func walk(value any, path string) any {
	if strings.TrimSpace(path) == "" {
		return value
	}
	current := value
	for _, step := range strings.Split(path, ".") {
		object, isObject := current.(map[string]any)
		if !isObject {
			return nil
		}
		current = object[step]
	}
	return current
}

func renderTemplate(template string, fields map[string]any) string {
	result := template
	for key, value := range fields {
		result = strings.ReplaceAll(result, "{{ "+key+" }}", fmt.Sprint(value))
		result = strings.ReplaceAll(result, "{{"+key+"}}", fmt.Sprint(value))
	}
	return result
}

func cacheKey(loader property.OptionsLoader, scope Scope, credentialID string) string {
	parts := []string{scope.TenantID, scope.WorkflowID, string(loader.Source), loader.Name, loader.Endpoint, credentialID}
	for _, key := range loader.DependsOn {
		parts = append(parts, key+"="+scope.Dependencies[key])
	}
	return strings.Join(parts, "\x00")
}

// cache is a bounded in-process TTL cache.
//
// A dropdown that fires a request to the customer's WAHA box on every open is
// the kind of thing that gets noticed in production and not in review.
type cache struct {
	mutex   sync.Mutex
	ttl     time.Duration
	entries map[string]cacheEntry
}

type cacheEntry struct {
	result  Result
	expires time.Time
}

func newCache(ttl time.Duration) *cache {
	return &cache{ttl: ttl, entries: map[string]cacheEntry{}}
}

func (store *cache) get(key string) (Result, bool) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	entry, found := store.entries[key]
	if !found || time.Now().After(entry.expires) {
		delete(store.entries, key)
		return Result{}, false
	}
	return entry.result, true
}

func (store *cache) put(key string, result Result) {
	store.mutex.Lock()
	defer store.mutex.Unlock()
	// A crude bound beats an unbounded map: the keys include tenant and
	// dependency values, so a hostile caller could otherwise grow it without
	// limit.
	if len(store.entries) > 1024 {
		store.entries = map[string]cacheEntry{}
	}
	store.entries[key] = cacheEntry{result: result, expires: time.Now().Add(store.ttl)}
}
