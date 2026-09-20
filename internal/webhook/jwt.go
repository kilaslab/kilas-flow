package webhook

import (
	"context"
	"crypto/ecdsa"
	"crypto/rsa"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"net/http"
	"strings"

	"github.com/golang-jwt/jwt/v5"
)

// jwtFamilies maps each algorithm this server verifies onto the key material it
// needs. An algorithm not in this map — including "none" — is refused before a
// token is even looked at, which is what stops an algorithm-confusion token
// from being honoured because its header named something the credential's own
// algorithm never agreed to.
var jwtFamilies = map[string]string{
	"HS256": "hmac", "HS384": "hmac", "HS512": "hmac",
	"RS256": "rsa", "RS384": "rsa", "RS512": "rsa",
	"PS256": "rsa", "PS384": "rsa", "PS512": "rsa",
	"ES256": "ecdsa", "ES384": "ecdsa", "ES512": "ecdsa",
}

// jwtKeyTypeForFamily is the credential's declared key type each family reads.
// An HS token is verified with the shared passphrase, an RS or ES token with
// the PEM public key.
var jwtKeyTypeForFamily = map[string]string{
	"hmac": "passphrase", "rsa": "pemKey", "ecdsa": "pemKey",
}

// verifyJWT answers who the caller is, or why not. The status split matches the
// rest of Handler.authenticate: 401 is the caller's fault (no token, bad
// signature, expired), 500 is this endpoint's (algorithm missing, key material
// missing or unreadable).
func verifyJWT(r *http.Request, fields map[string]string) (map[string]any, int, error) {
	authorization := strings.TrimSpace(r.Header.Get("Authorization"))
	scheme, presented, found := strings.Cut(authorization, " ")
	if !found || !strings.EqualFold(strings.TrimSpace(scheme), "Bearer") || strings.TrimSpace(presented) == "" {
		return nil, http.StatusUnauthorized, errors.New("This endpoint requires a bearer token.")
	}

	algorithm := strings.TrimSpace(fields["algorithm"])
	family, known := jwtFamilies[algorithm]
	if !known {
		return nil, http.StatusInternalServerError, errors.New("This webhook's JWT credential names an algorithm this server cannot verify.")
	}
	// The credential's declared key type has to agree with the algorithm's
	// family. Choosing the key material by algorithm alone would accept a PEM
	// public key offered as an HMAC secret — the RSA-to-HMAC confusion, in
	// which material this server returns as non-secret (`publicKey` is not a
	// secret field) becomes a signing key that whoever can read the credential
	// can use. An absent key type is read as a passphrase, which is the field's
	// own default.
	keyType := strings.TrimSpace(fields["keyType"])
	if keyType == "" {
		keyType = jwtKeyTypeForFamily["hmac"]
	}
	if keyType != jwtKeyTypeForFamily[family] {
		return nil, http.StatusInternalServerError, errors.New("This webhook's JWT credential names a key type that does not match its algorithm.")
	}

	var key any
	switch family {
	case "hmac":
		secret := fields["secret"]
		if secret == "" {
			return nil, http.StatusInternalServerError, errors.New("This webhook's JWT credential has no secret.")
		}
		key = []byte(secret)
	default:
		publicKey := fields["publicKey"]
		if strings.TrimSpace(publicKey) == "" {
			return nil, http.StatusInternalServerError, errors.New("This webhook's JWT credential has no public key.")
		}
		parsed, ok := parsePublicKey(publicKey, family)
		if !ok {
			return nil, http.StatusInternalServerError, errors.New("This webhook's JWT credential does not hold a usable public key.")
		}
		key = parsed
	}

	// The token's own `exp` and `nbf` are checked when it carries them, which is
	// what n8n's jwt.verify does; a token with no `exp` never expires and is
	// accepted, as it is there. `iat` is not checked — the library validates it
	// only under jwt.WithIssuedAt(), which this server does not pass.
	parsed, err := jwt.Parse(strings.TrimSpace(presented), func(*jwt.Token) (any, error) { return key, nil }, jwt.WithValidMethods([]string{algorithm}))
	if err != nil {
		return nil, http.StatusUnauthorized, errors.New("This token was not accepted.")
	}
	claims, ok := parsed.Claims.(jwt.MapClaims)
	if !ok {
		return nil, http.StatusInternalServerError, errors.New("This token's payload could not be read.")
	}
	return map[string]any(claims), 0, nil
}

// parsePublicKey reads the first PEM block of a credential's public key field
// as the key family the credential's algorithm named.
//
// PKIX is the spelling an exported public key usually takes; PKCS#1 is what an
// RSA key exported by older tooling looks like, so both are accepted for the
// rsa family. Anything else — a certificate, a private key, a different key
// type — is refused rather than coerced.
func parsePublicKey(encoded string, family string) (any, bool) {
	block, _ := pem.Decode([]byte(encoded))
	if block == nil {
		return nil, false
	}
	if parsed, err := x509.ParsePKIXPublicKey(block.Bytes); err == nil {
		switch family {
		case "rsa":
			if rsaKey, ok := parsed.(*rsa.PublicKey); ok {
				return rsaKey, true
			}
		case "ecdsa":
			if ecdsaKey, ok := parsed.(*ecdsa.PublicKey); ok {
				return ecdsaKey, true
			}
		}
		return nil, false
	}
	if family == "rsa" {
		if rsaKey, err := x509.ParsePKCS1PublicKey(block.Bytes); err == nil {
			return rsaKey, true
		}
	}
	return nil, false
}

// jwtClaimsKey carries a verified token's payload from admit to the item shape.
type jwtClaimsKey struct{}

func withJWTClaims(r *http.Request, claims map[string]any) {
	*r = *r.WithContext(context.WithValue(r.Context(), jwtClaimsKey{}, claims))
}

func jwtClaims(r *http.Request) map[string]any {
	claims, _ := r.Context().Value(jwtClaimsKey{}).(map[string]any)
	return claims
}
