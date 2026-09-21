package idempotency

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"strconv"
)

// Hash is the request's fingerprint: the same request always hashes the same,
// and a different request does not.
//
// The body is canonicalised first — decoded with UseNumber and re-marshalled,
// which sorts object keys and keeps every number's exact text — so a client
// that re-serialises its retry with different whitespace or key order is not
// told it conflicted with itself, while 1 and 1.0 stay different requests and
// an integer above 2^53 is not rounded into its neighbour.
//
// The tenant and the key are part of what is hashed as well as the request.
// Two tenants that happen to send the same body under the same key must not
// share a fingerprint, or the second one's replay would be a cross-tenant
// read.
func (s *Service) Hash(tenantID, key string, req Request) (string, error) {
	canonical, err := canonicalJSON(req.Body)
	if err != nil {
		return "", err
	}
	digest := sha256.New()
	for _, field := range []string{tenantID, key, req.Operation, req.Target} {
		writeHashField(digest, field)
	}
	digest.Write(canonical)
	return hex.EncodeToString(digest.Sum(nil)), nil
}

// writeHashField feeds one field in a form that cannot be confused with the
// field beside it: "ab"+"c" and "a"+"bc" must not hash the same.
func writeHashField(digest hash.Hash, field string) {
	digest.Write([]byte(strconv.Itoa(len(field))))
	digest.Write([]byte{':'})
	digest.Write([]byte(field))
}

// canonicalJSON re-encodes a value so that equal requests have equal bytes.
func canonicalJSON(body any) ([]byte, error) {
	raw, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("marshal the request body: %w", err)
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return nil, fmt.Errorf("canonicalise the request body: %w", err)
	}
	canonical, err := json.Marshal(value)
	if err != nil {
		return nil, fmt.Errorf("canonicalise the request body: %w", err)
	}
	return canonical, nil
}
