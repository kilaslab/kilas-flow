package credentials_test

import (
	"bytes"
	"errors"
	"net/http"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/credentials"
)

func testKey() []byte {
	key := make([]byte, 32)
	for index := range key {
		key[index] = byte(index)
	}
	return key
}

func TestCipherRoundTripsAPayload(t *testing.T) {
	t.Parallel()

	cipher, err := credentials.NewCipher(testKey())
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	payload := map[string]string{"user": "ada", "password": "hunter2"}

	sealed, err := cipher.Encrypt(payload)
	if err != nil {
		t.Fatalf("Encrypt() error = %v", err)
	}
	if bytes.Contains(sealed, []byte("hunter2")) {
		t.Fatalf("ciphertext contains the plaintext secret: %q", sealed)
	}

	opened, err := cipher.Decrypt(sealed)
	if err != nil {
		t.Fatalf("Decrypt() error = %v", err)
	}
	if opened["password"] != "hunter2" || opened["user"] != "ada" {
		t.Fatalf("Decrypt() = %#v, want the original payload", opened)
	}
}

func TestCipherProducesADifferentCiphertextEachTime(t *testing.T) {
	t.Parallel()

	cipher, _ := credentials.NewCipher(testKey())
	payload := map[string]string{"token": "same"}

	first, _ := cipher.Encrypt(payload)
	second, _ := cipher.Encrypt(payload)

	// A fresh nonce per seal keeps equal secrets from being visibly equal in
	// the database.
	if bytes.Equal(first, second) {
		t.Fatal("two encryptions of the same payload produced identical ciphertext")
	}
}

func TestCipherRejectsATamperedOrForeignCiphertext(t *testing.T) {
	t.Parallel()

	cipher, _ := credentials.NewCipher(testKey())
	sealed, _ := cipher.Encrypt(map[string]string{"token": "value"})

	tampered := append([]byte(nil), sealed...)
	tampered[len(tampered)-1] ^= 0xff
	if _, err := cipher.Decrypt(tampered); err == nil {
		t.Error("a tampered ciphertext decrypted successfully")
	}

	otherKey := testKey()
	otherKey[0] ^= 0xff
	other, _ := credentials.NewCipher(otherKey)
	if _, err := other.Decrypt(sealed); err == nil {
		t.Error("a ciphertext decrypted under the wrong key")
	}

	if _, err := cipher.Decrypt([]byte("too short")); err == nil {
		t.Error("a truncated ciphertext decrypted successfully")
	}
}

func TestNewCipherRequiresA256BitKey(t *testing.T) {
	t.Parallel()

	for _, size := range []int{0, 16, 31, 33} {
		if _, err := credentials.NewCipher(make([]byte, size)); err == nil {
			t.Errorf("NewCipher() accepted a %d-byte key", size)
		}
	}
}

func TestTypesDescribeTheirFieldsForAGenericEditor(t *testing.T) {
	t.Parallel()

	for _, id := range []string{"httpBasicAuth", "httpHeaderAuth", "httpBearerAuth"} {
		definition, found := credentials.Lookup(id)
		if !found {
			t.Fatalf("credential type %q is not registered", id)
		}
		if definition.DisplayName == "" || len(definition.Fields) == 0 {
			t.Errorf("credential type %q has no editor metadata", id)
		}
		hasSecret := false
		for _, field := range definition.Fields {
			if field.Key == "" || field.Label == "" {
				t.Errorf("credential type %q has an unlabelled field", id)
			}
			hasSecret = hasSecret || field.Secret
		}
		if !hasSecret {
			t.Errorf("credential type %q declares no secret field", id)
		}
	}
	if _, found := credentials.Lookup("nope"); found {
		t.Error("an unknown credential type resolved")
	}
}

func TestValidateRejectsMissingRequiredFieldsAndUnknownKeys(t *testing.T) {
	t.Parallel()

	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada"}); err == nil {
		t.Error("a basic-auth credential without a password was accepted")
	}
	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada", "password": "x", "extra": "y"}); err == nil {
		t.Error("an unknown field was accepted")
	}
	if err := credentials.Validate("nope", map[string]string{}); err == nil {
		t.Error("an unknown credential type was accepted")
	}
	if err := credentials.Validate("httpBasicAuth", map[string]string{"user": "ada", "password": "x"}); err != nil {
		t.Errorf("a complete basic-auth credential was rejected: %v", err)
	}
}

