// Master key sourcing: the key that opens the credential cipher may come from
// the process environment (KeyFromEnvironment, the long-standing path) or from
// an external secrets manager (KeyFromManager below). The two absences mean
// different things and must stay distinguishable in code, not just in a log
// message:
//
//   - ErrNoKey: nothing is configured. The server still starts and still runs
//     workflows; only credential storage is disabled. This is the standalone
//     install with no secrets to keep.
//   - ErrManagerUnreachable: a manager IS configured but down. The server
//     refuses to start. A transient network error at boot silently disabling
//     every credential in the installation is far worse than refusing to
//     start, so this path must never degrade into the "no key configured"
//     one.
//
// Boot wiring (in cmd/kilasflow, beside the existing switch — sketched, not
// applied, because that file is outside this change's ownership):
//
//	provider, _ := credentials.NewVaultProvider(addr, token, policy)
//	if err := provider.Health(ctx); err != nil { /* refuse startup */ }
//	key, err := credentials.KeyFromManager(ctx, provider, "prod/master-key")
//	switch {
//	case errors.Is(err, credentials.ErrManagerUnreachable):
//	    return fmt.Errorf("credential encryption key: %w", err) // refuse boot
//	case errors.Is(err, credentials.ErrNoKey): // nothing configured
//	    log.Warn("credential encryption key is not set; credential storage is disabled", ...)
//	    credentialStore = repository.NewCredentialStore(db.DB, nil)
//	...
//	}
package credentials

import (
	"context"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

// ErrManagerUnreachable reports a configured secrets manager that could not be
// reached at boot. The server refuses to start on this error; see above for
// why it must never become ErrNoKey.
var ErrManagerUnreachable = errors.New("external secret manager is unreachable")

// KeyFromManager reads the credential master key out of a secrets manager
// through an already-built provider. The fetched value decodes exactly like
// the environment path — base64, hex, or raw 32 bytes — so a key can move
// from one source to the other without being re-encoded.
func KeyFromManager(ctx context.Context, provider Provider, key string) ([]byte, error) {
	decoded, _, err := KeyFromManagerFormat(ctx, provider, key)
	return decoded, err
}

// KeyFromManagerFormat is KeyFromManager that also reports how the stored
// value was encoded, so boot can warn about a raw string.
func KeyFromManagerFormat(ctx context.Context, provider Provider, key string) ([]byte, KeyFormat, error) {
	if provider == nil {
		return nil, 0, fmt.Errorf("%w: no secret manager is configured", ErrNoKey)
	}
	if strings.TrimSpace(key) == "" {
		return nil, 0, fmt.Errorf("%w: no secret key is configured", ErrNoKey)
	}
	raw, err := provider.Fetch(ctx, key)
	if err != nil {
		return nil, 0, fmt.Errorf("%w: %v", ErrManagerUnreachable, err)
	}
	decoded, format, err := ParseKey(strings.TrimSpace(raw))
	if err != nil {
		return nil, 0, fmt.Errorf("external secret %q: %w", key, err)
	}
	return decoded, format, nil
}

// KeyFormat is how a configured key was written down.
type KeyFormat int

const (
	// KeyFormatBase64 and KeyFormatHex are 32 random bytes, encoded: what
	// `openssl rand -base64 32` or `-hex 32` prints.
	KeyFormatBase64 KeyFormat = iota + 1
	KeyFormatHex
	// KeyFormatRaw is a 32-character string used as the key bytes themselves.
	// It is accepted, because refusing it would lock an installation out of
	// every credential it already stores, but it is usually a passphrase
	// somebody typed rather than 256 bits of randomness, and boot says so.
	KeyFormatRaw
)

func (format KeyFormat) String() string {
	switch format {
	case KeyFormatBase64:
		return "base64"
	case KeyFormatHex:
		return "hex"
	case KeyFormatRaw:
		return "raw"
	}
	return "unknown"
}

// ParseKey accepts the three encodings operators paste — base64, hex, or raw
// 32 bytes — reports which one it found, and rejects everything else. The
// order matters: a 44-character base64 key or a 64-character hex key is
// never 32 characters long, so a raw string is only ever the last reading.
func ParseKey(raw string) ([]byte, KeyFormat, error) {
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == KeySize {
		return decoded, KeyFormatBase64, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == KeySize {
		return decoded, KeyFormatHex, nil
	}
	if len(raw) == KeySize {
		return []byte(raw), KeyFormatRaw, nil
	}
	return nil, 0, fmt.Errorf("must hold a %d-byte key encoded as base64, hex, or raw bytes", KeySize)
}
