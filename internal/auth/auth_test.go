package auth_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/kilaslabs/kilas-flow/internal/auth"
)

func testKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index * 3)
	}
	return key
}

func newIssuer(t *testing.T, now func() time.Time) *auth.Issuer {
	t.Helper()
	issuer, err := auth.NewIssuer(testKey(), time.Hour, now)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	return issuer
}

func TestAMintedKeyCarriesItsSecretOnlyInTheToken(t *testing.T) {
	t.Parallel()

	minted, err := auth.MintKey()
	if err != nil {
		t.Fatalf("MintKey() error = %v", err)
	}

	prefix, secret, ok := auth.SplitKey(minted.Token)
	if !ok {
		t.Fatalf("SplitKey(%q) reported the token was not one of ours", minted.Token)
	}
	if prefix != minted.Prefix {
		t.Errorf("prefix in token = %q, want %q", prefix, minted.Prefix)
	}
	// The stored hash must not be derivable from anything the server keeps, so
	// the secret has to be absent from both the prefix and the hash.
	if strings.Contains(minted.Prefix, secret) || strings.Contains(minted.Hash, secret) {
		t.Error("the stored halves of the key contain its secret")
	}
	if !auth.MatchKeySecret(secret, minted.Hash) {
		t.Error("the minted secret does not verify against its own hash")
	}
}

func TestTwoMintedKeysShareNoMaterial(t *testing.T) {
	t.Parallel()

	first, err := auth.MintKey()
	if err != nil {
		t.Fatalf("MintKey() error = %v", err)
	}
	second, err := auth.MintKey()
	if err != nil {
		t.Fatalf("MintKey() error = %v", err)
	}

	if first.Prefix == second.Prefix || first.Hash == second.Hash || first.Token == second.Token {
		t.Error("two minted keys share material; the generator is not random")
	}
}

func TestAKeyIsRefusedWhenAnyPartIsAltered(t *testing.T) {
	t.Parallel()

	minted, err := auth.MintKey()
	if err != nil {
		t.Fatalf("MintKey() error = %v", err)
	}
	_, secret, _ := auth.SplitKey(minted.Token)

	for name, presented := range map[string]string{
		"a different secret":    secret + "x",
		"the empty string":      "",
		"a truncated secret":    secret[:len(secret)-1],
		"the hash played back":  minted.Hash,
		"the whole token again": minted.Token,
	} {
		if auth.MatchKeySecret(presented, minted.Hash) {
			t.Errorf("MatchKeySecret accepted %s", name)
		}
	}
}

func TestATokenThatIsNotAKeyIsNotMistakenForOne(t *testing.T) {
	t.Parallel()

	for _, token := range []string{
		"kfe1.abc.def", // an embed token
		"kfa1", "kfa1_", "kfa1_onlytwo",
		"kfa1_nothex_secret",
		"Bearer kfa1_aa_bb",
		"",
	} {
		if _, _, ok := auth.SplitKey(token); ok {
			t.Errorf("SplitKey(%q) accepted a token that is not a key", token)
		}
	}
	if auth.LooksLikeKey("kfe1.abc.def") {
		t.Error("LooksLikeKey claimed an embed token as an API key")
	}
}

func TestAPasswordVerifiesOnlyAgainstItself(t *testing.T) {
	t.Parallel()

	hash, err := auth.HashPassword("correct horse battery staple")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	if strings.Contains(hash, "correct horse") {
		t.Error("the stored hash contains the password")
	}
	if !auth.MatchPassword("correct horse battery staple", hash) {
		t.Error("MatchPassword rejected the password it was given")
	}
	if auth.MatchPassword("Correct horse battery staple", hash) {
		t.Error("MatchPassword accepted a different password")
	}
}

func TestTwoAccountsWithOnePasswordGetDifferentHashes(t *testing.T) {
	t.Parallel()

	first, err := auth.HashPassword("shared")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	second, err := auth.HashPassword("shared")
	if err != nil {
		t.Fatalf("HashPassword() error = %v", err)
	}
	// Equal hashes would mean no salt, which tells an attacker holding a dump
	// which accounts share a password before they have cracked any of them.
	if first == second {
		t.Error("two hashes of one password are identical; the salt is not being used")
	}
}

