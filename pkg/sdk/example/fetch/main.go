// Command fetch is the example pack that uses the SDK's capabilities.
//
// It is the integration fixture for the pack loader: a node definition that
// declares the http and credentials capabilities, a credential attached to the
// node, and a module that asks the host to make the request. Built exactly the
// way a pack author's module is built:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o fetch.wasm ./pkg/sdk/example/fetch
//
// Its pack.json beside this file is a template: the .wasm is not committed, so
// seal the manifest before an installation can load it.
package main

import (
	"errors"
	"fmt"
	"strings"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
)

// run fetches the configured URL once per input item.
func run(call sdk.Call) ([]sdk.Item, error) {
	url, _ := call.Parameters["url"].(string)
	if strings.TrimSpace(url) == "" {
		return nil, fmt.Errorf("the url parameter is required")
	}
	credential, _ := call.Parameters["credential"].(string)
	if strings.TrimSpace(credential) == "" {
		return nil, fmt.Errorf("the credential parameter is required")
	}

	// A non-secret field of the credential the node attached: the base URL its
	// API lives at. Reading a secret field is refused by design — the host
	// applies secrets to the request itself and never puts one in this
	// module's memory.
	base, err := sdk.CredentialField(credential, "baseUrl")
	switch {
	case err == nil:
		if base != "" {
			url = strings.TrimRight(base, "/") + "/" + strings.TrimLeft(url, "/")
		}
	case errors.Is(err, sdk.ErrNotFoundError):
		// The credential carries no base URL, so the url parameter is used as
		// the author wrote it.
	default:
		return nil, fmt.Errorf("reading the credential: %w", err)
	}

	out := make([]sdk.Item, 0, len(call.Items))
	for range call.Items {
		response, body, err := sdk.HTTP(sdk.HTTPRequest{
			Method:     "GET",
			URL:        url,
			Credential: credential,
			TimeoutMS:  10000,
		}, nil)
		if err != nil {
			return nil, fmt.Errorf("fetching %s: %w", url, err)
		}
		out = append(out, sdk.Item{JSON: map[string]any{
			"status":    response.Status,
			"body":      string(body),
			"truncated": response.Truncated,
		}})
	}
	return out, nil
}

func main() { sdk.MainCall(run) }
