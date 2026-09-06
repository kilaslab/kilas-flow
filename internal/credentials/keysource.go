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
	if provider == nil {
		return nil, fmt.Errorf("%w: no secret manager is configured", ErrNoKey)
	}
	if strings.TrimSpace(key) == "" {
		return nil, fmt.Errorf("%w: no secret key is configured", ErrNoKey)
	}
	raw, err := provider.Fetch(ctx, key)
	if err != nil {
		return nil, fmt.Errorf("%w: %v", ErrManagerUnreachable, err)
	}
	decoded, err := decodeKey(strings.TrimSpace(raw))
	if err != nil {
		return nil, fmt.Errorf("external secret %q: %w", key, err)
	}
	return decoded, nil
}

// decodeKey accepts the three encodings operators paste — base64, hex, or raw
// 32 bytes — and rejects everything else with the variable or secret named by
// the caller.
func decodeKey(raw string) ([]byte, error) {
	if decoded, err := base64.StdEncoding.DecodeString(raw); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	if decoded, err := hex.DecodeString(raw); err == nil && len(decoded) == KeySize {
		return decoded, nil
	}
	if len(raw) == KeySize {
		return []byte(raw), nil
	}
	return nil, fmt.Errorf("must hold a %d-byte key encoded as base64, hex, or raw bytes", KeySize)
}
