package nodepack_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslab/kilas-flow/internal/credentials"
	"github.com/kilaslab/kilas-flow/internal/engine"
	"github.com/kilaslab/kilas-flow/internal/node"
	"github.com/kilaslab/kilas-flow/internal/nodepack"
	"github.com/kilaslab/kilas-flow/internal/property"
	"github.com/kilaslab/kilas-flow/internal/routing"
	"github.com/kilaslab/kilas-flow/internal/workflow"
)

// A scaffolded pack validates cleanly and round-trips through the real
// loader: written as a pack directory by the authoring helpers, installed by
// LoadDir, and executed on the shared interpreter.
func TestScaffoldRoundTripThroughLoadDir(t *testing.T) {
	t.Parallel()

	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer server.Close()

	// The scaffold's default base URL is templated over the credential the
	// way the Telegram pack's is, and `$credentials` only exposes fields of
	// a credential type this build knows. Pointing at the test server
	// instead is what an author does when their API lives somewhere else.
	pack := nodepack.Scaffold(nodepack.ScaffoldOptions{
		Type: "pack.example", DisplayName: "Example", BaseURL: server.URL,
	})
	if issues := nodepack.Validate(pack, nodepack.ManifestName); len(issues) != 0 {
		t.Fatalf("Validate(scaffold) = %v, want clean", issues)
	}

	dir := t.TempDir()
	if err := nodepack.WritePackDir(filepath.Join(dir, "example"), pack); err != nil {
		t.Fatalf("WritePackDir() error = %v", err)
	}

	set := installDirDeps(localPolicy())
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	definition, found := set.definitions.Get("pack.example", workflow.V(1))
	if !found {
		t.Fatal("the scaffolded pack did not register")
	}
	if definition.Source != node.SourcePack {
		t.Fatalf("Source = %q, want %q", definition.Source, node.SourcePack)
	}
	if len(definition.Credentials) != 1 || definition.Credentials[0].Type != "exampleApi" {
		t.Fatalf("Credentials = %#v, want the exampleApi credential type", definition.Credentials)
	}

	executor, _ := set.executors.Lookup(routing.ExecutorID)
	irDefinition, found := set.definitions.Lookup("pack.example", workflow.V(1))
	if !found {
		t.Fatal("the scaffolded pack did not register")
	}
	_, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "example-1", Name: "Example", Type: "pack.example", TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"resource": "message", "operation": "sendMessage", "chatId": "@channel", "text": "hi",
		},
		Credentials: map[string]string{"exampleApi": "cred-1"},
		Definition:  irDefinition,
	}, workflow.NodeInput{}, engine.Request{Credentials: exampleCredential{baseURL: server.URL}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "/sendMessage ") {
		t.Fatalf("calls = %#v, want the sendMessage request", calls)
	}
	if !strings.Contains(calls[0], "@channel") || !strings.Contains(calls[0], "hi") {
		t.Fatalf("calls = %#v, want the parameters in the request", calls)
	}
}

// TestMain registers the credential type the scaffolded pack references, once,
// before any test runs.
//
// Registering it from inside a test — which is what this package used to do —
// writes to the process-wide credentials registry while a sibling test is
// reading it, and `go test -race` reports exactly that as a data race. The
// registry's contract is composition-time registration ("Register adds one type
// during composition"), so the test was out of order rather than the registry
// being unsafe: nothing registers a credential type after the wiring in
// cmd/kilasflow has run.
func TestMain(m *testing.M) {
	if _, found := credentials.Default().Get("exampleApi"); !found {
		if err := credentials.Default().Register(exampleApiType()); err != nil {
			fmt.Fprintf(os.Stderr, "nodepack tests: register exampleApi: %v\n", err)
			os.Exit(1)
		}
	}
	os.Exit(m.Run())
}

// exampleApiType is the credential type Scaffold templates a pack's default base
// URL over, and the type the scaffolded pack binds in the round-trip test. It is
// declared here so there is one writer that runs before the parallel section.
func exampleApiType() credentials.Type {
	return credentials.Type{
		ID: "exampleApi", DisplayName: "Example",
		Description: "A self-hosted example instance: its base URL and its API key.",
		Properties: []property.PropertyDefinition{
			{Key: "baseUrl", Label: "Base URL", Kind: property.KindString, Required: true},
			{Key: "apiKey", Label: "API key", Kind: property.KindString, Required: true,
				TypeOptions: &property.TypeOptions{Password: true}},
		},
		Secrets:      []string{"apiKey"},
		Authenticate: &credentials.Authentication{Placement: credentials.PlacementQuery, Name: "key", Value: "{{ apiKey }}"},
	}
}

type exampleCredential struct{ baseURL string }

func (stub exampleCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "Example", Type: "exampleApi",
		Fields: map[string]string{"baseUrl": stub.baseURL, "apiKey": "secret"},
	}, nil
}

// A checksum written by the tooling is the checksum the loader verifies: a
// directory laid out by WritePackDir installs without further steps.
func TestWriteChecksumSatisfiesTheLoader(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	packDir := filepath.Join(dir, "example")
	if err := nodepack.WritePackDir(packDir, nodepack.Scaffold(nodepack.ScaffoldOptions{})); err != nil {
		t.Fatalf("WritePackDir() error = %v", err)
	}
	// Corrupt the manifest after the checksum was recorded, re-record, and
	// the loader accepts the directory again — the record follows the bytes.
	manifest, err := os.ReadFile(filepath.Join(packDir, nodepack.ManifestName))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, nodepack.ManifestName), append(manifest, '\n'), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	if err := nodepack.LoadDir(installDirDeps(localPolicy()).deps(), dir); err == nil {
		t.Fatal("LoadDir() accepted a manifest edited after its checksum was recorded")
	}
	if err := nodepack.WriteChecksum(packDir); err != nil {
		t.Fatalf("WriteChecksum() error = %v", err)
	}
	if err := nodepack.LoadDir(installDirDeps(localPolicy()).deps(), dir); err != nil {
		t.Fatalf("LoadDir() after WriteChecksum() error = %v", err)
	}
}