func TestAMalformedStoredHashRefusesEveryPassword(t *testing.T) {
	t.Parallel()

	for _, stored := range []string{"", "not-a-hash", "pbkdf2-sha256$0$c2FsdA$aGFzaA", "bcrypt$1$a$b"} {
		if auth.MatchPassword("anything", stored) {
			t.Errorf("MatchPassword accepted a password against stored form %q", stored)
		}
	}
}

func TestASessionRoundTripsAndCarriesItsTenant(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	session, token, err := issuer.IssueSession("usr-1", "tenant-a", "someone@example.com", "v1")
	if err != nil {
		t.Fatalf("IssueSession() error = %v", err)
	}
	if session.ExpiresAt.Sub(session.IssuedAt) != time.Hour {
		t.Errorf("session lifetime = %s, want the configured hour", session.ExpiresAt.Sub(session.IssuedAt))
	}

	verified, err := issuer.VerifySession(token)
	if err != nil {
		t.Fatalf("VerifySession() error = %v", err)
	}
	if verified.TenantID != "tenant-a" || verified.UserID != "usr-1" {
		t.Errorf("verified session = %#v, want the issued one", verified)
	}
	// The fingerprint rides in the token, because a verifier that re-reads the
	// account has nothing else to compare against.
	if verified.UserVersion != "v1" {
		t.Errorf("verified UserVersion = %q, want the one issued", verified.UserVersion)
	}
}

// A session that cannot name the credential state it was minted against can
// never be revoked, so the issuer refuses to mint one rather than hand the
// middleware a token it would have to refuse anyway.
func TestASessionWithoutAUserVersionIsNotMinted(t *testing.T) {
	t.Parallel()

	if _, _, err := newIssuer(t, nil).IssueSession("usr-1", "tenant-a", "someone@example.com", ""); err == nil {
		t.Error("IssueSession() minted a session with no user version")
	}
}

func TestAUserVersionChangesWithTheCredentialStateItSummarises(t *testing.T) {
	t.Parallel()

	enabled := auth.UserVersion("pbkdf2-sha256$600000$c2FsdA$aGFzaA", false)
	if enabled == "" {
		t.Fatal("UserVersion() = empty, want a fingerprint")
	}
	// The hash itself must not be recoverable from what the cookie carries.
	if strings.Contains(enabled, "aGFzaA") || strings.Contains(enabled, "pbkdf2") {
		t.Errorf("the fingerprint %q discloses the stored hash", enabled)
	}
	// The two things revalidation has to notice, and only those.
	if auth.UserVersion("pbkdf2-sha256$600000$c2FsdA$b3RoZXI", false) == enabled {
		t.Error("a changed password hash produced the same fingerprint")
	}
	if auth.UserVersion("pbkdf2-sha256$600000$c2FsdA$aGFzaA", true) == enabled {
		t.Error("a disabled account produced the same fingerprint")
	}
}

func TestASessionSignedByAnotherKeyIsRefused(t *testing.T) {
	t.Parallel()

	other, err := auth.NewIssuer(append(testKey()[:31], 0xff), time.Hour, nil)
	if err != nil {
		t.Fatalf("NewIssuer() error = %v", err)
	}
	_, token, err := other.IssueSession("usr-1", "tenant-a", "someone@example.com", "v1")
	if err != nil {
		t.Fatalf("IssueSession() error = %v", err)
	}

	if _, err := newIssuer(t, nil).VerifySession(token); err == nil {
		t.Error("VerifySession accepted a session signed with a different key")
	}
}

