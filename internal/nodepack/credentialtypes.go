// Credential types a node pack declares for itself (FEAT-n12211).
//
// A pack that calls a host's own API needs a credential shaped for that API —
// a base URL, an API key, a header — and until this file a pack could only
// name one of the types compiled into the binary. The registry was already
// open (credentials.Registry.Register); what was missing was a manifest block
// that reaches it. A declaration here is converted into exactly the
// credentials.Type a built-in would be, so a pack's type is listed by
// GET /credential-types, signs routed and lifecycle requests, runs its test
// and carries a host scope the same way a built-in does.
//
// Two rules keep the global catalogue sane:
//
//   - Namespace. A pack's type ID is `<pack type>.<word>`, as in
//     `pack.acme.api` for the pack `pack.acme`. No built-in ID has a dot, so a
//     pack cannot shadow one, and two packs cannot claim each other's names.
//   - Immutability. A released ID is a contract with every stored credential
//     of that type. The loader enforces the part it can see: two installed
//     packs (two versions of one pack, typically) that declare the same ID must
//     declare it identically, or the boot is refused. The part it cannot see —
//     never changing a released ID's field keys or secret flags in a later
//     release — is documented rather than enforced.
package nodepack

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/property"
)

// CredentialType is one credential type a pack declares.
//
// It is credentials.Type minus what a pack may not say: a pack cannot declare
// a dial probe or claim its secret is never sent over HTTP, because both
// relax what the host checks. Fields are a flat list in the property language
// with a `secret` flag, the same shape GET /credential-types returns.
type CredentialType struct {
	ID          string            `json:"id"`
	DisplayName string            `json:"displayName"`
	Description string            `json:"description,omitempty"`
	Fields      []CredentialField `json:"fields"`
	// Authenticate says how the type signs an outbound request. Absent means
	// it does not sign one, which is still useful for a type that only holds
	// a trigger's HMAC secret.
	Authenticate *credentials.Authentication `json:"authenticate,omitempty"`
	// Test is an optional probe proving a stored credential works.
	Test *credentials.TestRequest `json:"test,omitempty"`
	// DefaultDomains and DefaultDomainsFrom are the host scope a credential of
	// this type carries when its owner saved none. Without one, a credential
	// is unscoped and an embedded guest cannot use it.
	DefaultDomains     []string `json:"defaultDomains,omitempty"`
	DefaultDomainsFrom string   `json:"defaultDomainsFrom,omitempty"`
}

// CredentialField is one field of a pack's credential type.
type CredentialField struct {
	Key         string                    `json:"key"`
	Label       string                    `json:"label"`
	Description string                    `json:"description,omitempty"`
	Kind        property.Kind             `json:"kind"`
	Required    bool                      `json:"required,omitempty"`
	Default     any                       `json:"default,omitempty"`
	Options     []property.PropertyOption `json:"options,omitempty"`
	TypeOptions *property.TypeOptions     `json:"typeOptions,omitempty"`
	// Secret makes the API never return the field after it is stored, and
	// masks it in the editor. A field can be masked and still readable — set
	// typeOptions.password without secret — which is the right choice for an
	// account ID that is sensitive on screen but needed back on edit.
	Secret bool `json:"secret,omitempty"`
}

// credentialTypeIDLimit is the width of the stored credential's type column.
const credentialTypeIDLimit = 64

// credentialTypeName is the word after the pack's own type in an ID.
var credentialTypeName = regexp.MustCompile(`^[A-Za-z][A-Za-z0-9]*$`)

// credentialFieldKinds are the kinds a stored credential field can hold: a
// credential's values are strings at rest, so only scalar controls fit.
var credentialFieldKinds = []property.Kind{property.KindString, property.KindNumber, property.KindBoolean, property.KindOptions}

// credentialPlacements are the placements a pack may declare.
var credentialPlacements = []credentials.Placement{
	credentials.PlacementHeader, credentials.PlacementQuery, credentials.PlacementBasic,
	credentials.PlacementBearer, credentials.PlacementPath, credentials.PlacementCustom,
}

