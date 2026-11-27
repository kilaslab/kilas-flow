package n8n

import (
	"strings"

	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// The file and format family's node types, mirrored from the node pack (a test
// holds the two lists together).
const (
	CryptoNodeType   = "kilasflow.crypto"
	XMLNodeType      = "kilasflow.xml"
	MarkdownNodeType = "kilasflow.markdown"
)

// copyToKilas carries the named parameters across, turning n8n's `=`-prefixed
// strings into expression markers and leaving everything absent absent: n8n
// omits a parameter that equals its default, and the native node's own fallback
// is the same default.
func copyToKilas(source map[string]any, keys ...string) map[string]any {
	copied := map[string]any{}
	for _, key := range keys {
		if value, present := source[key]; present {
			copied[key] = fromN8NValue(value)
		}
	}
	return copied
}

// copyToN8N is the way back.
func copyToN8N(source map[string]any, keys ...string) map[string]any {
	copied := map[string]any{}
	for _, key := range keys {
		if value, present := source[key]; present {
			copied[key] = toN8NValue(value)
		}
	}
	return copied
}

// --- Crypto --------------------------------------------------------------------
//
// n8n's Crypto node and the native one share every parameter name, so the
// translators copy. The one thing that can differ is the signing algorithm: n8n
// offers whatever the host's OpenSSL lists, and the native node signs with the
// four RSA-with-SHA names (and an EC key over the same digest). A name outside
// that list is refused to the placeholder rather than signing with something
// else.

var cryptoParameterKeys = []string{
	"action", "type", "value", "dataPropertyName", "secret", "encoding",
	"algorithm", "privateKey", "encodingType", "stringLength",
}

var cryptoSignAlgorithms = map[string]bool{
	"RSA-SHA1": true, "RSA-SHA256": true, "RSA-SHA384": true, "RSA-SHA512": true,
}

func refuseUnsupportedCryptoAlgorithm(node Node) string {
	if stringParameter(node.Parameters, "action") != "sign" {
		return ""
	}
	algorithm := stringParameter(node.Parameters, "algorithm")
	if algorithm == "" || strings.HasPrefix(algorithm, "=") || cryptoSignAlgorithms[algorithm] {
		return ""
	}
	return "this Crypto node signs with " + algorithm + ", which KilasFlow does not implement: it signs with RSA-SHA1, RSA-SHA256, " +
		"RSA-SHA384 or RSA-SHA512 (and elliptic-curve keys over those digests); it was imported as an unsupported placeholder so the " +
		"workflow does not run with a different signature than the one it was written for"
}

func cryptoToKilas(node Node) (map[string]any, []Unsupported) {
	return copyToKilas(node.Parameters, cryptoParameterKeys...), nil
}

func cryptoToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return copyToN8N(node.Parameters, cryptoParameterKeys...), nil
}

// --- XML -----------------------------------------------------------------------
//
// The two nodes share their parameter names (mode, dataPropertyName and an
// options collection), so the translators copy. The options are an allow-list:
// n8n's XML node has more of them than the native one implements, and a node
// that sets one the native node lacks would convert differently than its author
// intended, so it is refused to the placeholder with the option named.

// xmlSupportedOptions mirrors nodes.XMLOptionKeys (a test holds the two
// together).
var xmlSupportedOptions = map[string]bool{
	"attrkey": true, "charkey": true, "explicitArray": true, "explicitRoot": true, "ignoreAttrs": true,
	"mergeAttrs": true, "normalize": true, "normalizeTags": true, "trim": true, "includeWhiteChars": true,
	"async": true, "cdata": true, "headless": true, "rootName": true, "renderOpts": true,
}

func refuseUnsupportedXMLOption(node Node) string {
	options, _ := node.Parameters["options"].(map[string]any)
	unsupported := make([]string, 0)
	for _, key := range sortedKeys(options) {
		if !xmlSupportedOptions[key] {
			unsupported = append(unsupported, key)
		}
	}
	if len(unsupported) == 0 {
		return ""
	}
	return "this XML node sets the option " + strings.Join(unsupported, ", ") + ", which KilasFlow's XML node does not implement; " +
		"it was imported as an unsupported placeholder so the workflow does not run with a different conversion than the one it was written for"
}

func xmlToKilas(node Node) (map[string]any, []Unsupported) {
	converted := copyToKilas(node.Parameters, "mode", "dataPropertyName")
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		carried := map[string]any{}
		for key, value := range options {
			if key == "async" {
				// Which scheduler a parser uses; it never changes the result.
				continue
			}
			if render, isRender := value.(map[string]any); isRender && key == "renderOpts" {
				carried[key] = copyToKilas(render, sortedKeys(render)...)
				continue
			}
			carried[key] = fromN8NValue(value)
		}
		if len(carried) > 0 {
			converted["options"] = carried
		}
	}
	return converted, nil
}

func xmlToN8N(node workflow.Node) (map[string]any, []Lossy) {
	written := copyToN8N(node.Parameters, "mode", "dataPropertyName")
	if options, ok := node.Parameters["options"].(map[string]any); ok && len(options) > 0 {
		carried := map[string]any{}
		for key, value := range options {
			if render, isRender := value.(map[string]any); isRender && key == "renderOpts" {
				carried[key] = copyToN8N(render, sortedKeys(render)...)
				continue
			}
			carried[key] = toN8NValue(value)
		}
		written["options"] = carried
	}
	return written, nil
}

// XMLOptionSupported reports whether the importer carries an XML option. It
// exists so a test can hold the importer's list to the native node's.
func XMLOptionSupported(key string) bool { return xmlSupportedOptions[key] }
