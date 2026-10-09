package credentials

import (
	"bytes"
	"crypto/aes"
	"crypto/cipher"
	"crypto/hkdf"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// Sealed payload formats.
//
// v0 is what every payload looked like before this format was versioned:
// nonce(12) || ciphertext || tag, AES-256-GCM under the raw master key with no
// additional data. It is opened, never written, so an installation upgraded
// in place keeps reading every row it already holds.
//
// v1 is magic(4) || key id(4) || nonce(12) || ciphertext || tag. The AES key
// is derived from the master key with HKDF, so the master key itself never
// seals anything, and the additional data binds the payload to what it
// belongs to (see AAD). The 8-byte header is part of that additional data, so
// a payload cannot be relabelled with another key id and still open.
//
// The key id is a 4-byte HKDF output of the master key: enough to pick the
// right key out of a keyring of two, never enough to say anything about the
// key. Two keys sharing an id is harmless — Open tries every key that matches.
const (
	sealMagic      = "kfs\x01"
	sealKeyIDSize  = 4
	sealHeaderSize = len(sealMagic) + sealKeyIDSize

	sealKeyInfo   = "kilasflow/credential-seal/v1"
	sealKeyIDInfo = "kilasflow/credential-key-id/v1"

	credentialPurpose = "kilasflow/credential/v1"
	lifecyclePurpose  = "kilasflow/webhook-lifecycle/v1"
)

// errUndecryptable is the one answer every failure to open gets. A tampered
// payload, a wrong key and a payload moved from another row are not told
// apart: the difference would only help someone probing the store.
var errUndecryptable = errors.New("credential payload could not be decrypted")

// AAD names what a sealed payload belongs to. A v1 payload opens only under
// the AAD it was sealed with, so a payload copied into another row — another
// tenant's credential, another credential's id, another type, another
// webhook route — fails to open instead of authenticating as that row.
//
// The encoding is purpose || 0x00 || part || 0x00 || part ..., after the
// header. The separator is NUL rather than a printable character so no part
// can ever contain it by accident, and a part that contains it on purpose is
// refused: with that rule and a fixed number of parts per purpose, two
// different rows can never encode to the same bytes.
type AAD struct {
	purpose string
	parts   []string
}

// CredentialAAD binds a stored credential's payload to its tenant, id and type.
func CredentialAAD(tenantID, credentialID, typeID string) AAD {
	return AAD{purpose: credentialPurpose, parts: []string{tenantID, credentialID, typeID}}
}

// LifecycleAAD binds what a trigger's registration captured to the tenant and
// webhook route it was captured for.
func LifecycleAAD(tenantID, route string) AAD {
	return AAD{purpose: lifecyclePurpose, parts: []string{tenantID, route}}
}

// encode renders the additional data for one payload header.
func (aad AAD) encode(header []byte) ([]byte, error) {
	if aad.purpose == "" {
		return nil, errors.New("a sealed payload needs additional data naming what it belongs to")
	}
	size := len(header) + len(aad.purpose)
	for _, part := range aad.parts {
		if strings.IndexByte(part, 0) >= 0 {
			return nil, errors.New("sealed payload additional data must not contain a NUL byte")
		}
		size += 1 + len(part)
	}
	encoded := make([]byte, 0, size)
	encoded = append(encoded, header...)
	encoded = append(encoded, aad.purpose...)
	for _, part := range aad.parts {
		encoded = append(encoded, 0)
		encoded = append(encoded, part...)
	}
	return encoded, nil
}

// DeriveKey derives a 32-byte key for one purpose from a master key, with
// HKDF-SHA256. info names the purpose; two purposes never share a key, so a
// value one of them signs or seals means nothing to the other.
func DeriveKey(master []byte, info string) ([]byte, error) {
	if len(master) != KeySize {
		return nil, fmt.Errorf("key derivation needs a %d-byte master key, got %d", KeySize, len(master))
	}
	if strings.TrimSpace(info) == "" {
		return nil, errors.New("key derivation needs a purpose")
	}
	return hkdf.Key(sha256.New, master, nil, info, KeySize)
}

// Cipher seals credential payloads and webhook lifecycle state.
//
// It holds a keyring: the primary key, which seals, and any secondary keys,
// which only open. A secondary key is how a master key is rotated without a
// window where some payloads cannot be read — see Seal, Open and
// OpenForReseal, and docs/operate/key-rotation.
type Cipher struct {
	keys []sealKey
}

// sealKey is one master key, held as the two AEADs it is used through: the
// derived v1 key and, for payloads written before v1, the raw key itself.
type sealKey struct {
	id     [sealKeyIDSize]byte
	v1     cipher.AEAD
	legacy cipher.AEAD
}

// NewCipher builds a cipher from a 32-byte primary master key and any number
// of 32-byte secondary keys. Only the primary seals. A secondary equal to the
// primary is dropped rather than refused, since configuring the same key twice
// is harmless.
func NewCipher(primary []byte, secondary ...[]byte) (*Cipher, error) {
	if len(primary) != KeySize {
		return nil, fmt.Errorf("credential encryption key must be %d bytes, got %d", KeySize, len(primary))
	}
	key, err := newSealKey(primary)
	if err != nil {
		return nil, err
	}
	keyring := &Cipher{keys: []sealKey{key}}
	for _, master := range secondary {
		if len(master) != KeySize {
			return nil, fmt.Errorf("secondary credential encryption key must be %d bytes, got %d", KeySize, len(master))
		}
		if bytes.Equal(master, primary) {
			continue
		}
		key, err := newSealKey(master)
		if err != nil {
			return nil, err
		}
		keyring.keys = append(keyring.keys, key)
	}
	return keyring, nil
}

func newSealKey(master []byte) (sealKey, error) {
	legacy, err := newGCM(master)
	if err != nil {
		return sealKey{}, err
	}
	derived, err := DeriveKey(master, sealKeyInfo)
	if err != nil {
		return sealKey{}, fmt.Errorf("credential cipher: %w", err)
	}
	v1, err := newGCM(derived)
	if err != nil {
		return sealKey{}, err
	}
	id, err := hkdf.Key(sha256.New, master, nil, sealKeyIDInfo, sealKeyIDSize)
	if err != nil {
		return sealKey{}, fmt.Errorf("credential cipher: %w", err)
	}
	key := sealKey{v1: v1, legacy: legacy}
	copy(key.id[:], id)
	return key, nil
}

func newGCM(key []byte) (cipher.AEAD, error) {
	block, err := aes.NewCipher(key)
	if err != nil {
		return nil, fmt.Errorf("credential cipher: %w", err)
	}
	aead, err := cipher.NewGCM(block)
	if err != nil {
		return nil, fmt.Errorf("credential cipher: %w", err)
	}
	return aead, nil
}

// Seal encrypts a field payload in the v1 format under the primary key, bound
// to aad. Each call uses a fresh random nonce, so two identical payloads never
// produce identical ciphertext in storage.
func (c *Cipher) Seal(fields map[string]string, aad AAD) ([]byte, error) {
	if c == nil || len(c.keys) == 0 {
		return nil, errors.New("credential cipher is not configured")
	}
	key := c.keys[0]
	header := make([]byte, 0, sealHeaderSize)
	header = append(header, sealMagic...)
	header = append(header, key.id[:]...)
	additional, err := aad.encode(header)
	if err != nil {
		return nil, err
	}
	plaintext, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("encode credential payload: %w", err)
	}
	nonce := make([]byte, key.v1.NonceSize())
	if _, err := rand.Read(nonce); err != nil {
		return nil, fmt.Errorf("credential nonce: %w", err)
	}
	sealed := make([]byte, 0, sealHeaderSize+len(nonce)+len(plaintext)+key.v1.Overhead())
	sealed = append(sealed, header...)
	sealed = append(sealed, nonce...)
	return key.v1.Seal(sealed, nonce, plaintext, additional), nil
}

