package credentials

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"sort"
	"strings"

	"github.com/kilaslabs/kilas-flow/internal/property"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
)

// Placement is where a credential puts its secret on an outbound request.
type Placement string

const (
	// PlacementHeader sets a named header.
	PlacementHeader Placement = "header"
	// PlacementQuery sets a named query parameter.
	PlacementQuery Placement = "query"
	// PlacementBasic sends RFC 7617 basic auth.
	PlacementBasic Placement = "basicAuth"
	// PlacementBearer sends an Authorization bearer token.
	PlacementBearer Placement = "bearer"
	// PlacementPath substitutes credential fields into the request's path.
	//
	// Telegram's Bot API puts the token in the path — /bot<token>/sendMessage
	// — which no header or query placement can express. A node writes the
	// marker `{credential.accessToken}` and the substitution happens *here*,
	// inside the package that already holds the secret: the alternative was
	// exposing the token to the expression evaluator through `$credentials`,
	// which carries non-secret fields only and is meant to keep carrying only
	// those.
	//
	// It is also not the same as declaring no authentication, which still means
	// "this credential cannot sign an HTTP request" and is still refused.
	PlacementPath Placement = "path"
)

// credentialPathMarker is what a node writes where a credential field goes.
var credentialPathMarker = regexp.MustCompile(`\{credential\.([A-Za-z0-9_]+)\}`)

// Authentication describes how a credential authenticates a request, as data.
//
// This replaces a switch on the type ID. The point is that a new credential
// type needs no Go change: `wahaApi` is
// `{header, "X-Api-Key", "{{ apiKey }}"}` and nothing else. A type that does
// not authenticate an HTTP request at all — postgres, mysql, sqlite — declares
// none, and Apply refuses it by absence rather than by a default branch, which
// is a better error anyway.
type Authentication struct {
	Placement Placement `json:"placement"`
	// Name is the header or query parameter name, where the placement needs one.
	Name string `json:"name,omitempty"`
	// Value is a template over field keys, written `{{ key }}`.
	Value string `json:"value,omitempty"`
	// User and Password name the fields basic auth draws from.
	User     string `json:"user,omitempty"`
	Password string `json:"password,omitempty"`
}

// TestRequest is an optional probe proving a credential actually works.
type TestRequest struct {
	Method string `json:"method,omitempty"`
	// URL is a template over field keys, so a credential carrying its own base
	// URL can probe its own instance.
	URL string `json:"url"`
	// ExpectStatusBelow passes when the response code is under this value.
	// Zero means "any 2xx or 3xx".
	ExpectStatusBelow int `json:"expectStatusBelow,omitempty"`
}

// Type is one registered credential type.
//
// Its fields are described in the same language a node's parameters use, so a
// credential gets conditional visibility and password masking for free rather
// than through a parallel model that would drift.
type Type struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
	Description string `json:"description,omitempty"`
	// Properties describe the fields in the shared language.
	Properties []property.PropertyDefinition `json:"properties"`
	// Secrets names the fields the API never returns after they are stored.
	//
	// Deliberately separate from `typeOptions.password`, which only masks a
	// field in the UI. Conflating them would make a masked-but-readable field
	// start coming back as the placeholder, and users would overwrite real
	// values with it.
	Secrets []string `json:"secrets,omitempty"`
	// Authenticate says how this type signs an outbound request. Nil means it
	// does not authenticate HTTP at all.
	Authenticate *Authentication `json:"authenticate,omitempty"`
	// Test is an optional probe.
	Test *TestRequest `json:"test,omitempty"`
}

// Registry holds the credential types this installation supports.
//
// A value rather than a package-level map, in the shape of the node registry,
// because a map cannot be extended by a node pack and a pack that needs
// `wahaApi` or `telegramApi` cannot edit this package.
//
// There is a package-level `defaultRegistry` below all the same, built once
// from the built-in types. It exists for callers that have no registry of their
// own to thread through; a deployment that registers pack credentials builds
// its own and passes it. An earlier version of this comment claimed the
// package-level form had been avoided, which was not true of the file it sat
// in.
type Registry struct {
	types map[string]Type
}

// NewRegistry creates an empty credential registry.
func NewRegistry() *Registry { return &Registry{types: map[string]Type{}} }

