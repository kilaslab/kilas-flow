package nodes

import (
	"context"
	"crypto"
	"crypto/ecdsa"
	"crypto/hmac"
	"crypto/md5" //nolint:gosec // MD5 is offered because imported workflows name it; the node labels it as a checksum, not a security control.
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1" //nolint:gosec // SHA-1 is offered for the same reason as MD5.
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"math/big"
	"strings"

	"github.com/google/uuid"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The Crypto node's server-owned binding and type.
const (
	CryptoExecutorID = "core.crypto"
	CryptoNodeType   = "kilasflow.crypto"
)

// The Crypto node's actions, as n8n names them.
const (
	CryptoActionHash     = "hash"
	CryptoActionHmac     = "hmac"
	CryptoActionSign     = "sign"
	CryptoActionGenerate = "generate"
)

// cryptoMaxRandomLength bounds a generated string so a mistyped length cannot
// ask the engine for gigabytes of randomness.
const cryptoMaxRandomLength = 1 << 16

// cryptoHashes maps the digest names n8n writes onto Go constructors. The names
// are the wire values of an imported node, so they are kept as written; SHA1 is
// an addition for the many workflows that checksum with it.
var cryptoHashes = map[string]func() hash.Hash{
	"MD5":      md5.New,
	"SHA1":     sha1.New,
	"SHA256":   sha256.New,
	"SHA384":   sha512.New384,
	"SHA512":   sha512.New,
	"SHA3-224": func() hash.Hash { return sha3.New224() },
	"SHA3-256": func() hash.Hash { return sha3.New256() },
	"SHA3-384": func() hash.Hash { return sha3.New384() },
	"SHA3-512": func() hash.Hash { return sha3.New512() },
}

// cryptoSignHashes maps a signing algorithm (n8n lists OpenSSL's names, such
// as RSA-SHA256) onto the digest the signature is taken over.
var cryptoSignHashes = map[string]crypto.Hash{
	"RSA-SHA1":   crypto.SHA1,
	"RSA-SHA256": crypto.SHA256,
	"RSA-SHA384": crypto.SHA384,
	"RSA-SHA512": crypto.SHA512,
}

func cryptoHashOptions() []node.PropertyOption {
	return []node.PropertyOption{
		{Label: "MD5", Value: "MD5"},
		{Label: "SHA1", Value: "SHA1"},
		{Label: "SHA256", Value: "SHA256"},
		{Label: "SHA384", Value: "SHA384"},
		{Label: "SHA512", Value: "SHA512"},
		{Label: "SHA3-224", Value: "SHA3-224"},
		{Label: "SHA3-256", Value: "SHA3-256"},
		{Label: "SHA3-384", Value: "SHA3-384"},
		{Label: "SHA3-512", Value: "SHA3-512"},
	}
}

func cryptoEncodingOptions() []node.PropertyOption {
	return []node.PropertyOption{
		{Label: "HEX", Value: "hex"},
		{Label: "BASE64", Value: "base64"},
	}
}

func whenAction(actions ...string) []node.VisibilityCondition {
	conditions := make([]node.VisibilityCondition, 0, len(actions))
	for _, action := range actions {
		conditions = append(conditions, node.VisibilityCondition{Key: "action", Equals: action})
	}
	return conditions
}

// cryptoNode hashes, signs and generates, per item, writing the answer to a
// field. Its parameters carry n8n's own names (action, type, value,
// dataPropertyName, encoding, secret, privateKey, algorithm, encodingType,
// stringLength) so an import copies them and an export writes them back.
func cryptoNode() node.Definition {
	return node.Definition{
		Type:        CryptoNodeType,
		Version:     workflow.V(1),
		DisplayName: "Crypto",
		Description: "Hashes or signs a value, or generates a random string or UUID, and writes the result to a field.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "builtin:shield-check"},
		IconColor:   "#6366f1",
		Subtitle:    "{{ $parameter.action }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "action", Label: "Action", Kind: node.PropertyOptions, Required: true, Default: CryptoActionHash,
				Options: []node.PropertyOption{
					{Label: "Hash", Value: CryptoActionHash},
					{Label: "Hmac", Value: CryptoActionHmac},
					{Label: "Sign", Value: CryptoActionSign},
					{Label: "Generate", Value: CryptoActionGenerate},
				},
				Description: "Hash a string, authenticate it with a secret (Hmac), sign it with an RSA or EC private key, or generate random text.",
			},
			{
				Key: "type", Label: "Type", Kind: node.PropertyOptions, Default: "MD5",
				Options:     cryptoHashOptions(),
				Description: "The digest. MD5 and SHA1 are checksums, not protection against a determined attacker.",
				VisibleWhen: whenAction(CryptoActionHash, CryptoActionHmac),
			},
			{
				Key: "value", Label: "Value", Kind: node.PropertyString,
				Description: "The text to hash or sign.",
				VisibleWhen: whenAction(CryptoActionHash, CryptoActionHmac, CryptoActionSign),
			},
			{
				Key: "dataPropertyName", Label: "Property Name", Kind: node.PropertyString, Default: "data",
				Description: "The field the result is written to. Dot notation reaches a nested field.",
			},
			{
				Key: "secret", Label: "Secret", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Password: true},
				Description: "The key the Hmac is computed with.",
				VisibleWhen: whenAction(CryptoActionHmac),
			},
			{
				Key: "algorithm", Label: "Algorithm", Kind: node.PropertyOptions, Default: "RSA-SHA256",
				Options: []node.PropertyOption{
					{Label: "RSA-SHA1", Value: "RSA-SHA1"},
					{Label: "RSA-SHA256", Value: "RSA-SHA256"},
					{Label: "RSA-SHA384", Value: "RSA-SHA384"},
					{Label: "RSA-SHA512", Value: "RSA-SHA512"},
				},
				Description: "The signature algorithm. A PEM key that is an elliptic-curve key signs with ECDSA over the same digest.",
				VisibleWhen: whenAction(CryptoActionSign),
			},
			{
				Key: "encoding", Label: "Encoding", Kind: node.PropertyOptions, Default: "hex",
				Options:     cryptoEncodingOptions(),
				Description: "How the digest or signature bytes are written as text.",
				VisibleWhen: whenAction(CryptoActionHash, CryptoActionHmac, CryptoActionSign),
			},
			{
				Key: "privateKey", Label: "Private Key", Kind: node.PropertyString,
				TypeOptions: &node.TypeOptions{Password: true, Rows: 4},
				Description: "A PEM private key (PKCS#1, PKCS#8 or SEC1).",
				VisibleWhen: whenAction(CryptoActionSign),
			},
			{
				Key: "encodingType", Label: "Encoding Type", Kind: node.PropertyOptions, Default: "uuid",
				Options: []node.PropertyOption{
					{Label: "ASCII", Value: "ascii"},
					{Label: "BASE64", Value: "base64"},
					{Label: "HEX", Value: "hex"},
					{Label: "UUID", Value: "uuid"},
				},
				Description: "A random string in this alphabet, or a version 4 UUID.",
				VisibleWhen: whenAction(CryptoActionGenerate),
			},
			{
				Key: "stringLength", Label: "String Length", Kind: node.PropertyNumber, Default: 32,
				Description: "How many characters to generate.",
				VisibleWhen: []node.VisibilityCondition{
					{Key: "action", Equals: CryptoActionGenerate},
					{Key: "encodingType", Equals: "ascii"}, {Key: "encodingType", Equals: "base64"}, {Key: "encodingType", Equals: "hex"},
				},
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CryptoExecutorID,
		Validate:       validateCryptoConfiguration,
	}
}

