// Command nodepackgen turns an OpenAPI 3 document into a KilasFlow node pack.
//
// It exists because a hand-written node does not scale to a hundred operations
// and does not scale at all to a service whose API is already described. The
// output is a JSON pack file, committed and reviewed like any other source, and
// a Markdown report naming everything the document contained that the pack does
// not.
//
// The report is not a courtesy. Anything this generator cannot express is
// absent from the pack, so without a list of what was left out the pack looks
// complete and is not.
//
//	nodepackgen -spec third_party/waha/openapi-202502.json \
//	  -manifest packs/waha/manifest.json \
//	  -out packs/waha/pack.json \
//	  -report packs/waha/REPORT.md
package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"strings"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "nodepackgen:", err)
		os.Exit(1)
	}
}

func run() error {
	specPath := flag.String("spec", "", "path to the OpenAPI 3 document")
	manifestPath := flag.String("manifest", "", "path to the pack manifest")
	outPath := flag.String("out", "", "path to write the pack JSON to")
	reportPath := flag.String("report", "", "path to write the generation report to")
	flag.Parse()

	if *specPath == "" || *manifestPath == "" || *outPath == "" {
		return fmt.Errorf("-spec, -manifest and -out are required")
	}

	raw, err := os.ReadFile(*specPath)
	if err != nil {
		return fmt.Errorf("read spec: %w", err)
	}
	var doc document
	if err := json.Unmarshal(raw, &doc); err != nil {
		return fmt.Errorf("parse spec: %w", err)
	}
	manifestRaw, err := os.ReadFile(*manifestPath)
	if err != nil {
		return fmt.Errorf("read manifest: %w", err)
	}
	loaded, err := decodeManifest(manifestRaw)
	if err != nil {
		return err
	}

	pack, generated, err := generate(&doc, *specPath, raw, loaded)
	if err != nil {
		return err
	}
	generated.Manifest, generated.Out = *manifestPath, *outPath

	encoded, err := encodePack(pack)
	if err != nil {
		return err
	}
	if err := os.WriteFile(*outPath, encoded, 0o644); err != nil {
		return fmt.Errorf("write pack: %w", err)
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, []byte(renderReport(generated)), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "nodepackgen: %d operations in %d resources, %d skipped, %d notes\n",
		generated.Operations, len(pack.Resources), len(generated.Skipped), len(generated.Notes))
	return nil
}

// decodeManifest refuses an unknown field, so a manifest key this build ignores
// is a failure rather than a setting that silently does nothing.
func decodeManifest(raw []byte) (manifest, error) {
	decoder := json.NewDecoder(strings.NewReader(string(raw)))
	decoder.DisallowUnknownFields()
	var loaded manifest
	if err := decoder.Decode(&loaded); err != nil {
		return manifest{}, fmt.Errorf("parse manifest: %w", err)
	}
	return loaded, nil
}

// encodePack writes the pack the same way every time.
//
// Indented and newline-terminated so it reviews as text, and with HTML escaping
// off so a description containing `&` or `<` is written as itself rather than
// as an entity that would then differ from the document it came from.
func encodePack(pack any) ([]byte, error) {
	builder := &strings.Builder{}
	encoder := json.NewEncoder(builder)
	encoder.SetEscapeHTML(false)
	encoder.SetIndent("", "  ")
	if err := encoder.Encode(pack); err != nil {
		return nil, fmt.Errorf("encode pack: %w", err)
	}
	return []byte(builder.String()), nil
}

// renderReport writes what the pack does not contain.
func renderReport(generated *report) string {
	out := &strings.Builder{}
	fmt.Fprintf(out, "# Node pack generation report\n\n")
	fmt.Fprintf(out, "- Source: `%s`\n", generated.Source)
	fmt.Fprintf(out, "- SHA-256: `%s`\n", generated.SourceDigest)
	if generated.Title != "" {
		fmt.Fprintf(out, "- API: %s %s\n", generated.Title, generated.APIVersion)
	}
	fmt.Fprintf(out, "- Operations generated: %d\n", generated.Operations)
	if generated.Out != "" {
		fmt.Fprintf(out, "\nRegenerate with:\n\n```\nnodepackgen -spec %s -manifest %s -out %s\n```\n",
			generated.Source, generated.Manifest, generated.Out)
	}
	fmt.Fprintf(out, "\nEverything listed below is **absent from the pack**.\n")

	fmt.Fprintf(out, "\n## Operations left out (%d)\n\n", len(generated.Skipped))
	if len(generated.Skipped) == 0 {
		fmt.Fprintf(out, "None.\n")
	}
	for _, line := range generated.Skipped {
		fmt.Fprintf(out, "- %s\n", line)
	}

	fmt.Fprintf(out, "\n## Degraded or merged (%d)\n\n", len(generated.Notes))
	if len(generated.Notes) == 0 {
		fmt.Fprintf(out, "None.\n")
	}
	for _, line := range generated.Notes {
		fmt.Fprintf(out, "- %s\n", line)
	}
	return out.String()
}
