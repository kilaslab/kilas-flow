package nodes_test

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"regexp"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
	"github.com/kilaslab/kilas-flow/nodes"
)

func runCrypto(t *testing.T, parameters map[string]any, items ...workflow.Item) []workflow.Item {
	t.Helper()
	output, err := runCryptoErr(t, parameters, items...)
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	return output
}

func runCryptoErr(t *testing.T, parameters map[string]any, items ...workflow.Item) ([]workflow.Item, error) {
	t.Helper()
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	output, err := flowExecutor(t, nodes.CryptoExecutorID).Execute(context.Background(),
		flowNode(t, nodes.CryptoNodeType, parameters), workflow.NodeInput{"main": items}, engine.Request{})
	if err != nil {
		return nil, err
	}
	return output[0], nil
}

func TestCryptoHashesWithEachDigestAndEncoding(t *testing.T) {
	t.Parallel()

	// Digests of the text "hello" from the digests' own published vectors.
	cases := []struct{ kind, encoding, want string }{
		{"MD5", "hex", "5d41402abc4b2a76b9719d911017c592"},
		{"SHA1", "hex", "aaf4c61ddcc5e8a2dabede0f3b482cd9aea9434d"},
		{"SHA256", "hex", "2cf24dba5fb0a30e26e83b2ac5b9e29e1b161e5c1fa7425e73043362938b9824"},
		{"SHA256", "base64", "LPJNul+wow4m6DsqxbninhsWHlwfp0JecwQzYpOLmCQ="},
		{"SHA3-256", "hex", "3338be694f50c5f338814986cdf0686453a888b84f424d792af4b9202398f392"},
	}
	for _, tc := range cases {
		items := runCrypto(t, map[string]any{"action": "hash", "type": tc.kind, "value": "hello", "encoding": tc.encoding, "dataPropertyName": "digest"})
		if got := items[0].JSON["digest"]; got != tc.want {
			t.Errorf("%s/%s = %v, want %s", tc.kind, tc.encoding, got, tc.want)
		}
	}
	// The remaining digests are checked for length, since their vectors are
	// not worth transcribing: an unsupported name would error instead.
	lengths := map[string]int{"SHA384": 96, "SHA512": 128, "SHA3-224": 56, "SHA3-384": 96, "SHA3-512": 128}
	for kind, want := range lengths {
		items := runCrypto(t, map[string]any{"action": "hash", "type": kind, "value": "hello", "dataPropertyName": "digest"})
		if got, _ := items[0].JSON["digest"].(string); len(got) != want {
			t.Errorf("%s digest has %d hex characters, want %d", kind, len(got), want)
		}
	}
}

func TestCryptoHashDefaultsToHexAndTheDataField(t *testing.T) {
	t.Parallel()

	items := runCrypto(t, map[string]any{"action": "hash", "type": "MD5", "value": "hello"},
		workflow.Item{JSON: map[string]any{"keep": "me"}})
	if items[0].JSON["data"] != "5d41402abc4b2a76b9719d911017c592" || items[0].JSON["keep"] != "me" {
		t.Fatalf("item = %#v, want the digest in data beside the untouched fields", items[0].JSON)
	}
}

func TestCryptoWritesToANestedFieldThroughDotNotation(t *testing.T) {
	t.Parallel()

	items := runCrypto(t, map[string]any{"action": "hash", "type": "MD5", "value": "hello", "dataPropertyName": "hash.md5"})
	nested, _ := items[0].JSON["hash"].(map[string]any)
	if nested["md5"] != "5d41402abc4b2a76b9719d911017c592" {
		t.Fatalf("item = %#v, want hash.md5 set", items[0].JSON)
	}
}

func TestCryptoHashesAValueResolvedFromEachItem(t *testing.T) {
	t.Parallel()

	items := runCrypto(t, map[string]any{
		"action": "hash", "type": "MD5", "dataPropertyName": "digest",
		"value": map[string]any{"mode": "expression", "value": "{{ $json.name }}"},
	},
		workflow.Item{JSON: map[string]any{"name": "hello"}},
		workflow.Item{JSON: map[string]any{"name": "world"}})
	if len(items) != 2 || items[0].JSON["digest"] != "5d41402abc4b2a76b9719d911017c592" ||
		items[1].JSON["digest"] != "7d793037a0760186574b0282f2f435e7" {
		t.Fatalf("items = %#v, want one digest per item's own value", items)
	}
}

func TestCryptoHmacMatchesPublishedVectors(t *testing.T) {
	t.Parallel()

	const message = "The quick brown fox jumps over the lazy dog"
	cases := []struct{ kind, want string }{
		{"SHA256", "f7bc83f430538424b13298e6aa6fb143ef4d59a14946175997479dbc2d1a3cd8"},
		{"MD5", "80070713463e7749b90c2dc24911e275"},
		{"SHA1", "de7c9b85b8b78aa6bc8a7a36f70a90701c9db4d9"},
	}
	for _, tc := range cases {
		items := runCrypto(t, map[string]any{"action": "hmac", "type": tc.kind, "value": message, "secret": "key", "encoding": "hex"})
		if items[0].JSON["data"] != tc.want {
			t.Errorf("HMAC-%s = %v, want %s", tc.kind, items[0].JSON["data"], tc.want)
		}
	}
}