// toType converts a declaration into the registry's own type.
func (declared CredentialType) toType() credentials.Type {
	converted := credentials.Type{
		ID: declared.ID, DisplayName: declared.DisplayName, Description: declared.Description,
		DefaultDomains:     append([]string(nil), declared.DefaultDomains...),
		DefaultDomainsFrom: declared.DefaultDomainsFrom,
	}
	for _, field := range declared.Fields {
		definition := property.PropertyDefinition{
			Key: field.Key, Label: field.Label, Description: field.Description,
			Kind: field.Kind, Required: field.Required, Default: field.Default,
			Options: append([]property.PropertyOption(nil), field.Options...),
		}
		if field.TypeOptions != nil {
			typeOptions := *field.TypeOptions
			definition.TypeOptions = &typeOptions
		}
		if field.Secret {
			// A secret is always masked: a value the API never returns must
			// not sit on screen in the clear while it is typed either.
			if definition.TypeOptions == nil {
				definition.TypeOptions = &property.TypeOptions{}
			}
			definition.TypeOptions.Password = true
			converted.Secrets = append(converted.Secrets, field.Key)
		}
		converted.Properties = append(converted.Properties, definition)
	}
	if declared.Authenticate != nil {
		authentication := *declared.Authenticate
		converted.Authenticate = &authentication
	}
	if declared.Test != nil {
		test := *declared.Test
		converted.Test = &test
	}
	return converted
}