// Register adds one type during composition.
func (registry *Registry) Register(credentialType Type) error {
	if registry == nil {
		return fmt.Errorf("credential registry is not initialised")
	}
	if strings.TrimSpace(credentialType.ID) == "" || strings.TrimSpace(credentialType.DisplayName) == "" {
		return fmt.Errorf("credential type ID and display name are required")
	}
	if _, exists := registry.types[credentialType.ID]; exists {
		return fmt.Errorf("credential type %q is already registered", credentialType.ID)
	}
	seen := map[string]struct{}{}
	for _, field := range credentialType.Properties {
		if field.Key == "" || field.Label == "" || !property.Known(field.Kind) {
			return fmt.Errorf("credential type %q has an invalid field", credentialType.ID)
		}
		if _, exists := seen[field.Key]; exists {
			return fmt.Errorf("credential type %q has duplicate field %q", credentialType.ID, field.Key)
		}
		seen[field.Key] = struct{}{}
	}
	for _, secret := range credentialType.Secrets {
		if _, declared := seen[secret]; !declared {
			return fmt.Errorf("credential type %q marks unknown field %q as secret", credentialType.ID, secret)
		}
	}
	registry.types[credentialType.ID] = credentialType
	return nil
}

// Get returns one type.
func (registry *Registry) Get(id string) (Type, bool) {
	if registry == nil {
		return Type{}, false
	}
	credentialType, found := registry.types[id]
	return cloneType(credentialType), found
}

// List returns every type in stable order.
func (registry *Registry) List() []Type {
	if registry == nil {
		return []Type{}
	}
	list := make([]Type, 0, len(registry.types))
	for _, credentialType := range registry.types {
		list = append(list, cloneType(credentialType))
	}
	sort.Slice(list, func(left, right int) bool { return list[left].ID < list[right].ID })
	return list
}

func cloneType(credentialType Type) Type {
	credentialType.Properties = append([]property.PropertyDefinition(nil), credentialType.Properties...)
	credentialType.Secrets = append([]string(nil), credentialType.Secrets...)
	if credentialType.Authenticate != nil {
		authentication := *credentialType.Authenticate
		credentialType.Authenticate = &authentication
	}
	if credentialType.Test != nil {
		test := *credentialType.Test
		credentialType.Test = &test
	}
	return credentialType
}

// ApplyAuthentication signs a request from a type's declared descriptor.
//
// A type with no descriptor is refused by absence: postgres and mysql do not
// authenticate an HTTP request, and saying "this type cannot authenticate an
// HTTP request" is a better error than a default branch reached by accident.
func ApplyAuthentication(request *http.Request, credentialType Type, fields map[string]string) error {
	descriptor := credentialType.Authenticate
	if descriptor == nil {
		return fmt.Errorf("credential type %q cannot authenticate an HTTP request", credentialType.ID)
	}
	switch descriptor.Placement {
	case PlacementPath:
		substituted := credentialPathMarker.ReplaceAllStringFunc(request.URL.Path, func(match string) string {
			return fields[credentialPathMarker.FindStringSubmatch(match)[1]]
		})
		if substituted == request.URL.Path {
			return fmt.Errorf("credential type %q expects a {credential.…} marker in the request path", credentialType.ID)
		}
		// RawPath is cleared so the URL re-encodes from the substituted Path;
		// leaving it would send the marker.
		request.URL.Path, request.URL.RawPath = substituted, ""
	case PlacementBasic:
		request.SetBasicAuth(fields[descriptor.User], fields[descriptor.Password])
	case PlacementBearer:
		request.Header.Set("Authorization", "Bearer "+expand(descriptor.Value, fields))
	case PlacementHeader:
		name := strings.TrimSpace(expand(descriptor.Name, fields))
		if name == "" {
			return fmt.Errorf("credential header name is empty")
		}
		request.Header.Set(name, expand(descriptor.Value, fields))
	case PlacementQuery:
		name := strings.TrimSpace(expand(descriptor.Name, fields))
		if name == "" {
			return fmt.Errorf("credential query parameter name is empty")
		}
		query := request.URL.Query()
		query.Set(name, expand(descriptor.Value, fields))
		request.URL.RawQuery = query.Encode()
	default:
		return fmt.Errorf("credential type %q declares unsupported placement %q", credentialType.ID, descriptor.Placement)
	}
	return nil
}

