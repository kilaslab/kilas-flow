// Vault KV v2 over plain HTTP, through the instance egress policy.
//
// No vendor SDK on purpose: the KV v2 read is one GET with a token header,
// and going through safehttp inherits the SSRF policy and egress control
// every other outbound request already obeys. A Vault address on loopback or a
// private network needs the same explicit grant any workflow HTTP call would —
// an allowed_private_endpoints entry — and never allow_private_networks.
package credentials

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/safehttp"
)

// VaultProviderName is the provider name bindings use for HashiCorp Vault.
const VaultProviderName = "vault"

// VaultProvider reads secrets from a Vault KV v2 mount. The key is the secret
// path without the mount's data/ prefix (prod/api-token), and the value is
// the secret's "value" field: {"data":{"data":{"value":"…"}}}. One field keeps
// the reference a single string; a secret with several fields exposes the one
// workflows need under "value".
type VaultProvider struct {
	address string
	token   string
	client  *http.Client
	policy  safehttp.Policy
}

// NewVaultProvider builds a Vault provider. The address must be http(s); the
// token is used as given, so whatever form the operator's auth method emits
// arrives untouched.
func NewVaultProvider(address, token string, policy safehttp.Policy) (*VaultProvider, error) {
	parsed, err := url.Parse(strings.TrimSpace(address))
	if err != nil || parsed.Host == "" {
		return nil, fmt.Errorf("vault address %q is not a URL", address)
	}
	switch strings.ToLower(parsed.Scheme) {
	case "http", "https":
	default:
		return nil, fmt.Errorf("vault address %q must be http(s)", address)
	}
	if err := policy.CheckURL(parsed); err != nil {
		return nil, err
	}
	if strings.TrimSpace(token) == "" {
		return nil, fmt.Errorf("vault needs a token")
	}
	if policy.Timeout <= 0 {
		policy.Timeout = safehttp.DefaultPolicy().Timeout
	}
	return &VaultProvider{
		address: strings.TrimRight(parsed.String(), "/"),
		token:   token,
		client:  safehttp.NewClient(policy),
		policy:  policy,
	}, nil
}

// vaultFactory builds a Vault provider for one tenant binding. The token comes
// out of the environment named by the binding at call time: a rotated token is
// picked up on the next fetch, and no token ever lands in a row.
func vaultFactory(binding Binding, policy safehttp.Policy) (Provider, error) {
	token, err := managerToken(binding)
	if err != nil {
		return nil, err
	}
	return NewVaultProvider(binding.Address, token, policy)
}

// Name identifies the provider in bindings.
func (provider *VaultProvider) Name() string { return VaultProviderName }

// Health reports whether the manager answers. Any 2xx, a standby 429, and the
// DR/performance-standby 472/473 all mean reachable: only the operator's load
// balancer topology decides which one a healthy cluster answers with. Anything
// else — sealed, down, forbidden — is unreachable for boot purposes, because
// starting without secrets and silently disabling every credential would be
// worse than refusing to start.
func (provider *VaultProvider) Health(ctx context.Context) error {
	request, err := http.NewRequestWithContext(ctx, http.MethodGet, provider.address+"/v1/sys/health", nil)
	if err != nil {
		return fmt.Errorf("vault health request: %w", err)
	}
	response, err := provider.client.Do(request)
	if err != nil {
		return fmt.Errorf("vault is unreachable: %w", safehttp.RedactError(err))
	}
	defer response.Body.Close()
	_, _, _ = provider.policy.ReadBody(response.Body)
	switch response.StatusCode {
	case http.StatusOK, http.StatusTooManyRequests, 472, 473:
		return nil
	default:
		return fmt.Errorf("vault answered health with status %d", response.StatusCode)
	}
}

// Fetch reads one KV v2 secret. A 404 is ErrSecretNotFound — a missing
// rotation, not a blip — so the caller can report it rather than retry it. The
// response body never enters an error: it is the manager's data, and errors
// go to logs.
func (provider *VaultProvider) Fetch(ctx context.Context, key string) (string, error) {
	trimmed := strings.Trim(strings.TrimSpace(key), "/")
	if trimmed == "" {
		return "", fmt.Errorf("%w: vault needs a secret path", ErrBadReference)
	}
	segments := strings.Split(trimmed, "/")
	for index, segment := range segments {
		segments[index] = url.PathEscape(segment)
	}
	request, err := http.NewRequestWithContext(ctx, http.MethodGet,
		provider.address+"/v1/secret/data/"+strings.Join(segments, "/"), nil)
	if err != nil {
		return "", fmt.Errorf("vault request: %w", err)
	}
	request.Header.Set("X-Vault-Token", provider.token)
	response, err := provider.client.Do(request)
	if err != nil {
		return "", fmt.Errorf("vault fetch: %w", safehttp.RedactError(err))
	}
	defer response.Body.Close()
	switch response.StatusCode {
	case http.StatusOK:
	case http.StatusNotFound:
		return "", fmt.Errorf("%w: %q", ErrSecretNotFound, key)
	case http.StatusForbidden:
		return "", fmt.Errorf("vault refused the token for %q", key)
	default:
		return "", fmt.Errorf("vault answered %q with status %d", key, response.StatusCode)
	}
	body, _, err := provider.policy.ReadBody(response.Body)
	if err != nil {
		return "", fmt.Errorf("vault response: %w", err)
	}
	var envelope struct {
		Data struct {
			Data map[string]string `json:"data"`
		} `json:"data"`
	}
	if err := json.Unmarshal(body, &envelope); err != nil {
		return "", fmt.Errorf("vault answered %q with an unreadable body", key)
	}
	value, found := envelope.Data.Data["value"]
	if !found || strings.TrimSpace(value) == "" {
		return "", fmt.Errorf("vault secret %q holds no \"value\" field", key)
	}
	return value, nil
}