// checkCredentialTypes reports every problem in a pack's credentialTypes block,
// each at its JSON path, and the two places the rest of the manifest refers to
// the pack's own types.
func checkCredentialTypes(pack *Pack) []Issue {
	var issues []Issue
	fail := func(path, format string, args ...any) {
		issues = append(issues, Issue{Path: path, Message: fmt.Sprintf(format, args...)})
	}
	prefix := strings.TrimSpace(pack.Type) + "."
	declared := map[string]CredentialType{}
	for i, credentialType := range pack.CredentialTypes {
		base := fmt.Sprintf("$.credentialTypes[%d]", i)
		before := len(issues)
		id := credentialType.ID
		switch {
		case strings.TrimSpace(id) == "":
			fail(base+".id", "a credential type needs an id")
		case strings.TrimSpace(pack.Type) != "" && (!strings.HasPrefix(id, prefix) || !credentialTypeName.MatchString(strings.TrimPrefix(id, prefix))):
			fail(base+".id", "credential type %q is outside this pack's namespace: name it %q followed by one word of letters and digits, such as %q",
				id, prefix, prefix+"api")
		case len(id) > credentialTypeIDLimit:
			fail(base+".id", "credential type %q is %d characters long, and an id may be at most %d", id, len(id), credentialTypeIDLimit)
		}
		if _, twice := declared[id]; twice && id != "" {
			fail(base+".id", "node pack %q declares credential type %q twice", pack.Type, id)
		}
		if strings.TrimSpace(credentialType.DisplayName) == "" {
			fail(base+".displayName", "credential type %q needs a display name", id)
		}
		if len(credentialType.Fields) == 0 {
			fail(base+".fields", "credential type %q declares no fields", id)
		}
		keys := map[string]bool{}
		secret := map[string]bool{}
		for j, field := range credentialType.Fields {
			at := fmt.Sprintf("%s.fields[%d]", base, j)
			switch {
			case strings.TrimSpace(field.Key) == "":
				fail(at+".key", "a credential field needs a key")
			case keys[field.Key]:
				fail(at+".key", "credential type %q declares field %q twice", id, field.Key)
			case strings.HasPrefix(field.Key, "$") || strings.Contains(field.Key, ","):
				fail(at+".key", "field %q may not start with $ or contain a comma: the credential store keeps its own bookkeeping under those", field.Key)
			}
			keys[field.Key] = true
			secret[field.Key] = field.Secret
			if strings.TrimSpace(field.Label) == "" {
				fail(at+".label", "field %q needs a label", field.Key)
			}
			if !kindIn(field.Kind, credentialFieldKinds) {
				fail(at+".kind", "field %q has kind %q: a credential field is one of %s", field.Key, string(field.Kind), kindNames(credentialFieldKinds))
			}
			if field.Kind == property.KindOptions && len(field.Options) == 0 {
				fail(at+".options", "field %q is an options field and declares no options", field.Key)
			}
			if field.Kind != property.KindOptions && len(field.Options) > 0 {
				fail(at+".options", "field %q declares options, which only an options field has", field.Key)
			}
			if field.Default != nil {
				if _, isString := field.Default.(string); !isString {
					fail(at+".default", "field %q has a default that is not a string: a stored credential value is a string, so write it as one", field.Key)
				}
			}
		}
		checkMarkers := func(path, template string) {
			for _, key := range credentials.TemplateFields(template) {
				if !keys[key] {
					fail(path, "{{ %s }} names no field of credential type %q", key, id)
				}
			}
		}
		if auth := credentialType.Authenticate; auth != nil {
			at := base + ".authenticate"
			if !placementIn(auth.Placement, credentialPlacements) {
				fail(at+".placement", "placement %q is not one of %s", string(auth.Placement), placementNames())
			}
			switch auth.Placement {
			case credentials.PlacementHeader, credentials.PlacementQuery:
				if strings.TrimSpace(auth.Name) == "" {
					fail(at+".name", "a %s placement needs a name", auth.Placement)
				}
				if strings.TrimSpace(auth.Value) == "" {
					fail(at+".value", "a %s placement needs a value", auth.Placement)
				}
			case credentials.PlacementBearer, credentials.PlacementCustom:
				if strings.TrimSpace(auth.Value) == "" {
					fail(at+".value", "a %s placement needs a value", auth.Placement)
				}
			case credentials.PlacementBasic:
				if !keys[auth.User] {
					fail(at+".user", "basicAuth user must name a field of credential type %q, and %q does not", id, auth.User)
				}
				if !keys[auth.Password] {
					fail(at+".password", "basicAuth password must name a field of credential type %q, and %q does not", id, auth.Password)
				}
			}
			checkMarkers(at+".name", auth.Name)
			checkMarkers(at+".value", auth.Value)
		}
		if test := credentialType.Test; test != nil {
			at := base + ".test"
			if strings.TrimSpace(test.URL) == "" {
				fail(at+".url", "a credential test needs a url")
			}
			checkMarkers(at+".url", test.URL)
			if test.ExpectStatusBelow < 0 || test.ExpectStatusBelow > 599 {
				fail(at+".expectStatusBelow", "expectStatusBelow must be an HTTP status between 0 and 599, not %d", test.ExpectStatusBelow)
			}
		}
		if from := credentialType.DefaultDomainsFrom; from != "" {
			switch {
			case !keys[from]:
				fail(base+".defaultDomainsFrom", "defaultDomainsFrom names %q, which is not a field of credential type %q", from, id)
			case secret[from]:
				fail(base+".defaultDomainsFrom", "defaultDomainsFrom names %q, which is secret: the scope is read back and shown, so it must come from a readable field", from)
			}
			if len(credentialType.DefaultDomains) > 0 {
				fail(base+".defaultDomains", "declare defaultDomains or defaultDomainsFrom, not both")
			}
		}
		for j, domain := range credentialType.DefaultDomains {
			if strings.TrimSpace(domain) == "" || strings.Contains(domain, "/") {
				fail(fmt.Sprintf("%s.defaultDomains[%d]", base, j), "a default domain is a host name such as %q, not %q", "api.example.com", domain)
			}
		}
		// The registry's own rules last, and only on a declaration that passed
		// every rule above, so its one-line refusal never repeats one of them.
		if len(issues) == before {
			if err := credentials.NewRegistry().Register(credentialType.toType()); err != nil {
				fail(base, "%v", err)
			}
		}
		if id != "" {
			declared[id] = credentialType
		}
	}
	if own := pack.CredentialType; own != "" && strings.TrimSpace(pack.Type) != "" && strings.HasPrefix(own, prefix) {
		if _, found := declared[own]; !found {
			fail("$.credentialType", "credential type %q is in this pack's namespace, so the pack must declare it under credentialTypes", own)
		}
	}
	if pack.Trigger != nil && pack.Trigger.HMAC != nil && pack.Trigger.HMAC.SecretCredential != nil {
		secretCredential := pack.Trigger.HMAC.SecretCredential
		if credentialType, found := declared[secretCredential.Type]; found {
			known := false
			for _, field := range credentialType.Fields {
				known = known || field.Key == secretCredential.Field
			}
			if !known {
				fail("$.trigger.hmac.secretCredential.field", "field %q is not declared by credential type %q", secretCredential.Field, secretCredential.Type)
			}
		}
	}
	return issues
}

