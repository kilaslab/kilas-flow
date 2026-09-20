package main

import (
	"bytes"
	"go/ast"
	"go/parser"
	"go/token"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/kilaslab/kilas-flow/internal/config"
	"github.com/kilaslab/kilas-flow/internal/embed"
)

func embedIssuerConfig() config.Config {
	cfg := config.Default()
	cfg.Embed.AllowedOrigins = []string{"https://host.example"}
	return cfg
}

func workflowSessionRequest() embed.Request {
	return embed.Request{
		TenantID: "tenant-a", WorkflowID: "wf-1",
		Scopes: []embed.Scope{embed.ScopeRead}, Origin: "https://host.example",
	}
}

func TestEmbedIssuerIsBuiltFromTheConfiguration(t *testing.T) {
	cfg := embedIssuerConfig()
	cfg.Embed.SessionTTL = 6 * time.Minute
	cfg.Branding = config.Branding{Name: "Acme Flows", Logo: "https://cdn.example/logo.png"}

	issuer, err := newEmbedIssuer(bytes.Repeat([]byte{7}, 32), cfg)
	if err != nil {
		t.Fatalf("newEmbedIssuer() error = %v", err)
	}
	session, _, err := issuer.Issue(workflowSessionRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != 6*time.Minute {
		t.Errorf("lifetime = %s, want the configured embed.session_ttl of 6m", got)
	}
	// The same helper must carry branding.name and branding.logo: a key the
	// issuer is never told about is the dead configuration this ticket removes.
	if session.Branding.Name != "Acme Flows" || session.Branding.LogoURL != "https://cdn.example/logo.png" {
		t.Errorf("session branding = %#v, want the configured deployment defaults", session.Branding)
	}
}

func TestADefaultConfigurationKeepsTheHistoricalEmbedBehaviour(t *testing.T) {
	issuer, err := newEmbedIssuer(bytes.Repeat([]byte{7}, 32), embedIssuerConfig())
	if err != nil {
		t.Fatalf("newEmbedIssuer() error = %v", err)
	}
	session, _, err := issuer.Issue(workflowSessionRequest())
	if err != nil {
		t.Fatalf("Issue() error = %v", err)
	}
	if got := session.ExpiresAt.Sub(session.IssuedAt); got != embed.DefaultLifetime {
		t.Errorf("lifetime under config.Default() = %s, want the historical %s", got, embed.DefaultLifetime)
	}
	if session.Branding != (embed.Branding{}) {
		t.Errorf("branding under config.Default() = %#v, want the zero value: an unconfigured deployment mints headerless embeds", session.Branding)
	}
}

func TestEmbedIssuerRefusesADefaultTheCapWouldBreak(t *testing.T) {
	// Validate is bypassed here on purpose: the issuer option is the second line
	// of defence for a Config built in-process rather than loaded.
	cfg := embedIssuerConfig()
	cfg.Embed.SessionTTL = embed.MaxLifetime + time.Second

	if _, err := newEmbedIssuer(bytes.Repeat([]byte{7}, 32), cfg); err == nil {
		t.Fatal("newEmbedIssuer() accepted a default above the hard cap")
	}
}

// TestBootBuildsTheEmbedIssuerThroughTheConfiguredHelper pins the call site.
//
// TestEmbedIssuerIsBuiltFromTheConfiguration proves the helper reads the key,
// but a helper nobody calls proves nothing: main.go going back to
// embed.NewIssuer(key, origins, nil) would leave every test above green while
// embed.session_ttl went back to being defined, defaulted and read by nothing.
// So this reads the source of the boot package and holds two lines: no
// non-test file builds an embed issuer directly except the helper's own, and
// some non-test file calls the helper.
func TestBootBuildsTheEmbedIssuerThroughTheConfiguredHelper(t *testing.T) {
	files, err := filepath.Glob("*.go")
	if err != nil {
		t.Fatalf("glob: %v", err)
	}

	fileSet := token.NewFileSet()
	var helperCalls int
	for _, name := range files {
		if strings.HasSuffix(name, "_test.go") {
			continue
		}
		source, err := os.ReadFile(name)
		if err != nil {
			t.Fatalf("read %s: %v", name, err)
		}
		parsed, err := parser.ParseFile(fileSet, name, source, 0)
		if err != nil {
			t.Fatalf("parse %s: %v", name, err)
		}
		ast.Inspect(parsed, func(node ast.Node) bool {
			call, ok := node.(*ast.CallExpr)
			if !ok {
				return true
			}
			switch callee := call.Fun.(type) {
			case *ast.SelectorExpr:
				pkg, isIdent := callee.X.(*ast.Ident)
				if isIdent && pkg.Name == "embed" && callee.Sel.Name == "NewIssuer" && name != "embed_issuer.go" {
					t.Errorf("%s calls embed.NewIssuer directly: build the issuer through newEmbedIssuer so embed.session_ttl reaches it",
						fileSet.Position(call.Pos()))
				}
			case *ast.Ident:
				if callee.Name == "newEmbedIssuer" {
					helperCalls++
				}
			}
			return true
		})
	}
	if helperCalls == 0 {
		t.Error("no non-test file calls newEmbedIssuer: the boot path is not building the embed issuer from the configuration")
	}
}
