package auth

// Tests that have to mint a token the issuer's own API will not produce.
//
// IssueSession refuses an empty user version, so the only way to reach the
// verifier's counterpart rule is to sign the payload directly. That is an
// internal detail of this package, which is why this file sits in `package
// auth` rather than beside the black-box tests in auth_test.go: the behaviour
// under test — a session that predates the fingerprint is refused — is
// reachable in production only through a token signed by an older build.

import (
	"testing"
	"time"
)

// internalTestKey mirrors the fixture the black-box tests use; this file cannot
// see it because it lives in the other test package.
func internalTestKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index * 3)
	}
	return key
}

// A cookie minted before UserVersion existed must be refused outright. It is
// signed and unexpired, so nothing else in the token can catch it, and a
// verifier that admitted it would be revalidating nothing at all.
func TestASessionWithoutAUserVersionIsRefused(t *testing.T) {
	t.Parallel()

	issuer, err := NewIssuer(internalTestKey(), time.Hour, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	now := time.Now().UTC()
	token, err := issuer.sign(SessionVersion, Session{
		UserID: "usr-1", TenantID: "tenant-a", Email: "someone@example.com",
		IssuedAt: now, ExpiresAt: now.Add(time.Hour),
	})
	if err != nil {
		t.Fatalf("sign() error = %v", err)
	}

	if _, err := issuer.VerifySession(token); err == nil {
		t.Error("VerifySession accepted a session carrying no user version")
	}
}
