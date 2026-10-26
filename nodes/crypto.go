package nodes

// The Crypto utility node (FEAT-re138f): hash, HMAC, a JWT sign, and a UUID
// — the operations n8n's own Crypto node carries that need nothing but the
// item in hand.
//
// Everything here is the Go standard library plus the two libraries this
// module already ships (golang-jwt for the token, google/uuid for the id), so
// the node is pure compute with no credential and no outbound traffic: a
// secret arrives as a parameter exactly the way n8n's HMAC secret does, and a
// JWT signs with the shared secret or the PEM private key named on the node.
import (
	"context"
	"crypto/hmac"
	"crypto/md5"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha1"
	"crypto/sha256"
	"crypto/sha3"
	"crypto/sha512"
	"crypto/x509"
	"encoding/base64"
	"encoding/hex"
	"encoding/pem"
	"fmt"
	"hash"
	"strings"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"

	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/expression"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

const (
	CryptoExecutorID = "core.crypto"
	CryptoNodeType   = "kilasflow.crypto"
)

// The Crypto operations, under n8n's values, with the UUID the audit's
// templates reach for beside them.
const (
	CryptoOperationHash = "hash"
	CryptoOperationHMAC = "hmac"
	CryptoOperationSign = "sign"
	CryptoOperationUUID = "uuid"
)

func cryptoNode() node.Definition {
	return node.Definition{
		Type:        CryptoNodeType,
		Version:     workflow.V(1),
		DisplayName: "Crypto",
		Description: "Hashes a value, computes an HMAC, signs a JWT, or generates a UUID — all in process, with no credential.",
		Category:    "Transform",
		Group:       []node.NodeGroup{node.GroupTransform},
		Icon:        &node.NodeIcon{Light: "crypto"},
		IconLight:   cryptoArtwork(),
		IconColor:   "#16a34a",
		Subtitle:    "{{ $parameter.operation }}",
		Inputs:      mainInput(),
		Outputs:     mainOutput(),
		Parameters: []node.PropertyDefinition{
			{
				Key: "operation", Label: "Operation", Kind: node.PropertyOptions, Required: true, Default: CryptoOperationHash,
				Options: []node.PropertyOption{
					{Label: "Hash", Value: CryptoOperationHash},
					{Label: "HMAC", Value: CryptoOperationHMAC},
					{Label: "Sign (JWT)", Value: CryptoOperationSign},
					{Label: "Generate UUID", Value: CryptoOperationUUID},
				},
			},
			{
				Key: "type", Label: "Hash Type", Kind: node.PropertyOptions, Default: "SHA256",
				Options: []node.PropertyOption{
					{Label: "MD5", Value: "MD5"}, {Label: "SHA1", Value: "SHA1"},
					{Label: "SHA256", Value: "SHA256"}, {Label: "SHA3", Value: "SHA3"},
					{Label: "SHA384", Value: "SHA384"}, {Label: "SHA512", Value: "SHA512"},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: CryptoOperationHash}},
			},
			{
				Key: "hashType", Label: "Hash Type", Kind: node.PropertyOptions, Default: "SHA256",
				Options: []node.PropertyOption{
					{Label: "SHA1", Value: "SHA1"}, {Label: "SHA256", Value: "SHA256"},
					{Label: "SHA384", Value: "SHA384"}, {Label: "SHA512", Value: "SHA512"},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: CryptoOperationHMAC}},
			},
			{
				Key: "secret", Label: "Secret", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 2},
				Description: "The HMAC key, or the JWT shared secret. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: CryptoOperationHMAC},
					{Key: "operation", Equals: CryptoOperationSign}},
			},
			{
				Key: "signingAlgorithm", Label: "Signing Algorithm", Kind: node.PropertyOptions, Default: "HS256",
				Options: []node.PropertyOption{
					{Label: "HS256", Value: "HS256"}, {Label: "HS384", Value: "HS384"}, {Label: "HS512", Value: "HS512"},
					{Label: "RS256", Value: "RS256"}, {Label: "RS384", Value: "RS384"}, {Label: "RS512", Value: "RS512"},
				},
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: CryptoOperationSign}},
			},
			{
				Key: "privateKeyPem", Label: "Private Key (PEM)", Kind: node.PropertyString, TypeOptions: &node.TypeOptions{Rows: 4},
				Description: "The RSA private key an RS algorithm signs with, PEM-encoded. Supports expressions.",
				VisibleWhen: []node.VisibilityCondition{{Key: "signingAlgorithm", Equals: "RS256"},
					{Key: "signingAlgorithm", Equals: "RS384"}, {Key: "signingAlgorithm", Equals: "RS512"}},
			},
			{
				Key: "digestType", Label: "Digest Encoding", Kind: node.PropertyOptions, Default: "hex",
				Options: []node.PropertyOption{
					{Label: "Hex", Value: "hex"},
					{Label: "Base64", Value: "base64"},
					{Label: "Base64 URL", Value: "base64url"},
				},
				Description: "How the hash or HMAC is written.",
				VisibleWhen: []node.VisibilityCondition{{Key: "operation", Equals: CryptoOperationHash},
					{Key: "operation", Equals: CryptoOperationHMAC}},
			},
			{
				Key: "inputFieldName", Label: "Input Field", Kind: node.PropertyString, Default: "data",
				Description: "The field holding the value to hash or sign. When it is absent, the whole item is the payload.",
			},
			{
				Key: "outputFieldName", Label: "Output Field", Kind: node.PropertyString, Default: "data",
				Description: "The field the digest, token or UUID is written to.",
			},
		},
		SharedSettings: sharedSettings(),
		ExecutorID:     CryptoExecutorID,
		Validate:       validateCryptoNode,
	}
}

