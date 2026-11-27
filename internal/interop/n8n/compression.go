package n8n

import "github.com/kilaslab/kilas-flow/internal/workflow"

// --- Compression ---------------------------------------------------------------
//
// The two nodes share every parameter name (operation, binaryPropertyName,
// outputFormat, fileName, binaryPropertyOutput, outputPrefix), so the
// translators copy.

var compressionParameterKeys = []string{
	"operation", "binaryPropertyName", "outputFormat", "fileName", "binaryPropertyOutput", "outputPrefix",
}

func compressionToKilas(node Node) (map[string]any, []Unsupported) {
	return copyToKilas(node.Parameters, compressionParameterKeys...), nil
}

func compressionToN8N(node workflow.Node) (map[string]any, []Lossy) {
	return copyToN8N(node.Parameters, compressionParameterKeys...), nil
}
