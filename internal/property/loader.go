package property

import (
	"fmt"
	"strings"
)

// LoaderSource says where a property's selectable values come from.
//
// The distinction is load-bearing rather than cosmetic. An outbound loader
// constructs an HTTP request to somebody else's service, so it is governed by
// the egress policy, the credential's allowed domains and everything else that
// defends against SSRF. An internal loader never constructs a request at all —
// a datastore list or an information_schema lookup happens inside this process
// — so those defences do not apply to it and must not be read as though they
// did. Conflating the two would either apply meaningless checks to an internal
// lookup or, far worse, skip real ones on an outbound call.
type LoaderSource string

const (
	// LoaderHTTP fetches options from a service over the network.
	LoaderHTTP LoaderSource = "http"
	// LoaderInternal reads options from this process.
	LoaderInternal LoaderSource = "internal"
)

// OptionsLoader declares where a property's selectable values come from.
//
// It is data, deliberately. n8n's dominant form names a JavaScript function on
// the node class, which is exactly the thing this project does not have and
// does not want: a loader that could execute would have to run somewhere, and
// nothing that runs arbitrary code can keep the egress policy or the
// credential scoping that this one gets for free.
type OptionsLoader struct {
	Source LoaderSource `json:"source"`
	// Method and Endpoint describe an outbound request. The endpoint may
	// reference dependency values as `{{ key }}`; every substitution is
	// URL-escaped, never concatenated.
	Method   string `json:"method,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	// BaseURLParameter names a parameter holding the service's own base URL,
	// prepended to Endpoint.
	//
	// It is separate from a dependency substitution because the two need
	// opposite treatment: a dependency is a value going *into* a path segment
	// and is always escaped, while a base URL is a URL and escaping it would
	// turn https://host/v1 into one unusable segment. Keeping them apart is
	// what lets "every dependency value is escaped" stay true with no
	// exception to reason about.
	BaseURLParameter string `json:"baseUrlParameter,omitempty"`
	// CredentialType names the credential the request is signed with.
	CredentialType string `json:"credentialType,omitempty"`
	// ItemsPath walks the response to the list, dot-separated. Empty means the
	// response is the list.
	ItemsPath string `json:"itemsPath,omitempty"`
	// LabelTemplate renders each option's label from the item's fields.
	LabelTemplate string `json:"labelTemplate,omitempty"`
	// ValueField names the field each option's value is read from.
	ValueField string `json:"valueField,omitempty"`
	// Name identifies an internal loader, so the server knows which lookup to
	// run without the request naming a function.
	Name string `json:"name,omitempty"`
	// DependsOn lists the parameter keys this loader reads.
	//
	// It is what tells the panel to refetch: when a listed parameter changes,
	// the cached list is discarded. Without it a user changes `resource` and
	// keeps the previous resource's operations.
	DependsOn []string `json:"dependsOn,omitempty"`
}

// ValidateLoader refuses a loader this server cannot honour.
func ValidateLoader(loader *OptionsLoader) error {
	if loader == nil {
		return nil
	}
	switch loader.Source {
	case LoaderHTTP:
		if strings.TrimSpace(loader.Endpoint) == "" {
			return fmt.Errorf("an http options loader needs an endpoint")
		}
		if strings.TrimSpace(loader.ValueField) == "" {
			return fmt.Errorf("an http options loader needs a value field")
		}
	case LoaderInternal:
		if strings.TrimSpace(loader.Name) == "" {
			return fmt.Errorf("an internal options loader needs a name")
		}
	default:
		return fmt.Errorf("options loader source %q is not supported", loader.Source)
	}
	return nil
}
