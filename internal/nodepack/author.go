// Scaffold and checksum helpers for hand-written node packs.
//
// The reference hand-written pack is packs/telegram/pack.json: resources of
// operations over a shared request default, one parameter per key with the
// resources and operations that show it, and a credential type the pack
// authenticates with. Scaffold copies that shape at the smallest size that
// still loads and runs — one resource, one operation, one credential
// reference — so an author starts from something the loader accepts rather
// than from a blank file and a decoder error.
package nodepack

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// ScaffoldOptions names the pack Scaffold builds. Zero values fall back to
// the example pack; only Type and DisplayName usually need setting.
type ScaffoldOptions struct {
	Type           string
	DisplayName    string
	Description    string
	Category       string
	CredentialType string
	BaseURL        string
}

// Scaffold builds the smallest pack that still demonstrates the format's real
// ergonomics: one resource with one operation, the two parameters that
// operation needs, and the credential reference its base URL is templated
// over. The result loads and runs without further editing.
func Scaffold(opts ScaffoldOptions) *Pack {
	if strings.TrimSpace(opts.Type) == "" {
		opts.Type = "pack.example"
	}
	if strings.TrimSpace(opts.DisplayName) == "" {
		opts.DisplayName = "Example"
	}
	if strings.TrimSpace(opts.Category) == "" {
		opts.Category = "Messaging"
	}
	if strings.TrimSpace(opts.Description) == "" {
		opts.Description = "An example hand-written pack. Rename the resource and operation to match the API it calls."
	}
	if strings.TrimSpace(opts.CredentialType) == "" {
		opts.CredentialType = "exampleApi"
	}
	if strings.TrimSpace(opts.BaseURL) == "" {
		opts.BaseURL = "{{ $credentials.baseUrl }}"
	}
	return &Pack{
		Type:            opts.Type,
		Version:         workflow.V(1),
		DisplayName:     opts.DisplayName,
		Description:     opts.Description,
		Category:        opts.Category,
		Icon:            "builtin:send",
		Subtitle:        "{{ $parameter.operation }}: {{ $parameter.resource }}",
		CredentialType:  opts.CredentialType,
		RequestDefaults: routing.Request{BaseURL: opts.BaseURL},
		Parameters: []Parameter{
			{
				Key: "chatId", Label: "Chat ID",
				Description: "Where to send it. Sent as written.",
				Kind:        property.KindString,
				Required:    true,
				Resources:   []string{"message"},
				Operations:  []string{"sendMessage"},
			},
			{
				Key: "text", Label: "Text",
				Description: "The message text.",
				Kind:        property.KindString,
				Resources:   []string{"message"},
				Operations:  []string{"sendMessage"},
			},
		},
		Resources: []Resource{
			{
				Name: "message",
				Operations: []Operation{
					{
						Name: "sendMessage", Description: "Send a message.",
						Method: "POST", URL: "/sendMessage",
						Sends: []routing.Send{
							{From: "chatId", Type: "body", Property: "chatId"},
							{From: "text", Type: "body", Property: "text"},
						},
					},
				},
			},
		},
		Generator: Provenance{Tool: "nodepackgen", Source: "scaffold"},
	}
}

// Encode writes a pack the same way every time: indented and
// newline-terminated so it reviews as text, with HTML escaping off so a
// description containing `&` or `<` is written as itself.
func Encode(pack *Pack) ([]byte, error) {
	var buf bytes.Buffer
	encoder := json.NewEncoder(&buf)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pack); err != nil {
		return nil, fmt.Errorf("encode node pack: %w", err)
	}
	return buf.Bytes(), nil
}

// Checksum is the hex SHA-256 of a manifest, the digest the checksum sidecar
// records.
func Checksum(manifest []byte) string {
	sum := sha256.Sum256(manifest)
	return hex.EncodeToString(sum[:])
}

// WritePackDir lays out one pack directory: the manifest Decode reads plus
// the checksum sidecar the loader verifies, in the `sha256sum` output shape
// the loader also accepts. The checksum is generated here so an operator is
// never asked to compute a digest by hand.
func WritePackDir(packDir string, pack *Pack) error {
	manifest, err := Encode(pack)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return fmt.Errorf("write node pack dir: %w", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, ManifestName), manifest, 0o644); err != nil {
		return fmt.Errorf("write node pack dir: %w", err)
	}
	return WriteChecksum(packDir)
}

// WriteChecksum regenerates a pack directory's checksum sidecar from its
// manifest. Same bytes the loader hashes, same `sha256sum`-compatible shape,
// so what this writes is what LoadDir verifies.
func WriteChecksum(packDir string) error {
	manifest, err := os.ReadFile(filepath.Join(packDir, ManifestName))
	if err != nil {
		return fmt.Errorf("write checksum: read %s: %w", ManifestName, err)
	}
	record := Checksum(manifest) + "  " + ManifestName + "\n"
	if err := os.WriteFile(filepath.Join(packDir, ChecksumName), []byte(record), 0o644); err != nil {
		return fmt.Errorf("write checksum: %w", err)
	}
	return nil
}