func validateCryptoNode(n workflow.Node) error {
	switch operation := statementText(n.Parameters, "operation"); operation {
	case "", CryptoOperationHash, CryptoOperationHMAC, CryptoOperationSign, CryptoOperationUUID:
		return nil
	default:
		return fmt.Errorf("operation %q is not one this node runs; it hashes, computes an HMAC, signs a JWT or generates a UUID", operation)
	}
}

func executeCrypto(ctx context.Context, ir workflow.IRNode, input workflow.NodeInput, request engine.Request) (workflow.NodeOutput, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	items := input["main"]
	if len(items) == 0 {
		items = []workflow.Item{{JSON: map[string]any{}}}
	}
	output := make([]workflow.Item, 0, len(items))
	for index, item := range items {
		parameters, err := expression.Resolve(ir.Parameters, expressionContext(item, input, request, index))
		if err != nil {
			return nil, fmt.Errorf("node %q: %w", ir.Name, err)
		}
		operation := textValue(parameters["operation"], CryptoOperationHash)
		inputField := strings.TrimSpace(textValue(parameters["inputFieldName"], "data"))
		if inputField == "" {
			inputField = "data"
		}
		outputField := strings.TrimSpace(textValue(parameters["outputFieldName"], "data"))
		if outputField == "" {
			outputField = "data"
		}
		fields, err := cloneItemJSON(item.JSON)
		if err != nil {
			return nil, fmt.Errorf("node %q: item %d is not JSON-serialisable: %w", ir.Name, index+1, err)
		}
		switch operation {
		case CryptoOperationHash:
			digest, err := digestBytes(hashAlgorithm(textValue(parameters["type"], "SHA256")), []byte(inputText(item.JSON, inputField)))
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			fields[outputField] = encodeDigest(digest, textValue(parameters["digestType"], "hex"))
		case CryptoOperationHMAC:
			secret := textValue(parameters["secret"], "")
			if secret == "" {
				return nil, fmt.Errorf("node %q: item %d: an HMAC needs its secret", ir.Name, index+1)
			}
			mac := hmacBytes(hashAlgorithm(textValue(parameters["hashType"], "SHA256")), []byte(secret), []byte(inputText(item.JSON, inputField)))
			fields[outputField] = encodeDigest(mac, textValue(parameters["digestType"], "hex"))
		case CryptoOperationSign:
			token, err := signJWT(parameters, item, inputField)
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: %w", ir.Name, index+1, err)
			}
			fields[outputField] = token
		case CryptoOperationUUID:
			id, err := uuid.NewRandomFromReader(rand.Reader)
			if err != nil {
				return nil, fmt.Errorf("node %q: item %d: the UUID could not be generated: %w", ir.Name, index+1, err)
			}
			fields[outputField] = id.String()
		default:
			return nil, fmt.Errorf("node %q: operation %q has no runner here", ir.Name, operation)
		}
		output = append(output, workflow.Item{JSON: fields, Binary: item.Binary, Paired: item.Paired})
	}
	return workflow.NodeOutput{output}, nil
}