func TestApplyAttachesAuthenticationToARequest(t *testing.T) {
	t.Parallel()

	basic, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(basic, "httpBasicAuth", map[string]string{"user": "ada", "password": "hunter2"}); err != nil {
		t.Fatalf("Apply() basic error = %v", err)
	}
	user, password, ok := basic.BasicAuth()
	if !ok || user != "ada" || password != "hunter2" {
		t.Errorf("basic auth = (%q, %q, %v), want the credential", user, password, ok)
	}

	bearer, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(bearer, "httpBearerAuth", map[string]string{"token": "abc123"}); err != nil {
		t.Fatalf("Apply() bearer error = %v", err)
	}
	if got := bearer.Header.Get("Authorization"); got != "Bearer abc123" {
		t.Errorf("bearer header = %q, want %q", got, "Bearer abc123")
	}

	header, _ := http.NewRequest(http.MethodGet, "https://api.test/", nil)
	if err := credentials.Apply(header, "httpHeaderAuth", map[string]string{"name": "X-Api-Key", "value": "k-1"}); err != nil {
		t.Fatalf("Apply() header error = %v", err)
	}
	if got := header.Header.Get("X-Api-Key"); got != "k-1" {
		t.Errorf("custom header = %q, want %q", got, "k-1")
	}
}

func TestAllowsHostScopesACredentialToItsDomains(t *testing.T) {
	t.Parallel()

	record := credentials.Record{AllowedDomains: []string{"api.test", "*.internal.test"}}

	for host, want := range map[string]bool{
		"api.test":               true,
		"API.TEST":               true,
		"api.test:443":           true,
		"svc.internal.test":      true,
		"deep.svc.internal.test": true,
		"internal.test":          false,
		"evil.test":              false,
		"api.test.evil.test":     false,
	} {
		if got := record.AllowsHost(host); got != want {
			t.Errorf("AllowsHost(%q) = %v, want %v", host, got, want)
		}
	}

	// No declared scope means the credential is unrestricted, which stays an
	// explicit decision the API surfaces rather than a silent default.
	unrestricted := credentials.Record{}
	if !unrestricted.AllowsHost("anything.test") {
		t.Error("a credential with no declared domains rejected a host")
	}
}

func TestRedactedRemovesEverySecretFieldValue(t *testing.T) {
	t.Parallel()

	fields := credentials.Redacted("httpBasicAuth", map[string]string{"user": "ada", "password": "hunter2"})

	if fields["user"] != "ada" {
		t.Errorf("non-secret field = %q, want it preserved", fields["user"])
	}
	if strings.Contains(fields["password"], "hunter2") {
		t.Errorf("secret field leaked: %q", fields["password"])
	}
	if fields["password"] == "" {
		t.Error("a set secret must still be reported as set")
	}
}

// Not parallel: t.Setenv mutates process state.
func TestKeyFromEnvironmentAcceptsBase64AndHex(t *testing.T) {
	t.Setenv("KF_TEST_KEY_B64", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=")
	key, err := credentials.KeyFromEnvironment("KF_TEST_KEY_B64")
	if err != nil || len(key) != 32 {
		t.Fatalf("KeyFromEnvironment(base64) = (%d bytes, %v), want 32 bytes", len(key), err)
	}

	t.Setenv("KF_TEST_KEY_HEX", "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f")
	hexKey, err := credentials.KeyFromEnvironment("KF_TEST_KEY_HEX")
	if err != nil || !bytes.Equal(hexKey, key) {
		t.Fatalf("KeyFromEnvironment(hex) = (%x, %v), want the same key", hexKey, err)
	}

	t.Setenv("KF_TEST_KEY_SHORT", "dG9vLXNob3J0")
	if _, err := credentials.KeyFromEnvironment("KF_TEST_KEY_SHORT"); err == nil {
		t.Error("a short key was accepted")
	}

	if _, err := credentials.KeyFromEnvironment("KF_TEST_KEY_ABSENT"); !errors.Is(err, credentials.ErrNoKey) {
		t.Errorf("missing key error = %v, want ErrNoKey", err)
	}
}
