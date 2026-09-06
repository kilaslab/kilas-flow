package credentials

import (
	"crypto/aes"
	"crypto/cipher"
	"crypto/rand"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strings"
	"time"
)

// KeySize is the AES-256 master key length in bytes.
const KeySize = 32

// RedactedValue marks a secret field that is set but withheld. It is a
// non-empty placeholder so an editor can tell "configured" from "empty"
// without ever receiving the secret.
const RedactedValue = "••••••••"

// ErrNoKey reports that the configured environment variable holds no key.
var ErrNoKey = errors.New("credential encryption key is not set")

// Record is one stored credential. Fields is populated only while a caller
// legitimately holds the decrypted payload; persisted rows keep it sealed.
type Record struct {
	ID       string
	TenantID string
	Name     string
	Type     string
	Fields   map[string]string
	// AllowedDomains scopes where this credential may be sent. An empty list
	// means unrestricted, which the API surfaces explicitly.
	AllowedDomains []string
	CreatedAt      time.Time
	UpdatedAt      time.Time
}

// AllowsHost reports whether this credential may be sent to a host. A leading
// `*.` matches any subdomain but never the bare parent domain, so scoping to
// `*.internal.test` does not silently authorize `internal.test` itself.
func (record Record) AllowsHost(host string) bool {
	if len(record.AllowedDomains) == 0 {
		return true
	}
	host = strings.ToLower(strings.TrimSuffix(hostWithoutPort(host), "."))
	for _, domain := range record.AllowedDomains {
		domain = strings.ToLower(strings.TrimSpace(domain))
		if domain == "" {
			continue
		}
		if suffix, wildcard := strings.CutPrefix(domain, "*."); wildcard {
			if strings.HasSuffix(host, "."+suffix) {
				return true
			}
			continue
		}
		if host == domain {
			return true
		}
	}
	return false
}

func hostWithoutPort(host string) string {
	if index := strings.LastIndex(host, ":"); index > 0 && !strings.Contains(host[index:], "]") {
		return host[:index]
	}
	return host
}

// Field is one credential input, described so the editor can render a generic
// form instead of a per-type one.
type Field struct {
	Key         string `json:"key"`
	Label       string `json:"label"`
	Description string `json:"description,omitempty"`
	Required    bool   `json:"required"`
	// Default is what a fresh credential starts with.
	//
	// It matters for a field that is required *and* almost always the same
	// value — a Bot API base URL, say. Without it, "required" means every user
	// types the same string, and a required field with no suggestion is how a
	// credential form gets abandoned.
	Default string `json:"default,omitempty"`
	// Secret fields are never returned by the API after they are stored.
	Secret bool `json:"secret"`
}

// Definition describes one supported credential type.
type Definition struct {
	ID          string  `json:"id"`
	DisplayName string  `json:"displayName"`
	Description string  `json:"description,omitempty"`
	Fields      []Field `json:"fields"`
}

// Lookup returns one credential type definition.
func Lookup(id string) (Definition, bool) {
	credentialType, found := Default().Get(id)
	if !found {
		return Definition{}, false
	}
	return credentialType.Definition(), true
}

// List returns every credential type in stable order.
func List() []Definition {
	types := Default().List()
	list := make([]Definition, 0, len(types))
	for _, credentialType := range types {
		list = append(list, credentialType.Definition())
	}
	return list
}

// Validate checks a payload against its type's declared fields. Unknown keys
// are rejected rather than stored, so a typo cannot silently disable auth.
func Validate(typeID string, fields map[string]string) error {
	definition, found := Lookup(typeID)
	if !found {
		return fmt.Errorf("credential type %q is not supported", typeID)
	}
	known := make(map[string]Field, len(definition.Fields))
	for _, field := range definition.Fields {
		known[field.Key] = field
	}
	for key := range fields {
		if _, ok := known[key]; !ok {
			return fmt.Errorf("credential type %q has no field %q", typeID, key)
		}
	}
	for _, field := range definition.Fields {
		if field.Required && strings.TrimSpace(fields[field.Key]) == "" {
			return fmt.Errorf("credential field %q is required", field.Key)
		}
	}
	return nil
}