// Bad manifests fail with the file and the field: every case must name both,
// so an author is never left guessing which field of which file broke.
func TestValidateNamesFileAndField(t *testing.T) {
	t.Parallel()

	scaffold, err := nodepack.Encode(nodepack.Scaffold(nodepack.ScaffoldOptions{}))
	if err != nil {
		t.Fatalf("Encode() error = %v", err)
	}
	cases := map[string]struct {
		mutate func(raw map[string]any) map[string]any
		want   []string
	}{
		"unknown top-level field": {
			mutate: func(raw map[string]any) map[string]any { raw["parametersX"] = []any{}; return raw },
			want:   []string{nodepack.ManifestName, "parametersX"},
		},
		"parameters where properties belong": {
			mutate: func(raw map[string]any) map[string]any {
				raw["properties"] = raw["parameters"]
				delete(raw, "parameters")
				return raw
			},
			want: []string{nodepack.ManifestName, "properties"},
		},
		"reserved namespace": {
			mutate: func(raw map[string]any) map[string]any { raw["type"] = "kilasflow.evil"; return raw },
			want:   []string{nodepack.ManifestName, "kilasflow."},
		},
		"reserved cascade key": {
			mutate: func(raw map[string]any) map[string]any {
				params := raw["parameters"].([]any)
				params = append(params, map[string]any{"key": "resource", "label": "Resource", "kind": "string"})
				raw["parameters"] = params
				return raw
			},
			want: []string{nodepack.ManifestName, "resource"},
		},
		"duplicate parameter": {
			mutate: func(raw map[string]any) map[string]any {
				params := raw["parameters"].([]any)
				raw["parameters"] = append(params, params[0])
				return raw
			},
			want: []string{nodepack.ManifestName, "chatId"},
		},
		"unknown property kind": {
			mutate: func(raw map[string]any) map[string]any {
				params := raw["parameters"].([]any)
				params[0].(map[string]any)["kind"] = "fancy"
				return raw
			},
			want: []string{nodepack.ManifestName, "chatId"},
		},
		"refused routing hook": {
			mutate: func(raw map[string]any) map[string]any {
				resources := raw["resources"].([]any)
				operations := resources[0].(map[string]any)["operations"].([]any)
				operations[0].(map[string]any)["sends"] = []any{
					map[string]any{"from": "text", "type": "body", "property": "text", "preSend": []any{"scrub"}}}
				return raw
			},
			want: []string{nodepack.ManifestName, "preSend"},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			raw := decodePack(t, scaffold)
			issues := nodepack.ValidateBytes(encodePack(t, tc.mutate(raw)), nodepack.ManifestName)
			if len(issues) == 0 {
				t.Fatal("ValidateBytes() = clean, want issues")
			}
			joined := flattenIssues(issues)
			for _, want := range tc.want {
				if !strings.Contains(joined, want) {
					t.Fatalf("ValidateBytes() issues = %v, want them to name %q", issues, want)
				}
			}
		})
	}
}

// A directory that fails the loader fails the validator with the same names:
// the pack, the manifest, and the checksum sidecar.
func TestValidateDirMirrorsLoadDirFailures(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	if err := nodepack.WritePackDir(filepath.Join(dir, "tampered"),
		nodepack.Scaffold(nodepack.ScaffoldOptions{Type: "pack.tampered"})); err != nil {
		t.Fatalf("WritePackDir() error = %v", err)
	}
	manifest, err := os.ReadFile(filepath.Join(dir, "tampered", nodepack.ManifestName))
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(dir, "tampered", nodepack.ManifestName), append(manifest, ' '), 0o644); err != nil {
		t.Fatalf("WriteFile() error = %v", err)
	}
	issues := nodepack.ValidateDir(dir, "tampered")
	if len(issues) == 0 {
		t.Fatal("ValidateDir() = clean, want the digest mismatch")
	}
	joined := flattenIssues(issues)
	for _, want := range []string{`"tampered"`, "digest"} {
		if !strings.Contains(joined, want) {
			t.Fatalf("ValidateDir() issues = %v, want them to name %q", issues, want)
		}
	}
}

func decodePack(t *testing.T, manifest []byte) map[string]any {
	t.Helper()
	var raw map[string]any
	if err := json.Unmarshal(manifest, &raw); err != nil {
		t.Fatalf("unmarshal scaffold error = %v", err)
	}
	return raw
}

func encodePack(t *testing.T, raw map[string]any) []byte {
	t.Helper()
	encoded, err := json.Marshal(raw)
	if err != nil {
		t.Fatalf("marshal pack error = %v", err)
	}
	return encoded
}

func flattenIssues(issues []nodepack.Issue) string {
	rendered := make([]string, 0, len(issues))
	for _, issue := range issues {
		rendered = append(rendered, fmt.Sprintf("%v", issue))
	}
	return strings.Join(rendered, "\n")
}
