package api_test

import (
	"testing"

	"github.com/kilaslab/kilas-flow/internal/api"
	"github.com/kilaslab/kilas-flow/internal/config"
)

// The boot log names a URL a person can open: the public URL when one is
// configured, otherwise the listen address with a wildcard host as localhost.
func TestEditorURLNamesAnAddressToOpen(t *testing.T) {
	for name, test := range map[string]struct {
		server config.Server
		want   string
	}{
		"wildcard host":        {config.Server{Host: "0.0.0.0", Port: 8080}, "http://localhost:8080/app/workflows"},
		"empty host":           {config.Server{Host: "", Port: 8080}, "http://localhost:8080/app/workflows"},
		"ipv6 wildcard":        {config.Server{Host: "::", Port: 9000}, "http://localhost:9000/app/workflows"},
		"loopback":             {config.Server{Host: "127.0.0.1", Port: 18080}, "http://127.0.0.1:18080/app/workflows"},
		"public url":           {config.Server{Host: "0.0.0.0", Port: 8080, PublicURL: "https://flows.example"}, "https://flows.example/app/workflows"},
		"public url with path": {config.Server{Host: "0.0.0.0", Port: 8080, PublicURL: "https://acme.example/flows/"}, "https://acme.example/flows/app/workflows"},
	} {
		if got := api.EditorURL(test.server); got != test.want {
			t.Errorf("%s: EditorURL() = %q, want %q", name, got, test.want)
		}
	}
}
