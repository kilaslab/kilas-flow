package credentials

import (
	"bytes"
	"testing"
)

// TestTheHeaderIsPartOfTheAdditionalData opens a v1 payload with the matching
// key directly, past the key-id lookup, after changing a header byte. The
// lookup alone would also refuse a changed key id; this proves the header is
// authenticated by the AEAD itself and not only by the lookup.
func TestTheHeaderIsPartOfTheAdditionalData(t *testing.T) {
	t.Parallel()

	cipher, err := NewCipher(bytes.Repeat([]byte{8}, KeySize))
	if err != nil {
		t.Fatalf("NewCipher() error = %v", err)
	}
	aad := CredentialAAD("default", "cred_1", "httpBasicAuth")
	sealed, err := cipher.Seal(map[string]string{"password": "p"}, aad)
	if err != nil {
		t.Fatalf("Seal() error = %v", err)
	}
	key := cipher.keys[0]
	if _, ok := key.openV1(sealed, aad); !ok {
		t.Fatal("the unchanged payload did not open")
	}
	for index := range sealHeaderSize {
		mutated := append([]byte(nil), sealed...)
		mutated[index] ^= 0x80
		if _, ok := key.openV1(mutated, aad); ok {
			t.Errorf("header byte %d is not authenticated", index)
		}
	}
}