// inputText is the value a hash or HMAC reads: the named field when the item
// carries it, the whole item otherwise — a payload is whatever the workflow
// pointed the node at.
func inputText(document map[string]any, field string) string {
	if value, present := document[field]; present {
		return textValue(value, fmt.Sprint(value))
	}
	return fmt.Sprint(document)
}

// hashAlgorithm maps n8n's hash names onto constructors. SHA3 is SHA3-512,
// which is the variant n8n's own node computes for that name.
func hashAlgorithm(name string) func() hash.Hash {
	switch strings.ToUpper(strings.TrimSpace(name)) {
	case "MD5":
		return md5.New
	case "SHA1":
		return sha1.New
	case "SHA3":
		// crypto/sha3's constructor returns its concrete type rather than
		// hash.Hash, so it is wrapped rather than named.
		return func() hash.Hash { return sha3.New512() }
	case "SHA384":
		return sha512.New384
	case "SHA512":
		return sha512.New
	default:
		return sha256.New
	}
}

func digestBytes(algorithm func() hash.Hash, payload []byte) ([]byte, error) {
	digest := algorithm()
	if _, err := digest.Write(payload); err != nil {
		return nil, fmt.Errorf("the value could not be hashed: %w", err)
	}
	return digest.Sum(nil), nil
}

func hmacBytes(algorithm func() hash.Hash, secret, payload []byte) []byte {
	mac := hmac.New(algorithm, secret)
	mac.Write(payload)
	return mac.Sum(nil)
}

func encodeDigest(digest []byte, digestType string) string {
	switch digestType {
	case "base64":
		return base64.StdEncoding.EncodeToString(digest)
	case "base64url":
		return base64.RawURLEncoding.EncodeToString(digest)
	default:
		return hex.EncodeToString(digest)
	}
}

// signJWT signs the payload as a JSON Web Token: the named field when the
// item carries an object there, the whole item otherwise. An HS algorithm
// signs with the shared secret; an RS algorithm with the PEM private key. A
// payload without an expiry gets one of an hour, because a token that never
// expires is a credential that never expires.
func signJWT(parameters map[string]any, item workflow.Item, inputField string) (string, error) {
	claims := jwt.MapClaims{}
	if value, present := item.JSON[inputField]; present {
		if record, ok := value.(map[string]any); ok {
			for key, entry := range record {
				claims[key] = entry
			}
		} else {
			claims["value"] = value
		}
	} else {
		for key, entry := range item.JSON {
			claims[key] = entry
		}
	}
	if _, hasExpiry := claims["exp"]; !hasExpiry {
		claims["exp"] = time.Now().Add(time.Hour).Unix()
	}
	algorithm := strings.ToUpper(strings.TrimSpace(textValue(parameters["signingAlgorithm"], "HS256")))
	var key any
	switch algorithm {
	case "HS256", "HS384", "HS512":
		secret := textValue(parameters["secret"], "")
		if secret == "" {
			return "", fmt.Errorf("an %s token needs its shared secret", algorithm)
		}
		key = []byte(secret)
	default:
		private, err := parseRSAPrivateKey(textValue(parameters["privateKeyPem"], ""))
		if err != nil {
			return "", err
		}
		key = private
	}
	token := jwt.NewWithClaims(jwt.GetSigningMethod(algorithm), claims)
	signed, err := token.SignedString(key)
	if err != nil {
		return "", fmt.Errorf("the token could not be signed: %w", err)
	}
	return signed, nil
}

func parseRSAPrivateKey(pemText string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemText))
	if block == nil {
		return nil, fmt.Errorf("the private key is not PEM-encoded")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	parsed, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("the private key does not parse: %w", err)
	}
	key, ok := parsed.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("the private key is not an RSA key")
	}
	return key, nil
}

// cryptoArtwork is the node's own monochrome mark: a key in a shield, in the
// editor's stroke idiom.
func cryptoArtwork() *node.IconAsset {
	return &node.IconAsset{
		MediaType: node.IconSVG,
		Bytes: []byte(`<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 24 24" fill="none" stroke="currentColor" stroke-width="2" stroke-linecap="round" stroke-linejoin="round">` +
			`<path d="M12 2a4 4 0 0 0-4 4c0 1.5.8 2.8 2 3.5V12h4V9.5A4 4 0 0 0 12 2z"/><path d="M10 12h4v4h-4z"/><path d="M12 16v4"/>` +
			`</svg>`),
	}
}
