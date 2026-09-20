package cli

import (
	"net/url"
	"strings"
)

// apiPrefix is the path prefix every operation shares. The stage-2
// contract-walk test asserts apiPrefix == api.APIPrefix, so the CLI cannot
// drift from the server it drives.
const apiPrefix = "/api/v1"

// apiVersion is the version of the API the CLI speaks, as reported by
// `kilasflow version`.
const apiVersion = "v1"

// apiPath turns an operation-relative path into a server-absolute one.
func apiPath(path string) string { return apiPrefix + path }

// normalizeBaseURL trims a trailing slash and rejects anything that is not an
// absolute http(s) URL.
//
// A relative or malformed base is a usage mistake, not a server error: the
// caller typed something the CLI cannot act on, which is exit 2.
func normalizeBaseURL(base string) (string, error) {
	trimmed := strings.TrimSpace(base)
	if trimmed == "" {
		return "", usageError("no server URL: pass --url or set %s", envURLVar)
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return "", usageError("invalid server URL %q: %v", trimmed, err)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", usageError("invalid server URL %q: the scheme must be http or https", trimmed)
	}
	if parsed.Host == "" {
		return "", usageError("invalid server URL %q: it has no host", trimmed)
	}

	return strings.TrimRight(trimmed, "/"), nil
}
