// Command echo is the hand-built example pack for the guest SDK.
//
// It is an ordinary main package importing only the SDK and the standard
// library, built exactly the way a pack author's module is built:
//
//	GOOS=wasip1 GOARCH=wasm CGO_ENABLED=0 go build -o echo.wasm ./pkg/sdk/example/echo
//
// The host runs the artifact like any Code node: items on stdin, the envelope
// on stdout. This file is also the build-output provenance the SDK ticket
// asks for — see the ticket's work evidence for the recorded build.
package main

import (
	"fmt"
	"strings"

	sdk "github.com/kilaslabs/kilas-flow/pkg/sdk"
)

func run(items []sdk.Item) ([]sdk.Item, error) {
	out := make([]sdk.Item, 0, len(items))
	for _, item := range items {
		name, _ := item.JSON["name"].(string)
		if strings.TrimSpace(name) == "" {
			return nil, fmt.Errorf("item is missing its name")
		}
		out = append(out, sdk.Item{JSON: map[string]any{
			"name":     name,
			"greeting": "hello, " + name,
		}})
	}
	return out, nil
}

func main() { sdk.Main(run) }
