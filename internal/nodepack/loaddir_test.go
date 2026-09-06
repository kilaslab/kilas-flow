package nodepack_test

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/kilaslabs/kilas-flow/internal/engine"
	"github.com/kilaslabs/kilas-flow/internal/loadoptions"
	"github.com/kilaslabs/kilas-flow/internal/node"
	"github.com/kilaslabs/kilas-flow/internal/nodepack"
	"github.com/kilaslabs/kilas-flow/internal/routing"
	"github.com/kilaslabs/kilas-flow/internal/safehttp"
	"github.com/kilaslabs/kilas-flow/internal/webhook"
	"github.com/kilaslabs/kilas-flow/internal/workflow"
)

type dirInstalled struct {
	definitions *node.Registry
	executors   *engine.Registry
	triggers    *nodepack.TriggerRegistry
	deliveries  *webhook.Registry
	lifecycles  *webhook.LifecycleRegistry
	routes      *routing.Registry
	options     *loadoptions.Resolver
}

func installDirDeps(policy safehttp.Policy) dirInstalled {
	definitions := node.NewRegistry()
	routes := routing.NewRegistry()
	executors := engine.NewRegistry()
	if err := executors.Register(routing.ExecutorID, routing.NewExecutor(policy, routes, definitions)); err != nil {
		panic(err)
	}
	triggers := nodepack.NewTriggerRegistry()
	if err := executors.Register(nodepack.TriggerExecutorID, nodepack.NewTriggerExecutor(triggers, policy)); err != nil {
		panic(err)
	}
	return dirInstalled{
		definitions: definitions,
		executors:   executors,
		triggers:    triggers,
		deliveries:  webhook.NewRegistry(),
		lifecycles:  webhook.NewLifecycleRegistry(),
		routes:      routes,
		options:     loadoptions.NewResolver(policy, 0),
	}
}

func (set dirInstalled) deps() nodepack.DirDeps {
	return nodepack.DirDeps{
		Definitions: set.definitions,
		Routes:      set.routes,
		Triggers:    set.triggers,
		Deliveries:  set.deliveries,
		Lifecycles:  set.lifecycles,
		Executors:   set.executors,
		Options:     set.options,
	}
}

func localPolicy() safehttp.Policy {
	policy := safehttp.DefaultPolicy()
	policy.AllowPrivateNetworks = true
	return policy
}

// writePack lays out one pack directory: the manifest plus the checksum the
// operator approves with `sha256sum pack.json`.
func writePack(t *testing.T, dir, name string, manifest []byte) {
	t.Helper()
	packDir := filepath.Join(dir, name)
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatalf("MkdirAll() error = %v", err)
	}
	if err := os.WriteFile(filepath.Join(packDir, nodepack.ManifestName), manifest, 0o644); err != nil {
		t.Fatalf("WriteFile(manifest) error = %v", err)
	}
	sum := sha256.Sum256(manifest)
	checksum := hex.EncodeToString(sum[:]) + "  " + nodepack.ManifestName + "\n"
	if err := os.WriteFile(filepath.Join(packDir, nodepack.ChecksumName), []byte(checksum), 0o644); err != nil {
		t.Fatalf("WriteFile(checksum) error = %v", err)
	}
}

func telegramManifest(t *testing.T) []byte {
	t.Helper()
	manifest, err := os.ReadFile("../../packs/telegram/pack.json")
	if err != nil {
		t.Fatalf("ReadFile(pack.json) error = %v", err)
	}
	return manifest
}