func validateCryptoConfiguration(n workflow.Node) error {
	action := cryptoText(n.Parameters["action"], CryptoActionHash)
	if expression.IsExpression(n.Parameters["action"]) {
		return nil
	}
	switch action {
	case CryptoActionHash, CryptoActionHmac:
		if kind := cryptoText(n.Parameters["type"], "MD5"); !expression.IsExpression(n.Parameters["type"]) {
			if _, known := cryptoHashes[kind]; !known {
				return fmt.Errorf("type %q is not a supported digest", kind)
			}
		}
	case CryptoActionSign:
		if name := cryptoText(n.Parameters["algorithm"], "RSA-SHA256"); !expression.IsExpression(n.Parameters["algorithm"]) {
			if _, known := cryptoSignHashes[name]; !known {
				return fmt.Errorf("algorithm %q is not supported: sign with RSA-SHA1, RSA-SHA256, RSA-SHA384 or RSA-SHA512", name)
			}
		}
	case CryptoActionGenerate:
	default:
		return fmt.Errorf("action must be hash, hmac, sign or generate")
	}
	if encoding := cryptoText(n.Parameters["encoding"], "hex"); !expression.IsExpression(n.Parameters["encoding"]) && encoding != "hex" && encoding != "base64" {
		return fmt.Errorf("encoding must be hex or base64")
	}
	return nil
}

// cryptoText reads a parameter as text with a fallback for an absent one.
func cryptoText(value any, fallback string) string {
	text := strings.TrimSpace(textValue(value, ""))
	if text == "" {
		return fallback
	}
	return text
}

func executeCrypto(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	out := make([]workflow.Item, 0, len(input["main"]))
	for index, item := range input["main"] {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		result, err := computeCrypto(parameters)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
		}
		built := cloneItem(item)
		if built.JSON == nil {
			built.JSON = map[string]any{}
		}
		setPath(built.JSON, cryptoText(parameters["dataPropertyName"], "data"), result)
		out = append(out, built)
	}
	return workflow.NodeOutput{out}, nil
}

