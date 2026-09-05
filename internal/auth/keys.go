package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"fmt"
	"strconv"
	"strings"
	"sync"
)

// KeyVersion prefixes every API key so the format can change without an older
// key being reinterpreted under new rules. It is also what lets the
// authentication middleware tell an API key from an embed token by inspection,
// before either has been verified.
const KeyVersion = "kfa1"

const (
	// keyPrefixBytes is the length of the public lookup handle. It is a
	// database index key, not a secret, so it only has to be wide enough that
	// two keys do not collide.
	keyPrefixBytes = 6
	// keySecretBytes is the part that is actually checked. 32 bytes of
	// crypto/rand is well beyond guessing, which is what lets the stored form
	// be a plain SHA-256 rather than a password KDF.
	keySecretBytes = 32
)

// MintedKey is a newly created API key, in the only moment its secret exists.
//
// Token is returned to the caller once and never stored; Hash is stored and
// never returned. Nothing in this struct can be reconstructed from the other
// half, which is the point: a database dump discloses no usable key.
type MintedKey struct {
	// Prefix is the public handle the verifier looks a row up by.
	Prefix string
	// Hash is the stored SHA-256 of the secret half, hex encoded.
	Hash string
	// Token is the whole credential, the only copy that will ever exist.
	Token string
}

// MintKey generates a new API key.
//
// SHA-256 rather than a password KDF: the secret is 32 bytes this process drew
// from crypto/rand, not a human's password, so there is no low-entropy guess
// space for a slow hash to defend. Paying bcrypt's milliseconds on every
// request would buy nothing and would make the hot path of every API call a
// deliberate CPU burn an unauthenticated caller could trigger at will.
func MintKey() (MintedKey, error) {
	prefix := make([]byte, keyPrefixBytes)
	if _, err := rand.Read(prefix); err != nil {
		return MintedKey{}, fmt.Errorf("generate an API key prefix: %w", err)
	}
	secret := make([]byte, keySecretBytes)
	if _, err := rand.Read(secret); err != nil {
		return MintedKey{}, fmt.Errorf("generate an API key secret: %w", err)
	}

	handle := hex.EncodeToString(prefix)
	encoded := base64.RawURLEncoding.EncodeToString(secret)

	return MintedKey{
		Prefix: handle,
		Hash:   HashKeySecret(encoded),
		Token:  KeyVersion + "_" + handle + "_" + encoded,
	}, nil
}

// HashKeySecret returns the stored form of a key's secret half.
func HashKeySecret(secret string) string {
	sum := sha256.Sum256([]byte(secret))
	return hex.EncodeToString(sum[:])
}

// SplitKey separates a presented token into its lookup handle and its secret.
//
// It reports only whether the token is shaped like one of ours. A token that
// splits cleanly is still unauthenticated until its secret is compared against
// a stored hash.
func SplitKey(token string) (prefix, secret string, ok bool) {
	// The secret half is base64url and may itself contain an underscore, so
	// the split is bounded at three rather than run over the whole string.
	parts := strings.SplitN(strings.TrimSpace(token), "_", 3)
	if len(parts) != 3 || parts[0] != KeyVersion || parts[1] == "" || parts[2] == "" {
		return "", "", false
	}
	if _, err := hex.DecodeString(parts[1]); err != nil {
		return "", "", false
	}
	return parts[1], parts[2], true
}

// LooksLikeKey reports whether a bearer token claims to be an API key.
//
// It is a routing decision made before verification, so that a request holding
// an embed token is handed to the embed layer rather than refused for not
// carrying a key.
func LooksLikeKey(token string) bool {
	return strings.HasPrefix(strings.TrimSpace(token), KeyVersion+"_")
}

// MatchKeySecret compares a presented secret against a stored hash.
//
// The comparison is constant time so that the time a rejection takes says
// nothing about how much of the secret was right.
func MatchKeySecret(presented, storedHash string) bool {
	computed := HashKeySecret(presented)
	return subtle.ConstantTimeCompare([]byte(computed), []byte(storedHash)) == 1
}

// passwordIterations is the PBKDF2 work factor for a human's password.
//
// Unlike an API key secret, a password is chosen by a person and sits in a
// guessable space, so the stored form has to be expensive to try. This is the
// OWASP figure for PBKDF2-HMAC-SHA256; it is recorded in every stored hash so
// raising it later leaves existing passwords verifiable.
const passwordIterations = 600_000

const passwordSaltBytes = 16

// HashPassword returns the stored form of a person's password.
//
// PBKDF2-HMAC-SHA256 from the standard library rather than bcrypt or Argon2id:
// both of those would be a new dependency for a login form that is not the
// product, and PBKDF2 at this work factor is a supported choice rather than a
// clever one. It is the weaker of the three against an attacker with GPUs, and
// that is the trade being made.
func HashPassword(password string) (string, error) {
	salt := make([]byte, passwordSaltBytes)
	if _, err := rand.Read(salt); err != nil {
		return "", fmt.Errorf("generate a password salt: %w", err)
	}
	return encodePassword(password, salt, passwordIterations)
}

func encodePassword(password string, salt []byte, iterations int) (string, error) {
	derived, err := pbkdf2.Key(sha256.New, password, salt, iterations, sha256.Size)
	if err != nil {
		return "", fmt.Errorf("derive a password hash: %w", err)
	}
	return strings.Join([]string{
		"pbkdf2-sha256",
		strconv.Itoa(iterations),
		base64.RawStdEncoding.EncodeToString(salt),
		base64.RawStdEncoding.EncodeToString(derived),
	}, "$"), nil
}

// DecoyPasswordHash is a valid stored hash of a password nobody holds.
//
// A login for an address with no account verifies against this, so a refusal
// costs the same as one for an address that does exist. Without it the endpoint
// answers "no such user" measurably faster than "wrong password", which is an
// account enumeration oracle regardless of how carefully the message is worded.
//
// Derived once on first use rather than at init, so a deployment that never
// turns authentication on never pays for it.
var DecoyPasswordHash = sync.OnceValue(func() string {
	hash, err := HashPassword("decoy: no account has this password")
	if err != nil {
		// Only reachable if the process cannot read randomness at all, in
		// which case nothing else here works either. An empty hash still
		// matches nothing, so login stays closed.
		return ""
	}
	return hash
})

// MatchPassword verifies a presented password against its stored form.
//
// A malformed stored hash answers false rather than an error: the caller is a
// login handler, and every negative outcome there has to look identical from
// outside.
func MatchPassword(presented, stored string) bool {
	parts := strings.Split(stored, "$")
	if len(parts) != 4 || parts[0] != "pbkdf2-sha256" {
		return false
	}
	iterations, err := strconv.Atoi(parts[1])
	if err != nil || iterations <= 0 {
		return false
	}
	salt, err := base64.RawStdEncoding.DecodeString(parts[2])
	if err != nil {
		return false
	}
	recomputed, err := encodePassword(presented, salt, iterations)
	if err != nil {
		return false
	}
	return subtle.ConstantTimeCompare([]byte(recomputed), []byte(stored)) == 1
}