func TestAnExpiredSessionIsRefused(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	clock := moment
	issuer := newIssuer(t, func() time.Time { return clock })
	_, token, err := issuer.IssueSession("usr-1", "tenant-a", "someone@example.com", "v1")
	if err != nil {
		t.Fatalf("IssueSession() error = %v", err)
	}

	clock = moment.Add(time.Hour + time.Second)
	if _, err := issuer.VerifySession(token); err == nil {
		t.Error("VerifySession accepted a session past its expiry")
	}
}

func TestAnEmbedTokenIsNotAcceptedAsASession(t *testing.T) {
	t.Parallel()

	// The two formats are signed by different keys in production, but the
	// version prefix has to refuse a cross-format token even when it is not.
	issuer := newIssuer(t, nil)
	_, ticket, err := issuer.IssueTicket("tenant-a", "exec-1", 0)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}
	if _, err := issuer.VerifySession(ticket); err == nil {
		t.Error("VerifySession accepted a stream ticket as a login")
	}
}

func TestAStreamTicketIsSpentOnFirstUse(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	_, token, err := issuer.IssueTicket("tenant-a", "exec-1", 0)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}

	if _, err := issuer.RedeemTicket(token, "exec-1"); err != nil {
		t.Fatalf("RedeemTicket() first use error = %v", err)
	}
	// A ticket travels in a query string and lands in access logs, so the
	// second presentation of one has to be worthless.
	if _, err := issuer.RedeemTicket(token, "exec-1"); err == nil {
		t.Error("RedeemTicket accepted a ticket that had already been spent")
	}
}

func TestAStreamTicketOpensOnlyTheExecutionItNames(t *testing.T) {
	t.Parallel()

	issuer := newIssuer(t, nil)
	_, token, err := issuer.IssueTicket("tenant-a", "exec-1", 0)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}

	if _, err := issuer.RedeemTicket(token, "exec-2"); err == nil {
		t.Error("RedeemTicket opened an execution the ticket does not name")
	}
}

func TestAStreamTicketExpiresWithinSeconds(t *testing.T) {
	t.Parallel()

	moment := time.Date(2026, 9, 6, 12, 0, 0, 0, time.UTC)
	clock := moment
	issuer := newIssuer(t, func() time.Time { return clock })
	// A caller asking for an hour gets the ceiling, not the hour.
	ticket, token, err := issuer.IssueTicket("tenant-a", "exec-1", time.Hour)
	if err != nil {
		t.Fatalf("IssueTicket() error = %v", err)
	}
	if got := ticket.ExpiresAt.Sub(moment); got > auth.MaxTicketTTL {
		t.Errorf("ticket lifetime = %s, want at most %s", got, auth.MaxTicketTTL)
	}

	clock = moment.Add(auth.MaxTicketTTL + time.Second)
	if _, err := issuer.RedeemTicket(token, "exec-1"); err == nil {
		t.Error("RedeemTicket accepted an expired ticket")
	}
}

func TestAWeakSigningKeyIsRefused(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 31} {
		if _, err := auth.NewIssuer(make([]byte, size), time.Hour, nil); err == nil {
			t.Errorf("NewIssuer() accepted a %d-byte key", size)
		}
	}
}

func TestAPrincipalSurvivesTheRequestContext(t *testing.T) {
	t.Parallel()

	if _, found := auth.PrincipalFrom(context.Background()); found {
		t.Error("PrincipalFrom found a principal in a bare context")
	}

	ctx := auth.WithPrincipal(context.Background(), auth.Principal{TenantID: "tenant-a", Kind: auth.KindAPIKey})
	principal, found := auth.PrincipalFrom(ctx)
	if !found || principal.TenantID != "tenant-a" {
		t.Errorf("PrincipalFrom() = %#v, %t; want the stored principal", principal, found)
	}

	// A principal with no tenant could scope nothing, so it must not read as
	// authenticated: a handler seeing "found" would query with an empty tenant.
	empty := auth.WithPrincipal(context.Background(), auth.Principal{Kind: auth.KindAPIKey})
	if _, found := auth.PrincipalFrom(empty); found {
		t.Error("PrincipalFrom reported a tenantless principal as authenticated")
	}
}