// expand substitutes `{{ key }}` references from the credential's own fields.
//
// Deliberately not the expression evaluator: a descriptor is written by whoever
// declares the credential type, and the full grammar would let it read run-time
// data while signing a request.
func expand(template string, fields map[string]string) string {
	result := template
	for key, value := range fields {
		result = strings.ReplaceAll(result, "{{ "+key+" }}", value)
		result = strings.ReplaceAll(result, "{{"+key+"}}", value)
	}
	return result
}

// Fields renders a type's properties in the older flat shape.
//
// The API and the editor read a credential type as a list of fields with a
// `secret` flag, and that contract is unchanged by the properties underneath
// becoming the shared language. Masking and non-disclosure stay two separate
// flags: `typeOptions.password` hides a field in the UI, `Secrets` stops the
// API ever returning it. A field that is masked but readable must keep coming
// back with its real value, or users overwrite it with the placeholder.
func (credentialType Type) Fields() []Field {
	secret := make(map[string]struct{}, len(credentialType.Secrets))
	for _, key := range credentialType.Secrets {
		secret[key] = struct{}{}
	}
	fields := make([]Field, 0, len(credentialType.Properties))
	for _, declared := range credentialType.Properties {
		_, withheld := secret[declared.Key]
		fallback, _ := declared.Default.(string)
		fields = append(fields, Field{
			Key: declared.Key, Label: declared.Label, Description: declared.Description,
			Required: declared.Required, Default: fallback, Secret: withheld,
		})
	}
	return fields
}

// Definition renders a type in the shape the existing API returns.
func (credentialType Type) Definition() Definition {
	return Definition{
		ID: credentialType.ID, DisplayName: credentialType.DisplayName,
		Description: credentialType.Description, Fields: credentialType.Fields(),
	}
}

// defaultRegistry holds the built-in types.
//
// It exists so the package-level Lookup, List, Validate and Apply keep working
// while call sites are threaded to an explicit registry. It is built once and
// never mutated afterwards, which is the property the package-level map could
// not offer: a map is writable by anything that can see it.
var defaultRegistry = func() *Registry {
	registry := NewRegistry()
	if err := RegisterAll(registry); err != nil {
		panic(fmt.Sprintf("built-in credential types are invalid: %v", err))
	}
	return registry
}()

// Default is the built-in credential registry.
func Default() *Registry { return defaultRegistry }

// RunTest probes a credential through the instance's egress policy.
//
// The credential's own AllowedDomains narrow the policy further, so a scoped
// credential cannot be used to reach anywhere its workflows could not.
func RunTest(ctx context.Context, credentialType Type, record Record, fields map[string]string, policy safehttp.Policy) (string, error) {
	if credentialType.Test == nil {
		return "", fmt.Errorf("this credential type has no test defined")
	}
	target := expand(credentialType.Test.URL, fields)
	if target == "" {
		return "", fmt.Errorf("this credential type's test has no URL")
	}
	parsed, err := url.Parse(target)
	if err != nil {
		return "", fmt.Errorf("the test URL is not a valid URL")
	}
	if !record.AllowsHost(parsed.Hostname()) {
		return "", fmt.Errorf("this credential is not allowed to reach %s", parsed.Hostname())
	}

	method := strings.ToUpper(credentialType.Test.Method)
	if method == "" {
		method = http.MethodGet
	}
	request, err := http.NewRequestWithContext(ctx, method, target, nil)
	if err != nil {
		return "", fmt.Errorf("the test request could not be built")
	}
	if credentialType.Authenticate != nil {
		if err := ApplyAuthentication(request, credentialType, fields); err != nil {
			return "", err
		}
	}

	response, err := safehttp.NewClient(policy).Do(request)
	if err != nil {
		return "", fmt.Errorf("the service could not be reached")
	}
	defer response.Body.Close()
	// The body is drained and discarded rather than read: returning it would
	// turn pass/fail into a general-purpose fetch.
	_, _, _ = policy.ReadBody(response.Body)

	limit := credentialType.Test.ExpectStatusBelow
	if limit == 0 {
		limit = 400
	}
	if response.StatusCode >= limit {
		return "", fmt.Errorf("the service answered %d", response.StatusCode)
	}
	return fmt.Sprintf("the service answered %d", response.StatusCode), nil
}
