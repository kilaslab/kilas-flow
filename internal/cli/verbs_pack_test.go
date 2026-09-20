package cli

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// invalidPack is a manifest with several problems at once: an unknown field,
// and a pack that declares neither resources nor a trigger. The validator's
// whole point is to report them together rather than one per run, so the verb
// must carry them all.
const invalidPack = `{"type":"pack.demo","version":1,"displayName":"Demo","category":"Test","nonsense":true}`

// writePackDir lays out a pack directory the way an install has it: the
// manifest plus the checksum sidecar the loader insists on.
func writePackDir(t *testing.T, manifest []byte) string {
	t.Helper()

	root := t.TempDir()
	dir := filepath.Join(root, "demo")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		t.Fatalf("mkdir %s: %v", dir, err)
	}
	if err := os.WriteFile(filepath.Join(dir, "pack.json"), manifest, 0o600); err != nil {
		t.Fatalf("write pack.json: %v", err)
	}
	sum := sha256.Sum256(manifest)
	record := hex.EncodeToString(sum[:]) + "  pack.json\n"
	if err := os.WriteFile(filepath.Join(dir, "pack.sha256"), []byte(record), 0o600); err != nil {
		t.Fatalf("write pack.sha256: %v", err)
	}

	return dir
}

func TestPackValidateAcceptsAValidPackFile(t *testing.T) {
	// A pack this repository ships, so the verb is proven against a real
	// manifest rather than against one the test invented.
	manifest := filepath.Join(repoRoot(t), "packs", "telegram", "pack.json")

	code, handled, stdout, stderr := runCLI(t, Env{
		// An unroutable URL on purpose: pack validate is local, so a request
		// would fail the command instead of proving anything.
		Args:   []string{"pack", "validate", manifest, "--url", "http://127.0.0.1:1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if !handled {
		t.Fatal("`pack validate` fell through to the server path")
	}
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["ok"] != true {
		t.Fatalf("data.ok = %v, want true", data["ok"])
	}
	if data["path"] != manifest {
		t.Fatalf("data.path = %v, want %s", data["path"], manifest)
	}
	if issues, _ := data["issues"].([]any); len(issues) != 0 {
		t.Fatalf("data.issues = %v, want none", data["issues"])
	}
	if meta, _ := doc["meta"].(map[string]any); meta["operation"] != "pack validate" {
		t.Fatalf("meta.operation = %v, want the local verb's own path", meta["operation"])
	}
}

func TestPackValidateAcceptsAnInstalledPackDirectory(t *testing.T) {
	manifest, err := os.ReadFile(filepath.Join(repoRoot(t), "packs", "telegram", "pack.json"))
	if err != nil {
		t.Fatalf("read the pack manifest: %v", err)
	}

	dir := writePackDir(t, manifest)

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"pack", "validate", dir, "--url", "http://127.0.0.1:1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitOK {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitOK, stdout, stderr)
	}

	doc := envelope(t, stdout)
	data, _ := doc["data"].(map[string]any)
	if data["ok"] != true {
		t.Fatalf("data.ok = %v, want true (data=%v)", data["ok"], data)
	}
	if data["path"] != dir {
		t.Fatalf("data.path = %v, want %s", data["path"], dir)
	}
}

func TestPackValidateReportsEveryProblemAtOnce(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pack.json")
	if err := os.WriteFile(path, []byte(invalidPack), 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"pack", "validate", path, "--url", "http://127.0.0.1:1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitFailure {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitFailure, stdout, stderr)
	}

	doc := envelope(t, stdout)
	if failure := envelopeFailure(doc); failure != "invalid_pack" {
		t.Fatalf("error.code = %q, want invalid_pack", failure)
	}
	message, _ := doc["error"].(map[string]any)["message"].(string)
	if !strings.Contains(message, path) {
		t.Errorf("error.message %q does not name the pack that was checked", message)
	}

	detail, _ := doc["error"].(map[string]any)["detail"].(map[string]any)
	issues, _ := detail["issues"].([]any)
	if len(issues) < 2 {
		t.Fatalf("error.detail.issues holds %d problems, want every one: %v", len(issues), detail)
	}

	unknownFieldSeen := false
	for _, entry := range issues {
		issue, _ := entry.(map[string]any)
		if issue["severity"] != "error" {
			t.Errorf("issue %v does not carry a severity", issue)
		}
		if issue["file"] != path {
			t.Errorf("issue.file = %v, want %s", issue["file"], path)
		}
		if issue["path"] == "" || issue["message"] == "" {
			t.Errorf("issue %v is missing its address or its explanation", issue)
		}
		if strings.Contains(issue["message"].(string), "nonsense") {
			unknownFieldSeen = true
		}
	}
	if !unknownFieldSeen {
		t.Errorf("no issue names the unknown field: %v", issues)
	}
}

func TestPackValidateRefusesAMissingPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "absent")

	code, _, stdout, stderr := runCLI(t, Env{
		Args:   []string{"pack", "validate", path, "--url", "http://127.0.0.1:1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})
	if code != ExitUsage {
		t.Fatalf("exit = %d, want %d (stdout=%q stderr=%q)", code, ExitUsage, stdout, stderr)
	}

	doc := envelope(t, stdout)
	if failure := envelopeFailure(doc); failure != "usage" {
		t.Fatalf("error.code = %q, want usage", failure)
	}
	message, _ := doc["error"].(map[string]any)["message"].(string)
	if !strings.Contains(message, path) {
		t.Errorf("error.message %q does not name the path that is missing", message)
	}
}

// TestPackValidateNamesThePacksOwnProblems keeps the projection honest: the
// envelope carries the validator's own files, paths and messages, so a caller
// can open the file at the address the issue names.
func TestPackValidateNamesThePacksOwnProblems(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pack.json")
	if err := os.WriteFile(path, []byte(invalidPack), 0o600); err != nil {
		t.Fatalf("write the manifest: %v", err)
	}

	_, _, stdout, _ := runCLI(t, Env{
		Args:   []string{"pack", "validate", path, "--url", "http://127.0.0.1:1", "--json"},
		Getenv: homeEnv(t.TempDir(), nil),
	})

	doc := envelope(t, stdout)
	detail, _ := doc["error"].(map[string]any)["detail"].(map[string]any)
	encoded, err := json.Marshal(detail["issues"])
	if err != nil {
		t.Fatalf("marshal the issues: %v", err)
	}
	if !strings.Contains(string(encoded), "$.") {
		t.Fatalf("the issues carry no JSON path: %s", encoded)
	}
}
