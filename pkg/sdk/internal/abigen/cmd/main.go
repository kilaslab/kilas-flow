// Command abigen writes pkg/sdk's wasip1 bindings from the ABI table.
//
// It is invoked by the go:generate directive in abi.go:
//
//	go generate ./pkg/sdk/...
package main

import (
	"flag"
	"fmt"
	"os"

	sdk "github.com/kilaslab/kilas-flow/pkg/sdk"
	"github.com/kilaslab/kilas-flow/pkg/sdk/internal/abigen"
)

func main() {
	output := flag.String("o", "", "the file to write the bindings to")
	flag.Parse()
	if *output == "" {
		fmt.Fprintln(os.Stderr, "abigen: -o is required")
		os.Exit(2)
	}
	source, err := abigen.Render(sdk.Functions)
	if err != nil {
		fmt.Fprintf(os.Stderr, "abigen: %v\n", err)
		os.Exit(1)
	}
	if err := os.WriteFile(*output, source, 0o644); err != nil {
		fmt.Fprintf(os.Stderr, "abigen: %v\n", err)
		os.Exit(1)
	}
}