// Redacted returns field values safe to send to a client: non-secret values as
// stored, secret values replaced by a set/unset marker.
func Redacted(typeID string, fields map[string]string) map[string]string {
	definition, found := Lookup(typeID)
	if !found {
		return map[string]string{}
	}
	safe := make(map[string]string, len(definition.Fields))
	for _, field := range definition.Fields {
		value, present := fields[field.Key]
		if !field.Secret {
			if present {
				safe[field.Key] = value
			}
			continue
		}
		// A required secret is always set on a stored credential, so the marker
		// is correct whether the caller holds the plaintext or only the public
		// half of the payload.
		if present && value == "" && !field.Required {
			safe[field.Key] = ""
			continue
		}
		safe[field.Key] = RedactedValue
	}
	return safe
}

// Split separates a payload into its secret and non-secret halves.
//
// Only the secret half is encrypted. Keeping the rest in plaintext lets a
// listing show a username or header name without the master key being used to
// satisfy an ordinary read, which keeps the key on the smallest possible path.
// Unknown keys are dropped: Validate has already rejected them for any write
// that reaches storage.
func Split(typeID string, fields map[string]string) (secret, public map[string]string) {
	secret, public = map[string]string{}, map[string]string{}
	definition, found := Lookup(typeID)
	if !found {
		return secret, public
	}
	for _, field := range definition.Fields {
		value, present := fields[field.Key]
		if !present {
			continue
		}
		if field.Secret {
			secret[field.Key] = value
			continue
		}
		public[field.Key] = value
	}
	return secret, public
}

// Apply attaches this credential's authentication to an outbound request.
func Apply(request *http.Request, typeID string, fields map[string]string) error {
	credentialType, found := Default().Get(typeID)
	if !found {
		return fmt.Errorf("credential type %q is not supported", typeID)
	}
	return ApplyAuthentication(request, credentialType, fields)
}

// Cipher seals credential payloads with AES-256-GCM.
type Cipher struct {
	aead cipher.AEAD
}

// NewCipher builds a cipher from a 32-byte master key.
func NewCipher(key []byte) (*Cipher, error) {
	if len(key) != KeySize {
		return nil, fmt.Errorf("credential encryption key must be %d bytes, got %d", KeySize, len(key))
	}
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credential cipher: %w", err)
	}
	return &Cipher{aead: aead}, nil
}

// Encrypt seals a field payload. Each call uses a fresh random nonce, so two
// identical payloads never produce identical ciphertext in storage.
func (c *Cipher) Encrypt(fields map[string]string) ([]byte, error) {
	if c == nil {
		return nil, fmt.Errorf("credential cipher is not configured")
	}
	plaintext, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode credential payload: %w", err)
	}
	nonce := make([]byte, c.aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("credential nonce: %w", err)
	}
	return c.aead.Seal(nonce, nonce, plaintext, nil), nil
}

// Decrypt opens a sealed payload. Authentication failure is reported without
// distinguishing a tampered ciphertext from a wrong key.
func (c *Cipher) Decrypt(sealed []byte) (map[string]string, error) {
	if c == nil {
		return nil, fmt.Errorf("credential cipher is not configured")
	}
	nonceSize := c.aead.NonceSize()
	if len(sealed) < nonceSize+1 {
		return nil, fmt.Errorf("credential payload is malformed")
	}
	plaintext, err := c.aead.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	if err != nil {
		return nil, fmt.Errorf("credential payload could not be decrypted")
	}
	fields := map[string]string{}
	if err := json.Unmarshal(plaintext, &fields); err != nil {
		return nil, fmt.Errorf("decode credential payload: %w", err)
	}
	return fields, nil
}

// KeyFromEnvironment reads the master key from an environment variable.
//
// The key never comes from the config file: a config file is routinely
// committed, copied between environments, and included in support bundles.
// Base64 and hex are both accepted so operators can paste whichever their
// secret manager emits.
func KeyFromEnvironment(name string) ([]byte, error) {
	raw := strings.TrimSpace(os.Getenv(name))
	if raw == "" {
		return nil, fmt.Errorf("%w: set %s", ErrNoKey, name)
	}
	decoded, err := decodeKey(raw)
	if err != nil {
		return nil, fmt.Errorf("%s %w", name, err)
	}
	return decoded, nil
}