// packCredentialTypes returns a pack's declared types converted for the
// registry, or the first problem with them.
func packCredentialTypes(pack *Pack) ([]credentials.Type, error) {
	if issues := checkCredentialTypes(pack); len(issues) > 0 {
		return nil, fmt.Errorf("%s: %s", issues[0].Path, issues[0].Message)
	}
	types := make([]credentials.Type, 0, len(pack.CredentialTypes))
	for _, declared := range pack.CredentialTypes {
		types = append(types, declared.toType())
	}
	return types, nil
}

// withOwnCredentialTypes resolves the pack's own declarations before falling
// back to next, so `nodepackgen validate` — which has not registered them
// anywhere — judges a module pack's credentials the way the server will.
func withOwnCredentialTypes(pack *Pack, next credentialLookup) credentialLookup {
	if len(pack.CredentialTypes) == 0 {
		return next
	}
	own := map[string]credentials.Type{}
	for _, declared := range pack.CredentialTypes {
		if declared.ID != "" {
			own[declared.ID] = declared.toType()
		}
	}
	return func(id string) (credentials.Type, bool) {
		if credentialType, found := own[id]; found {
			return credentialType, true
		}
		return next(id)
	}
}

// credentialTypeOwner is the pack that first registered a type and what it
// declared, so a second declaration can be compared with it.
type credentialTypeOwner struct {
	pack        string
	declaration []byte
}

// registerPackCredentialTypes registers every directory pack's credential
// types, before any pack is installed, so a pack can name a type another pack
// declares whatever order the directory lists them in.
//
// It follows the community-package path in internal/sidecarnode: a duplicate
// is refused naming both owners, and a type the deployment already registered
// is refused naming its display name. The one difference is that two packs
// declaring the same ID identically are accepted once, because that is what
// two installed versions of one pack look like.
func registerPackCredentialTypes(registry *credentials.Registry, loaded []loadedPack) error {
	owners := map[string]credentialTypeOwner{}
	for _, one := range loaded {
		types, err := packCredentialTypes(one.pack)
		if err != nil {
			return fmt.Errorf("load node pack %q: %w", one.name, err)
		}
		if len(types) == 0 {
			continue
		}
		if registry == nil {
			return fmt.Errorf("load node pack %q: this server supplied no credential registry, so the pack's credentialTypes cannot be registered", one.name)
		}
		for index, credentialType := range types {
			declaration, err := json.Marshal(one.pack.CredentialTypes[index])
			if err != nil {
				return fmt.Errorf("load node pack %q: credential type %q: %w", one.name, credentialType.ID, err)
			}
			if owner, taken := owners[credentialType.ID]; taken {
				if string(owner.declaration) == string(declaration) {
					continue
				}
				return fmt.Errorf("credential type %q is declared differently by node pack %q and node pack %q: a released credential type is immutable, so give the changed declaration a new id",
					credentialType.ID, owner.pack, one.name)
			}
			if existing, found := registry.Get(credentialType.ID); found {
				return fmt.Errorf("credential type %q from node pack %q is already registered by this deployment as %q",
					credentialType.ID, one.name, existing.DisplayName)
			}
			if err := registry.Register(credentialType); err != nil {
				return fmt.Errorf("load node pack %q: %w", one.name, err)
			}
			owners[credentialType.ID] = credentialTypeOwner{pack: one.name, declaration: declaration}
		}
	}
	return nil
}

func kindIn(kind property.Kind, kinds []property.Kind) bool {
	for _, candidate := range kinds {
		if candidate == kind {
			return true
		}
	}
	return false
}

func kindNames(kinds []property.Kind) string {
	names := make([]string, 0, len(kinds))
	for _, kind := range kinds {
		names = append(names, string(kind))
	}
	return strings.Join(names, ", ")
}

func placementIn(placement credentials.Placement, placements []credentials.Placement) bool {
	for _, candidate := range placements {
		if candidate == placement {
			return true
		}
	}
	return false
}

func placementNames() string {
	names := make([]string, 0, len(credentialPlacements))
	for _, placement := range credentialPlacements {
		names = append(names, string(placement))
	}
	return strings.Join(names, ", ")
}