// Open decrypts a sealed payload, v1 or legacy v0, under any key in the
// keyring. A v1 payload opens only under the AAD it was sealed with. A v0
// payload carries no additional data and opens under any AAD: that is the
// residual exposure OpenForReseal and the -reseal-credentials pass remove.
func (c *Cipher) Open(sealed []byte, aad AAD) (map[string]string, error) {
	fields, _, err := c.open(sealed, aad)
	return fields, err
}

// OpenForReseal is Open that also reports whether the payload should be sealed
// again: true for a legacy v0 payload and for a v1 payload under a secondary
// key, false only for v1 under the primary key.
func (c *Cipher) OpenForReseal(sealed []byte, aad AAD) (fields map[string]string, stale bool, err error) {
	fields, current, err := c.open(sealed, aad)
	return fields, !current, err
}

func (c *Cipher) open(sealed []byte, aad AAD) (map[string]string, bool, error) {
	if c == nil || len(c.keys) == 0 {
		return nil, false, errors.New("credential cipher is not configured")
	}
	// Refused whatever the payload's version, so a caller that builds its AAD
	// wrongly learns it from the first legacy row rather than the first v1 one.
	if _, err := aad.encode(nil); err != nil {
		return nil, false, err
	}
	if len(sealed) >= sealHeaderSize && string(sealed[:len(sealMagic)]) == sealMagic {
		for index, key := range c.keys {
			if !bytes.Equal(sealed[len(sealMagic):sealHeaderSize], key.id[:]) {
				continue
			}
			if plaintext, ok := key.openV1(sealed, aad); ok {
				fields, err := decodeSealedFields(plaintext)
				return fields, index == 0, err
			}
		}
	}
	// Not v1, or v1 that did not open. A legacy random nonce begins with the
	// magic once in 2^32 payloads, so this fallback is what keeps every legacy
	// payload readable. It cannot weaken v1: a v1 payload is sealed under a
	// derived key and never authenticates under the raw key.
	for _, key := range c.keys {
		if plaintext, ok := key.openLegacy(sealed); ok {
			fields, err := decodeSealedFields(plaintext)
			return fields, false, err
		}
	}
	return nil, false, errUndecryptable
}

// openV1 opens a v1 payload with this key, using the payload's own header in
// the additional data. Its caller has matched the key id, but the header is
// authenticated here regardless.
func (key sealKey) openV1(sealed []byte, aad AAD) ([]byte, bool) {
	nonceSize := key.v1.NonceSize()
	if len(sealed) < sealHeaderSize+nonceSize+key.v1.Overhead() {
		return nil, false
	}
	additional, err := aad.encode(sealed[:sealHeaderSize])
	if err != nil {
		return nil, false
	}
	body := sealed[sealHeaderSize:]
	plaintext, err := key.v1.Open(nil, body[:nonceSize], body[nonceSize:], additional)
	return plaintext, err == nil
}

// openLegacy opens a v0 payload: raw master key, no additional data.
func (key sealKey) openLegacy(sealed []byte) ([]byte, bool) {
	nonceSize := key.legacy.NonceSize()
	if len(sealed) < nonceSize+key.legacy.Overhead() {
		return nil, false
	}
	plaintext, err := key.legacy.Open(nil, sealed[:nonceSize], sealed[nonceSize:], nil)
	return plaintext, err == nil
}

func decodeSealedFields(plaintext []byte) (map[string]string, error) {
	fields := map[string]string{}
	if err := json.Unmarshal(plaintext, &fields); err != nil {
		return nil, fmt.Errorf("decode credential payload: %w", err)
	}
	return fields, nil
}