// computeCrypto is the whole of the node's behaviour over resolved parameters.
func computeCrypto(parameters map[string]any) (string, error) {
	action := cryptoText(parameters["action"], CryptoActionHash)
	encoding := cryptoText(parameters["encoding"], "hex")
	switch action {
	case CryptoActionHash:
		constructor, err := cryptoDigest(parameters)
		if err != nil {
			return "", err
		}
		digest := constructor()
		digest.Write([]byte(textValue(parameters["value"], "")))
		return cryptoEncode(digest.Sum(nil), encoding)
	case CryptoActionHmac:
		constructor, err := cryptoDigest(parameters)
		if err != nil {
			return "", err
		}
		mac := hmac.New(constructor, []byte(textValue(parameters["secret"], "")))
		mac.Write([]byte(textValue(parameters["value"], "")))
		return cryptoEncode(mac.Sum(nil), encoding)
	case CryptoActionSign:
		signature, err := cryptoSign(parameters)
		if err != nil {
			return "", err
		}
		return cryptoEncode(signature, encoding)
	case CryptoActionGenerate:
		return cryptoGenerate(parameters)
	default:
		return "", fmt.Errorf("action %q is not supported", action)
	}
}

func cryptoDigest(parameters map[string]any) (func() hash.Hash, error) {
	kind := cryptoText(parameters["type"], "MD5")
	constructor, known := cryptoHashes[kind]
	if !known {
		return nil, fmt.Errorf("type %q is not a supported digest", kind)
	}
	return constructor, nil
}

func cryptoEncode(raw []byte, encoding string) (string, error) {
	switch encoding {
	case "hex":
		return hex.EncodeToString(raw), nil
	case "base64":
		return base64.StdEncoding.EncodeToString(raw), nil
	default:
		return "", fmt.Errorf("encoding %q is not supported: use hex or base64", encoding)
	}
}

// cryptoSign signs the value with a PEM private key. It uses PKCS#1 v1.5 for an
// RSA key, which is what OpenSSL's RSA-SHA256 names, and ASN.1 ECDSA for an
// elliptic-curve key, over the digest the algorithm names.
func cryptoSign(parameters map[string]any) ([]byte, error) {
	name := cryptoText(parameters["algorithm"], "RSA-SHA256")
	algorithm, known := cryptoSignHashes[name]
	if !known {
		return nil, fmt.Errorf("algorithm %q is not supported: sign with RSA-SHA1, RSA-SHA256, RSA-SHA384 or RSA-SHA512", name)
	}
	key, err := cryptoPrivateKey(textValue(parameters["privateKey"], ""))
	if err != nil {
		return nil, err
	}
	hasher := algorithm.New()
	hasher.Write([]byte(textValue(parameters["value"], "")))
	digest := hasher.Sum(nil)
	switch typed := key.(type) {
	case *rsa.PrivateKey:
		return rsa.SignPKCS1v15(rand.Reader, typed, algorithm, digest)
	case *ecdsa.PrivateKey:
		return ecdsa.SignASN1(rand.Reader, typed, digest)
	default:
		return nil, fmt.Errorf("the private key is not an RSA or EC key")
	}
}

func cryptoPrivateKey(text string) (any, error) {
	if strings.TrimSpace(text) == "" {
		return nil, fmt.Errorf("a private key is required to sign")
	}
	// A key pasted into a single-line field loses its newlines as literal \n.
	text = strings.ReplaceAll(text, `\n`, "\n")
	block, _ := pem.Decode([]byte(text))
	if block == nil {
		return nil, fmt.Errorf("the private key is not PEM encoded")
	}
	if key, err := x509.ParsePKCS8PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	if key, err := x509.ParseECPrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	return nil, fmt.Errorf("the private key could not be parsed as PKCS#1, PKCS#8 or SEC1")
}

const cryptoASCIIAlphabet = "!\"#$%&'()*+,-./0123456789:;<=>?@ABCDEFGHIJKLMNOPQRSTUVWXYZ[\\]^_`abcdefghijklmnopqrstuvwxyz{|}~"

func cryptoGenerate(parameters map[string]any) (string, error) {
	kind := cryptoText(parameters["encodingType"], "uuid")
	if kind == "uuid" {
		return uuid.NewString(), nil
	}
	length := int(numberValue(parameters["stringLength"]))
	if length == 0 && parameters["stringLength"] == nil {
		length = 32
	}
	if length < 1 || length > cryptoMaxRandomLength {
		return "", fmt.Errorf("string length must be between 1 and %d", cryptoMaxRandomLength)
	}
	switch kind {
	case "hex":
		raw := make([]byte, (length+1)/2)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		return hex.EncodeToString(raw)[:length], nil
	case "base64":
		raw := make([]byte, (length*3+3)/4+1)
		if _, err := rand.Read(raw); err != nil {
			return "", err
		}
		return base64.StdEncoding.EncodeToString(raw)[:length], nil
	case "ascii":
		var builder strings.Builder
		for builder.Len() < length {
			position, err := rand.Int(rand.Reader, big.NewInt(int64(len(cryptoASCIIAlphabet))))
			if err != nil {
				return "", err
			}
			builder.WriteByte(cryptoASCIIAlphabet[position.Int64()])
		}
		return builder.String(), nil
	default:
		return "", fmt.Errorf("encoding type %q is not supported", kind)
	}
}
