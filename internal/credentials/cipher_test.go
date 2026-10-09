package credentials_test

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
)

// goldenLegacyKey and goldenLegacyPayload were captured from Cipher.Encrypt as
// it stood before sealed payloads carried a version (2026-10-09): the raw
// master key, a random nonce, no additional data. Every credential stored
// before this release looks like this, so it must keep opening.
const (
	goldenLegacyKey     = "0123456789abcdef0123456789abcdef"
	goldenLegacyPayload = "4dc2ff7a6fcb08c0989a81772956e78190b6117455fe37fb1d48c677febdc57822baecf132ce872782820f8466300a96cded"
)

func otherKey(fill byte) []byte {
	return bytes.Repeat([]byte{fill}, credentials.KeySize)
}

// legacySeal builds a v0 payload by hand with the standard library, the way
// the old Encrypt did, so the format is pinned by something other than the
// code under test.
func legacySeal(t *testing.T, key []byte, fields map[string]string) []byte {
	t.Helper()
	block, err := aes.NewCipher(key)
	if err != nil {
		t.Fatalf("aes.NewCipher() error = %v", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		t.Fatalf("cipher.NewGCM() error = %v", err)
	}
	plaintext, err := json.Marshal(fields)
	if err != nil {
		t.Fatalf("json.Marshal() error = %v", err)
	}
	nonce := make([]byte, aead.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		t.Fatalf("rand.Read() error = %v", err)
	}
	return aead.Seal(nonce, nonce, plaintext, nil)
}

func TestALegacyPayloadStillOpens(t *testing.T) {
	t.Parallel()

	golden, err := hex.DecodeString(goldenLegacyPayload)
	if err != nil {
		t.Fatalf("decode the golden payload: %v", err)
	}
	cipher, err := credentials.NewCipher([]byte(goldenLegacyKey))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	// A legacy payload was sealed with no additional data, so it opens under
	// whatever the row it sits in is called.
	aad := credentials.CredentialAAD("default", "cred_golden", "httpBasicAuth")
	opened, err := cipher.Open(golden, aad)
	if err != nil {
		t.Fatalf("Open(golden v0) error = %v", err)
	}
	if opened["password"] != "hunter2" || len(opened) != 1 {
		t.Fatalf("Open(golden v0) = %#v, want the captured payload", opened)
	}

	key := otherKey(7)
	handmade := legacySeal(t, key, map[string]string{"token": "abc"})
	current, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	if opened, err := current.Open(handmade, aad); err != nil || opened["token"] != "abc" {
		t.Fatalf("Open(hand-built v0) = %#v, %v", opened, err)
	}

	// The legacy payload of a key that is now the secondary still opens.
	rotated, err := credentials.NewCipher(otherKey(9), key)
	if err != nil {
		t.Fatalf("NewCipher(primary, secondary) error = %v", err)
	}
	if opened, err := rotated.Open(handmade, aad); err != nil || opened["token"] != "abc" {
		t.Fatalf("Open(v0 under the secondary key) = %#v, %v", opened, err)
	}
	if opened, err := rotated.Open(handmade, credentials.LifecycleAAD("default", "route")); err != nil || opened["token"] != "abc" {
		t.Fatalf("Open(v0 as lifecycle state) = %#v, %v", opened, err)
	}
}

func TestNewSealsCarryTheVersionPrefixAndKeyID(t *testing.T) {
	t.Parallel()

	master := otherKey(3)
	cipher, err := credentials.NewCipher(master)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	sealed, err := cipher.Seal(map[string]string{"password": "hunter2"}, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatal("the sealed payload carries the plaintext secret")
	}
	if string(sealed[:4]) != "kfs\x01" {
		t.Fatalf("sealed[:4] = %q, want the v1 magic", sealed[:4])
	}
	kid, err := hkdf.Key(sha256.New, master, nil, "kilasflow/credential-key-id/v1", 4)
	if err != nil {
		t.Fatalf("hkdf.Key() error = %v", err)
	}
	if !bytes.Equal(sealed[4:8], kid) {
		t.Fatalf("sealed[4:8] = %x, want the key id %x", sealed[4:8], kid)
	}

	// v1 never seals under the master key itself: the AES key is derived.
	derived, err := hkdf.Key(sha256.New, master, nil, "kilasflow/credential-seal/v1", 32)
	if err != nil {
		t.Fatalf("hkdf.Key() error = %v", err)
	}
	block, _ := aes.NewCipher(derived)
	aead, _ := cipherGCM(block)
	additional := append(append([]byte(nil), sealed[:8]...), []byte("kilasflow/credential/v1\x00default\x00cred_1\x00httpBasicAuth")...)
	body := sealed[8:]
	plaintext, err := aead.Open(nil, body[:12], body[12:], additional)
	if err != nil {
		t.Fatalf("the v1 payload did not open under the derived key and the documented AAD: %v", err)
	}
	if string(plaintext) != `{"password":"hunter2"}` {
		t.Fatalf("plaintext = %s", plaintext)
	}
	if _, err := legacyOpen(master, sealed); err == nil {
		t.Fatal("a v1 payload opened under the raw master key")
	}

	if opened, err := cipher.Open(sealed, aad); err != nil || opened["password"] != "hunter2" {
		t.Fatalf("Open() = %#v, %v", opened, err)
	}
	second, _ := cipher.Seal(map[string]string{"password": "hunter2"}, aad)
	if bytes.Equal(sealed, second) {
		t.Fatal("two seals of the same payload are identical: the nonce is not fresh")
	}
}

func cipherGCM(block cipher.Block) (cipher.AEAD, error) { return cipher.NewGCM(block) }

func legacyOpen(key, sealed []byte) ([]byte, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, err
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	return aead.Open(nil, sealed[:12], sealed[12:], nil)
}

func TestASealOpensOnlyUnderItsOwnTenantIDAndType(t *testing.T) {
	t.Parallel()

	cipher, err := credentials.NewCipher(otherKey(4))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	sealed, err := cipher.Seal(map[string]string{"token": "t"}, credentials.CredentialAAD("tenant-a", "cred_1", "httpBearerAuth"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := cipher.Open(sealed, credentials.CredentialAAD("tenant-a", "cred_1", "httpBearerAuth")); err != nil {
		t.Fatalf("Open() under its own row error = %v", err)
	}
	for name, aad := range map[string]credentials.AAD{
		"another tenant":     credentials.CredentialAAD("tenant-b", "cred_1", "httpBearerAuth"),
		"another credential": credentials.CredentialAAD("tenant-a", "cred_2", "httpBearerAuth"),
		"another type":       credentials.CredentialAAD("tenant-a", "cred_1", "httpHeaderAuth"),
		// The separator is part of the encoding, so moving a boundary is a
		// different row, not the same bytes.
		"a shifted boundary": credentials.CredentialAAD("tenant-acred_1", "", "httpBearerAuth"),
		// The purpose keeps lifecycle state and credentials apart even when
		// their parts happen to spell the same thing.
		"another purpose": credentials.LifecycleAAD("tenant-a", "cred_1"),
	} {
		if _, err := cipher.Open(sealed, aad); err == nil {
			t.Errorf("a seal opened under %s", name)
		}
	}

	route, err := cipher.Seal(map[string]string{"secret": "s"}, credentials.LifecycleAAD("tenant-a", "r1"))
	if err != nil {
		t.Fatalf("Seal(lifecycle) error = %v", err)
	}
	if _, err := cipher.Open(route, credentials.LifecycleAAD("tenant-a", "r2")); err == nil {
		t.Error("lifecycle state opened under another route")
	}
	if _, err := cipher.Open(route, credentials.LifecycleAAD("tenant-b", "r1")); err == nil {
		t.Error("lifecycle state opened under another tenant")
	}
}

func TestTheHeaderIsAuthenticated(t *testing.T) {
	t.Parallel()

	cipher, err := credentials.NewCipher(otherKey(5))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	sealed, err := cipher.Seal(map[string]string{"password": "p"}, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	for index := range 8 {
		mutated := append([]byte(nil), sealed...)
		mutated[index] ^= 0x01
		if _, err := cipher.Open(mutated, aad); err == nil {
			t.Errorf("a payload with header byte %d changed still opened", index)
		}
	}
	truncated := sealed[:20]
	if _, err := cipher.Open(truncated, aad); err == nil {
		t.Error("a truncated payload opened")
	}
	if _, err := cipher.Open([]byte("too short"), aad); err == nil {
		t.Error("a malformed payload opened")
	}
}

func TestASecondaryKeyOpensButNeverSeals(t *testing.T) {
	t.Parallel()

	oldKey, newKey := otherKey(0x11), otherKey(0x22)
	old, err := credentials.NewCipher(oldKey)
	if err != nil {
		t.Fatalf("NewCipher(old) error = %v", err)
	}
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	underOld, err := old.Seal(map[string]string{"password": "old"}, aad)
	if err != nil {
		t.Fatalf("Seal(old) error = %v", err)
	}

	rotating, err := credentials.NewCipher(newKey, oldKey)
	if err != nil {
		t.Fatalf("NewCipher(new, old) error = %v", err)
	}
	if opened, err := rotating.Open(underOld, aad); err != nil || opened["password"] != "old" {
		t.Fatalf("the secondary key did not open its own seal: %#v, %v", opened, err)
	}
	underNew, err := rotating.Seal(map[string]string{"password": "new"}, aad)
	if err != nil {
		t.Fatalf("Seal(rotating) error = %v", err)
	}
	if _, err := old.Open(underNew, aad); err == nil {
		t.Fatal("a rotating cipher sealed under its secondary key")
	}
	fresh, err := credentials.NewCipher(newKey)
	if err != nil {
		t.Fatalf("NewCipher(new) error = %v", err)
	}
	if opened, err := fresh.Open(underNew, aad); err != nil || opened["password"] != "new" {
		t.Fatalf("the primary key did not open a rotating seal: %#v, %v", opened, err)
	}

	// Without the secondary the old seal is unreadable, which is exactly why
	// the secondary stays configured until every row has been resealed.
	if _, err := fresh.Open(underOld, aad); err == nil {
		t.Fatal("a seal under a key that is no longer configured opened")
	}

	if _, err := credentials.NewCipher(newKey, make([]byte, 16)); err == nil {
		t.Fatal("NewCipher() accepted a 16-byte secondary key")
	}
}

func TestOpenForResealReportsWhatIsNotCurrent(t *testing.T) {
	t.Parallel()

	oldKey, newKey := otherKey(0x31), otherKey(0x32)
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	old, _ := credentials.NewCipher(oldKey)
	rotating, err := credentials.NewCipher(newKey, oldKey)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	underOld, _ := old.Seal(map[string]string{"password": "p"}, aad)
	underNew, _ := rotating.Seal(map[string]string{"password": "p"}, aad)
	legacy := legacySeal(t, newKey, map[string]string{"password": "p"})

	for name, test := range map[string]struct {
		sealed []byte
		stale  bool
	}{
		"v1 under the primary":   {underNew, false},
		"v1 under the secondary": {underOld, true},
		"v0 under the primary":   {legacy, true},
	} {
		fields, stale, err := rotating.OpenForReseal(test.sealed, aad)
		if err != nil {
			t.Errorf("%s: OpenForReseal() error = %v", name, err)
			continue
		}
		if fields["password"] != "p" {
			t.Errorf("%s: fields = %#v", name, fields)
		}
		if stale != test.stale {
			t.Errorf("%s: stale = %t, want %t", name, stale, test.stale)
		}
	}
}

func TestAADRefusesAPartHoldingNUL(t *testing.T) {
	t.Parallel()

	cipher, err := credentials.NewCipher(otherKey(6))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	if _, err := cipher.Seal(map[string]string{"a": "b"}, credentials.CredentialAAD("tenant\x00x", "cred_1", "t")); err == nil {
		t.Error("Seal() accepted additional data holding NUL")
	}
	sealed, err := cipher.Seal(map[string]string{"a": "b"}, credentials.CredentialAAD("tenant", "cred_1", "t"))
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	if _, err := cipher.Open(sealed, credentials.CredentialAAD("tenant", "cred_1\x00", "t")); err == nil {
		t.Error("Open() accepted additional data holding NUL")
	}
	if _, err := cipher.Seal(map[string]string{"a": "b"}, credentials.AAD{}); err == nil {
		t.Error("Seal() accepted empty additional data")
	}
}

// A legacy payload carries no AAD, so as long as it opens it opens in any
// row: resealing the stored rows does not retire a copy taken before the
// reseal (a dump, a backup, a replica). Refusing legacy seals does, for every
// key in the keyring, while v1 payloads keep opening.
func TestRefusingLegacySealsNeverOpensAV0Payload(t *testing.T) {
	t.Parallel()

	key := otherKey(0x44)
	aad := credentials.CredentialAAD("default", "cred_1", "httpBasicAuth")
	legacy := legacySeal(t, key, map[string]string{"password": "old"})
	golden, _ := hex.DecodeString(goldenLegacyPayload)

	open, err := credentials.NewCipher(key)
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	if fields, err := open.Open(legacy, aad); err != nil || fields["password"] != "old" {
		t.Fatalf("the default keyring did not open a legacy payload: %#v, %v", fields, err)
	}
	current, err := open.Seal(map[string]string{"password": "new"}, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}

	refusing := open.RefusingLegacySeals()
	if _, err := refusing.Open(legacy, aad); err == nil {
		t.Fatal("a keyring refusing legacy seals opened a v0 payload")
	}
	if _, _, err := refusing.OpenForReseal(legacy, aad); err == nil {
		t.Fatal("OpenForReseal() opened a v0 payload while legacy seals are refused")
	}
	if fields, err := refusing.Open(current, aad); err != nil || fields["password"] != "new" {
		t.Fatalf("refusing legacy seals stopped a v1 payload opening: %#v, %v", fields, err)
	}
	// Under a secondary key too, and for the golden vector of the old format.
	goldenRefusing, _ := credentials.NewCipher(key, []byte(goldenLegacyKey))
	goldenRefusing = goldenRefusing.RefusingLegacySeals()
	if _, err := goldenRefusing.Open(golden, aad); err == nil {
		t.Fatal("a v0 payload under the secondary key opened while legacy seals are refused")
	}
	// The original keyring is unchanged: refusing is a copy, not a mutation.
	if _, err := open.Open(legacy, aad); err != nil {
		t.Fatalf("RefusingLegacySeals() changed the keyring it was called on: %v", err)
	}
}