func TestCryptoSignsWithAnRSAKeyInEitherPEMForm(t *testing.T) {
	t.Parallel()

	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	pkcs8, err := x509.MarshalPKCS8PrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalPKCS8PrivateKey() error = %v", err)
	}
	forms := map[string]string{
		"pkcs1": string(pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(key)})),
		"pkcs8": string(pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: pkcs8})),
	}
	digest := sha256.Sum256([]byte("payload"))
	for name, privateKey := range forms {
		items := runCrypto(t, map[string]any{
			"action": "sign", "algorithm": "RSA-SHA256", "value": "payload", "privateKey": privateKey, "encoding": "base64",
		})
		signature, err := base64.StdEncoding.DecodeString(items[0].JSON["data"].(string))
		if err != nil {
			t.Fatalf("%s: the signature is not base64: %v", name, err)
		}
		if err := rsa.VerifyPKCS1v15(&key.PublicKey, crypto.SHA256, digest[:], signature); err != nil {
			t.Errorf("%s: the signature does not verify: %v", name, err)
		}
	}
}

func TestCryptoSignsWithAnECKey(t *testing.T) {
	t.Parallel()

	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatalf("GenerateKey() error = %v", err)
	}
	encoded, err := x509.MarshalECPrivateKey(key)
	if err != nil {
		t.Fatalf("MarshalECPrivateKey() error = %v", err)
	}
	items := runCrypto(t, map[string]any{
		"action": "sign", "algorithm": "RSA-SHA256", "value": "payload", "encoding": "hex",
		"privateKey": string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: encoded})),
	})
	signature, _ := hex.DecodeString(items[0].JSON["data"].(string))
	digest := sha256.Sum256([]byte("payload"))
	if !ecdsa.VerifyASN1(&key.PublicKey, digest[:], signature) {
		t.Fatalf("the EC signature does not verify")
	}
}

func TestCryptoSignRefusesAMissingOrUnreadableKey(t *testing.T) {
	t.Parallel()

	for name, privateKey := range map[string]string{"missing": "", "not PEM": "hello", "bad body": "-----BEGIN PRIVATE KEY-----\nAAAA\n-----END PRIVATE KEY-----"} {
		_, err := runCryptoErr(t, map[string]any{"action": "sign", "algorithm": "RSA-SHA256", "value": "x", "privateKey": privateKey})
		if err == nil {
			t.Errorf("%s: want an error, got none", name)
		}
	}
}

func TestCryptoGeneratesUUIDsAndRandomStrings(t *testing.T) {
	t.Parallel()

	uuidPattern := regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)
	items := runCrypto(t, map[string]any{"action": "generate", "encodingType": "uuid", "dataPropertyName": "id"},
		workflow.Item{JSON: map[string]any{}}, workflow.Item{JSON: map[string]any{}})
	first, _ := items[0].JSON["id"].(string)
	second, _ := items[1].JSON["id"].(string)
	if !uuidPattern.MatchString(first) || !uuidPattern.MatchString(second) || first == second {
		t.Fatalf("ids = %q, %q, want two different v4 UUIDs", first, second)
	}

	alphabets := map[string]*regexp.Regexp{
		"hex":    regexp.MustCompile(`^[0-9a-f]+$`),
		"base64": regexp.MustCompile(`^[A-Za-z0-9+/]+$`),
		"ascii":  regexp.MustCompile(`^[\x21-\x7e]+$`),
	}
	for kind, alphabet := range alphabets {
		for _, length := range []float64{1, 7, 32, 33} {
			items := runCrypto(t, map[string]any{"action": "generate", "encodingType": kind, "stringLength": length, "dataPropertyName": "token"})
			token, _ := items[0].JSON["token"].(string)
			if len(token) != int(length) || !alphabet.MatchString(token) {
				t.Errorf("%s of length %v = %q, want %v characters of that alphabet", kind, length, token, length)
			}
		}
	}
}

func TestCryptoGenerateBoundsTheLength(t *testing.T) {
	t.Parallel()

	for _, length := range []float64{0, -1, 1 << 30} {
		if _, err := runCryptoErr(t, map[string]any{"action": "generate", "encodingType": "hex", "stringLength": length}); err == nil {
			t.Errorf("length %v: want an error, got none", length)
		}
	}
}

func TestCryptoValidationNamesTheBadChoice(t *testing.T) {
	t.Parallel()

	registry := node.NewRegistry()
	if err := nodes.RegisterAll(registry); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	definition, _ := registry.Lookup(nodes.CryptoNodeType, workflow.V(1))
	cases := map[string]map[string]any{
		"digest":    {"action": "hash", "type": "CRC32"},
		"algorithm": {"action": "sign", "algorithm": "DSA-SHA256"},
		"encoding":  {"action": "hash", "type": "MD5", "encoding": "latin1"},
		"action":    {"action": "encrypt"},
	}
	for name, parameters := range cases {
		err := definition.Validate(workflow.Node{Parameters: parameters})
		if err == nil || !strings.Contains(err.Error(), name) {
			t.Errorf("%s: Validate() = %v, want an error naming it", name, err)
		}
	}
	if err := definition.Validate(workflow.Node{Parameters: map[string]any{"action": "hash", "type": "SHA256"}}); err != nil {
		t.Errorf("Validate() of a good node = %v", err)
	}
}
