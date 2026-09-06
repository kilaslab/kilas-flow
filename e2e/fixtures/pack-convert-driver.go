//go:build ignore

// Converts one operator-transcribed declarative node into an installable pack
// directory (FEAT-ykyfbd box 7). The transcription is format facts the
// operator copied out of an installed community package — resource and
// operation strings, parameter shapes, routing metadata — never the package
// itself (see internal/nodepack/convert.go and .pine/memory/licensing.md).
//
// Run from the e2e suite, never from the server: the converter is build-time
// tooling, so an unconvertible source fails here and not at startup.
//
//	go run e2e/fixtures/pack-convert-driver.go -in <transcription.json> -type <pack.type> -out <packs-dir>/<name> [-report <report.md>]
package main

import (
	"flag"
	"fmt"
	"os"

	"github.com/kilaslabs/kilas-flow/internal/nodepack"
)

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "pack-convert:", err)
		os.Exit(1)
	}
}

func run() error {
	in := flag.String("in", "", "transcribed declarative node JSON")
	packType := flag.String("type", "", "namespaced pack type the converted pack registers as, e.g. pack.e2eacme")
	out := flag.String("out", "", "pack directory to write (pack.json plus the checksum sidecar the loader verifies)")
	reportPath := flag.String("report", "", "path to write the conversion report markdown to")
	flag.Parse()
	if *in == "" || *packType == "" || *out == "" {
		return fmt.Errorf("-in, -type and -out are required")
	}
	data, err := os.ReadFile(*in)
	if err != nil {
		return fmt.Errorf("read transcription: %w", err)
	}
	pack, report, err := nodepack.ConvertDocument(data, nodepack.ConvertOptions{PackType: *packType})
	if err != nil {
		return err
	}
	if err := nodepack.WritePackDir(*out, pack); err != nil {
		return fmt.Errorf("write pack dir: %w", err)
	}
	if *reportPath != "" {
		if err := os.WriteFile(*reportPath, []byte(report.Markdown(*packType)), 0o644); err != nil {
			return fmt.Errorf("write report: %w", err)
		}
	}
	fmt.Fprintf(os.Stderr, "pack-convert: %s with %d operations, %d excluded, %d notes\n",
		pack.Type, report.Converted, len(report.Excluded), len(report.Notes))
	return nil
}