// A copy of the Telegram pack loaded from disk registers as a pack source
// and runs on the shared interpreter exactly as the embedded one does.
func TestLoadDirLoadsAndRunsCopyOfTelegramPack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writePack(t, dir, "telegram", telegramManifest(t))

	set := installDirDeps(localPolicy())
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}

	definition, found := set.definitions.Get("pack.telegram", workflow.V(1))
	if !found {
		t.Fatal("the directory-loaded Telegram pack did not register")
	}
	if definition.Source != node.SourcePack {
		t.Fatalf("Source = %q, want %q", definition.Source, node.SourcePack)
	}
	if len(definition.Credentials) != 1 || definition.Credentials[0].Type != "telegramApi" {
		t.Fatalf("Credentials = %#v, want the telegramApi credential type", definition.Credentials)
	}

	var calls []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		calls = append(calls, r.URL.Path+" "+string(raw))
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"ok":true,"result":{"message_id":11}}`))
	}))
	defer server.Close()

	executor, _ := set.executors.Lookup(routing.ExecutorID)
	irDefinition, found := set.definitions.Lookup("pack.telegram", workflow.V(1))
	if !found {
		t.Fatal("the directory-loaded Telegram pack did not register")
	}
	output, err := executor.Execute(context.Background(), workflow.IRNode{
		ID: "telegram-1", Name: "Telegram", Type: "pack.telegram", TypeVersion: workflow.V(1),
		Parameters: map[string]any{
			"resource": "message", "operation": "sendMessage", "chatId": "@my_channel", "text": "hi back",
		},
		Credentials: map[string]string{"telegramApi": "cred-1"},
		Definition:  irDefinition,
	}, workflow.NodeInput{}, engine.Request{Credentials: stubCredential{baseURL: server.URL}})
	if err != nil {
		t.Fatalf("Execute() error = %v", err)
	}
	if len(calls) != 1 || !strings.HasPrefix(calls[0], "/bot123:ABC/sendMessage ") {
		t.Fatalf("calls = %#v, want the sendMessage request", calls)
	}
	if result, _ := output[0][0].JSON["result"].(map[string]any); result["message_id"] != float64(11) {
		t.Fatalf("item = %#v, want the API's answer", output[0][0].JSON)
	}
}

// A trigger pack loaded from disk registers its event table beside its
// definition, the way the embedded WAHA trigger does.
func TestLoadDirLoadsTriggerPackFromDisk(t *testing.T) {
	t.Parallel()

	manifest, err := os.ReadFile("../../packs/waha/pack-trigger-202409.json")
	if err != nil {
		t.Fatalf("ReadFile() error = %v", err)
	}
	dir := t.TempDir()
	writePack(t, dir, "waha-trigger", manifest)

	set := installDirDeps(localPolicy())
	if err := nodepack.LoadDir(set.deps(), dir); err != nil {
		t.Fatalf("LoadDir() error = %v", err)
	}
	if _, found := set.definitions.Get("pack.wahaTrigger", workflow.V(202409)); !found {
		t.Fatal("the directory-loaded trigger pack did not register its definition")
	}
	trigger, found := set.triggers.Lookup("pack.wahaTrigger", workflow.V(202409))
	if !found || trigger == nil {
		t.Fatal("the directory-loaded trigger pack did not register its event table")
	}
	if len(trigger.Ports()) == 0 {
		t.Fatal("the directory-loaded trigger registered no outputs")
	}
}

type stubCredential struct{ baseURL string }

func (stub stubCredential) ResolveCredential(context.Context, string) (engine.Credential, error) {
	return engine.Credential{
		ID: "cred-1", Name: "Bot", Type: "telegramApi",
		Fields: map[string]string{"accessToken": "123:ABC", "baseUrl": stub.baseURL},
	}, nil
}

func TestLoadDirFailureNamesThePackAndTheReason(t *testing.T) {
	t.Parallel()

	evilPack := `{"type":"kilasflow.evil","version":1,"displayName":"Evil","category":"Test",` +
		`"requestDefaults":{},"resources":[{"name":"r","operations":[{"name":"op","method":"GET","url":"/x"}]}],` +
		`"parameters":[],"generator":{"tool":"t","source":"s"}}`

	cases := map[string]struct {
		pack     string
		manifest []byte
		mutate   func(t *testing.T, dir string)
		want     []string
	}{
		"malformed manifest": {
			pack:     "broken",
			manifest: []byte(`{"type": "pack.broken", oops`),
			want:     []string{`"broken"`, nodepack.ManifestName},
		},
		"checksum mismatch": {
			pack:     "tampered",
			manifest: telegramManifestForTable(t),
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				path := filepath.Join(dir, "tampered", nodepack.ManifestName)
				raw, err := os.ReadFile(path)
				if err != nil {
					t.Fatalf("ReadFile() error = %v", err)
				}
				if err := os.WriteFile(path, append(raw, ' '), 0o644); err != nil {
					t.Fatalf("WriteFile() error = %v", err)
				}
			},
			want: []string{`"tampered"`, "digest"},
		},
		"missing checksum": {
			pack:     "unsigned",
			manifest: telegramManifestForTable(t),
			mutate: func(t *testing.T, dir string) {
				t.Helper()
				if err := os.Remove(filepath.Join(dir, "unsigned", nodepack.ChecksumName)); err != nil {
					t.Fatalf("Remove() error = %v", err)
				}
			},
			want: []string{`"unsigned"`, nodepack.ChecksumName},
		},
		"missing manifest": {
			pack:     "empty",
			manifest: nil,
			want:     []string{`"empty"`, nodepack.ManifestName},
		},
		"reserved namespace": {
			pack:     "evil",
			manifest: []byte(evilPack),
			want:     []string{`"evil"`, "kilasflow."},
		},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			dir := t.TempDir()
			if tc.manifest != nil {
				writePack(t, dir, tc.pack, tc.manifest)
			} else {
				if err := os.MkdirAll(filepath.Join(dir, tc.pack), 0o755); err != nil {
					t.Fatalf("MkdirAll() error = %v", err)
				}
			}
			if tc.mutate != nil {
				tc.mutate(t, dir)
			}
			set := installDirDeps(localPolicy())
			err := nodepack.LoadDir(set.deps(), dir)
			if err == nil {
				t.Fatalf("LoadDir() = nil, want an error naming the pack")
			}
			for _, want := range tc.want {
				if !strings.Contains(err.Error(), want) {
					t.Fatalf("LoadDir() error = %v, want it to name %q", err, want)
				}
			}
		})
	}
}

// Loading the same node type twice is refused, and the refusal names the
// pack that collided rather than silently winning or losing.
func TestLoadDirDuplicateRegistrationNamesThePack(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	writePack(t, dir, "a-telegram", telegramManifestForTable(t))
	writePack(t, dir, "b-telegram", telegramManifestForTable(t))

	set := installDirDeps(localPolicy())
	err := nodepack.LoadDir(set.deps(), dir)
	if err == nil {
		t.Fatal("LoadDir() = nil, want the duplicate registration refused")
	}
	if !strings.Contains(err.Error(), `"b-telegram"`) {
		t.Fatalf("LoadDir() error = %v, want it to name the colliding pack", err)
	}
}

func TestLoadDirAbsentOrEmptyIsSilent(t *testing.T) {
	t.Parallel()

	for name, dir := range map[string]string{
		"unset":   "",
		"blank":   "   ",
		"missing": filepath.Join(t.TempDir(), "no-such-dir"),
		"empty":   t.TempDir(),
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			set := installDirDeps(localPolicy())
			if err := nodepack.LoadDir(set.deps(), dir); err != nil {
				t.Fatalf("LoadDir(%q) error = %v, want silent success", dir, err)
			}
			if got := set.definitions.List(); len(got) != 0 {
				t.Fatalf("LoadDir(%q) registered %d definitions, want none", dir, len(got))
			}
		})
	}
}

// telegramManifestForTable reads the manifest outside a subtest, because the
// table above is built before any of them run.
func telegramManifestForTable(t *testing.T) []byte {
	t.Helper()
	manifest, err := os.ReadFile("../../packs/telegram/pack.json")
	if err != nil {
		t.Fatalf("ReadFile(pack.json) error = %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(manifest, &decoded); err != nil {
		t.Fatalf("Unmarshal() error = %v", err)
	}
	return manifest
}
